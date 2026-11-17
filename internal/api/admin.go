// Copyright (c) 2026 Vassel Goleyu. MIT License.

package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/quality"
	"github.com/sellebeww/spillway/internal/store"
)

// Every handler below takes its organization from the authenticated Principal. Resources are
// loaded through the organization-scoped store first, so another organization's IDs are plain
// "not found"; a resource of a site the caller may not touch is also "not found" (no enumeration).

// siteOf loads the site of the {id} path parameter for the caller.
func (s *Server) siteOf(w http.ResponseWriter, r *http.Request) (*store.OrgStore, store.Site, bool) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return nil, store.Site{}, false
	}
	p := principalOf(r)
	o := s.St.ForOrg(p.OrgID)
	site, err := o.GetSite(r.Context(), id)
	if err != nil || !p.can(site.ID) {
		if err == nil {
			err = store.ErrNotFound
		}
		s.storeErr(w, err)
		return nil, store.Site{}, false
	}
	return o, site, true
}

func (s *Server) deviceOf(w http.ResponseWriter, r *http.Request) (*store.OrgStore, store.Device, bool) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return nil, store.Device{}, false
	}
	p := principalOf(r)
	o := s.St.ForOrg(p.OrgID)
	dev, err := o.GetDevice(r.Context(), id)
	if err != nil || !p.can(dev.SiteID) {
		if err == nil {
			err = store.ErrNotFound
		}
		s.storeErr(w, err)
		return nil, store.Device{}, false
	}
	return o, dev, true
}

func (s *Server) ruleOf(w http.ResponseWriter, r *http.Request) (*store.OrgStore, store.AlertRule, bool) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return nil, store.AlertRule{}, false
	}
	p := principalOf(r)
	o := s.St.ForOrg(p.OrgID)
	rule, err := o.GetRule(r.Context(), id)
	if err != nil || !p.can(rule.SiteID) {
		if err == nil {
			err = store.ErrNotFound
		}
		s.storeErr(w, err)
		return nil, store.AlertRule{}, false
	}
	return o, rule, true
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	ids := []uuid.UUID{}
	for id := range p.Sites {
		ids = append(ids, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": p.UserID, "email": p.Email, "admin": p.Admin, "site_ids": ids})
}

// ---- sites ----

type siteReq struct {
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Lat      *float64 `json:"lat"`
	Lon      *float64 `json:"lon"`
	TZ       string   `json:"tz"`
	IsPublic bool     `json:"is_public"`
}

func (q siteReq) input() (store.SiteInput, bool) {
	if q.Lat == nil || q.Lon == nil || math.IsNaN(*q.Lat) || math.IsNaN(*q.Lon) {
		return store.SiteInput{}, false
	}
	return store.SiteInput{Slug: q.Slug, Name: q.Name, Lat: *q.Lat, Lon: *q.Lon, TZ: q.TZ, IsPublic: q.IsPublic}, true
}

func (s *Server) listSites(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	all, err := s.St.ForOrg(p.OrgID).ListSites(r.Context())
	if err != nil {
		s.storeErr(w, err)
		return
	}
	var vis []store.Site
	for _, st := range all {
		if p.can(st.ID) {
			vis = append(vis, st)
		}
	}
	limit, offset := page(r)
	writeJSON(w, http.StatusOK, paged(vis, limit, offset))
}

func (s *Server) createSite(w http.ResponseWriter, r *http.Request) {
	var q siteReq
	if !decodeJSON(w, r, &q) {
		return
	}
	in, ok := q.input()
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid_input", "lat and lon are required")
		return
	}
	site, err := s.St.ForOrg(principalOf(r).OrgID).CreateSite(r.Context(), in)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, site)
}

func (s *Server) getSite(w http.ResponseWriter, r *http.Request) {
	if _, site, ok := s.siteOf(w, r); ok {
		writeJSON(w, http.StatusOK, site)
	}
}

func (s *Server) updateSite(w http.ResponseWriter, r *http.Request) {
	o, site, ok := s.siteOf(w, r)
	if !ok {
		return
	}
	var q siteReq
	if !decodeJSON(w, r, &q) {
		return
	}
	in, vok := q.input()
	if !vok {
		writeErr(w, http.StatusBadRequest, "invalid_input", "lat and lon are required")
		return
	}
	upd, err := o.UpdateSite(r.Context(), site.ID, in)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, upd)
}

// ---- devices ----

type deviceReq struct {
	DeviceKey         string  `json:"device_key"`
	Name              string  `json:"name"`
	ExpectedIntervalS int     `json:"expected_interval_s"`
	LowBattV          float64 `json:"low_batt_v"`
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	o, site, ok := s.siteOf(w, r)
	if !ok {
		return
	}
	devs, err := o.ListDevices(r.Context(), &site.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	limit, offset := page(r)
	writeJSON(w, http.StatusOK, paged(devs, limit, offset))
}

// createDevice registers a device. The secret appears in this response and nowhere else.
func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	o, site, ok := s.siteOf(w, r)
	if !ok {
		return
	}
	var q deviceReq
	if !decodeJSON(w, r, &q) {
		return
	}
	dev, secret, err := o.RegisterDevice(r.Context(), s.Cipher, store.DeviceInput{SiteID: site.ID, Key: q.DeviceKey, Name: q.Name,
		ExpectedIntervalS: q.ExpectedIntervalS, LowBattV: q.LowBattV})
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"device": dev, "secret": secret})
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	if _, dev, ok := s.deviceOf(w, r); ok {
		writeJSON(w, http.StatusOK, dev)
	}
}

func (s *Server) rotateSecret(w http.ResponseWriter, r *http.Request) {
	o, dev, ok := s.deviceOf(w, r)
	if !ok {
		return
	}
	secret, err := o.RotateDeviceSecret(r.Context(), s.Cipher, dev.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret})
}

func (s *Server) deviceHealth(w http.ResponseWriter, r *http.Request) {
	o, dev, ok := s.deviceOf(w, r)
	if !ok {
		return
	}
	now := s.Now()
	interval := time.Duration(dev.ExpectedIntervalS) * time.Second
	hour, err := o.ReadingsRange(r.Context(), dev.ID, now.Add(-time.Hour), now.Add(time.Minute), 10000)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	scores := make([]float64, len(hour))
	for i, rd := range hour {
		scores[i] = rd.Score
	}
	day, err := o.ReadingsRange(r.Context(), dev.ID, now.Add(-24*time.Hour), now.Add(time.Minute), 10000)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	var samples []quality.BattSample
	for _, rd := range day {
		if rd.BattV != nil {
			samples = append(samples, quality.BattSample{TS: rd.TS, V: *rd.BattV})
		}
	}
	var slope *float64
	if v, ok := quality.BatterySlopePerDay(samples, now); ok {
		slope = &v
	}
	var lastSeen time.Time
	if dev.LastSeenAt != nil {
		lastSeen = *dev.LastSeenAt
	}
	// The newest reading of the last 24 h (any quality), so operators can preview a calibration against real data.
	var last map[string]any
	for _, rd := range day {
		if last == nil || rd.TS.After(last["ts"].(time.Time)) {
			last = map[string]any{"ts": rd.TS.UTC(), "raw_mm": rd.RawMM, "level_cm": rd.LevelCm, "batt_v": rd.BattV, "quality_score": rd.Score}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": quality.Status(now, lastSeen, interval), "last_seen_at": dev.LastSeenAt,
		"stale": quality.IsStale(now, lastSeen, interval), "health_score": quality.HealthScore(scores, interval, time.Hour),
		"readings_last_hour": len(hour), "battery_slope_v_per_day": slope, "last_reading": last, "evaluated_at": now,
	})
}

// ---- calibrations ----

type calReq struct {
	OffsetCm        *float64   `json:"offset_cm"`
	MinValidMM      *int       `json:"min_valid_mm"`
	MaxValidMM      *int       `json:"max_valid_mm"`
	ValidFrom       *time.Time `json:"valid_from"`
	Note            string     `json:"note"`
	ConfirmBackdate bool       `json:"confirm_backdate"`
}

type calJSON struct {
	Version    int       `json:"version"`
	OffsetCm   float64   `json:"offset_cm"`
	MinValidMM int       `json:"min_valid_mm"`
	MaxValidMM int       `json:"max_valid_mm"`
	ValidFrom  time.Time `json:"valid_from"`
	Note       string    `json:"note"`
}

func toCalJSON(c domain.Calibration) calJSON {
	return calJSON{c.Version, c.OffsetCm, c.MinValidMM, c.MaxValidMM, c.ValidFrom.UTC(), c.Note}
}

func (s *Server) listCalibrations(w http.ResponseWriter, r *http.Request) {
	o, dev, ok := s.deviceOf(w, r)
	if !ok {
		return
	}
	cals, err := o.ListCalibrations(r.Context(), dev.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	out := make([]calJSON, len(cals))
	for i, c := range cals {
		out[i] = toCalJSON(c)
	}
	limit, offset := page(r)
	writeJSON(w, http.StatusOK, paged(out, limit, offset))
}

func (s *Server) addCalibration(w http.ResponseWriter, r *http.Request) {
	o, dev, ok := s.deviceOf(w, r)
	if !ok {
		return
	}
	var q calReq
	if !decodeJSON(w, r, &q) {
		return
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, "invalid_input", msg) }
	switch {
	case q.OffsetCm == nil || math.IsNaN(*q.OffsetCm) || math.Abs(*q.OffsetCm) > 100000:
		bad("offset_cm is required and must be within +/-100000")
		return
	case q.ValidFrom == nil || q.ValidFrom.After(s.Now().AddDate(1, 0, 0)):
		bad("valid_from is required and must not be more than a year ahead")
		return
	case (q.MinValidMM == nil) != (q.MaxValidMM == nil):
		bad("give both min_valid_mm and max_valid_mm, or neither")
		return
	case len(q.Note) > 500:
		bad("note is limited to 500 characters")
		return
	}
	c := domain.Calibration{OffsetCm: *q.OffsetCm, ValidFrom: q.ValidFrom.UTC(), Note: q.Note}
	if q.MinValidMM != nil {
		c.MinValidMM, c.MaxValidMM = *q.MinValidMM, *q.MaxValidMM
	}
	added, err := o.AddCalibration(r.Context(), dev.ID, c, q.ConfirmBackdate)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toCalJSON(added))
}

// ---- rules ----

type tierReq struct {
	Value    float64 `json:"value"`
	Severity int     `json:"severity"`
}

type ruleReq struct {
	Type         string    `json:"type"`
	Tiers        []tierReq `json:"tiers"`
	HysteresisCm *float64  `json:"hysteresis_cm"`
	CooldownS    *int      `json:"cooldown_s"`
	MinQuality   *float64  `json:"min_quality"`
	Enabled      *bool     `json:"enabled"`
}

type ruleJSON struct {
	ID           uuid.UUID `json:"id"`
	SiteID       uuid.UUID `json:"site_id"`
	Type         string    `json:"type"`
	Tiers        []tierReq `json:"tiers"`
	HysteresisCm float64   `json:"hysteresis_cm"`
	CooldownS    int       `json:"cooldown_s"`
	MinQuality   float64   `json:"min_quality"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toRuleJSON(r store.AlertRule) ruleJSON {
	tiers := make([]tierReq, len(r.Tiers))
	for i, t := range r.Tiers {
		tiers[i] = tierReq{t.Value, int(t.Severity)}
	}
	return ruleJSON{r.ID, r.SiteID, string(r.Kind), tiers, r.HysteresisCm, int(r.Cooldown / time.Second), r.MinQuality, r.Enabled, r.CreatedAt, r.UpdatedAt}
}

// validate applies the API-level limits on top of the domain validation done by the store.
func (q ruleReq) input(siteID uuid.UUID) (store.RuleInput, error) {
	kind := domain.RuleKind(q.Type)
	if kind != domain.KindThreshold && kind != domain.KindRateOfRise {
		return store.RuleInput{}, errors.New("type must be THRESHOLD or RATE_OF_RISE")
	}
	if len(q.Tiers) < 1 || len(q.Tiers) > 3 {
		return store.RuleInput{}, errors.New("give 1 to 3 tiers")
	}
	in := store.RuleInput{SiteID: siteID, Kind: kind, Enabled: true}
	for _, t := range q.Tiers {
		if math.IsNaN(t.Value) || math.IsInf(t.Value, 0) || t.Value < 0 || t.Value > 10000 {
			return store.RuleInput{}, errors.New("tier value must be between 0 and 10000")
		}
		in.Tiers = append(in.Tiers, domain.Tier{Value: t.Value, Severity: domain.Severity(t.Severity)})
	}
	if q.HysteresisCm != nil {
		if kind == domain.KindRateOfRise {
			return store.RuleInput{}, errors.New("hysteresis_cm applies to THRESHOLD rules only (RATE_OF_RISE uses a fixed 20 percent)")
		}
		if math.IsNaN(*q.HysteresisCm) || *q.HysteresisCm <= 0 || *q.HysteresisCm > 1000 {
			return store.RuleInput{}, errors.New("hysteresis_cm must be above 0 and at most 1000")
		}
		in.HysteresisCm = *q.HysteresisCm
	}
	if q.CooldownS != nil {
		if *q.CooldownS < 60 || *q.CooldownS > 86400 {
			return store.RuleInput{}, errors.New("cooldown_s must be between 60 and 86400")
		}
		in.Cooldown = time.Duration(*q.CooldownS) * time.Second
	}
	if q.MinQuality != nil {
		if math.IsNaN(*q.MinQuality) || *q.MinQuality < 0.01 || *q.MinQuality > 1 {
			return store.RuleInput{}, errors.New("min_quality must be between 0.01 and 1")
		}
		in.MinQuality = *q.MinQuality
	}
	if q.Enabled != nil {
		in.Enabled = *q.Enabled
	}
	return in, nil
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	o, site, ok := s.siteOf(w, r)
	if !ok {
		return
	}
	rs, err := o.ListRules(r.Context(), &site.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	out := make([]ruleJSON, len(rs))
	for i, x := range rs {
		out[i] = toRuleJSON(x)
	}
	limit, offset := page(r)
	writeJSON(w, http.StatusOK, paged(out, limit, offset))
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	o, site, ok := s.siteOf(w, r)
	if !ok {
		return
	}
	var q ruleReq
	if !decodeJSON(w, r, &q) {
		return
	}
	in, err := q.input(site.ID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	rule, err := o.CreateRule(r.Context(), in)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toRuleJSON(rule))
}

func (s *Server) getRule(w http.ResponseWriter, r *http.Request) {
	if _, rule, ok := s.ruleOf(w, r); ok {
		writeJSON(w, http.StatusOK, toRuleJSON(rule))
	}
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	o, rule, ok := s.ruleOf(w, r)
	if !ok {
		return
	}
	var q ruleReq
	if !decodeJSON(w, r, &q) {
		return
	}
	in, err := q.input(rule.SiteID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	upd, err := o.UpdateRule(r.Context(), rule.ID, in)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toRuleJSON(upd))
}

// ---- alerts, channels, telegram links, operators, audit ----

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	o, site, ok := s.siteOf(w, r)
	if !ok {
		return
	}
	limit, offset := page(r)
	evs, err := o.ListEventsBySite(r.Context(), site.ID, limit+offset)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, paged(evs, limit, offset))
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	o, site, ok := s.siteOf(w, r)
	if !ok {
		return
	}
	chs, err := o.ListSubscribers(r.Context(), site.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	type chJSON struct {
		ID        uuid.UUID `json:"id"`
		Kind      string    `json:"kind"`
		ChatHint  string    `json:"chat_hint"` // masked: operators do not need the full chat id
		CreatedAt time.Time `json:"created_at"`
	}
	out := make([]chJSON, len(chs))
	for i, c := range chs {
		hint := c.ChatID
		if len(hint) > 4 {
			hint = "..." + hint[len(hint)-4:]
		}
		out[i] = chJSON{c.ID, c.Kind, hint, c.CreatedAt}
	}
	limit, offset := page(r)
	writeJSON(w, http.StatusOK, paged(out, limit, offset))
}

// createLink issues a single-use Telegram invitation (24 h). The token is shown once.
func (s *Server) createLink(w http.ResponseWriter, r *http.Request) {
	o, site, ok := s.siteOf(w, r)
	if !ok {
		return
	}
	if s.Cfg.BotUsername == "" {
		writeErr(w, http.StatusConflict, "telegram_not_configured", "the Telegram bot is not configured")
		return
	}
	tok, exp, err := o.CreateSubscriptionToken(r.Context(), site.ID, 24*time.Hour, s.Now())
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"url": fmt.Sprintf("https://t.me/%s?start=%s", s.Cfg.BotUsername, tok), "expires_at": exp})
}

func (s *Server) revokeLinks(w http.ResponseWriter, r *http.Request) {
	o, site, ok := s.siteOf(w, r)
	if !ok {
		return
	}
	n, err := o.RevokeSubscriptionTokens(r.Context(), site.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}

func (s *Server) addOperator(w http.ResponseWriter, r *http.Request) {
	o, site, ok := s.siteOf(w, r)
	if !ok {
		return
	}
	var q struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	if !strings.Contains(q.Email, "@") || len(q.Email) > 254 {
		writeErr(w, http.StatusBadRequest, "invalid_input", "a valid e-mail address is required")
		return
	}
	u, err := o.UserByEmail(r.Context(), q.Email)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "user_not_found", "no such user in this organization; the user must sign in once first")
		return
	}
	if err != nil {
		s.storeErr(w, err)
		return
	}
	if _, err := o.SetMembership(r.Context(), u.ID, store.RoleOperator, &site.ID); err != nil {
		s.storeErr(w, err)
		return
	}
	s.forgetPrincipals()
	writeJSON(w, http.StatusCreated, map[string]any{"user_id": u.ID, "site_id": site.ID, "role": store.RoleOperator})
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, offset := page(r)
	entries, err := s.St.ForOrg(principalOf(r).OrgID).ListAudit(r.Context(), min(limit+offset, 1000))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, paged(slices.Clone(entries), limit, offset))
}
