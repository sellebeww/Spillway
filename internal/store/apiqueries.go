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

// RotateDeviceSecret replaces a device's secret with a fresh random one and returns it (hex).
// The new secret is shown once; the old one stops working immediately. Audited without any secret.
func (o *OrgStore) RotateDeviceSecret(ctx context.Context, c *auth.Cipher, id uuid.UUID) (string, error) {
	raw, secretHex, err := auth.GenerateDeviceSecret()
	if err != nil {
		return "", err
	}
	enc, err := c.Encrypt(raw, deviceAAD(id))
	if err != nil {
		return "", fmt.Errorf("encrypt device secret: %w", err)
	}
	err = o.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := o.exec(ctx, tx, `UPDATE devices SET secret_enc = $3 WHERE org_id = $1 AND id = $2`, id, enc)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return o.audit(ctx, tx, "device.rotate_secret", "device", id, nil, map[string]any{"rotated": true})
	})
	if err != nil {
		return "", err
	}
	return secretHex, nil
}

// CountAdmins returns how many admin memberships this organization has.
func (o *OrgStore) CountAdmins(ctx context.Context) (int, error) {
	n, err := fetchOne[struct {
		N int `db:"n"`
	}](ctx, o, o.s.pool, `SELECT count(*) AS n FROM memberships WHERE org_id = $1 AND role = 'admin'`)
	return n.N, err
}

// UserByEmail finds a user of this organization by e-mail (case-insensitive).
func (o *OrgStore) UserByEmail(ctx context.Context, email string) (User, error) {
	return fetchOne[User](ctx, o, o.s.pool, `SELECT id, org_id, oidc_sub, email, display_name, created_at FROM users
		WHERE org_id = $1 AND lower(email) = lower($2) ORDER BY created_at LIMIT 1`, email)
}

// ListEventsBySite returns the newest alert events of all rules of a site.
func (o *OrgStore) ListEventsBySite(ctx context.Context, siteID uuid.UUID, limit int) ([]AlertEvent, error) {
	return fetchAll[AlertEvent](ctx, o, o.s.pool, `SELECT e.id, e.org_id, e.rule_id, e.state, e.severity, e.opened_at, e.closed_at, e.peak_level,
			e.peak_rate, e.last_notified_at, e.device_id, e.reminder_count, e.notify_seq
		FROM alert_events e JOIN alert_rules r ON r.id = e.rule_id AND r.org_id = e.org_id
		WHERE e.org_id = $1 AND r.site_id = $2 ORDER BY e.opened_at DESC, e.id LIMIT $3`, siteID, clampLimit(limit, 50, 500))
}

// PublicDevice is a device of a public site with its newest trusted reading.
type PublicDevice struct {
	ID        uuid.UUID
	Name      string
	IntervalS int
	LastTS    *time.Time // timestamp of the newest reading with quality score >= 0.5
	LastRecv  *time.Time // when it arrived
	LevelCm   float64
}

// PublicSite is the public view of a site. It deliberately has no secrets and no user data.
type PublicSite struct {
	OrgID   uuid.UUID
	SiteID  uuid.UUID
	Slug    string
	Name    string
	Lat     float64
	Lon     float64
	TZ      string
	Devices []PublicDevice
}

type pubSiteRow struct {
	ID    uuid.UUID `db:"id"`
	OrgID uuid.UUID `db:"org_id"`
	Slug  string    `db:"slug"`
	Name  string    `db:"name"`
	Lat   float64   `db:"lat"`
	Lon   float64   `db:"lon"`
	TZ    string    `db:"tz"`
}

type pubDevRow struct {
	ID        uuid.UUID `db:"id"`
	SiteID    uuid.UUID `db:"site_id"`
	Name      string    `db:"name"`
	IntervalS int       `db:"expected_interval_s"`
}

type pubLastRow struct {
	DeviceID uuid.UUID `db:"device_id"`
	TS       time.Time `db:"ts"`
	Recv     time.Time `db:"received_at"`
	Level    float64   `db:"level_cm"`
}

// PublicSites lists the sites flagged is_public of every organization with their devices and newest
// trusted reading; slug limits the result to one site. It serves unauthenticated pages, so it can
// not be organization-scoped: it only ever returns is_public sites and no secrets or user data.
func (s *Store) PublicSites(ctx context.Context, slug string) ([]PublicSite, error) {
	rows, err := s.pool.Query(ctx, `-- unscoped: public pages list is_public sites of every organization
		SELECT id, org_id, slug, name, ST_Y(location::geometry) AS lat, ST_X(location::geometry) AS lon, tz
		FROM sites WHERE is_public AND ($1 = '' OR slug = $1) ORDER BY name, id`, slug)
	if err != nil {
		return nil, mapErr(err)
	}
	sites, err := pgx.CollectRows(rows, pgx.RowToStructByName[pubSiteRow])
	if err != nil || len(sites) == 0 {
		return nil, mapErr(err)
	}
	ids := make([]uuid.UUID, len(sites))
	for i, st := range sites {
		ids[i] = st.ID
	}
	drows, err := s.pool.Query(ctx, `-- unscoped: devices of the public sites selected above
		SELECT d.id, d.site_id, d.name, d.expected_interval_s FROM devices d WHERE d.site_id = ANY($1) ORDER BY d.name, d.id`, ids)
	if err != nil {
		return nil, mapErr(err)
	}
	devs, err := pgx.CollectRows(drows, pgx.RowToStructByName[pubDevRow])
	if err != nil {
		return nil, mapErr(err)
	}
	devIDs := make([]uuid.UUID, len(devs))
	for i, d := range devs {
		devIDs[i] = d.ID
	}
	lrows, err := s.pool.Query(ctx, `-- unscoped: newest trusted reading of the public devices selected above
		SELECT DISTINCT ON (r.device_id) r.device_id, r.ts, r.received_at, r.level_cm FROM readings r
		WHERE r.device_id = ANY($1) AND r.ts > now() - interval '30 days' AND r.ts < now() + interval '1 hour' AND r.quality_score >= 0.5
		ORDER BY r.device_id, r.ts DESC`, devIDs)
	if err != nil {
		return nil, mapErr(err)
	}
	lasts, err := pgx.CollectRows(lrows, pgx.RowToStructByName[pubLastRow])
	if err != nil {
		return nil, mapErr(err)
	}
	lastBy := map[uuid.UUID]pubLastRow{}
	for _, l := range lasts {
		lastBy[l.DeviceID] = l
	}
	out := make([]PublicSite, len(sites))
	idx := map[uuid.UUID]int{}
	for i, st := range sites {
		out[i] = PublicSite{OrgID: st.OrgID, SiteID: st.ID, Slug: st.Slug, Name: st.Name, Lat: st.Lat, Lon: st.Lon, TZ: st.TZ}
		idx[st.ID] = i
	}
	for _, d := range devs {
		pd := PublicDevice{ID: d.ID, Name: d.Name, IntervalS: d.IntervalS}
		if l, ok := lastBy[d.ID]; ok {
			ts, rc := l.TS, l.Recv
			pd.LastTS, pd.LastRecv, pd.LevelCm = &ts, &rc, l.Level
		}
		out[idx[d.SiteID]].Devices = append(out[idx[d.SiteID]].Devices, pd)
	}
	return out, nil
}
