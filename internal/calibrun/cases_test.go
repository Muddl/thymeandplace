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
