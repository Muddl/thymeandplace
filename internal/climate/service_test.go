package climate_test

import (
	"context"
	"math"
	"testing"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/dbtest"
)

func TestDistanceKm(t *testing.T) {
	// London → Paris ≈ 344 km.
	if d := climate.DistanceKm(51.5074, -0.1278, 48.8566, 2.3522); math.Abs(d-343.5) > 2 {
		t.Fatalf("London–Paris = %.1f km", d)
	}
}

func TestPickNearest(t *testing.T) {
	own := climate.CellID(10, 10)
	var n climate.MonthlyNormals
	cells := map[int]climate.MonthlyNormals{own: n}
	if id, ok := climate.PickNearest(10, 10, own, cells); !ok || id != own {
		t.Fatal("own cell must win when present")
	}
	nb := climate.NeighborCells(own)
	// Point in the east part of its cell: the east neighbour is nearer than the west.
	lat, lng := climate.CellCenter(own)
	lng += 0.1
	east, west := own+1, own-1
	cells = map[int]climate.MonthlyNormals{east: n, west: n}
	if id, ok := climate.PickNearest(lat, lng, own, cells); !ok || id != east {
		t.Fatalf("got %d, want east %d (neighbours %v)", id, east, nb)
	}
	if _, ok := climate.PickNearest(lat, lng, own, map[int]climate.MonthlyNormals{}); ok {
		t.Fatal("no cells → not ok")
	}
	// A far cell that is not a neighbour must be ignored.
	if _, ok := climate.PickNearest(lat, lng, own, map[int]climate.MonthlyNormals{own + 5: n}); ok {
		t.Fatal("non-neighbour used")
	}
	// From the centre, opposite neighbours are exactly equidistant (centres
	// are multiples of 0.125°): ties go to the lower cell ID. Public callers
	// always pass the query cell's centre (cellinput), so this is routine.
	clat, clng := climate.CellCenter(own)
	if id, ok := climate.PickNearest(clat, clng, own, map[int]climate.MonthlyNormals{east: n, west: n}); !ok || id != west {
		t.Fatalf("east/west tie: got %d, want west %d", id, west)
	}
	north, south := own-climate.GridCols, own+climate.GridCols
	if id, ok := climate.PickNearest(clat, clng, own, map[int]climate.MonthlyNormals{north: n, south: n}); !ok || id != north {
		t.Fatalf("north/south tie: got %d, want north %d", id, north)
	}
}

func TestServiceNormals(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	svc := climate.NewService(pool)

	l, err := svc.Normals(ctx, dbtest.Lisbon.Lat, dbtest.Lisbon.Lng)
	if err != nil || l.Normals == nil || l.Cell != l.QueryCell {
		t.Fatalf("lisbon: %+v %v", l, err)
	}
	if math.Abs(l.Normals.TminC[0]-8.3) > 1e-4 {
		t.Fatalf("tmin Jan = %v", l.Normals.TminC[0])
	}

	// Review Focus 2: coastal cell with no row falls back to the land neighbour.
	fb, err := svc.Normals(ctx, dbtest.FallbackPoint.Lat, dbtest.FallbackPoint.Lng)
	if err != nil || fb.Normals == nil || fb.Cell != l.Cell || fb.QueryCell == fb.Cell {
		t.Fatalf("fallback: %+v %v", fb, err)
	}

	sea, err := svc.Normals(ctx, dbtest.MidAtlantic.Lat, dbtest.MidAtlantic.Lng)
	if err != nil || sea.Normals != nil {
		t.Fatalf("mid-ocean: %+v %v", sea, err)
	}

	n, err := svc.ForCell(ctx, l.Cell)
	if err != nil || n == nil {
		t.Fatalf("ForCell: %v %v", n, err)
	}
	if n, err := svc.ForCell(ctx, sea.QueryCell); err != nil || n != nil {
		t.Fatalf("ForCell(absent) = %v %v", n, err)
	}
}
