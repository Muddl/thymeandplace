package catalog_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

// writeTx runs catalog.Write in its own committed transaction.
func writeTx(t *testing.T, pool *pgxpool.Pool, upserts []catalog.Ingredient, deletes []string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := catalog.Write(ctx, store.New(tx), upserts, deletes); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWriteLoadRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)

	snap, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Writing the catalog fires the version triggers from migration 00004,
	// so a fresh catalog must show a bumped (non-zero) version.
	if snap.Len() != 11 || snap.Version() == 0 {
		t.Fatalf("len=%d version=%d", snap.Len(), snap.Version())
	}
	for _, want := range catalogtest.SmallCatalog() {
		got, ok := snap.Get(want.Slug)
		if !ok {
			t.Fatalf("%s missing", want.Slug)
		}
		if !reflect.DeepEqual(got, catalog.Normalize(want)) {
			t.Errorf("%s:\n got %+v\nwant %+v", want.Slug, got, catalog.Normalize(want))
		}
	}
}

// Review Focus 1: 16.1 survives the real column.
func TestRealPrecisionRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	i := catalogtest.Annual()
	i.GrowTempMinC, i.GrowTempMaxC = catalogtest.P(16.1), catalogtest.P(29.9)
	writeTx(t, pool, []catalog.Ingredient{i}, nil)
	snap, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := snap.Get("tomato")
	if *got.GrowTempMinC != 16.1 || *got.GrowTempMaxC != 29.9 {
		t.Fatalf("got %v / %v", *got.GrowTempMinC, *got.GrowTempMaxC)
	}
}

func TestWriteReplacesLinks(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)

	tomato := catalogtest.Annual()
	tomato.Pairings = []string{"kale"} // basil removed, kale added
	cod := catalogtest.Seafood()
	cod.Regions = []catalog.SeafoodRegion{{EcoregionID: 30, Months: []int{12}}}
	writeTx(t, pool, []catalog.Ingredient{tomato, cod}, []string{"banana"})

	snap, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := snap.Get("tomato"); !reflect.DeepEqual(got.Pairings, []string{"kale"}) {
		t.Fatalf("tomato pairings = %v", got.Pairings)
	}
	if got, _ := snap.Get("atlantic-cod"); len(got.Regions) != 1 || got.Regions[0].EcoregionID != 30 {
		t.Fatalf("cod regions = %+v", got.Regions)
	}
	if _, ok := snap.Get("banana"); ok {
		t.Fatal("banana not deleted")
	}
}

func TestWritePairingsBetweenNewIngredients(t *testing.T) {
	pool := dbtest.New(t)
	a := catalogtest.Hardy()
	a.Slug, a.Pairings = "chard", []string{"leek"}
	b := catalogtest.Hardy()
	b.Slug, b.Pairings = "leek", []string{"chard"}
	writeTx(t, pool, []catalog.Ingredient{a, b}, nil) // fails on FK if rows aren't written first
}
