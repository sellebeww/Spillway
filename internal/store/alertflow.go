// Copyright (c) 2026 Vassel Goleyu. MIT License.

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sellebeww/spillway/internal/domain"
)

// DecisionInput is one rule-engine decision to persist. Kind is OPENED, ESCALATED, DEESCALATED,
// CLOSED or REMINDER. Payload is the JSON the notification sender renders the message from.
type DecisionInput struct {
	RuleID, DeviceID   uuid.UUID
	Kind               string
	Severity, Previous domain.Severity
	At                 time.Time
	LevelCm            float64
	Rate               *float64
	Payload            []byte
}

// DecisionResult reports what ApplyDecision did.
type DecisionResult struct {
	EventID  uuid.UUID
	Applied  bool  // false: the event already was in the target state (idempotent re-apply)
	Enqueued int64 // outbox rows created, one per active subscribed channel
}

var decisionKinds = map[string]bool{"OPENED": true, "ESCALATED": true, "DEESCALATED": true, "CLOSED": true, "REMINDER": true}

// ApplyDecision changes the alert event and schedules the notifications in ONE transaction:
// there is never an alert change without its notifications, nor notifications without the change.
// Re-applying a decision that is already in effect changes nothing.
func (o *OrgStore) ApplyDecision(ctx context.Context, in DecisionInput) (DecisionResult, error) {
	if !decisionKinds[in.Kind] {
		return DecisionResult{}, fmt.Errorf("%w: decision kind %q", ErrInvalid, in.Kind)
	}
	var sv int16
	if in.Kind != "CLOSED" {
		var err error
		if sv, err = severityArg(in.Severity); err != nil {
			return DecisionResult{}, err
		}
	}
	var res DecisionResult
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		seq := 0
		if in.Kind == "OPENED" {
			ev, err := fetchOne[AlertEvent](ctx, o, tx, `INSERT INTO alert_events (org_id, rule_id, device_id, state, severity, opened_at, peak_level, peak_rate, last_notified_at)
				SELECT $1, r.id, d.id, 'OPEN', $4, $5, $6, $7, $5 FROM alert_rules r, devices d
				WHERE r.org_id = $1 AND r.id = $2 AND d.org_id = $1 AND d.id = $3
				ON CONFLICT DO NOTHING RETURNING `+eventCols, in.RuleID, in.DeviceID, sv, in.At, in.LevelCm, in.Rate)
			if errors.Is(err, ErrNotFound) { // already open (replay), or not ours
				cur, cerr := o.openEvent(ctx, tx, in.RuleID, in.DeviceID, false)
				if cerr != nil {
					return cerr
				}
				res = DecisionResult{EventID: cur.ID}
				return nil
			}
			if err != nil {
				return err
			}
			res.EventID, res.Applied = ev.ID, true
		} else {
			cur, err := o.openEvent(ctx, tx, in.RuleID, in.DeviceID, true)
			if errors.Is(err, ErrNotFound) && in.Kind == "CLOSED" {
				return nil // already closed
			}
			if err != nil {
				return err
			}
			res.EventID = cur.ID
			switch in.Kind {
			case "ESCALATED", "DEESCALATED":
				if cur.Severity == in.Severity {
					return nil // already in this state
				}
				_, err = o.exec(ctx, tx, `UPDATE alert_events SET severity = $3, peak_level = GREATEST(peak_level, $4), peak_rate = GREATEST(peak_rate, $5), last_notified_at = $6, notify_seq = notify_seq + 1
					WHERE org_id = $1 AND id = $2`, cur.ID, sv, in.LevelCm, in.Rate, in.At)
				seq = cur.NotifySeq + 1
			case "REMINDER":
				seq = cur.NotifySeq + 1
				_, err = o.exec(ctx, tx, `UPDATE alert_events SET peak_level = GREATEST(peak_level, $3), peak_rate = GREATEST(peak_rate, $4), last_notified_at = $5, reminder_count = reminder_count + 1, notify_seq = notify_seq + 1
					WHERE org_id = $1 AND id = $2`, cur.ID, in.LevelCm, in.Rate, in.At)
			case "CLOSED":
				_, err = o.exec(ctx, tx, `UPDATE alert_events SET state = 'CLOSED', closed_at = $3, last_notified_at = $3, notify_seq = notify_seq + 1 WHERE org_id = $1 AND id = $2`, cur.ID, in.At)
				seq = cur.NotifySeq + 1
			}
			if err != nil {
				return err
			}
			res.Applied = true
		}
		payload := in.Payload
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		tag, err := o.exec(ctx, tx, `INSERT INTO notifications_outbox (org_id, alert_event_id, kind, seq, channel_id, payload)
			SELECT $1, $2, $3, $4, c.id, $5 FROM alert_rules r
			JOIN subscriptions s ON s.site_id = r.site_id AND s.org_id = r.org_id
			JOIN channels c ON c.id = s.channel_id AND c.org_id = s.org_id
			WHERE r.org_id = $1 AND r.id = $6 AND c.is_active
			ON CONFLICT (alert_event_id, kind, channel_id, seq) DO NOTHING`, res.EventID, in.Kind, seq, payload, in.RuleID)
		if err != nil {
			return err
		}
		res.Enqueued = tag.RowsAffected()
		return nil
	})
	return res, err
}

func (o *OrgStore) openEvent(ctx context.Context, q querier, ruleID, deviceID uuid.UUID, lock bool) (AlertEvent, error) {
	sql := `SELECT ` + eventCols + ` FROM alert_events WHERE org_id = $1 AND rule_id = $2 AND device_id = $3 AND state = 'OPEN'`
	if lock {
		sql += ` FOR UPDATE`
	}
	return fetchOne[AlertEvent](ctx, o, q, sql, ruleID, deviceID)
}

// OpenEventForDevice returns the open event of a rule for one device, or ErrNotFound.
func (o *OrgStore) OpenEventForDevice(ctx context.Context, ruleID, deviceID uuid.UUID) (AlertEvent, error) {
	return o.openEvent(ctx, o.s.pool, ruleID, deviceID, false)
}

// OpenEventsAll lists every open event of every organization, to warm the rule engines after a
// restart. It serves the alert runtime, so it is not organization-scoped.
func (s *Store) OpenEventsAll(ctx context.Context) ([]AlertEvent, error) {
	rows, err := s.pool.Query(ctx, `-- unscoped: the alert runtime restores every open alert at start
		SELECT `+eventCols+` FROM alert_events WHERE state = 'OPEN' AND device_id IS NOT NULL ORDER BY opened_at`)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[AlertEvent])
	return out, mapErr(err)
}

// ReadingsForReplay returns readings since a time in arrival order, to rebuild rule state.
func (o *OrgStore) ReadingsForReplay(ctx context.Context, deviceID uuid.UUID, since time.Time) ([]StoredReading, error) {
	return fetchAll[StoredReading](ctx, o, o.s.pool, `SELECT r.ts, r.received_at, r.sid, r.fc, r.raw_value, r.level_cm,
			r.quality_flags, r.quality_score, r.late, r.batt_v
		FROM readings r JOIN devices d ON d.id = r.device_id AND d.org_id = $1
		WHERE r.device_id = $2 AND r.ts >= $3 ORDER BY r.received_at, r.ts, r.fc LIMIT 50000`, deviceID, since)
}

// DeviceStatusRow is what the periodic status check needs.
type DeviceStatusRow struct {
	ID         uuid.UUID  `db:"id"`
	OrgID      uuid.UUID  `db:"org_id"`
	IntervalS  int        `db:"expected_interval_s"`
	LastSeenAt *time.Time `db:"last_seen_at"`
	LastStatus string     `db:"last_status"`
}

// DevicesForStatusCheck lists all devices for the periodic ONLINE/DELAYED/OFFLINE check.
func (s *Store) DevicesForStatusCheck(ctx context.Context) ([]DeviceStatusRow, error) {
	rows, err := s.pool.Query(ctx, `-- unscoped: the status check covers every organization
		SELECT id, org_id, expected_interval_s, last_seen_at, last_status FROM devices`)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[DeviceStatusRow])
	return out, mapErr(err)
}

// BotOffset returns the persisted getUpdates offset (0 when none).
func (s *Store) BotOffset(ctx context.Context, name string) (int64, error) {
	var v int64
	err := s.pool.QueryRow(ctx, `-- unscoped: global bot state
		SELECT value FROM bot_state WHERE name = $1`, name).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return v, mapErr(err)
}

// SetBotOffset persists the getUpdates offset.
func (s *Store) SetBotOffset(ctx context.Context, name string, v int64) error {
	_, err := s.pool.Exec(ctx, `-- unscoped: global bot state
		INSERT INTO bot_state (name, value) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, name, v)
	return mapErr(err)
}

// ChannelsByChat finds a chat's channels in every organization (a chat may follow several).
// The bot knows only the chat id, so this lookup cannot be organization-scoped.
func (s *Store) ChannelsByChat(ctx context.Context, kind, chatID string) ([]Channel, error) {
	rows, err := s.pool.Query(ctx, `-- unscoped: the bot identifies a chat before knowing its organizations
		SELECT `+channelCols+` FROM channels WHERE kind = $1 AND chat_id = $2 ORDER BY created_at`, kind, chatID)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[Channel])
	return out, mapErr(err)
}

// GetChannel returns one channel of this organization.
func (o *OrgStore) GetChannel(ctx context.Context, id uuid.UUID) (Channel, error) {
	return fetchOne[Channel](ctx, o, o.s.pool, `SELECT `+channelCols+` FROM channels WHERE org_id = $1 AND id = $2`, id)
}

// OutboxStats reports the number of pending notifications and the creation time of the oldest.
func (s *Store) OutboxStats(ctx context.Context) (pending int64, oldest *time.Time, err error) {
	err = s.pool.QueryRow(ctx, `-- unscoped: queue metrics cover every organization
		SELECT count(*), min(created_at) FROM notifications_outbox WHERE status = 'PENDING'`).Scan(&pending, &oldest)
	return pending, oldest, mapErr(err)
}

// RevokeSubscriptionTokens invalidates every unused deep-link token of a site.
func (o *OrgStore) RevokeSubscriptionTokens(ctx context.Context, siteID uuid.UUID) (int64, error) {
	var n int64
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := o.exec(ctx, tx, `UPDATE subscription_tokens SET used_at = now() WHERE org_id = $1 AND site_id = $2 AND used_at IS NULL`, siteID)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		return o.audit(ctx, tx, "subscription_token.revoke", "site", siteID, nil, map[string]any{"revoked": n})
	})
	return n, err
}
