package ingest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/sellebeww/spillway/internal/simulate"
)

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/ingest", strings.NewReader(body)))
	return rec
}

func bodyOf(r *rig, i int, fc uint32, age time.Duration) string {
	d := r.devs[i]
	return string(simulate.Body(d.Payload("s", fc, t0.Add(-age), 300, nil)))
}

func TestHTTPStatusCodes(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	r := newRig(t, 2, testConfig(), nil, nil)
	h := r.p.HTTPHandler(NewLimiter(1000, 1000, 1000), 1024, 5*time.Second)

	rec := post(t, h, bodyOf(r, 0, 1, time.Second))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"stored"`) {
		t.Fatalf("first post: %d %s", rec.Code, rec.Body)
	}
	if r.be.stored() != 1 {
		t.Fatal("a 200 must mean the reading is already committed")
	}
	if rec = post(t, h, bodyOf(r, 0, 1, time.Second)); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"duplicate"`) {
		t.Fatalf("repeat: %d %s", rec.Code, rec.Body)
	}

	badSig := bodyOf(r, 0, 2, time.Second)
	badSig = badSig[:len(badSig)-10] + "ffffffff\"}" // same length, wrong signature
	ghost := `{"device":"ghost","sid":"s","fc":1,"ts":1780000000,"raw_mm":2000,"sig":"` + strings.Repeat("0", 64) + `"}`
	a, b := post(t, h, badSig), post(t, h, ghost)
	if a.Code != 401 || b.Code != 401 || a.Body.String() != b.Body.String() {
		t.Fatalf("bad signature and unknown device must be indistinguishable: %d %q / %d %q", a.Code, a.Body, b.Code, b.Body)
	}
	if rec = post(t, h, "garbage"); rec.Code != 400 {
		t.Fatalf("garbage: %d", rec.Code)
	}
	if rec = post(t, h, strings.Repeat("x", 2048)); rec.Code != 413 {
		t.Fatalf("oversize: %d", rec.Code)
	}
	if rec = post(t, h, bodyOf(r, 0, 3, 25*time.Hour)); rec.Code != 422 {
		t.Fatalf("too old: %d", rec.Code)
	}
	var out map[string]string
	if err := json.Unmarshal(a.Body.Bytes(), &out); err != nil || out["error"] != "unauthorized" {
		t.Fatalf("error body: %s", a.Body)
	}
	r.close()
	for reason, want := range map[string]float64{ReasonSig: 1, ReasonUnknownDevice: 1, ReasonSchema: 2, ReasonTooOld: 1} {
		if v := metric(t, r.reg, "spillway_ingest_rejected_total", map[string]string{"reason": reason}); v != want {
			t.Errorf("rejected{%s} = %v, want %v", reason, v, want)
		}
	}
	if v := metric(t, r.reg, "spillway_ingest_received_total", map[string]string{"source": "http"}); v != 7 {
		t.Errorf("received{http} = %v, want 7", v)
	}
}

func TestHTTPRateLimitPerDevice(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	r := newRig(t, 2, testConfig(), nil, nil)
	h := r.p.HTTPHandler(NewLimiter(1, 2, 100), 1024, 5*time.Second)
	for fc := uint32(1); fc <= 2; fc++ {
		if rec := post(t, h, bodyOf(r, 0, fc, time.Second)); rec.Code != 200 {
			t.Fatalf("burst message %d: %d", fc, rec.Code)
		}
	}
	rec := post(t, h, bodyOf(r, 0, 3, time.Second))
	if secs, err := strconv.Atoi(rec.Header().Get("Retry-After")); rec.Code != 429 || err != nil || secs < 1 {
		t.Fatalf("third message: %d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if rec := post(t, h, bodyOf(r, 1, 1, time.Second)); rec.Code != 200 {
		t.Fatalf("another device is not affected: %d", rec.Code)
	}
	r.close()
	if v := metric(t, r.reg, "spillway_ingest_rejected_total", map[string]string{"reason": ReasonRateLimit}); v != 1 {
		t.Fatalf("rate limit metric %v", v)
	}
}

func TestHTTPBusyAnswers503WithRetryAfter(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	cfg := testConfig()
	cfg.Shards, cfg.QueueSize, cfg.WriterQueue, cfg.BatchSize = 1, 1, 1, 1
	gate := make(chan struct{})
	r := newRig(t, 1, cfg, func(b *fakeBackend) { b.gate = gate }, nil)
	h := r.p.HTTPHandler(nil, 1024, 80*time.Millisecond)

	// A request that is accepted but cannot commit in time: 503 and "try again", not a lie.
	rec := post(t, h, bodyOf(r, 0, 1, time.Second))
	if rec.Code != 503 || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("timeout: %d %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	// Fill the queue, then the handler must refuse immediately.
	for fc := uint32(2); fc < 60; fc++ {
		_ = r.p.Submit(context.Background(), r.msg(0, "s", fc, time.Second, 2000+int(fc)), false)
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	rec = post(t, h, bodyOf(r, 0, 100, time.Second))
	if rec.Code != 503 || rec.Header().Get("Retry-After") != "1" || time.Since(start) > 60*time.Millisecond {
		t.Fatalf("backlog: %d Retry-After=%q after %v", rec.Code, rec.Header().Get("Retry-After"), time.Since(start))
	}
	close(gate)
	r.close()
}
