// Package catalog holds the ingredient catalog domain types, validation
// that mirrors the database constraints, and the immutable Snapshot.
package catalog

import "thymeandplace/internal/ocean"

type Category string

const (
	CategoryFruit     Category = "fruit"
	CategoryVegetable Category = "vegetable"
	CategoryHerb      Category = "herb"
	CategoryMushroom  Category = "mushroom"
	CategorySeafood   Category = "seafood"
)

var Categories = []Category{CategoryFruit, CategoryVegetable, CategoryHerb, CategoryMushroom, CategorySeafood}

type Kind string

const (
	KindAnnual         Kind = "annual"
	KindPerennialFruit Kind = "perennial_fruit"
	KindYearRound      Kind = "cultivated_year_round"
	KindWildMushroom   Kind = "wild_mushroom"
	KindSeafood        Kind = "seafood"
)

var Kinds = []Kind{KindAnnual, KindPerennialFruit, KindYearRound, KindWildMushroom, KindSeafood}

type Frost string

const (
	FrostTender Frost = "tender"
	FrostHardy  Frost = "hardy"
)

// SeafoodRegion says when a species is in season in one marine ecoregion.
// Months and PeakMonths hold 1–12.
type SeafoodRegion struct {
	EcoregionID ocean.EcoregionID
	Months      []int
	PeakMonths  []int
}

// Ingredient is one catalog row plus its pairings and seafood regions.
// Pointer fields are NULL-able columns; nil means "not set".
type Ingredient struct {
	Slug     string
	Name     string
	Category Category
	Kind     Kind

	// annual / perennial_fruit
	Frost           Frost // "" when not set
	GrowTempMinC    *float64
	GrowTempMaxC    *float64
	MonthsToHarvest *int
	StorageMonths   int

	// perennial_fruit
	ChillMonthsMin       *int
	WarmupTempC          *float64
	MonthsBloomToHarvest *int
	HarvestSpanMonths    *int

	// wild_mushroom
	FruitingTempMinC *float64
	FruitingTempMaxC *float64
	MinPrecipMM      *float64

	// content
	TipChoose string
	TipStore  string
	Note      string

	Pairings []string
	Regions  []SeafoodRegion
}
