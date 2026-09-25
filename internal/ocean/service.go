package ocean

import (
	"context"
	"fmt"
	"slices"

	"thymeandplace/internal/store"
)

// ReachMeters is how far a coast may be from an ecoregion to count as "nearby".
const ReachMeters = 100_000

type Service struct{ db store.DBTX }

func NewService(db store.DBTX) *Service { return &Service{db: db} }

// Nearby returns ecoregions within ReachMeters of the point, ascending.
func (s *Service) Nearby(ctx context.Context, lat, lng float64) ([]EcoregionID, error) {
	ids, err := store.NearbyEcoregions(ctx, s.db, "ocean_ecoregion", lat, lng, ReachMeters)
	if err != nil {
		return nil, fmt.Errorf("ocean lookup: %w", err)
	}
	var out []EcoregionID
	for _, id := range ids {
		out = append(out, EcoregionID(id))
	}
	return out, nil
}

// Missing returns the IDs that do not exist in the live ecoregion data, ascending.
func (s *Service) Missing(ctx context.Context, ids []EcoregionID) ([]EcoregionID, error) {
	req := make([]int32, len(ids))
	for i, id := range ids {
		req[i] = int32(id)
	}
	found, err := store.New(s.db).ExistingEcoregions(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ecoregion check: %w", err)
	}
	var out []EcoregionID
	for _, id := range ids {
		if !slices.Contains(found, int32(id)) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out, nil
}
