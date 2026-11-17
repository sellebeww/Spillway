// Copyright (c) 2026 Vassel Goleyu. MIT License.

package store

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// CreateOrg creates a tenant. It is the one operation that cannot be scoped to an
// organization because the organization does not exist yet.
func (s *Store) CreateOrg(ctx context.Context, slug, name string) (Org, error) {
	if !slugRe.MatchString(slug) || strings.TrimSpace(name) == "" || len(name) > 200 {
		return Org{}, fmt.Errorf("%w: organization slug or name", ErrInvalid)
	}
	var org Org
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Org{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `-- unscoped: creates the tenant itself
		INSERT INTO organizations (slug, name) VALUES ($1, $2) RETURNING id, slug, name, created_at`, slug, name)
	if err != nil {
		return Org{}, mapErr(err)
	}
	if org, err = pgx.CollectOneRow(rows, pgx.RowToStructByName[Org]); err != nil {
		return Org{}, mapErr(err)
	}
	if err := s.ForOrg(org.ID).audit(ctx, tx, "org.create", "organization", org.ID, nil, org); err != nil {
		return Org{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Org{}, fmt.Errorf("commit transaction: %w", mapErr(err))
	}
	return org, nil
}

// OrgBySlug looks a tenant up by its slug (bootstrap and dev tooling).
func (s *Store) OrgBySlug(ctx context.Context, slug string) (Org, error) {
	rows, err := s.pool.Query(ctx, `-- unscoped: resolves a tenant by slug
		SELECT id, slug, name, created_at FROM organizations WHERE slug = $1`, slug)
	if err != nil {
		return Org{}, mapErr(err)
	}
	org, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[Org])
	return org, mapErr(err)
}

const siteCols = `id, org_id, slug, name, ST_Y(location::geometry) AS lat, ST_X(location::geometry) AS lon, tz, is_public, created_at, updated_at`

func (in *SiteInput) normalize() error {
	if in.TZ == "" {
		in.TZ = "Asia/Jakarta"
	}
	if _, err := time.LoadLocation(in.TZ); err != nil {
		return fmt.Errorf("%w: unknown time zone", ErrInvalid)
	}
	switch {
	case !slugRe.MatchString(in.Slug):
		return fmt.Errorf("%w: site slug must match %s", ErrInvalid, slugRe)
	case strings.TrimSpace(in.Name) == "" || len(in.Name) > 200:
		return fmt.Errorf("%w: site name", ErrInvalid)
	case in.Lat < -90 || in.Lat > 90 || in.Lon < -180 || in.Lon > 180:
		return fmt.Errorf("%w: coordinates out of range", ErrInvalid)
	}
	return nil
}

// CreateSite registers a site.
func (o *OrgStore) CreateSite(ctx context.Context, in SiteInput) (Site, error) {
	if err := in.normalize(); err != nil {
		return Site{}, err
	}
	var site Site
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		site, err = fetchOne[Site](ctx, o, tx, `INSERT INTO sites (org_id, slug, name, location, tz, is_public)
			VALUES ($1, $2, $3, ST_SetSRID(ST_MakePoint($5, $4), 4326)::geography, $6, $7)
			RETURNING `+siteCols, in.Slug, in.Name, in.Lat, in.Lon, in.TZ, in.IsPublic)
		if err != nil {
			return err
		}
		return o.audit(ctx, tx, "site.create", "site", site.ID, nil, site)
	})
	return site, err
}

// GetSite returns one site of this organization.
func (o *OrgStore) GetSite(ctx context.Context, id uuid.UUID) (Site, error) {
	return fetchOne[Site](ctx, o, o.s.pool, `SELECT `+siteCols+` FROM sites WHERE org_id = $1 AND id = $2`, id)
}

// ListSites returns all sites of this organization ordered by name.
func (o *OrgStore) ListSites(ctx context.Context) ([]Site, error) {
	return fetchAll[Site](ctx, o, o.s.pool, `SELECT `+siteCols+` FROM sites WHERE org_id = $1 ORDER BY name, id`)
}

// UpdateSite replaces the editable fields of a site.
func (o *OrgStore) UpdateSite(ctx context.Context, id uuid.UUID, in SiteInput) (Site, error) {
	if err := in.normalize(); err != nil {
		return Site{}, err
	}
	var after Site
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		before, err := fetchOne[Site](ctx, o, tx, `SELECT `+siteCols+` FROM sites WHERE org_id = $1 AND id = $2 FOR UPDATE`, id)
		if err != nil {
			return err
		}
		after, err = fetchOne[Site](ctx, o, tx, `UPDATE sites SET slug = $3, name = $4,
			location = ST_SetSRID(ST_MakePoint($6, $5), 4326)::geography, tz = $7, is_public = $8, updated_at = now()
			WHERE org_id = $1 AND id = $2 RETURNING `+siteCols, id, in.Slug, in.Name, in.Lat, in.Lon, in.TZ, in.IsPublic)
		if err != nil {
			return err
		}
		return o.audit(ctx, tx, "site.update", "site", id, before, after)
	})
	return after, err
}
