// Command devseed loads the small reference fixture and the test catalog
// into DATABASE_URL. For local development and end-to-end tests only.
package main

import (
	"context"
	"fmt"
	"os"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/db"
	"thymeandplace/internal/devdata"
	"thymeandplace/internal/store"
)

func main() {
	if err := run(context.Background(), os.Getenv("DATABASE_URL")); err != nil {
		fmt.Fprintln(os.Stderr, "devseed:", err)
		os.Exit(1)
	}
	fmt.Println("devseed ok")
}

func run(ctx context.Context, url string) error {
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
