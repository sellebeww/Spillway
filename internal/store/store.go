// Copyright (c) 2026 Vassel Goleyu. MIT License.

// Package store owns database access: the pgx pool and schema migrations.
package store

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/sellebeww/spillway/migrations"
)

// NewPool creates a pgx pool. Connections are established lazily.
func NewPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err) // pgx redacts the password in this error
	}
	// Ingest runs one worker per CPU plus the batch writer and the API: give the pool room beyond
	// pgx's default of max(4, CPUs) unless the URL sets pool_max_conns explicitly.
	if !strings.Contains(url, "pool_max_conns") {
		cfg.MaxConns = int32(max(16, 2*runtime.NumCPU())) //nolint:gosec // small
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}
	return pool, nil
}

// Migrate applies all pending embedded migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }() // only releases the borrowed pool wrapper

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
