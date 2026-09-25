package climate

import (
	"math"
	"testing"
)

func TestCellIDFormula(t *testing.T) {
	cases := []struct {
		name     string
		lat, lng float64
		want     int
	}{
		{"north-west corner", 89.9, -179.9, 0},
		{"first row second col", 89.9, -179.7, 1},
		{"second row", 89.7, -179.9, 1440},
		{"london", 51.5074, -0.1278, 153*1440 + 719},
		{"sydney", -33.8688, 151.2093, 495*1440 + 1324},
		{"south-east corner", -89.99, 179.99, 719*1440 + 1439},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CellID(c.lat, c.lng); got != c.want {
				t.Fatalf("CellID(%v,%v) = %d, want %d", c.lat, c.lng, got, c.want)
			}
		})
	}
}

// Review Focus 1: edges and out-of-range inputs stay inside the grid.
func TestCellIDEdges(t *testing.T) {
	cases := []struct {
		name     string
		lat, lng float64
		want     int
	}{
		{"lat exactly 90", 90, 0, 0*1440 + 720},
		{"lat exactly -90 clamps to last row", -90, 0, 719*1440 + 720},
		{"lat below -90 clamps", -123, 0, 719*1440 + 720},
		{"lat above 90 clamps", 100, 0, 0*1440 + 720},
		{"lng exactly 180 wraps to -180", 0, 180, 360*1440 + 0},
		{"lng exactly -180", 0, -180, 360*1440 + 0},
		{"lng 540 wraps to -180", 0, 540, 360*1440 + 0},
		{"lng -181 wraps to 179", 0, -181, 360*1440 + 1436},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CellID(c.lat, c.lng)
			if got != c.want {
				t.Fatalf("CellID(%v,%v) = %d, want %d", c.lat, c.lng, got, c.want)
			}
			if !ValidCellID(got) {
				t.Fatalf("CellID(%v,%v) = %d is not a valid cell", c.lat, c.lng, got)
			}
		})
	}
}

func TestCellCenterRoundTrip(t *testing.T) {
	for _, id := range []int{0, 1, 1440, 153*1440 + 719, 719*1440 + 1439} {
		lat, lng := CellCenter(id)
		if got := CellID(lat, lng); got != id {
			t.Fatalf("CellID(CellCenter(%d)) = %d", id, got)
		}
	}
	lat, lng := CellCenter(0)
	if math.Abs(lat-89.875) > 1e-9 || math.Abs(lng+179.875) > 1e-9 {
		t.Fatalf("CellCenter(0) = %v,%v, want 89.875,-179.875", lat, lng)
	}
}

func TestNeighborCells(t *testing.T) {
	mid := 360*1440 + 720
	got := NeighborCells(mid)
	if len(got) != 8 {
		t.Fatalf("mid-grid neighbours = %d, want 8", len(got))
	}
	want := map[int]bool{
		359*1440 + 719: true, 359*1440 + 720: true, 359*1440 + 721: true,
		360*1440 + 719: true, 360*1440 + 721: true,
		361*1440 + 719: true, 361*1440 + 720: true, 361*1440 + 721: true,
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected neighbour %d", id)
		}
	}

	// Longitude wraps: column 0 neighbours column 1439.
	west := 360*1440 + 0
	found := false
	for _, id := range NeighborCells(west) {
		if id == 360*1440+1439 {
			found = true
		}
	}
	if !found {
		t.Fatalf("column 0 should neighbour column 1439")
	}

	// Top row has no row above: 5 neighbours.
	if n := len(NeighborCells(0*1440 + 5)); n != 5 {
		t.Fatalf("top row neighbours = %d, want 5", n)
	}
}

func TestValidCellID(t *testing.T) {
	if ValidCellID(-1) || ValidCellID(720*1440) {
		t.Fatal("out-of-range IDs must be invalid")
	}
	if !ValidCellID(0) || !ValidCellID(720*1440-1) {
		t.Fatal("boundary IDs must be valid")
	}
}

func TestMean(t *testing.T) {
	var n MonthlyNormals
	n.TminC[3], n.TmaxC[3] = 4, 16
	if n.Mean(3) != 10 {
		t.Fatalf("Mean = %v, want 10", n.Mean(3))
	}
}
