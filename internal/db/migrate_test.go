package db_test

import (
	"context"
	"errors"
	"testing"

	"thymeandplace/internal/db"
	"thymeandplace/internal/dbtest"
)

func TestSchemaCurrent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	if err := db.SchemaCurrent(ctx, pool); err != nil {
		t.Fatalf("current schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = (SELECT max(version_id) FROM goose_db_version)`); err != nil {
		t.Fatal(err)
	}
	if err := db.SchemaCurrent(ctx, pool); !errors.Is(err, db.ErrPendingMigrations) {
		t.Fatalf("pending: %v, want ErrPendingMigrations", err)
	}
}

func TestRequireMigratedMatchesSchemaCurrent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	url := dbtest.URLAs(pool, dbtest.App)
	if err := db.RequireMigrated(ctx, url); err != nil {
		t.Fatalf("RequireMigrated on a current schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = (SELECT max(version_id) FROM goose_db_version)`); err != nil {
		t.Fatal(err)
	}
	if err := db.RequireMigrated(ctx, url); !errors.Is(err, db.ErrPendingMigrations) {
		t.Fatalf("RequireMigrated pending: %v", err)
	}
}

// A database that has applied a version this binary does not ship is
// still current: the extra row is a newer binary's work, not a hole.
func TestSchemaCurrentAllowsNewerSchema(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	if _, err := pool.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (99999, true)`); err != nil {
		t.Fatal(err)
	}
	if err := db.SchemaCurrent(ctx, pool); err != nil {
		t.Fatalf("newer schema: %v", err)
	}
}

// A hole below the highest applied version is goose's missing-migrations
// error. /readyz treats that as not current, same as a pending tail.
func TestSchemaCurrentMissingMigrations(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = (SELECT min(version_id) FROM goose_db_version WHERE version_id > 0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.SchemaCurrent(ctx, pool); !errors.Is(err, db.ErrPendingMigrations) {
		t.Fatalf("missing migrations: %v, want ErrPendingMigrations", err)
	}
}

func TestSchemaCurrentReleasesPoolConn(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.As(t, dbtest.New(t), dbtest.App)
	if err := db.SchemaCurrent(ctx, pool); err != nil {
		t.Fatalf("current schema: %v", err)
	}
	if n := pool.Stat().AcquiredConns(); n != 0 {
		t.Fatalf("acquired after SchemaCurrent: %d, want 0", n)
	}
	if err := db.SchemaCurrent(ctx, pool); err != nil {
		t.Fatalf("second SchemaCurrent: %v", err)
	}
	if n := pool.Stat().AcquiredConns(); n != 0 {
		t.Fatalf("acquired after second SchemaCurrent: %d, want 0", n)
	}
}
