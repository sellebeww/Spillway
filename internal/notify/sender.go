// Copyright (c) 2026 Vassel Goleyu. MIT License.

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sellebeww/spillway/internal/store"
)

func htmlEscape(s string) string { return html.EscapeString(s) }

// Metrics of the sender.
type Metrics struct {
	Sent        prometheus.Counter
	Retries     *prometheus.CounterVec // reason: rate_limited, transient
	Failed      *prometheus.CounterVec // reason: blocked, rejected, exhausted, inactive, bad_payload
	Deactivated prometheus.Counter
	Pending     prometheus.Gauge
	LagSeconds  prometheus.Gauge
}

// NewMetrics registers the sender metrics.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Sent:        prometheus.NewCounter(prometheus.CounterOpts{Name: "spillway_notify_sent_total", Help: "Notifications delivered."}),
		Retries:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "spillway_notify_retries_total", Help: "Deliveries rescheduled."}, []string{"reason"}),
		Failed:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "spillway_notify_failed_total", Help: "Notifications given up on."}, []string{"reason"}),
		Deactivated: prometheus.NewCounter(prometheus.CounterOpts{Name: "spillway_notify_channels_deactivated_total", Help: "Channels disabled after Telegram answered 403."}),
		Pending:     prometheus.NewGauge(prometheus.GaugeOpts{Name: "spillway_notify_outbox_pending", Help: "Pending outbox rows."}),
		LagSeconds:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "spillway_notify_outbox_lag_seconds", Help: "Age of the oldest pending outbox row."}),
	}
	reg.MustRegister(m.Sent, m.Retries, m.Failed, m.Deactivated, m.Pending, m.LagSeconds)
	return m
}

// Sender delivers outbox rows. Rows are claimed with FOR UPDATE SKIP LOCKED and a lease, so any
// number of workers (and processes) can run without sending a row twice; a crash before the row
// is marked sent makes it come back after the lease (at-least-once, with a window of one request).
type Sender struct {
	TG         *Client
	St         *store.Store
	PublicURL  string
	Log        *slog.Logger
	M          *Metrics
	Now        func() time.Time
	ChatEvery  time.Duration // minimum spacing per chat (default 1 s)
	GlobalRate time.Duration // minimum spacing overall (default 40 ms, about 25 per second)
	Lease      time.Duration // default 2 min
	Poll       time.Duration // idle poll interval (default 500 ms)
	MaxAttempt int           // default 8

	mu     sync.Mutex
	chat   map[string]time.Time
	global time.Time
}

func (s *Sender) defaults() {
	if s.ChatEvery == 0 {
		s.ChatEvery = time.Second
	}
	if s.GlobalRate == 0 {
		s.GlobalRate = 40 * time.Millisecond
	}
	if s.Lease == 0 {
		s.Lease = 2 * time.Minute
	}
	if s.Poll == 0 {
		s.Poll = 500 * time.Millisecond
	}
	if s.MaxAttempt == 0 {
		s.MaxAttempt = 8
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Log == nil {
		s.Log = slog.New(slog.DiscardHandler)
	}
	s.mu.Lock()
	if s.chat == nil {
		s.chat = map[string]time.Time{}
	}
	s.mu.Unlock()
}

// Run starts workers goroutines and blocks until ctx ends and they have finished their current row.
func (s *Sender) Run(ctx context.Context, workers int) {
	s.defaults()
	var wg sync.WaitGroup
	for i := 0; i < max(workers, 1); i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.loop(ctx) }()
	}
	if s.M != nil {
		wg.Add(1)
		go func() { defer wg.Done(); s.stats(ctx) }()
	}
	wg.Wait()
}

func (s *Sender) stats(ctx context.Context) {
	for {
		if pending, oldest, err := s.St.OutboxStats(ctx); err == nil {
			s.M.Pending.Set(float64(pending))
			lag := 0.0
			if oldest != nil {
				lag = s.Now().Sub(*oldest).Seconds()
			}
			s.M.LagSeconds.Set(max(lag, 0))
		}
		if !sleep(ctx, 10*time.Second) {
			return
		}
	}
}

func (s *Sender) loop(ctx context.Context) {
	for ctx.Err() == nil {
		items, err := s.St.ClaimDueNotifications(ctx, s.Now(), 5, s.Lease)
		if err != nil {
			if ctx.Err() == nil {
				s.Log.Warn("claim notifications failed", "err", err)
			}
			sleep(ctx, s.Poll)
			continue
		}
		if len(items) == 0 {
			sleep(ctx, s.Poll)
			continue
		}
		for _, it := range items {
			if ctx.Err() != nil {
				return // unfinished rows keep their lease and return after it expires
			}
			s.deliver(ctx, it)
		}
	}
}

// pace reserves a send slot honouring the per-chat and global spacing, then waits for it.
func (s *Sender) pace(ctx context.Context, chat string) bool {
	s.mu.Lock()
	now := s.Now()
	at := now
	for _, t := range []time.Time{s.chat[chat], s.global} {
		if t.After(at) {
			at = t
		}
	}
	s.chat[chat] = at.Add(s.ChatEvery)
	s.global = at.Add(s.GlobalRate)
	s.mu.Unlock()
	return at.Sub(now) <= 0 || sleep(ctx, at.Sub(now))
}

func backoff(attempts int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < attempts && d < 15*time.Minute; i++ {
		d *= 2
	}
	return min(d, 15*time.Minute)
}

func (s *Sender) deliver(ctx context.Context, n store.Notification) {
	o := s.St.ForOrg(n.OrgID)
	fail := func(reason, msg string) {
		if err := o.MarkNotificationFailed(ctx, n.ID, msg); err != nil {
			s.Log.Warn("mark failed", "err", err)
		}
		if s.M != nil {
			s.M.Failed.WithLabelValues(reason).Inc()
		}
	}
	ch, err := o.GetChannel(ctx, n.ChannelID)
	if err != nil || !ch.IsActive {
		fail("inactive", "channel missing or inactive")
		return
	}
	var p Payload
	if err := json.Unmarshal(n.Payload, &p); err != nil || p.Kind == "" {
		fail("bad_payload", "payload cannot be rendered")
		return
	}
	text := DelayNote(p, s.Now()) + RenderAlert(p, s.PublicURL)
	if !s.pace(ctx, ch.ChatID) {
		return
	}
	err = s.TG.SendMessage(ctx, ch.ChatID, text)
	if ctx.Err() != nil && err != nil {
		return // shutting down mid-request: the lease returns the row
	}
	now := s.Now()
	var api *APIError
	switch {
	case err == nil:
		if merr := o.MarkNotificationSent(ctx, n.ID, now); merr != nil {
			s.Log.Error("mark sent failed; the row may be sent again after its lease", "err", merr)
			return
		}
		if s.M != nil {
			s.M.Sent.Inc()
		}
	case errors.As(err, &api) && api.Code == 429:
		wait := api.RetryAfter
		if wait <= 0 {
			wait = backoff(n.Attempts)
		}
		s.mu.Lock() // everyone slows down, not only this chat
		s.global = now.Add(wait)
		s.mu.Unlock()
		_ = o.MarkNotificationRetry(ctx, n.ID, now.Add(wait+time.Second), "rate limited")
		if s.M != nil {
			s.M.Retries.WithLabelValues("rate_limited").Inc()
		}
	case errors.As(err, &api) && api.Code == 403: // the user blocked the bot or left the chat
		if derr := o.DeactivateChannel(store.WithActor(ctx, store.Actor{Label: "notify"}), ch.ID); derr != nil {
			s.Log.Warn("deactivate channel failed", "err", derr)
		}
		if s.M != nil {
			s.M.Deactivated.Inc()
		}
		fail("blocked", "bot blocked: channel deactivated")
	case errors.As(err, &api) && api.Code >= 400 && api.Code < 500:
		fail("rejected", api.Error())
	case n.Attempts >= s.MaxAttempt:
		fail("exhausted", "gave up after repeated errors")
	default:
		_ = o.MarkNotificationRetry(ctx, n.ID, now.Add(backoff(n.Attempts)), "transient error")
		if s.M != nil {
			s.M.Retries.WithLabelValues("transient").Inc()
		}
	}
}
