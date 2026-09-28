package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/store"
)

// The runtime role can do everything the app does, and nothing it doesn't:
// no DDL, no file or program access, and no rewriting of the audit tables.
func TestAppRolePrivileges(t *testing.T) {
	ctx := context.Background()
	super := dbtest.New(t)
	dbtest.LoadReference(t, super)
	tx, err := super.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Write(ctx, store.New(tx), catalogtest.SmallCatalog(), nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	app := dbtest.As(t, super, dbtest.App)

	t.Run("allowed", func(t *testing.T) {
		if _, err := catalog.Load(ctx, app); err != nil {
			t.Fatalf("catalog.Load: %v", err)
		}
		svc := changeset.NewService(app, ocean.NewService(app), func(context.Context) ([]calibration.Case, error) { return nil, nil })
		k := catalogtest.Hardy()
		k.Name = "Curly kale"
		pending, _, err := svc.Preview(ctx, changeset.FormChangeset(k))
		if err != nil {
			t.Fatalf("preview: %v", err)
		}
		if err := svc.Apply(ctx, pending.ID, "owner", changeset.ApplyOptions{}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		var actor string
		if err := app.QueryRow(ctx, `SELECT actor FROM catalog_history WHERE change_id = $1 LIMIT 1`, pending.ID).Scan(&actor); err != nil || actor != "owner" {
			t.Fatalf("history row written by the definer trigger: actor=%q err=%v", actor, err)
		}
		if err := store.New(app).InsertLoginAttempt(ctx, store.InsertLoginAttemptParams{
			At: pgTime(time.Now()), Ip: "192.0.2.1", IpKey: "192.0.2.1", Method: "passkey", Outcome: "failure",
		}); err != nil {
			t.Fatalf("login-log insert: %v", err)
		}
		for _, sql := range []string{
			"LISTEN data_version_changed",
			"SELECT count(*) FROM climate_cell",
			"SELECT count(*) FROM ocean_ecoregion",
			"SELECT count(*) FROM place",
			"SELECT prune_admin_login_log('90 days')",
		} {
			if _, err := app.Exec(ctx, sql); err != nil {
				t.Errorf("%s: %v", sql, err)
			}
		}
		tx, err := app.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err := tx.Exec(ctx, "SELECT version FROM catalog_meta WHERE id FOR UPDATE"); err != nil {
			t.Errorf("catalog_meta FOR UPDATE: %v", err)
		}
	})

	t.Run("refused", func(t *testing.T) {
		for _, sql := range []string{
			"UPDATE admin_login_log SET outcome = 'success'",
			"DELETE FROM admin_login_log",
			"UPDATE catalog_history SET actor = 'someone else'",
			"DELETE FROM catalog_history",
			"UPDATE catalog_meta SET version = 0",
			"TRUNCATE ingredient",
			"CREATE TABLE x ()",
			"COPY (SELECT 1) TO PROGRAM 'true'",
			"SELECT pg_read_file('/etc/passwd')",
		} {
			_, err := app.Exec(ctx, sql)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Errorf("%s: want insufficient_privilege (42501), got %v", sql, err)
			}
		}
		_, err := app.Exec(ctx, "SELECT prune_admin_login_log('1 day')")
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "P0001" {
			t.Errorf("prune with a 1-day retention: want the function's own error, got %v", err)
		}
	})
}

func pgTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// A table ingest creates and swaps in as tp_owner is readable by the app
// with no extra grant.
func TestOwnerCreatedTablesReadableByApp(t *testing.T) {
	ctx := context.Background()
	super := dbtest.New(t)
	owner := dbtest.As(t, super, dbtest.Owner)
	for _, sql := range []string{
		"CREATE TABLE place_v99 (LIKE place_v0 INCLUDING ALL)",
		"CREATE OR REPLACE VIEW place AS SELECT * FROM place_v99",
	} {
		if _, err := owner.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	app := dbtest.As(t, super, dbtest.App)
	if _, err := app.Exec(ctx, "SELECT count(*) FROM place"); err != nil {
		t.Fatalf("view on an owner-created table: %v", err)
	}
	if _, err := app.Exec(ctx, "SELECT count(*) FROM place_v99"); err != nil {
		t.Fatalf("owner-created table: %v", err)
	}
}

// The backup role can read everything pg_dump needs, and write nothing.
func TestBackupRoleReadsOnly(t *testing.T) {
	ctx := context.Background()
	super := dbtest.New(t)
	backup := dbtest.As(t, super, dbtest.Backup)
	for _, table := range []string{"ingredient", "catalog_history", "admin_login_log", "admin_backup", "dataset_versions", "goose_db_version"} {
		if _, err := backup.Exec(ctx, "SELECT count(*) FROM "+table); err != nil {
			t.Errorf("read %s: %v", table, err)
		}
	}
	_, err := backup.Exec(ctx, "DELETE FROM ingredient")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Errorf("backup role delete: want 42501, got %v", err)
	}
}
