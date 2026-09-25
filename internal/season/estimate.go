package season

import (
	"time"

	"thymeandplace/internal/catalog"
)

// Estimate returns every ingredient in season in in.Month, in slug order.
func Estimate(in Input, snap *catalog.Snapshot) []Result {
	if in.Month < time.January || in.Month > time.December {
		return nil
	}
	var out []Result
	for _, ing := range snap.All() {
		months, basis, ok := Year(in, ing)
		if !ok {
			continue
		}
		st := months[in.Month-1]
		if st == Out {
			continue
		}
		out = append(out, Result{
			Slug:     ing.Slug,
			Status:   st,
			Months:   months,
			Estimate: ing.Kind != catalog.KindSeafood,
			Basis:    basis,
		})
	}
	return out
}
