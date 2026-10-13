package changeset

import (
	"math"
	"os"
	"testing"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/season"
)

func testCases(t *testing.T) []calibration.Case {
	t.Helper()
	fx, err := calibration.LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	n := climatetest.Temperate()
	return []calibration.Case{{Fixture: fx[0], Input: season.Input{Normals: &n}}}
}

func TestPreviewScoreDropNeedsOverride(t *testing.T) {
	cur := snap(t)
	tomato := catalogtest.Annual()
	tomato.Pairings = []string{"basil"}
	tomato.MonthsToHarvest = catalogtest.P(1) // harvest May–Sep vs guide Jul–Sep: 3/5
	proposed, _ := cur.With([]catalog.Ingredient{tomato}, nil)

	p := BuildPreview(cur, proposed, testCases(t))
	if math.Abs(p.Before.Score-(1+4.0/6)/3) > 1e-9 || math.Abs(p.After.Score-(0.6+4.0/6)/3) > 1e-9 {
		t.Fatalf("scores %v → %v", p.Before.Score, p.After.Score)
	}
	if !p.NeedsOverride || p.Delta >= 0 {
		t.Fatalf("override=%v delta=%v", p.NeedsOverride, p.Delta)
	}
	if len(p.Items) != 1 || p.Items[0].Slug != "tomato" || *p.Items[0].ScoreBefore != 1 || math.Abs(*p.Items[0].ScoreAfter-0.6) > 1e-9 {
		t.Fatalf("items = %+v", p.Items)
	}
	b := p.Items[0].Bars[0]
	if b.Fixture != "Temperate test town" || b.Before[7] != season.Peak || b.After[6] != season.Peak || b.After[4] != season.InSeason {
		t.Fatalf("bars = %+v", b)
	}
}

func TestPreviewImprovementNoOverride(t *testing.T) {
	cur := snap(t)
	apple := catalogtest.Perennial()
	apple.StorageMonths = 2 // Aug–Nov matches the guide exactly
	proposed, _ := cur.With([]catalog.Ingredient{apple}, nil)
	p := BuildPreview(cur, proposed, testCases(t))
	if p.NeedsOverride || p.Delta <= 0 {
		t.Fatalf("override=%v delta=%v", p.NeedsOverride, p.Delta)
	}
}

func TestPreviewAddedAndDeletedBars(t *testing.T) {
	cur := snap(t)
	chard := catalogtest.Hardy()
	chard.Slug, chard.Name = "chard", "Chard"
	proposed, _ := cur.With([]catalog.Ingredient{chard}, []string{"banana"})
	p := BuildPreview(cur, proposed, testCases(t))
	for _, it := range p.Items {
		b := it.Bars[0]
		switch it.Slug {
		case "chard":
			if b.HadBefore || !b.HasAfter {
				t.Fatalf("chard bars = %+v", b)
			}
		case "banana":
			if !b.HadBefore || b.HasAfter {
				t.Fatalf("banana bars = %+v", b)
			}
		}
	}
}

// Review Focus 5: first import into an empty catalog.
func TestPreviewFromEmptyCatalog(t *testing.T) {
	empty, _ := catalog.NewSnapshot(0, nil)
	proposed, err := empty.With(catalogtest.SmallCatalog(), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := BuildPreview(empty, proposed, testCases(t))
	if p.NeedsOverride || p.Before.Compared != 0 || len(p.Diff.Added) != 11 || math.IsNaN(p.Delta) {
		t.Fatalf("preview = %+v", p)
	}
}
