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
	"thymeandplace/internal/climate"
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
	// The brief's per-category minimums.
	for c, need := range map[catalog.Category]int{
		catalog.CategoryVegetable: 30, catalog.CategoryFruit: 15, catalog.CategoryHerb: 8, catalog.CategoryMushroom: 5,
	} {
		if byCat[c] < need {
			t.Errorf("%s: %d items, need ≥%d", c, byCat[c], need)
		}
	}
	for _, slug := range []string{"basil", "parsley", "coriander", "dill", "mint", "chives", "thyme", "rosemary"} {
		if ing, ok := snap.Get(slug); !ok || ing.Category != catalog.CategoryHerb {
			t.Errorf("herb %s is missing", slug)
		}
	}
	cultivated, wild := 0, 0
	for _, ing := range snap.All() {
		if ing.Category == catalog.CategoryMushroom {
			switch ing.Kind {
			case catalog.KindYearRound:
				cultivated++
			case catalog.KindWildMushroom:
				wild++
			}
		}
	}
	if cultivated < 2 || wild < 3 {
		t.Errorf("mushrooms: %d cultivated (need ≥2), %d wild (need ≥3)", cultivated, wild)
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

func yearIn(n climate.MonthlyNormals, ing catalog.Ingredient) ([12]season.Status, bool) {
	m, _, ok := season.Year(season.Input{Normals: &n}, ing)
	return m, ok
}

// Ruling G: frost-sensitive perennials are not grown in a hard-winter
// continental climate. The list is by slug so the gate checks the catalog's
// frost settings, not just the engine rule.
func TestNoFrostSensitivePerennialInContinentalClimate(t *testing.T) {
	snap := launch(t)
	for _, slug := range []string{"lemon", "orange", "mandarin", "avocado", "pomegranate", "globe-artichoke", "kiwifruit", "fig"} {
		ing, ok := snap.Get(slug)
		if !ok {
			t.Errorf("%s is missing", slug)
			continue
		}
		if m, ok := yearIn(climatetest.Continental(), ing); ok {
			t.Errorf("%s is in season in the NYC-like continental climate: %v", slug, m)
		}
	}
	if ing, ok := snap.Get("lime"); ok {
		if m, ok := yearIn(climatetest.Continental(), ing); ok {
			t.Errorf("lime is in season in the NYC-like continental climate: %v", m)
		}
	}
}

// Temperate fruit is not grown in the humid tropics.
func TestNoTemperateFruitInTropics(t *testing.T) {
	snap := launch(t)
	for _, slug := range []string{"apple", "cherry", "strawberry"} {
		ing, ok := snap.Get(slug)
		if !ok {
			t.Errorf("%s is missing", slug)
			continue
		}
		if m, ok := yearIn(climatetest.Tropical(), ing); ok {
			t.Errorf("%s is in season in the tropical climate: %v", slug, m)
		}
	}
}

// Heat-loving crops are not grown outdoors in a short, cool-summer climate
// (Oslo-like, warmest month mean 16.5 °C). The calibration score cannot see
// this, because the fixtures for such cities do not list these crops.
func TestNoHeatCropsInCoolSummerClimate(t *testing.T) {
	snap := launch(t)
	for _, slug := range []string{"okra", "watermelon", "sweet-potato", "eggplant", "bell-pepper", "chili-pepper", "fig"} {
		ing, ok := snap.Get(slug)
		if !ok {
			t.Errorf("%s is missing", slug)
			continue
		}
		if m, ok := yearIn(climatetest.CoolSummer(), ing); ok {
			t.Errorf("%s is in season in the Oslo-like cool-summer climate: %v", slug, m)
		}
	}
}

// A Berlin-like climate (warmest month 19.5 °C, coldest-month mean minimum
// -1.5 °C) has too short a warm run for okra and watermelon, and its winters
// kill every frost-tender perennial (fig, citrus, avocado, kiwifruit, ...).
// Like the cool-summer gate, this covers cities whose guides do not list
// these crops, which the calibration score cannot see.
func TestNoHeatCropsOrTenderFruitInCentralEurope(t *testing.T) {
	snap := launch(t)
	slugs := []string{"okra", "watermelon", "fig"}
	for _, ing := range snap.All() {
		if ing.Kind == catalog.KindPerennialFruit && ing.Frost == catalog.FrostTender {
			slugs = append(slugs, ing.Slug)
		}
	}
	for _, slug := range slugs {
		ing, ok := snap.Get(slug)
		if !ok {
			t.Errorf("%s is missing", slug)
			continue
		}
		if m, ok := yearIn(climatetest.CentralEurope(), ing); ok {
			t.Errorf("%s is in season in the Berlin-like climate: %v", slug, m)
		}
	}
}

// Cool-season crops picked in summer (or winter) must not be year-round in
// mild maritime climates.
func TestCoolSeasonCropsNotYearRoundInMaritimeClimates(t *testing.T) {
	snap := launch(t)
	climates := map[string]climate.MonthlyNormals{
		"maritime-north": climatetest.MaritimeNorth(), "maritime-south": climatetest.MaritimeSouth(),
	}
	for _, slug := range []string{"pea", "broad-bean", "lettuce", "radish", "brussels-sprouts", "parsnip"} {
		ing, ok := snap.Get(slug)
		if !ok {
			t.Errorf("%s is missing", slug)
			continue
		}
		for name, n := range climates {
			m, ok := yearIn(n, ing)
			if !ok {
				t.Errorf("%s never in season in %s", slug, name)
			} else if m[0] == season.YearRound {
				t.Errorf("%s is year-round in %s", slug, name)
			}
		}
	}
}

// Temperate fruit must reach mild-winter regions.
func TestAppleInMildSouthernClimate(t *testing.T) {
	apple, ok := launch(t).Get("apple")
	if !ok {
		t.Fatal("apple is missing")
	}
	if m, ok := yearIn(climatetest.MaritimeSouth(), apple); !ok {
		t.Errorf("apple is not in season in the Hobart-like climate: %v", m)
	}
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]`)

// nameKey normalises a name for near-duplicate detection: lower case,
// letters and digits only, and a plural ending reduced to the singular.
func nameKey(name string) string {
	key := nonAlnum.ReplaceAllString(strings.ToLower(name), "")
	switch {
	case strings.HasSuffix(key, "ies"):
		return strings.TrimSuffix(key, "ies") + "y"
	case strings.HasSuffix(key, "oes"), strings.HasSuffix(key, "ches"), strings.HasSuffix(key, "shes"),
		strings.HasSuffix(key, "sses"), strings.HasSuffix(key, "xes"):
		return strings.TrimSuffix(key, "es")
	case strings.HasSuffix(key, "ss"):
		return key
	default:
		return strings.TrimSuffix(key, "s")
	}
}

func TestNameKeyPlurals(t *testing.T) {
	for _, pair := range [][2]string{
		{"Tomatoes", "Tomato"}, {"Cherries", "Cherry"}, {"Peaches", "Peach"},
		{"Grapes", "Grape"}, {"Spring onions", "Spring onion"}, {"Brussels sprouts", "Brussels sprout"},
		{"Radishes", "Radish"}, {"Watercress", "Watercress"},
	} {
		if a, b := nameKey(pair[0]), nameKey(pair[1]); a != b {
			t.Errorf("nameKey(%q) = %q, nameKey(%q) = %q; want equal", pair[0], a, pair[1], b)
		}
	}
	if nameKey("Pea") == nameKey("Peach") {
		t.Error("pea and peach must not collide")
	}
}

// Review Focus 4: names that normalise to the same text are likely duplicates.
func TestNoNearDuplicateNames(t *testing.T) {
	seen := map[string]string{}
	for _, ing := range launch(t).All() {
		key := nameKey(ing.Name)
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
