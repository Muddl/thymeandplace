// Package calibrun connects calibration fixtures to live reference data and
// runs the daily drift check.
package calibrun

import (
	"context"
	"fmt"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/season"
)

type NormalsFinder interface {
	Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error)
}

type EcoregionFinder interface {
	Nearby(ctx context.Context, lat, lng float64) ([]ocean.EcoregionID, error)
}

// Cases resolves every fixture location to an engine input (once per call).
func Cases(ctx context.Context, fixtures []calibration.Fixture, cl NormalsFinder, oc EcoregionFinder) ([]calibration.Case, error) {
	out := make([]calibration.Case, 0, len(fixtures))
	for _, f := range fixtures {
		l, err := cl.Normals(ctx, f.Lat, f.Lng)
		if err != nil {
			return nil, fmt.Errorf("fixture %s normals: %w", f.ID, err)
		}
		eco, err := oc.Nearby(ctx, f.Lat, f.Lng)
		if err != nil {
			return nil, fmt.Errorf("fixture %s ecoregions: %w", f.ID, err)
		}
		out = append(out, calibration.Case{Fixture: f, Input: season.Input{Normals: l.Normals, Ecoregions: eco}})
	}
	return out, nil
}
