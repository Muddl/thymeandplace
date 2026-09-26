package season

import (
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/ocean"
)

// statuses builds an expected [12]Status from month→status pairs (1-based).
func statuses(pairs map[int]Status) [12]Status {
	var s [12]Status
	for m, st := range pairs {
		s[m-1] = st
	}
	return s
}

func land(n climate.MonthlyNormals) Input { return Input{Normals: &n} }

func year(t *testing.T, in Input, ing catalog.Ingredient) [12]Status {
	t.Helper()
	m, _, _ := Year(in, ing)
	return m
}

func TestAnnualTender(t *testing.T) {
	// Growable Apr–Sep (6 months), 3 to harvest → harvest Jul–Sep, peak Aug.
	got := year(t, land(climatetest.Temperate()), catalogtest.Annual())
	want := statuses(map[int]Status{7: InSeason, 8: Peak, 9: InSeason})
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAnnualMinInclusive(t *testing.T) {
	// April mean is exactly 16 = grow_temp_min_c; it must count as growable.
	ing := catalogtest.Annual()
	ing.MonthsToHarvest = catalogtest.P(5) // run Apr–Sep (6) > 5 → harvest Sep only
	got := year(t, land(climatetest.Temperate()), ing)
	if got != statuses(map[int]Status{9: Peak}) {
		t.Fatalf("April boundary not inclusive: %v", got)
	}
}

func TestAnnualRunNotLongerThanHarvestYieldsNothing(t *testing.T) {
	ing := catalogtest.Annual()
	ing.MonthsToHarvest = catalogtest.P(6) // run length 6 is not > 6
	if _, _, ok := Year(land(climatetest.Temperate()), ing); ok {
		t.Fatal("run of length == months_to_harvest must produce no harvest")
	}
}

func TestAnnualHardyWrapsDecJan(t *testing.T) {
	// Kale 4–20 °C hardy: growable Oct–May (8, wraps). 2 to harvest →
	// harvest Dec–May (6), peak Feb–Mar.
	got := year(t, land(climatetest.Temperate()), catalogtest.Hardy())
	want := statuses(map[int]Status{12: InSeason, 1: InSeason, 2: Peak, 3: Peak, 4: InSeason, 5: InSeason})
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAnnualTenderSplitsOnFrost(t *testing.T) {
	// Same as kale but tender: Dec (tmin 2, not > 2) and Jan are frosty →
	// runs Feb–May (4) and Oct–Nov (2). Harvest Apr–May (both peak); Oct–Nov too short.
	ing := catalogtest.Hardy()
	ing.Frost = catalog.FrostTender
	got := year(t, land(climatetest.Temperate()), ing)
	if got != statuses(map[int]Status{4: Peak, 5: Peak}) {
		t.Fatalf("got %v", got)
	}
}

func TestAnnualAllGrowableIsYearRound(t *testing.T) {
	got := year(t, land(climatetest.Flat(27, 8, 200)), catalogtest.Annual())
	for m, s := range got {
		if s != YearRound {
			t.Fatalf("month %d = %v, want year-round", m+1, s)
		}
	}
}

func TestStorage(t *testing.T) {
	ing := catalogtest.Annual()
	ing.StorageMonths = 2
	got := year(t, land(climatetest.Temperate()), ing)
	want := statuses(map[int]Status{7: InSeason, 8: Peak, 9: InSeason, 10: FromStorage, 11: FromStorage})
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPerennialHardy(t *testing.T) {
	// chill months (<7): Jan 5, Dec 6 → 2 ≥ 2. Warm-up ≥10 first after below: Mar.
	// Harvest Mar+5 = Aug for 2 months → Aug–Sep, both peak. Storage 4 → Oct–Jan.
	got := year(t, land(climatetest.Temperate()), catalogtest.Perennial())
	want := statuses(map[int]Status{8: Peak, 9: Peak, 10: FromStorage, 11: FromStorage, 12: FromStorage, 1: FromStorage})
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPerennialChillThreshold(t *testing.T) {
	ing := catalogtest.Perennial()
	ing.ChillMonthsMin = catalogtest.P(3) // only 2 months below 7
	if _, _, ok := Year(land(climatetest.Temperate()), ing); ok {
		t.Fatal("insufficient chill must not be local")
	}
	// A month at exactly 7 °C is not a chill month.
	means := [12]float64{7, 8, 12, 16, 20, 24, 26, 25, 21, 15, 10, 6}
	n := climatetest.FromMeans(means, 8, [12]float64{})
	ing.ChillMonthsMin = catalogtest.P(2)
	if _, _, ok := Year(land(n), ing); ok {
		t.Fatal("mean == 7 must not count as chill")
	}
}

func TestPerennialTenderFrostFilter(t *testing.T) {
	// Orange: warm-up ≥20 first after below: May. +7 → Dec, span 4: Dec–Mar.
	// Tender: Dec (tmin 2) and Jan (1) frosty → Feb–Mar, both peak.
	var orange catalog.Ingredient
	for _, i := range catalogtest.SmallCatalog() {
		if i.Slug == "orange" {
			orange = i
		}
	}
	got := year(t, land(climatetest.Temperate()), orange)
	if got != statuses(map[int]Status{2: Peak, 3: Peak}) {
		t.Fatalf("got %v", got)
	}
}

func smallOrange(t *testing.T) catalog.Ingredient {
	t.Helper()
	for _, i := range catalogtest.SmallCatalog() {
		if i.Slug == "orange" {
			return i
		}
	}
	t.Fatal("orange missing from SmallCatalog")
	return catalog.Ingredient{}
}

// Ruling G: a tender perennial is not grown where the coldest month's mean
// minimum is below 0 °C (winter-kill), even if its harvest months are frost-free.
func TestPerennialTenderWinterKill(t *testing.T) {
	n := climatetest.Temperate()
	n.TminC[0] = -0.5 // Jan mean 4.25: still a chill month; warm-up still Mar/May
	orange := smallOrange(t)
	if got, _, ok := Year(land(n), orange); ok {
		t.Fatalf("tender perennial must winter-kill below 0 °C, got %v", got)
	}
	hardy := orange
	hardy.Frost = catalog.FrostHardy
	if _, _, ok := Year(land(n), hardy); !ok {
		t.Fatal("winter-kill must apply only to tender perennials")
	}
}

// Ruling G edge: a coldest-month tmin of exactly 0 °C is not winter-kill.
func TestPerennialTenderWinterKillBoundary(t *testing.T) {
	n := climatetest.Temperate()
	n.TminC[0] = 0 // Jan is frosty (not > 2) but survivable
	got := year(t, land(n), smallOrange(t))
	if got != statuses(map[int]Status{2: Peak, 3: Peak}) {
		t.Fatalf("tmin == 0 must not winter-kill: got %v", got)
	}
}

// Review Focus 2: no warm-up transition → not local, no panic.
func TestPerennialNoWarmupTransition(t *testing.T) {
	ing := catalogtest.Perennial()
	ing.ChillMonthsMin = catalogtest.P(0)
	if _, _, ok := Year(land(climatetest.Flat(27, 8, 100)), ing); ok {
		t.Fatal("always-warm climate has no warm-up month")
	}
	if _, _, ok := Year(land(climatetest.Flat(2, 4, 100)), ing); ok {
		t.Fatal("always-cold climate has no warm-up month")
	}
}

// Review Focus 2: two warm-ups → the first from January wins, deterministically.
func TestPerennialBimodalFirstWarmupWins(t *testing.T) {
	means := [12]float64{5, 12, 5, 5, 5, 5, 12, 5, 5, 5, 5, 5} // warm-ups in Feb and Jul
	n := climatetest.FromMeans(means, 4, [12]float64{})
	ing := catalogtest.Perennial()
	ing.MonthsBloomToHarvest, ing.HarvestSpanMonths, ing.StorageMonths = catalogtest.P(1), catalogtest.P(1), 0
	for i := 0; i < 5; i++ {
		got := year(t, land(n), ing)
		if got != statuses(map[int]Status{3: Peak}) {
			t.Fatalf("run %d: got %v, want Mar peak", i, got)
		}
	}
}

// Review Focus 5: a 12-month harvest window.
func TestPerennialTwelveMonthWindow(t *testing.T) {
	// Mean crosses 10 once (warm-up in Mar) but tmin stays > 2 all year.
	means := [12]float64{8, 9, 11, 13, 15, 17, 18, 17, 15, 13, 11, 9}
	n := climatetest.FromMeans(means, 4, [12]float64{})
	ing := catalogtest.Perennial()
	ing.Frost, ing.ChillMonthsMin = catalog.FrostTender, catalogtest.P(0)
	ing.MonthsBloomToHarvest, ing.HarvestSpanMonths, ing.StorageMonths = catalogtest.P(1), catalogtest.P(12), 3
	got := year(t, land(n), ing)
	// Start = Mar + 1 = Apr (index 3). Peak offsets 5,6 → Sep, Oct.
	want := [12]Status{}
	for m := range want {
		want[m] = InSeason
	}
	want[8], want[9] = Peak, Peak
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestYearRoundKind(t *testing.T) {
	got := year(t, land(climatetest.Polar()), catalogtest.YearRound())
	for m, s := range got {
		if s != YearRound {
			t.Fatalf("month %d = %v", m+1, s)
		}
	}
}

func TestWildMushroom(t *testing.T) {
	// 8–16 °C and precip ≥ 50: Feb 8, Mar 12, Apr 16, Nov 10. Oct 15 has 40 mm.
	got := year(t, land(climatetest.Temperate()), catalogtest.WildMushroom())
	want := statuses(map[int]Status{2: InSeason, 3: InSeason, 4: InSeason, 11: InSeason})
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWildMushroomMinPrecipInclusive(t *testing.T) {
	// Jan mean = 10 (within 8-16), precip exactly 50 = min_precip_mm; it
	// must count as fruiting. Every other month is outside the fruiting
	// temperature band, so Jan is the only candidate.
	means := [12]float64{10, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30}
	precip := [12]float64{50, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60}
	n := climatetest.FromMeans(means, 4, precip)
	got := year(t, land(n), catalogtest.WildMushroom())
	want := statuses(map[int]Status{1: InSeason})
	if got != want {
		t.Fatalf("precip == min_precip_mm not inclusive: got %v, want %v", got, want)
	}
}

func TestWildMushroomStorageDoesNotDowngradeLaterRun(t *testing.T) {
	// Two separate fruiting runs: Jan-Feb (8-16 range) and May-Jun, split
	// by Mar-Apr and Jul-Dec outside the band. storage_months=3.
	//
	// Hand derivation:
	//   fruit  = [T,T,F,F,T,T,F,F,F,F,F,F] (Jan,Feb,May,Jun)
	//   runs(fruit,0) = {Start:Jan(0),Len:2}, {Start:May(4),Len:2}
	//   harvestStatuses is not used by wildMushroom; InSeason is set
	//   directly for every fruiting month: Jan,Feb,May,Jun = InSeason.
	//   applyStorage, run {0,2} (Jan-Feb): end=Feb(1). k=1..3 covers
	//     Mar(2), Apr(3), May(4). Mar and Apr are Out -> FromStorage.
	//     May is already InSeason (2) which is NOT < FromStorage (1), so
	//     the no-downgrade guard must leave it InSeason, not overwrite it.
	//   applyStorage, run {4,2} (May-Jun): end=Jun(5). k=1..3 covers
	//     Jul(6), Aug(7), Sep(8), all Out -> FromStorage.
	//   Final: Jan=InSeason, Feb=InSeason, Mar=FromStorage, Apr=FromStorage,
	//     May=InSeason, Jun=InSeason, Jul=FromStorage, Aug=FromStorage,
	//     Sep=FromStorage, Oct=Out, Nov=Out, Dec=Out.
	means := [12]float64{10, 10, 30, 30, 10, 10, 30, 30, 30, 30, 30, 30}
	precip := [12]float64{60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60}
	n := climatetest.FromMeans(means, 4, precip)
	ing := catalogtest.WildMushroom()
	ing.StorageMonths = 3
	got := year(t, land(n), ing)
	want := statuses(map[int]Status{
		1: InSeason, 2: InSeason, 3: FromStorage, 4: FromStorage,
		5: InSeason, 6: InSeason, 7: FromStorage, 8: FromStorage, 9: FromStorage,
	})
	if got != want {
		t.Fatalf("storage downgraded the later run: got %v, want %v", got, want)
	}
}

func TestSeafood(t *testing.T) {
	cod := catalogtest.Seafood()
	got, basis, ok := Year(Input{Ecoregions: []ocean.EcoregionID{25}}, cod)
	if !ok || basis != "general guidance; check local fishing rules" {
		t.Fatalf("ok=%v basis=%q", ok, basis)
	}
	if got != statuses(map[int]Status{1: InSeason, 2: Peak, 3: Peak, 4: InSeason}) {
		t.Fatalf("got %v", got)
	}
	if _, _, ok := Year(Input{Ecoregions: []ocean.EcoregionID{99}}, cod); ok {
		t.Fatal("no region in reach must not be in season")
	}
	if _, _, ok := Year(Input{}, cod); ok {
		t.Fatal("inland must not be in season")
	}
}

func TestSeafoodPrecedenceAcrossRegions(t *testing.T) {
	ing := catalogtest.Seafood()
	ing.Regions = []catalog.SeafoodRegion{
		{EcoregionID: 1, Months: []int{5}, PeakMonths: []int{5}},
		{EcoregionID: 2, Months: []int{5}},
	}
	for _, order := range [][]ocean.EcoregionID{{1, 2}, {2, 1}} {
		got := year(t, Input{Ecoregions: order}, ing)
		if got[4] != Peak {
			t.Fatalf("order %v: May = %v, want peak", order, got[4])
		}
	}
	ing.Regions[0], ing.Regions[1] = ing.Regions[1], ing.Regions[0]
	if got := year(t, Input{Ecoregions: []ocean.EcoregionID{1, 2}}, ing); got[4] != Peak {
		t.Fatalf("region order changed result: %v", got[4])
	}
}

func TestLandKindsNeedNormals(t *testing.T) {
	if _, _, ok := Year(Input{}, catalogtest.YearRound()); ok {
		t.Fatal("land ingredient with no normals must not be in season")
	}
}

func TestBasisText(t *testing.T) {
	_, basis, _ := Year(land(climatetest.Temperate()), catalogtest.Annual())
	if basis != "growing conditions Apr–Sep; about 3 months to harvest" {
		t.Fatalf("basis = %q", basis)
	}
}
