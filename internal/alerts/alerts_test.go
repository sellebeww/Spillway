package alerts

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/goleak"

	"github.com/sellebeww/spillway/internal/auth"
	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/ingest"
	"github.com/sellebeww/spillway/internal/rules"
	"github.com/sellebeww/spillway/internal/simulate"
	"github.com/sellebeww/spillway/internal/store"
	"github.com/sellebeww/spillway/internal/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.Cleanup()
	os.Exit(code)
}

type clock struct{ ns atomic.Int64 }

func (c *clock) set(t time.Time) { c.ns.Store(t.UnixNano()) }
func (c *clock) now() time.Time  { return time.Unix(0, c.ns.Load()).UTC() }

func cipher() *auth.Cipher {
	c, _ := auth.NewCipher(1, bytes.Repeat([]byte{5}, 32), nil)
	return c
}

func tiers3() []domain.Tier {
	return []domain.Tier{{Value: 100, Severity: 1}, {Value: 150, Severity: 2}, {Value: 190, Severity: 3}}
}

func rorTiers() []domain.Tier {
	return []domain.Tier{{Value: 30, Severity: 1}, {Value: 60, Severity: 2}, {Value: 120, Severity: 3}}
}

func prometheusRegistry() *prometheus.Registry { return prometheus.NewRegistry() }

type env struct {
	db    *testutil.DB
	o     *store.OrgStore
	site  store.Site
	dev   store.Device
	sim   simulate.Device
	rules []store.AlertRule
	t0    time.Time
}

// newEnv creates an organization with one site, one calibrated device, a threshold rule (and
// optionally a rate-of-rise rule) and two subscribed Telegram channels ("a" and "b").
func newEnv(t *testing.T, withRoR bool) *env {
	t.Helper()
	db := testutil.SharedDB(t)
	// docs/SPEC.md 6.10: no leaked goroutines. Whatever the test starts (sink shards, ticker) must be gone at its end;
	// the shared pool and containers predate this snapshot.
	noLeaks := goleak.IgnoreCurrent()
	t.Cleanup(func() { goleak.VerifyNone(t, noLeaks) })
	ctx := context.Background()
	tag := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	org, err := db.Store.CreateOrg(ctx, "a-"+tag, "Alerts "+tag)
	if err != nil {
		t.Fatal(err)
	}
	o := db.Store.ForOrg(org.ID)
	site, err := o.CreateSite(ctx, store.SiteInput{Slug: "s-" + tag, Name: "Site " + tag, Lat: -6, Lon: 106, IsPublic: true})
	if err != nil {
		t.Fatal(err)
	}
	dev, secret, err := o.RegisterDevice(ctx, cipher(), store.DeviceInput{SiteID: site.ID, Key: "d-" + tag, Name: "Sensor " + tag})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.AddCalibration(ctx, dev.ID, domain.Calibration{OffsetCm: 500, ValidFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}, false); err != nil {
		t.Fatal(err)
	}
	sim, err := simulate.NewDevice(dev.Key, secret, 500)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{db: db, o: o, site: site, dev: dev, sim: sim, t0: time.Now().UTC().Add(-8 * time.Hour).Truncate(time.Minute)}
	r1, err := o.CreateRule(ctx, store.RuleInput{SiteID: site.ID, Kind: domain.KindThreshold, Tiers: tiers3(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	e.rules = append(e.rules, r1)
	if withRoR {
		r2, err := o.CreateRule(ctx, store.RuleInput{SiteID: site.ID, Kind: domain.KindRateOfRise, Tiers: rorTiers(), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		e.rules = append(e.rules, r2)
	}
	for _, c := range []string{"a", "b"} {
		ch, err := o.UpsertChannel(ctx, "telegram", c)
		if err != nil {
			t.Fatal(err)
		}
		if err := o.Subscribe(ctx, site.ID, ch.ID); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *env) newSink(clk *clock) *Sink {
	cfg := DefaultConfig()
	cfg.Shards, cfg.TickEvery = 2, time.Hour
	return New(e.db.Store, cfg, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})), clk.now, nil)
}

func (e *env) tickTime(tick int) time.Time { return e.t0.Add(time.Duration(tick) * time.Minute) }

// feed stores a reading (as ingest would have) and hands it to the sink, if sink != nil.
func (e *env) feed(t *testing.T, sink *Sink, tick int, level, score float64) {
	t.Helper()
	ts := e.tickTime(tick)
	_, err := e.o.InsertReadings(context.Background(), e.dev.ID, []store.ReadingRow{{TS: ts, ReceivedAt: ts, SID: "s", FC: uint32(tick + 1), //nolint:gosec // small
		RawMM: int((500 - level) * 10), LevelCm: level, Flags: []domain.QualityFlag{domain.FlagOK}, Score: score}})
	if err != nil {
		t.Fatal(err)
	}
	if sink != nil {
		sink.Handle(context.Background(), ingest.ReadingEvent{OrgID: e.o.OrgID(), SiteID: e.site.ID, DeviceID: e.dev.ID, DeviceKey: e.dev.Key,
			TS: ts, ReceivedAt: ts, LevelCm: level, Score: score})
	}
}

// sequence lists the notifications of channel "a" for a rule in outbox order, as KIND or KIND:seq.
func (e *env) sequence(t *testing.T, ruleID uuid.UUID) []string {
	t.Helper()
	rows, err := e.db.Pool.Query(context.Background(), `SELECT o.kind, o.seq FROM notifications_outbox o
		JOIN channels c ON c.id = o.channel_id JOIN alert_events a ON a.id = o.alert_event_id
		WHERE o.org_id = $1 AND c.chat_id = 'a' AND a.rule_id = $2 ORDER BY o.id`, e.o.OrgID(), ruleID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	reminders := 0
	for rows.Next() {
		var k string
		var seq int
		if err := rows.Scan(&k, &seq); err != nil {
			t.Fatal(err)
		}
		if k == "REMINDER" {
			reminders++
			k = fmt.Sprintf("%s:%d", k, reminders)
		}
		out = append(out, k)
	}
	return out
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.Pool.QueryRow(context.Background(), sql, append([]any{e.o.OrgID()}, args...)...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// oracle runs the pure engine over points and returns its decisions as sequence strings.
func oracle(t *testing.T, r store.AlertRule, minQuality float64, pts []rules.Point) (seq []string, ds []rules.Decision) {
	t.Helper()
	eng, err := rules.NewEngine([]domain.Rule{r.Domain()}, minQuality)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range pts {
		for _, d := range eng.Process(p) {
			ds = append(ds, d)
			k := string(d.Kind)
			if d.Kind == rules.Reminder {
				n++
				k = fmt.Sprintf("%s:%d", k, n)
			}
			seq = append(seq, k)
		}
	}
	return seq, ds
}

func wavePoints(e *env, total int) []rules.Point {
	var pts []rules.Point
	for tick := 0; tick < total; tick++ {
		ts := e.tickTime(tick)
		pts = append(pts, rules.Point{TS: ts, ReceivedAt: ts, LevelCm: simulate.Level(simulate.FloodWave, 0, tick, total), Score: 1})
	}
	return pts
}

func TestFloodWaveGivesTheFullAlertLifecycle(t *testing.T) {
	e := newEnv(t, false)
	const total = 300
	clk := &clock{}
	sink := e.newSink(clk)
	if err := sink.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for tick := 0; tick < total; tick++ {
		clk.set(e.tickTime(tick))
		e.feed(t, sink, tick, simulate.Level(simulate.FloodWave, 0, tick, total), 1)
	}
	if err := sink.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	want, decisions := oracle(t, e.rules[0], 0.5, wavePoints(e, total))
	got := e.sequence(t, e.rules[0].ID)
	if !slices.Equal(got, want) {
		t.Fatalf("notifications differ from the pure engine\n got %v\nwant %v", got, want)
	}
	count := func(prefix string) (n int) {
		for _, k := range got {
			if strings.HasPrefix(k, prefix) {
				n++
			}
		}
		return
	}
	if got[0] != "OPENED" || got[len(got)-1] != "CLOSED" || count("ESCALATED") < 2 || count("DEESCALATED") < 1 {
		t.Fatalf("expected OPENED -> ESCALATED x2+ -> ... -> DEESCALATED -> CLOSED, got %v", got)
	}
	var lastRem time.Time
	for _, d := range decisions {
		if d.Kind == rules.Reminder {
			if !lastRem.IsZero() && d.At.Sub(lastRem) < 30*time.Minute {
				t.Fatalf("reminders %v apart", d.At.Sub(lastRem))
			}
			lastRem = d.At
		}
	}
	if n := e.count(t, `SELECT count(*) FROM notifications_outbox WHERE org_id = $1`); n != 2*len(want) {
		t.Fatalf("%d outbox rows, want %d (every decision for both channels)", n, 2*len(want))
	}
	if n := e.count(t, `SELECT count(*) FROM alert_events WHERE org_id = $1 AND state = 'CLOSED' AND reminder_count = $2 AND peak_level > 195`, count("REMINDER")); n != 1 {
		t.Fatalf("exactly one closed event with %d reminders and a peak near 200, found %d", count("REMINDER"), n)
	}
	t.Logf("flood-wave, %d readings (1/min): %d notifications per channel = OPENED 1, ESCALATED %d, DEESCALATED %d, REMINDER %d, CLOSED 1; %d outbox rows for 2 channels; sequence %v",
		total, len(got), count("ESCALATED"), count("DEESCALATED"), count("REMINDER"), 2*len(got), got)
}

func TestRestartRebuildsTheSameStateAsLive(t *testing.T) {
	const total, cut = 300, 152 // the wave has just reached its plateau (alerts open) when the process dies
	live := newEnv(t, true)
	restarted := newEnv(t, true)

	clkL := &clock{}
	sinkL := live.newSink(clkL)
	if err := sinkL.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for tick := 0; tick < total; tick++ {
		clkL.set(live.tickTime(tick))
		live.feed(t, sinkL, tick, simulate.Level(simulate.FloodWave, 0, tick, total), 1)
	}
	_ = sinkL.Sync(t.Context())
	_ = sinkL.Close(t.Context())

	clkR := &clock{}
	sink1 := restarted.newSink(clkR)
	if err := sink1.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for tick := 0; tick < cut; tick++ {
		clkR.set(restarted.tickTime(tick))
		restarted.feed(t, sink1, tick, simulate.Level(simulate.FloodWave, 0, tick, total), 1)
	}
	_ = sink1.Sync(t.Context())
	_ = sink1.Close(t.Context()) // the process dies
	if n := restarted.count(t, `SELECT count(*) FROM alert_events WHERE org_id = $1 AND state = 'OPEN'`); n < 1 {
		t.Fatalf("expected an open alert at the cut, found %d", n)
	}
	sink2 := restarted.newSink(clkR) // the process restarts: no memory of anything
	if err := sink2.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for tick := cut; tick < total; tick++ {
		clkR.set(restarted.tickTime(tick))
		restarted.feed(t, sink2, tick, simulate.Level(simulate.FloodWave, 0, tick, total), 1)
	}
	_ = sink2.Sync(t.Context())
	_ = sink2.Close(t.Context())

	for i := range live.rules {
		a, b := live.sequence(t, live.rules[i].ID), restarted.sequence(t, restarted.rules[i].ID)
		if len(a) == 0 || !slices.Equal(a, b) {
			t.Fatalf("rule %s: the restarted run differs from the uninterrupted run\n live      %v\n restarted %v", live.rules[i].Kind, a, b)
		}
	}
	state := func(e *env) string {
		return fmt.Sprint(e.count(t, `SELECT count(*) FROM alert_events WHERE org_id = $1`), e.count(t, `SELECT coalesce(sum(reminder_count),0) FROM alert_events WHERE org_id = $1`),
			e.count(t, `SELECT count(*) FROM alert_events WHERE org_id = $1 AND state = 'CLOSED'`), e.count(t, `SELECT count(*) FROM notifications_outbox WHERE org_id = $1`))
	}
	if state(live) != state(restarted) {
		t.Fatalf("event/notification totals differ: live %s restarted %s", state(live), state(restarted))
	}
	t.Logf("restart at tick %d of %d: both rules produced identical notification sequences (threshold %v)", cut, total, live.sequence(t, live.rules[0].ID))
}

func TestDecisionLostBeforeCommitIsReconciledAfterRestart(t *testing.T) {
	const total = 300
	ref, crashed := newEnv(t, false), newEnv(t, false)
	want, ds := oracle(t, ref.rules[0], 0.5, wavePoints(ref, total))
	openTick := int(ds[0].TS.Sub(ref.t0) / time.Minute)

	clk := &clock{}
	s1 := crashed.newSink(clk)
	if err := s1.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for tick := 0; tick < openTick; tick++ {
		clk.set(crashed.tickTime(tick))
		crashed.feed(t, s1, tick, simulate.Level(simulate.FloodWave, 0, tick, total), 1)
	}
	_ = s1.Sync(t.Context())
	_ = s1.Close(t.Context())
	// The reading that triggers OPENED is committed, but the process dies before the decision is stored.
	clk.set(crashed.tickTime(openTick))
	crashed.feed(t, nil, openTick, simulate.Level(simulate.FloodWave, 0, openTick, total), 1)
	if crashed.count(t, `SELECT count(*) FROM alert_events WHERE org_id = $1`) != 0 {
		t.Fatal("setup: no alert should exist yet")
	}

	s2 := crashed.newSink(clk)
	if err := s2.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for tick := openTick + 1; tick < total; tick++ {
		clk.set(crashed.tickTime(tick))
		crashed.feed(t, s2, tick, simulate.Level(simulate.FloodWave, 0, tick, total), 1)
	}
	_ = s2.Sync(t.Context())
	_ = s2.Close(t.Context())
	kinds := func(seq []string) []string {
		var out []string
		for _, k := range seq {
			out = append(out, strings.SplitN(k, ":", 2)[0])
		}
		return out
	}
	got := crashed.sequence(t, crashed.rules[0].ID)
	if !slices.Equal(kinds(got), kinds(want)) {
		t.Fatalf("the missed OPENED must be emitted after the restart, and everything after it must follow\n got %v\nwant %v", got, want)
	}
	if got[0] != "OPENED" || crashed.count(t, `SELECT count(*) FROM alert_events WHERE org_id = $1`) != 1 {
		t.Fatalf("exactly one alert, opened once: %v", got)
	}
}

// outcome is what a scenario run left behind after going through the real ingest pipeline.
type outcome struct {
	rows, events, notifications, reminders int
	flags                                  map[string]int
	late                                   int
	scored                                 []rules.Point // stored readings as engine input WITHOUT quality gating (control run)
}

func pipelineRun(t *testing.T, e *env, total int, gen func(tick int) (level float64, skip bool, age time.Duration)) outcome {
	t.Helper()
	ctx := context.Background()
	clk := &clock{}
	sink := e.newSink(clk)
	if err := sink.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := ingest.DefaultConfig()
	cfg.Shards = 2
	pipe := ingest.New(cfg, ingest.Deps{Backend: ingest.StoreBackend{Store: e.db.Store, Cipher: cipher()}, Sink: sink,
		Metrics: ingest.NewMetrics(prometheusRegistry()), Now: clk.now}, nil)
	for tick := 0; tick < total; tick++ {
		clk.set(e.tickTime(tick))
		level, skip, age := gen(tick)
		if skip {
			continue
		}
		p := e.sim.Payload("sid1", uint32(tick+1), e.tickTime(tick).Add(-age), level, nil) //nolint:gosec // small
		if err := pipe.Submit(ctx, &ingest.Message{Payload: simulate.Body(p), TopicDevice: e.dev.Key, Source: "mqtt", ReceivedAt: e.tickTime(tick)}, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := pipe.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sink.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(ctx); err != nil {
		t.Fatal(err)
	}
	out := outcome{flags: map[string]int{}}
	rows, err := e.o.ReadingsForReplay(ctx, e.dev.ID, e.t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	out.rows = len(rows)
	for _, r := range rows {
		for _, f := range r.Flags {
			out.flags[f]++
		}
		if r.Late {
			out.late++
		}
		out.scored = append(out.scored, rules.Point{TS: r.TS, ReceivedAt: r.ReceivedAt, LevelCm: r.LevelCm, Score: 1})
	}
	out.events = e.count(t, `SELECT count(*) FROM alert_events WHERE org_id = $1`)
	for _, r := range e.rules {
		out.notifications += len(e.sequence(t, r.ID))
	}
	out.reminders = e.count(t, `SELECT count(*) FROM notifications_outbox WHERE org_id = $1 AND kind = 'REMINDER' AND channel_id = (SELECT id FROM channels WHERE org_id = $1 AND chat_id = 'a')`)
	return out
}

// unguarded feeds the same stored readings to the engines with every reading trusted (min quality 0).
func unguarded(t *testing.T, e *env, pts []rules.Point) (decisions, reminders int) {
	t.Helper()
	for _, r := range e.rules {
		seq, _ := oracle(t, r, 0, pts)
		for _, k := range seq {
			if strings.HasPrefix(k, "REMINDER") {
				reminders++
			}
		}
		decisions += len(seq)
	}
	return
}

func TestQualityScoringKeepsNoiseFromRaisingAlerts(t *testing.T) {
	const total = 200
	t.Run("spike-noise", func(t *testing.T) {
		e := newEnv(t, true)
		out := pipelineRun(t, e, total, func(tick int) (float64, bool, time.Duration) {
			return simulate.Level(simulate.SpikeNoise, 0, tick, total), false, 0
		})
		spikes := 0
		for tick := 0; tick < total; tick++ {
			if m := tick % 17; m == 5 || m == 6 {
				spikes++
			}
		}
		if out.rows != total || out.flags["SPIKE"] != spikes {
			t.Fatalf("rows=%d SPIKE flags=%d, want %d and %d: every absurd reading must be flagged", out.rows, out.flags["SPIKE"], total, spikes)
		}
		if out.events != 0 || out.notifications != 0 {
			t.Fatalf("FALSE ALERT: %d alert events and %d notifications from a calm river with echo spikes", out.events, out.notifications)
		}
		raw, _ := unguarded(t, e, out.scored)
		if raw == 0 {
			t.Fatal("the control run without quality gating must produce false alerts, otherwise this test proves nothing")
		}
		t.Logf("spike-noise: %d readings, %d flagged SPIKE (2 per burst), WITH quality gating 0 alerts / 0 notifications; WITHOUT it the same data gives %d false decisions", out.rows, spikes, raw)
	})
	t.Run("flatline at a normal level", func(t *testing.T) {
		e := newEnv(t, true)
		out := pipelineRun(t, e, total, func(tick int) (float64, bool, time.Duration) {
			return simulate.Level(simulate.Flatline, 0, tick, total), false, 0
		})
		if out.events != 0 || out.flags["FLATLINE"] < 100 {
			t.Fatalf("events=%d FLATLINE flags=%d (want 0 and >= 100)", out.events, out.flags["FLATLINE"])
		}
		t.Logf("flatline: %d readings, %d flagged FLATLINE from the 20th identical value, 0 alerts", out.rows, out.flags["FLATLINE"])
	})
	t.Run("sensor stuck high does not spam reminders", func(t *testing.T) {
		e := newEnv(t, false)
		out := pipelineRun(t, e, total, func(tick int) (float64, bool, time.Duration) {
			if tick < 40 {
				return simulate.Level(simulate.Steady, 0, tick, total), false, 0
			}
			return 185, false, 0 // frozen at a value above the SIAGA threshold
		})
		if out.flags["FLATLINE"] < 100 || out.events != 1 || out.notifications != 1 || out.reminders != 0 {
			t.Fatalf("FLATLINE=%d events=%d notifications=%d reminders=%d, want >=100/1/1/0", out.flags["FLATLINE"], out.events, out.notifications, out.reminders)
		}
		_, rem := unguarded(t, e, out.scored)
		if rem < 4 {
			t.Fatalf("control run without gating gives only %d reminders", rem)
		}
		t.Logf("stuck-high sensor over %d minutes: the first 19 identical readings can raise ONE alert (a limit of the design: flatline needs 20 values); after FLATLINE detection there are 0 reminders (1 notification in total). WITHOUT quality gating the same data sends %d reminders", total-40, rem)
	})
	t.Run("dropout and late-burst", func(t *testing.T) {
		e := newEnv(t, true)
		out := pipelineRun(t, e, total, func(tick int) (float64, bool, time.Duration) {
			return simulate.Level(simulate.Steady, 0, tick, total), simulate.Skip(simulate.Dropout, tick, total), 0
		})
		if out.rows != total-total/5 || out.events != 0 {
			t.Fatalf("dropout: rows=%d (want %d) events=%d", out.rows, total-total/5, out.events)
		}
		e2 := newEnv(t, true)
		out2 := pipelineRun(t, e2, total, func(tick int) (float64, bool, time.Duration) {
			return simulate.Level(simulate.Steady, 0, tick, total), false, simulate.Age(simulate.LateBurst, tick, total)
		})
		if out2.late != total/5 || out2.flags["LATE"] != total/5 || out2.events != 0 || out2.rows != total {
			t.Fatalf("late-burst: rows=%d late=%d LATE flags=%d events=%d, want %d/%d/%d/0", out2.rows, out2.late, out2.flags["LATE"], out2.events, total, total/5, total/5)
		}
		t.Logf("dropout: %d of %d readings arrived (gap of %d), late-burst: %d readings stored as late and flagged LATE; neither raised an alert", out.rows, total, total/5, out2.late)
	})
}
