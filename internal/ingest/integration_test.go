package ingest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/simulate"
	"github.com/sellebeww/spillway/internal/store"
	"github.com/sellebeww/spillway/internal/testutil"
)

// itNow is the fixed pipeline clock of the integration tests, so "late" is deterministic
// however long the run takes.
var itNow = time.Now().UTC().Truncate(time.Second)

type tenant struct {
	org     *store.OrgStore
	site    store.Site
	devs    []simulate.Device
	ids     []uuid.UUID
	rawIDs  []string
	devKeys []string
}

func (e *testEnv) newTenant(t *testing.T, n int) *tenant {
	t.Helper()
	ctx := context.Background()
	tag := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	org, err := e.st.CreateOrg(ctx, "it-"+tag, "Integration "+tag)
	if err != nil {
		t.Fatal(err)
	}
	o := e.st.ForOrg(org.ID)
	site, err := o.CreateSite(ctx, store.SiteInput{Slug: "site-" + tag, Name: "Site", Lat: -6.2, Lon: 106.8})
	if err != nil {
		t.Fatal(err)
	}
	tn := &tenant{org: o, site: site}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("it-%s-%02d", tag, i)
		dev, secret, err := o.RegisterDevice(ctx, e.cipher, store.DeviceInput{SiteID: site.ID, Key: key, Name: key})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := o.AddCalibration(ctx, dev.ID, domain.Calibration{OffsetCm: 500, ValidFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}, false); err != nil {
			t.Fatal(err)
		}
		d, err := simulate.NewDevice(key, secret, 500)
		if err != nil {
			t.Fatal(err)
		}
		tn.devs, tn.ids, tn.devKeys = append(tn.devs, d), append(tn.ids, dev.ID), append(tn.devKeys, key)
		tn.rawIDs = append(tn.rawIDs, dev.ID.String())
	}
	return tn
}

type stackOpts struct {
	redisAddr string // empty: no Redis layer
	noMemory  bool   // disable the in-memory dedupe window, so only Redis and the unique index remain
	mqtt      bool
	clientID  string
}

type stack struct {
	pipe *Pipeline
	sub  *Subscriber
	reg  *prometheus.Registry
	rdb  *redis.Client
	e    *testEnv
}

func (e *testEnv) newStack(t *testing.T, o stackOpts) *stack {
	t.Helper()
	s := &stack{reg: prometheus.NewRegistry(), e: e}
	cfg := DefaultConfig()
	if o.noMemory {
		cfg.RecentKeys = 0
	}
	deps := Deps{Backend: StoreBackend{Store: e.st, Cipher: e.cipher}, Metrics: NewMetrics(s.reg),
		Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return itNow }}
	if o.redisAddr != "" {
		s.rdb = redis.NewClient(&redis.Options{Addr: o.redisAddr, DialTimeout: 300 * time.Millisecond})
		deps.Dedupe = &RedisDedupe{C: s.rdb, TTL: time.Hour}
		deps.Publisher = &RedisPublisher{C: s.rdb}
	}
	s.pipe = New(cfg, deps, s.reg)
	if o.mqtt {
		sub, err := StartSubscriber(context.Background(), MQTTConfig{URL: e.mqttURL, ClientID: o.clientID, SessionExpiry: 600}, s.pipe, deps.Log)
		if err != nil {
			t.Fatal(err)
		}
		s.sub = sub
		waitFor(t, func() bool { return sub.Connected() == nil })
		time.Sleep(300 * time.Millisecond) // let the SUBSCRIBE complete
	}
	return s
}

// stop shuts down gracefully, in the order serve uses.
func (s *stack) stop(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if s.sub != nil {
		if err := s.sub.StopIntake(ctx); err != nil {
			t.Errorf("stop intake: %v", err)
		}
	}
	if err := s.pipe.Close(ctx); err != nil {
		t.Errorf("close pipeline: %v", err)
	}
	if s.sub != nil {
		if err := s.sub.Close(ctx); err != nil {
			t.Errorf("close subscriber: %v", err)
		}
	}
	if s.rdb != nil {
		_ = s.rdb.Close()
	}
}

// crash stops the process the hard way: the pipeline is aborted (unfinished messages stay
// unacknowledged) and the connection is dropped.
func (s *stack) crash() {
	aborted, cancel := context.WithCancel(context.Background())
	cancel()
	_ = s.pipe.Close(aborted)
	ctx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = s.sub.Close(ctx)
	if s.rdb != nil {
		_ = s.rdb.Close()
	}
}

func (s *stack) m(name string, labels map[string]string) float64 {
	fams, _ := s.reg.Gather()
	for _, f := range fams {
		if f.GetName() != name {
			continue
		}
		sum := 0.0
	next:
		for _, m := range f.GetMetric() {
			for _, lp := range m.GetLabel() {
				if want, ok := labels[lp.GetName()]; ok && want != lp.GetValue() {
					continue next
				}
			}
			switch {
			case m.Counter != nil:
				sum += m.GetCounter().GetValue()
			case m.Gauge != nil:
				sum += m.GetGauge().GetValue()
			case m.Histogram != nil:
				sum += float64(m.GetHistogram().GetSampleCount())
			}
		}
		return sum
	}
	return 0
}

func (s *stack) finished() float64 {
	return s.m("spillway_ingest_stored_total", nil) + s.m("spillway_ingest_duplicates_total", nil) + s.m("spillway_ingest_rejected_total", nil)
}

func (e *testEnv) rows(t *testing.T, tn *tenant, where string) int {
	t.Helper()
	var n int
	q := `SELECT count(*) FROM readings WHERE device_id = ANY($1)` + where
	if err := e.pool.QueryRow(context.Background(), q, tn.ids).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// signedBody builds one wire message for a device.
func signedBody(d simulate.Device, sid string, fc uint32, age time.Duration, level float64) []byte {
	return simulate.Body(d.Payload(sid, fc, itNow.Add(-age), level, nil))
}

type planned struct {
	device string
	body   []byte
}

// mixedPlan builds, per device, the traffic described in the Phase 4 brief: unique messages,
// duplicates of earlier ones, out-of-order messages (older timestamps arriving later), late
// messages (more than two minutes old) and messages with a wrong signature.
func mixedPlan(tn *tenant, unique, dups, ooo, late, bad int) (lists [][]planned, wantRows, wantLate int) {
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic traffic
	for _, d := range tn.devs {
		var l []planned
		var originals [][]byte
		for i := 0; i < unique; i++ {
			b := signedBody(d, "sA", uint32(1+i), time.Duration(i%100)*time.Second, 100+float64(i%37)) //nolint:gosec // small
			l, originals = append(l, planned{d.Key, b}), append(originals, b)
		}
		var tail []planned
		for i := 0; i < dups; i++ {
			tail = append(tail, planned{d.Key, originals[rng.IntN(len(originals))]})
		}
		for j := 0; j < ooo; j++ { // older than anything sent so far, still within the 2 minute window
			tail = append(tail, planned{d.Key, signedBody(d, "sA", uint32(1001+j), 101*time.Second+time.Duration(j%9)*time.Second, 100+float64(j%11))}) //nolint:gosec // small
		}
		for j := 0; j < late; j++ {
			tail = append(tail, planned{d.Key, signedBody(d, "sA", uint32(2001+j), 10*time.Minute+time.Duration(j)*time.Second, 100+float64(j%13))}) //nolint:gosec // small
		}
		for j := 0; j < bad; j++ {
			s := string(signedBody(d, "sA", uint32(3001+j), time.Second, 120)) //nolint:gosec // small
			i := strings.Index(s, `"sig":"`) + len(`"sig":"`)
			flipped := "ff" // same length, valid hex, wrong signature
			if s[i:i+2] == "ff" {
				flipped = "00"
			}
			tail = append(tail, planned{d.Key, []byte(s[:i] + flipped + s[i+2:])})
		}
		rng.Shuffle(len(tail), func(a, b int) { tail[a], tail[b] = tail[b], tail[a] })
		lists = append(lists, append(l, tail...))
	}
	n := len(tn.devs)
	return lists, n * (unique + ooo + late), n * late
}

func publishAll(t *testing.T, url string, lists [][]planned) time.Duration {
	t.Helper()
	ctx := context.Background()
	s, err := simulate.NewMQTTSender(ctx, url, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	start := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // 8 concurrent publishers, each device's list stays in order
	for _, l := range lists {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			for _, m := range l {
				if err := s.Send(ctx, m.device, m.body); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	return time.Since(start)
}

func TestIngestMQTTExactResults(t *testing.T) {
	e := getEnv(t)
	ctx := context.Background()
	tn := e.newTenant(t, 20)
	st := e.newStack(t, stackOpts{redisAddr: e.redisAddr, mqtt: true, clientID: "it-mixed-" + uuid.NewString()[:8]})
	rdb := redis.NewClient(&redis.Options{Addr: e.redisAddr})
	defer func() { _ = rdb.Close() }()
	ps := rdb.Subscribe(ctx, "spillway:site:"+tn.site.ID.String())
	defer func() { _ = ps.Close() }()
	events := ps.Channel(redis.WithChannelSize(50000))
	var evMu sync.Mutex
	evCount := 0
	go func() {
		for range events {
			evMu.Lock()
			evCount++
			evMu.Unlock()
		}
	}()

	lists, wantRows, wantLate := mixedPlan(tn, 500, 100, 25, 15, 5)
	total := 0
	for _, l := range lists {
		total += len(l)
	}
	if total != 12900 || wantRows != 10800 {
		t.Fatalf("plan has %d messages and %d expected rows", total, wantRows)
	}

	begin := time.Now()
	sendTime := publishAll(t, e.mqttURL, lists)
	eventually(t, 120*time.Second, "all messages to be processed", func() bool { return int(st.finished()) >= total })
	elapsed := time.Since(begin)
	st.stop(t)

	if got := e.rows(t, tn, ""); got != wantRows {
		t.Fatalf("rows in database: %d, want %d (unique messages that were accepted)", got, wantRows)
	}
	if got := e.rows(t, tn, ` AND late`); got != wantLate {
		t.Errorf("late rows: %d, want %d", got, wantLate)
	}
	if got := e.rows(t, tn, ` AND NOT late`); got != wantRows-wantLate {
		t.Errorf("on-time rows: %d, want %d", got, wantRows-wantLate)
	}
	if got := e.rows(t, tn, ` AND 'LATE' = ANY(quality_flags)`); got != wantLate {
		t.Errorf("rows flagged LATE: %d, want %d", got, wantLate)
	}
	if got := e.rows(t, tn, ` AND fc >= 3001`); got != 0 {
		t.Errorf("%d rows with a wrong signature were stored", got)
	}
	var distinct int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT DISTINCT device_id, ts, sid, fc FROM readings WHERE device_id = ANY($1)) x`, tn.ids).Scan(&distinct); err != nil || distinct != wantRows {
		t.Errorf("distinct keys %d (err %v), want %d: duplicates were stored", distinct, err, wantRows)
	}
	checks := map[string]float64{
		"spillway_ingest_received_total":   float64(total),
		"spillway_ingest_stored_total":     float64(wantRows),
		"spillway_ingest_duplicates_total": 2000,
		"spillway_ingest_late_total":       float64(wantLate),
	}
	for name, want := range checks {
		if got := st.m(name, nil); got != want {
			t.Errorf("metric %s = %v, want %v", name, got, want)
		}
	}
	if got := st.m("spillway_ingest_rejected_total", map[string]string{"reason": ReasonSig}); got != 100 {
		t.Errorf("rejected{sig} = %v, want 100", got)
	}
	if got := st.m("spillway_ingest_e2e_seconds", nil); got != float64(wantRows) {
		t.Errorf("e2e latency samples %v, want %d", got, wantRows)
	}
	if st.m("spillway_ingest_queue_depth", nil) != 0 {
		t.Error("queue depth must be 0 after the drain")
	}

	// One pub/sub event per newly stored reading, and a last value that never moves backwards.
	eventually(t, 30*time.Second, "pub/sub events", func() bool { evMu.Lock(); defer evMu.Unlock(); return evCount >= wantRows })
	evMu.Lock()
	if evCount != wantRows {
		t.Errorf("published %d events, want exactly %d", evCount, wantRows)
	}
	evMu.Unlock()
	if ts, err := rdb.HGet(ctx, "spillway:last:"+tn.rawIDs[0], "ts").Int64(); err != nil || ts != itNow.Unix() {
		t.Errorf("last value ts = %d (err %v), want %d: late data must not replace newer data", ts, err, itNow.Unix())
	}
	var online int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE id = ANY($1) AND last_status = 'ONLINE' AND last_seen_at IS NOT NULL`, tn.ids).Scan(&online)
	if online != 20 {
		t.Errorf("%d of 20 devices marked ONLINE", online)
	}
	t.Logf("ingest over MQTT, %d messages (%d rows): published in %v (%.0f msg/s), fully processed in %v (%.0f msg/s, %.0f rows/s); GOMAXPROCS machine, 20 devices, QoS 1, real Mosquitto + Redis + TimescaleDB in Docker",
		total, wantRows, sendTime.Round(time.Millisecond), float64(total)/sendTime.Seconds(), elapsed.Round(time.Millisecond), float64(total)/elapsed.Seconds(), float64(wantRows)/elapsed.Seconds())
}

func TestIngestSurvivesRedisOutage(t *testing.T) {
	e := getEnv(t)
	tn := e.newTenant(t, 8)
	ctx := context.Background()
	addr, stopRedis, err := testutil.StartRedis(ctx)
	if err != nil {
		t.Skipf("cannot start a dedicated Redis: %v", err)
	}
	defer stopRedis()
	st := e.newStack(t, stackOpts{redisAddr: addr, noMemory: true, mqtt: true, clientID: "it-redis-" + uuid.NewString()[:8]})

	// Phase A (Redis up): the first 150 unique messages per device and 20 duplicates of them.
	// Phase B (Redis killed): the remaining 150 unique ones and 50 duplicates of both phases.
	build := func(from, to, dups int, pool [][]byte, d simulate.Device) ([]planned, [][]byte) {
		var l []planned
		for i := from; i < to; i++ {
			b := signedBody(d, "sB", uint32(1+i), time.Duration(i%100)*time.Second, 100+float64(i%41)) //nolint:gosec // small
			l, pool = append(l, planned{d.Key, b}), append(pool, b)
		}
		for i := 0; i < dups; i++ {
			l = append(l, planned{d.Key, pool[(i*7)%len(pool)]})
		}
		return l, pool
	}
	pools := make([][][]byte, len(tn.devs))
	phase := func(from, to, dups int) [][]planned {
		var lists [][]planned
		for i, d := range tn.devs {
			var l []planned
			l, pools[i] = build(from, to, dups, pools[i], d)
			lists = append(lists, l)
		}
		return lists
	}
	a := phase(0, 150, 20)
	publishAll(t, e.mqttURL, a)
	eventually(t, 60*time.Second, "phase A", func() bool { return int(st.finished()) >= 8*170 })
	if st.m("spillway_ingest_duplicates_total", map[string]string{"layer": "redis"})+st.m("spillway_ingest_duplicates_total", map[string]string{"layer": "database"}) != 160 {
		t.Fatalf("phase A duplicates not all caught: stored=%v redis=%v database=%v memory=%v rejected=%v", st.m("spillway_ingest_stored_total", nil),
			st.m("spillway_ingest_duplicates_total", map[string]string{"layer": "redis"}), st.m("spillway_ingest_duplicates_total", map[string]string{"layer": "database"}),
			st.m("spillway_ingest_duplicates_total", map[string]string{"layer": "memory"}), st.m("spillway_ingest_rejected_total", nil))
	}

	stopRedis() // the outage
	b := phase(150, 300, 50)
	publishAll(t, e.mqttURL, b)
	eventually(t, 90*time.Second, "phase B", func() bool { return int(st.finished()) >= 8*(170+200) })
	st.stop(t)

	if got := e.rows(t, tn, ""); got != 8*300 {
		t.Fatalf("rows %d, want %d: the pipeline must keep working and stay idempotent without Redis", got, 8*300)
	}
	if st.m("spillway_ingest_duplicates_total", nil) != 8*70 {
		t.Errorf("duplicates counted %v, want %d", st.m("spillway_ingest_duplicates_total", nil), 8*70)
	}
	if db := st.m("spillway_ingest_duplicates_total", map[string]string{"layer": "database"}); db < 8*50 {
		t.Errorf("only %v duplicates reached the unique index, want at least %d (the outage phase)", db, 8*50)
	}
	if st.m("spillway_ingest_redis_errors_total", nil) == 0 {
		t.Error("the outage was not visible in the redis error metric")
	}
}

func TestIngestRestartMidStreamLosesNothing(t *testing.T) {
	e := getEnv(t)
	ctx := context.Background()
	tn := e.newTenant(t, 10)
	clientID := "it-restart-" + uuid.NewString()[:8]
	opts := stackOpts{redisAddr: e.redisAddr, mqtt: true, clientID: clientID}
	a := e.newStack(t, opts)

	var lists [][]planned
	for _, d := range tn.devs {
		var l []planned
		for i := 0; i < 400; i++ {
			l = append(l, planned{d.Key, signedBody(d, "sR", uint32(1+i), time.Duration(i%100)*time.Second, 100+float64(i%29))}) //nolint:gosec // small
		}
		lists = append(lists, l)
	}
	pubDone := make(chan struct{})
	go func() { defer close(pubDone); publishAll(t, e.mqttURL, lists) }()

	eventually(t, 60*time.Second, "the first process to commit some data", func() bool { return a.m("spillway_ingest_stored_total", nil) >= 1000 })
	storedByA := a.m("spillway_ingest_stored_total", nil)
	a.crash() // the process dies in the middle of the stream
	t.Logf("process A died after committing %.0f rows; the broker keeps queueing for the persistent session", storedByA)

	time.Sleep(500 * time.Millisecond) // a visible outage while the publisher keeps going
	b := e.newStack(t, opts)           // restart: same client id, so the persistent session resumes
	<-pubDone
	eventually(t, 120*time.Second, "all 4000 rows after the restart", func() bool { return e.rows(t, tn, "") >= 4000 })
	time.Sleep(500 * time.Millisecond) // a stray duplicate would show up now
	b.stop(t)

	if got := e.rows(t, tn, ""); got != 4000 {
		t.Fatalf("rows %d, want exactly 4000 (loss or duplicates)", got)
	}
	var distinct int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT DISTINCT device_id, ts, sid, fc FROM readings WHERE device_id = ANY($1)) x`, tn.ids).Scan(&distinct); err != nil || distinct != 4000 {
		t.Fatalf("distinct %d (err %v)", distinct, err)
	}
	for _, id := range tn.ids {
		var n int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM readings WHERE device_id = $1`, id).Scan(&n); err != nil || n != 400 {
			t.Fatalf("device %v has %d rows, want 400 (err %v)", id, n, err)
		}
	}
	t.Logf("restart: A committed %.0f, B committed %.0f and absorbed %.0f redelivered duplicates (memory %.0f, redis %.0f, database %.0f)",
		storedByA, b.m("spillway_ingest_stored_total", nil), b.m("spillway_ingest_duplicates_total", nil),
		b.m("spillway_ingest_duplicates_total", map[string]string{"layer": "memory"}),
		b.m("spillway_ingest_duplicates_total", map[string]string{"layer": "redis"}),
		b.m("spillway_ingest_duplicates_total", map[string]string{"layer": "database"}))
}

func TestIngestHTTPEndToEnd(t *testing.T) {
	e := getEnv(t)
	tn := e.newTenant(t, 1)
	st := e.newStack(t, stackOpts{redisAddr: e.redisAddr})
	srv := httptest.NewServer(st.pipe.HTTPHandler(NewLimiter(100, 100, 100), 1024, 10*time.Second))
	defer srv.Close()
	d := tn.devs[0]

	send := func(body []byte) (int, string) {
		resp, err := http.Post(srv.URL, "application/json", strings.NewReader(string(body))) //nolint:noctx // test client
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	good := signedBody(d, "sH", 1, time.Second, 150)
	if code, body := send(good); code != 200 || !strings.Contains(body, "stored") {
		t.Fatalf("first: %d %s", code, body)
	}
	if got := e.rows(t, tn, ""); got != 1 {
		t.Fatalf("a 200 must mean the row is committed, rows=%d", got)
	}
	if code, body := send(good); code != 200 || !strings.Contains(body, "duplicate") {
		t.Fatalf("repeat: %d %s", code, body)
	}
	if code, _ := send([]byte(strings.Replace(string(signedBody(d, "sH", 2, time.Second, 150)), `"raw_mm":3500`, `"raw_mm":3501`, 1))); code != 401 {
		t.Fatalf("tampered message: %d", code)
	}
	if got := e.rows(t, tn, ""); got != 1 {
		t.Fatalf("rows=%d, want 1", got)
	}
	st.stop(t)
	if got := st.m("spillway_ingest_received_total", map[string]string{"source": "http"}); got != 3 {
		t.Errorf("received{http} = %v, want 3", got)
	}
}
