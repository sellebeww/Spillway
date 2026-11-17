// Copyright (c) 2026 Vassel Goleyu. MIT License.

package simulate_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/rules"
	"github.com/sellebeww/spillway/internal/simulate"
)

// The expected ticks below were computed by hand from docs/SPEC.md section 5.4 with a separate
// program (docs/AUDIT.md, "Domain"), not by running internal/rules. They guard the engine against
// drifting from the written specification: flood-wave, device 0, 300 readings at 1 per minute.
func replay(t *testing.T, rule domain.Rule, quantise bool) []string {
	t.Helper()
	const total = 300
	eng, err := rules.NewEngine([]domain.Rule{rule}, domain.DefaultMinQuality)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var out []string
	for tick := 0; tick < total; tick++ {
		lvl := simulate.Level(simulate.FloodWave, 0, tick, total)
		if quantise { // what the real pipeline stores: raw_mm in whole mm against a 500 cm head
			lvl = 500 - math.Round((500-lvl)*10)/10
		}
		ts := t0.Add(time.Duration(tick) * time.Minute)
		for _, d := range eng.Process(rules.Point{TS: ts, ReceivedAt: ts, LevelCm: lvl, Score: 1}) {
			out = append(out, fmt.Sprintf("%d:%s", int(d.TS.Sub(t0).Minutes()), d.Kind))
		}
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestFloodWaveThresholdMatchesHandCalculation(t *testing.T) {
	rule := domain.NewThresholdRule("r", []domain.Tier{{Value: 100, Severity: 1}, {Value: 150, Severity: 2}, {Value: 190, Severity: 3}})
	eq(t, replay(t, rule, false), []string{"93:OPENED", "115:ESCALATED", "137:ESCALATED", "167:REMINDER", "197:REMINDER", "207:DEESCALATED", "234:DEESCALATED", "263:CLOSED"})
	// Rounding to 0.1 cm (the stored precision) moves three ticks by one minute and changes nothing else.
	eq(t, replay(t, rule, true), []string{"93:OPENED", "115:ESCALATED", "136:ESCALATED", "166:REMINDER", "196:REMINDER", "207:DEESCALATED", "234:DEESCALATED", "263:CLOSED"})
}

func TestFloodWaveRateOfRiseMatchesHandCalculation(t *testing.T) {
	rule := domain.NewRateOfRiseRule("r", []domain.Tier{{Value: 30, Severity: 1}, {Value: 60, Severity: 2}, {Value: 120, Severity: 3}})
	// Hand-computed: the OLS rate peaks at 141.5 cm/h (tick 117) and confirms the tiers at ticks 73, 77, 95; the
	// reminder is the 30 minute cooldown after tick 95; the rate then falls below 80 percent of each tier (20 percent
	// hysteresis, 2 consecutive evaluations) at ticks 138, 147 and 152.
	eq(t, replay(t, rule, false), []string{"73:OPENED", "77:ESCALATED", "95:ESCALATED", "125:REMINDER", "138:DEESCALATED", "147:DEESCALATED", "152:CLOSED"})
}
