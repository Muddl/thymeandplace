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

// Review finding 4 (Plan 2 whole-plan review): spec §13.3 requires every DB
// foreign key, not just CHECKs, to reject invalid rows. A pairing to a slug
// that doesn't exist must fail on the ingredient_pairing.pairs_with FK.
func TestPairingToUnknownSlugRejectedByForeignKey(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)

	tomato := catalogtest.Annual()
	tomato.Pairings = []string{"does-not-exist"}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	err = catalog.Write(ctx, store.New(tx), []catalog.Ingredient{tomato}, nil)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("want foreign_key_violation (23503), got %v", err)
	}
}

// Review finding 4 (Plan 2 whole-plan review): deleting an ingredient must
// cascade to its seafood_region rows via the ON DELETE CASCADE FK.
func TestDeletingIngredientCascadesSeafoodRegions(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	writeTx(t, pool, []catalog.Ingredient{catalogtest.Seafood()}, nil)

	writeTx(t, pool, nil, []string{"atlantic-cod"})

	var count int
	err := pool.QueryRow(ctx, "SELECT count(*) FROM seafood_region WHERE slug = $1", "atlantic-cod").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("seafood_region rows after delete = %d, want 0", count)
	}
}
