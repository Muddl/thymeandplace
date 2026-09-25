package ocean_test

import (
	"context"
	"reflect"
	"testing"

	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/ocean"
)

func TestNearby(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	svc := ocean.NewService(pool)

	cases := []struct {
		name     string
		lat, lng float64
		want     []ocean.EcoregionID
	}{
		{"lisbon coast", dbtest.Lisbon.Lat, dbtest.Lisbon.Lng, []ocean.EcoregionID{30}},
		{"sydney coast", dbtest.Sydney.Lat, dbtest.Sydney.Lng, []ocean.EcoregionID{204}},
		{"madrid inland", dbtest.Madrid.Lat, dbtest.Madrid.Lng, nil},
		// East edge of ecoregion 30 is lng -9.4. At lat 40, 1° lng ≈ 85.40 km.
		{"97 km inside reach", 40, -9.4 + 97/85.40, []ocean.EcoregionID{30}},
		{"103 km out of reach", 40, -9.4 + 103/85.40, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := svc.Nearby(ctx, c.lat, c.lng)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v %v, want %v", got, err, c.want)
			}
		})
	}
}

func TestMissing(t *testing.T) {
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	got, err := ocean.NewService(pool).Missing(context.Background(), []ocean.EcoregionID{25, 999, 30, 7})
	if err != nil || !reflect.DeepEqual(got, []ocean.EcoregionID{7, 999}) {
		t.Fatalf("got %v %v", got, err)
	}
}
