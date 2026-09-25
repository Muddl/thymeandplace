package catalog

import (
	"slices"
	"sort"
	"strconv"
)

// Normalize puts an ingredient in canonical form so that values survive a
// database round trip unchanged and equal ingredients compare equal:
// real fields are rounded to float32 precision (shortest decimal form),
// pairings and regions are sorted, and empty slices become nil.
func Normalize(ing Ingredient) Ingredient {
	ing = clone(ing)
	for _, p := range []**float64{
		&ing.GrowTempMinC, &ing.GrowTempMaxC, &ing.WarmupTempC,
		&ing.FruitingTempMinC, &ing.FruitingTempMaxC, &ing.MinPrecipMM,
	} {
		if *p != nil {
			v := Real32(**p)
			*p = &v
		}
	}
	slices.Sort(ing.Pairings)
	if len(ing.Pairings) == 0 {
		ing.Pairings = nil
	}
	sort.Slice(ing.Regions, func(i, j int) bool { return ing.Regions[i].EcoregionID < ing.Regions[j].EcoregionID })
	for i := range ing.Regions {
		slices.Sort(ing.Regions[i].Months)
		slices.Sort(ing.Regions[i].PeakMonths)
		if len(ing.Regions[i].Months) == 0 {
			ing.Regions[i].Months = nil
		}
		if len(ing.Regions[i].PeakMonths) == 0 {
			ing.Regions[i].PeakMonths = nil
		}
	}
	if len(ing.Regions) == 0 {
		ing.Regions = nil
	}
	return ing
}

// Real32 rounds v to the nearest float32 and returns its shortest decimal
// representation as a float64 (16.1 stays 16.1).
func Real32(v float64) float64 {
	out, _ := strconv.ParseFloat(strconv.FormatFloat(float64(float32(v)), 'g', -1, 32), 64)
	return out
}
