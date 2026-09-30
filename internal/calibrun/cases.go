// Package calibrun connects calibration fixtures to live reference data and
// runs the daily drift check.
package calibrun

import (
	"context"
	"fmt"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/cellinput"
	"thymeandplace/internal/climate"
)

type (
	NormalsFinder   = cellinput.NormalsFinder
	EcoregionFinder = cellinput.EcoregionFinder
)

// Cases resolves every fixture to an engine input (once per call). Each
// fixture's point selects its cell, and the inputs are resolved at the cell
// centre, exactly as /season does (core spec §13.2). The month is left unset.
func Cases(ctx context.Context, fixtures []calibration.Fixture, cl NormalsFinder, oc EcoregionFinder) ([]calibration.Case, error) {
	r := cellinput.Resolver{Climate: cl, Ocean: oc}
	out := make([]calibration.Case, 0, len(fixtures))
	for _, f := range fixtures {
		in, err := r.Resolve(ctx, climate.CellID(f.Lat, f.Lng))
		if err != nil {
			return nil, fmt.Errorf("fixture %s: %w", f.ID, err)
		}
		out = append(out, calibration.Case{Fixture: f, Input: in.Engine(0)})
	}
	return out, nil
}
