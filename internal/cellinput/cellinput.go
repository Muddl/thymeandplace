// Package cellinput turns a query cell into engine inputs. Every lookup uses
// the cell's centre, so the inputs are a function of the cell and the live
// reference data, never of a point inside the cell. /season, /ingredient and
// calibration all resolve through it (core spec §11.3).
package cellinput

import (
	"context"
	"fmt"
	"time"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/season"
)

// Inputs are the engine inputs for one query cell.
type Inputs struct {
	QueryCell   int
	NormalsCell int                     // QueryCell, or the neighbour the fallback chose
	Normals     *climate.MonthlyNormals // nil when no land cell is within reach
	Ecoregions  []ocean.EcoregionID     // ascending; empty inland or with seafood off
}

// Supported reports whether the engine can say anything for the cell.
func (in Inputs) Supported() bool { return in.Normals != nil || len(in.Ecoregions) > 0 }

// Engine is the season-engine input for month m.
func (in Inputs) Engine(m time.Month) season.Input {
	return season.Input{Normals: in.Normals, Ecoregions: in.Ecoregions, Month: m}
}

type NormalsFinder interface {
	Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error)
}

type EcoregionFinder interface {
	Nearby(ctx context.Context, lat, lng float64) ([]ocean.EcoregionID, error)
}

// Resolver looks inputs up. A nil Ocean means no ecoregion is ever in reach
// (the public site with seafood off).
type Resolver struct {
	Climate NormalsFinder
	Ocean   EcoregionFinder
}

// Resolve returns the cell's inputs, looked up at its centre. CellID of a
// centre is the cell itself (climate.TestCellCenterRoundTrip), so the
// fallback measures from the centre of the query cell. An invalid cell ID is
// an error, and nothing is queried.
func (r Resolver) Resolve(ctx context.Context, cell int) (Inputs, error) {
	if !climate.ValidCellID(cell) {
		return Inputs{}, fmt.Errorf("cellinput: invalid cell %d", cell)
	}
	lat, lng := climate.CellCenter(cell)
	l, err := r.Climate.Normals(ctx, lat, lng)
	if err != nil {
		return Inputs{}, fmt.Errorf("cell %d normals: %w", cell, err)
	}
	in := Inputs{QueryCell: cell, NormalsCell: l.Cell, Normals: l.Normals}
	if r.Ocean != nil {
		if in.Ecoregions, err = r.Ocean.Nearby(ctx, lat, lng); err != nil {
			return Inputs{}, fmt.Errorf("cell %d ecoregions: %w", cell, err)
		}
	}
	return in, nil
}
