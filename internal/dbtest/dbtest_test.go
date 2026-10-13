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

func TestRolesMatchProductionSetup(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	for _, role := range []string{dbtest.Owner, dbtest.App, dbtest.Backup} {
		var super, createdb, createrole bool
		if err := pool.QueryRow(ctx,
			`SELECT rolsuper, rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = $1`, role,
		).Scan(&super, &createdb, &createrole); err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if super || createdb || createrole {
			t.Errorf("%s: super=%v createdb=%v createrole=%v, want all false", role, super, createdb, createrole)
		}
	}
	var member bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_auth_members m
			JOIN pg_roles r ON r.oid = m.member
			JOIN pg_roles g ON g.oid = m.roleid
			WHERE r.rolname = $1 AND g.rolname = 'pg_read_all_data'
		)`, dbtest.Backup).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if !member {
		t.Fatal("tp_backup is not a member of pg_read_all_data")
	}
	var owner string
	if err := pool.QueryRow(ctx, `SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_database WHERE datname = current_database()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != dbtest.Owner {
		t.Fatalf("database owner is %q, want %s", owner, dbtest.Owner)
	}
}
