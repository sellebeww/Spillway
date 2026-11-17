// Copyright (c) 2026 Vassel Goleyu. MIT License.

package ingest

import (
	"sync"
	"time"
)

// Limiter is a per-key token bucket. Time is passed in, so it is deterministic to test.
// The key space is attacker controlled (device names are unauthenticated at this point),
// so the map is bounded: idle buckets are swept, and beyond maxKeys new keys are refused.
type Limiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	maxKeys int
	buckets map[string]*bucket
	swept   time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

// NewLimiter allows rate messages per second per key with the given burst.
func NewLimiter(rate float64, burst, maxKeys int) *Limiter {
	return &Limiter{rate: rate, burst: float64(burst), maxKeys: maxKeys, buckets: map[string]*bucket{}}
}

// Allow consumes a token for key at time now. When refused it returns how long to wait.
func (l *Limiter) Allow(key string, now time.Time) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.swept) > time.Minute {
		l.sweep(now)
	}
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= l.maxKeys {
			return false, time.Minute
		}
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.at).Seconds()*l.rate)
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// sweep drops buckets that would be full again anyway.
func (l *Limiter) sweep(now time.Time) {
	l.swept = now
	idle := time.Duration(l.burst/l.rate*float64(time.Second)) + time.Second
	for k, b := range l.buckets {
		if now.Sub(b.at) > idle {
			delete(l.buckets, k)
		}
	}
}
