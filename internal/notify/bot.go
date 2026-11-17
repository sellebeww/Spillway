// Copyright (c) 2026 Vassel Goleyu. MIT License.

package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/sellebeww/spillway/internal/quality"
	"github.com/sellebeww/spillway/internal/store"
)

const channelKind = "telegram"

// Bot serves the Telegram commands by long polling getUpdates (no webhook). The update offset is
// persisted, so a restart neither replays old commands nor skips new ones.
type Bot struct {
	TG          *Client
	St          *store.Store
	Name        string // key of the persisted offset, default "telegram"
	PollSeconds int    // long-poll timeout, default 25
	Now         func() time.Time
	Log         *slog.Logger
}

func (b *Bot) defaults() {
	if b.Name == "" {
		b.Name = "telegram"
	}
	if b.PollSeconds == 0 {
		b.PollSeconds = 25
	}
	if b.Now == nil {
		b.Now = time.Now
	}
	if b.Log == nil {
		b.Log = slog.New(slog.DiscardHandler)
	}
}

// Run polls until ctx ends. Errors back off (and obey Telegram's retry_after); the loop never exits on them.
func (b *Bot) Run(ctx context.Context) error {
	b.defaults()
	offset, err := b.St.BotOffset(ctx, b.Name)
	if err != nil {
		return fmt.Errorf("load bot offset: %w", err)
	}
	backoff := time.Second
	for ctx.Err() == nil {
		ups, err := b.TG.GetUpdates(ctx, offset, b.PollSeconds)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			wait := backoff
			var api *APIError
			if errors.As(err, &api) && api.RetryAfter > 0 {
				wait = api.RetryAfter
			}
			b.Log.Warn("telegram getUpdates failed", "err", err, "wait", wait)
			if !sleep(ctx, wait) {
				return nil
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		for _, u := range ups {
			if herr := b.HandleUpdate(ctx, u); herr != nil {
				b.Log.Warn("handling update failed", "err", herr)
			}
			offset = u.UpdateID + 1
			if err := b.St.SetBotOffset(ctx, b.Name, offset); err != nil {
				b.Log.Error("persist bot offset failed", "err", err)
			}
		}
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// HandleUpdate processes one update: /start <token>, /stop, /status.
func (b *Bot) HandleUpdate(ctx context.Context, u Update) error {
	b.defaults()
	if u.Message == nil || u.Message.Text == "" {
		return nil
	}
	chat := strconv.FormatInt(u.Message.Chat.ID, 10)
	fields := strings.Fields(u.Message.Text)
	cmd, _, _ := strings.Cut(strings.ToLower(fields[0]), "@") // "/start@MyBot" in groups
	arg := ""
	if len(fields) > 1 {
		arg = fields[1]
	}
	var text string
	var err error
	switch cmd {
	case "/start":
		text, err = b.start(ctx, chat, arg)
	case "/stop":
		text, err = b.stop(ctx, chat)
	case "/status":
		text, err = b.status(ctx, chat)
	default:
		return nil // ignore chatter
	}
	if err != nil {
		b.Log.Error("command failed", "cmd", cmd, "err", err)
		text = msgError
	}
	return b.TG.SendMessage(ctx, chat, text)
}

func (b *Bot) start(ctx context.Context, chat, token string) (string, error) {
	if token == "" {
		return msgHelp + SafetyLine, nil
	}
	org, siteID, err := b.St.RedeemSubscriptionToken(ctx, token, b.Now())
	if errors.Is(err, store.ErrNotFound) {
		return msgBadToken, nil
	}
	if err != nil {
		return "", err
	}
	ctx = store.WithActor(ctx, store.Actor{Label: "telegram"})
	o := b.St.ForOrg(org)
	ch, err := o.UpsertChannel(ctx, channelKind, chat)
	if err != nil {
		return "", err
	}
	if err := o.Subscribe(ctx, siteID, ch.ID); err != nil {
		return "", err
	}
	site, err := o.GetSite(ctx, siteID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(msgSubscribe, htmlEscape(site.Name)) + SafetyLine, nil
}

func (b *Bot) stop(ctx context.Context, chat string) (string, error) {
	chs, err := b.St.ChannelsByChat(ctx, channelKind, chat)
	if err != nil {
		return "", err
	}
	ctx = store.WithActor(ctx, store.Actor{Label: "telegram"})
	for _, ch := range chs {
		o := b.St.ForOrg(ch.OrgID)
		sites, err := o.ListChannelSites(ctx, ch.ID)
		if err != nil {
			return "", err
		}
		for _, s := range sites {
			if err := o.Unsubscribe(ctx, s, ch.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
				return "", err
			}
		}
		if ch.IsActive {
			if err := o.DeactivateChannel(ctx, ch.ID); err != nil {
				return "", err
			}
		}
	}
	return msgStopped, nil
}

func (b *Bot) status(ctx context.Context, chat string) (string, error) {
	chs, err := b.St.ChannelsByChat(ctx, channelKind, chat)
	if err != nil {
		return "", err
	}
	now := b.Now()
	var lines []string
	for _, ch := range chs {
		if !ch.IsActive {
			continue
		}
		o := b.St.ForOrg(ch.OrgID)
		siteIDs, err := o.ListChannelSites(ctx, ch.ID)
		if err != nil {
			return "", err
		}
		for _, sid := range siteIDs {
			site, err := o.GetSite(ctx, sid)
			if err != nil {
				return "", err
			}
			lines = append(lines, "<b>"+htmlEscape(site.Name)+"</b>")
			devs, err := o.ListDevices(ctx, &sid)
			if err != nil {
				return "", err
			}
			for _, d := range devs {
				last, err := o.LatestReading(ctx, d.ID)
				if errors.Is(err, store.ErrNotFound) {
					lines = append(lines, NoDataLine(d.Name))
					continue
				}
				if err != nil {
					return "", err
				}
				interval := time.Duration(d.ExpectedIntervalS) * time.Second
				lines = append(lines, StatusLine(d.Name, last.LevelCm, last.TS, site.TZ, quality.IsStaleReading(now, last.TS, last.ReceivedAt, interval)))
			}
		}
	}
	if len(lines) == 0 {
		return msgNoSub, nil
	}
	return strings.Join(lines, "\n") + "\n\n" + SafetyLine, nil
}
