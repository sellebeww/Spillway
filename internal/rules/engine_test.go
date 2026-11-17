package rules

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/sellebeww/spillway/internal/domain"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const (
	W = domain.SeverityWaspada
	S = domain.SeveritySiaga
	A = domain.SeverityAwas
)

func sec(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }

// pt builds an on-time, full-quality point at second n.
func pt(n int, level float64) Point {
	return Point{TS: sec(n), ReceivedAt: sec(n), LevelCm: level, Score: 1}
}

func thrRule() domain.Rule {
	return domain.NewThresholdRule("thr", []domain.Tier{{Value: 100, Severity: W}, {Value: 150, Severity: S}, {Value: 200, Severity: A}})
}

func rorRule() domain.Rule {
	return domain.NewRateOfRiseRule("ror", []domain.Tier{{Value: 40, Severity: W}, {Value: 100, Severity: S}})
}

func newEngine(t *testing.T, rules ...domain.Rule) *Engine {
	t.Helper()
	e, err := NewEngine(rules, domain.DefaultMinQuality)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// feed processes levels one per minute starting at minute `from`, returning all decisions.
func feed(e *Engine, from int, levels ...float64) []Decision {
	var out []Decision
	for i, l := range levels {
		out = append(out, e.Process(pt((from+i)*60, l))...)
	}
	return out
}

type kv struct {
	Kind DecisionKind
	Sev  domain.Severity
}

func kinds(ds []Decision) []kv {
	var out []kv
	for _, d := range ds {
		out = append(out, kv{d.Kind, d.Severity})
	}
	return out
}

func repeat(v float64, n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = v
	}
	return s
}

func hasKind(ds []Decision, k DecisionKind) bool {
	for _, d := range ds {
		if d.Kind == k {
			return true
		}
	}
	return false
}

func TestNewEngineValidation(t *testing.T) {
	bad := domain.NewThresholdRule("x", []domain.Tier{{Value: 200, Severity: W}, {Value: 100, Severity: S}})
	if _, err := NewEngine([]domain.Rule{bad}, 0.5); err == nil {
		t.Error("invalid rule accepted")
	}
	if _, err := NewEngine([]domain.Rule{thrRule()}, 1.5); err == nil {
		t.Error("min quality > 1 accepted")
	}
}

func TestThresholdSingleSpikeDoesNotOpen(t *testing.T) {
	e := newEngine(t, thrRule())
	if ds := feed(e, 0, 10, 10, 10, 400, 10, 10, 10); len(ds) != 0 {
		t.Fatalf("unexpected decisions: %v", kinds(ds))
	}
}

func TestThresholdOpensThenEscalatesStepwise(t *testing.T) {
	e := newEngine(t, thrRule())
	ds := feed(e, 0, 90, 105, 110, 160, 165, 210, 215)
	want := []kv{{Opened, W}, {Escalated, S}, {Escalated, A}}
	if !reflect.DeepEqual(kinds(ds), want) {
		t.Fatalf("got %v want %v", kinds(ds), want)
	}
	if ds[0].LevelCm != 110 || ds[0].Previous != 0 || ds[1].Previous != W {
		t.Fatalf("decision details wrong: %+v", ds)
	}
}

func TestThresholdHysteresisPreventsFlapping(t *testing.T) {
	e := newEngine(t, thrRule())
	if ds := feed(e, 0, 90, 100, 101); !reflect.DeepEqual(kinds(ds), []kv{{Opened, W}}) {
		t.Fatalf("open: %v", kinds(ds))
	}
	// Oscillate around the tier (100) but never 3 consecutive below 95.
	if ds := feed(e, 3, 98, 102, 97, 103, 96, 101); len(ds) != 0 {
		t.Fatalf("flapped: %v", kinds(ds))
	}
	if ds := feed(e, 9, 94, 94); len(ds) != 0 {
		t.Fatalf("closed after only 2 low readings: %v", kinds(ds))
	}
	if ds := feed(e, 11, 94); !reflect.DeepEqual(kinds(ds), []kv{{Closed, 0}}) {
		t.Fatalf("3rd low reading must close: %v", kinds(ds))
	}
}

func TestThresholdDeescalateThenClose(t *testing.T) {
	e := newEngine(t, thrRule())
	ds := feed(e, 0, 90, 100, 101, 155, 160, 140, 140, 140, 80, 80, 80)
	want := []kv{{Opened, W}, {Escalated, S}, {Deescalated, W}, {Closed, 0}}
	if !reflect.DeepEqual(kinds(ds), want) {
		t.Fatalf("got %v want %v", kinds(ds), want)
	}
	if ds[2].Previous != S || ds[3].Previous != W {
		t.Fatalf("previous severities wrong: %+v", ds)
	}
}

func TestThresholdReopenAfterClose(t *testing.T) {
	e := newEngine(t, thrRule())
	ds := feed(e, 0, 100, 101, 80, 80, 80, 120, 120)
	want := []kv{{Opened, W}, {Closed, 0}, {Opened, W}}
	if !reflect.DeepEqual(kinds(ds), want) {
		t.Fatalf("got %v want %v", kinds(ds), want)
	}
}

func TestCooldownSuppressesRemindersButEscalationPasses(t *testing.T) {
	t.Run("reminder once per cooldown", func(t *testing.T) {
		e := newEngine(t, thrRule())
		ds := feed(e, 0, append([]float64{90, 100, 101}, repeat(110, 98)...)...) // minutes 0..100
		var reminders []time.Time
		for _, d := range ds {
			if d.Kind == Reminder {
				reminders = append(reminders, d.At)
			}
		}
		want := []time.Time{t0.Add(32 * time.Minute), t0.Add(62 * time.Minute), t0.Add(92 * time.Minute)}
		if !reflect.DeepEqual(reminders, want) {
			t.Fatalf("reminders at %v, want %v", reminders, want)
		}
		if ds[0].Kind != Opened || len(ds) != 4 {
			t.Fatalf("unexpected decisions: %v", kinds(ds))
		}
	})
	t.Run("escalation inside cooldown is still sent and restarts the timer", func(t *testing.T) {
		e := newEngine(t, thrRule())
		ds := feed(e, 0, append([]float64{90, 100, 101, 160, 160}, repeat(165, 60)...)...)
		if ds[1].Kind != Escalated || ds[1].At != t0.Add(4*time.Minute) {
			t.Fatalf("escalation not immediate: %+v", ds[1])
		}
		if ds[2].Kind != Reminder || ds[2].At != t0.Add(34*time.Minute) {
			t.Fatalf("first reminder must be 30 min after the escalation: %+v", ds[2])
		}
	})
	t.Run("no reminder inside cooldown", func(t *testing.T) {
		e := newEngine(t, thrRule())
		ds := feed(e, 0, append([]float64{90, 100, 101}, repeat(110, 25)...)...)
		if len(ds) != 1 {
			t.Fatalf("got %v", kinds(ds))
		}
	})
}

func TestLowQualityReadingsAreIgnored(t *testing.T) {
	e := newEngine(t, thrRule())
	var ds []Decision
	for i := 0; i < 3; i++ {
		p := pt(i*60, 300)
		p.Score = 0.4
		ds = append(ds, e.Process(p)...)
	}
	ds = append(ds, e.Process(pt(180, 300))...)
	if len(ds) != 0 {
		t.Fatalf("low-score readings influenced rules: %v", kinds(ds))
	}
	ds = e.Process(pt(240, 300))
	if !reflect.DeepEqual(kinds(ds), []kv{{Opened, A}}) {
		t.Fatalf("got %v", kinds(ds))
	}
	// Score exactly at the minimum is used.
	e2 := newEngine(t, thrRule())
	p1, p2 := pt(0, 300), pt(60, 300)
	p1.Score, p2.Score = 0.5, 0.5
	e2.Process(p1)
	if ds := e2.Process(p2); len(ds) != 1 {
		t.Fatalf("score == min_quality must count: %v", kinds(ds))
	}
}

func lateAt(ts, recv int, level float64) Point {
	return Point{TS: sec(ts), ReceivedAt: sec(recv), LevelCm: level, Score: 1}
}

func cloneEngine(e *Engine) *Engine {
	c := *e
	c.window = append([]Point(nil), e.window...)
	c.rules = make([]*ruleState, len(e.rules))
	for i, r := range e.rules {
		rc := *r
		rc.rates = append([]float64(nil), r.rates...)
		c.rules[i] = &rc
	}
	return &c
}

func TestLateDataPolicy(t *testing.T) {
	prime := func(e *Engine) {
		for i := 0; i <= 10; i++ { // valid points every minute up to second 600
			e.Process(pt(i*60, 10))
		}
	}
	t.Run("late and far from latest valid is ignored entirely", func(t *testing.T) {
		e := newEngine(t, thrRule())
		prime(e)
		snapshot := cloneEngine(e)
		if ds := e.Process(lateAt(100, 700, 500)); len(ds) != 0 {
			t.Fatalf("decisions: %v", kinds(ds))
		}
		if ds := e.Process(lateAt(110, 710, 500)); len(ds) != 0 {
			t.Fatalf("decisions: %v", kinds(ds))
		}
		if !reflect.DeepEqual(e, snapshot) {
			t.Fatal("ignored late data changed engine state")
		}
	})
	t.Run("late but within 5 minutes of latest valid is evaluated", func(t *testing.T) {
		e := newEngine(t, thrRule())
		prime(e)
		if ds := e.Process(lateAt(630, 800, 120)); len(ds) != 0 {
			t.Fatalf("one high point must not open: %v", kinds(ds))
		}
		ds := e.Process(lateAt(640, 810, 120))
		if !reflect.DeepEqual(kinds(ds), []kv{{Opened, W}}) {
			t.Fatalf("got %v", kinds(ds))
		}
	})
	t.Run("exactly 2 minutes old is not late", func(t *testing.T) {
		e := newEngine(t, thrRule())
		prime(e)
		e.Process(lateAt(130, 250, 500)) // 120 s old: not "late" (> 2 min), so it is evaluated normally
		if len(e.window) != 12 {
			t.Fatalf("on-time out-of-order point must enter the window, window=%d", len(e.window))
		}
	})
	t.Run("late point inserted in timestamp order", func(t *testing.T) {
		e := newEngine(t, thrRule())
		prime(e)
		e.Process(lateAt(590, 800, 15))
		for i := 1; i < len(e.window); i++ {
			if !e.window[i-1].TS.Before(e.window[i].TS) {
				t.Fatal("window not sorted")
			}
		}
	})
	t.Run("duplicate timestamp ignored", func(t *testing.T) {
		e := newEngine(t, thrRule())
		prime(e)
		n := len(e.window)
		e.Process(pt(600, 999))
		if len(e.window) != n {
			t.Fatal("duplicate ts grew window")
		}
	})
}

func TestRiseRateOnSyntheticSlopes(t *testing.T) {
	for _, slope := range []float64{-20, 0, 10, 30, 60, 120, 240} { // cm per hour
		for _, noise := range []float64{0, 0.2} {
			var pts []Point
			for i := 0; i < 11; i++ {
				n := noise
				if i%2 == 1 {
					n = -noise
				}
				pts = append(pts, pt(i*60, 100+slope*float64(i)/60+n))
			}
			got, ok := riseRate(pts)
			if !ok {
				t.Fatalf("slope %v: not ok", slope)
			}
			tol := 0.01 * math.Abs(slope)
			if slope == 0 {
				tol = 0.5 // alternating noise leaves a small residual slope
			}
			if math.Abs(got-slope) > tol {
				t.Errorf("slope %v noise %v: got %v", slope, noise, got)
			}
		}
	}
}

func TestRiseRateMinimumData(t *testing.T) {
	ramp := func(n, stepSec int) []Point {
		var p []Point
		for i := 0; i < n; i++ {
			p = append(p, pt(i*stepSec, float64(i)))
		}
		return p
	}
	cases := []struct {
		name string
		pts  []Point
		ok   bool
	}{
		{"3 points", ramp(3, 120), false},
		{"4 points spanning 3 min", ramp(4, 60), false},
		{"4 points spanning exactly 5 min", ramp(4, 100), true},
		{"4 points spanning 4m57s", ramp(4, 99), false},
		{"6 points over 5 min", ramp(6, 60), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := riseRate(c.pts); ok != c.ok {
				t.Fatalf("ok=%v want %v", ok, c.ok)
			}
		})
	}
}

func TestRateOfRiseOpensAndEscalates(t *testing.T) {
	e := newEngine(t, rorRule())
	// 60 cm/h: 1 cm per minute. First evaluable at the 6th point, confirmed by the 7th.
	var ds []Decision
	for i := 0; i < 6; i++ {
		ds = append(ds, e.Process(pt(i*60, float64(i)))...)
	}
	if len(ds) != 0 {
		t.Fatalf("opened before 2 confirming evaluations: %v", kinds(ds))
	}
	ds = e.Process(pt(6*60, 6))
	if len(ds) != 1 || ds[0].Kind != Opened || ds[0].Severity != W {
		t.Fatalf("got %v", kinds(ds))
	}
	if ds[0].RateCmPerHour == nil || math.Abs(*ds[0].RateCmPerHour-60) > 0.6 {
		t.Fatalf("rate = %v, want 60 +/- 1%%", ds[0].RateCmPerHour)
	}
	// Accelerate to 120 cm/h.
	level := 6.0
	for i := 7; i < 27; i++ {
		level += 2
		ds = append(ds, e.Process(pt(i*60, level))...)
	}
	if !reflect.DeepEqual(kinds(ds), []kv{{Opened, W}, {Escalated, S}}) {
		t.Fatalf("got %v", kinds(ds))
	}
	// Plateau: the rate decays, the alert steps down and finally closes.
	var tail []Decision
	for i := 27; i < 70; i++ {
		tail = append(tail, e.Process(pt(i*60, level))...)
	}
	if len(tail) == 0 || tail[len(tail)-1].Kind != Closed {
		t.Fatalf("expected the alert to close, got %v", kinds(tail))
	}
	if !hasKind(tail, Deescalated) || hasKind(tail, Opened) {
		t.Fatalf("expected a de-escalation and no re-open: %v", kinds(tail))
	}
}

func TestRateOfRiseInsufficientDataNeverOpens(t *testing.T) {
	e := newEngine(t, rorRule())
	// 4 points in 3 minutes at 200 cm/h: too short a span.
	var ds []Decision
	for i := 0; i < 4; i++ {
		ds = append(ds, e.Process(pt(i*60, float64(i)*200.0/60))...)
	}
	if len(ds) != 0 {
		t.Fatalf("got %v", kinds(ds))
	}
}

func TestRateOfRiseHysteresis20Percent(t *testing.T) {
	run := func(perMin float64) []Decision {
		e := newEngine(t, rorRule())
		level := 0.0
		for i := 0; i < 7; i++ { // open at 60 cm/h
			e.Process(pt(i*60, float64(i)))
			level = float64(i)
		}
		var out []Decision
		for i := 7; i < 32; i++ { // 25 minutes, below the 30 minute reminder cooldown
			level += perMin
			out = append(out, e.Process(pt(i*60, level))...)
		}
		return out
	}
	// 36 cm/h is below the 40 tier but above 0.8 x 40 = 32: stays open, silently.
	if ds := run(0.6); len(ds) != 0 {
		t.Fatalf("36 cm/h should hold the alert: %v", kinds(ds))
	}
	// 24 cm/h is below 32: closes.
	if ds := run(0.4); len(ds) != 1 || ds[0].Kind != Closed {
		t.Fatalf("24 cm/h should close: %v", kinds(ds))
	}
}

func TestRateOfRiseIgnoresLowQuality(t *testing.T) {
	e := newEngine(t, rorRule())
	var ds []Decision
	for i := 0; i < 20; i++ {
		p := pt(i*60, float64(i)*2)
		p.Score = 0.1
		ds = append(ds, e.Process(p)...)
	}
	if len(ds) != 0 || len(e.window) != 0 {
		t.Fatalf("low quality data used: %v window=%d", kinds(ds), len(e.window))
	}
}

// scenario: quiet, steep rise, plateau, recession, with some low-quality points.
func scenario() []Point {
	var ps []Point
	level := 10.0
	for i := 0; i < 200; i++ {
		switch {
		case i >= 30 && i < 100:
			level += 3
		case i >= 140:
			level -= 2
		}
		p := pt(i*60, level)
		if i%17 == 0 {
			p.Score = 0.2
		}
		ps = append(ps, p)
	}
	return ps
}

func TestRebuildMatchesLive(t *testing.T) {
	rules := []domain.Rule{thrRule(), rorRule()}
	pts := scenario()

	live, _ := NewEngine(rules, domain.DefaultMinQuality)
	var liveDs []Decision
	for _, p := range pts {
		liveDs = append(liveDs, live.Process(p)...)
	}
	if len(liveDs) < 5 {
		t.Fatalf("scenario too quiet: %v", kinds(liveDs))
	}

	for _, k := range []int{0, 5, 60, 95, 120, 150, 199} {
		rebuilt, err := Rebuild(rules, domain.DefaultMinQuality, pts[:k])
		if err != nil {
			t.Fatal(err)
		}
		var tail []Decision
		for _, p := range pts[k:] {
			tail = append(tail, rebuilt.Process(p)...)
		}
		var want []Decision
		for _, d := range liveDs {
			if !d.At.Before(pts[k].ReceivedAt) {
				want = append(want, d)
			}
		}
		if !reflect.DeepEqual(tail, want) {
			t.Fatalf("k=%d: decisions after rebuild differ\n got %v\nwant %v", k, kinds(tail), kinds(want))
		}
		if !reflect.DeepEqual(rebuilt, live) {
			t.Fatalf("k=%d: state differs from live", k)
		}
	}
	// Rebuilding while an alert is open must restore the open state (no duplicate Opened).
	mid, _ := Rebuild(rules, domain.DefaultMinQuality, pts[:95])
	if mid.rules[0].cur == 0 {
		t.Fatal("expected an open alert at k=95")
	}
}

func TestRemindOnlyWithFreshData(t *testing.T) {
	e := newEngine(t, thrRule())
	feed(e, 0, 90, 100, 101) // opens at minute 2, last reading arrived at minute 2
	if ds := e.Remind(t0.Add(10*time.Minute), 5*time.Minute); len(ds) != 0 {
		t.Fatalf("inside the cooldown: %v", kinds(ds))
	}
	if ds := e.Remind(t0.Add(33*time.Minute), 5*time.Minute); len(ds) != 0 {
		t.Fatalf("stale data (31 min old) must never produce a reminder: %v", kinds(ds))
	}
	if ds := e.Remind(t0.Add(33*time.Minute), time.Hour); len(ds) != 1 || ds[0].Kind != Reminder || ds[0].LevelCm != 101 {
		t.Fatalf("fresh data and cooldown elapsed: %+v", ds)
	}
	if ds := e.Remind(t0.Add(34*time.Minute), time.Hour); len(ds) != 0 {
		t.Fatal("a reminder restarts the cooldown")
	}
}

func TestAdoptContinuesAnAlertOlderThanTheReplayWindow(t *testing.T) {
	e := newEngine(t, thrRule())
	e.Adopt("thr", S, t0.Add(time.Hour))
	if sev, last := e.State("thr"); sev != S || !last.Equal(t0.Add(time.Hour)) {
		t.Fatalf("state %v %v", sev, last)
	}
	e.Adopt("thr", W, t0) // never replaces or moves the clock backwards
	if sev, last := e.State("thr"); sev != S || !last.Equal(t0.Add(time.Hour)) {
		t.Fatalf("adopt overwrote the state: %v %v", sev, last)
	}
	// With the severity adopted, three low readings close it instead of re-opening it.
	if ds := feed(e, 0, 50, 50, 50); !reflect.DeepEqual(kinds(ds), []kv{{Closed, 0}}) {
		t.Fatalf("got %v", kinds(ds))
	}
}
