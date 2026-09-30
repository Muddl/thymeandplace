package cellinput_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"thymeandplace/internal/cellinput"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/ocean"
)

type seen struct{ lat, lng float64 }

type fakeNormals struct {
	got    []seen
	lookup climate.Lookup
	err    error
}

func (f *fakeNormals) Normals(_ context.Context, lat, lng float64) (climate.Lookup, error) {
	f.got = append(f.got, seen{lat, lng})
	return f.lookup, f.err
}

type fakeEco struct {
	got []seen
	ids []ocean.EcoregionID
	err error
}

func (f *fakeEco) Nearby(_ context.Context, lat, lng float64) ([]ocean.EcoregionID, error) {
	f.got = append(f.got, seen{lat, lng})
	return f.ids, f.err
}

// Review Focus 5: cells at the grid's edges and corners pass their exact
// centres.
func TestResolveLooksUpAtCellCentre(t *testing.T) {
	ctx := context.Background()
	last := climate.GridRows*climate.GridCols - 1
	for _, cell := range []int{0, climate.GridCols - 1, climate.CellID(40.1, -8.2), (climate.GridRows - 1) * climate.GridCols, last} {
		n := climatetest.Temperate()
		cl := &fakeNormals{lookup: climate.Lookup{QueryCell: cell, Cell: cell + 1, Normals: &n}}
		oc := &fakeEco{ids: []ocean.EcoregionID{25}}
		in, err := cellinput.Resolver{Climate: cl, Ocean: oc}.Resolve(ctx, cell)
		if err != nil {
			t.Fatalf("cell %d: %v", cell, err)
		}
		lat, lng := climate.CellCenter(cell)
		want := seen{lat, lng}
		if len(cl.got) != 1 || cl.got[0] != want || len(oc.got) != 1 || oc.got[0] != want {
			t.Fatalf("cell %d: normals at %v, ocean at %v; want centre %v", cell, cl.got, oc.got, want)
		}
		if in.QueryCell != cell || in.NormalsCell != cell+1 || in.Normals != &n || len(in.Ecoregions) != 1 {
			t.Fatalf("cell %d: %+v", cell, in)
		}
	}
}

func TestResolveWithoutOceanSkipsEcoregions(t *testing.T) {
	cl := &fakeNormals{}
	in, err := cellinput.Resolver{Climate: cl}.Resolve(context.Background(), 100)
	if err != nil || in.Ecoregions != nil {
		t.Fatalf("%+v %v", in, err)
	}
}

func TestResolveErrors(t *testing.T) {
	ctx := context.Background()
	down := errors.New("down")
	cl, oc := &fakeNormals{}, &fakeEco{}
	for _, bad := range []int{-1, climate.GridRows * climate.GridCols} {
		if _, err := (cellinput.Resolver{Climate: cl, Ocean: oc}).Resolve(ctx, bad); err == nil {
			t.Fatalf("cell %d: want error", bad)
		}
	}
	if len(cl.got)+len(oc.got) != 0 {
		t.Fatal("an invalid cell must not query")
	}
	if _, err := (cellinput.Resolver{Climate: &fakeNormals{err: down}, Ocean: oc}).Resolve(ctx, 5); !errors.Is(err, down) {
		t.Fatalf("normals error not wrapped: %v", err)
	}
	if _, err := (cellinput.Resolver{Climate: &fakeNormals{}, Ocean: &fakeEco{err: down}}).Resolve(ctx, 5); !errors.Is(err, down) {
		t.Fatalf("ocean error not wrapped: %v", err)
	}
}

func TestSupportedAndEngine(t *testing.T) {
	n := climatetest.Temperate()
	for _, tc := range []struct {
		in   cellinput.Inputs
		want bool
	}{
		{cellinput.Inputs{}, false},
		{cellinput.Inputs{Normals: &n}, true},
		{cellinput.Inputs{Ecoregions: []ocean.EcoregionID{1}}, true},
		{cellinput.Inputs{Normals: &n, Ecoregions: []ocean.EcoregionID{1}}, true},
	} {
		if got := tc.in.Supported(); got != tc.want {
			t.Errorf("%+v: Supported = %v", tc.in, got)
		}
	}
	e := cellinput.Inputs{Normals: &n, Ecoregions: []ocean.EcoregionID{3}}.Engine(time.March)
	if e.Normals != &n || e.Month != time.March || len(e.Ecoregions) != 1 {
		t.Fatalf("Engine = %+v", e)
	}
}

// Integration: the real services behind the resolver.
func TestResolveAgainstReferenceData(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	r := cellinput.Resolver{Climate: climate.NewService(pool), Ocean: ocean.NewService(pool)}

	lisbon := climate.CellID(dbtest.Lisbon.Lat, dbtest.Lisbon.Lng)
	in, err := r.Resolve(ctx, lisbon)
	if err != nil || in.Normals == nil || in.NormalsCell != lisbon || len(in.Ecoregions) != 1 || in.Ecoregions[0] != 30 {
		t.Fatalf("lisbon: %+v %v", in, err)
	}
	fb := climate.CellID(dbtest.FallbackPoint.Lat, dbtest.FallbackPoint.Lng)
	if in, err := r.Resolve(ctx, fb); err != nil || in.Normals == nil || in.NormalsCell != lisbon || in.QueryCell != fb {
		t.Fatalf("fallback: %+v %v", in, err)
	}
	sea := climate.CellID(dbtest.MidAtlantic.Lat, dbtest.MidAtlantic.Lng)
	if in, err := r.Resolve(ctx, sea); err != nil || in.Supported() {
		t.Fatalf("mid-Atlantic: %+v %v", in, err)
	}
}
