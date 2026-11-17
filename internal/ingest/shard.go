// Copyright (c) 2026 Vassel Goleyu. MIT License.

package ingest

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/sellebeww/spillway/internal/auth"
	"github.com/sellebeww/spillway/internal/calibration"
	"github.com/sellebeww/spillway/internal/domain"
	"github.com/sellebeww/spillway/internal/ingest/protocol"
	"github.com/sellebeww/spillway/internal/quality"
	"github.com/sellebeww/spillway/internal/store"
)

// shard is one worker goroutine. It owns every map below, so no locking is needed, and it
// processes its devices' messages strictly in queue order.
type shard struct {
	p    *Pipeline
	in   chan *item
	devs map[string]*devState
	neg  map[string]time.Time // unknown keys and until when to remember that
}

type devState struct {
	info   DeviceInfo
	infoAt time.Time
	cals   []domain.Calibration
	calsAt time.Time
	eval   *quality.Evaluator
	recent *keyRing
}

func (s *shard) run() {
	for it := range s.in {
		s.process(it)
	}
}

func fail(it *item, err error) { it.finish(Outcome{Kind: Failed, Err: err}) }

func (s *shard) process(it *item) {
	p := s.p
	ctx := p.hardCtx
	if err := ctx.Err(); err != nil {
		fail(it, err) // hard shutdown: leave the message unacknowledged
		return
	}
	ds, err := s.device(ctx, it.p.Device)
	switch {
	case errors.Is(err, ErrUnknownDevice):
		p.reject(it, ReasonUnknownDevice)
		return
	case err != nil:
		fail(it, err)
		return
	}
	if !protocol.Verify(ds.info.Secret, it.p) {
		p.reject(it, ReasonSig)
		return
	}
	ts := time.Unix(it.p.TS, 0).UTC()
	age := it.recv.Sub(ts)
	if age > domain.MaxLateAge {
		p.reject(it, ReasonTooOld)
		return
	}

	rk := it.p.SID + ":" + strconv.FormatUint(uint64(it.p.FC), 10)
	if ds.recent.has(rk) {
		p.m.Duplicates.WithLabelValues("memory").Inc()
		it.finish(Outcome{Kind: Duplicate})
		return
	}
	if p.dd != nil {
		seen, derr := p.dd.Seen(ctx, protocol.IdempotencyKey(it.p))
		if derr != nil {
			p.m.RedisErrors.Inc() // fail open
		} else if seen {
			p.m.Duplicates.WithLabelValues("redis").Inc()
			it.finish(Outcome{Kind: Duplicate})
			return
		}
	}

	cal, err := s.calibrationAt(ctx, ds, ts)
	switch {
	case errors.Is(err, calibration.ErrNoCalibration):
		p.reject(it, ReasonNoCalibration)
		return
	case err != nil:
		fail(it, err)
		return
	}
	if err := s.ensureEvaluator(ctx, ds); err != nil {
		fail(it, err)
		return
	}
	reading := domain.Reading{DeviceKey: it.p.Device, SID: it.p.SID, FC: it.p.FC, TS: ts, ReceivedAt: it.recv, RawMM: it.p.RawMM, BattV: it.p.BattV}
	res := ds.eval.Evaluate(reading, cal)
	ds.recent.add(rk)

	late := age > domain.LateAfter
	a := &accepted{it: it, dev: ds.info, late: late, row: store.DeviceReading{DeviceID: ds.info.ID, ReadingRow: store.ReadingRow{
		TS: ts, ReceivedAt: it.recv, SID: it.p.SID, FC: it.p.FC, RawMM: it.p.RawMM, LevelCm: calibration.Level(cal.OffsetCm, it.p.RawMM),
		Flags: res.Flags, Score: res.Score, Late: late, BattV: it.p.BattV}}}
	select {
	case p.out <- a:
	case <-ctx.Done():
		fail(it, ctx.Err())
	}
}

// device returns the cached device state, refreshing it after DeviceTTL. Unknown keys are
// remembered briefly so a flood of bogus names cannot hammer the database.
func (s *shard) device(ctx context.Context, key string) (*devState, error) {
	p := s.p
	now := p.now()
	ds := s.devs[key]
	if ds != nil && now.Sub(ds.infoAt) < p.cfg.DeviceTTL {
		return ds, nil
	}
	if until, ok := s.neg[key]; ok && now.Before(until) {
		return nil, ErrUnknownDevice
	}
	info, err := retry(ctx, func() (DeviceInfo, error) { return p.be.LookupDevice(ctx, key) })
	switch {
	case errors.Is(err, ErrUnknownDevice):
		if len(s.neg) > 10000 {
			clear(s.neg)
		}
		s.neg[key] = now.Add(p.cfg.UnknownTTL)
		delete(s.devs, key)
		return nil, err
	case err != nil:
		if ds != nil {
			return ds, nil // keep serving from the stale entry while the database is unreachable
		}
		return nil, err
	}
	if ds == nil {
		ds = &devState{recent: newKeyRing(p.cfg.RecentKeys)}
		s.devs[key] = ds
	}
	ds.info, ds.infoAt = info, now
	return ds, nil
}

// calibrationAt resolves the version in force at the reading time, refreshing the cached
// list when it is stale or when no version matches (a calibration may have just been added).
func (s *shard) calibrationAt(ctx context.Context, ds *devState, ts time.Time) (domain.Calibration, error) {
	p := s.p
	now := p.now()
	if ds.cals == nil || now.Sub(ds.calsAt) >= p.cfg.DeviceTTL {
		if err := s.loadCals(ctx, ds, now); err != nil && ds.cals == nil {
			return domain.Calibration{}, err
		}
	}
	cal, err := calibration.Resolve(ds.cals, ts)
	if errors.Is(err, calibration.ErrNoCalibration) && now.Sub(ds.calsAt) >= 5*time.Second {
		if lerr := s.loadCals(ctx, ds, now); lerr != nil {
			return domain.Calibration{}, lerr
		}
		cal, err = calibration.Resolve(ds.cals, ts)
	}
	return cal, err
}

func (s *shard) loadCals(ctx context.Context, ds *devState, now time.Time) error {
	cals, err := retry(ctx, func() ([]domain.Calibration, error) { return s.p.be.Calibrations(ctx, ds.info) })
	if err != nil {
		return err
	}
	ds.cals, ds.calsAt = cals, now
	if ds.cals == nil {
		ds.cals = []domain.Calibration{}
	}
	return nil
}

// ensureEvaluator rebuilds the per-device quality state from the stored history the first time
// a device is seen after a start, and seeds the in-memory dedupe window with the same rows.
func (s *shard) ensureEvaluator(ctx context.Context, ds *devState) error {
	if ds.eval != nil {
		return nil
	}
	hist, err := retry(ctx, func() ([]store.StoredReading, error) { return s.p.be.History(ctx, ds.info, s.p.cfg.HistoryRows) })
	if err != nil {
		return err
	}
	cfg := quality.DefaultConfig()
	cfg.LowBattV = ds.info.LowBattV
	inputs := make([]quality.Input, 0, len(hist))
	for _, h := range hist {
		ds.recent.add(h.SID + ":" + strconv.FormatInt(h.FC, 10))
		cal, cerr := calibration.Resolve(ds.cals, h.TS)
		if cerr != nil {
			continue
		}
		inputs = append(inputs, quality.Input{Cal: cal, Reading: domain.Reading{DeviceKey: ds.info.Key, SID: h.SID,
			FC: uint32(h.FC), TS: h.TS, ReceivedAt: h.ReceivedAt, RawMM: h.RawMM, BattV: h.BattV}}) //nolint:gosec // fc is CHECKed to 0..4294967295
	}
	ds.eval = quality.Rebuild(cfg, inputs)
	return nil
}

// keyRing remembers the last n message keys of a device (a nil ring remembers nothing).
type keyRing struct {
	keys []string
	set  map[string]struct{}
	next int
}

func newKeyRing(n int) *keyRing {
	if n <= 0 {
		return nil
	}
	return &keyRing{keys: make([]string, n), set: make(map[string]struct{}, n)}
}

func (r *keyRing) has(k string) bool {
	if r == nil {
		return false
	}
	_, ok := r.set[k]
	return ok
}

func (r *keyRing) add(k string) {
	if r == nil {
		return
	}
	if _, ok := r.set[k]; ok {
		return
	}
	if old := r.keys[r.next]; old != "" {
		delete(r.set, old)
	}
	r.keys[r.next] = k
	r.set[k] = struct{}{}
	r.next = (r.next + 1) % len(r.keys)
}

// retry runs fn until it succeeds, backing off up to 2 s, and gives up only when the pipeline is
// hard-stopped. It deliberately never fails a message on a transient error: MQTT acknowledgements
// must be sent in order, so one permanently unacknowledged message would stall every later ack and
// with it the whole stream. Blocking this shard instead is honest backpressure (nothing can be stored
// while the database is away anyway). An unknown device is an answer, and a secret that cannot be
// decrypted is permanent, so neither is retried.
func retry[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	delay := 100 * time.Millisecond
	for {
		v, err := fn()
		if err == nil || errors.Is(err, ErrUnknownDevice) || errors.Is(err, auth.ErrDecrypt) || ctx.Err() != nil {
			return v, err
		}
		select {
		case <-ctx.Done():
			return v, err
		case <-time.After(delay):
		}
		delay = min(delay*2, 2*time.Second)
	}
}
