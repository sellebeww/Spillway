// Copyright (c) 2026 Vassel Goleyu. MIT License.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sellebeww/spillway/internal/calibration"
	"github.com/sellebeww/spillway/internal/ingest"
	"github.com/sellebeww/spillway/internal/store"
)

const maxJSONBody = 64 << 10

// errorBody is the one error shape of the whole API.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	var b errorBody
	b.Error.Code, b.Error.Message = code, msg
	writeJSON(w, status, b)
}

// decodeJSON reads a strict JSON body: size limited, unknown fields refused, one object only.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "request body is too large")
			return false
		}
		writeErr(w, http.StatusBadRequest, "invalid_json", "body must be a single JSON object with known fields only")
		return false
	}
	if dec.More() {
		writeErr(w, http.StatusBadRequest, "invalid_json", "body must be a single JSON object")
		return false
	}
	return true
}

// storeErr maps store errors to API errors; anything unexpected is a 500 without details.
func (s *Server) storeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, calibration.ErrNoCalibration):
		writeErr(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, calibration.ErrBackdated):
		writeErr(w, http.StatusConflict, "backdated", "valid_from is before the active calibration; set confirm_backdate to proceed")
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", "the resource conflicts with an existing one")
	case errors.Is(err, store.ErrInvalid):
		writeErr(w, http.StatusBadRequest, "invalid_input", cleanInvalid(err))
	default:
		s.Log.Error("request failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func cleanInvalid(err error) string {
	msg := strings.TrimPrefix(err.Error(), store.ErrInvalid.Error())
	msg = strings.TrimLeft(msg, ": ")
	if msg == "" || len(msg) > 200 {
		return "invalid input"
	}
	return msg
}

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return uuid.Nil, false
	}
	return id, true
}

// page reads limit and offset (default 50, max 200).
func page(r *http.Request) (limit, offset int) {
	limit, offset = 50, 0
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = min(v, 200)
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = min(v, 100000)
	}
	return
}

func paged[T any](items []T, limit, offset int) map[string]any {
	if offset > len(items) {
		offset = len(items)
	}
	end := min(offset+limit, len(items))
	out := items[offset:end]
	if out == nil {
		out = []T{}
	}
	return map[string]any{"items": out, "limit": limit, "offset": offset, "total": len(items)}
}

func (s *Server) clientIP(r *http.Request) string {
	if s.Cfg.TrustProxy {
		// With one trusted reverse proxy (Caddy) the last X-Forwarded-For entry is the address it saw.
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// cors allows the configured origins only (never credentials). "*" in the list applies to public
// endpoints only: authenticated endpoints need their origin listed explicitly.
func (s *Server) cors(public bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.applyCORS(w, r, public)
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) applyCORS(w http.ResponseWriter, r *http.Request, public bool) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	w.Header().Add("Vary", "Origin")
	for _, o := range s.Cfg.CORSOrigins {
		if o == origin || (o == "*" && public) {
			if o == "*" {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			w.Header().Set("Access-Control-Expose-Headers", "Retry-After")
			return
		}
	}
}

func (s *Server) preflight(w http.ResponseWriter, r *http.Request) {
	s.applyCORS(w, r, strings.HasPrefix(r.URL.Path, "/v1/public/"))
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID")
		w.Header().Set("Access-Control-Max-Age", "600")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) limitIP(get func() *ingest.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ok, after := get().Allow(s.clientIP(r), s.Now()); !ok {
				w.Header().Set("Retry-After", fmt.Sprint(int(after.Seconds())+1))
				writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
