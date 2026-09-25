// Package catalogtest provides valid ingredient builders, the shared table
// of invalid ingredients (rejected by both app validation and the DB), and
// a small test catalog.
package catalogtest

import (
	"strings"

	"thymeandplace/internal/catalog"
)

// P returns a pointer to v.
func P[T any](v T) *T { return &v }

func Annual() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "tomato", Name: "Tomato", Category: catalog.CategoryVegetable, Kind: catalog.KindAnnual,
		Frost: catalog.FrostTender, GrowTempMinC: P(16.0), GrowTempMaxC: P(30.0), MonthsToHarvest: P(3),
		TipChoose: "Heavy for its size, glossy, fragrant at the stem.",
		TipStore:  "Stem-side down at room temperature.",
	}
}

func Hardy() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "kale", Name: "Kale", Category: catalog.CategoryVegetable, Kind: catalog.KindAnnual,
		Frost: catalog.FrostHardy, GrowTempMinC: P(4.0), GrowTempMaxC: P(20.0), MonthsToHarvest: P(2),
		TipChoose: "Crisp, deep-coloured leaves.", TipStore: "Wrapped loosely in the fridge.",
	}
}

func Perennial() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "apple", Name: "Apple", Category: catalog.CategoryFruit, Kind: catalog.KindPerennialFruit,
		Frost: catalog.FrostHardy, ChillMonthsMin: P(2), WarmupTempC: P(10.0),
		MonthsBloomToHarvest: P(5), HarvestSpanMonths: P(2), StorageMonths: 4,
		TipChoose: "Firm, no soft spots.", TipStore: "Cold and humid; away from greens.",
	}
}

func YearRound() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "banana", Name: "Banana", Category: catalog.CategoryFruit, Kind: catalog.KindYearRound,
		TipChoose: "Yellow with a few brown flecks.", TipStore: "Room temperature.",
	}
}

func WildMushroom() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "chanterelle", Name: "Chanterelle", Category: catalog.CategoryMushroom, Kind: catalog.KindWildMushroom,
		FruitingTempMinC: P(8.0), FruitingTempMaxC: P(16.0), MinPrecipMM: P(50.0),
		TipChoose: "Dry, firm, fruity smell.", TipStore: "Paper bag in the fridge.",
	}
}

func Seafood() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "atlantic-cod", Name: "Atlantic cod", Category: catalog.CategorySeafood, Kind: catalog.KindSeafood,
		Regions: []catalog.SeafoodRegion{{EcoregionID: 25, Months: []int{1, 2, 3, 4}, PeakMonths: []int{2, 3}}},
		TipChoose: "Firm, translucent flesh; clean sea smell.", TipStore: "Coldest part of the fridge.",
	}
}

type InvalidExample struct {
	Name       string
	Ingredient catalog.Ingredient
	Field      string // field Validate must report
}

func with(base catalog.Ingredient, f func(*catalog.Ingredient)) catalog.Ingredient {
	f(&base)
	return base
}

// InvalidExamples lists ingredients that BOTH catalog validation and the
// database constraints must reject. Plan 2 runs this table against Postgres.
func InvalidExamples() []InvalidExample {
	return []InvalidExample{
		{"uppercase slug", with(Annual(), func(i *catalog.Ingredient) { i.Slug = "Tomato" }), "slug"},
		{"double hyphen slug", with(Annual(), func(i *catalog.Ingredient) { i.Slug = "a--b" }), "slug"},
		{"trailing hyphen slug", with(Annual(), func(i *catalog.Ingredient) { i.Slug = "tomato-" }), "slug"},
		{"empty name", with(Annual(), func(i *catalog.Ingredient) { i.Name = "" }), "name"},
		{"81 char name", with(Annual(), func(i *catalog.Ingredient) { i.Name = strings.Repeat("x", 81) }), "name"},
		{"storage 10", with(Annual(), func(i *catalog.Ingredient) { i.StorageMonths = 10 }), "storage_months"},
		{"storage -1", with(Annual(), func(i *catalog.Ingredient) { i.StorageMonths = -1 }), "storage_months"},
		{"seafood kind, vegetable category", with(Seafood(), func(i *catalog.Ingredient) { i.Category = catalog.CategoryVegetable }), "kind"},
		{"seafood category, annual kind", with(Annual(), func(i *catalog.Ingredient) { i.Category = catalog.CategorySeafood }), "kind"},
		{"wild mushroom in vegetable", with(WildMushroom(), func(i *catalog.Ingredient) { i.Category = catalog.CategoryVegetable }), "kind"},
		{"annual missing frost", with(Annual(), func(i *catalog.Ingredient) { i.Frost = "" }), "frost"},
		{"annual missing grow min", with(Annual(), func(i *catalog.Ingredient) { i.GrowTempMinC = nil }), "grow_temp_min_c"},
		{"annual missing grow max", with(Annual(), func(i *catalog.Ingredient) { i.GrowTempMaxC = nil }), "grow_temp_max_c"},
		{"annual grow max equals min", with(Annual(), func(i *catalog.Ingredient) { i.GrowTempMaxC = P(16.0) }), "grow_temp_max_c"},
		{"annual missing months to harvest", with(Annual(), func(i *catalog.Ingredient) { i.MonthsToHarvest = nil }), "months_to_harvest"},
		{"annual months to harvest 0", with(Annual(), func(i *catalog.Ingredient) { i.MonthsToHarvest = P(0) }), "months_to_harvest"},
		{"annual months to harvest 12", with(Annual(), func(i *catalog.Ingredient) { i.MonthsToHarvest = P(12) }), "months_to_harvest"},
		{"perennial missing frost", with(Perennial(), func(i *catalog.Ingredient) { i.Frost = "" }), "frost"},
		{"perennial missing chill", with(Perennial(), func(i *catalog.Ingredient) { i.ChillMonthsMin = nil }), "chill_months_min"},
		{"perennial chill 13", with(Perennial(), func(i *catalog.Ingredient) { i.ChillMonthsMin = P(13) }), "chill_months_min"},
		{"perennial missing warmup", with(Perennial(), func(i *catalog.Ingredient) { i.WarmupTempC = nil }), "warmup_temp_c"},
		{"perennial bloom 0", with(Perennial(), func(i *catalog.Ingredient) { i.MonthsBloomToHarvest = P(0) }), "months_bloom_to_harvest"},
		{"perennial missing bloom", with(Perennial(), func(i *catalog.Ingredient) { i.MonthsBloomToHarvest = nil }), "months_bloom_to_harvest"},
		{"perennial span 13", with(Perennial(), func(i *catalog.Ingredient) { i.HarvestSpanMonths = P(13) }), "harvest_span_months"},
		{"perennial missing span", with(Perennial(), func(i *catalog.Ingredient) { i.HarvestSpanMonths = nil }), "harvest_span_months"},
		{"mushroom fruiting max below min", with(WildMushroom(), func(i *catalog.Ingredient) { i.FruitingTempMaxC = P(5.0) }), "fruiting_temp_max_c"},
		{"mushroom missing fruiting min", with(WildMushroom(), func(i *catalog.Ingredient) { i.FruitingTempMinC = nil }), "fruiting_temp_min_c"},
		{"mushroom negative precip", with(WildMushroom(), func(i *catalog.Ingredient) { i.MinPrecipMM = P(-1.0) }), "min_precip_mm"},
		{"mushroom missing precip", with(WildMushroom(), func(i *catalog.Ingredient) { i.MinPrecipMM = nil }), "min_precip_mm"},
		{"self pairing", with(Annual(), func(i *catalog.Ingredient) { i.Pairings = []string{"tomato"} }), "pairings"},
		{"region with no months", with(Seafood(), func(i *catalog.Ingredient) { i.Regions[0].Months = nil; i.Regions[0].PeakMonths = nil }), "regions[0].months"},
		{"region month 13", with(Seafood(), func(i *catalog.Ingredient) { i.Regions[0].Months = []int{12, 13}; i.Regions[0].PeakMonths = nil }), "regions[0].months"},
		{"region month 0", with(Seafood(), func(i *catalog.Ingredient) { i.Regions[0].Months = []int{0, 1}; i.Regions[0].PeakMonths = nil }), "regions[0].months"},
		{"region 13 months", with(Seafood(), func(i *catalog.Ingredient) {
			i.Regions[0].Months = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 1}
			i.Regions[0].PeakMonths = nil
		}), "regions[0].months"},
		{"peak outside months", with(Seafood(), func(i *catalog.Ingredient) { i.Regions[0].PeakMonths = []int{9} }), "regions[0].peak_months"},
	}
}

// Mackerel is a second seafood species present in two ecoregions.
func Mackerel() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "mackerel", Name: "Mackerel", Category: catalog.CategorySeafood, Kind: catalog.KindSeafood,
		Regions: []catalog.SeafoodRegion{
			{EcoregionID: 25, Months: []int{6, 7, 8, 9}, PeakMonths: []int{7, 8}},
			{EcoregionID: 30, Months: []int{10, 11}},
		},
		TipChoose: "Bright eyes, stiff body.", TipStore: "Eat the day you buy it.",
	}
}
