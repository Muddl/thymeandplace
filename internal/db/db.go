// Package db opens the Postgres pool and runs migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver for goose
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"thymeandplace/migrations"
)

// Open connects a pool and verifies it with a ping.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, url string) error {
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer sqldb.Close()
	// A session-scoped Postgres advisory lock so two replicas migrating at
	// once serialise instead of one failing on a concurrent schema change.
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("session locker: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqldb, migrations.FS, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	defer p.Close()
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
