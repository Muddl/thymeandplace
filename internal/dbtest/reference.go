package dbtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/climate"
)

type Point struct{ Lat, Lng float64 }

var (
	Lisbon      = Point{38.7167, -9.1333}
	Madrid      = Point{40.4165, -3.7026}
	Tromso      = Point{69.6496, 18.9560}
	Sydney      = Point{-33.8679, 151.2073}
	MidAtlantic = Point{0, -30}
	// FallbackPoint is the centre of the cell just west of Lisbon's cell.
	// That cell has no row, so lookups must fall back to Lisbon's cell.
	FallbackPoint = Point{38.625, -9.375}
)

type cellFixture struct {
	at                 Point
	tmin, tmax, precip [12]float32
}

var cells = []cellFixture{
	{Lisbon,
		[12]float32{8.3, 9.1, 10.9, 12.0, 14.1, 16.9, 18.3, 18.8, 17.9, 15.2, 11.8, 9.6},
		[12]float32{14.8, 16.2, 18.9, 20.3, 23.0, 26.6, 28.7, 29.0, 26.9, 22.8, 18.3, 15.5},
		[12]float32{100, 90, 60, 70, 50, 15, 5, 5, 30, 100, 110, 125}},
	{Madrid,
		[12]float32{2.7, 3.7, 5.6, 7.2, 10.7, 15.1, 18.1, 17.9, 14.6, 10.5, 6.1, 3.5},
		[12]float32{9.8, 12.0, 16.3, 18.2, 22.2, 28.2, 32.1, 31.3, 26.4, 19.8, 14.0, 10.4},
		[12]float32{33, 35, 25, 45, 50, 20, 10, 10, 25, 60, 50, 55}},
	{Tromso,
		[12]float32{-6.5, -6.7, -5.5, -2.5, 1.5, 5.8, 9.0, 8.4, 4.6, 0.5, -3.3, -5.3},
		[12]float32{-1.6, -1.6, 0.2, 3.3, 7.9, 12.5, 15.8, 14.6, 10.0, 4.6, 1.3, -0.6},
		[12]float32{110, 95, 90, 65, 50, 55, 75, 85, 105, 130, 110, 115}},
	{Sydney,
		[12]float32{18.7, 18.8, 17.6, 14.7, 11.5, 9.3, 8.1, 9.0, 11.1, 13.6, 15.6, 17.5},
		[12]float32{25.9, 25.8, 24.7, 22.4, 19.4, 16.9, 16.3, 17.8, 20.0, 22.1, 23.6, 25.2},
		[12]float32{95, 120, 130, 125, 120, 130, 100, 80, 70, 75, 85, 75}},
}

var ecoregions = []struct {
	id   int32
	name string
	wkt  string
}{
	{30, "Test Iberian Shelf", "MULTIPOLYGON(((-10.5 36.5,-9.4 36.5,-9.4 43.5,-10.5 43.5,-10.5 36.5)))"},
	{25, "Test North Sea", "MULTIPOLYGON(((0 51,8 51,8 58,0 58,0 51)))"},
	{204, "Test Tasman Coast", "MULTIPOLYGON(((151.4 -35,153 -35,153 -32,151.4 -32,151.4 -35)))"},
}

type placeFixture struct {
	id                  int32
	name, ascii, admin1 string
	country             string
	pop                 int32
	at                  Point
}

var namedPlaces = []placeFixture{
	{1, "Lisbon", "Lisbon", "Lisbon", "PT", 506654, Point{38.7167, -9.1333}},
	{2, "Porto", "Porto", "Porto", "PT", 249633, Point{41.1496, -8.611}},
	{3, "Madrid", "Madrid", "Madrid", "ES", 3255944, Point{40.4165, -3.7026}},
	{4, "Tromsø", "Tromso", "Troms", "NO", 38980, Point{69.6496, 18.9560}},
	{5, "Sydney", "Sydney", "New South Wales", "AU", 4627345, Point{-33.8679, 151.2073}},
	{6, "London", "London", "England", "GB", 8961989, Point{51.5085, -0.1257}},
	{7, "London", "London", "Ontario", "CA", 346765, Point{42.9834, -81.2330}},
	{8, "Paris", "Paris", "Île-de-France", "FR", 2138551, Point{48.8534, 2.3488}},
	{9, "Paris", "Paris", "Texas", "US", 24171, Point{33.6609, -95.5555}},
	{10, "Zürich", "Zurich", "Zurich", "CH", 341730, Point{47.3667, 8.55}},
	{11, "São Paulo", "Sao Paulo", "São Paulo", "BR", 10021295, Point{-23.5475, -46.6361}},
}

// LoadReference inserts the small reference fixture into the *_v0 tables
// behind the live views.
func LoadReference(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, c := range cells {
		_, err := pool.Exec(ctx, `INSERT INTO climate_cell_v0 VALUES ($1,$2,$3,$4)`,
			climate.CellID(c.at.Lat, c.at.Lng), c.tmin[:], c.tmax[:], c.precip[:])
		if err != nil {
			t.Fatalf("cell: %v", err)
		}
	}
	for _, e := range ecoregions {
		if _, err := pool.Exec(ctx, `INSERT INTO ocean_ecoregion_v0 VALUES ($1,$2,ST_GeogFromText($3))`, e.id, e.name, e.wkt); err != nil {
			t.Fatalf("ecoregion: %v", err)
		}
	}
	all := append([]placeFixture{}, namedPlaces...)
	for i := 0; i < 39; i++ {
		all = append(all, placeFixture{
			id: int32(100 + i), name: fmt.Sprintf("Filler %02d", i), ascii: fmt.Sprintf("Filler %02d", i),
			country: "ZZ", pop: int32(1000 + i), at: Point{-60 + float64(i)*3, -170 + float64(i)*8},
		})
	}
	for _, p := range all {
		var admin1 any
		if p.admin1 != "" {
			admin1 = p.admin1
		}
		_, err := pool.Exec(ctx, `INSERT INTO place_v0 VALUES ($1,$2,$3,$4,$5,$6, ST_SetSRID(ST_MakePoint($8,$7),4326)::geography)`,
			p.id, p.name, p.ascii, admin1, p.country, p.pop, p.at.Lat, p.at.Lng)
		if err != nil {
			t.Fatalf("place %s: %v", p.name, err)
		}
	}
}
