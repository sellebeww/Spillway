// Copyright (c) 2026 Vassel Goleyu. MIT License.

package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/sellebeww/spillway/internal/ingest/protocol"
)

// HTTPHandler serves POST /v1/ingest through the same pipeline as MQTT. It answers only after
// the reading is committed (200 stored/duplicate), so a 2xx is a durability guarantee; any
// other answer means the device should retry, which is safe because ingest is idempotent.
//
//	200 {"status":"stored"|"duplicate"}   400 bad payload   401 unknown device or bad signature
//	422 too old / device not calibrated   429 rate limited   503 busy or database trouble (Retry-After)
func (p *Pipeline) HTTPHandler(lim *Limiter, maxBody int64, wait time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				p.m.Received.WithLabelValues("http").Inc()
				p.m.Rejected.WithLabelValues(ReasonSchema).Inc()
				reply(w, http.StatusRequestEntityTooLarge, "payload too large")
				return
			}
			reply(w, http.StatusBadRequest, "unreadable body")
			return
		}
		pl, perr := protocol.Parse(body)
		if perr != nil {
			p.m.Received.WithLabelValues("http").Inc()
			p.m.Rejected.WithLabelValues(ReasonSchema).Inc()
			reply(w, http.StatusBadRequest, "invalid payload")
			return
		}
		if lim != nil {
			if ok, after := lim.Allow(pl.Device, p.now()); !ok {
				p.m.Received.WithLabelValues("http").Inc()
				p.m.Rejected.WithLabelValues(ReasonRateLimit).Inc()
				w.Header().Set("Retry-After", strconv.Itoa(int(after.Seconds())+1))
				reply(w, http.StatusTooManyRequests, "rate limited")
				return
			}
		}
		done := make(chan Outcome, 1)
		msg := &Message{Parsed: &pl, Source: "http", Done: func(o Outcome) { done <- o }}
		if err := p.Submit(r.Context(), msg, false); err != nil {
			w.Header().Set("Retry-After", "1")
			reply(w, http.StatusServiceUnavailable, "busy, retry later")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), wait)
		defer cancel()
		select {
		case o := <-done:
			writeOutcome(w, o)
		case <-ctx.Done():
			// The message may still commit; the retry is deduplicated.
			w.Header().Set("Retry-After", "5")
			reply(w, http.StatusServiceUnavailable, "timed out, retry later")
		}
	})
}

func writeOutcome(w http.ResponseWriter, o Outcome) {
	switch o.Kind {
	case Stored:
		reply(w, http.StatusOK, "stored")
	case Duplicate:
		reply(w, http.StatusOK, "duplicate")
	case Rejected:
		switch o.Reason {
		case ReasonSig, ReasonUnknownDevice: // identical answers: no device-existence oracle
			reply(w, http.StatusUnauthorized, "unauthorized")
		case ReasonSchema, ReasonTopicMismatch:
			reply(w, http.StatusBadRequest, "invalid payload")
		default:
			reply(w, http.StatusUnprocessableEntity, o.Reason)
		}
	default:
		w.Header().Set("Retry-After", "5")
		reply(w, http.StatusServiceUnavailable, "temporarily unavailable")
	}
}

func reply(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	key := "error"
	if code == http.StatusOK {
		key = "status"
	}
	_ = json.NewEncoder(w).Encode(map[string]string{key: msg})
}
