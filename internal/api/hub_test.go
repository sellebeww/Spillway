package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/goleak"
)

type fakeSource struct{ ch chan SourceEvent }

func (f *fakeSource) Subscribe(context.Context) (<-chan SourceEvent, error) { return f.ch, nil }

type sseRig struct {
	t      *testing.T
	hub    *Hub
	srv    *Server
	ts     *httptest.Server
	src    *fakeSource
	pubID  uuid.UUID
	privID uuid.UUID
	cancel context.CancelFunc
	done   chan struct{}
	client *http.Client
}

func newSSERig(t *testing.T) *sseRig {
	pub, priv := uuid.New(), uuid.New()
	src := &fakeSource{ch: make(chan SourceEvent, 64)}
	hub := NewHub(src, func(context.Context) (map[uuid.UUID]string, error) {
		return map[uuid.UUID]string{pub: "kali-contoh"}, nil
	})
	hub.Heartbeat = 40 * time.Millisecond
	srv := NewServer(nil, nil, nil, hub, ServerConfig{MaxSSEPerIP: 2}, nil)
	r := chi.NewRouter()
	srv.Mount(r)
	ctx, cancel := context.WithCancel(context.Background())
	g := &sseRig{t: t, hub: hub, srv: srv, src: src, pubID: pub, privID: priv, cancel: cancel, done: make(chan struct{}),
		client: &http.Client{Transport: &http.Transport{}}}
	go func() { defer close(g.done); _ = hub.Run(ctx) }()
	g.ts = httptest.NewServer(r)
	return g
}

func (g *sseRig) close() {
	g.cancel()
	<-g.done
	g.ts.Close()
	g.client.CloseIdleConnections()
}

func (g *sseRig) emit(site uuid.UUID, level, score float64) {
	b, _ := json.Marshal(map[string]any{"device": "secret-device-key", "device_id": uuid.NewString(), "ts": time.Now().Unix(), "level_cm": level, "quality_score": score, "late": false, "flags": []string{"OK"}})
	g.src.ch <- SourceEvent{SiteID: site, Data: b}
}

type stream struct {
	lines chan string
	stop  context.CancelFunc
}

func (g *sseRig) connect(query, lastID string) (*stream, int) {
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", g.ts.URL+"/v1/public/stream"+query, nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := g.client.Do(req) //nolint:bodyclose // closed by the reader goroutine below
	if err != nil {
		cancel()
		g.t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		_ = resp.Body.Close()
		cancel()
		return nil, resp.StatusCode
	}
	s := &stream{lines: make(chan string, 256), stop: cancel}
	go func() {
		defer close(s.lines)
		defer func() { _ = resp.Body.Close() }()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			s.lines <- sc.Text()
		}
	}()
	return s, 200
}

// next returns the next data event (id, payload), or ok=false after the timeout. Comments and
// retry lines are skipped but heartbeats are counted through the returned pings.
func (s *stream) next(timeout time.Duration) (id, data string, pings int, ok bool) {
	deadline := time.After(timeout)
	for {
		select {
		case l, open := <-s.lines:
			if !open {
				return "", "", pings, false
			}
			switch {
			case l == ": ping":
				pings++
			case strings.HasPrefix(l, "id: "):
				id = strings.TrimPrefix(l, "id: ")
			case strings.HasPrefix(l, "data: "):
				return id, strings.TrimPrefix(l, "data: "), pings, true
			}
		case <-deadline:
			return "", "", pings, false
		}
	}
}

func TestStreamForwardsOnlyPublicTrustedEvents(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	g := newSSERig(t)
	defer g.close()
	s, code := g.connect("", "")
	if code != 200 {
		t.Fatalf("connect: %d", code)
	}
	defer s.stop()
	time.Sleep(50 * time.Millisecond)

	g.emit(g.privID, 10, 1)   // a private site: must never be delivered
	g.emit(g.pubID, 11, 0.2)  // low quality: dropped
	g.emit(g.pubID, 123.4, 1) // delivered
	id, data, _, ok := s.next(2 * time.Second)
	if !ok {
		t.Fatal("no event received")
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(data), &ev); err != nil || ev["site"] != "kali-contoh" || ev["level_cm"] != 123.4 {
		t.Fatalf("event: %s %v", data, err)
	}
	if _, bad := ev["device"]; bad || strings.Contains(data, "secret-device-key") || strings.Contains(data, "quality_score") || strings.Contains(data, "flags") {
		t.Fatalf("the public payload must be rebuilt without internal fields: %s", data)
	}
	if !strings.Contains(id, "-") {
		t.Fatalf("event id %q", id)
	}
	if _, d2, _, more := s.next(150 * time.Millisecond); more {
		t.Fatalf("an event that must not be delivered arrived: %s", d2)
	}
}

func TestStreamHeartbeatReplayAndSiteFilter(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	g := newSSERig(t)
	defer g.close()

	s, _ := g.connect("", "")
	time.Sleep(50 * time.Millisecond)
	g.emit(g.pubID, 1, 1)
	id1, _, pings, ok := s.next(2 * time.Second)
	if !ok {
		t.Fatal("first event")
	}
	g.emit(g.pubID, 2, 1)
	if _, _, p2, ok := s.next(2 * time.Second); !ok || pings+p2 < 0 {
		t.Fatal("second event")
	}
	// Heartbeat: an idle stream keeps sending comments.
	idle := 0
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
		_, _, n, _ := s.next(100 * time.Millisecond)
		idle += n
	}
	if idle == 0 {
		t.Fatal("no heartbeat on an idle stream")
	}
	s.stop()

	// While disconnected two more events arrive; reconnecting with Last-Event-ID replays what was missed.
	g.emit(g.pubID, 3, 1)
	g.emit(g.pubID, 4, 1)
	time.Sleep(100 * time.Millisecond)
	r, code := g.connect("", id1)
	if code != 200 {
		t.Fatal(code)
	}
	var levels []float64
	for i := 0; i < 3; i++ {
		_, data, _, ok := r.next(2 * time.Second)
		if !ok {
			t.Fatalf("replay stopped after %v", levels)
		}
		var ev map[string]any
		_ = json.Unmarshal([]byte(data), &ev)
		levels = append(levels, ev["level_cm"].(float64))
	}
	r.stop()
	if levels[0] != 2 || levels[1] != 3 || levels[2] != 4 {
		t.Fatalf("replay must deliver exactly the missed events in order: %v", levels)
	}
	// A foreign epoch (the server restarted) replays nothing, and a malformed id is harmless.
	for _, bad := range []string{"deadbeef-1", "garbage", "-", "x-y"} {
		c, _ := g.connect("", bad)
		if _, _, _, got := c.next(150 * time.Millisecond); got {
			t.Fatalf("Last-Event-ID %q must not replay", bad)
		}
		c.stop()
	}
	// Per-site filtering at the hub (the site must be known as public for the HTTP route).
	a, _, _ := g.hub.subscribe("other-site", "")
	g.emit(g.pubID, 5, 1)
	time.Sleep(100 * time.Millisecond)
	select {
	case <-a.ch:
		t.Fatal("a client of another site received an event")
	default:
	}
	g.hub.unsubscribe(a)
}

func TestStreamLimitsConnectionsPerIPAndCleansUp(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	g := newSSERig(t)
	defer g.close()
	s1, c1 := g.connect("", "")
	s2, c2 := g.connect("", "")
	if c1 != 200 || c2 != 200 {
		t.Fatalf("%d %d", c1, c2)
	}
	if _, code := g.connect("", ""); code != 429 {
		t.Fatalf("the third stream from one IP must be refused, got %d", code)
	}
	waitUntil(t, func() bool { return g.hub.Clients() == 2 })
	s1.stop() // a client that goes away
	waitUntil(t, func() bool { return g.hub.Clients() == 1 })
	if s3, code := g.connect("", ""); code != 200 {
		t.Fatalf("a slot must be free again after a disconnect: %d", code)
	} else {
		s3.stop()
	}
	s2.stop()
	waitUntil(t, func() bool { return g.hub.Clients() == 0 })
	g.srv.mu.Lock()
	left := len(g.srv.sseIP) + g.srv.sseTotal
	g.srv.mu.Unlock()
	if left != 0 {
		t.Fatalf("connection bookkeeping leaked: %d", left)
	}
	if _, code := g.connect("?site=Not_A_Slug", ""); code != 404 {
		t.Fatalf("invalid site filter: %d", code)
	}
}

func TestStreamEndsWhenTheHubStops(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	g := newSSERig(t)
	s, _ := g.connect("", "")
	waitUntil(t, func() bool { return g.hub.Clients() == 1 })
	g.cancel() // shutdown: open streams must not keep the HTTP server from stopping
	for {
		_, _, _, ok := s.next(3 * time.Second)
		if !ok {
			break
		}
	}
	waitUntil(t, func() bool { return g.hub.Clients() == 0 })
	s.stop()
	g.close()
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(end) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
