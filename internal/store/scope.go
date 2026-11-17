// Copyright (c) 2026 Vassel Goleyu. MIT License.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the entry point to all persistence. Tenant data is only reachable through
// ForOrg, whose methods can only run organization-scoped SQL (see OrgStore.exec/query).
type Store struct{ pool *pgxpool.Pool }

// New wraps a pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ForOrg returns the repositories scoped to one organization.
func (s *Store) ForOrg(orgID uuid.UUID) *OrgStore { return &OrgStore{s: s, org: orgID} }

// OrgStore is every repository of one organization. Parameter $1 of every query it runs is
// the organization id, and a query that does not filter on it is refused at runtime.
type OrgStore struct {
	s   *Store
	org uuid.UUID
}

// OrgID returns the organization this store is scoped to.
func (o *OrgStore) OrgID() uuid.UUID { return o.org }

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

var (
	// SELECT / UPDATE / DELETE must filter "org_id = $1".
	scopedFilter = regexp.MustCompile(`(?i)\borg_id\s*=\s*\$1\b`)
	// INSERT must list org_id first and supply $1 first.
	scopedInsert = regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\s+\w+\s*\(\s*org_id\s*,.*?(VALUES\s*\(|SELECT)\s*\$1\s*,`)
)

// IsScoped reports whether sql follows the organization-scoping convention.
func IsScoped(sql string) bool {
	sql = sqlComment.ReplaceAllString(sql, "") // a filter mentioned in a comment does not count
	return scopedFilter.MatchString(sql) || scopedInsert.MatchString(sql)
}

var sqlComment = regexp.MustCompile(`--[^\n]*`)

// exec, query and fetch are the only places tenant SQL is sent to the database.
func (o *OrgStore) exec(ctx context.Context, q querier, sql string, args ...any) (pgconn.CommandTag, error) {
	if !IsScoped(sql) {
		return pgconn.CommandTag{}, ErrUnscoped
	}
	tag, err := q.Exec(ctx, sql, append([]any{o.org}, args...)...)
	return tag, mapErr(err)
}

func (o *OrgStore) query(ctx context.Context, q querier, sql string, args ...any) (pgx.Rows, error) {
	if !IsScoped(sql) {
		return nil, ErrUnscoped
	}
	rows, err := q.Query(ctx, sql, append([]any{o.org}, args...)...)
	return rows, mapErr(err)
}

func fetchAll[T any](ctx context.Context, o *OrgStore, q querier, sql string, args ...any) ([]T, error) {
	rows, err := o.query(ctx, q, sql, args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	return out, mapErr(err)
}

func fetchOne[T any](ctx context.Context, o *OrgStore, q querier, sql string, args ...any) (T, error) {
	rows, err := o.query(ctx, q, sql, args...)
	if err != nil {
		var zero T
		return zero, err
	}
	v, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[T])
	return v, mapErr(err)
}

// mapErr turns database errors into the package sentinels. Messages carry the constraint
// name only, never row values.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return fmt.Errorf("%w (%s)", ErrConflict, pg.ConstraintName)
		case "23503":
			return fmt.Errorf("%w (referenced row does not exist in this organization: %s)", ErrNotFound, pg.ConstraintName)
		case "23502", "23514", "22P02", "22007", "22008", "22003", "22001":
			return fmt.Errorf("%w (%s)", ErrInvalid, pg.ConstraintName)
		case "23001": // restrict_violation: append-only tables
			return fmt.Errorf("%w (%s)", ErrConflict, pg.TableName)
		}
	}
	return err
}

func (o *OrgStore) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := o.s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", mapErr(err))
	}
	return nil
}

// Actor identifies who made a configuration change, for the audit log.
type Actor struct {
	UserID *uuid.UUID
	Label  string
}

type actorKey struct{}

// WithActor attaches the acting user to ctx; store mutations record it in the audit log.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

func actorFrom(ctx context.Context) Actor {
	if a, ok := ctx.Value(actorKey{}).(Actor); ok && a.Label != "" {
		return a
	}
	return Actor{Label: "system"}
}

// audit records a configuration change inside the caller's transaction, so the change and
// its audit row commit or roll back together. Never pass anything secret as before/after.
func (o *OrgStore) audit(ctx context.Context, tx pgx.Tx, action, entityType string, entityID any, before, after any) error {
	enc := func(v any) ([]byte, error) {
		if v == nil {
			return nil, nil
		}
		return json.Marshal(v)
	}
	b, err := enc(before)
	if err != nil {
		return fmt.Errorf("encode audit before: %w", err)
	}
	a, err := enc(after)
	if err != nil {
		return fmt.Errorf("encode audit after: %w", err)
	}
	actor := actorFrom(ctx)
	_, err = o.exec(ctx, tx, `INSERT INTO audit_log (org_id, actor_user_id, actor_label, action, entity_type, entity_id, before, after)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, actor.UserID, actor.Label, action, entityType, fmt.Sprint(entityID), b, a)
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// ListAudit returns the newest audit entries first.
func (o *OrgStore) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	return fetchAll[AuditEntry](ctx, o, o.s.pool, `SELECT id, at, actor_user_id, actor_label, action, entity_type, entity_id, before, after
		FROM audit_log WHERE org_id = $1 ORDER BY id DESC LIMIT $2`, clampLimit(limit, 100, 1000))
}

func clampLimit(n, def, maxN int) int {
	switch {
	case n <= 0:
		return def
	case n > maxN:
		return maxN
	default:
		return n
	}
}
