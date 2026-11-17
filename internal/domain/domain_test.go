package domain_test

import (
	"testing"
	"time"

	"github.com/sellebeww/spillway/internal/domain"
)

func TestSeverityString(t *testing.T) {
	cases := map[domain.Severity]string{
		domain.SeverityNone:    "NONE",
		domain.SeverityWaspada: "WASPADA",
		domain.SeveritySiaga:   "SIAGA",
		domain.SeverityAwas:    "AWAS",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", int(s), got, want)
		}
	}
	if domain.Severity(9).Valid() || domain.SeverityNone.Valid() {
		t.Error("only 1..3 are valid severities")
	}
}

func TestRuleDefaults(t *testing.T) {
	r := domain.NewThresholdRule("r", []domain.Tier{{Value: 100, Severity: domain.SeverityWaspada}})
	if r.HysteresisCm != 5 || r.Cooldown != 30*time.Minute || r.Kind != domain.KindThreshold {
		t.Fatalf("unexpected defaults: %+v", r)
	}
	if domain.NewRateOfRiseRule("x", nil).Kind != domain.KindRateOfRise {
		t.Fatal("wrong kind")
	}
}

func TestRuleValidate(t *testing.T) {
	w, s := domain.SeverityWaspada, domain.SeveritySiaga
	cases := []struct {
		name    string
		rule    domain.Rule
		wantErr bool
	}{
		{"ok", domain.NewThresholdRule("r", []domain.Tier{{Value: 100, Severity: w}, {Value: 150, Severity: s}}), false},
		{"no tiers", domain.NewThresholdRule("r", nil), true},
		{"values not ascending", domain.NewThresholdRule("r", []domain.Tier{{Value: 150, Severity: w}, {Value: 100, Severity: s}}), true},
		{"equal values", domain.NewThresholdRule("r", []domain.Tier{{Value: 100, Severity: w}, {Value: 100, Severity: s}}), true},
		{"severity not ascending", domain.NewThresholdRule("r", []domain.Tier{{Value: 100, Severity: s}, {Value: 150, Severity: w}}), true},
		{"invalid severity", domain.NewThresholdRule("r", []domain.Tier{{Value: 100, Severity: 0}}), true},
		{"empty id", domain.NewThresholdRule("", []domain.Tier{{Value: 100, Severity: w}}), true},
		{"unknown kind", domain.Rule{ID: "r", Kind: "X", Tiers: []domain.Tier{{Value: 1, Severity: w}}}, true},
		{"negative hysteresis", domain.Rule{ID: "r", Kind: domain.KindThreshold, Tiers: []domain.Tier{{Value: 1, Severity: w}}, HysteresisCm: -1}, true},
		{"negative cooldown", domain.Rule{ID: "r", Kind: domain.KindThreshold, Tiers: []domain.Tier{{Value: 1, Severity: w}}, Cooldown: -1}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.rule.Validate(); (err != nil) != c.wantErr {
				t.Fatalf("Validate() err = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}
