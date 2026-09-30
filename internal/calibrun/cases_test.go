package calibrun

import (
	"context"
	"testing"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/ocean"
)

func TestCases(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	fx := []calibration.Fixture{
		{ID: "lisbon", Name: "Lisbon", Lat: dbtest.Lisbon.Lat, Lng: dbtest.Lisbon.Lng, Source: "s"},
		{ID: "madrid", Name: "Madrid", Lat: dbtest.Madrid.Lat, Lng: dbtest.Madrid.Lng, Source: "s"},
		{ID: "sea", Name: "Sea", Lat: dbtest.MidAtlantic.Lat, Lng: dbtest.MidAtlantic.Lng, Source: "s"},
	}
	cases, err := Cases(ctx, fx, climate.NewService(pool), ocean.NewService(pool))
	if err != nil {
		t.Fatal(err)
	}
	if cases[0].Input.Normals == nil || len(cases[0].Input.Ecoregions) != 1 || cases[0].Input.Ecoregions[0] != 30 {
		t.Fatalf("lisbon = %+v", cases[0].Input)
	}
	if cases[1].Input.Normals == nil || len(cases[1].Input.Ecoregions) != 0 {
		t.Fatalf("madrid = %+v", cases[1].Input)
	}
	if cases[2].Input.Normals != nil {
		t.Fatalf("sea = %+v", cases[2].Input)
	}
}

type centreProbe struct{ got [][2]float64 }

func (c *centreProbe) Normals(_ context.Context, lat, lng float64) (climate.Lookup, error) {
	c.got = append(c.got, [2]float64{lat, lng})
	return climate.Lookup{QueryCell: climate.CellID(lat, lng), Cell: climate.CellID(lat, lng)}, nil
}

func (c *centreProbe) Nearby(_ context.Context, lat, lng float64) ([]ocean.EcoregionID, error) {
	c.got = append(c.got, [2]float64{lat, lng})
	return nil, nil
}

// Spec §4.5: each fixture's point selects its cell, and inputs are resolved
// at the cell centre exactly as /season does.
func TestCasesResolveAtCellCentre(t *testing.T) {
	fx := []calibration.Fixture{{ID: "a", Name: "A", Lat: 40.1, Lng: -8.2, Source: "s"}, {ID: "b", Name: "B", Lat: -33.87, Lng: 151.21, Source: "s"}}
	p := &centreProbe{}
	if _, err := Cases(context.Background(), fx, p, p); err != nil {
		t.Fatal(err)
	}
	if len(p.got) != 4 {
		t.Fatalf("lookups: %v", p.got)
	}
	for i, f := range fx {
		lat, lng := climate.CellCenter(climate.CellID(f.Lat, f.Lng))
		for _, got := range p.got[2*i : 2*i+2] {
			if got != [2]float64{lat, lng} {
				t.Fatalf("fixture %s looked up at %v, want (%v, %v)", f.ID, got, lat, lng)
			}
		}
	}
}
