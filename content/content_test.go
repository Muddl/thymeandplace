// Package content_test gates the launch content: counts, coverage, validity.
package content_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	calibfixtures "thymeandplace/calibration"
	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogjson"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/season"
)

func launch(t *testing.T) *catalog.Snapshot {
	t.Helper()
	f, err := os.Open("catalog/launch.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	items, err := catalogjson.Decode(f)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	snap, err := catalog.NewSnapshot(0, items)
	if err != nil {
		t.Fatalf("invalid catalog: %v", err)
	}
	return snap
}

func TestLaunchCatalogCoverage(t *testing.T) {
	snap := launch(t)
	land, seafood := 0, 0
	byCat := map[catalog.Category]int{}
	byKind := map[catalog.Kind]int{}
	for _, ing := range snap.All() {
		byCat[ing.Category]++
		byKind[ing.Kind]++
		if ing.Kind == catalog.KindSeafood {
			seafood++
			if len(ing.Regions) == 0 {
				t.Errorf("%s: seafood needs at least one region", ing.Slug)
			}
			for _, r := range ing.Regions {
				if r.EcoregionID < 1 || r.EcoregionID > 232 {
					t.Errorf("%s: ecoregion %d is outside MEOW 1–232", ing.Slug, r.EcoregionID)
				}
			}
		} else {
			land++
		}
		if strings.TrimSpace(ing.TipChoose) == "" || strings.TrimSpace(ing.TipStore) == "" {
			t.Errorf("%s: both tips are required for launch", ing.Slug)
		}
	}
	if land < 60 || seafood < 15 {
		t.Fatalf("land=%d (need ≥60) seafood=%d (need ≥15)", land, seafood)
	}
	for _, c := range catalog.Categories {
		if byCat[c] == 0 {
			t.Errorf("category %s not represented", c)
		}
	}
	for _, k := range catalog.Kinds {
		if byKind[k] == 0 {
			t.Errorf("kind %s not represented", k)
		}
	}
}

// Review Focus 3: every land ingredient grows somewhere plausible.
func TestEveryLandIngredientInSeasonSomewhere(t *testing.T) {
	for _, ing := range launch(t).All() {
		if ing.Kind == catalog.KindSeafood {
			continue
		}
		found := false
		for _, n := range climatetest.All() {
			n := n
			if _, _, ok := season.Year(season.Input{Normals: &n}, ing); ok {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s is never in season in any synthetic climate; check its parameters", ing.Slug)
		}
	}
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]`)

// Review Focus 4: names that normalise to the same text are likely duplicates.
func TestNoNearDuplicateNames(t *testing.T) {
	seen := map[string]string{}
	for _, ing := range launch(t).All() {
		key := nonAlnum.ReplaceAllString(strings.ToLower(ing.Name), "")
		key = strings.TrimSuffix(key, "s")
		if other, ok := seen[key]; ok {
			t.Errorf("%s and %s look like the same ingredient", other, ing.Slug)
		}
		seen[key] = ing.Slug
	}
}

func TestFixtureCoverage(t *testing.T) {
	fx, err := calibration.LoadFixtures(calibfixtures.FS())
	if err != nil {
		t.Fatal(err)
	}
	if len(fx) < 15 {
		t.Fatalf("%d fixtures, need about 15 (≥15)", len(fx))
	}
	south, tropics, high := 0, 0, 0
	for _, f := range fx {
		if f.Lat < 0 {
			south++
		}
		if f.Lat > -23.5 && f.Lat < 23.5 {
			tropics++
		}
		if f.Lat > 50 || f.Lat < -50 {
			high++
		}
	}
	if south < 4 || tropics < 2 || high < 2 {
		t.Fatalf("coverage: south=%d (≥4) tropics=%d (≥2) high-latitude=%d (≥2)", south, tropics, high)
	}

	// Review Focus 1: fixtures must mostly use catalog slugs.
	snap := launch(t)
	for _, f := range fx {
		var matched int
		var missing []string
		for slug := range f.Months {
			if _, ok := snap.Get(slug); ok {
				matched++
			} else {
				missing = append(missing, slug)
			}
		}
		if matched < 10 {
			t.Errorf("%s: only %d slugs match the catalog (need ≥10); unmatched: %v", f.ID, matched, missing)
		}
	}
}
