package ingest

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func TestLimiterBurstRefillAndRetryAfter(t *testing.T) {
	l := NewLimiter(2, 3, 100) // 2 per second, burst 3
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("d", t0); !ok {
			t.Fatalf("burst message %d refused", i+1)
		}
	}
	ok, after := l.Allow("d", t0)
	if ok || after != 500*time.Millisecond {
		t.Fatalf("4th message: ok=%v retryAfter=%v, want refused and 500ms", ok, after)
	}
	if ok, _ := l.Allow("d", t0.Add(500*time.Millisecond)); !ok {
		t.Fatal("one token must have refilled after 500 ms")
	}
	if ok, _ := l.Allow("other", t0); !ok {
		t.Fatal("keys are independent")
	}
	later := t0.Add(time.Hour)
	for i := 0; i < 3; i++ {
		l.Allow("d", later)
	}
	if ok, _ := l.Allow("d", later); ok {
		t.Fatal("tokens must not accumulate beyond the burst")
	}
}

func TestLimiterBoundsItsKeySpace(t *testing.T) {
	l := NewLimiter(1, 1, 3)
	for _, k := range []string{"a", "b", "c"} {
		if ok, _ := l.Allow(k, t0); !ok {
			t.Fatalf("%s refused", k)
		}
	}
	if ok, _ := l.Allow("d", t0); ok {
		t.Fatal("beyond maxKeys new keys are refused (bounded memory under a name flood)")
	}
	if ok, _ := l.Allow("d", t0.Add(10*time.Minute)); !ok {
		t.Fatal("idle buckets must be swept, freeing room")
	}
}
