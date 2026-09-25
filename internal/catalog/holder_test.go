package catalog_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHolderStartsEmpty(t *testing.T) {
	h := catalog.NewHolder(func(context.Context) (*catalog.Snapshot, error) { return nil, errors.New("down") }, quiet())
	if h.Current() != nil {
		t.Fatal("Current before load must be nil")
	}
	if err := h.Reload(context.Background()); err == nil || h.Current() != nil {
		t.Fatal("failed first load must leave Current nil and return an error")
	}
}

// Review Focus 4: a failed reload keeps the previous snapshot.
func TestHolderKeepsPreviousOnFailure(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)

	h := catalog.NewHolder(func(ctx context.Context) (*catalog.Snapshot, error) { return catalog.Load(ctx, pool) }, quiet())
	if err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	first := h.Current()
	if first == nil || first.Len() != 11 {
		t.Fatalf("first = %v", first)
	}

	pool.Close() // database unreachable
	if err := h.Reload(ctx); err == nil {
		t.Fatal("reload against a closed pool must fail")
	}
	if h.Current() != first {
		t.Fatal("failed reload replaced the live snapshot")
	}
}
