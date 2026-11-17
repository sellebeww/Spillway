// Copyright (c) 2026 Vassel Goleyu. MIT License.

package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisTimeout = 200 * time.Millisecond

// breaker stops calling a failing dependency for a few seconds, so an outage costs one
// timeout instead of one per message.
type breaker struct {
	mu    sync.Mutex
	until time.Time
}

var errBreakerOpen = errors.New("redis circuit open")

func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().After(b.until)
}

func (b *breaker) fail() {
	b.mu.Lock()
	b.until = time.Now().Add(5 * time.Second)
	b.mu.Unlock()
}

// RedisDedupe is the first dedupe layer. Keys are marked only after the reading is committed,
// so a hit is always trustworthy; if Redis fails the pipeline carries on (fail open) and the
// unique index decides.
type RedisDedupe struct {
	C   redis.UniversalClient
	TTL time.Duration // 24 h in production
	br  breaker
}

// Seen reports whether the key was committed before.
func (r *RedisDedupe) Seen(ctx context.Context, key string) (bool, error) {
	if !r.br.allow() {
		return false, errBreakerOpen
	}
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()
	n, err := r.C.Exists(ctx, "spillway:"+key).Result()
	if err != nil {
		r.br.fail()
		return false, fmt.Errorf("redis exists: %w", err)
	}
	return n > 0, nil
}

// Mark records committed keys.
func (r *RedisDedupe) Mark(ctx context.Context, keys []string) error {
	if !r.br.allow() {
		return errBreakerOpen
	}
	ctx, cancel := context.WithTimeout(ctx, 2*redisTimeout)
	defer cancel()
	_, err := r.C.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, k := range keys {
			p.Set(ctx, "spillway:"+k, 1, r.TTL)
		}
		return nil
	})
	if err != nil {
		r.br.fail()
		return fmt.Errorf("redis mark: %w", err)
	}
	return nil
}

// lastValue only moves the cached value forward in time, so a late reading never replaces a newer one.
var lastValue = redis.NewScript(`
local cur = redis.call('HGET', KEYS[1], 'ts')
if (not cur) or tonumber(ARGV[1]) >= tonumber(cur) then
  redis.call('HSET', KEYS[1], 'ts', ARGV[1], 'json', ARGV[2])
  redis.call('EXPIRE', KEYS[1], ARGV[3])
end
return 1`)

// RedisPublisher keeps the last value per device (hash spillway:last:{device_id}) and publishes
// one event per newly stored reading on channel spillway:site:{site_id} for the SSE fan-out.
type RedisPublisher struct {
	C  redis.UniversalClient
	br breaker
}

type eventJSON struct {
	Device     string   `json:"device"`
	DeviceID   string   `json:"device_id"`
	TS         int64    `json:"ts"`
	ReceivedAt int64    `json:"received_at"`
	LevelCm    float64  `json:"level_cm"`
	Score      float64  `json:"quality_score"`
	Flags      []string `json:"flags"`
	Late       bool     `json:"late"`
}

// Publish stores last values and publishes events (best effort).
func (r *RedisPublisher) Publish(ctx context.Context, evs []ReadingEvent) error {
	if !r.br.allow() {
		return errBreakerOpen
	}
	ctx, cancel := context.WithTimeout(ctx, 4*redisTimeout)
	defer cancel()
	_, err := r.C.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, ev := range evs {
			flags := make([]string, len(ev.Flags))
			for i, f := range ev.Flags {
				flags[i] = string(f)
			}
			body, err := json.Marshal(eventJSON{Device: ev.DeviceKey, DeviceID: ev.DeviceID.String(), TS: ev.TS.Unix(),
				ReceivedAt: ev.ReceivedAt.Unix(), LevelCm: ev.LevelCm, Score: ev.Score, Flags: flags, Late: ev.Late})
			if err != nil {
				return fmt.Errorf("encode event: %w", err)
			}
			lastValue.Eval(ctx, p, []string{"spillway:last:" + ev.DeviceID.String()}, ev.TS.Unix(), string(body), 7*24*3600)
			p.Publish(ctx, "spillway:site:"+ev.SiteID.String(), body)
		}
		return nil
	})
	if err != nil {
		r.br.fail()
		return fmt.Errorf("redis publish: %w", err)
	}
	return nil
}

// LogSink is the Phase 4 stand-in for the rule engine: it only counts at debug level.
type LogSink struct{ Log *slog.Logger }

// Handle logs one stored reading without any payload data.
func (s LogSink) Handle(ctx context.Context, ev ReadingEvent) {
	s.Log.DebugContext(ctx, "reading stored", "device", ev.DeviceKey, "level_cm", ev.LevelCm, "score", ev.Score, "late", ev.Late)
}
