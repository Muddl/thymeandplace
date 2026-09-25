package changeset

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/store"
)

// newTestService returns a service over a DB seeded with SmallCatalog and the
// reference fixture (ecoregions 25, 30, 204 exist).
func newTestService(t *testing.T, seed bool) (*Service, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	if seed {
		tx, _ := pool.Begin(ctx)
		if err := catalog.Write(ctx, store.New(tx), catalogtest.SmallCatalog(), nil); err != nil {
			t.Fatal(err)
		}
		_ = tx.Commit(ctx)
	}
	cases := testCases(t)
	svc := NewService(pool, ocean.NewService(pool), func(context.Context) ([]calibration.Case, error) { return cases, nil })
	return svc, pool
}

func TestServicePreviewStoresPending(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, true)
	k := catalogtest.Hardy()
	k.Name = "Curly kale"
	pending, pv, err := svc.Preview(ctx, FormChangeset(k))
	if err != nil {
		t.Fatal(err)
	}
	if pending.ID == 0 || pending.BaseVersion == 0 || time.Until(pending.ExpiresAt) < 59*time.Minute {
		t.Fatalf("pending = %+v", pending)
	}
	if len(pv.Diff.Changed) != 1 || pv.Diff.Unchanged != 10 {
		t.Fatalf("diff = %+v", pv.Diff)
	}
	again, pv2, err := svc.Reopen(ctx, pending.ID)
	if err != nil || again.ID != pending.ID || len(pv2.Diff.Changed) != 1 {
		t.Fatalf("reopen: %+v %v", again, err)
	}
}

func TestServicePreviewInvalid(t *testing.T) {
	svc, _ := newTestService(t, true)
	bad := catalogtest.Seafood()
	bad.Regions[0].EcoregionID = 12345
	_, _, err := svc.Preview(context.Background(), FormChangeset(bad))
	var inv *InvalidError
	if !errors.As(err, &inv) || inv.Problems[0].Field != "regions[0].ecoregion_id" {
		t.Fatalf("err = %v", err)
	}
}

// Review Focus 1: exporting then importing the same file changes nothing.
func TestExportImportNoPhantomChanges(t *testing.T) {
	ctx := context.Background()
	svc, pool := newTestService(t, true)
	snap, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Export(&buf, snap, time.Now()); err != nil {
		t.Fatal(err)
	}
	cs, err := ImportChangeset(&buf)
	if err != nil {
		t.Fatal(err)
	}
	_, pv, err := svc.Preview(ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if !pv.Diff.Empty() || pv.Delta != 0 {
		t.Fatalf("phantom changes: %+v delta=%v", pv.Diff, pv.Delta)
	}
}

// Review Focus 5 at the service level.
func TestFirstImportIntoEmptyCatalog(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, false)
	var buf bytes.Buffer
	empty, _ := catalog.NewSnapshot(0, catalogtest.SmallCatalog())
	_ = Export(&buf, empty, time.Now())
	cs, _ := ImportChangeset(&buf)
	_, pv, err := svc.Preview(ctx, cs)
	if err != nil || len(pv.Diff.Added) != 11 || pv.NeedsOverride {
		t.Fatalf("pv=%+v err=%v", pv.Diff, err)
	}
}
