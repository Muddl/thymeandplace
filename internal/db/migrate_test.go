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
