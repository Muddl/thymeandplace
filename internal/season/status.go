// Package season estimates what is in season from climate normals, nearby
// marine ecoregions and the catalog. It is pure: no I/O.
package season

import (
	"context"
	"time"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/ocean"
)

// Status of an ingredient in one month. The zero value means out of season.
// Numeric order encodes precedence: Peak > InSeason > FromStorage.
// YearRound is exclusive and never mixed with the others.
type Status int

const (
	Out Status = iota
	FromStorage
	InSeason
	Peak
	YearRound
)

func (s Status) String() string {
	switch s {
	case FromStorage:
		return "from storage"
	case InSeason:
		return "in season"
	case Peak:
		return "peak"
	case YearRound:
		return "year-round"
	default:
		return "out"
	}
}

// Input describes one location and month.
type Input struct {
	Normals    *climate.MonthlyNormals // nil if not on land
	Ecoregions []ocean.EcoregionID     // empty if inland
	Month      time.Month
}

// Result is one ingredient that is in season in Input.Month.
type Result struct {
	Slug     string
	Status   Status
	Months   [12]Status // full-year view; index 0 is January
	Estimate bool       // true for all land results
	Basis    string
}

// Adjuster lets later sub-projects correct results per area.
type Adjuster interface {
	Adjust(ctx context.Context, cellID int, results []Result) []Result
}

// NoopAdjuster returns results unchanged.
type NoopAdjuster struct{}

func (NoopAdjuster) Adjust(_ context.Context, _ int, results []Result) []Result { return results }

const (
	frostFreeTminC = 2.0
	chillMeanC     = 7.0
	// winterKillTminC: a tender perennial is not grown locally where the
	// coldest month's mean minimum is below this (controller ruling G).
	winterKillTminC = 0.0
	seafoodBasis    = "general guidance; check local fishing rules"
)
