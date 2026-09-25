package dbtest_test

import (
	"context"
	"testing"

	"thymeandplace/internal/dbtest"
)

func TestNewGivesMigratedIsolatedDatabases(t *testing.T) {
	ctx := context.Background()
	a := dbtest.New(t)
	b := dbtest.New(t)

	var v string
	if err := a.QueryRow(ctx, "SELECT postgis_version()").Scan(&v); err != nil {
		t.Fatalf("postgis missing: %v", err)
	}
	if _, err := a.Exec(ctx, "CREATE TABLE only_in_a (x int)"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := b.QueryRow(ctx, "SELECT to_regclass('only_in_a') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("databases are not isolated")
	}
	var sim float64
	if err := a.QueryRow(ctx, "SELECT similarity('london','londn')").Scan(&sim); err != nil || sim <= 0 {
		t.Fatalf("pg_trgm missing: %v %v", sim, err)
	}
}
