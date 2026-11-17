// Copyright (c) 2026 Vassel Goleyu. MIT License.

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// SourceEvent is one reading event of a site, as published by the ingest pipeline.
type SourceEvent struct {
	SiteID uuid.UUID
	Data   []byte
}

// EventSource delivers site events (Redis pub/sub in production).
type EventSource interface {
	Subscribe(ctx context.Context) (<-chan SourceEvent, error)
}

// RedisSource subscribes to spillway:site:* (one event per newly stored reading).
type RedisSource struct{ C redis.UniversalClient }

// Subscribe starts forwarding; the channel closes when ctx ends. The Redis client reconnects by itself.
func (r RedisSource) Subscribe(ctx context.Context) (<-chan SourceEvent, error) {
	ps := r.C.PSubscribe(ctx, "spillway:site:*")
	if _, err := ps.Receive(ctx); err != nil {
		_ = ps.Close()
		return nil, fmt.Errorf("subscribe to site events: %w", err)
	}
	out := make(chan SourceEvent, 1024)
	go func() {
		defer close(out)
		defer func() { _ = ps.Close() }()
		in := ps.Channel(redis.WithChannelSize(4096))
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-in:
				if !ok {
					return
				}
				id, err := uuid.Parse(strings.TrimPrefix(m.Channel, "spillway:site:"))
				if err != nil {
					continue
				}
				select {
				case out <- SourceEvent{SiteID: id, Data: []byte(m.Payload)}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

type hubEvent struct {
	id   uint64
	slug string
	data []byte
}

type hubClient struct {
	slug string // "" means every public site
	ch   chan hubEvent
}

// Hub fans site events out to the SSE clients of this process. Only is_public sites are ever
// forwarded, low quality readings are dropped, and the payload is rebuilt (no internal fields).
type Hub struct {
	src    EventSource
	lookup func(ctx context.Context) (map[uuid.UUID]string, error) // public site id -> slug
	epoch  string
	// Heartbeat is the interval of ": ping" comments that keep proxies from closing idle streams.
	Heartbeat time.Duration

	mu      sync.Mutex
	clients map[*hubClient]struct{}
	ring    []hubEvent
	seq     uint64
	slugs   map[uuid.UUID]string
	slugsAt time.Time
	tried   time.Time
	closed  bool
	now     func() time.Time
}

// NewHub creates a hub; call Run in a goroutine.
func NewHub(src EventSource, lookup func(ctx context.Context) (map[uuid.UUID]string, error)) *Hub {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &Hub{src: src, lookup: lookup, epoch: hex.EncodeToString(b), clients: map[*hubClient]struct{}{}, now: time.Now}
}

type inEvent struct {
	DeviceID string  `json:"device_id"`
	TS       int64   `json:"ts"`
	LevelCm  float64 `json:"level_cm"`
	Score    float64 `json:"quality_score"`
	Late     bool    `json:"late"`
}

// Run forwards events until ctx ends, then disconnects every client.
func (h *Hub) Run(ctx context.Context) error {
	defer h.closeAll()
	ch, err := h.src.Subscribe(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			h.handle(ctx, ev)
		}
	}
}

func (h *Hub) slugOf(ctx context.Context, id uuid.UUID) (string, bool) {
	h.mu.Lock()
	now := h.now()
	slug, ok := h.slugs[id]
	stale := now.Sub(h.slugsAt) > 30*time.Second
	retry := !ok && now.Sub(h.tried) > 5*time.Second // a site made public a moment ago
	if stale || retry {
		h.tried = now
		h.mu.Unlock()
		m, err := h.lookup(ctx)
		h.mu.Lock()
		if err == nil {
			h.slugs, h.slugsAt = m, now
			slug, ok = m[id]
		}
	}
	h.mu.Unlock()
	return slug, ok
}

func (h *Hub) handle(ctx context.Context, ev SourceEvent) {
	slug, ok := h.slugOf(ctx, ev.SiteID)
	if !ok {
		return // private site: never leaves the process
	}
	var in inEvent
	if json.Unmarshal(ev.Data, &in) != nil || in.Score < 0.5 {
		return
	}
	data, _ := json.Marshal(map[string]any{"site": slug, "device_id": in.DeviceID, "ts": in.TS, "level_cm": in.LevelCm, "late": in.Late})
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.seq++
	e := hubEvent{id: h.seq, slug: slug, data: data}
	h.ring = append(h.ring, e)
	if len(h.ring) > 256 {
		h.ring = h.ring[len(h.ring)-256:]
	}
	for c := range h.clients {
		if c.slug != "" && c.slug != slug {
			continue
		}
		select {
		case c.ch <- e:
		default: // too slow: drop it; it reconnects and replays with Last-Event-ID
			delete(h.clients, c)
			close(c.ch)
		}
	}
}

// subscribe registers a client and returns the events it missed since lastEventID.
func (h *Hub) subscribe(slug, lastEventID string) (*hubClient, []hubEvent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, nil, false
	}
	c := &hubClient{slug: slug, ch: make(chan hubEvent, 64)}
	h.clients[c] = struct{}{}
	var replay []hubEvent
	if ep, n, ok := strings.Cut(lastEventID, "-"); ok && ep == h.epoch {
		if last, err := strconv.ParseUint(n, 10, 64); err == nil {
			for _, e := range h.ring {
				if e.id > last && (slug == "" || e.slug == slug) {
					replay = append(replay, e)
				}
			}
		}
	}
	return c, replay, true
}

func (h *Hub) unsubscribe(c *hubClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.ch)
	}
}

// Clients returns the number of connected SSE clients.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *Hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for c := range h.clients {
		delete(h.clients, c)
		close(c.ch)
	}
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// publicStream is GET /v1/public/stream: server-sent events for one public site (?site=slug) or all.
func (s *Server) publicStream(w http.ResponseWriter, r *http.Request) {
	if s.Hub == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "streaming is not available")
		return
	}
	slug := r.URL.Query().Get("site")
	if slug != "" {
		if !slugRe.MatchString(slug) || !s.isPublic(r.Context(), slug) {
			writeErr(w, http.StatusNotFound, "not_found", "not found")
			return
		}
	}
	ip := s.clientIP(r)
	if !s.sseAcquire(ip) {
		w.Header().Set("Retry-After", "5")
		writeErr(w, http.StatusTooManyRequests, "too_many_streams", "too many open streams")
		return
	}
	defer s.sseRelease(ip)

	c, replay, ok := s.Hub.subscribe(slug, r.Header.Get("Last-Event-ID"))
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "shutting down")
		return
	}
	defer s.Hub.unsubscribe(c)

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // the server's WriteTimeout must not cut a stream
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(e hubEvent) bool {
		if _, err := fmt.Fprintf(w, "id: %s-%d\nevent: reading\ndata: %s\n\n", s.Hub.epoch, e.id, e.data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if _, err := fmt.Fprint(w, "retry: 5000\n\n"); err != nil || rc.Flush() != nil {
		return
	}
	for _, e := range replay {
		if !write(e) {
			return
		}
	}
	hb := s.Hub.Heartbeat
	if hb == 0 {
		hb = 15 * time.Second
	}
	tick := time.NewTicker(hb)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, open := <-c.ch:
			if !open || !write(e) {
				return
			}
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

func (s *Server) sseAcquire(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sseIP[ip] >= s.Cfg.MaxSSEPerIP || s.sseTotal >= 2000 {
		return false
	}
	s.sseIP[ip]++
	s.sseTotal++
	return true
}

func (s *Server) sseRelease(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sseIP[ip]--; s.sseIP[ip] <= 0 {
		delete(s.sseIP, ip)
	}
	s.sseTotal--
}
