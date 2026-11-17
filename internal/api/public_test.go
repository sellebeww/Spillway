package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/ingest"
	"github.com/sellebeww/spillway/internal/store"
)

// Fail-visible: the age of the measurement counts, not only the arrival. A device that just
// delivered a 10 minute old (buffered) reading is online but its level is DATA TERTUNDA.
func TestDeviceStateIsStaleWhenTheMeasurementIsOld(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	old, fresh := now.Add(-10*time.Minute), now.Add(-time.Minute)
	arrived := now
	d := store.PublicDevice{IntervalS: 60, LastTS: &old, LastRecv: &arrived, LevelCm: 50}
	if st := deviceState(d, now); !st.Stale || st.Status != string(domain.StatusOnline) {
		t.Fatalf("10 min old measurement just arrived: %+v, want stale but ONLINE", st)
	}
	d.LastTS = &fresh
	if st := deviceState(d, now); st.Stale {
		t.Fatalf("1 min old measurement must not be stale: %+v", st)
	}
}

type pubFx struct {
	*fx
	p1, p2, p3 store.Site // public: fresh, stale, no data
	d1, d2     store.Device
}

func (f *fx) publicSite(g *org, slug string) (store.Site, store.Device) {
	ctx := context.Background()
	s, err := g.o.CreateSite(ctx, store.SiteInput{Slug: slug, Name: "Pub " + slug, Lat: -7.5, Lon: 110.25, IsPublic: true})
	if err != nil {
		f.t.Fatal(err)
	}
	d, _, err := g.o.RegisterDevice(ctx, f.cipher, store.DeviceInput{SiteID: s.ID, Key: "pk-" + slug, Name: "Sensor " + slug})
	if err != nil {
		f.t.Fatal(err)
	}
	return s, d
}

func (f *fx) readings(g *org, dev uuid.UUID, rows ...store.ReadingRow) {
	if _, err := g.o.InsertReadings(context.Background(), dev, rows); err != nil {
		f.t.Fatal(err)
	}
}

func row(ts time.Time, level float64, score float64, fc uint32) store.ReadingRow {
	return store.ReadingRow{TS: ts, ReceivedAt: ts, SID: "s", FC: fc, RawMM: 3000, LevelCm: level, Flags: []domain.QualityFlag{domain.FlagOK}, Score: score}
}

func newPubFx(t *testing.T) *pubFx {
	f := newFx(t)
	tag := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	p := &pubFx{fx: f}
	p.p1, p.d1 = f.publicSite(f.a, "fresh-"+tag)
	p.p2, p.d2 = f.publicSite(f.b, "stale-"+tag)
	p.p3, _ = f.publicSite(f.b, "empty-"+tag)
	now := time.Now().UTC().Truncate(time.Second)
	f.readings(f.a, p.d1.ID,
		row(now.Add(-10*time.Second), 123.4, 1, 1),
		row(now.Add(-5*time.Second), 999, 0.2, 2)) // an absurd, low-quality value that must never be shown
	for i := 0; i < 120; i++ { // two days of history, one per 30 minutes
		f.readings(f.a, p.d1.ID, row(now.Add(-time.Duration(i+1)*30*time.Minute), 100+float64(i%10), 1, uint32(100+i)))
	}
	f.readings(f.b, p.d2.ID, row(now.Add(-3*time.Hour), 77.7, 1, 1))
	// the private site has data too
	f.readings(f.a, f.a.dev.ID, row(now.Add(-time.Second), 55.5, 1, 1))
	return p
}

func (p *pubFx) get(path string, hdr map[string]string) (int, []byte, http.Header) {
	p.srv.pubCache = pubCache{} // the 5 s cache would hide data written a moment ago
	req := httptest.NewRequestWithContext(p.t.Context(), "GET", path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes(), rec.Header()
}

func TestPublicSitesListsOnlyPublicSitesAndLeaksNothing(t *testing.T) {
	p := newPubFx(t)
	code, body, hdr := p.get("/v1/public/sites", nil)
	if code != 200 || !strings.Contains(hdr.Get("Cache-Control"), "public") {
		t.Fatalf("%d %v", code, hdr)
	}
	var fc struct {
		Type     string `json:"type"`
		Features []struct {
			Geometry struct {
				Type        string    `json:"type"`
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(body, &fc); err != nil || fc.Type != "FeatureCollection" {
		t.Fatalf("not GeoJSON: %v %s", err, body)
	}
	bySlug := map[string]map[string]any{}
	for _, ft := range fc.Features {
		bySlug[ft.Properties["slug"].(string)] = ft.Properties
		if ft.Geometry.Type != "Point" || len(ft.Geometry.Coordinates) != 2 || ft.Geometry.Coordinates[0] != 110.25 && ft.Properties["slug"] == p.p1.Slug {
			t.Fatalf("coordinates must be [lon, lat]: %+v", ft.Geometry)
		}
	}
	for _, s := range []store.Site{p.p1, p.p2, p.p3} {
		if bySlug[s.Slug] == nil {
			t.Fatalf("public site %s missing", s.Slug)
		}
	}
	if bySlug[p.a.site.Slug] != nil || bySlug[p.a.site2.Slug] != nil {
		t.Fatal("a private site is listed publicly")
	}
	fresh, stale, empty := bySlug[p.p1.Slug], bySlug[p.p2.Slug], bySlug[p.p3.Slug]
	if fresh["level_cm"] != 123.4 || fresh["stale"] != false || fresh["status"] != "ONLINE" || fresh["stale_label"] != nil || fresh["updated_at"] == nil {
		t.Fatalf("fresh site (the 999 cm low-quality value must be ignored): %+v", fresh)
	}
	if stale["stale"] != true || stale["stale_label"] != "DATA TERTUNDA" || stale["level_cm"] != 77.7 || stale["status"] == "ONLINE" {
		t.Fatalf("stale site must be flagged prominently: %+v", stale)
	}
	if empty["stale"] != true || empty["level_cm"] != nil || empty["updated_at"] != nil || empty["stale_label"] != "DATA TERTUNDA" {
		t.Fatalf("a site without data is DATA TERTUNDA: %+v", empty)
	}
	if !strings.Contains(fresh["notice"].(string), "BUKAN sistem peringatan dini resmi") {
		t.Fatal("the safety statement must accompany public data")
	}
	for _, forbidden := range []string{p.a.slug, p.b.slug, "pk-", "secret", "email", "org_id", "device_key", "@example", p.a.dev.Key} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("the public list contains %q", forbidden)
		}
	}
}

func TestPublicSiteDetailHidesPrivateSites(t *testing.T) {
	p := newPubFx(t)
	code, body, _ := p.get("/v1/public/sites/"+p.p1.Slug, nil)
	if code != 200 || !strings.Contains(string(body), "Sensor "+p.p1.Slug) || !strings.Contains(string(body), "BUKAN sistem peringatan dini resmi") {
		t.Fatalf("%d %s", code, body)
	}
	for _, forbidden := range []string{"pk-", "secret", "org_id", "device_key"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("detail leaks %q: %s", forbidden, body)
		}
	}
	c1, b1, _ := p.get("/v1/public/sites/"+p.a.site.Slug, nil) // exists, but private
	c2, b2, _ := p.get("/v1/public/sites/no-such-site-here", nil)
	if c1 != 404 || c2 != 404 || string(b1) != string(b2) {
		t.Fatalf("a private site must look exactly like a missing one: %d %q / %d %q", c1, b1, c2, b2)
	}
	if c, _, _ := p.get("/v1/public/sites/"+p.a.site.Slug+"/series", nil); c != 404 {
		t.Fatalf("series of a private site: %d", c)
	}
	if c, _, _ := p.get("/v1/public/sites/Bad_Slug!", nil); c != 404 {
		t.Fatalf("malformed slug: %d", c)
	}
}

func TestPublicSeriesPicksResolutionAndFiltersLowQuality(t *testing.T) {
	p := newPubFx(t)
	base := "/v1/public/sites/" + p.p1.Slug + "/series"
	series := func(q string) (int, map[string]any) {
		code, body, _ := p.get(base+q, nil)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		return code, m
	}
	if code, m := series(""); code != 200 || m["res"] != "1m" {
		t.Fatalf("24 h defaults to the 1 minute aggregate: %d %v", code, m["res"])
	}
	code, m := series("?from=" + time.Now().Add(-3*time.Hour).UTC().Format(time.RFC3339))
	if code != 200 || m["res"] != "raw" {
		t.Fatalf("3 h uses raw readings: %d %v", code, m["res"])
	}
	pts := m["points"].([]any)
	if len(pts) < 5 {
		t.Fatalf("expected the recent readings, got %d", len(pts))
	}
	for _, pt := range pts {
		if pt.(map[string]any)["level_cm"].(float64) > 500 {
			t.Fatal("a low-quality reading appears in the public series")
		}
	}
	from := time.Now().Add(-30 * time.Hour).UTC().Format(time.RFC3339)
	if code, m := series("?from=" + from); code != 200 || m["res"] != "1m" || len(m["points"].([]any)) == 0 {
		t.Fatalf("30 h uses the 1 minute aggregate: %d %v", code, m["res"])
	}
	from = time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	if code, m := series("?from=" + from); code != 200 || m["res"] != "1h" || len(m["points"].([]any)) == 0 {
		t.Fatalf("3 days uses hourly data: %d %v", code, m["res"])
	}
	if code, m := series("?from=" + time.Now().Add(-45*24*time.Hour).UTC().Format(time.RFC3339)); code != 200 || m["res"] != "1d" {
		t.Fatalf("45 days uses daily data: %d %v", code, m["res"])
	}
	if code, m := series("?res=1d&from=" + from); code != 200 || m["res"] != "1d" {
		t.Fatalf("explicit resolution: %d %v", code, m["res"])
	}
	if c, _ := series("?res=1s"); c != 400 {
		t.Fatalf("unknown resolution: %d", c)
	}
	if c, _ := series("?from=" + time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); c != 400 {
		t.Fatalf("empty range: %d", c)
	}
	if c, _ := series("?from=" + time.Now().Add(-500*24*time.Hour).UTC().Format(time.RFC3339)); c != 400 {
		t.Fatalf("range beyond 400 days: %d", c)
	}
	if c, _ := series("?from=yesterday"); c != 400 {
		t.Fatalf("bad time: %d", c)
	}
	if c, _ := series("?device=" + p.d2.ID.String()); c != 404 {
		t.Fatalf("a device of another site: %d", c)
	}
	if c, _ := series("?device=" + p.d1.ID.String()); c != 200 {
		t.Fatalf("explicit device: %d", c)
	}
	if m["truncated"] != false {
		t.Fatal("a short series is not truncated")
	}
}

func TestPublicEndpointsAreRateLimitedPerIP(t *testing.T) {
	p := newPubFx(t)
	p.srv.pubLim = ingest.NewLimiter(1, 3, 100)
	for i := 0; i < 3; i++ {
		if c, _, _ := p.get("/v1/public/sites", nil); c != 200 {
			t.Fatalf("request %d: %d", i+1, c)
		}
	}
	c, body, hdr := p.get("/v1/public/sites", nil)
	if c != 429 || hdr.Get("Retry-After") == "" || errCode(body) != "rate_limited" {
		t.Fatalf("burst exceeded: %d %s %v", c, body, hdr)
	}
	// X-Forwarded-For is ignored unless a trusted proxy is configured: it cannot dodge the limit.
	if c, _, _ := p.get("/v1/public/sites", map[string]string{"X-Forwarded-For": "203.0.113.9"}); c != 429 {
		t.Fatalf("a spoofed X-Forwarded-For must not reset the limit: %d", c)
	}
	p.srv.Cfg.TrustProxy = true
	p.srv.pubLim = ingest.NewLimiter(1, 1, 100)
	if c, _, _ := p.get("/v1/public/sites", map[string]string{"X-Forwarded-For": "203.0.113.1"}); c != 200 {
		t.Fatal("first client behind the proxy")
	}
	if c, _, _ := p.get("/v1/public/sites", map[string]string{"X-Forwarded-For": "203.0.113.1"}); c != 429 {
		t.Fatal("same client behind the proxy is limited")
	}
	if c, _, _ := p.get("/v1/public/sites", map[string]string{"X-Forwarded-For": "203.0.113.2"}); c != 200 {
		t.Fatal("another client behind the proxy has its own bucket")
	}
}

func TestCORSAndSecurityHeaders(t *testing.T) {
	p := newPubFx(t)
	_, _, h := p.get("/v1/public/sites", map[string]string{"Origin": "https://app.example"})
	if h.Get("Access-Control-Allow-Origin") != "https://app.example" || h.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("allowed origin: %v", h)
	}
	_, _, h = p.get("/v1/public/sites", map[string]string{"Origin": "https://evil.example"})
	if h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("a foreign origin must get no CORS header: %v", h)
	}
	for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY"} {
		if h.Get(k) != want {
			t.Errorf("%s = %q", k, h.Get(k))
		}
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("missing CSP")
	}

	p.srv.Cfg.CORSOrigins = []string{"*"}
	_, _, h = p.get("/v1/public/sites", map[string]string{"Origin": "https://anywhere.example"})
	if h.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("public wildcard: %v", h)
	}
	// The wildcard never opens the authenticated API to browsers.
	code, _, h := p.do("GET", "/v1/me", "", nil)
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/v1/me", nil)
	req.Header.Set("Origin", "https://anywhere.example")
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	if code != 401 || rec.Header().Get("Access-Control-Allow-Origin") != "" || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("authenticated endpoints: %d %v", code, rec.Header())
	}

	p.srv.Cfg.CORSOrigins = []string{"https://app.example"}
	pre := func(origin, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), "OPTIONS", path, nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", "GET")
		w := httptest.NewRecorder()
		p.h.ServeHTTP(w, r)
		return w
	}
	if w := pre("https://app.example", "/v1/sites"); w.Code != 204 || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
	if w := pre("https://evil.example", "/v1/sites"); w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("foreign preflight: %d %v", w.Code, w.Header())
	}
}
