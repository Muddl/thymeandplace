package season

import (
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate/climatetest"
)

func rotateStatuses(s [12]Status, k int) [12]Status {
	var out [12]Status
	for m := 0; m < 12; m++ {
		out[((m+k)%12+12)%12] = s[m]
	}
	return out
}

func landItems() []catalog.Ingredient {
	var out []catalog.Ingredient
	for _, i := range catalogtest.SmallCatalog() {
		if i.Kind != catalog.KindSeafood {
			out = append(out, i)
		}
	}
	return out
}

// Rotating the climate by k months rotates every result by k months.
// k = 6 is the hemisphere-symmetry property from the spec.
func TestRotationEquivariance(t *testing.T) {
	for name, n := range climatetest.All() {
		base := n
		for k := 0; k < 12; k++ {
			rot := climatetest.Rotate(base, k)
			for _, ing := range landItems() {
				want, _, _ := Year(Input{Normals: &base}, ing)
				got, _, _ := Year(Input{Normals: &rot}, ing)
				if got != rotateStatuses(want, k) {
					t.Fatalf("%s k=%d %s: got %v, want %v", name, k, ing.Slug, got, rotateStatuses(want, k))
				}
			}
		}
	}
}

func TestHemisphereSymmetry(t *testing.T) {
	north, south := climatetest.TemperateNorth(), climatetest.TemperateSouth()
	for _, ing := range landItems() {
		n, _, _ := Year(Input{Normals: &north}, ing)
		s, _, _ := Year(Input{Normals: &south}, ing)
		if s != rotateStatuses(n, 6) {
			t.Fatalf("%s: south %v is not north rotated by 6 (%v)", ing.Slug, s, rotateStatuses(n, 6))
		}
	}
}

// A growing run that crosses Dec→Jan harvests the same as the unwrapped run.
func TestWrapInvariance(t *testing.T) {
	temperate := climatetest.Temperate()
	unwrapped := climatetest.Rotate(temperate, 3) // kale's Oct–May run becomes Jan–Aug
	wrapped, _, _ := Year(Input{Normals: &temperate}, catalogtest.Hardy())
	straight, _, _ := Year(Input{Normals: &unwrapped}, catalogtest.Hardy())
	if straight != rotateStatuses(wrapped, 3) {
		t.Fatalf("wrapped %v vs unwrapped %v", wrapped, straight)
	}
}

func TestDeterminism(t *testing.T) {
	items := catalogtest.SmallCatalog()
	n := climatetest.Mediterranean()
	in := Input{Normals: &n, Month: time.April}

	first := Estimate(in, mustSnapFrom(t, items))
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 20; i++ {
		r.Shuffle(len(items), func(a, b int) { items[a], items[b] = items[b], items[a] })
		if got := Estimate(in, mustSnapFrom(t, items)); !reflect.DeepEqual(got, first) {
			t.Fatalf("iteration %d differs", i)
		}
	}
}

func mustSnapFrom(t *testing.T, items []catalog.Ingredient) *catalog.Snapshot {
	t.Helper()
	s, err := catalog.NewSnapshot(1, items)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Ruling G: for every synthetic climate shifted so its coldest tmin sits just
// below, exactly at, and just above 0 °C, a tender perennial is local only
// when that minimum is not below 0. The ingredient is tuned per climate so that
// nothing else can rule it out: no chill needed, a warm-up threshold halfway
// through the climate's range, and a 12-month harvest window (so some
// frost-free month is always harvested). The tropical climate is skipped: its
// 2 °C range leaves no frost-free month once the coldest tmin is near 0.
func TestTenderPerennialWinterKillThreshold(t *testing.T) {
	var tender []catalog.Ingredient
	for _, i := range landItems() {
		if i.Kind == catalog.KindPerennialFruit && i.Frost == catalog.FrostTender {
			tender = append(tender, i)
		}
	}
	if len(tender) == 0 {
		t.Fatal("SmallCatalog has no tender perennial")
	}
	for name, base := range climatetest.All() {
		if name == "tropical" {
			continue
		}
		coldest, lo, hi := base.TminC[0], base.Mean(0), base.Mean(0)
		for m := range base.TminC {
			coldest = min(coldest, base.TminC[m])
			lo, hi = min(lo, base.Mean(m)), max(hi, base.Mean(m))
		}
		for _, target := range []float64{-0.01, 0, 0.01} {
			n := base
			shift := target - coldest
			for m := range n.TminC {
				n.TminC[m] += shift
				n.TmaxC[m] += shift
			}
			for _, ing := range tender {
				ing.ChillMonthsMin = catalogtest.P(0)
				ing.WarmupTempC = catalogtest.P((lo+hi)/2 + shift)
				ing.MonthsBloomToHarvest, ing.HarvestSpanMonths = catalogtest.P(1), catalogtest.P(12)
				_, _, ok := Year(Input{Normals: &n}, ing)
				if target < 0 && ok {
					t.Errorf("%s coldest tmin %v: %s must winter-kill", name, target, ing.Slug)
				}
				if target >= 0 && !ok {
					t.Errorf("%s coldest tmin %v: %s must survive", name, target, ing.Slug)
				}
			}
		}
	}
}
