// Command devseed loads the small reference fixture and the test catalog
// into DATABASE_URL. For local development and end-to-end tests only. It
// refuses to run unless DEVSEED_ALLOW=1 is set, so it cannot be pointed at a
// production database by accident even though it ships in the ingest image.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/db"
	"thymeandplace/internal/devdata"
	"thymeandplace/internal/store"
)

func main() {
	os.Exit(run(os.Getenv, os.Stdout, os.Stderr))
}

// run checks the DEVSEED_ALLOW guard, then seeds the database. It never
// connects to or migrates DATABASE_URL unless the guard passes.
func run(getenv func(string) string, stdout, stderr io.Writer) int {
	if getenv("DEVSEED_ALLOW") != "1" {
		fmt.Fprintln(stderr, "devseed seeds synthetic reference data and resets the sample catalog; set DEVSEED_ALLOW=1 only for local development or end-to-end tests")
		return 2
	}
	if err := seed(context.Background(), getenv("DATABASE_URL")); err != nil {
		fmt.Fprintln(stderr, "devseed:", err)
		return 1
	}
	fmt.Fprintln(stdout, "devseed ok")
	fmt.Fprintln(stdout, "devseed: reset the sample ingredients")
	return 0
}

func seed(ctx context.Context, url string) error {
	if err := db.Migrate(ctx, url); err != nil {
		return err
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := devdata.SeedReference(ctx, pool); err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor', 'devseed', true)`); err != nil {
		return err
	}
	if err := catalog.Write(ctx, store.New(tx), catalogtest.SmallCatalog(), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
