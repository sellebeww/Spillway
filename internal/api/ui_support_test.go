// Copyright (c) 2026 Vassel Goleyu. MIT License.

package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/store"
)

// These two tests cover the API additions the web UI needs (Phase 7).

func TestPublicSiteShowsThresholdsSeverityAndAlertsWithoutInternalIDs(t *testing.T) {
	p := newPubFx(t)
	ctx := context.Background()
	rule, err := p.a.o.CreateRule(ctx, store.RuleInput{SiteID: p.p1.ID, Kind: domain.KindThreshold, Enabled: true,
		Tiers: []domain.Tier{{Value: 150, Severity: domain.SeverityWaspada}, {Value: 200, Severity: domain.SeveritySiaga}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.a.o.CreateRule(ctx, store.RuleInput{SiteID: p.p1.ID, Kind: domain.KindThreshold, Enabled: false,
		Tiers: []domain.Tier{{Value: 999, Severity: domain.SeverityAwas}}}); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		code, body, _ := p.get("/v1/public/sites/"+p.p1.Slug, nil)
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		for _, forbidden := range []string{rule.ID.String(), "rule_id", "org_id"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("the public detail leaks %q: %s", forbidden, body)
			}
		}
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		return m
	}
	m := read()
	if th := m["thresholds"].([]any); len(th) != 2 || m["severity"] != float64(0) || len(m["alerts"].([]any)) != 0 {
		t.Fatalf("thresholds of enabled rules only, no open alert yet: %+v", m)
	}
	if _, err := p.a.o.OpenEvent(ctx, rule.ID, domain.SeveritySiaga, time.Now(), 201, nil); err != nil {
		t.Fatal(err)
	}
	m = read()
	al := m["alerts"].([]any)
	if m["severity"] != float64(2) || len(al) != 1 || al[0].(map[string]any)["state"] != "OPEN" {
		t.Fatalf("an open SIAGA alert must show: %+v", m)
	}
}

func TestDeviceHealthReturnsTheLastReadingForCalibrationPreview(t *testing.T) {
	f := newFx(t)
	admin := f.token(f.a, "adm", true)
	path := "/v1/devices/" + f.a.dev.ID.String() + "/health"
	var h struct {
		LastReading *struct {
			RawMM   int     `json:"raw_mm"`
			LevelCm float64 `json:"level_cm"`
		} `json:"last_reading"`
	}
	_, body, _ := f.do("GET", path, admin, nil)
	if _ = json.Unmarshal(body, &h); h.LastReading != nil {
		t.Fatalf("no reading yet, last_reading must be null: %s", body)
	}
	now := time.Now().UTC().Truncate(time.Second)
	f.readings(f.a, f.a.dev.ID, row(now.Add(-time.Minute), 100, 1, 1), row(now.Add(-10*time.Second), 120.5, 1, 2))
	_, body, _ = f.do("GET", path, admin, nil)
	if err := json.Unmarshal(body, &h); err != nil || h.LastReading == nil || h.LastReading.LevelCm != 120.5 || h.LastReading.RawMM != 3000 {
		t.Fatalf("the newest reading must be returned: %s", body)
	}
}
