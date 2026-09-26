package admin

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/season"
	"thymeandplace/internal/store"
)

// catalogEnv is an admin env with the reference fixture, SmallCatalog and a
// changeset service scoring against the test fixture.
func catalogEnv(t *testing.T) (*env, *client) {
	t.Helper()
	e := newEnv(t, true)
	ctx := context.Background()
	dbtest.LoadReference(t, e.pool)
	tx, _ := e.pool.Begin(ctx)
	if err := catalog.Write(ctx, store.New(tx), catalogtest.SmallCatalog(), nil); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)

	fx, err := calibration.LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	n := climatetest.Temperate()
	cases := []calibration.Case{{Fixture: fx[0], Input: season.Input{Normals: &n}}}
	svc := changeset.NewService(e.pool, ocean.NewService(e.pool), func(context.Context) ([]calibration.Case, error) { return cases, nil })
	e.h = New(Deps{
		Auth: e.auth, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Changes: svc, Pool: e.pool,
		Load: func(ctx context.Context) (*catalog.Snapshot, error) { return catalog.Load(ctx, e.pool) },
	})
	c, _ := e.enrolled(t)
	return e, c
}

func (e *env) snap(t *testing.T) *catalog.Snapshot {
	t.Helper()
	s, err := catalog.Load(context.Background(), e.pool)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
