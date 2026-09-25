package catalog_test

import (
	"reflect"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
)

func TestNormalizeRoundsThroughFloat32(t *testing.T) {
	i := catalogtest.Annual()
	i.GrowTempMinC = catalogtest.P(16.123456789)
	n := catalog.Normalize(i)
	if *n.GrowTempMinC != 16.123457 {
		t.Fatalf("got %v, want 16.123457", *n.GrowTempMinC)
	}
	i.GrowTempMinC = catalogtest.P(16.1)
	if n := catalog.Normalize(i); *n.GrowTempMinC != 16.1 {
		t.Fatalf("16.1 became %v", *n.GrowTempMinC)
	}
}

func TestNormalizeSortsAndNils(t *testing.T) {
	i := catalogtest.Seafood()
	i.Pairings = []string{"potato", "dill", "butter"}
	i.Regions = []catalog.SeafoodRegion{
		{EcoregionID: 30, Months: []int{11, 10}, PeakMonths: []int{}},
		{EcoregionID: 25, Months: []int{4, 1, 3, 2}, PeakMonths: []int{3, 2}},
	}
	n := catalog.Normalize(i)
	if !reflect.DeepEqual(n.Pairings, []string{"butter", "dill", "potato"}) {
		t.Fatalf("pairings = %v", n.Pairings)
	}
	if n.Regions[0].EcoregionID != 25 || !reflect.DeepEqual(n.Regions[0].Months, []int{1, 2, 3, 4}) ||
		!reflect.DeepEqual(n.Regions[0].PeakMonths, []int{2, 3}) || n.Regions[1].PeakMonths != nil {
		t.Fatalf("regions = %+v", n.Regions)
	}
	empty := catalogtest.Annual()
	empty.Pairings = []string{}
	if catalog.Normalize(empty).Pairings != nil {
		t.Fatal("empty pairings should normalise to nil")
	}
	// The input is not modified.
	if i.Pairings[0] != "potato" {
		t.Fatal("Normalize mutated its input")
	}
}
