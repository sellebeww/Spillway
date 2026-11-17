// Copyright (c) 2026 Vassel Goleyu. MIT License.

package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/notify"
	"github.com/sellebeww/spillway/internal/quality"
	"github.com/sellebeww/spillway/internal/store"
)

const staleLabel = "DATA TERTUNDA"

// publicList returns the public sites, cached for 5 seconds so a crowd of anonymous visitors
// costs the database one query per interval.
func (s *Server) publicList(ctx context.Context) ([]store.PublicSite, error) {
	s.mu.Lock()
	if s.Now().Sub(s.pubCache.at) < 5*time.Second && s.pubCache.sites != nil {
		out := s.pubCache.sites
		s.mu.Unlock()
		return out, nil
	}
	s.mu.Unlock()
	sites, err := s.St.PublicSites(ctx, "")
	if err != nil {
		return nil, err
	}
	if sites == nil {
		sites = []store.PublicSite{}
	}
	s.mu.Lock()
	s.pubCache = pubCache{at: s.Now(), sites: sites}
	s.mu.Unlock()
	return sites, nil
}

func (s *Server) isPublic(ctx context.Context, slug string) bool {
	_, ok := s.findPublic(ctx, slug)
	return ok
}

func (s *Server) findPublic(ctx context.Context, slug string) (store.PublicSite, bool) {
	sites, err := s.publicList(ctx)
	if err != nil {
		return store.PublicSite{}, false
	}
	for _, p := range sites {
		if p.Slug == slug {
			return p, true
		}
	}
	return store.PublicSite{}, false
}

// PublicSlugs maps public site ids to slugs (the hub uses it to decide what may leave the process).
func (s *Server) PublicSlugs(ctx context.Context) (map[uuid.UUID]string, error) {
	sites, err := s.St.PublicSites(ctx, "")
	if err != nil {
		return nil, err
	}
	m := make(map[uuid.UUID]string, len(sites))
	for _, p := range sites {
		m[p.SiteID] = p.Slug
	}
	return m, nil
}

type devState struct {
	Level   *float64
	Updated *time.Time
	Status  string
	Stale   bool
}

func deviceState(d store.PublicDevice, now time.Time) devState {
	iv := time.Duration(d.IntervalS) * time.Second
	if d.LastTS == nil || d.LastRecv == nil {
		return devState{Status: string(quality.Status(now, time.Time{}, iv)), Stale: true}
	}
	lvl := d.LevelCm
	return devState{Level: &lvl, Updated: d.LastTS, Status: string(quality.Status(now, *d.LastRecv, iv)), Stale: quality.IsStaleReading(now, *d.LastTS, *d.LastRecv, iv)}
}

// siteState summarises a site by its freshest device.
func siteState(p store.PublicSite, now time.Time) devState {
	best := devState{Status: string(domain.StatusOffline), Stale: true}
	var bestTS time.Time
	for _, d := range p.Devices {
		st := deviceState(d, now)
		if st.Updated != nil && st.Updated.After(bestTS) {
			best, bestTS = st, *st.Updated
		}
	}
	return best
}

func labelOf(stale bool) *string {
	if !stale {
		return nil
	}
	l := staleLabel
	return &l
}

// publicSites is GET /v1/public/sites: a GeoJSON FeatureCollection of the public sites.
func (s *Server) publicSites(w http.ResponseWriter, r *http.Request) {
	sites, err := s.publicList(r.Context())
	if err != nil {
		s.storeErr(w, err)
		return
	}
	now := s.Now()
	feats := make([]map[string]any, 0, len(sites))
	for _, p := range sites {
		st := siteState(p, now)
		feats = append(feats, map[string]any{
			"type":     "Feature",
			"geometry": map[string]any{"type": "Point", "coordinates": []float64{p.Lon, p.Lat}},
			"properties": map[string]any{"slug": p.Slug, "name": p.Name, "tz": p.TZ, "level_cm": st.Level, "status": st.Status,
				"updated_at": st.Updated, "stale": st.Stale, "stale_label": labelOf(st.Stale), "notice": notify.SafetyLine},
		})
	}
	w.Header().Set("Cache-Control", "public, max-age=10")
	writeJSON(w, http.StatusOK, map[string]any{"type": "FeatureCollection", "features": feats})
}

// publicSite is GET /v1/public/sites/{slug}. A private or unknown slug gives the same 404.
func (s *Server) publicSite(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	p, ok := s.findPublic(r.Context(), slug)
	if !slugRe.MatchString(slug) || !ok {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	now := s.Now()
	st := siteState(p, now)
	devs := make([]map[string]any, 0, len(p.Devices))
	for _, d := range p.Devices {
		ds := deviceState(d, now)
		devs = append(devs, map[string]any{"id": d.ID, "name": d.Name, "level_cm": ds.Level, "status": ds.Status,
			"updated_at": ds.Updated, "stale": ds.Stale, "stale_label": labelOf(ds.Stale)})
	}
	severity, thresholds, alerts, err := s.publicAlertView(r.Context(), p, now)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=10")
	writeJSON(w, http.StatusOK, map[string]any{"slug": p.Slug, "name": p.Name, "lat": p.Lat, "lon": p.Lon, "tz": p.TZ,
		"level_cm": st.Level, "status": st.Status, "updated_at": st.Updated, "stale": st.Stale, "stale_label": labelOf(st.Stale),
		"severity": severity, "thresholds": thresholds, "alerts": alerts,
		"devices": devs, "notice": notify.SafetyLine})
}

// publicAlertView is what the public page may show about alerting: the highest open severity (0 = none),
// the level thresholds of enabled THRESHOLD rules and the alerts of the last 30 days. No rule, device or
// organization ids leave the process.
func (s *Server) publicAlertView(ctx context.Context, p store.PublicSite, now time.Time) (int, []map[string]any, []map[string]any, error) {
	o := s.St.ForOrg(p.OrgID)
	rules, err := o.ListRules(ctx, &p.SiteID)
	if err != nil {
		return 0, nil, nil, err
	}
	thresholds := []map[string]any{}
	for _, r := range rules {
		if !r.Enabled || r.Kind != domain.KindThreshold {
			continue
		}
		for _, t := range r.Tiers {
			thresholds = append(thresholds, map[string]any{"level_cm": t.Value, "severity": int(t.Severity)})
		}
	}
	events, err := o.ListEventsBySite(ctx, p.SiteID, 50)
	if err != nil {
		return 0, nil, nil, err
	}
	severity := 0
	alerts := []map[string]any{}
	for _, e := range events {
		if e.State == "OPEN" && int(e.Severity) > severity {
			severity = int(e.Severity)
		}
		if e.OpenedAt.After(now.Add(-30*24*time.Hour)) || e.ClosedAt == nil {
			alerts = append(alerts, map[string]any{"severity": int(e.Severity), "state": e.State, "opened_at": e.OpenedAt.UTC(),
				"closed_at": e.ClosedAt, "peak_level_cm": e.PeakLevel})
		}
	}
	return severity, thresholds, alerts, nil
}

const maxSeriesPoints = 5000

// publicSeries is GET /v1/public/sites/{slug}/series. The resolution is chosen from the range
// (res=auto) and the number of points is capped; only trusted readings (score >= 0.5) are shown.
func (s *Server) publicSeries(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	p, ok := s.findPublic(r.Context(), slug)
	if !slugRe.MatchString(slug) || !ok {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	q := r.URL.Query()
	now := s.Now()
	to, from := now, now.Add(-24*time.Hour)
	var err error
	if v := q.Get("to"); v != "" {
		if to, err = time.Parse(time.RFC3339, v); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_input", "to must be RFC 3339")
			return
		}
	}
	if v := q.Get("from"); v != "" {
		if from, err = time.Parse(time.RFC3339, v); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_input", "from must be RFC 3339")
			return
		}
	}
	if !to.After(from) || to.Sub(from) > 400*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "invalid_input", "the range must be positive and at most 400 days")
		return
	}
	var dev *store.PublicDevice
	if v := q.Get("device"); v == "" && len(p.Devices) > 0 {
		dev = &p.Devices[0]
	} else if id, perr := uuid.Parse(v); perr == nil {
		for i := range p.Devices {
			if p.Devices[i].ID == id {
				dev = &p.Devices[i]
			}
		}
	}
	if dev == nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	res := store.Resolution(q.Get("res"))
	switch res {
	case "", "auto":
		res = store.ChooseResolution(to.Sub(from))
	case store.ResRaw, store.Res1m, store.Res1h, store.Res1d:
	default:
		writeErr(w, http.StatusBadRequest, "invalid_input", "res must be auto, raw, 1m, 1h or 1d")
		return
	}
	o := s.St.ForOrg(p.OrgID)
	var points []map[string]any
	truncated := false
	if res == store.ResRaw {
		rows, err := o.ReadingsRange(r.Context(), dev.ID, from, to, maxSeriesPoints)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		truncated = len(rows) == maxSeriesPoints
		for _, x := range rows {
			if x.Score >= 0.5 {
				points = append(points, map[string]any{"ts": x.TS.UTC(), "level_cm": x.LevelCm, "late": x.Late})
			}
		}
	} else {
		rows, err := o.Aggregates(r.Context(), dev.ID, res, from, to, maxSeriesPoints)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		truncated = len(rows) == maxSeriesPoints
		for _, x := range rows {
			points = append(points, map[string]any{"ts": x.Bucket.UTC(), "min": x.Min, "avg": x.Avg, "max": x.Max, "n": x.N})
		}
	}
	if points == nil {
		points = []map[string]any{}
	}
	w.Header().Set("Cache-Control", "public, max-age=15")
	writeJSON(w, http.StatusOK, map[string]any{"site": slug, "device_id": dev.ID, "res": res, "from": from.UTC(), "to": to.UTC(),
		"truncated": truncated, "points": points, "notice": notify.SafetyLine})
}
