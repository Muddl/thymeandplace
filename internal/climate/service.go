package climate

import (
	"context"
	"fmt"
	"math"

	"thymeandplace/internal/store"
)

const earthRadiusKm = 6371.0088

// DistanceKm is the great-circle distance between two points.
func DistanceKm(lat1, lng1, lat2, lng2 float64) float64 {
	rad := math.Pi / 180
	dLat, dLng := (lat2-lat1)*rad, (lng2-lng1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKm * math.Asin(math.Min(1, math.Sqrt(a)))
}

// PickNearest returns own if it has data, otherwise the neighbour of own
// whose centre is nearest (lat, lng). Ties go to the lower cell ID. Public
// callers pass the centre of the query cell (internal/cellinput), so the
// result depends only on the cell.
func PickNearest(lat, lng float64, own int, cells map[int]MonthlyNormals) (int, bool) {
	if _, ok := cells[own]; ok {
		return own, true
	}
	best, bestD := -1, math.Inf(1)
	for _, id := range NeighborCells(own) {
		if _, ok := cells[id]; !ok {
			continue
		}
		clat, clng := CellCenter(id)
		d := DistanceKm(lat, lng, clat, clng)
		if d < bestD || (d == bestD && id < best) {
			best, bestD = id, d
		}
	}
	return best, best >= 0
}

// Lookup is the result of a normals lookup for one point.
type Lookup struct {
	QueryCell int             // cell containing the point
	Cell      int             // cell whose normals were used
	Normals   *MonthlyNormals // nil when no land cell is within reach
}

type Service struct{ db store.DBTX }

func NewService(db store.DBTX) *Service { return &Service{db: db} }

const liveView = "climate_cell"

// Normals looks up the point's cell and its neighbours in one query and
// applies the nearest-land fallback.
func (s *Service) Normals(ctx context.Context, lat, lng float64) (Lookup, error) {
	own := CellID(lat, lng)
	ids := []int32{int32(own)}
	for _, id := range NeighborCells(own) {
		ids = append(ids, int32(id))
	}
	rows, err := store.CellsIn(ctx, s.db, liveView, ids)
	if err != nil {
		return Lookup{}, fmt.Errorf("climate lookup: %w", err)
	}
	cells, err := CellsToNormals(rows)
	if err != nil {
		return Lookup{}, err
	}
	id, ok := PickNearest(lat, lng, own, cells)
	if !ok {
		return Lookup{QueryCell: own, Cell: own}, nil
	}
	n := cells[id]
	return Lookup{QueryCell: own, Cell: id, Normals: &n}, nil
}

// CellsToNormals converts rows, rejecting arrays that are not 12 long.
func CellsToNormals(rows []store.CellRow) (map[int]MonthlyNormals, error) {
	out := make(map[int]MonthlyNormals, len(rows))
	for _, r := range rows {
		if len(r.TminC) != 12 || len(r.TmaxC) != 12 || len(r.PrecipMM) != 12 {
			return nil, fmt.Errorf("cell %d: arrays must have 12 values", r.CellID)
		}
		var n MonthlyNormals
		for m := 0; m < 12; m++ {
			n.TminC[m], n.TmaxC[m], n.PrecipMM[m] = float64(r.TminC[m]), float64(r.TmaxC[m]), float64(r.PrecipMM[m])
		}
		out[int(r.CellID)] = n
	}
	return out, nil
}
