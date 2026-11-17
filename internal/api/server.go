// Copyright (c) 2026 Vassel Goleyu. MIT License.

package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sellebeww/spillway/internal/auth"
	"github.com/sellebeww/spillway/internal/ingest"
	"github.com/sellebeww/spillway/internal/store"
)

// ServerConfig holds the HTTP API settings.
type ServerConfig struct {
	BootstrapAdminEmail string   // the first admin of an organization (needs a verified e-mail)
	DefaultOrg          string   // organization slug used when the token has no "org" claim
	CORSOrigins         []string // allowed browser origins; "*" is honoured for public endpoints only
	TrustProxy          bool     // read the client IP from X-Forwarded-For (only behind a trusted proxy)
	BotUsername         string   // Telegram bot username, for invitation links
	MaxSSEPerIP         int      // concurrent SSE connections per IP (default 5)
}

// Server is the REST, public and streaming API. Admin endpoints are disabled when Ver is nil.
type Server struct {
	St     *store.Store
	Cipher *auth.Cipher
	Ver    *auth.Verifier
	Hub    *Hub
	Cfg    ServerConfig
	Log    *slog.Logger
	Now    func() time.Time

	pubLim *ingest.Limiter
	apiLim *ingest.Limiter

	mu       sync.Mutex
	orgs     map[string]cachedOrg
	princ    map[string]*Principal
	pubCache pubCache
	sseIP    map[string]int
	sseTotal int
}

type cachedOrg struct {
	org store.Org
	at  time.Time
}

type pubCache struct {
	at    time.Time
	sites []store.PublicSite
}

// NewServer wires the API. Rate limits: public 5 req/s per IP (burst 30), authenticated 20 req/s (burst 60).
func NewServer(st *store.Store, c *auth.Cipher, v *auth.Verifier, hub *Hub, cfg ServerConfig, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if cfg.MaxSSEPerIP == 0 {
		cfg.MaxSSEPerIP = 5
	}
	return &Server{St: st, Cipher: c, Ver: v, Hub: hub, Cfg: cfg, Log: log, Now: time.Now,
		pubLim: ingest.NewLimiter(5, 30, 100000), apiLim: ingest.NewLimiter(20, 60, 100000),
		orgs: map[string]cachedOrg{}, princ: map[string]*Principal{}, sseIP: map[string]int{}}
}

// Principal is the authenticated caller. Every handler takes its organization from here, never from input.
type Principal struct {
	OrgID  uuid.UUID
	UserID uuid.UUID
	Sub    string
	Email  string
	Admin  bool
	Sites  map[uuid.UUID]bool
	exp    time.Time
}

func (p *Principal) can(site uuid.UUID) bool { return p.Admin || p.Sites[site] }

type ctxKey struct{}

func principalOf(r *http.Request) *Principal { return r.Context().Value(ctxKey{}).(*Principal) }

var errNoOrg = errors.New("no organization")

func (s *Server) orgBySlug(ctx context.Context, slug string) (store.Org, error) {
	s.mu.Lock()
	c, ok := s.orgs[slug]
	s.mu.Unlock()
	if ok && s.Now().Sub(c.at) < time.Minute {
		return c.org, nil
	}
	org, err := s.St.OrgBySlug(ctx, slug)
	if err != nil {
		return store.Org{}, err
	}
	s.mu.Lock()
	s.orgs[slug] = cachedOrg{org, s.Now()}
	s.mu.Unlock()
	return org, nil
}

// resolve maps verified claims to a Principal: it finds the organization, provisions the user on
// first login (JIT) and loads the roles from memberships. Token roles are deliberately ignored.
func (s *Server) resolve(ctx context.Context, cl *auth.Claims) (*Principal, error) {
	slug := cl.Org
	if slug == "" {
		slug = s.Cfg.DefaultOrg
	}
	if slug == "" {
		return nil, errNoOrg
	}
	key := slug + "|" + cl.Subject
	s.mu.Lock()
	if p, ok := s.princ[key]; ok && s.Now().Before(p.exp) {
		s.mu.Unlock()
		return p, nil
	}
	s.mu.Unlock()

	org, err := s.orgBySlug(ctx, slug)
	if err != nil {
		return nil, errNoOrg
	}
	o := s.St.ForOrg(org.ID)
	// Only a verified e-mail is stored: admins assign operators by e-mail, so an unverified claim
	// (anyone can type any address at some identity providers) must never identify a person.
	var email *string
	if cl.Email != "" && cl.EmailVerified {
		email = &cl.Email
	}
	u, err := o.UpsertUser(ctx, cl.Subject, email, cl.Name)
	if err != nil {
		return nil, err
	}
	mems, err := o.ListMemberships(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if len(mems) == 0 && s.Cfg.BootstrapAdminEmail != "" && cl.EmailVerified && strings.EqualFold(cl.Email, s.Cfg.BootstrapAdminEmail) {
		if n, cerr := o.CountAdmins(ctx); cerr == nil && n == 0 {
			m, merr := o.SetMembership(store.WithActor(ctx, store.Actor{UserID: &u.ID, Label: "bootstrap"}), u.ID, store.RoleAdmin, nil)
			if merr != nil {
				return nil, merr
			}
			mems = append(mems, m)
		}
	}
	p := &Principal{OrgID: org.ID, UserID: u.ID, Sub: cl.Subject, Email: cl.Email, Sites: map[uuid.UUID]bool{}, exp: s.Now().Add(20 * time.Second)}
	for _, m := range mems {
		if m.Role == store.RoleAdmin {
			p.Admin = true
		} else if m.SiteID != nil {
			p.Sites[*m.SiteID] = true
		}
	}
	s.mu.Lock()
	s.princ[key] = p
	s.mu.Unlock()
	return p, nil
}

func (s *Server) forgetPrincipals() {
	s.mu.Lock()
	clear(s.princ)
	s.mu.Unlock()
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail := func() {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			writeErr(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		}
		h := r.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || s.Ver == nil {
			fail()
			return
		}
		cl, err := s.Ver.Verify(r.Context(), tok)
		if err != nil {
			fail()
			return
		}
		p, err := s.resolve(r.Context(), cl)
		if errors.Is(err, errNoOrg) {
			writeErr(w, http.StatusForbidden, "no_organization", "the account is not linked to an organization")
			return
		}
		if err != nil {
			s.Log.Error("resolve principal", "err", err)
			writeErr(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, p)
		ctx = store.WithActor(ctx, store.Actor{UserID: &p.UserID, Label: p.Email})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAdmin lets only organization admins through.
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !principalOf(r).Admin {
			writeErr(w, http.StatusForbidden, "forbidden", "admin role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Mount registers every route. Authenticated routes live under /v1, public ones under /v1/public.
func (s *Server) Mount(r chi.Router) {
	r.Options("/v1/*", s.preflight)

	r.Group(func(r chi.Router) {
		r.Use(s.secure, s.cors(true), s.limitIP(func() *ingest.Limiter { return s.pubLim }))
		r.Get("/v1/public/sites", s.publicSites)
		r.Get("/v1/public/sites/{slug}", s.publicSite)
		r.Get("/v1/public/sites/{slug}/series", s.publicSeries)
		r.Get("/v1/public/stream", s.publicStream)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.secure, s.cors(false), noStore, s.limitIP(func() *ingest.Limiter { return s.apiLim }), s.authenticate)
		r.Get("/v1/me", s.me)
		r.Get("/v1/sites", s.listSites)
		r.Get("/v1/sites/{id}", s.getSite)
		r.Get("/v1/sites/{id}/devices", s.listDevices)
		r.Post("/v1/sites/{id}/devices", s.createDevice)
		r.Get("/v1/sites/{id}/rules", s.listRules)
		r.Post("/v1/sites/{id}/rules", s.createRule)
		r.Get("/v1/sites/{id}/alerts", s.listAlerts)
		r.Get("/v1/sites/{id}/channels", s.listChannels)
		r.Post("/v1/sites/{id}/subscription-links", s.createLink)
		r.Post("/v1/sites/{id}/subscription-links/revoke", s.revokeLinks)
		r.Get("/v1/devices/{id}", s.getDevice)
		r.Post("/v1/devices/{id}/rotate-secret", s.rotateSecret)
		r.Get("/v1/devices/{id}/health", s.deviceHealth)
		r.Get("/v1/devices/{id}/calibrations", s.listCalibrations)
		r.Post("/v1/devices/{id}/calibrations", s.addCalibration)
		r.Get("/v1/rules/{id}", s.getRule)
		r.Put("/v1/rules/{id}", s.updateRule)
		r.With(requireAdmin).Post("/v1/sites", s.createSite)
		r.With(requireAdmin).Put("/v1/sites/{id}", s.updateSite)
		r.With(requireAdmin).Post("/v1/sites/{id}/operators", s.addOperator)
		r.With(requireAdmin).Get("/v1/audit", s.listAudit)
	})
}
