package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sellebeww/spillway/internal/auth"
	"github.com/sellebeww/spillway/internal/auth/jwttest"
	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/ingest"
	"github.com/sellebeww/spillway/internal/store"
	"github.com/sellebeww/spillway/internal/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.Cleanup()
	os.Exit(code)
}

type org struct {
	slug   string
	o      *store.OrgStore
	site   store.Site // private site 1
	site2  store.Site
	dev    store.Device
	secret string
	rule   store.AlertRule
}

type fx struct {
	t      *testing.T
	db     *testutil.DB
	is     *jwttest.Issuer
	srv    *Server
	h      http.Handler
	a, b   *org
	cipher *auth.Cipher
}

func newFx(t *testing.T) *fx {
	t.Helper()
	db := testutil.SharedDB(t)
	is := jwttest.New(t)
	c, _ := auth.NewCipher(1, bytes.Repeat([]byte{3}, 32), nil)
	ver := &auth.Verifier{Issuer: is.URL, Audience: is.Audience, JWKSURL: is.JWKSURL()}
	srv := NewServer(db.Store, c, ver, nil, ServerConfig{BootstrapAdminEmail: "boot@example.org", CORSOrigins: []string{"https://app.example"}, BotUsername: "spillway_bot"}, nil)
	srv.apiLim = ingest.NewLimiter(1e6, 1e6, 10)
	srv.pubLim = ingest.NewLimiter(1e6, 1e6, 10)
	r := chi.NewRouter()
	srv.Mount(r)
	f := &fx{t: t, db: db, is: is, srv: srv, h: r, cipher: c}
	f.a, f.b = f.newOrg(), f.newOrg()
	return f
}

func (f *fx) newOrg() *org {
	t := f.t
	ctx := context.Background()
	tag := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	o, err := f.db.Store.CreateOrg(ctx, "o-"+tag, "Org "+tag)
	if err != nil {
		t.Fatal(err)
	}
	st := f.db.Store.ForOrg(o.ID)
	mk := func(slug string) store.Site {
		s, err := st.CreateSite(ctx, store.SiteInput{Slug: slug + "-" + tag, Name: slug, Lat: -6, Lon: 106})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	g := &org{slug: o.Slug, o: st, site: mk("alpha"), site2: mk("beta")}
	dev, secret, err := st.RegisterDevice(ctx, f.cipher, store.DeviceInput{SiteID: g.site.ID, Key: "d-" + tag, Name: "Sensor"})
	if err != nil {
		t.Fatal(err)
	}
	g.dev, g.secret = dev, secret
	if _, err := st.AddCalibration(ctx, dev.ID, domain.Calibration{OffsetCm: 500, ValidFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}, false); err != nil {
		t.Fatal(err)
	}
	g.rule, err = st.CreateRule(ctx, store.RuleInput{SiteID: g.site.ID, Kind: domain.KindThreshold, Enabled: true, Tiers: []domain.Tier{{Value: 100, Severity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// token returns a valid token for sub in organization g; the user gets its roles through the store.
func (f *fx) token(g *org, sub string, admin bool, sites ...uuid.UUID) string {
	f.t.Helper()
	ctx := context.Background()
	email := sub + "@example.org"
	u, err := g.o.UpsertUser(ctx, sub, &email, sub)
	if err != nil {
		f.t.Fatal(err)
	}
	if admin {
		if _, err := g.o.SetMembership(ctx, u.ID, store.RoleAdmin, nil); err != nil {
			f.t.Fatal(err)
		}
	}
	for _, s := range sites {
		if _, err := g.o.SetMembership(ctx, u.ID, store.RoleOperator, &s); err != nil {
			f.t.Fatal(err)
		}
	}
	return f.is.Token(f.is.Claims(sub, email, g.slug))
}

func (f *fx) do(method, path, tok string, body any) (int, []byte, http.Header) {
	f.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequestWithContext(f.t.Context(), method, path, rd)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes(), rec.Header()
}

func errCode(b []byte) string {
	var e errorBody
	_ = json.Unmarshal(b, &e)
	return e.Error.Code
}

func TestAuthenticationRejectsBadTokens(t *testing.T) {
	f := newFx(t)
	noExp := f.is.Claims("u1", "u1@example.org", f.a.slug)
	delete(noExp, "exp")
	mut := func(k string, v any) string {
		c := f.is.Claims("u1", "u1@example.org", f.a.slug)
		c[k] = v
		return f.is.Token(c)
	}
	cases := map[string]string{
		"missing":      "",
		"garbage":      "abc.def.ghi",
		"expired":      mut("exp", time.Now().Add(-time.Hour).Unix()),
		"wrong issuer": mut("iss", "https://evil.example"),
		"wrong aud":    mut("aud", "other-api"),
		"no exp":       f.is.Token(noExp),
		"tampered":     f.is.Token(f.is.Claims("u1", "u1@example.org", f.a.slug)) + "x",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			code, body, hdr := f.do("GET", "/v1/me", tok, nil)
			if code != 401 || errCode(body) != "unauthorized" || !strings.Contains(hdr.Get("WWW-Authenticate"), "Bearer") {
				t.Fatalf("%d %s", code, body)
			}
		})
	}
	if code, body, _ := f.do("GET", "/v1/me", f.is.Token(f.is.Claims("u2", "u2@example.org", "no-such-org")), nil); code != 403 || errCode(body) != "no_organization" {
		t.Fatalf("unknown org: %d %s", code, body)
	}
	tok := f.is.Token(f.is.Claims("plain", "plain@example.org", f.a.slug))
	if code, body, _ := f.do("GET", "/v1/me", tok, nil); code != 200 || !strings.Contains(string(body), `"admin":false`) {
		t.Fatalf("me: %d %s", code, body)
	}
	if code, body, _ := f.do("GET", "/v1/sites", tok, nil); code != 200 || !strings.Contains(string(body), `"total":0`) {
		t.Fatalf("a user without a role sees nothing: %d %s", code, body)
	}
	if code, _, _ := f.do("GET", "/v1/audit", tok, nil); code != 403 {
		t.Fatalf("audit without a role: %d", code)
	}
	if code, _, _ := f.do("GET", fmt.Sprintf("/v1/sites/%s", f.a.site.ID), tok, nil); code != 404 {
		t.Fatalf("site without a role: %d", code)
	}
}

func TestBootstrapAdminOnlyOnce(t *testing.T) {
	f := newFx(t)
	c := f.is.Claims("boot-1", "boot@example.org", f.b.slug)
	code, body, _ := f.do("GET", "/v1/me", f.is.Token(c), nil)
	if code != 200 || !strings.Contains(string(body), `"admin":true`) {
		t.Fatalf("the bootstrap e-mail must become admin: %d %s", code, body)
	}
	c2 := f.is.Claims("boot-2", "boot@example.org", f.b.slug)
	if _, body, _ := f.do("GET", "/v1/me", f.is.Token(c2), nil); !strings.Contains(string(body), `"admin":false`) {
		t.Fatalf("a second account with the bootstrap e-mail must not become admin: %s", body)
	}
	g := f.newOrg()
	c3 := f.is.Claims("boot-3", "boot@example.org", g.slug)
	c3["email_verified"] = false
	if _, body, _ := f.do("GET", "/v1/me", f.is.Token(c3), nil); !strings.Contains(string(body), `"admin":false`) {
		t.Fatalf("unverified e-mail: %s", body)
	}
}

func TestRolesAdminOperatorAndCrossSite(t *testing.T) {
	f := newFx(t)
	a := f.a
	admin := f.token(a, "adm", true)
	op := f.token(a, "op", false, a.site.ID)
	s1, s2 := a.site.ID, a.site2.ID
	dev2, _, err := a.o.RegisterDevice(context.Background(), f.cipher, store.DeviceInput{SiteID: s2, Key: "x-" + uuid.NewString()[:8], Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	type tc struct {
		name, method, path string
		body               any
		tok                string
		want               int
	}
	devBody := func() map[string]any { return map[string]any{"device_key": "k-" + uuid.NewString()[:8], "name": "N"} }
	ruleBody := map[string]any{"type": "THRESHOLD", "tiers": []map[string]any{{"value": 120, "severity": 1}}}
	cases := []tc{
		{"operator reads own site", "GET", "/v1/sites/" + s1.String(), nil, op, 200},
		{"operator cannot see another site", "GET", "/v1/sites/" + s2.String(), nil, op, 404},
		{"admin sees every site", "GET", "/v1/sites/" + s2.String(), nil, admin, 200},
		{"operator cannot create sites", "POST", "/v1/sites", map[string]any{"slug": "zz-new", "name": "Z", "lat": 1, "lon": 1}, op, 403},
		{"operator cannot edit sites", "PUT", "/v1/sites/" + s1.String(), map[string]any{"slug": "zz-new", "name": "Z", "lat": 1, "lon": 1}, op, 403},
		{"operator cannot read the audit log", "GET", "/v1/audit", nil, op, 403},
		{"operator cannot assign operators", "POST", "/v1/sites/" + s1.String() + "/operators", map[string]any{"email": "x@example.org"}, op, 403},
		{"operator registers a device on own site", "POST", "/v1/sites/" + s1.String() + "/devices", devBody(), op, 201},
		{"operator cannot register on another site", "POST", "/v1/sites/" + s2.String() + "/devices", devBody(), op, 404},
		{"operator cannot read another site's device", "GET", "/v1/devices/" + dev2.ID.String(), nil, op, 404},
		{"operator cannot rotate another site's secret", "POST", "/v1/devices/" + dev2.ID.String() + "/rotate-secret", nil, op, 404},
		{"operator creates a rule on own site", "POST", "/v1/sites/" + s1.String() + "/rules", ruleBody, op, 201},
		{"operator cannot create a rule elsewhere", "POST", "/v1/sites/" + s2.String() + "/rules", ruleBody, op, 404},
		{"operator lists own alerts", "GET", "/v1/sites/" + s1.String() + "/alerts", nil, op, 200},
		{"operator cannot list foreign alerts", "GET", "/v1/sites/" + s2.String() + "/alerts", nil, op, 404},
		{"admin reads the audit log", "GET", "/v1/audit", nil, admin, 200},
		{"admin creates a site", "POST", "/v1/sites", map[string]any{"slug": "ok-" + uuid.NewString()[:6], "name": "Z", "lat": 1, "lon": 1}, admin, 201},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, body, _ := f.do(c.method, c.path, c.tok, c.body); code != c.want {
				t.Fatalf("got %d want %d: %s", code, c.want, body)
			}
		})
	}
	if code, body, _ := f.do("GET", "/v1/sites", op, nil); !strings.Contains(string(body), `"total":1`) || code != 200 {
		t.Fatalf("an operator lists only the assigned site: %s", body)
	}
}

// TestCrossOrganizationIsolation drives every authenticated endpoint with organization A's IDs and
// an admin token of organization B: all must be "not found", and A's data must stay untouched.
func TestCrossOrganizationIsolation(t *testing.T) {
	f := newFx(t)
	a, b := f.a, f.b
	adminB := f.token(b, "admin-b", true)
	_ = f.token(a, "admin-a", true)
	s, d, r := a.site.ID.String(), a.dev.ID.String(), a.rule.ID.String()
	devBody := map[string]any{"device_key": "k-" + uuid.NewString()[:8], "name": "N"}
	ruleBody := map[string]any{"type": "THRESHOLD", "tiers": []map[string]any{{"value": 120, "severity": 1}}}
	calBody := map[string]any{"offset_cm": 1, "valid_from": "2030-01-01T00:00:00Z"}
	siteBody := map[string]any{"slug": "hacked-" + uuid.NewString()[:6], "name": "Hacked", "lat": 1, "lon": 1}
	endpoints := []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/sites/" + s, nil}, {"PUT", "/v1/sites/" + s, siteBody},
		{"GET", "/v1/sites/" + s + "/devices", nil}, {"POST", "/v1/sites/" + s + "/devices", devBody},
		{"GET", "/v1/sites/" + s + "/rules", nil}, {"POST", "/v1/sites/" + s + "/rules", ruleBody},
		{"GET", "/v1/sites/" + s + "/alerts", nil}, {"GET", "/v1/sites/" + s + "/channels", nil},
		{"POST", "/v1/sites/" + s + "/subscription-links", nil}, {"POST", "/v1/sites/" + s + "/subscription-links/revoke", nil},
		{"POST", "/v1/sites/" + s + "/operators", map[string]any{"email": "a@example.org"}},
		{"GET", "/v1/devices/" + d, nil}, {"POST", "/v1/devices/" + d + "/rotate-secret", nil},
		{"GET", "/v1/devices/" + d + "/health", nil}, {"GET", "/v1/devices/" + d + "/calibrations", nil},
		{"POST", "/v1/devices/" + d + "/calibrations", calBody},
		{"GET", "/v1/rules/" + r, nil}, {"PUT", "/v1/rules/" + r, ruleBody},
	}
	for _, e := range endpoints {
		t.Run(e.method+" "+strings.ReplaceAll(e.path, "/v1/", ""), func(t *testing.T) {
			code, body, _ := f.do(e.method, e.path, adminB, e.body)
			if code != 404 {
				t.Fatalf("cross-organization access answered %d: %s", code, body)
			}
			if strings.Contains(string(body), a.dev.Key) || strings.Contains(string(body), a.slug) {
				t.Fatal("the error leaks organization A's data")
			}
		})
	}
	for _, path := range []string{"/v1/sites", "/v1/audit"} {
		_, body, _ := f.do("GET", path, adminB, nil)
		if strings.Contains(string(body), a.site.ID.String()) || strings.Contains(string(body), a.dev.ID.String()) || strings.Contains(string(body), a.site.Slug) {
			t.Fatalf("%s leaks organization A: %s", path, body)
		}
	}
	ctx := context.Background()
	if devs, _ := a.o.ListDevices(ctx, nil); len(devs) != 1 {
		t.Fatalf("A has %d devices", len(devs))
	}
	if cals, _ := a.o.ListCalibrations(ctx, a.dev.ID); len(cals) != 1 {
		t.Fatal("A's calibration history changed")
	}
	if rs, _ := a.o.ListRules(ctx, nil); len(rs) != 1 || len(rs[0].Tiers) != 1 || rs[0].Tiers[0].Value != 100 {
		t.Fatal("A's rule changed")
	}
	if st, _ := a.o.GetSite(ctx, a.site.ID); st.Name != "alpha" {
		t.Fatalf("A's site was renamed to %q", st.Name)
	}
	da, _ := f.db.Store.DeviceAuthByKey(ctx, a.dev.Key)
	if raw, err := da.Secret(f.cipher); err != nil || fmt.Sprintf("%x", raw) != a.secret {
		t.Fatal("A's device secret was rotated by another organization")
	}
}

func TestValidationIsStrict(t *testing.T) {
	f := newFx(t)
	admin := f.token(f.a, "adm", true)
	site := "/v1/sites/" + f.a.site.ID.String()
	tier := func(v float64, s int) map[string]any { return map[string]any{"value": v, "severity": s} }
	tiers := func(ts ...map[string]any) map[string]any { return map[string]any{"type": "THRESHOLD", "tiers": ts} }
	one := []map[string]any{tier(30, 1)}
	rules := map[string]any{
		"descending tiers":      tiers(tier(150, 1), tier(100, 2)),
		"severity out of range": tiers(tier(100, 4)),
		"severity zero":         tiers(tier(100, 0)),
		"no tiers":              map[string]any{"type": "THRESHOLD", "tiers": []any{}},
		"four tiers":            tiers(tier(1, 1), tier(2, 2), tier(3, 3), tier(4, 3)),
		"negative value":        tiers(tier(-1, 1)),
		"huge value":            tiers(tier(1e9, 1)),
		"unknown type":          map[string]any{"type": "NO_DATA", "tiers": one},
		"hysteresis on rate":    map[string]any{"type": "RATE_OF_RISE", "hysteresis_cm": 5, "tiers": one},
		"zero hysteresis":       map[string]any{"type": "THRESHOLD", "hysteresis_cm": 0, "tiers": one},
		"cooldown too short":    map[string]any{"type": "THRESHOLD", "cooldown_s": 5, "tiers": one},
		"min quality zero":      map[string]any{"type": "THRESHOLD", "min_quality": 0, "tiers": one},
		"unknown field":         map[string]any{"type": "THRESHOLD", "tiers": one, "third_kind": true},
	}
	for name, body := range rules {
		t.Run("rule "+name, func(t *testing.T) {
			if code, resp, _ := f.do("POST", site+"/rules", admin, body); code != 400 || errCode(resp) == "" {
				t.Fatalf("accepted: %d %s", code, resp)
			}
		})
	}
	if code, _, _ := f.do("POST", site+"/rules", admin, map[string]any{"type": "RATE_OF_RISE", "tiers": []map[string]any{tier(30, 1), tier(60, 2)}, "cooldown_s": 600}); code != 201 {
		t.Fatalf("a valid rate-of-rise rule: %d", code)
	}

	cal := "/v1/devices/" + f.a.dev.ID.String() + "/calibrations"
	type vc struct {
		method, path string
		body         any
		want         int
	}
	other := map[string]vc{
		"not json":              {"POST", "/v1/sites", "{nope", 400},
		"trailing data":         {"POST", "/v1/sites", `{"slug":"aa-bb","name":"n","lat":1,"lon":1} {}`, 400},
		"missing coordinates":   {"POST", "/v1/sites", map[string]any{"slug": "aa-bb", "name": "n"}, 400},
		"latitude 91":           {"POST", "/v1/sites", map[string]any{"slug": "aa-bb", "name": "n", "lat": 91, "lon": 1}, 400},
		"bad slug":              {"POST", "/v1/sites", map[string]any{"slug": "Bad Slug", "name": "n", "lat": 1, "lon": 1}, 400},
		"bad timezone":          {"POST", "/v1/sites", map[string]any{"slug": "aa-bb", "name": "n", "lat": 1, "lon": 1, "tz": "Mars/Base"}, 400},
		"oversized body":        {"POST", "/v1/sites", `{"slug":"` + strings.Repeat("x", 70000) + `"}`, 413},
		"device bad key":        {"POST", site + "/devices", map[string]any{"device_key": "has space", "name": "n"}, 400},
		"device interval":       {"POST", site + "/devices", map[string]any{"device_key": "okk", "name": "n", "expected_interval_s": 999999}, 400},
		"calibration no offset": {"POST", cal, map[string]any{"valid_from": "2030-01-01T00:00:00Z"}, 400},
		"calibration min only":  {"POST", cal, map[string]any{"offset_cm": 1, "min_valid_mm": 5, "valid_from": "2030-01-01T00:00:00Z"}, 400},
		"bad uuid":              {"GET", "/v1/devices/not-a-uuid", nil, 404},
	}
	for name, c := range other {
		t.Run(name, func(t *testing.T) {
			if code, resp, _ := f.do(c.method, c.path, admin, c.body); code != c.want {
				t.Fatalf("got %d want %d: %s", code, c.want, resp)
			}
		})
	}
	if code, _, _ := f.do("POST", cal, admin, map[string]any{"offset_cm": 7, "valid_from": "2027-01-01T00:00:00Z"}); code != 201 {
		t.Fatalf("a later calibration: %d", code)
	}
	if code, body, _ := f.do("POST", cal, admin, map[string]any{"offset_cm": 1, "valid_from": "2021-01-01T00:00:00Z"}); code != 409 || errCode(body) != "backdated" {
		t.Fatalf("a backdated calibration needs confirmation: %d %s", code, body)
	}
	if code, _, _ := f.do("POST", cal, admin, map[string]any{"offset_cm": 1, "valid_from": "2021-01-01T00:00:00Z", "confirm_backdate": true}); code != 201 {
		t.Fatalf("a confirmed backdate: %d", code)
	}
	if code, body, _ := f.do("GET", cal+"?limit=1", admin, nil); code != 200 || !strings.Contains(string(body), `"limit":1`) || !strings.Contains(string(body), `"total":3`) {
		t.Fatalf("pagination: %d %s", code, body)
	}
}

func TestSecretIsShownExactlyOnce(t *testing.T) {
	f := newFx(t)
	admin := f.token(f.a, "adm", true)
	site := "/v1/sites/" + f.a.site.ID.String()
	code, body, hdr := f.do("POST", site+"/devices", admin, map[string]any{"device_key": "once-" + uuid.NewString()[:6], "name": "Once"})
	var created struct {
		Device store.Device `json:"device"`
		Secret string       `json:"secret"`
	}
	if err := json.Unmarshal(body, &created); code != 201 || err != nil || len(created.Secret) != 64 || hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("register: %d %s", code, body)
	}
	for _, path := range []string{"/v1/devices/" + created.Device.ID.String(), site + "/devices", "/v1/devices/" + created.Device.ID.String() + "/health", "/v1/audit", site, site + "/channels", site + "/rules"} {
		_, b, _ := f.do("GET", path, admin, nil)
		if strings.Contains(string(b), created.Secret) || strings.Contains(strings.ToLower(string(b)), "secret") {
			t.Fatalf("GET %s exposes secret material: %s", path, b)
		}
	}
	code, body, _ = f.do("POST", "/v1/devices/"+created.Device.ID.String()+"/rotate-secret", admin, nil)
	var rot struct{ Secret string }
	_ = json.Unmarshal(body, &rot)
	if code != 200 || len(rot.Secret) != 64 || rot.Secret == created.Secret {
		t.Fatalf("rotate: %d %s", code, body)
	}
	da, _ := f.db.Store.DeviceAuthByKey(context.Background(), created.Device.Key)
	raw, err := da.Secret(f.cipher)
	if err != nil || fmt.Sprintf("%x", raw) != rot.Secret {
		t.Fatal("the rotated secret must be the one stored, and the old one must be gone")
	}
	_, audit, _ := f.do("GET", "/v1/audit", admin, nil)
	if strings.Contains(string(audit), created.Secret) || strings.Contains(string(audit), rot.Secret) {
		t.Fatal("the audit log contains a secret")
	}
	if !strings.Contains(string(audit), "device.rotate_secret") || !strings.Contains(string(audit), "device.register") {
		t.Fatalf("mutations must be audited: %s", audit)
	}
}

func TestEveryMutationIsAudited(t *testing.T) {
	f := newFx(t)
	admin := f.token(f.a, "adm", true)
	site := "/v1/sites/" + f.a.site.ID.String()
	f.do("POST", "/v1/sites", admin, map[string]any{"slug": "aud-" + uuid.NewString()[:6], "name": "A", "lat": 1, "lon": 1})
	f.do("PUT", site, admin, map[string]any{"slug": "alpha-" + f.a.slug[2:], "name": "Renamed", "lat": 1, "lon": 1})
	f.do("POST", site+"/rules", admin, map[string]any{"type": "THRESHOLD", "tiers": []map[string]any{{"value": 9, "severity": 1}}})
	f.do("PUT", "/v1/rules/"+f.a.rule.ID.String(), admin, map[string]any{"type": "THRESHOLD", "enabled": false, "tiers": []map[string]any{{"value": 9, "severity": 1}}})
	f.do("POST", "/v1/devices/"+f.a.dev.ID.String()+"/calibrations", admin, map[string]any{"offset_cm": 2, "valid_from": "2031-01-01T00:00:00Z"})
	f.do("POST", site+"/subscription-links", admin, nil)
	f.do("POST", site+"/subscription-links/revoke", admin, nil)
	_, body, _ := f.do("GET", "/v1/audit?limit=200", admin, nil)
	for _, action := range []string{"site.create", "site.update", "rule.create", "rule.update", "calibration.add", "subscription_token.create", "subscription_token.revoke"} {
		if !strings.Contains(string(body), `"`+action+`"`) {
			t.Errorf("no audit entry for %s", action)
		}
	}
	if !strings.Contains(string(body), "adm@example.org") {
		t.Error("the audit log must name the acting user")
	}
}

func TestUnverifiedEmailCannotImpersonateAnOperatorTarget(t *testing.T) {
	f := newFx(t)
	admin := f.token(f.a, "adm", true)
	c := f.is.Claims("imposter", "victim@example.org", f.a.slug)
	c["email_verified"] = false
	if code, _, _ := f.do("GET", "/v1/me", f.is.Token(c), nil); code != 200 {
		t.Fatalf("login: %d", code)
	}
	code, body, _ := f.do("POST", "/v1/sites/"+f.a.site.ID.String()+"/operators", admin, map[string]any{"email": "victim@example.org"})
	if code != 404 || errCode(body) != "user_not_found" {
		t.Fatalf("an unverified e-mail must not be matchable: %d %s", code, body)
	}
	// A verified user is found and, once assigned, can act on the site.
	ok := f.is.Claims("real", "real@example.org", f.a.slug)
	tok := f.is.Token(ok)
	f.do("GET", "/v1/me", tok, nil)
	if code, body, _ := f.do("POST", "/v1/sites/"+f.a.site.ID.String()+"/operators", admin, map[string]any{"email": "REAL@example.org"}); code != 201 {
		t.Fatalf("assign: %d %s", code, body)
	}
	if code, body, _ := f.do("GET", "/v1/sites/"+f.a.site.ID.String(), tok, nil); code != 200 {
		t.Fatalf("the new operator must see the site at once: %d %s", code, body)
	}
}
