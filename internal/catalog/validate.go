package catalog

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

var slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Problem is one validation failure. Field uses the database column name,
// or "regions[i].months"-style paths for nested values.
type Problem struct {
	Slug    string
	Field   string
	Message string
}

func (p Problem) String() string {
	if p.Field == "" {
		return fmt.Sprintf("%s: %s", p.Slug, p.Message)
	}
	return fmt.Sprintf("%s: %s %s", p.Slug, p.Field, p.Message)
}

// ValidationError carries every problem found in a catalog or changeset.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.String()
	}
	return "catalog validation failed: " + strings.Join(parts, "; ")
}

// Validate checks one ingredient against rules that mirror the database
// CHECK constraints (see migrations), plus a few app-only rules noted inline.
// It does not check that pairing targets exist; Snapshot does that.
func (ing Ingredient) Validate() []Problem {
	var ps []Problem
	add := func(field, msg string, args ...any) {
		ps = append(ps, Problem{Slug: ing.Slug, Field: field, Message: fmt.Sprintf(msg, args...)})
	}

	if !slugRE.MatchString(ing.Slug) {
		add("slug", "must be lowercase letters and digits, separated by single hyphens")
	}
	if ing.Slug == "new" {
		add("slug", "“new” is reserved; choose another slug")
	}
	if n := utf8.RuneCountInString(ing.Name); n < 1 || n > 80 {
		add("name", "must be 1 to 80 characters")
	}
	if !slices.Contains(Categories, ing.Category) { // app-only: DB enum rejects too, with a less friendly error
		add("category", "must be one of fruit, vegetable, herb, mushroom, seafood")
	}
	if !slices.Contains(Kinds, ing.Kind) {
		add("kind", "must be one of annual, perennial_fruit, cultivated_year_round, wild_mushroom, seafood")
	}
	if ing.Frost != "" && ing.Frost != FrostTender && ing.Frost != FrostHardy {
		add("frost", "must be tender or hardy")
	}
	if ing.StorageMonths < 0 || ing.StorageMonths > 9 {
		add("storage_months", "must be between 0 and 9")
	}
	if (ing.Kind == KindSeafood) != (ing.Category == CategorySeafood) {
		add("kind", "seafood kind and seafood category must go together")
	}
	if ing.Kind == KindWildMushroom && ing.Category != CategoryMushroom {
		add("kind", "wild_mushroom must be in the mushroom category")
	}

	smallintFields := []struct {
		field string
		v     *int
	}{
		{"months_to_harvest", ing.MonthsToHarvest},
		{"chill_months_min", ing.ChillMonthsMin},
		{"months_bloom_to_harvest", ing.MonthsBloomToHarvest},
		{"harvest_span_months", ing.HarvestSpanMonths},
	}
	for _, f := range smallintFields {
		if f.v != nil && (*f.v < math.MinInt16 || *f.v > math.MaxInt16) {
			add(f.field, "is out of range")
		}
	}

	// App-only: the DB stores reals as float8 (which accepts Infinity), but
	// export re-encodes them as JSON numbers, which cannot represent Inf/NaN.
	// A value must survive both as-is and after rounding to float32 (the
	// column's effective precision, see Normalize/Real32).
	realFields := []struct {
		field string
		v     *float64
	}{
		{"grow_temp_min_c", ing.GrowTempMinC},
		{"grow_temp_max_c", ing.GrowTempMaxC},
		{"warmup_temp_c", ing.WarmupTempC},
		{"fruiting_temp_min_c", ing.FruitingTempMinC},
		{"fruiting_temp_max_c", ing.FruitingTempMaxC},
		{"min_precip_mm", ing.MinPrecipMM},
	}
	for _, f := range realFields {
		if f.v == nil {
			continue
		}
		rounded := float64(float32(*f.v))
		if math.IsInf(*f.v, 0) || math.IsNaN(*f.v) || math.IsInf(rounded, 0) || math.IsNaN(rounded) {
			add(f.field, "must be a finite number within ±3.4e38")
		}
	}

	switch ing.Kind {
	case KindAnnual:
		if ing.Frost == "" {
			add("frost", "is required for annuals")
		}
		if ing.GrowTempMinC == nil {
			add("grow_temp_min_c", "is required for annuals")
		}
		if ing.GrowTempMaxC == nil {
			add("grow_temp_max_c", "is required for annuals")
		} else if ing.GrowTempMinC != nil && !(*ing.GrowTempMaxC > *ing.GrowTempMinC) {
			add("grow_temp_max_c", "must be greater than grow_temp_min_c")
		}
		if ing.MonthsToHarvest == nil || *ing.MonthsToHarvest < 1 || *ing.MonthsToHarvest > 11 {
			add("months_to_harvest", "must be between 1 and 11 for annuals")
		}
	case KindPerennialFruit:
		if ing.Frost == "" {
			add("frost", "is required for perennial fruit")
		}
		if ing.ChillMonthsMin == nil || *ing.ChillMonthsMin < 0 || *ing.ChillMonthsMin > 12 {
			add("chill_months_min", "must be between 0 and 12 for perennial fruit")
		}
		if ing.WarmupTempC == nil {
			add("warmup_temp_c", "is required for perennial fruit")
		}
		if ing.MonthsBloomToHarvest == nil || *ing.MonthsBloomToHarvest < 1 || *ing.MonthsBloomToHarvest > 11 {
			add("months_bloom_to_harvest", "must be between 1 and 11 for perennial fruit")
		}
		if ing.HarvestSpanMonths == nil || *ing.HarvestSpanMonths < 1 || *ing.HarvestSpanMonths > 12 {
			add("harvest_span_months", "must be between 1 and 12 for perennial fruit")
		}
	case KindWildMushroom:
		if ing.FruitingTempMinC == nil {
			add("fruiting_temp_min_c", "is required for wild mushrooms")
		}
		if ing.FruitingTempMaxC == nil {
			add("fruiting_temp_max_c", "is required for wild mushrooms")
		} else if ing.FruitingTempMinC != nil && !(*ing.FruitingTempMaxC > *ing.FruitingTempMinC) {
			add("fruiting_temp_max_c", "must be greater than fruiting_temp_min_c")
		}
		if ing.MinPrecipMM == nil || *ing.MinPrecipMM < 0 {
			add("min_precip_mm", "must be 0 or more for wild mushrooms")
		}
	}

	seen := map[string]bool{}
	for _, p := range ing.Pairings {
		switch {
		case p == ing.Slug:
			add("pairings", "cannot include the ingredient itself")
		case seen[p]:
			add("pairings", "lists %q twice", p)
		}
		seen[p] = true
	}

	// App-only: regions belong to seafood, and seafood needs at least one.
	if ing.Kind == KindSeafood && len(ing.Regions) == 0 {
		add("regions", "seafood needs at least one region")
	}
	if ing.Kind != KindSeafood && len(ing.Regions) > 0 {
		add("regions", "only seafood can have regions")
	}
	// Normalize sorts regions by ecoregion id, so the regions[i] index in a
	// problem's Field can point at a different line than the owner saw in
	// their input. Name the ecoregion in the message so the problem is
	// unambiguous even when the index has shifted.
	seenEco := map[int32]bool{}
	for i, r := range ing.Regions {
		if seenEco[int32(r.EcoregionID)] {
			add(fmt.Sprintf("regions[%d].ecoregion_id", i), "ecoregion %d: is listed twice", r.EcoregionID)
		}
		seenEco[int32(r.EcoregionID)] = true
		if len(r.Months) < 1 || len(r.Months) > 12 {
			add(fmt.Sprintf("regions[%d].months", i), "ecoregion %d: must list 1 to 12 months", r.EcoregionID)
		}
		for _, m := range r.Months {
			if m < 1 || m > 12 {
				add(fmt.Sprintf("regions[%d].months", i), "ecoregion %d: must be between 1 and 12", r.EcoregionID)
				break
			}
		}
		for _, m := range r.PeakMonths {
			if !slices.Contains(r.Months, m) {
				add(fmt.Sprintf("regions[%d].peak_months", i), "ecoregion %d: must be a subset of months", r.EcoregionID)
				break
			}
		}
	}
	return ps
}
