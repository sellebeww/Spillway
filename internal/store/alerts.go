// Copyright (c) 2026 Vassel Goleyu. MIT License.

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sellebeww/spillway/internal/domain"
)

type tierJSON struct {
	Value    float64 `json:"value"`
	Severity int     `json:"severity"`
}

type ruleParams struct {
	Tiers        []tierJSON `json:"tiers"`
	HysteresisCm float64    `json:"hysteresis_cm,omitempty"`
}

type ruleRow struct {
	ID         uuid.UUID `db:"id"`
	OrgID      uuid.UUID `db:"org_id"`
	SiteID     uuid.UUID `db:"site_id"`
	Type       string    `db:"type"`
	Params     []byte    `db:"params"`
	CooldownS  int       `db:"cooldown_s"`
	MinQuality float64   `db:"min_quality"`
	Enabled    bool      `db:"enabled"`
	CreatedAt  time.Time `db:"created_at"`
	UpdatedAt  time.Time `db:"updated_at"`
}

const ruleCols = `id, org_id, site_id, type, params, cooldown_s, min_quality, enabled, created_at, updated_at`

func (r ruleRow) rule() (AlertRule, error) {
	var p ruleParams
	if err := json.Unmarshal(r.Params, &p); err != nil {
		return AlertRule{}, fmt.Errorf("decode rule params: %w", err)
	}
	tiers := make([]domain.Tier, len(p.Tiers))
	for i, t := range p.Tiers {
		tiers[i] = domain.Tier{Value: t.Value, Severity: domain.Severity(t.Severity)}
	}
	return AlertRule{
		ID: r.ID, OrgID: r.OrgID, SiteID: r.SiteID, Kind: domain.RuleKind(r.Type), Tiers: tiers,
		HysteresisCm: p.HysteresisCm, Cooldown: time.Duration(r.CooldownS) * time.Second,
		MinQuality: r.MinQuality, Enabled: r.Enabled, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}

// encode validates the input with the pure engine's rules and produces the stored form.
func (in RuleInput) encode() (params []byte, cooldownS int, minQuality float64, err error) {
	cooldown := in.Cooldown
	if cooldown == 0 {
		cooldown = domain.DefaultCooldown
	}
	minQuality = in.MinQuality
	if minQuality == 0 {
		minQuality = domain.DefaultMinQuality
	}
	hyst := in.HysteresisCm
	if in.Kind == domain.KindThreshold && hyst == 0 {
		hyst = domain.DefaultHysteresisCm
	}
	if in.Kind == domain.KindRateOfRise {
		hyst = 0 // fixed 20 percent, not configurable
	}
	probe := domain.Rule{ID: "new", Kind: in.Kind, Tiers: in.Tiers, HysteresisCm: hyst, Cooldown: cooldown}
	if err := probe.Validate(); err != nil {
		return nil, 0, 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if minQuality < 0 || minQuality > 1 {
		return nil, 0, 0, fmt.Errorf("%w: min_quality must be within 0..1", ErrInvalid)
	}
	p := ruleParams{HysteresisCm: hyst}
	for _, t := range in.Tiers {
		p.Tiers = append(p.Tiers, tierJSON{Value: t.Value, Severity: int(t.Severity)})
	}
	params, err = json.Marshal(p)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("encode rule params: %w", err)
	}
	return params, int(cooldown / time.Second), minQuality, nil
}

// CreateRule stores a validated alert rule for a site.
func (o *OrgStore) CreateRule(ctx context.Context, in RuleInput) (AlertRule, error) {
	params, cooldownS, minQ, err := in.encode()
	if err != nil {
		return AlertRule{}, err
	}
	var out AlertRule
	err = o.inTx(ctx, func(tx pgx.Tx) error {
		row, err := fetchOne[ruleRow](ctx, o, tx, `INSERT INTO alert_rules (org_id, site_id, type, params, cooldown_s, min_quality, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+ruleCols, in.SiteID, string(in.Kind), params, cooldownS, minQ, in.Enabled)
		if err != nil {
			return err
		}
		if out, err = row.rule(); err != nil {
			return err
		}
		return o.audit(ctx, tx, "rule.create", "alert_rule", out.ID, nil, out)
	})
	return out, err
}

// GetRule returns one rule of this organization.
func (o *OrgStore) GetRule(ctx context.Context, id uuid.UUID) (AlertRule, error) {
	row, err := fetchOne[ruleRow](ctx, o, o.s.pool, `SELECT `+ruleCols+` FROM alert_rules WHERE org_id = $1 AND id = $2`, id)
	if err != nil {
		return AlertRule{}, err
	}
	return row.rule()
}

// ListRules returns the rules of this organization, optionally of one site.
func (o *OrgStore) ListRules(ctx context.Context, siteID *uuid.UUID) ([]AlertRule, error) {
	rows, err := fetchAll[ruleRow](ctx, o, o.s.pool, `SELECT `+ruleCols+` FROM alert_rules
		WHERE org_id = $1 AND ($2::uuid IS NULL OR site_id = $2) ORDER BY created_at, id`, siteID)
	if err != nil {
		return nil, err
	}
	out := make([]AlertRule, len(rows))
	for i, r := range rows {
		if out[i], err = r.rule(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// UpdateRule replaces the editable fields of a rule (the site cannot change).
func (o *OrgStore) UpdateRule(ctx context.Context, id uuid.UUID, in RuleInput) (AlertRule, error) {
	params, cooldownS, minQ, err := in.encode()
	if err != nil {
		return AlertRule{}, err
	}
	var out AlertRule
	err = o.inTx(ctx, func(tx pgx.Tx) error {
		beforeRow, err := fetchOne[ruleRow](ctx, o, tx, `SELECT `+ruleCols+` FROM alert_rules WHERE org_id = $1 AND id = $2 FOR UPDATE`, id)
		if err != nil {
			return err
		}
		before, err := beforeRow.rule()
		if err != nil {
			return err
		}
		row, err := fetchOne[ruleRow](ctx, o, tx, `UPDATE alert_rules SET type = $3, params = $4, cooldown_s = $5, min_quality = $6,
			enabled = $7, updated_at = now() WHERE org_id = $1 AND id = $2 RETURNING `+ruleCols,
			id, string(in.Kind), params, cooldownS, minQ, in.Enabled)
		if err != nil {
			return err
		}
		if out, err = row.rule(); err != nil {
			return err
		}
		return o.audit(ctx, tx, "rule.update", "alert_rule", id, before, out)
	})
	return out, err
}

const eventCols = `id, org_id, rule_id, state, severity, opened_at, closed_at, peak_level, peak_rate, last_notified_at, device_id, reminder_count, notify_seq`

// OpenEvent starts the alert event of a rule. A second open event for the same rule is ErrConflict.
func (o *OrgStore) OpenEvent(ctx context.Context, ruleID uuid.UUID, sev domain.Severity, openedAt time.Time, level float64, rate *float64) (AlertEvent, error) {
	sv, err := severityArg(sev)
	if err != nil {
		return AlertEvent{}, err
	}
	// INSERT ... SELECT from the org's own rules: a foreign rule yields no row (not found) and never
	// reaches the one-open-per-rule index, so no cross-tenant "conflict" can reveal its state.
	return fetchOne[AlertEvent](ctx, o, o.s.pool, `INSERT INTO alert_events (org_id, rule_id, state, severity, opened_at, peak_level, peak_rate, last_notified_at)
		SELECT $1, r.id, 'OPEN', $3, $4, $5, $6, $4 FROM alert_rules r WHERE r.org_id = $1 AND r.id = $2
		RETURNING `+eventCols, ruleID, sv, openedAt, level, rate)
}

// UpdateEvent changes the severity of an open event (escalation, de-escalation or a
// reminder) and raises the recorded peaks.
func (o *OrgStore) UpdateEvent(ctx context.Context, eventID uuid.UUID, sev domain.Severity, level float64, rate *float64, notifiedAt time.Time) (AlertEvent, error) {
	sv, err := severityArg(sev)
	if err != nil {
		return AlertEvent{}, err
	}
	return fetchOne[AlertEvent](ctx, o, o.s.pool, `UPDATE alert_events SET severity = $3,
			peak_level = GREATEST(peak_level, $4), peak_rate = GREATEST(peak_rate, $5), last_notified_at = $6
		WHERE org_id = $1 AND id = $2 AND state = 'OPEN' RETURNING `+eventCols, eventID, sv, level, rate, notifiedAt)
}

// CloseEvent closes an open event.
func (o *OrgStore) CloseEvent(ctx context.Context, eventID uuid.UUID, closedAt time.Time) (AlertEvent, error) {
	return fetchOne[AlertEvent](ctx, o, o.s.pool, `UPDATE alert_events SET state = 'CLOSED', closed_at = $3, last_notified_at = $3
		WHERE org_id = $1 AND id = $2 AND state = 'OPEN' RETURNING `+eventCols, eventID, closedAt)
}

// OpenEventForRule returns the open event of a rule, or ErrNotFound.
func (o *OrgStore) OpenEventForRule(ctx context.Context, ruleID uuid.UUID) (AlertEvent, error) {
	return fetchOne[AlertEvent](ctx, o, o.s.pool, `SELECT `+eventCols+` FROM alert_events
		WHERE org_id = $1 AND rule_id = $2 AND state = 'OPEN'`, ruleID)
}

// ListEvents returns the newest events of a rule first.
func (o *OrgStore) ListEvents(ctx context.Context, ruleID uuid.UUID, limit int) ([]AlertEvent, error) {
	return fetchAll[AlertEvent](ctx, o, o.s.pool, `SELECT `+eventCols+` FROM alert_events
		WHERE org_id = $1 AND rule_id = $2 ORDER BY opened_at DESC, id LIMIT $3`, ruleID, clampLimit(limit, 50, 500))
}

// severityArg converts a severity to its smallint column value after checking the range.
func severityArg(sev domain.Severity) (int16, error) {
	if !sev.Valid() {
		return 0, fmt.Errorf("%w: severity must be 1..3", ErrInvalid)
	}
	return int16(sev), nil //nolint:gosec // bounded to 1..3 by Valid()
}
