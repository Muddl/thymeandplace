package catalog_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

// Every shared invalid example must be rejected by a DB constraint
// (SQLSTATE class 23: integrity constraint violation).
func TestInvalidExamplesRejectedByDatabase(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	// basil is a pairing target used by some examples.
	var basil catalog.Ingredient
	for _, i := range catalogtest.SmallCatalog() {
		if i.Slug == "basil" {
			basil = i
			basil.Pairings = nil
		}
	}
	writeTx(t, pool, []catalog.Ingredient{basil}, nil)

	for _, ex := range catalogtest.InvalidExamples() {
		t.Run(ex.Name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx) //nolint:errcheck
			err = catalog.Write(ctx, store.New(tx), []catalog.Ingredient{ex.Ingredient}, nil)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || !strings.HasPrefix(pgErr.Code, "23") {
				t.Fatalf("want integrity violation, got %v", err)
			}
		})
	}
}

// Every valid builder is accepted by the database.
func TestValidBuildersAcceptedByDatabase(t *testing.T) {
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)
}
