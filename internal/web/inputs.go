package web

import (
	"context"

	"thymeandplace/internal/cache"
	"thymeandplace/internal/cellinput"
)

// inputsFor returns the query cell's engine inputs from the per-cell cache,
// or resolves them (at the cell centre) through the DB gate and caches them.
// /season misses and /ingredient both read through it, so the list and the
// detail page always see the same inputs for a cell. Cells with no normals
// are cached too; failures are not.
func (s *Server) inputsFor(ctx context.Context, cell int, version string) (cellinput.Inputs, error) {
	key := cache.CellKey{CellID: cell, DataVersion: version}
	if in, ok := s.cells.Get(key); ok {
		return in, nil
	}
	var in cellinput.Inputs
	err := s.withDB(ctx, func(ctx context.Context) error {
		var err error
		in, err = s.inputs.Resolve(ctx, cell)
		return err
	})
	if err != nil {
		return cellinput.Inputs{}, err
	}
	s.cells.Add(key, in)
	return in, nil
}
