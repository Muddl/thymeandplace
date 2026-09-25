// Package dbtest gives each test a fresh, migrated Postgres/PostGIS database.
// One container starts per test binary; a template database is migrated once
// and cloned per test.
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"thymeandplace/internal/db"
)

const Image = "postgis/postgis:17-3.5"

var (
	once     sync.Once
	adminURL string
	startErr error
	seq      atomic.Int64
	createMu sync.Mutex
)

func start() {
	ctx := context.Background()
	c, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("tp_admin"),
		postgres.WithUsername("tp"),
		postgres.WithPassword("tp"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		startErr = fmt.Errorf("start container: %w", err)
		return
	}
	// The container is reaped by testcontainers when the test binary exits.
	adminURL, err = c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		startErr = err
		return
	}
	if err := exec(ctx, adminURL, "CREATE DATABASE tp_template"); err != nil {
		startErr = err
		return
	}
	if err := db.Migrate(ctx, withDB(adminURL, "tp_template")); err != nil {
		startErr = err
	}
}

// New returns a pool on a brand-new migrated database. It is dropped when
// the test ends.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("TP_SKIP_DB") == "1" {
		t.Skip("TP_SKIP_DB=1")
	}
	once.Do(start)
	if startErr != nil {
		t.Fatalf("dbtest: %v", startErr)
	}
	ctx := context.Background()
	name := fmt.Sprintf("t%d_%d", os.Getpid(), seq.Add(1))

	createMu.Lock()
	err := exec(ctx, adminURL, "CREATE DATABASE "+name+" TEMPLATE tp_template")
	createMu.Unlock()
	if err != nil {
		t.Fatalf("dbtest: create database: %v", err)
	}
	pool, err := pgxpool.New(ctx, withDB(adminURL, name))
	if err != nil {
		t.Fatalf("dbtest: pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_ = exec(context.Background(), adminURL, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return pool
}

func exec(ctx context.Context, dsn, sql string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, sql)
	return err
}

func withDB(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + name
	return u.String()
}
