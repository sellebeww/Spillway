// Copyright (c) 2026 Vassel Goleyu. MIT License.

package ingest

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/sellebeww/spillway/internal/ingest/protocol"
	"github.com/sellebeww/spillway/internal/store"
)

// Errors returned by Submit. When Submit returns an error the message's Done is NOT called;
// the caller decides (HTTP answers 503, MQTT leaves the message unacknowledged).
var (
	ErrBacklog = errors.New("ingest queue is full")
	ErrClosed  = errors.New("ingest pipeline is closed")
)

// Config tunes the pipeline. DefaultConfig gives safe production values.
type Config struct {
	Shards        int           // workers; one device always maps to the same worker (ordering)
	QueueSize     int           // bounded queue per shard
	WriterQueue   int           // bounded queue between workers and the batch writer
	BatchSize     int           // rows per insert
	BatchInterval time.Duration // flush a partial batch after this long
	DeviceTTL     time.Duration // device and calibration cache lifetime
	UnknownTTL    time.Duration // negative cache for unknown device keys
	RecentKeys    int           // per-device in-memory dedupe window (0 disables)
	HistoryRows   int           // readings replayed to rebuild a device's quality state
	StatusEvery   time.Duration // minimum spacing of device status writes
}

// DefaultConfig returns the defaults: 500 rows or 100 ms per batch, bounded queues.
func DefaultConfig() Config {
	return Config{Shards: max(4, runtime.NumCPU()), QueueSize: 1024, WriterQueue: 2048, BatchSize: 500,
		BatchInterval: 100 * time.Millisecond, DeviceTTL: time.Minute, UnknownTTL: 10 * time.Second,
		RecentKeys: 128, HistoryRows: 64, StatusEvery: 30 * time.Second}
}

// Deps are the pipeline's collaborators. Dedupe, Publisher and Sink may be nil.
type Deps struct {
	Backend   Backend
	Dedupe    Dedupe
	Publisher Publisher
	Sink      ReadingSink
	Metrics   *Metrics
	Log       *slog.Logger
	Now       func() time.Time
}

// Message is one telemetry message to process.
type Message struct {
	Payload     []byte            // raw JSON (ignored when Parsed is set)
	Parsed      *protocol.Payload // already validated by the caller (HTTP)
	TopicDevice string            // device key from the MQTT topic, must equal the payload's
	Source      string            // "mqtt" or "http", a metric label
	ReceivedAt  time.Time         // zero means "now"
	Done        func(Outcome)     // called exactly once, after commit or rejection
}

type item struct {
	msg  *Message
	p    protocol.Payload
	recv time.Time
}

func (it *item) finish(o Outcome) {
	if it.msg.Done != nil {
		it.msg.Done(o)
	}
}

type accepted struct {
	it   *item
	dev  DeviceInfo
	row  store.DeviceReading
	late bool
}

// Pipeline is the ingest engine. Create it with New and stop it with Close.
type Pipeline struct {
	cfg    Config
	be     Backend
	dd     Dedupe
	pub    Publisher
	sink   ReadingSink
	m      *Metrics
	log    *slog.Logger
	now    func() time.Time
	shards []*shard
	out    chan *accepted

	mu        sync.RWMutex // guards closed and the shard channels against concurrent send/close
	closed    bool
	closing   chan struct{}
	closeOnce sync.Once
	workers   sync.WaitGroup
	writerEnd chan struct{}
	done      chan struct{}   // closed when every goroutine has finished
	hardCtx   context.Context //nolint:containedctx // lifetime of the whole pipeline, cancelled to abort on shutdown timeout
	hardStop  context.CancelFunc

	touched map[uuid.UUID]time.Time // writer goroutine only
}

// New starts the workers and the batch writer. reg registers the queue depth gauge.
func New(cfg Config, d Deps, reg prometheus.Registerer) *Pipeline {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	p := &Pipeline{cfg: cfg, be: d.Backend, dd: d.Dedupe, pub: d.Publisher, sink: d.Sink, m: d.Metrics, log: d.Log, now: d.Now,
		out: make(chan *accepted, cfg.WriterQueue), closing: make(chan struct{}), writerEnd: make(chan struct{}), done: make(chan struct{}),
		touched: map[uuid.UUID]time.Time{}}
	p.hardCtx, p.hardStop = context.WithCancel(context.Background())
	for i := 0; i < cfg.Shards; i++ {
		s := &shard{p: p, in: make(chan *item, cfg.QueueSize), devs: map[string]*devState{}, neg: map[string]time.Time{}}
		p.shards = append(p.shards, s)
		p.workers.Add(1)
		go func() { defer p.workers.Done(); s.run() }()
	}
	go func() {
		defer close(p.writerEnd)
		runBatcher(p.out, cfg.BatchSize, cfg.BatchInterval, p.commit)
	}()
	if reg != nil {
		reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "spillway_ingest_queue_depth", Help: "Messages waiting in shard queues and the writer queue."}, p.depth))
	}
	return p
}

func (p *Pipeline) depth() float64 {
	n := len(p.out)
	for _, s := range p.shards {
		n += len(s.in)
	}
	return float64(n)
}

func shardOf(key string, n int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(n)) //nolint:gosec // n is a small positive shard count
}

// Submit validates the message shape and queues it on the shard of its device, so messages of
// one device are processed in submission order. With wait=false a full queue returns ErrBacklog
// immediately (HTTP); with wait=true Submit blocks until there is room, the context ends or the
// pipeline closes (MQTT backpressure: the sender is slowed, nothing is dropped).
func (p *Pipeline) Submit(ctx context.Context, msg *Message, wait bool) error {
	recv := msg.ReceivedAt
	if recv.IsZero() {
		recv = p.now()
	}
	p.m.Received.WithLabelValues(msg.Source).Inc()
	it := &item{msg: msg, recv: recv}
	if msg.Parsed != nil {
		it.p = *msg.Parsed
	} else {
		pl, err := protocol.Parse(msg.Payload)
		if err != nil {
			p.reject(it, ReasonSchema)
			return nil
		}
		it.p = pl
	}
	if msg.TopicDevice != "" && msg.TopicDevice != it.p.Device {
		p.reject(it, ReasonTopicMismatch)
		return nil
	}
	sh := p.shards[shardOf(it.p.Device, len(p.shards))]

	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrClosed
	}
	select {
	case sh.in <- it:
		return nil
	default:
	}
	if !wait {
		return ErrBacklog
	}
	p.m.Backpressure.Inc()
	select {
	case sh.in <- it:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.closing:
		return ErrClosed
	}
}

func (p *Pipeline) reject(it *item, reason string) {
	p.m.Rejected.WithLabelValues(reason).Inc()
	it.finish(Outcome{Kind: Rejected, Reason: reason})
}

// Close stops accepting messages, drains every queue, flushes the last batch and waits for all
// goroutines. If ctx ends first, in-flight work is aborted: those messages complete as Failed
// (so MQTT leaves them unacknowledged and the broker redelivers them) and Close returns ctx.Err().
func (p *Pipeline) Close(ctx context.Context) error {
	p.closeOnce.Do(func() {
		close(p.closing) // wakes senders blocked on a full queue so they release the read lock
		p.mu.Lock()
		p.closed = true
		for _, s := range p.shards {
			close(s.in)
		}
		p.mu.Unlock()
		go func() { // teardown runs exactly once, however often Close is called
			p.workers.Wait()
			close(p.out)
			<-p.writerEnd
			close(p.done)
		}()
	})
	select {
	case <-p.done:
		p.hardStop()
		return nil
	case <-ctx.Done():
		p.hardStop()
		<-p.done
		return ctx.Err()
	}
}

// commit writes one batch (called only from the writer goroutine).
func (p *Pipeline) commit(batch []*accepted) {
	p.m.BatchSize.Observe(float64(len(batch)))
	byOrg := map[uuid.UUID][]*accepted{}
	var order []uuid.UUID
	for _, a := range batch {
		if _, ok := byOrg[a.dev.OrgID]; !ok {
			order = append(order, a.dev.OrgID)
		}
		byOrg[a.dev.OrgID] = append(byOrg[a.dev.OrgID], a)
	}
	var events []ReadingEvent
	var marks []string
	for _, org := range order {
		evs, mk := p.commitOrg(org, byOrg[org])
		events, marks = append(events, evs...), append(marks, mk...)
	}
	ctx := p.hardCtx
	if p.dd != nil && len(marks) > 0 {
		if err := p.dd.Mark(ctx, marks); err != nil {
			p.m.RedisErrors.Inc() // fail open: the unique index still deduplicates
		}
	}
	if len(events) == 0 {
		return
	}
	if p.pub != nil {
		if err := p.pub.Publish(ctx, events); err != nil {
			p.m.PublishErrs.Inc()
			p.log.Warn("publish failed", "err", err)
		}
	}
	if p.sink != nil {
		for _, ev := range events {
			p.sink.Handle(ctx, ev)
		}
	}
	p.touch(events)
}

func (p *Pipeline) commitOrg(org uuid.UUID, items []*accepted) (events []ReadingEvent, marks []string) {
	ctx := p.hardCtx
	rows := make([]store.DeviceReading, len(items))
	for i, a := range items {
		rows[i] = a.row
	}
	inserted, err := p.insert(ctx, org, rows)
	switch {
	case errors.Is(err, errAborted):
		for _, a := range items {
			a.it.finish(Outcome{Kind: Failed, Err: err})
		}
		return nil, nil
	case err != nil && len(items) > 1: // a poison row must not block its neighbours: retry one by one
		for _, a := range items {
			e, m := p.commitOrg(org, []*accepted{a})
			events, marks = append(events, e...), append(marks, m...)
		}
		return events, marks
	case err != nil:
		p.log.Error("row rejected by the database", "err", err)
		p.m.Rejected.WithLabelValues(ReasonDBInvalid).Inc()
		items[0].it.finish(Outcome{Kind: Rejected, Reason: ReasonDBInvalid})
		return nil, nil
	}
	// Keys compare by instant (microseconds), never by time.Time value: the driver returns
	// timestamps in another location than the pipeline builds them in.
	got := make(map[rowKey]struct{}, len(inserted))
	for _, k := range inserted {
		got[keyOf(k.DeviceID, k.TS, k.SID, k.FC)] = struct{}{}
	}
	now := p.now()
	for _, a := range items {
		r := a.row
		marks = append(marks, protocol.IdempotencyKey(a.it.p))
		k := keyOf(r.DeviceID, r.TS, r.SID, r.FC)
		if _, ok := got[k]; !ok {
			p.m.Duplicates.WithLabelValues("database").Inc()
			a.it.finish(Outcome{Kind: Duplicate})
			continue
		}
		delete(got, k) // a second copy of the same key in this batch was skipped by the database
		p.m.Stored.Inc()
		p.m.E2ESecs.Observe(now.Sub(a.it.recv).Seconds())
		if a.late {
			p.m.Late.Inc()
		}
		for _, f := range r.Flags {
			p.m.Flags.WithLabelValues(string(f)).Inc()
		}
		events = append(events, ReadingEvent{OrgID: org, SiteID: a.dev.SiteID, DeviceID: a.dev.ID, DeviceKey: a.dev.Key, TS: r.TS,
			ReceivedAt: r.ReceivedAt, RawMM: r.RawMM, LevelCm: r.LevelCm, Score: r.Score, Flags: r.Flags, Late: r.Late})
		a.it.finish(Outcome{Kind: Stored})
	}
	return events, marks
}

type rowKey struct {
	dev uuid.UUID
	ts  int64
	sid string
	fc  uint32
}

func keyOf(dev uuid.UUID, ts time.Time, sid string, fc uint32) rowKey {
	return rowKey{dev, ts.UnixMicro(), sid, fc}
}

var errAborted = errors.New("ingest aborted")

// insert retries transient database errors with backoff until it succeeds or the pipeline is
// hard-stopped. Permanent errors (invalid data) are returned immediately.
func (p *Pipeline) insert(ctx context.Context, org uuid.UUID, rows []store.DeviceReading) ([]store.ReadingKey, error) {
	backoff := 100 * time.Millisecond
	for {
		start := time.Now()
		keys, err := p.be.Insert(ctx, org, rows)
		p.m.InsertSecs.Observe(time.Since(start).Seconds())
		if err == nil {
			return keys, nil
		}
		p.m.DBErrors.Inc()
		if errors.Is(err, store.ErrInvalid) || errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict) {
			return nil, err
		}
		p.log.Warn("insert failed, retrying", "err", err, "rows", len(rows), "backoff", backoff)
		select {
		case <-ctx.Done():
			return nil, errAborted
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

// touch updates last_seen/status of devices, at most once per StatusEvery per device.
func (p *Pipeline) touch(events []ReadingEvent) {
	now := p.now()
	seen := map[uuid.UUID]ReadingEvent{}
	for _, ev := range events {
		if last, ok := p.touched[ev.DeviceID]; ok && now.Sub(last) < p.cfg.StatusEvery {
			continue
		}
		seen[ev.DeviceID] = ev
	}
	for id, ev := range seen {
		d := DeviceInfo{ID: id, OrgID: ev.OrgID, SiteID: ev.SiteID, Key: ev.DeviceKey}
		if err := p.be.Touch(p.hardCtx, d, ev.ReceivedAt); err != nil {
			p.log.Warn("device status update failed", "err", err)
			continue
		}
		p.touched[id] = now
	}
}
