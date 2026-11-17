// Copyright (c) 2026 Vassel Goleyu. MIT License.

package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Roles stored in memberships. "public" is the absence of a login.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
)

// UpsertUser records an OIDC identity inside this organization. It syncs identity data on
// login and is not a configuration change, so it is not audited.
func (o *OrgStore) UpsertUser(ctx context.Context, oidcSub string, email *string, displayName string) (User, error) {
	if strings.TrimSpace(oidcSub) == "" || len(oidcSub) > 255 {
		return User{}, fmt.Errorf("%w: oidc subject", ErrInvalid)
	}
	return fetchOne[User](ctx, o, o.s.pool, `INSERT INTO users (org_id, oidc_sub, email, display_name) VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_id, oidc_sub) DO UPDATE SET email = EXCLUDED.email, display_name = EXCLUDED.display_name
		RETURNING id, org_id, oidc_sub, email, display_name, created_at`, oidcSub, email, displayName)
}

// SetMembership grants a role. admin covers the whole organization and takes no site;
// operator is bound to exactly one site.
func (o *OrgStore) SetMembership(ctx context.Context, userID uuid.UUID, role string, siteID *uuid.UUID) (Membership, error) {
	if (role != RoleAdmin && role != RoleOperator) || (role == RoleAdmin) != (siteID == nil) {
		return Membership{}, fmt.Errorf("%w: admin takes no site, operator needs one", ErrInvalid)
	}
	var m Membership
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		// Parents are verified in the same statement so a foreign user or site is "not found",
		// never a unique-index conflict that would reveal another tenant's memberships.
		m, err = fetchOne[Membership](ctx, o, tx, `INSERT INTO memberships (org_id, user_id, role, site_id)
			SELECT $1, u.id, $3, $4 FROM users u
			WHERE u.org_id = $1 AND u.id = $2 AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM sites s WHERE s.org_id = $1 AND s.id = $4))
			RETURNING id, org_id, user_id, role, site_id`, userID, role, siteID)
		if err != nil {
			return err
		}
		return o.audit(ctx, tx, "membership.add", "user", userID, nil, m)
	})
	return m, err
}

// RemoveMembership revokes a role.
func (o *OrgStore) RemoveMembership(ctx context.Context, id uuid.UUID) error {
	return o.inTx(ctx, func(tx pgx.Tx) error {
		m, err := fetchOne[Membership](ctx, o, tx, `DELETE FROM memberships WHERE org_id = $1 AND id = $2
			RETURNING id, org_id, user_id, role, site_id`, id)
		if err != nil {
			return err
		}
		return o.audit(ctx, tx, "membership.remove", "user", m.UserID, m, nil)
	})
}

// ListMemberships returns the roles of one user.
func (o *OrgStore) ListMemberships(ctx context.Context, userID uuid.UUID) ([]Membership, error) {
	return fetchAll[Membership](ctx, o, o.s.pool, `SELECT id, org_id, user_id, role, site_id FROM memberships
		WHERE org_id = $1 AND user_id = $2 ORDER BY role, id`, userID)
}
