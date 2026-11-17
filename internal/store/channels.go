// Copyright (c) 2026 Vassel Goleyu. MIT License.

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sellebeww/spillway/internal/auth"
)

const channelCols = `id, org_id, kind, chat_id, is_active, created_at`

// UpsertChannel registers a Telegram chat, or reactivates it if it already exists.
func (o *OrgStore) UpsertChannel(ctx context.Context, kind, chatID string) (Channel, error) {
	var ch Channel
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		ch, err = fetchOne[Channel](ctx, o, tx, `INSERT INTO channels (org_id, kind, chat_id) VALUES ($1, $2, $3)
			ON CONFLICT (org_id, kind, chat_id) DO UPDATE SET is_active = true RETURNING `+channelCols, kind, chatID)
		if err != nil {
			return err
		}
		return o.audit(ctx, tx, "channel.upsert", "channel", ch.ID, nil, map[string]any{"kind": ch.Kind, "is_active": ch.IsActive})
	})
	return ch, err
}

// GetChannelByChat finds a channel by its chat id.
func (o *OrgStore) GetChannelByChat(ctx context.Context, kind, chatID string) (Channel, error) {
	return fetchOne[Channel](ctx, o, o.s.pool, `SELECT `+channelCols+` FROM channels WHERE org_id = $1 AND kind = $2 AND chat_id = $3`, kind, chatID)
}

// DeactivateChannel stops deliveries to a channel (for example after Telegram answers 403).
func (o *OrgStore) DeactivateChannel(ctx context.Context, id uuid.UUID) error {
	return o.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := o.exec(ctx, tx, `UPDATE channels SET is_active = false WHERE org_id = $1 AND id = $2`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return o.audit(ctx, tx, "channel.deactivate", "channel", id, nil, map[string]any{"is_active": false})
	})
}

// Subscribe links a channel to a site (idempotent).
func (o *OrgStore) Subscribe(ctx context.Context, siteID, channelID uuid.UUID) error {
	return o.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := o.exec(ctx, tx, `INSERT INTO subscriptions (org_id, site_id, channel_id)
			SELECT $1, s.id, c.id FROM sites s, channels c WHERE s.org_id = $1 AND s.id = $2 AND c.org_id = $1 AND c.id = $3
			ON CONFLICT (site_id, channel_id) DO NOTHING`, siteID, channelID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 { // already subscribed is fine; a foreign site or channel is not
			if err := o.requireOwnedTx(ctx, tx, `SELECT 1 FROM sites s, channels c WHERE s.org_id = $1 AND s.id = $2 AND c.org_id = $1 AND c.id = $3`, siteID, channelID); err != nil {
				return err
			}
		}
		return o.audit(ctx, tx, "subscription.add", "site", siteID, nil, map[string]any{"channel_id": channelID})
	})
}

// Unsubscribe removes the link between a site and a channel.
func (o *OrgStore) Unsubscribe(ctx context.Context, siteID, channelID uuid.UUID) error {
	return o.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := o.exec(ctx, tx, `DELETE FROM subscriptions WHERE org_id = $1 AND site_id = $2 AND channel_id = $3`, siteID, channelID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return o.audit(ctx, tx, "subscription.remove", "site", siteID, map[string]any{"channel_id": channelID}, nil)
	})
}

// ListSubscribers returns the active channels subscribed to a site.
func (o *OrgStore) ListSubscribers(ctx context.Context, siteID uuid.UUID) ([]Channel, error) {
	return fetchAll[Channel](ctx, o, o.s.pool, `SELECT c.id, c.org_id, c.kind, c.chat_id, c.is_active, c.created_at
		FROM subscriptions s JOIN channels c ON c.id = s.channel_id AND c.org_id = s.org_id
		WHERE s.org_id = $1 AND s.site_id = $2 AND c.is_active ORDER BY c.created_at, c.id`, siteID)
}

// ListChannelSites returns the sites a channel is subscribed to.
func (o *OrgStore) ListChannelSites(ctx context.Context, channelID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := fetchAll[struct {
		SiteID uuid.UUID `db:"site_id"`
	}](ctx, o, o.s.pool, `SELECT site_id FROM subscriptions WHERE org_id = $1 AND channel_id = $2 ORDER BY created_at, id`, channelID)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		out[i] = r.SiteID
	}
	return out, nil
}

// CreateSubscriptionToken issues a single-use deep-link token for a site. The plaintext
// token is returned once; only its SHA-256 is stored.
func (o *OrgStore) CreateSubscriptionToken(ctx context.Context, siteID uuid.UUID, ttl time.Duration, now time.Time) (string, time.Time, error) {
	if ttl <= 0 || ttl > 30*24*time.Hour {
		return "", time.Time{}, fmt.Errorf("%w: token ttl must be within (0, 30 days]", ErrInvalid)
	}
	token, hash, err := auth.GenerateSubscriptionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := now.Add(ttl)
	actor := actorFrom(ctx)
	err = o.inTx(ctx, func(tx pgx.Tx) error {
		id, err := fetchOne[struct {
			ID uuid.UUID `db:"id"`
		}](ctx, o, tx, `INSERT INTO subscription_tokens (org_id, site_id, token_hash, expires_at, created_by)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, siteID, hash, expires, actor.UserID)
		if err != nil {
			return err
		}
		return o.audit(ctx, tx, "subscription_token.create", "site", siteID, nil, map[string]any{"token_id": id.ID, "expires_at": expires})
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// RedeemSubscriptionToken consumes a token (single use, not expired) and returns the
// organization and site it was issued for. The caller knows only the token, so this lookup
// cannot be organization-scoped; the 192-bit random token is the credential.
func (s *Store) RedeemSubscriptionToken(ctx context.Context, token string, now time.Time) (orgID, siteID uuid.UUID, err error) {
	rows, err := s.pool.Query(ctx, `-- unscoped: a deep-link token identifies its own organization
		UPDATE subscription_tokens SET used_at = $2
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2 RETURNING org_id, site_id`, auth.HashToken(token), now)
	if err != nil {
		return uuid.Nil, uuid.Nil, mapErr(err)
	}
	r, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[struct {
		OrgID  uuid.UUID `db:"org_id"`
		SiteID uuid.UUID `db:"site_id"`
	}])
	if err != nil {
		return uuid.Nil, uuid.Nil, mapErr(err)
	}
	return r.OrgID, r.SiteID, nil
}

const notifCols = `id, org_id, alert_event_id, channel_id, kind, seq, status, attempts, next_attempt_at, last_error, payload`

// EnqueueNotification adds an outbox row. It reports false when the same
// (event, kind, channel, seq) was already enqueued, which makes delivery idempotent.
func (o *OrgStore) EnqueueNotification(ctx context.Context, in NotificationInput) (bool, error) {
	payload := in.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	tag, err := o.exec(ctx, o.s.pool, `INSERT INTO notifications_outbox (org_id, alert_event_id, kind, seq, channel_id, payload)
		SELECT $1, e.id, $3, $4, c.id, $6 FROM alert_events e, channels c
		WHERE e.org_id = $1 AND e.id = $2 AND c.org_id = $1 AND c.id = $5
		ON CONFLICT (alert_event_id, kind, channel_id, seq) DO NOTHING`,
		in.EventID, in.Kind, in.Seq, in.ChannelID, payload)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		// A duplicate, or an event/channel of another organization: only the former may answer "false".
		if err := o.requireOwned(ctx, `SELECT 1 FROM alert_events e, channels c WHERE e.org_id = $1 AND e.id = $2 AND c.org_id = $1 AND c.id = $3`, in.EventID, in.ChannelID); err != nil {
			return false, err
		}
	}
	return tag.RowsAffected() == 1, nil
}

// GetNotification returns one outbox row.
func (o *OrgStore) GetNotification(ctx context.Context, id int64) (Notification, error) {
	return fetchOne[Notification](ctx, o, o.s.pool, `SELECT `+notifCols+` FROM notifications_outbox WHERE org_id = $1 AND id = $2`, id)
}

// ClaimDueNotifications leases up to limit due rows for lease seconds (attempts is incremented
// and next_attempt_at pushed out, so a crashed worker's rows come back). Several workers may
// call it concurrently. It serves every organization, so it is not organization-scoped.
func (s *Store) ClaimDueNotifications(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]Notification, error) {
	rows, err := s.pool.Query(ctx, `-- unscoped: the delivery worker serves every organization
		UPDATE notifications_outbox o SET attempts = o.attempts + 1,
			next_attempt_at = $1::timestamptz + make_interval(secs => $2)
		WHERE o.id IN (SELECT id FROM notifications_outbox WHERE status = 'PENDING' AND next_attempt_at <= $1
			ORDER BY next_attempt_at, id LIMIT $3 FOR UPDATE SKIP LOCKED)
		RETURNING o.id, o.org_id, o.alert_event_id, o.channel_id, o.kind, o.seq, o.status, o.attempts, o.next_attempt_at, o.last_error, o.payload`,
		now, lease.Seconds(), clampLimit(limit, 10, 100))
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[Notification])
	return out, mapErr(err)
}

func (o *OrgStore) finishNotification(ctx context.Context, id int64, sql string, args ...any) error {
	tag, err := o.exec(ctx, o.s.pool, sql, append([]any{id}, args...)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// MarkNotificationSent records a successful delivery.
func (o *OrgStore) MarkNotificationSent(ctx context.Context, id int64, at time.Time) error {
	return o.finishNotification(ctx, id, `UPDATE notifications_outbox SET status = 'SENT', sent_at = $3, last_error = NULL
		WHERE org_id = $1 AND id = $2 AND status = 'PENDING'`, at)
}

// MarkNotificationRetry schedules another attempt (backoff, or Telegram's retry_after).
// errMsg must not contain tokens or message bodies.
func (o *OrgStore) MarkNotificationRetry(ctx context.Context, id int64, next time.Time, errMsg string) error {
	return o.finishNotification(ctx, id, `UPDATE notifications_outbox SET next_attempt_at = $3, last_error = $4
		WHERE org_id = $1 AND id = $2 AND status = 'PENDING'`, next, truncate(errMsg, 500))
}

// MarkNotificationFailed gives up on a notification.
func (o *OrgStore) MarkNotificationFailed(ctx context.Context, id int64, errMsg string) error {
	return o.finishNotification(ctx, id, `UPDATE notifications_outbox SET status = 'FAILED', last_error = $3
		WHERE org_id = $1 AND id = $2 AND status = 'PENDING'`, truncate(errMsg, 500))
}

// requireOwned returns ErrNotFound unless the (organization-scoped) existence query yields a row.
func (o *OrgStore) requireOwned(ctx context.Context, sql string, args ...any) error {
	return o.requireOwnedTx(ctx, o.s.pool, sql, args...)
}

func (o *OrgStore) requireOwnedTx(ctx context.Context, q querier, sql string, args ...any) error {
	rows, err := o.query(ctx, q, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return ErrNotFound
	}
	return rows.Err()
}
