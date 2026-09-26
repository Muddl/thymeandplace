package dbtest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/devdata"
)

type Point = devdata.Point

var (
	Lisbon        = devdata.Lisbon
	Madrid        = devdata.Madrid
	Tromso        = devdata.Tromso
	Sydney        = devdata.Sydney
	MidAtlantic   = devdata.MidAtlantic
	FallbackPoint = devdata.FallbackPoint
)

// LoadReference inserts the small reference fixture behind the live views.
func LoadReference(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	if err := devdata.SeedReference(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
}
