// Package dbtest gives each test a fresh, migrated Postgres/PostGIS database.
// One container starts per test binary; a template database is migrated once
// and cloned per test.
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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

// Same pinned image as deploy/compose.yaml's db service.
const Image = "postgis/postgis:17-3.5@sha256:01a6a70e41e6c4467c8f55f6063555ed72db2d6662cd0d571040d42eadaeb6f6"

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
	// Same file as production (deploy/db/roles.sql): create the template
	// database, apply that SQL as the superuser, then migrate as tp_owner.
	if err := exec(ctx, adminURL, "CREATE DATABASE tp_template"); err != nil {
		startErr = fmt.Errorf("CREATE DATABASE tp_template: %w", err)
		return
	}
	if err := applyProductionRoles(ctx, withDB(adminURL, "tp_template"), map[string]string{
		"owner_password":  Owner,
		"app_password":    App,
		"backup_password": Backup,
		"db":              "tp_template",
	}); err != nil {
		startErr = err
		return
	}
	if err := db.Migrate(ctx, withRole(withDB(adminURL, "tp_template"), Owner)); err != nil {
		startErr = err
	}
}

// New returns a superuser pool on a brand-new migrated database (so fixtures
// can insert freely); As gives a pool on the same database as another role.
// The database is dropped when the test ends.
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
	err := exec(ctx, adminURL, "CREATE DATABASE "+name+" TEMPLATE tp_template OWNER tp_owner")
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

// Roles created in the test container; each one's password is its name.
const (
	Owner  = "tp_owner"
	App    = "tp_app"
	Backup = "tp_backup"
)

// As returns a pool on pool's database (one from New) connected as role
// (Owner, App or Backup), closed when the test ends.
func As(t testing.TB, pool *pgxpool.Pool, role string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), withRole(withDB(adminURL, pool.Config().ConnConfig.Database), role))
	if err != nil {
		t.Fatalf("dbtest: pool as %s: %v", role, err)
	}
	t.Cleanup(p.Close)
	return p
}

// URLAs is the connection string for pool's database as role.
func URLAs(pool *pgxpool.Pool, role string) string {
	return withRole(withDB(adminURL, pool.Config().ConnConfig.Database), role)
}

func withRole(dsn, role string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		panic(err)
	}
	u.User = url.UserPassword(role, role)
	return u.String()
}

// ProductionRolesSQL is the role setup file production runs on a fresh volume.
const ProductionRolesSQL = "../../deploy/db/roles.sql"

func applyProductionRoles(ctx context.Context, dsn string, vars map[string]string) error {
	raw, err := os.ReadFile(ProductionRolesSQL)
	if err != nil {
		return fmt.Errorf("read %s: %w", ProductionRolesSQL, err)
	}
	sql, err := expandPsqlVars(string(raw), vars)
	if err != nil {
		return err
	}
	if err := exec(ctx, dsn, sql); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(ProductionRolesSQL), err)
	}
	return nil
}

var (
	psqlQuotedLiteral = regexp.MustCompile(`:'([A-Za-z_][A-Za-z0-9_]*)'`)
	psqlQuotedIdent   = regexp.MustCompile(`:"([A-Za-z_][A-Za-z0-9_]*)"`)
)

func expandPsqlVars(sql string, vars map[string]string) (string, error) {
	var missing string
	lookup := func(name string) (string, bool) {
		val, ok := vars[name]
		if !ok && missing == "" {
			missing = name
		}
		return val, ok
	}
	sql = psqlQuotedLiteral.ReplaceAllStringFunc(sql, func(m string) string {
		name := psqlQuotedLiteral.FindStringSubmatch(m)[1]
		val, ok := lookup(name)
		if !ok {
			return m
		}
		return quoteLiteral(val)
	})
	sql = psqlQuotedIdent.ReplaceAllStringFunc(sql, func(m string) string {
		name := psqlQuotedIdent.FindStringSubmatch(m)[1]
		val, ok := lookup(name)
		if !ok {
			return m
		}
		return quoteIdent(val)
	})
	if missing != "" {
		return "", fmt.Errorf("%s: unset psql variable %q", filepath.Base(ProductionRolesSQL), missing)
	}
	return sql, nil
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
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
