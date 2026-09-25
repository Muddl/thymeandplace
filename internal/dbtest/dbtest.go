// Package dbtest gives each test a fresh, migrated Postgres/PostGIS database.
// One container starts per test binary; a template database is migrated once
// and cloned per test.
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moby/moby/client"
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

// containerStartBackoff is the delay before each retry of postgres.Run, one
// entry per retry attempt (so len(containerStartBackoff)+1 attempts total).
// Docker-provider detection has been observed to race transiently when
// multiple test binaries start containers at once; a short retry absorbs it.
var containerStartBackoff = []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second}

func start() {
	ctx := context.Background()
	// testcontainers-go resolves the Docker host once per process
	// (internal/core.ExtractDockerHost, guarded by a sync.Once) and, absent
	// DOCKER_HOST, does so on Windows by os.Stat-ing the named pipe; that
	// stat has been observed to fail transiently when two test binaries
	// start containers at the same time, and because the result is cached
	// for the process's lifetime, no amount of retrying postgres.Run below
	// can recover from it once it happens. Setting DOCKER_HOST up front to
	// the same value the Docker client already defaults to (this is a no-op
	// on a normal Windows setup) avoids that stat, and the race, entirely.
	// This is Windows-only: on other OSes Docker-context detection (Docker
	// Desktop for Linux, Colima, rootless Docker, Podman, ...) can resolve a
	// different host than the plain default, and presetting DOCKER_HOST here
	// would incorrectly override that detection.
	if runtime.GOOS == "windows" && os.Getenv("DOCKER_HOST") == "" {
		os.Setenv("DOCKER_HOST", client.DefaultDockerHost)
	}
	var c *postgres.PostgresContainer
	var err error
	for attempt := 0; ; attempt++ {
		c, err = postgres.Run(ctx, Image,
			postgres.WithDatabase("tp_admin"),
			postgres.WithUsername("tp"),
			postgres.WithPassword("tp"),
			postgres.BasicWaitStrategies(),
		)
		if err == nil {
			break
		}
		if c != nil {
			_ = c.Terminate(ctx)
		}
		if attempt >= len(containerStartBackoff) {
			startErr = fmt.Errorf("start container: %w", err)
			return
		}
		time.Sleep(containerStartBackoff[attempt])
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
