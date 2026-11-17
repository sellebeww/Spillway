package store

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sellebeww/spillway/internal/domain"
)

func TestChooseResolution(t *testing.T) {
	cases := []struct {
		span time.Duration
		want Resolution
	}{
		{time.Minute, ResRaw}, {6 * time.Hour, ResRaw},
		{6*time.Hour + time.Second, Res1m}, {48 * time.Hour, Res1m},
		{48*time.Hour + time.Second, Res1h}, {30 * 24 * time.Hour, Res1h},
		{30*24*time.Hour + time.Second, Res1d}, {365 * 24 * time.Hour, Res1d},
	}
	for _, c := range cases {
		if got := ChooseResolution(c.span); got != c.want {
			t.Errorf("span %v: got %s want %s", c.span, got, c.want)
		}
	}
}

func TestIsScoped(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{"select", `SELECT id FROM sites WHERE org_id = $1 AND id = $2`, true},
		{"update", `UPDATE sites SET name = $3 WHERE org_id = $1 AND id = $2`, true},
		{"delete spacing", "DELETE FROM x WHERE\n\torg_id=$1", true},
		{"join condition", `SELECT 1 FROM readings r JOIN devices d ON d.id = r.device_id AND d.org_id = $1`, true},
		{"insert values", `INSERT INTO sites (org_id, slug) VALUES ($1, $2)`, true},
		{"insert select", "INSERT INTO t (org_id, a)\nSELECT $1, x FROM y", true},
		{"no filter", `SELECT id FROM sites`, false},
		{"wrong parameter", `SELECT id FROM sites WHERE org_id = $2`, false},
		{"param prefix", `SELECT id FROM sites WHERE org_id = $10`, false},
		{"other column", `SELECT id FROM sites WHERE site_org_id = $1`, false},
		{"filter only in a comment", "SELECT id FROM sites -- org_id = $1\nWHERE id = $2", false},
		{"insert without org first", `INSERT INTO sites (slug, org_id) VALUES ($2, $1)`, false},
		{"insert not using $1 first", `INSERT INTO sites (org_id, slug) VALUES ($2, $1)`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsScoped(c.sql); got != c.want {
				t.Fatalf("IsScoped = %v, want %v", got, c.want)
			}
		})
	}
}

// TestNoUnscopedDatabaseCalls statically checks that tenant SQL can only reach the database
// through OrgStore.exec/query (which refuse unscoped SQL). Any other Exec/Query call must carry
// an explicit "-- unscoped:" marker AND live in a function on the reviewed allowlist.
func TestNoUnscopedDatabaseCalls(t *testing.T) {
	allowed := map[string]bool{
		"CreateOrg": true, "OrgBySlug": true, "DeviceAuthByKey": true,
		"RedeemSubscriptionToken": true, "ClaimDueNotifications": true, "ApplyRetention": true,
		"OpenEventsAll": true, "PublicSites": true, "DevicesForStatusCheck": true, "BotOffset": true, "SetBotOffset": true, "ChannelsByChat": true, "OutboxStats": true,
	}
	guard := map[string]bool{"exec": true, "query": true}
	dbCalls := map[string]bool{"Exec": true, "Query": true, "QueryRow": true, "SendBatch": true, "CopyFrom": true}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f) //nolint:gosec // package source files
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !dbCalls[sel.Sel.Name] || len(call.Args) < 2 {
					return true
				}
				found++
				if guard[fn.Name.Name] {
					return true
				}
				marked := strings.HasPrefix(firstString(call.Args[1]), "-- unscoped:")
				switch {
				case !marked:
					t.Errorf("%s: %s calls %s directly; use o.exec/o.query/fetchOne/fetchAll", f, fn.Name.Name, sel.Sel.Name)
				case !allowed[fn.Name.Name]:
					t.Errorf("%s: %s uses an unscoped query but is not on the reviewed allowlist", f, fn.Name.Name)
				}
				return true
			})
		}
	}
	if found < 5 {
		t.Fatalf("inspected only %d database calls; the check is not working", found)
	}
}

// firstString returns the leftmost string literal of an expression ("a" + b + "c" gives a).
func firstString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			return strings.Trim(v.Value, "`\"")
		}
	case *ast.BinaryExpr:
		return firstString(v.X)
	}
	return ""
}

func TestSiteInputNormalize(t *testing.T) {
	ok := SiteInput{Slug: "kali-1", Name: "Kali", Lat: -6.2, Lon: 106.8}
	cases := []struct {
		name    string
		mutate  func(*SiteInput)
		wantErr bool
	}{
		{"valid, tz defaulted", func(*SiteInput) {}, false},
		{"explicit zone", func(s *SiteInput) { s.TZ = "Asia/Makassar" }, false},
		{"unknown zone", func(s *SiteInput) { s.TZ = "Mars/Base" }, true},
		{"bad slug", func(s *SiteInput) { s.Slug = "Bad Slug" }, true},
		{"empty name", func(s *SiteInput) { s.Name = "  " }, true},
		{"lat too high", func(s *SiteInput) { s.Lat = 90.1 }, true},
		{"lon too low", func(s *SiteInput) { s.Lon = -180.1 }, true},
		{"boundary coordinates", func(s *SiteInput) { s.Lat, s.Lon = 90, -180 }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := ok
			c.mutate(&in)
			err := in.normalize()
			if (err != nil) != c.wantErr || (err != nil && !errors.Is(err, ErrInvalid)) {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if err == nil && in.TZ == "" {
				t.Fatal("tz must default")
			}
		})
	}
}

func TestRuleInputEncodeAndDecode(t *testing.T) {
	tiers := []domain.Tier{{Value: 100, Severity: domain.SeverityWaspada}, {Value: 150, Severity: domain.SeveritySiaga}}

	params, cooldownS, minQ, err := RuleInput{Kind: domain.KindThreshold, Tiers: tiers}.encode()
	if err != nil || cooldownS != 1800 || minQ != 0.5 {
		t.Fatalf("defaults: %v %d %v", err, cooldownS, minQ)
	}
	rule, err := ruleRow{Type: "THRESHOLD", Params: params, CooldownS: cooldownS, MinQuality: minQ}.rule()
	if err != nil || rule.HysteresisCm != 5 || len(rule.Tiers) != 2 || rule.Tiers[1].Severity != domain.SeveritySiaga || rule.Cooldown != 30*time.Minute {
		t.Fatalf("decoded: %+v %v", rule, err)
	}
	if err := rule.Domain().Validate(); err != nil {
		t.Fatalf("stored rule must satisfy the engine: %v", err)
	}

	params, _, _, err = RuleInput{Kind: domain.KindRateOfRise, Tiers: tiers, HysteresisCm: 9}.encode()
	if err != nil || strings.Contains(string(params), "hysteresis") {
		t.Fatalf("rate of rise has a fixed 20 percent hysteresis, not a configurable one: %s %v", params, err)
	}

	for name, in := range map[string]RuleInput{
		"no tiers":            {Kind: domain.KindThreshold},
		"descending tiers":    {Kind: domain.KindThreshold, Tiers: []domain.Tier{tiers[1], tiers[0]}},
		"unknown kind":        {Kind: "OTHER", Tiers: tiers},
		"min quality too big": {Kind: domain.KindThreshold, Tiers: tiers, MinQuality: 1.5},
		"negative cooldown":   {Kind: domain.KindThreshold, Tiers: tiers, Cooldown: -time.Second},
	} {
		if _, _, _, err := in.encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := (ruleRow{Params: []byte("not json")}).rule(); err == nil {
		t.Error("corrupt params must be an error")
	}
}

func TestMapErr(t *testing.T) {
	cases := []struct {
		code string
		want error
	}{
		{"23505", ErrConflict}, {"23503", ErrNotFound}, {"23514", ErrInvalid}, {"23502", ErrInvalid}, {"23001", ErrConflict},
	}
	for _, c := range cases {
		err := mapErr(&pgconn.PgError{Code: c.code, ConstraintName: "x", Message: "secret value 12345"})
		if !errors.Is(err, c.want) {
			t.Errorf("code %s: %v", c.code, err)
		}
		if strings.Contains(err.Error(), "12345") {
			t.Errorf("code %s: error leaks the database message", c.code)
		}
	}
	other := errors.New("boom")
	if !errors.Is(mapErr(other), other) || mapErr(nil) != nil {
		t.Error("unknown errors pass through unchanged")
	}
}

func TestClampLimitAndTruncate(t *testing.T) {
	if clampLimit(0, 10, 100) != 10 || clampLimit(-5, 10, 100) != 10 || clampLimit(500, 10, 100) != 100 || clampLimit(7, 10, 100) != 7 {
		t.Error("clampLimit")
	}
	if truncate("abcdef", 3) != "abc" || truncate("ab", 3) != "ab" {
		t.Error("truncate")
	}
}

func TestMissingActorFallsBackToSystem(t *testing.T) {
	if actorFrom(t.Context()).Label != "system" {
		t.Error("default actor must be system")
	}
	ctx := WithActor(t.Context(), Actor{Label: "alice"})
	if actorFrom(ctx).Label != "alice" {
		t.Error("actor not carried")
	}
}
