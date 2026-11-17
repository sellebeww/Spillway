package quality

import (
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/sellebeww/spillway/internal/domain"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

var cal = domain.Calibration{Version: 1, OffsetCm: 500, MinValidMM: 250, MaxValidMM: 4500, ValidFrom: t0.Add(-24 * time.Hour)}

// rd builds an on-time reading i minutes after t0.
func rd(i, raw int) domain.Reading {
	ts := t0.Add(time.Duration(i) * time.Minute)
	fc := uint32(i) //nolint:gosec // i is a small test index
	return domain.Reading{DeviceKey: "d", SID: "s", FC: fc, TS: ts, ReceivedAt: ts, RawMM: raw}
}

func hasFlag(r Result, f domain.QualityFlag) bool { return slices.Contains(r.Flags, f) }

func TestOutOfRangeBoundaries(t *testing.T) {
	cases := []struct {
		raw  int
		want bool
	}{{249, true}, {250, false}, {4500, false}, {4501, true}, {0, true}}
	for _, c := range cases {
		e := NewEvaluator(DefaultConfig())
		r := e.Evaluate(rd(0, c.raw), cal)
		if hasFlag(r, domain.FlagOutOfRange) != c.want {
			t.Errorf("raw=%d: flags=%v, want out-of-range=%v", c.raw, r.Flags, c.want)
		}
		if c.want && r.Score != 0 {
			t.Errorf("raw=%d: score=%v, want 0", c.raw, r.Score)
		}
	}
}

func TestOKReading(t *testing.T) {
	r := NewEvaluator(DefaultConfig()).Evaluate(rd(0, 2000), cal)
	if r.Score != 1 || !reflect.DeepEqual(r.Flags, []domain.QualityFlag{domain.FlagOK}) {
		t.Fatalf("got %+v", r)
	}
}

func TestLowBattery(t *testing.T) {
	v := func(x float64) *float64 { return &x }
	cases := []struct {
		name string
		batt *float64
		want bool
	}{{"below threshold", v(3.29), true}, {"at threshold", v(3.30), false}, {"healthy", v(4.0), false}, {"absent", nil, false}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := rd(0, 2000)
			in.BattV = c.batt
			r := NewEvaluator(DefaultConfig()).Evaluate(in, cal)
			if hasFlag(r, domain.FlagLowBattery) != c.want {
				t.Fatalf("flags=%v", r.Flags)
			}
			if c.want && r.Score != 0.9 {
				t.Fatalf("score=%v want 0.9", r.Score)
			}
		})
	}
}

func TestFlatlineExactlyAtN(t *testing.T) {
	e := NewEvaluator(DefaultConfig())
	for i := 0; i < 25; i++ {
		r := e.Evaluate(rd(i, 2000), cal)
		wantFlat := i >= 19 // the 20th identical reading is index 19
		if hasFlag(r, domain.FlagFlatline) != wantFlat {
			t.Fatalf("reading #%d: flags=%v, want flatline=%v", i+1, r.Flags, wantFlat)
		}
		if wantFlat && r.Score != 0.3 {
			t.Fatalf("score=%v want 0.3", r.Score)
		}
	}
}

func TestFlatlineResetsOnChange(t *testing.T) {
	e := NewEvaluator(DefaultConfig())
	i := 0
	for run := 0; run < 3; run++ {
		for k := 0; k < 19; k++ { // 19 identical, then one different: never 20
			if hasFlag(e.Evaluate(rd(i, 2000), cal), domain.FlagFlatline) {
				t.Fatalf("flatline at N=19 (i=%d)", i)
			}
			i++
		}
		e.Evaluate(rd(i, 2100), cal)
		i++
	}
}

func baseline(e *Evaluator, n int) int {
	for i := 0; i < n; i++ {
		e.Evaluate(rd(i, 2500+[]int{0, 2, -2, 1, -1}[i%5]), cal)
	}
	return n
}

func TestSpikeSingleJump150cm(t *testing.T) {
	e := NewEvaluator(DefaultConfig())
	n := baseline(e, 7)
	r := e.Evaluate(rd(n, 1000), cal) // 150 cm closer to the sensor
	if !hasFlag(r, domain.FlagSpike) || r.Score != 0.2 {
		t.Fatalf("got %+v", r)
	}
}

// The window deliberately includes flagged values (docs/QUALITY.md), so a persistent jump is flagged for
// the first 4 readings and accepted from the 5th (when 4 of the 7 window values are the new level).
// This is the honest limit of the design: five consecutive absurd but in-range readings pass as real.
func TestPersistentLevelShiftIsAcceptedOnceTheWindowAdapts(t *testing.T) {
	e := NewEvaluator(DefaultConfig())
	n := baseline(e, 7)
	var flagged []bool
	for i := 0; i < 8; i++ {
		flagged = append(flagged, hasFlag(e.Evaluate(rd(n+i, 1000), cal), domain.FlagSpike))
	}
	want := []bool{true, true, true, true, false, false, false, false}
	if !slices.Equal(flagged, want) {
		t.Fatalf("SPIKE flags for 8 consecutive readings at a new level: %v, want %v", flagged, want)
	}
}

func TestSpikeNeedsFiveHistoryValues(t *testing.T) {
	e := NewEvaluator(DefaultConfig())
	n := baseline(e, 4)
	if r := e.Evaluate(rd(n, 1000), cal); hasFlag(r, domain.FlagSpike) {
		t.Fatal("spike flagged with only 4 history values")
	}
	e = NewEvaluator(DefaultConfig())
	n = baseline(e, 5)
	if r := e.Evaluate(rd(n, 1000), cal); !hasFlag(r, domain.FlagSpike) {
		t.Fatal("spike not flagged with 5 history values")
	}
}

// A real, fast rise must never be treated as a spike (60 cm/h and faster).
func TestHampelIgnoresRealRapidRise(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cmPerStep  int
		stepMinute int
	}{{"60 cm/h at 1 min", 1, 1}, {"60 cm/h at 5 min", 5, 5}, {"60 cm/h at 10 min", 10, 10}, {"120 cm/h at 1 min", 2, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEvaluator(DefaultConfig())
			raw := 4000
			for i := 0; i < 40; i++ {
				r := e.Evaluate(rd(i*tc.stepMinute, raw), cal)
				if hasFlag(r, domain.FlagSpike) {
					t.Fatalf("reading %d (raw %d) wrongly flagged: %v", i, raw, r.Flags)
				}
				raw -= tc.cmPerStep * 10 // water rises, distance to sensor shrinks (mm)
			}
		})
	}
}

func TestHampelMADZeroDoesNotDivideByZero(t *testing.T) {
	cases := []struct {
		name string
		raw  int
		want bool
	}{
		{"1 cm step is within the MAD floor", 2510, false},
		{"2 cm still within 2.22 cm", 2520, false},
		{"3 cm exceeds the floor threshold of 2.22 cm", 2530, true},
		{"negative 3 cm also flagged", 2470, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := NewEvaluator(DefaultConfig())
			for i := 0; i < 6; i++ {
				e.Evaluate(rd(i, 2500), cal) // identical: MAD = 0 (flatline irrelevant below N=20)
			}
			r := e.Evaluate(rd(6, c.raw), cal)
			if hasFlag(r, domain.FlagSpike) != c.want {
				t.Fatalf("flags=%v want spike=%v", r.Flags, c.want)
			}
			if math.IsNaN(r.Score) {
				t.Fatal("NaN score")
			}
		})
	}
}

func TestOutOfRangeValueDoesNotPolluteHistory(t *testing.T) {
	e := NewEvaluator(DefaultConfig())
	baseline(e, 7)
	e.Evaluate(rd(7, 100), cal) // out of range, must not enter the window
	if r := e.Evaluate(rd(8, 2500), cal); hasFlag(r, domain.FlagSpike) || r.Score != 1 {
		t.Fatalf("got %+v", r)
	}
}

func TestClockSkewAndLate(t *testing.T) {
	at := func(sid string, ts, recv time.Time) domain.Reading {
		return domain.Reading{DeviceKey: "d", SID: sid, TS: ts, ReceivedAt: recv, RawMM: 2000}
	}
	now := t0.Add(time.Hour)
	cases := []struct {
		name      string
		steps     []domain.Reading
		wantFlags []domain.QualityFlag // flags of the LAST step
		wantScore float64
	}{
		{"future ts beyond 10 min", []domain.Reading{at("s", now.Add(11*time.Minute), now)}, []domain.QualityFlag{domain.FlagClockSkew}, 0.5},
		{"future ts within 10 min is fine", []domain.Reading{at("s", now.Add(9*time.Minute), now)}, []domain.QualityFlag{domain.FlagOK}, 1},
		{"3 min old on a synced session is LATE", []domain.Reading{at("s", now.Add(-3*time.Minute), now)}, []domain.QualityFlag{domain.FlagLate}, 1},
		{"exactly 2 min old is not late", []domain.Reading{at("s", now.Add(-2*time.Minute), now)}, []domain.QualityFlag{domain.FlagOK}, 1},
		{"1 h old after a synced first reading is LATE, not skew", []domain.Reading{at("s", now, now), at("s", now.Add(-time.Hour), now)}, []domain.QualityFlag{domain.FlagLate}, 1},
		{"1 h old as first reading of the session is skew", []domain.Reading{at("s", now.Add(-time.Hour), now)}, []domain.QualityFlag{domain.FlagClockSkew}, 0.5},
		{"skewed session stays skewed", []domain.Reading{at("s", now.Add(-time.Hour), now), at("s", now.Add(-59*time.Minute), now.Add(time.Minute))}, []domain.QualityFlag{domain.FlagClockSkew}, 0.5},
		{"new sid re-evaluates sync", []domain.Reading{at("a", now.Add(-time.Hour), now), at("b", now.Add(time.Minute), now.Add(time.Minute))}, []domain.QualityFlag{domain.FlagOK}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := NewEvaluator(DefaultConfig())
			var r Result
			for _, s := range c.steps {
				r = e.Evaluate(s, cal)
			}
			if !reflect.DeepEqual(r.Flags, c.wantFlags) || r.Score != c.wantScore {
				t.Fatalf("got %+v, want flags %v score %v", r, c.wantFlags, c.wantScore)
			}
		})
	}
}

func TestScoreIsMinimumOfComponents(t *testing.T) {
	b := 3.0
	in := domain.Reading{DeviceKey: "d", SID: "s", TS: t0.Add(20 * time.Minute), ReceivedAt: t0, RawMM: 2000, BattV: &b}
	r := NewEvaluator(DefaultConfig()).Evaluate(in, cal)
	want := []domain.QualityFlag{domain.FlagClockSkew, domain.FlagLowBattery}
	if !reflect.DeepEqual(r.Flags, want) || r.Score != 0.5 {
		t.Fatalf("got %+v", r)
	}
}

// history builds a mixed 160-reading sequence: noise, a spike, a flat stretch, a slow fall, a new session.
func history() []Input {
	var in []Input
	for i := 0; i < 160; i++ {
		var raw int
		switch {
		case i == 40:
			raw = 1200 // single spike
		case i >= 60 && i < 90:
			raw = 2800 // flat stretch (30 identical)
		case i >= 90:
			raw = 2800 - (i-90)*5
		default:
			raw = 3000 - i*2 + []int{0, 1, -1}[i%3]
		}
		r := rd(i, raw)
		if i >= 120 {
			r.SID = "s2"
		}
		if i%11 == 0 {
			b := 3.2
			r.BattV = &b
		}
		in = append(in, Input{Reading: r, Cal: cal})
	}
	return in
}

func TestRebuildMatchesLive(t *testing.T) {
	in := history()
	live := NewEvaluator(DefaultConfig())
	var liveRes []Result
	for _, x := range in {
		liveRes = append(liveRes, live.Evaluate(x.Reading, x.Cal))
	}

	for _, k := range []int{0, 1, 7, 45, 80, 125, 159} {
		rebuilt := Rebuild(DefaultConfig(), in[:k])
		var res []Result
		for _, x := range in[k:] {
			res = append(res, rebuilt.Evaluate(x.Reading, x.Cal))
		}
		if !reflect.DeepEqual(res, liveRes[k:]) {
			t.Fatalf("k=%d: results after rebuild differ from live", k)
		}
		if !reflect.DeepEqual(rebuilt, live) {
			t.Fatalf("k=%d: final state differs from live", k)
		}
	}
	// the scenario really exercised the flags
	seen := map[domain.QualityFlag]bool{}
	for _, r := range liveRes {
		for _, f := range r.Flags {
			seen[f] = true
		}
	}
	for _, f := range []domain.QualityFlag{domain.FlagSpike, domain.FlagFlatline, domain.FlagLowBattery} {
		if !seen[f] {
			t.Errorf("scenario never produced %s", f)
		}
	}
}

func TestHealthScore(t *testing.T) {
	ones := func(n int, v float64) []float64 {
		s := make([]float64, n)
		for i := range s {
			s[i] = v
		}
		return s
	}
	cases := []struct {
		name   string
		scores []float64
		want   float64
	}{
		{"all received, perfect", ones(60, 1), 1},
		{"half received, 0.8 quality", ones(30, 0.8), 0.4},
		{"over-delivery capped at ratio 1", ones(120, 1), 1},
		{"none", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HealthScore(c.scores, time.Minute, time.Hour); math.Abs(got-c.want) > 1e-12 {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
	if got := HealthScore(ones(3, 1), 0, time.Hour); math.IsNaN(got) || math.IsInf(got, 0) {
		t.Fatalf("zero interval produced %v", got)
	}
}

func TestBatterySlopePerDay(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	var s []BattSample
	s = append(s, BattSample{TS: now.Add(-30 * time.Hour), V: 0}) // outside the 24 h window: ignored
	for h := 24; h >= 0; h-- {
		s = append(s, BattSample{TS: now.Add(-time.Duration(h) * time.Hour), V: 4.0 - 0.1*float64(24-h)/24})
	}
	slope, ok := BatterySlopePerDay(s, now)
	if !ok || math.Abs(slope-(-0.1)) > 1e-9 {
		t.Fatalf("slope=%v ok=%v, want -0.1", slope, ok)
	}
	if _, ok := BatterySlopePerDay(s[:2], now); ok {
		t.Error("one sample inside the window must not be ok")
	}
	if _, ok := BatterySlopePerDay(nil, now); ok {
		t.Error("no samples must not be ok")
	}
}

func TestStatus(t *testing.T) {
	const iv = time.Minute
	cases := []struct {
		name     string
		interval time.Duration
		age      time.Duration
		want     domain.DeviceStatus
	}{
		{"fresh", iv, 30 * time.Second, domain.StatusOnline},
		{"exactly 2x", iv, 2 * time.Minute, domain.StatusOnline},
		{"just over 2x", iv, 2*time.Minute + time.Second, domain.StatusDelayed},
		{"offline floor is 15 min", iv, 15 * time.Minute, domain.StatusDelayed},
		{"just over 15 min", iv, 15*time.Minute + time.Second, domain.StatusOffline},
		{"10x beats 15 min for slow devices", 5 * time.Minute, 50 * time.Minute, domain.StatusDelayed},
		{"10x exceeded", 5 * time.Minute, 50*time.Minute + time.Second, domain.StatusOffline},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Status(t0, t0.Add(-c.age), c.interval); got != c.want {
				t.Fatalf("got %s want %s", got, c.want)
			}
		})
	}
	if Status(t0, time.Time{}, iv) != domain.StatusOffline {
		t.Error("never seen must be OFFLINE")
	}
}

// A reading that was measured long ago must show as stale even when it only just arrived
// (a buffered or late message): the arrival time alone must never make old data look current.
func TestIsStaleReadingJudgesTheOlderOfMeasurementAndArrival(t *testing.T) {
	iv := time.Minute // threshold 5 min
	for _, c := range []struct {
		name      string
		ts, recv  time.Duration // offsets from now (negative = past)
		wantStale bool
	}{
		{"fresh and just measured", -30 * time.Second, -29 * time.Second, false},
		{"measured 10 min ago, arrived just now (late burst)", -10 * time.Minute, 0, true},
		{"measured 4 min ago, arrived just now", -4 * time.Minute, 0, false},
		{"measured recently, but arrival is old", -time.Minute, -6 * time.Minute, true},
		{"device clock ahead: arrival decides", 2 * time.Hour, -6 * time.Minute, true},
		{"device clock ahead, fresh arrival", 2 * time.Hour, -time.Minute, false},
	} {
		if got := IsStaleReading(t0, t0.Add(c.ts), t0.Add(c.recv), iv); got != c.wantStale {
			t.Errorf("%s: stale=%v, want %v", c.name, got, c.wantStale)
		}
	}
}

func TestStaleAfter(t *testing.T) {
	if StaleAfter(time.Minute) != 5*time.Minute || StaleAfter(10*time.Minute) != 20*time.Minute {
		t.Fatal("stale threshold must be max(2 x interval, 5 min)")
	}
	if !IsStale(t0, t0.Add(-6*time.Minute), time.Minute) || IsStale(t0, t0.Add(-5*time.Minute), time.Minute) {
		t.Fatal("IsStale boundary wrong")
	}
}
