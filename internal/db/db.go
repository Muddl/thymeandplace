// Package db opens the Postgres pool and runs migrations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	neturl "net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"thymeandplace/migrations"
)

// PoolOptions are one binary's pool defaults (core spec §14 connection
// budget). A pool_max_conns or pool_min_conns parameter in the database URL
// wins over them, so one deployment can be tuned without a rebuild.
type PoolOptions struct {
	MaxConns int32 // used when the URL has no pool_max_conns; 0 → pgx's default
	MinConns int32 // used when the URL has no pool_min_conns
}

// Pool sizes per binary. Without them pgx defaults to max(4, NumCPU), which
// is 4 on a small VPS.
var (
	ServerPool = PoolOptions{MaxConns: 16, MinConns: 2}
	CLIPool    = PoolOptions{MaxConns: 4}
)

// poolConfig parses url and applies o wherever the URL doesn't set the
// matching pool_* parameter itself.
func poolConfig(url string, o PoolOptions) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if o.MaxConns > 0 && !hasParam(url, "pool_max_conns") {
		cfg.MaxConns = o.MaxConns
	}
	if o.MinConns > 0 && !hasParam(url, "pool_min_conns") {
		cfg.MinConns = o.MinConns
	}
	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
	return cfg, nil
}

// hasParam reports whether a connection string (URL or keyword/value form)
// sets name.
func hasParam(connString, name string) bool {
	if strings.HasPrefix(connString, "postgres://") || strings.HasPrefix(connString, "postgresql://") {
		u, err := neturl.Parse(connString)
		return err == nil && u.Query().Has(name)
	}
	for _, f := range strings.Fields(connString) {
		if k, _, ok := strings.Cut(f, "="); ok && strings.TrimSpace(k) == name {
			return true
		}
	}
	return false
}

// Open connects a pool sized by o (see PoolOptions) and verifies it with a ping.
func Open(ctx context.Context, url string, o PoolOptions) (*pgxpool.Pool, error) {
	cfg, err := poolConfig(url, o)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// ErrPendingMigrations means the database is behind this binary's migrations.
var ErrPendingMigrations = errors.New("database has pending migrations; run the migrate service (server -migrate-only)")

// RequireMigrated returns ErrPendingMigrations unless every migration has
// been applied. It only reads goose_db_version, so the least-privilege app
// role can run it; applying migrations is the migrate service's job.
func RequireMigrated(ctx context.Context, url string) error {
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer sqldb.Close()
	return checkMigrated(ctx, sqldb)
}

// SchemaCurrent is RequireMigrated against an open pool, so /readyz and the
// watchdog can reuse the app's connections instead of opening another one.
func SchemaCurrent(ctx context.Context, pool *pgxpool.Pool) error {
	sqldb := stdlib.OpenDBFromPool(pool)
	defer sqldb.Close()
	return checkMigrated(ctx, sqldb)
}

func checkMigrated(ctx context.Context, sqldb *sql.DB) error {
	p, err := goose.NewProvider(goose.DialectPostgres, sqldb, migrations.FS)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	defer p.Close()
	pending, err := p.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("check migrations: %w", err)
	}
	if pending {
		return ErrPendingMigrations
	}
	return nil
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
