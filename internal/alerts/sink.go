// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package alerts runs the rule engine on stored readings and turns its decisions into alert events
// plus scheduled notifications, in one transaction each.
package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/ingest"
	"github.com/sellebeww/spillway/internal/notify"
	"github.com/sellebeww/spillway/internal/quality"
	"github.com/sellebeww/spillway/internal/rules"
	"github.com/sellebeww/spillway/internal/store"
)

// Config tunes the sink.
type Config struct {
	Shards    int           // workers; a device always maps to the same worker (ordering)
	QueueSize int           // bounded queue per worker; a full queue slows the ingest writer, nothing is dropped
	RuleTTL   time.Duration // how long a device's rules are cached
	Replay    time.Duration // history replayed to rebuild rule state (24 h)
	TickEvery time.Duration // period of the reminder and device status check
}

// DefaultConfig returns production defaults.
func DefaultConfig() Config {
	return Config{Shards: 4, QueueSize: 1024, RuleTTL: 2 * time.Minute, Replay: 24 * time.Hour, TickEvery: 30 * time.Second}
}

type msgKind int

const (
	kEvent msgKind = iota
	kWarm
	kTick
	kBarrier
)

type msg struct {
	kind msgKind
	ev   ingest.ReadingEvent // kEvent; kWarm uses OrgID and DeviceID
	now  time.Time
	done chan struct{}
}

// Sink implements ingest.ReadingSink. Handle is called from the single ingest writer goroutine in
// commit order; it only enqueues, and each device is evaluated by one worker, in that order.
type Sink struct {
	st      *store.Store
	cfg     Config
	log     *slog.Logger
	now     func() time.Time
	shards  []*shard
	decided *prometheus.CounterVec
	applyEr prometheus.Counter

	mu      sync.RWMutex
	closed  bool
	closing chan struct{}
	wg      sync.WaitGroup
	stop    context.CancelFunc
	bg      sync.WaitGroup
}

// New builds the sink; call Start to launch it. now may be nil (wall clock); reg may be nil.
func New(st *store.Store, cfg Config, log *slog.Logger, now func() time.Time, reg prometheus.Registerer) *Sink {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Sink{st: st, cfg: cfg, log: log, now: now, closing: make(chan struct{}),
		decided: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "spillway_alert_decisions_total", Help: "Rule decisions applied, by kind."}, []string{"kind"}),
		applyEr: prometheus.NewCounter(prometheus.CounterOpts{Name: "spillway_alert_apply_errors_total", Help: "Failed attempts to persist a decision."})}
	if reg != nil {
		reg.MustRegister(s.decided, s.applyEr)
	}
	return s
}

// Start launches the workers, restores every open alert, and begins the periodic check.
func (s *Sink) Start(ctx context.Context) error {
	for i := 0; i < s.cfg.Shards; i++ {
		sh := &shard{s: s, in: make(chan msg, s.cfg.QueueSize), devs: map[uuid.UUID]*devEng{}}
		s.shards = append(s.shards, sh)
		s.wg.Add(1)
		go func() { defer s.wg.Done(); sh.run() }()
	}
	open, err := s.st.OpenEventsAll(ctx)
	if err != nil {
		return err
	}
	for _, ev := range open {
		if ev.DeviceID != nil {
			_ = s.enqueue(ctx, msg{kind: kWarm, ev: ingest.ReadingEvent{OrgID: ev.OrgID, DeviceID: *ev.DeviceID}})
		}
	}
	bctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.stop = cancel
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		t := time.NewTicker(s.cfg.TickEvery)
		defer t.Stop()
		for {
			select {
			case <-bctx.Done():
				return
			case <-t.C:
				_ = s.Tick(bctx, s.now())
				s.updateStatuses(bctx, s.now())
			}
		}
	}()
	return nil
}

func (s *Sink) shardOf(dev uuid.UUID) *shard {
	h := fnv.New32a()
	_, _ = h.Write(dev[:])
	return s.shards[int(h.Sum32()%uint32(len(s.shards)))] //nolint:gosec // small shard count
}

func (s *Sink) enqueue(ctx context.Context, m msg) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ingest.ErrClosed
	}
	sh := s.shardOf(m.ev.DeviceID)
	select {
	case sh.in <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.closing:
		return ingest.ErrClosed
	}
}

// Handle implements ingest.ReadingSink.
func (s *Sink) Handle(ctx context.Context, ev ingest.ReadingEvent) {
	if err := s.enqueue(ctx, msg{kind: kEvent, ev: ev}); err != nil {
		// The reading is already committed; the next start replays it from the database and
		// reconciles, so a dropped hand-off delays an alert but never loses it.
		s.log.Warn("alert hand-off skipped", "err", err)
	}
}

// Sync waits until every message enqueued before the call has been processed (tests, shutdown).
func (s *Sink) Sync(ctx context.Context) error { return s.broadcast(ctx, msg{kind: kBarrier}) }

// Tick evaluates due reminders for open alerts with fresh data, as of now, and waits for it.
func (s *Sink) Tick(ctx context.Context, now time.Time) error {
	return s.broadcast(ctx, msg{kind: kTick, now: now})
}

func (s *Sink) broadcast(ctx context.Context, m msg) error {
	var dones []chan struct{}
	for _, sh := range s.shards {
		d := make(chan struct{})
		mm := m
		mm.done = d
		s.mu.RLock()
		if s.closed {
			s.mu.RUnlock()
			return ingest.ErrClosed
		}
		select {
		case sh.in <- mm:
		case <-ctx.Done():
			s.mu.RUnlock()
			return ctx.Err()
		}
		s.mu.RUnlock()
		dones = append(dones, d)
	}
	for _, d := range dones {
		select {
		case <-d:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Close drains the queues and stops the workers.
func (s *Sink) Close(ctx context.Context) error {
	if s.stop != nil {
		s.stop()
	}
	s.bg.Wait()
	s.mu.Lock()
	if !s.closed {
		close(s.closing)
		s.closed = true
		for _, sh := range s.shards {
			close(sh.in)
		}
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// updateStatuses writes ONLINE/DELAYED/OFFLINE for devices whose state changed. It only affects
// what the UI shows: there is deliberately no alarm for a dead sensor (scope lock).
func (s *Sink) updateStatuses(ctx context.Context, now time.Time) {
	rows, err := s.st.DevicesForStatusCheck(ctx)
	if err != nil {
		s.log.Warn("device status check failed", "err", err)
		return
	}
	for _, r := range rows {
		if r.LastSeenAt == nil {
			continue
		}
		want := quality.Status(now, *r.LastSeenAt, time.Duration(r.IntervalS)*time.Second)
		if string(want) == r.LastStatus {
			continue
		}
		if err := s.st.ForOrg(r.OrgID).UpdateDeviceStatus(ctx, r.ID, *r.LastSeenAt, want); err != nil {
			s.log.Warn("device status update failed", "err", err)
		}
	}
}

type shard struct {
	s    *Sink
	in   chan msg
	devs map[uuid.UUID]*devEng // owned by this worker goroutine
}

type devEng struct {
	org, site, dev uuid.UUID
	siteName       string
	slug, tz       string
	public         bool
	devName        string
	interval       time.Duration
	rules          []store.AlertRule
	engs           map[uuid.UUID]*rules.Engine
	sig            string
	loadedAt       time.Time
	last           rules.Point // newest replayed or live point
}

func (sh *shard) run() {
	ctx := context.Background()
	for m := range sh.in {
		switch m.kind {
		case kEvent:
			sh.event(ctx, m.ev)
		case kWarm:
			if _, err := sh.ensure(ctx, m.ev.OrgID, m.ev.DeviceID); err != nil {
				sh.s.log.Warn("restoring open alert failed", "err", err)
			}
		case kTick:
			for _, d := range sh.devs {
				for _, r := range d.rules {
					for _, dec := range d.engs[r.ID].Remind(m.now, quality.StaleAfter(d.interval)) {
						sh.apply(ctx, d, r, dec)
					}
				}
			}
		}
		if m.done != nil {
			close(m.done)
		}
	}
}

func (sh *shard) event(ctx context.Context, ev ingest.ReadingEvent) {
	d, err := sh.ensure(ctx, ev.OrgID, ev.DeviceID)
	if err != nil {
		sh.s.log.Error("alert state unavailable", "err", err)
		return
	}
	pt := rules.Point{TS: ev.TS, ReceivedAt: ev.ReceivedAt, LevelCm: ev.LevelCm, Score: ev.Score}
	if pt.TS.After(d.last.TS) && ev.Score >= domain.DefaultMinQuality {
		d.last = pt
	}
	for _, r := range d.rules {
		for _, dec := range d.engs[r.ID].Process(pt) {
			sh.apply(ctx, d, r, dec)
		}
	}
}

// ensure returns the device's engines, (re)building them from the database when the device is
// new to this process or its rules changed.
func (sh *shard) ensure(ctx context.Context, org, devID uuid.UUID) (*devEng, error) {
	s := sh.s
	now := s.now()
	d := sh.devs[devID]
	if d != nil && now.Sub(d.loadedAt) < s.cfg.RuleTTL {
		return d, nil
	}
	o := s.st.ForOrg(org)
	dev, err := o.GetDevice(ctx, devID)
	if err != nil {
		return nil, err
	}
	site, err := o.GetSite(ctx, dev.SiteID)
	if err != nil {
		return nil, err
	}
	all, err := o.ListRules(ctx, &dev.SiteID)
	if err != nil {
		return nil, err
	}
	var enabled []store.AlertRule
	var sig strings.Builder
	for _, r := range all {
		if r.Enabled {
			enabled = append(enabled, r)
			sig.WriteString(r.ID.String() + r.UpdatedAt.String())
		}
	}
	if d != nil && d.sig == sig.String() {
		d.loadedAt = now
		return d, nil
	}
	nd := &devEng{org: org, site: site.ID, dev: devID, siteName: site.Name, slug: site.Slug, tz: site.TZ, public: site.IsPublic,
		devName: dev.Name, interval: time.Duration(dev.ExpectedIntervalS) * time.Second, rules: enabled,
		engs: map[uuid.UUID]*rules.Engine{}, sig: sig.String(), loadedAt: now}
	hist, err := o.ReadingsForReplay(ctx, devID, now.Add(-s.cfg.Replay))
	if err != nil {
		return nil, err
	}
	for _, r := range enabled {
		eng, err := rules.NewEngine([]domain.Rule{r.Domain()}, r.MinQuality)
		if err != nil {
			s.log.Error("rule rejected by the engine", "err", err)
			continue
		}
		for _, h := range hist { // replay discards decisions: nothing is notified twice
			p := rules.Point{TS: h.TS, ReceivedAt: h.ReceivedAt, LevelCm: h.LevelCm, Score: h.Score}
			eng.Process(p)
			if h.Score >= r.MinQuality && p.TS.After(nd.last.TS) {
				nd.last = p
			}
		}
		nd.engs[r.ID] = eng
	}
	sh.devs[devID] = nd
	for _, r := range nd.rules {
		if nd.engs[r.ID] != nil {
			sh.reconcile(ctx, nd, r)
		}
	}
	return nd, nil
}

// reconcile brings the database in line with the rebuilt engine: a transition that was decided but
// not committed before a crash is emitted now, and an alert older than the replay window is adopted.
func (sh *shard) reconcile(ctx context.Context, d *devEng, r store.AlertRule) {
	o := sh.s.st.ForOrg(d.org)
	eng := d.engs[r.ID]
	sev, _ := eng.State(r.ID.String())
	ev, err := o.OpenEventForDevice(ctx, r.ID, d.dev)
	now := sh.s.now()
	dec := rules.Decision{RuleID: r.ID.String(), Severity: sev, At: now, TS: d.last.TS, LevelCm: d.last.LevelCm}
	switch {
	case err == nil && sev == ev.Severity:
		if ev.LastNotifiedAt != nil {
			eng.Adopt(r.ID.String(), sev, *ev.LastNotifiedAt)
		}
	case err == nil && sev == domain.SeverityNone: // the alert predates the replay window
		last := ev.OpenedAt
		if ev.LastNotifiedAt != nil {
			last = *ev.LastNotifiedAt
		}
		eng.Adopt(r.ID.String(), ev.Severity, last)
	case err == nil:
		dec.Previous = ev.Severity
		dec.Kind = rules.Escalated
		if sev < ev.Severity {
			dec.Kind = rules.Deescalated
		}
		sh.apply(ctx, d, r, dec)
	case errors.Is(err, store.ErrNotFound) && sev > domain.SeverityNone:
		dec.Kind = rules.Opened
		sh.apply(ctx, d, r, dec)
	case err != nil && !errors.Is(err, store.ErrNotFound):
		sh.s.log.Warn("reconcile lookup failed", "err", err)
	}
}

// apply persists one decision (alert change + notifications in one transaction), retrying
// transient database errors until they clear: a decision is never skipped because of an outage.
func (sh *shard) apply(ctx context.Context, d *devEng, r store.AlertRule, dec rules.Decision) {
	s := sh.s
	p := notify.Payload{Kind: string(dec.Kind), Severity: int(dec.Severity), Previous: int(dec.Previous), SiteName: d.siteName,
		SiteSlug: d.slug, SitePublic: d.public, TZ: d.tz, DeviceName: d.devName, LevelCm: dec.LevelCm, RateCmPerHour: dec.RateCmPerHour, TS: dec.TS.Unix()}
	raw, _ := json.Marshal(p)
	in := store.DecisionInput{RuleID: r.ID, DeviceID: d.dev, Kind: string(dec.Kind), Severity: dec.Severity, Previous: dec.Previous,
		At: dec.At, LevelCm: dec.LevelCm, Rate: dec.RateCmPerHour, Payload: raw}
	o := s.st.ForOrg(d.org)
	delay := 100 * time.Millisecond
	for {
		_, err := o.ApplyDecision(ctx, in)
		if errors.Is(err, store.ErrNotFound) && in.Kind != "OPENED" && in.Severity > 0 {
			in.Kind = "OPENED" // the open event is missing (lost before a crash): create it now
			p.Kind = "OPENED"
			in.Payload, _ = json.Marshal(p)
			continue
		}
		if err == nil {
			s.decided.WithLabelValues(in.Kind).Inc()
			return
		}
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalid) {
			s.log.Error("decision rejected permanently", "kind", in.Kind, "err", err)
			return
		}
		s.applyEr.Inc()
		s.log.Warn("persisting decision failed, retrying", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 5*time.Second)
	}
}
