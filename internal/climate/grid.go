// Package climate holds the 0.25° climate grid and monthly normals.
package climate

import "math"

const (
	Resolution = 0.25
	GridCols   = 1440
	GridRows   = 720
)

// MonthlyNormals are 1991–2020 monthly climate normals for one grid cell.
// Index 0 is January.
type MonthlyNormals struct {
	TminC    [12]float64
	TmaxC    [12]float64
	PrecipMM [12]float64
}

// Mean is the monthly mean temperature, (tmin + tmax) / 2. m is 0-based.
func (n MonthlyNormals) Mean(m int) float64 {
	return (n.TminC[m] + n.TmaxC[m]) / 2
}

// CellID maps a coordinate to its grid cell. lat and lng must be finite;
// callers validate input before calling.
func CellID(lat, lng float64) int {
	row := int(math.Floor((90 - lat) / Resolution))
	row = min(max(row, 0), GridRows-1)
	col := int(math.Floor((normLng(lng) + 180) / Resolution))
	col = min(max(col, 0), GridCols-1)
	return row*GridCols + col
}

// CellCenter returns the centre coordinate of a cell.
func CellCenter(id int) (lat, lng float64) {
	row, col := id/GridCols, id%GridCols
	return 90 - (float64(row)+0.5)*Resolution, -180 + (float64(col)+0.5)*Resolution
}

// ValidCellID reports whether id is inside the grid.
func ValidCellID(id int) bool {
	return id >= 0 && id < GridRows*GridCols
}

// NeighborCells returns the up-to-8 cells around id. Longitude wraps;
// rows beyond the poles are omitted.
func NeighborCells(id int) []int {
	row, col := id/GridCols, id%GridCols
	out := make([]int, 0, 8)
	for dr := -1; dr <= 1; dr++ {
		r := row + dr
		if r < 0 || r >= GridRows {
			continue
		}
		for dc := -1; dc <= 1; dc++ {
			if dr == 0 && dc == 0 {
				continue
			}
			c := (col + dc + GridCols) % GridCols
			out = append(out, r*GridCols+c)
		}
	}
	return out
}

// normLng maps any longitude into [-180, 180).
func normLng(lng float64) float64 {
	l := math.Mod(lng+180, 360)
	if l < 0 {
		l += 360
	}
	return l - 180
}
