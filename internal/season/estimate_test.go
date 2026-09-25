package season

import (
	"testing"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/ocean"
)

func smallSnap(t *testing.T) *catalog.Snapshot {
	t.Helper()
	s, err := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func slugs(rs []Result) map[string]Result {
	out := map[string]Result{}
	for _, r := range rs {
		out[r.Slug] = r
	}
	return out
}

func TestEstimateAugustTemperateCoast(t *testing.T) {
	n := climatetest.Temperate()
	in := Input{Normals: &n, Ecoregions: []ocean.EcoregionID{25}, Month: time.August}
	got := slugs(Estimate(in, smallSnap(t)))

	checks := map[string]Status{
		"tomato": Peak, "apple": Peak, "banana": YearRound, "button-mushroom": YearRound,
		"mackerel": Peak,
	}
	for slug, want := range checks {
		if got[slug].Status != want {
			t.Errorf("%s = %v, want %v", slug, got[slug].Status, want)
		}
	}
	for _, absent := range []string{"kale", "atlantic-cod", "chanterelle"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s should be out of season in August", absent)
		}
	}
	if !got["tomato"].Estimate || got["mackerel"].Estimate {
		t.Error("Estimate flag: land true, seafood false")
	}
}

func TestEstimateInlandOmitsSeafood(t *testing.T) {
	n := climatetest.Temperate()
	got := slugs(Estimate(Input{Normals: &n, Month: time.February}, smallSnap(t)))
	if _, ok := got["atlantic-cod"]; ok {
		t.Fatal("inland must not list seafood")
	}
}

func TestEstimateOceanOnly(t *testing.T) {
	got := Estimate(Input{Ecoregions: []ocean.EcoregionID{25}, Month: time.February}, smallSnap(t))
	if len(got) != 1 || got[0].Slug != "atlantic-cod" || got[0].Status != Peak {
		t.Fatalf("got %+v", got)
	}
}

func TestEstimateSortedBySlug(t *testing.T) {
	n := climatetest.Temperate()
	rs := Estimate(Input{Normals: &n, Ecoregions: []ocean.EcoregionID{25, 30}, Month: time.August}, smallSnap(t))
	for i := 1; i < len(rs); i++ {
		if rs[i-1].Slug >= rs[i].Slug {
			t.Fatalf("not sorted: %s before %s", rs[i-1].Slug, rs[i].Slug)
		}
	}
}

// Review Focus 3: invalid months return nothing and never panic.
func TestEstimateInvalidMonth(t *testing.T) {
	n := climatetest.Temperate()
	for _, m := range []time.Month{0, 13, -1} {
		if got := Estimate(Input{Normals: &n, Month: m}, smallSnap(t)); got != nil {
			t.Fatalf("month %d: got %v, want nil", m, got)
		}
	}
}

func TestNoopAdjuster(t *testing.T) {
	rs := []Result{{Slug: "a"}}
	if got := (NoopAdjuster{}).Adjust(t.Context(), 1, rs); len(got) != 1 || got[0].Slug != "a" {
		t.Fatal("NoopAdjuster changed results")
	}
}
