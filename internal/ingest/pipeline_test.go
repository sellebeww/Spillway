package ingest

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/goleak"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/simulate"
	"github.com/sellebeww/spillway/internal/store"
)

const testSecretHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

var errTransient = errors.New("connection reset by peer")

type fakeBackend struct {
	mu         sync.Mutex
	devs       map[string]DeviceInfo
	cals       map[uuid.UUID][]domain.Calibration
	hist       []store.StoredReading
	rows       map[store.ReadingKey]store.DeviceReading
	order      []store.ReadingKey
	calls      []int
	failN      int           // fail this many Insert calls with a transient error
	gate       chan struct{} // when non-nil Insert blocks until it is closed (or the context ends)
	badFC      uint32        // Insert answers ErrInvalid for any batch containing this frame counter
	touches    int
	lookupFail int // fail this many LookupDevice calls with a transient error
}

func (b *fakeBackend) LookupDevice(_ context.Context, key string) (DeviceInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lookupFail > 0 {
		b.lookupFail--
		return DeviceInfo{}, errTransient
	}
	if d, ok := b.devs[key]; ok {
		return d, nil
	}
	return DeviceInfo{}, ErrUnknownDevice
}

func (b *fakeBackend) Calibrations(_ context.Context, d DeviceInfo) ([]domain.Calibration, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cals[d.ID], nil
}

func (b *fakeBackend) History(context.Context, DeviceInfo, int) ([]store.StoredReading, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hist, nil
}

func (b *fakeBackend) Insert(ctx context.Context, _ uuid.UUID, rows []store.DeviceReading) ([]store.ReadingKey, error) {
	if b.gate != nil {
		select {
		case <-b.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, len(rows))
	if b.failN > 0 {
		b.failN--
		return nil, errTransient
	}
	for _, r := range rows {
		if b.badFC != 0 && r.FC == b.badFC {
			return nil, store.ErrInvalid
		}
	}
	var keys []store.ReadingKey
	for _, r := range rows {
		k := store.ReadingKey{DeviceID: r.DeviceID, TS: r.TS, SID: r.SID, FC: r.FC}
		if _, dup := b.rows[k]; dup {
			continue
		}
		b.rows[k] = r
		b.order = append(b.order, k)
		keys = append(keys, k)
	}
	return keys, nil
}

func (b *fakeBackend) Touch(context.Context, DeviceInfo, time.Time) error {
	b.mu.Lock()
	b.touches++
	b.mu.Unlock()
	return nil
}

func (b *fakeBackend) stored() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.rows)
}

type fakeDedupe struct {
	mu     sync.Mutex
	seen   map[string]bool
	err    error
	marked []string
}

func (f *fakeDedupe) Seen(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[key], f.err
}

func (f *fakeDedupe) Mark(_ context.Context, keys []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marked = append(f.marked, keys...)
	return nil
}

type rig struct {
	t    *testing.T
	be   *fakeBackend
	p    *Pipeline
	reg  *prometheus.Registry
	devs []simulate.Device
	org  uuid.UUID

	mu       sync.Mutex
	outcomes []Outcome
}

func testConfig() Config {
	c := DefaultConfig()
	c.Shards, c.QueueSize, c.WriterQueue, c.BatchSize, c.BatchInterval = 4, 256, 512, 50, 5*time.Millisecond
	return c
}

func newRig(t *testing.T, nDev int, cfg Config, mut func(*fakeBackend), dd Dedupe) *rig {
	t.Helper()
	be := &fakeBackend{devs: map[string]DeviceInfo{}, cals: map[uuid.UUID][]domain.Calibration{}, rows: map[store.ReadingKey]store.DeviceReading{}}
	raw, _ := hex.DecodeString(testSecretHex)
	r := &rig{t: t, be: be, reg: prometheus.NewRegistry(), org: uuid.New()}
	for i := 0; i < nDev; i++ {
		key := fmt.Sprintf("dev-%02d", i)
		d, err := simulate.NewDevice(key, testSecretHex, 500)
		if err != nil {
			t.Fatal(err)
		}
		r.devs = append(r.devs, d)
		info := DeviceInfo{ID: uuid.New(), OrgID: r.org, SiteID: uuid.New(), Key: key, Secret: raw, Interval: time.Minute, LowBattV: 3.3}
		be.devs[key] = info
		be.cals[info.ID] = []domain.Calibration{{Version: 1, OffsetCm: 500, MinValidMM: 250, MaxValidMM: 4500, ValidFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}}
	}
	if mut != nil {
		mut(be)
	}
	r.p = New(cfg, Deps{Backend: be, Dedupe: dd, Metrics: NewMetrics(r.reg), Now: func() time.Time { return t0 }}, r.reg)
	return r
}

func (r *rig) done(o Outcome) {
	r.mu.Lock()
	r.outcomes = append(r.outcomes, o)
	r.mu.Unlock()
}

func (r *rig) count(k OutcomeKind, reason string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, o := range r.outcomes {
		if o.Kind == k && (reason == "" || o.Reason == reason) {
			n++
		}
	}
	return n
}

func (r *rig) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.outcomes)
}

// msg builds a signed message for device index i. ts is relative to the fixed pipeline clock.
func (r *rig) msg(i int, sid string, fc uint32, age time.Duration, raw int) *Message {
	d := r.devs[i]
	level := 500 - float64(raw)/10
	p := d.Payload(sid, fc, t0.Add(-age), level, nil)
	return &Message{Payload: simulate.Body(p), TopicDevice: d.Key, Source: "mqtt", Done: r.done}
}

func (r *rig) submit(m *Message) error { return r.p.Submit(context.Background(), m, true) }

func (r *rig) close() {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.p.Close(ctx); err != nil {
		r.t.Fatalf("close: %v", err)
	}
}

func metric(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	fams, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fams {
		if f.GetName() != name {
			continue
		}
	next:
		for _, m := range f.GetMetric() {
			for _, lp := range m.GetLabel() {
				if want, ok := labels[lp.GetName()]; ok && want != lp.GetValue() {
					continue next
				}
			}
			if len(m.GetLabel()) != len(labels) {
				continue
			}
			switch {
			case m.Counter != nil:
				return m.GetCounter().GetValue()
			case m.Gauge != nil:
				return m.GetGauge().GetValue()
			case m.Histogram != nil:
				return float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	return 0
}

func TestPerDeviceOrderIsPreservedUnderConcurrency(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	r := newRig(t, 8, testConfig(), nil, nil)
	var wg sync.WaitGroup
	for i := range r.devs { // one producer per device, like one sensor per connection
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fc := uint32(1); fc <= 250; fc++ {
				if err := r.submit(r.msg(i, "s", fc, time.Duration(300-fc)*100*time.Millisecond, 2000+int(fc))); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	r.close()

	if r.be.stored() != 2000 || r.count(Stored, "") != 2000 {
		t.Fatalf("stored %d rows, %d Stored outcomes, want 2000", r.be.stored(), r.count(Stored, ""))
	}
	last := map[uuid.UUID]uint32{}
	for _, k := range r.be.order {
		if k.FC <= last[k.DeviceID] {
			t.Fatalf("device %v: frame %d committed after %d (order broken)", k.DeviceID, k.FC, last[k.DeviceID])
		}
		last[k.DeviceID] = k.FC
	}
	if v := metric(t, r.reg, "spillway_ingest_stored_total", nil); v != 2000 {
		t.Fatalf("stored metric %v", v)
	}
	if v := metric(t, r.reg, "spillway_ingest_received_total", map[string]string{"source": "mqtt"}); v != 2000 {
		t.Fatalf("received metric %v", v)
	}
	if v := metric(t, r.reg, "spillway_ingest_batch_size", nil); v == 0 {
		t.Fatal("batch size histogram did not move")
	}
	if v := metric(t, r.reg, "spillway_ingest_e2e_seconds", nil); v != 2000 {
		t.Fatalf("e2e histogram samples %v", v)
	}
}

func TestCloseDrainsEverythingWithoutLoss(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	cfg := testConfig()
	cfg.BatchSize, cfg.BatchInterval = 500, time.Hour // only the final drain flush can write the tail
	r := newRig(t, 20, cfg, nil, nil)
	var wg sync.WaitGroup
	for i := range r.devs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fc := uint32(1); fc <= 100; fc++ {
				_ = r.submit(r.msg(i, "s", fc, time.Duration(fc)*time.Second/2, 2000+int(fc)))
			}
		}()
	}
	wg.Wait()
	r.close() // immediately: everything still queued or batched must be written
	if r.be.stored() != 2000 || r.count(Stored, "") != 2000 {
		t.Fatalf("lost data on shutdown: stored %d, outcomes %d of 2000", r.be.stored(), r.count(Stored, ""))
	}
	if err := r.submit(r.msg(0, "s", 999, 0, 2000)); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after close: %v", err)
	}
	if err := r.p.Close(context.Background()); err != nil {
		t.Fatalf("a second Close must be a harmless no-op: %v", err)
	}
}

func TestBackpressureNeverDropsSilently(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	cfg := testConfig()
	cfg.Shards, cfg.QueueSize, cfg.WriterQueue, cfg.BatchSize = 1, 2, 1, 1
	gate := make(chan struct{})
	r := newRig(t, 1, cfg, func(b *fakeBackend) { b.gate = gate }, nil)

	accepted, fc := 0, uint32(0)
	for {
		fc++
		err := r.p.Submit(context.Background(), r.msg(0, "s", fc, time.Second, 2000+int(fc)), false)
		if errors.Is(err, ErrBacklog) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		accepted++
		if accepted > 100 {
			t.Fatal("the queue never filled: it is not bounded")
		}
		time.Sleep(time.Millisecond) // let the worker move items along so the bound is the real capacity
	}
	if accepted < 2 {
		t.Fatalf("only %d messages accepted before backlog", accepted)
	}

	blocked := make(chan error, 1)
	go func() { blocked <- r.p.Submit(context.Background(), r.msg(0, "s", fc+1, time.Second, 2500), true) }()
	select {
	case err := <-blocked:
		t.Fatalf("a blocking submit must wait while the queue is full, returned %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if v := metric(t, r.reg, "spillway_ingest_backpressure_waits_total", nil); v < 1 {
		t.Fatal("backpressure was not counted")
	}
	if depth := metric(t, r.reg, "spillway_ingest_queue_depth", nil); depth < 1 {
		t.Fatalf("queue depth gauge reads %v while the queue is full", depth)
	}
	close(gate)
	if err := <-blocked; err != nil {
		t.Fatalf("blocked submit after drain: %v", err)
	}
	r.close()
	if want := accepted + 1; r.be.stored() != want || r.count(Stored, "") != want {
		t.Fatalf("accepted %d messages but stored %d (outcomes %d)", want, r.be.stored(), r.count(Stored, ""))
	}
}

func TestHardShutdownLeavesUnfinishedMessagesFailed(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	cfg := testConfig()
	cfg.Shards, cfg.QueueSize, cfg.WriterQueue, cfg.BatchSize = 1, 8, 4, 2
	r := newRig(t, 1, cfg, func(b *fakeBackend) { b.gate = make(chan struct{}) }, nil) // the database never answers
	submitted := 0
	for fc := uint32(1); fc <= 10; fc++ {
		if err := r.p.Submit(context.Background(), r.msg(0, "s", fc, time.Second, 2000+int(fc)), false); err == nil {
			submitted++
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := r.p.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close must report the deadline: %v", err)
	}
	if r.total() != submitted || r.count(Failed, "") != submitted {
		t.Fatalf("%d messages submitted, %d outcomes, %d failed: every message needs exactly one Failed outcome so MQTT leaves it unacknowledged",
			submitted, r.total(), r.count(Failed, ""))
	}
	if r.be.stored() != 0 {
		t.Fatal("nothing may be stored when the database never answered")
	}
}

func TestTransientDatabaseErrorsAreRetriedWithoutLoss(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	r := newRig(t, 2, testConfig(), func(b *fakeBackend) { b.failN = 3 }, nil)
	for fc := uint32(1); fc <= 40; fc++ {
		_ = r.submit(r.msg(int(fc)%2, "s", fc, time.Second, 2000+int(fc)))
	}
	r.close()
	if r.be.stored() != 40 || r.count(Stored, "") != 40 {
		t.Fatalf("stored %d outcomes %d, want 40", r.be.stored(), r.count(Stored, ""))
	}
	if v := metric(t, r.reg, "spillway_ingest_db_errors_total", nil); v != 3 {
		t.Fatalf("db error metric %v, want 3", v)
	}
}

func TestPoisonRowDoesNotBlockItsBatch(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	cfg := testConfig()
	cfg.BatchSize, cfg.BatchInterval = 100, time.Hour
	r := newRig(t, 1, cfg, func(b *fakeBackend) { b.badFC = 666 }, nil)
	for fc := uint32(660); fc <= 670; fc++ {
		_ = r.submit(r.msg(0, "s", fc, time.Second, 2000+int(fc)))
	}
	r.close()
	if r.count(Stored, "") != 10 || r.count(Rejected, ReasonDBInvalid) != 1 || r.be.stored() != 10 {
		t.Fatalf("stored=%d rejected(db_invalid)=%d rows=%d, want 10/1/10", r.count(Stored, ""), r.count(Rejected, ReasonDBInvalid), r.be.stored())
	}
}

func TestRejectionsAndDuplicates(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	r := newRig(t, 3, testConfig(), func(b *fakeBackend) {
		b.cals[b.devs["dev-02"].ID] = nil // dev-02 has no calibration yet
	}, nil)

	good := r.msg(0, "s", 1, time.Second, 2000)
	badSig := r.msg(0, "s", 2, time.Second, 2000)
	badSig.Payload = []byte(string(badSig.Payload[:len(badSig.Payload)-10]) + "ffffffff\"}") // same length, wrong signature
	unknown := r.msg(0, "s", 3, time.Second, 2000)
	unknown.TopicDevice = ""
	unknown.Payload = []byte(`{"device":"ghost-1","sid":"s","fc":3,"ts":1780000000,"raw_mm":2000,"sig":"` + string(make([]byte, 0)) + `0000000000000000000000000000000000000000000000000000000000000000"}`)
	garbage := &Message{Payload: []byte("not json"), Source: "mqtt", Done: r.done}
	wrongTopic := r.msg(0, "s", 4, time.Second, 2000)
	wrongTopic.TopicDevice = "dev-01"
	tooOld := r.msg(0, "s", 5, 25*time.Hour, 2000)
	noCal := r.msg(2, "s", 6, time.Second, 2000)
	dup := r.msg(0, "s", 1, time.Second, 2000) // same (sid, fc) as good

	for _, m := range []*Message{good, badSig, unknown, garbage, wrongTopic, tooOld, noCal, dup} {
		if err := r.submit(m); err != nil {
			t.Fatal(err)
		}
	}
	r.close()

	want := map[string]int{ReasonSig: 1, ReasonUnknownDevice: 1, ReasonSchema: 1, ReasonTopicMismatch: 1, ReasonTooOld: 1, ReasonNoCalibration: 1}
	for reason, n := range want {
		if got := r.count(Rejected, reason); got != n {
			t.Errorf("rejected %s: %d, want %d", reason, got, n)
		}
		if v := metric(t, r.reg, "spillway_ingest_rejected_total", map[string]string{"reason": reason}); v != float64(n) {
			t.Errorf("metric rejected{%s} = %v, want %d", reason, v, n)
		}
	}
	if r.count(Stored, "") != 1 || r.count(Duplicate, "") != 1 || r.be.stored() != 1 {
		t.Fatalf("stored=%d duplicates=%d rows=%d, want 1/1/1", r.count(Stored, ""), r.count(Duplicate, ""), r.be.stored())
	}
	if v := metric(t, r.reg, "spillway_ingest_duplicates_total", map[string]string{"layer": "memory"}); v != 1 {
		t.Fatalf("memory duplicate metric %v", v)
	}
}

func TestRedisLayerAndFailOpen(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	r0 := newRig(t, 1, testConfig(), nil, nil)
	seenKey := "ik:dev-00:s:7"
	dd := &fakeDedupe{seen: map[string]bool{seenKey: true}}
	r := newRig(t, 1, testConfig(), nil, dd)
	_ = r0.p.Close(context.Background())

	_ = r.submit(r.msg(0, "s", 7, time.Second, 2000)) // Redis says: committed before
	_ = r.submit(r.msg(0, "s", 8, time.Second, 2000)) // new
	r.close()
	if r.count(Duplicate, "") != 1 || r.count(Stored, "") != 1 || r.be.stored() != 1 {
		t.Fatalf("outcomes: dup=%d stored=%d rows=%d", r.count(Duplicate, ""), r.count(Stored, ""), r.be.stored())
	}
	if v := metric(t, r.reg, "spillway_ingest_duplicates_total", map[string]string{"layer": "redis"}); v != 1 {
		t.Fatalf("redis duplicate metric %v", v)
	}
	if !slices.Equal(dd.marked, []string{"ik:dev-00:s:8"}) {
		t.Fatalf("keys are marked only after commit, and only the committed ones: %v", dd.marked)
	}

	broken := &fakeDedupe{err: errors.New("redis down")}
	r2 := newRig(t, 1, testConfig(), nil, broken)
	for fc := uint32(1); fc <= 5; fc++ {
		_ = r2.submit(r2.msg(0, "s", fc, time.Second, 2000+int(fc)))
	}
	r2.close()
	if r2.count(Stored, "") != 5 {
		t.Fatalf("Redis errors must fail open: stored %d of 5", r2.count(Stored, ""))
	}
	if v := metric(t, r2.reg, "spillway_ingest_redis_errors_total", nil); v != 5 {
		t.Fatalf("redis error metric %v", v)
	}
}

func TestLateAndClockSkewFlags(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	r := newRig(t, 1, testConfig(), nil, nil)
	_ = r.submit(r.msg(0, "s", 1, time.Second, 2000))     // normal: sets the session as in sync
	_ = r.submit(r.msg(0, "s", 2, 10*time.Minute, 2010))  // late
	_ = r.submit(r.msg(0, "s", 3, -20*time.Minute, 2020)) // timestamp 20 minutes in the future
	r.close()
	byFC := map[uint32]store.DeviceReading{}
	for _, row := range r.be.rows {
		byFC[row.FC] = row
	}
	if byFC[1].Late || len(byFC[1].Flags) != 1 || byFC[1].Flags[0] != domain.FlagOK {
		t.Errorf("normal reading: %+v", byFC[1])
	}
	if !byFC[2].Late || !slices.Contains(byFC[2].Flags, domain.FlagLate) || byFC[2].Score != 1 {
		t.Errorf("late reading: %+v", byFC[2])
	}
	if !slices.Contains(byFC[3].Flags, domain.FlagClockSkew) || byFC[3].Score != 0.5 {
		t.Errorf("skewed reading: %+v", byFC[3])
	}
	if got := byFC[1].LevelCm; got < 299.9 || got > 300.1 {
		t.Errorf("level for raw 2000 and offset 500 should be 300, got %v", got)
	}
	if v := metric(t, r.reg, "spillway_ingest_late_total", nil); v != 1 {
		t.Errorf("late metric %v", v)
	}
	if v := metric(t, r.reg, "spillway_ingest_quality_flags_total", map[string]string{"flag": "CLOCK_SKEW"}); v != 1 {
		t.Errorf("flag metric %v", v)
	}
}

func TestQualityStateIsRebuiltFromHistory(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	r := newRig(t, 1, testConfig(), func(b *fakeBackend) {
		for fc := int64(1); fc <= 19; fc++ { // 19 identical readings already stored before the "restart"
			ts := t0.Add(-time.Duration(40-fc) * time.Second)
			b.hist = append(b.hist, store.StoredReading{TS: ts, ReceivedAt: ts, SID: "s", FC: fc, RawMM: 2000})
		}
	}, nil)
	_ = r.submit(r.msg(0, "s", 20, time.Second, 2000)) // the 20th identical value: flatline, as it would have been live
	_ = r.submit(r.msg(0, "s", 5, time.Second, 2000))  // redelivered after the restart: known from history
	r.close()
	if r.count(Duplicate, "") != 1 || r.count(Stored, "") != 1 {
		t.Fatalf("dup=%d stored=%d", r.count(Duplicate, ""), r.count(Stored, ""))
	}
	for _, row := range r.be.rows {
		if !slices.Contains(row.Flags, domain.FlagFlatline) || row.Score != 0.3 {
			t.Fatalf("state was not rebuilt from history: %+v", row)
		}
	}
}

func TestStatusUpdatesAreThrottled(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	r := newRig(t, 2, testConfig(), nil, nil)
	for fc := uint32(1); fc <= 30; fc++ {
		_ = r.submit(r.msg(int(fc)%2, "s", fc, time.Second, 2000+int(fc)))
	}
	r.close()
	r.be.mu.Lock()
	defer r.be.mu.Unlock()
	if r.be.touches < 1 || r.be.touches > 2 {
		t.Fatalf("device status written %d times for 2 devices within one throttle window", r.be.touches)
	}
}

func TestSameBatchDuplicateIsStoredAndCountedOnce(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	cfg := testConfig()
	cfg.RecentKeys, cfg.BatchSize, cfg.BatchInterval = 0, 100, time.Hour // no memory layer, one big batch
	r := newRig(t, 1, cfg, nil, nil)
	for range 3 {
		_ = r.submit(r.msg(0, "s", 1, time.Second, 2000)) // the same message three times
	}
	r.close()
	if r.count(Stored, "") != 1 || r.count(Duplicate, "") != 2 || r.be.stored() != 1 {
		t.Fatalf("stored=%d duplicate=%d rows=%d, want 1/2/1", r.count(Stored, ""), r.count(Duplicate, ""), r.be.stored())
	}
	if v := metric(t, r.reg, "spillway_ingest_stored_total", nil); v != 1 {
		t.Fatalf("stored metric %v, want 1: a copy inside the same batch must not count as new", v)
	}
	if v := metric(t, r.reg, "spillway_ingest_duplicates_total", map[string]string{"layer": "database"}); v != 2 {
		t.Fatalf("database duplicates %v, want 2", v)
	}
}

func TestTransientLookupErrorsNeverFailAMessage(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	r := newRig(t, 1, testConfig(), func(b *fakeBackend) { b.lookupFail = 3 }, nil)
	_ = r.submit(r.msg(0, "s", 1, time.Second, 2000))
	r.close()
	if r.count(Stored, "") != 1 || r.count(Failed, "") != 0 {
		t.Fatalf("stored=%d failed=%d: a database hiccup must delay, never fail, a message", r.count(Stored, ""), r.count(Failed, ""))
	}
}

func TestCloseReleasesBlockedSubmitters(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	cfg := testConfig()
	cfg.Shards, cfg.QueueSize, cfg.WriterQueue, cfg.BatchSize = 1, 1, 1, 1
	r := newRig(t, 1, cfg, func(b *fakeBackend) { b.gate = make(chan struct{}) }, nil)
	for fc := uint32(1); fc < 50; fc++ { // fill every queue
		if r.p.Submit(context.Background(), r.msg(0, "s", fc, time.Second, 2000+int(fc)), false) != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	blocked := make(chan error, 1)
	go func() { blocked <- r.p.Submit(context.Background(), r.msg(0, "s", 900, time.Second, 2900), true) }()
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = r.p.Close(ctx) // the database never answers: the deadline aborts
	select {
	case err := <-blocked:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("a submitter blocked on a full queue must be released with ErrClosed, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: Close did not release a blocked Submit")
	}
}
