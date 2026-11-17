package simulate

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/sellebeww/spillway/internal/ingest/protocol"
)

const testSecret = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func dev(t *testing.T, key string) Device {
	t.Helper()
	d, err := NewDevice(key, testSecret, 500)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestFloodWaveShape(t *testing.T) {
	const total = 1000
	base := Level(Steady, 0, 0, total)
	peakIdx, peak := 0, math.Inf(-1)
	for i := 0; i < total; i++ {
		if l := Level(FloodWave, 0, i, total); l > peak {
			peak, peakIdx = l, i
		}
	}
	if x := float64(peakIdx) / total; x < 0.45 || x > 0.65 {
		t.Errorf("peak at %.2f of the run, want between 0.45 and 0.65", x)
	}
	if math.Abs(peak-(base+140)) > 1 {
		t.Errorf("peak %.1f, want about %.1f", peak, base+140)
	}
	if first, last := Level(FloodWave, 0, 0, total), Level(FloodWave, 0, total-1, total); math.Abs(first-base) > 1 || math.Abs(last-base) > 1 {
		t.Errorf("wave must start and end at the base level: %.1f %.1f (base %.1f)", first, last, base)
	}
	if rise, fall := Level(FloodWave, 0, 400, total)-Level(FloodWave, 0, 250, total), Level(FloodWave, 0, 650, total)-Level(FloodWave, 0, 800, total); rise <= 0 || fall <= 0 {
		t.Errorf("rise %.1f and fall %.1f must both be positive", rise, fall)
	}
	for i := 0; i < total; i++ {
		if math.Abs(Level(Steady, 0, i, total)-base) > 0.5 {
			t.Fatalf("steady strays from its base at tick %d", i)
		}
	}
	if Level(FloodWave, 1, 300, total) == Level(FloodWave, 2, 300, total) {
		t.Error("devices must differ")
	}
}

func TestPayloadVerifiesAndRoundTrips(t *testing.T) {
	d := dev(t, "dev-001")
	batt := 3.7149
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := d.Payload("sid1", 7, ts, 123.4, &batt)
	if p.RawMM != 3766 || *p.BattV != 3.71 {
		t.Fatalf("raw=%d batt=%v", p.RawMM, *p.BattV)
	}
	back, err := protocol.Parse(Body(p))
	if err != nil {
		t.Fatalf("server parser rejects simulator output: %v", err)
	}
	raw, _ := hex.DecodeString(testSecret)
	if !protocol.Verify(raw, back) || back.FC != 7 || back.TS != ts.Unix() {
		t.Fatal("signature must verify after the JSON round trip")
	}
	if noBatt := d.Payload("sid1", 8, ts, 10, nil); noBatt.BattV != nil || !protocol.Verify(raw, noBatt) {
		t.Fatal("payload without battery")
	}
	if low := d.Payload("s", 1, ts, 9999, nil); low.RawMM != 0 {
		t.Fatalf("a level above the sensor clamps to 0, got %d", low.RawMM)
	}
}

type recorder struct {
	mu     sync.Mutex
	bodies map[string][][]byte
	failN  int
	calls  int
}

func (r *recorder) Send(_ context.Context, key string, body []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.failN > 0 && r.calls%r.failN == 0 {
		return errors.New("boom")
	}
	if r.bodies == nil {
		r.bodies = map[string][][]byte{}
	}
	r.bodies[key] = append(r.bodies[key], body)
	return nil
}

func TestRunSendsSignedMessagesAndReports(t *testing.T) {
	devs := []Device{dev(t, "a"), dev(t, "b"), dev(t, "c")}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := &recorder{}
	rep := Run(t.Context(), Config{Devices: devs, Scenario: FloodWave, Interval: time.Millisecond, Ticks: 5, Now: func() time.Time { return now }}, r)
	if rep.Sent != 15 || rep.Errors != 0 || rep.Rate() <= 0 {
		t.Fatalf("report: %+v", rep)
	}
	raw, _ := hex.DecodeString(testSecret)
	for _, d := range devs {
		var fcs []int
		for _, b := range r.bodies[d.Key] {
			p, err := protocol.Parse(b)
			if err != nil || !protocol.Verify(raw, p) || p.TS != now.Unix() {
				t.Fatalf("%s: bad message: %v", d.Key, err)
			}
			fcs = append(fcs, int(p.FC))
		}
		slices.Sort(fcs)
		if !slices.Equal(fcs, []int{1, 2, 3, 4, 5}) {
			t.Fatalf("%s frame counters: %v", d.Key, fcs)
		}
	}
}

func TestRunCountsErrors(t *testing.T) {
	r := &recorder{failN: 3}
	rep := Run(t.Context(), Config{Devices: []Device{dev(t, "a")}, Ticks: 9, Concurrency: 1}, r)
	if rep.Errors != 3 || rep.Sent != 6 || rep.FirstError == nil {
		t.Fatalf("report: %+v", rep)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if rep := Run(ctx, Config{Devices: []Device{dev(t, "a")}, Interval: time.Hour, Ticks: 100}, &recorder{}); rep.Sent > 1 {
		t.Fatalf("a cancelled run must stop at once: %+v", rep)
	}
}

func TestLoadDevices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.json")
	if err := os.WriteFile(path, []byte(`[{"device":"x1","secret":"`+testSecret+`"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	ds, err := LoadDevices(path)
	if err != nil || len(ds) != 1 || ds[0].OffsetCm != 500 {
		t.Fatalf("%+v %v", ds, err)
	}
	if err := os.WriteFile(path, []byte(`[{"device":"x1","secret":"zz"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDevices(path); err == nil {
		t.Fatal("a bad secret must be refused")
	}
	if _, err := LoadDevices(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("a missing file must be an error")
	}
}

func TestNewScenarios(t *testing.T) {
	const total = 200
	base := Level(Steady, 0, 0, total)
	spikes := 0
	for i := 0; i < total; i++ {
		l := Level(SpikeNoise, 0, i, total)
		if l > base+100 {
			spikes++
			if m := i % 17; m != 5 && m != 6 {
				t.Fatalf("spike at tick %d", i)
			}
		}
	}
	if spikes != 24 {
		t.Fatalf("spike-noise has %d absurd readings, want 24 (bursts of two)", spikes)
	}
	frozen := Level(Flatline, 0, total/5, total)
	for i := total / 5; i < total; i++ {
		if Level(Flatline, 0, i, total) != frozen {
			t.Fatalf("flatline moves at tick %d", i)
		}
	}
	if Level(Flatline, 0, 3, total) == Level(Flatline, 0, 4, total) {
		t.Fatal("flatline must vary before it freezes")
	}
	skipped, late := 0, 0
	for i := 0; i < total; i++ {
		if Skip(Dropout, i, total) {
			skipped++
		}
		if Age(LateBurst, i, total) == 10*time.Minute {
			late++
		}
		if Skip(Steady, i, total) || Age(Steady, i, total) != 0 {
			t.Fatal("other scenarios neither skip nor delay")
		}
	}
	if skipped != 40 || late != 40 {
		t.Fatalf("dropout skips %d, late-burst delays %d, want 40 each", skipped, late)
	}
	for _, n := range []string{"steady", "flood-wave", "spike-noise", "flatline", "dropout", "late-burst"} {
		if !ValidScenario(n) {
			t.Errorf("%s must be valid", n)
		}
	}
	if ValidScenario("tsunami") {
		t.Error("unknown scenario accepted")
	}
}

func TestFaultInjectionAccounting(t *testing.T) {
	devs := []Device{dev(t, "a"), dev(t, "b"), dev(t, "c"), dev(t, "d")}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r := &recorder{}
	const ticks = 200
	rep := Run(t.Context(), Config{Devices: devs, Ticks: ticks, Now: func() time.Time { return now }, Seed: 7,
		DuplicateRate: 0.1, LateRate: 0.1, OutOfOrderRate: 0.1}, r)
	if rep.Unique != int64(len(devs)*ticks) || rep.UniqueConfirmed != rep.Unique || rep.Errors != 0 {
		t.Fatalf("every distinct message must be generated once and confirmed: %+v", rep)
	}
	if rep.Sent != rep.Unique+rep.DuplicatesSent {
		t.Fatalf("transmissions %d != unique %d + duplicates %d", rep.Sent, rep.Unique, rep.DuplicatesSent)
	}
	if rep.DuplicatesSent == 0 || rep.Late == 0 || rep.OutOfOrder == 0 {
		t.Fatalf("faults were not injected: %+v", rep)
	}
	seen := map[string]int{}
	var lateSeen, reordered int
	for key, bodies := range r.bodies {
		prev := uint32(0)
		for _, b := range bodies {
			p, err := protocol.Parse(b)
			if err != nil {
				t.Fatal(err)
			}
			seen[fmt.Sprintf("%s/%d", key, p.FC)]++
			if age := now.Unix() - p.TS; age > 120 && age <= 7200 {
				lateSeen++
			} else if age != 0 {
				t.Fatalf("timestamp age %ds outside 3 to 120 minutes", age)
			}
			if p.FC < prev {
				reordered++
			}
			prev = max(prev, p.FC)
		}
	}
	if len(seen) != int(rep.Unique) {
		t.Fatalf("distinct (device, fc) = %d, want %d", len(seen), rep.Unique)
	}
	if lateSeen == 0 || reordered == 0 {
		t.Fatalf("late=%d reordered=%d on the wire", lateSeen, reordered)
	}
}

func TestFaultInjectionOffByDefault(t *testing.T) {
	r := &recorder{}
	rep := Run(t.Context(), Config{Devices: []Device{dev(t, "a")}, Ticks: 50, Concurrency: 1}, r)
	if rep.DuplicatesSent != 0 || rep.Late != 0 || rep.OutOfOrder != 0 || rep.Sent != 50 {
		t.Fatalf("report: %+v", rep)
	}
}

func TestReportJSONAndLatency(t *testing.T) {
	rep := Run(t.Context(), Config{Devices: []Device{dev(t, "a"), dev(t, "b")}, Ticks: 3}, &recorder{})
	js, err := rep.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(js, &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, js)
	}
	for _, k := range []string{"attempted", "confirmed", "errors", "unique", "unique_confirmed", "duplicates_sent", "late", "out_of_order", "elapsed_s", "rate_msg_per_s", "sender_latency_ms"} {
		if _, ok := got[k]; !ok {
			t.Errorf("report lacks %q", k)
		}
	}
	if got["attempted"].(float64) != 6 || got["confirmed"].(float64) != 6 {
		t.Errorf("counts: %v", got)
	}
	var h hist
	for range 99 {
		h.add(time.Millisecond)
	}
	h.add(500 * time.Millisecond)
	l := h.latency()
	if l.P50 < 0.9 || l.P50 > 1.3 || l.P99 > 1.3 || l.Max < 400 {
		t.Errorf("percentiles must be within one 1.2x bucket of the truth: %+v", l)
	}
}

func TestFailedDuplicateStillConfirmsWhenOneCopyArrives(t *testing.T) {
	r := &recorder{failN: 2} // every second transmission fails: of two copies exactly one arrives
	rep := Run(t.Context(), Config{Devices: []Device{dev(t, "a")}, Ticks: 10, Concurrency: 1, DuplicateRate: 1}, r)
	if rep.Unique != 10 || rep.UniqueConfirmed != 10 || rep.Errors != 10 || rep.Sent != 10 {
		t.Fatalf("report: %+v", rep)
	}
}
