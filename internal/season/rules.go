package season

import (
	"fmt"
	"strconv"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/ocean"
)

// Year computes the full-year statuses for one ingredient at one location.
// ok is false when the ingredient is not in season in any month.
// in.Month is ignored.
func Year(in Input, ing catalog.Ingredient) (months [12]Status, basis string, ok bool) {
	if ing.Kind == catalog.KindSeafood {
		months, basis = seafood(in.Ecoregions, ing)
	} else if in.Normals != nil {
		switch ing.Kind {
		case catalog.KindYearRound:
			months, basis = allYearRound(), "grown year-round"
		case catalog.KindAnnual:
			months, basis = annual(in.Normals, ing)
		case catalog.KindPerennialFruit:
			months, basis = perennial(in.Normals, ing)
		case catalog.KindWildMushroom:
			months, basis = wildMushroom(in.Normals, ing)
		}
	}
	for _, s := range months {
		if s != Out {
			return months, basis, true
		}
	}
	return [12]Status{}, "", false
}

func allYearRound() [12]Status {
	var m [12]Status
	for i := range m {
		m[i] = YearRound
	}
	return m
}

func frostFree(n *climate.MonthlyNormals, m int) bool { return n.TminC[m] > frostFreeTminC }

func annual(n *climate.MonthlyNormals, ing catalog.Ingredient) ([12]Status, string) {
	lo, hi, toHarvest := *ing.GrowTempMinC, *ing.GrowTempMaxC, *ing.MonthsToHarvest
	var grow [12]bool
	all := true
	for m := 0; m < 12; m++ {
		mean := n.Mean(m)
		grow[m] = lo <= mean && mean <= hi && (ing.Frost == catalog.FrostHardy || frostFree(n, m))
		all = all && grow[m]
	}
	if all {
		return allYearRound(), "growing conditions all year"
	}
	var harvest [12]bool
	for _, r := range runs(grow, 0) {
		if r.Len > toHarvest {
			for i := toHarvest; i < r.Len; i++ {
				harvest[(r.Start+i)%12] = true
			}
		}
	}
	months := harvestStatuses(harvest, 0)
	applyStorage(&months, harvest, 0, ing.StorageMonths)
	return months, fmt.Sprintf("growing conditions %s; about %d months to harvest", FormatMonths(grow), toHarvest)
}

func perennial(n *climate.MonthlyNormals, ing catalog.Ingredient) ([12]Status, string) {
	chill := 0
	for m := 0; m < 12; m++ {
		if n.Mean(m) < chillMeanC {
			chill++
		}
	}
	if chill < *ing.ChillMonthsMin {
		return [12]Status{}, ""
	}
	warm := *ing.WarmupTempC
	w := -1
	for m := 0; m < 12; m++ {
		if n.Mean(m) >= warm && n.Mean((m+11)%12) < warm {
			w = m
			break
		}
	}
	if w < 0 {
		return [12]Status{}, ""
	}
	start := (w + *ing.MonthsBloomToHarvest) % 12
	var harvest [12]bool
	for i := 0; i < *ing.HarvestSpanMonths; i++ {
		m := (start + i) % 12
		if ing.Frost == catalog.FrostTender && !frostFree(n, m) {
			continue
		}
		harvest[m] = true
	}
	months := harvestStatuses(harvest, start)
	applyStorage(&months, harvest, start, ing.StorageMonths)
	return months, fmt.Sprintf("warms past %s °C in %s; harvest %s", fmtC(warm), monthAbbr(w), FormatMonths(harvest))
}

func wildMushroom(n *climate.MonthlyNormals, ing catalog.Ingredient) ([12]Status, string) {
	var fruit [12]bool
	for m := 0; m < 12; m++ {
		mean := n.Mean(m)
		fruit[m] = *ing.FruitingTempMinC <= mean && mean <= *ing.FruitingTempMaxC && n.PrecipMM[m] >= *ing.MinPrecipMM
	}
	var months [12]Status
	for m, f := range fruit {
		if f {
			months[m] = InSeason
		}
	}
	applyStorage(&months, fruit, 0, ing.StorageMonths)
	return months, "fruiting weather " + FormatMonths(fruit)
}

func seafood(reach []ocean.EcoregionID, ing catalog.Ingredient) ([12]Status, string) {
	near := make(map[ocean.EcoregionID]bool, len(reach))
	for _, id := range reach {
		near[id] = true
	}
	var months [12]Status
	for _, r := range ing.Regions {
		if !near[r.EcoregionID] {
			continue
		}
		for _, m := range r.Months {
			if months[m-1] < InSeason {
				months[m-1] = InSeason
			}
		}
		for _, m := range r.PeakMonths {
			months[m-1] = Peak
		}
	}
	return months, seafoodBasis
}

func fmtC(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
