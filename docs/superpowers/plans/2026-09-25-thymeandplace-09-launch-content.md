# Thyme & Place — Plan 9: Launch Catalog and Calibration Fixtures Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Author the launch content: at least 60 land ingredients and 15 seafood species in the import format, and about 15 calibration fixtures taken from published seasonal guides. Machine-checked gates enforce the counts and coverage, and a calibration baseline is recorded before launch.

**Architecture:** Content is data, not code. The launch catalog lives in `content/catalog/launch.json` (the §9.4 import format) and is loaded into production through `/admin/import`, so it goes through the same validation, preview and calibration gate as any edit. Fixtures live in `calibration/fixtures/*.json` and are embedded in the binary (Plan 1). Go tests in `content/` are the acceptance gate: they decode, validate and count the content, and check fixture coverage against the catalog.

**Tech Stack:** JSON, Go tests, the running admin UI and ingestion pipeline (Plans 3, 7, 8).

**Spec:** `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (§1 success criteria 2, §8.5, §12, §13.2)

**Depends on:** Plans 1–8. Fixture authoring (Task 2) can start any time after Plan 1.

## Global Constraints

- Launch catalog: **at least 60 land ingredients and 15 seafood species, with every category represented** (§8.5): fruit, vegetable, herb, mushroom, seafood.
- Every wild mushroom shows the fixed disclaimer automatically; don't repeat it in `note`.
- Fixtures: about **15 real locations across both hemispheres and several climate types**. Each fixture holds per-ingredient months from **one published seasonal guide**, with the source (title and URL) recorded in the file (§13.2). **Do not invent fixture months.** If a guide doesn't list an ingredient, leave it out.
- Fixtures are read by the admin preview and drift check only, never shown publicly (§9.3). Record months as facts in your own JSON; don't copy a guide's text or layout.
- Seafood `ecoregion_id` values are MEOW `ECO_CODE_X` numbers (1–232) that exist in the live `ocean_ecoregion` data. The import rejects unknown IDs.
- No catalog change may lower the calibration score by more than 0.02 without an explicit override (§1, §9.2). The launch import is the baseline, so record its score.
- Commit after every task. Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **A fixture ingredient slug that doesn't match any catalog slug** (e.g. `courgette` vs `zucchini`) is silently skipped by scoring, which quietly shrinks calibration. The gate fails when fewer than 10 of a fixture's slugs exist in the catalog, and it lists the unmatched slugs. Pinned in Task 2.
2. **A seafood species whose regions are all far from any populated coast** never appears on the site. The gate checks each seafood species has at least one region, and the Task 3 import preview shows every ecoregion ID exists live.
3. **A parameter set that never produces a harvest anywhere** (e.g. `months_to_harvest` longer than any realistic growing run) passes validation but shows nowhere. The gate requires every land ingredient to be in season in at least one of the synthetic climates (tropical, temperate north/south, Mediterranean, alpine, polar). Pinned in Task 1. The Task 3 baseline then checks real fixture cities.
4. **Duplicate or near-duplicate slugs** (`spring-onion` and `scallion`): the gate flags names that normalise to the same string. Pinned in Task 1.
5. **Hemisphere bias in fixtures**: calibration that only covers the north would let southern regressions through. The gate requires at least 4 southern-hemisphere fixtures. Pinned in Task 2.

---

## File Structure

```
content/catalog/launch.json          # the launch catalog (import format)
content/content_test.go              # acceptance gates for catalog and fixtures
content/AUTHORING.md                 # parameter guidance and worked examples
calibration/fixtures/*.json          # one file per location
```

---

### Task 1: Launch catalog with its acceptance gate

**Files:**
- Create: `content/content_test.go`, `content/AUTHORING.md`, `content/catalog/launch.json`

**Interfaces:**
- Consumes: `catalogjson.Decode`, `catalog.NewSnapshot`.
- Produces: `content/catalog/launch.json`, ready for `/admin/import`.

- [ ] **Step 1: Write the gate test**

Create `content/content_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./content/ -count=1`
Expected: FAIL: `open catalog/launch.json: no such file or directory` (and the fixture test fails on count).

- [ ] **Step 3: Write the authoring guide**

Create `content/AUTHORING.md`:

````markdown
# Authoring the catalog

The engine rules are in spec §12.3. These are starting points; the preview's
calibration score is the judge. Temperatures are **monthly mean** temperatures
(the average of the monthly mean minimum and mean maximum), not daytime highs.

## Parameter guidance by kind

| Kind | Field | Typical range | Notes |
|---|---|---|---|
| annual, warm-season (tomato, pepper, squash, basil, beans, corn) | frost | `tender` | |
| | grow_temp_c | [15–18, 30–32] | lower bound is the key lever |
| | months_to_harvest | 2–4 | from planting (or transplant) to first harvest |
| annual, cool-season (kale, cabbage, peas, lettuce, spinach, leeks) | frost | `hardy` | |
| | grow_temp_c | [4–7, 18–22] | upper bound stops summer "bolting" months |
| | months_to_harvest | 1–4 | |
| root/storage crops (potato, onion, carrot, squash) | storage_months | 3–8 | only if typically sold from storage |
| perennial_fruit, temperate (apple, pear, plum, cherry) | chill_months_min | 3–5 | months with mean < 7 °C |
| | warmup_temp_c | 9–12 | spring warm-up |
| | months_bloom_to_harvest | 2 (cherry) – 6 (late apple) | |
| | harvest_span_months | 1–3 | |
| perennial_fruit, subtropical (citrus, avocado) | frost | `tender` | frost months drop out of the harvest |
| | chill_months_min | 0 | |
| cultivated_year_round | – | – | bananas, button mushrooms, greenhouse staples sold all year |
| wild_mushroom | fruiting_temp_c | [8–12, 16–20] | |
| | min_precip_mm | 40–80 | monthly |
| seafood | regions | MEOW ecoregions | months from fisheries guidance, peak where clear |

Known limitation (spec §12.3): a `perennial_fruit` needs one clear month when
the mean crosses `warmup_temp_c`. In climates that never cross it (the humid
tropics) it shows nowhere. Tropical fruit sold all year (banana, pineapple,
papaya) should be `cultivated_year_round`.

## Worked examples (copy the shape; tune the numbers)

```json
{ "slug": "tomato", "name": "Tomato", "category": "vegetable", "kind": "annual",
  "frost": "tender", "grow_temp_c": [16, 30], "months_to_harvest": 3, "storage_months": 0,
  "tips": { "choose": "Heavy for its size, glossy, fragrant at the stem.",
            "store": "Stem-side down at room temperature; refrigerate only when fully ripe." },
  "pairings": ["basil", "garlic"], "note": "" }

{ "slug": "kale", "name": "Kale", "category": "vegetable", "kind": "annual",
  "frost": "hardy", "grow_temp_c": [4, 20], "months_to_harvest": 2, "storage_months": 0,
  "tips": { "choose": "Crisp, deep-coloured leaves without yellowing.", "store": "Wrapped loosely in the fridge; use within 5 days." },
  "pairings": ["garlic", "lemon"], "note": "Sweeter after a frost." }

{ "slug": "apple", "name": "Apple", "category": "fruit", "kind": "perennial_fruit",
  "frost": "hardy", "chill_months_min": 3, "warmup_temp_c": 10, "months_bloom_to_harvest": 5,
  "harvest_span_months": 3, "storage_months": 5,
  "tips": { "choose": "Firm, no soft spots, smells like apple.", "store": "Cold and humid, away from leafy greens." },
  "pairings": ["pork", "cinnamon"], "note": "" }

{ "slug": "banana", "name": "Banana", "category": "fruit", "kind": "cultivated_year_round",
  "tips": { "choose": "Yellow with a few brown flecks for eating now.", "store": "Room temperature; refrigerate once ripe." },
  "pairings": [], "note": "" }

{ "slug": "chanterelle", "name": "Chanterelle", "category": "mushroom", "kind": "wild_mushroom",
  "fruiting_temp_c": [10, 18], "min_precip_mm": 60, "storage_months": 0,
  "tips": { "choose": "Dry, firm, apricot smell; from a seller who can name the forager.",
            "store": "Paper bag in the fridge; use within 3 days." },
  "pairings": ["butter", "thyme"], "note": "" }
```

## Finding ecoregion IDs for seafood

After the ocean dataset is live (Plan 3), list the ecoregions near the ports
where a species is landed:

```sql
SELECT ecoregion_id, name FROM ocean_ecoregion
WHERE ST_DWithin(geom, ST_SetSRID(ST_MakePoint(<lng>, <lat>), 4326)::geography, 100000)
ORDER BY name;
```

Use the months from a fisheries or sustainable-seafood seasonal guide for that
region and record the source in your working notes. `peak_months` is optional.

## Pairings

Pairings must reference slugs in the catalog (or the same import file). Keep
them to real kitchen pairings, three to six per ingredient. They are
directional: add both directions when both make sense.
````

- [ ] **Step 4: Author the catalog**

Write `content/catalog/launch.json` in the §9.4 format. Start from the worked examples and cover, as a minimum:
- **Vegetables (≥30):** warm-season annuals, cool-season annuals, roots and storage crops (potato, onion, carrot, winter squash with `storage_months`), and a few `cultivated_year_round` staples.
- **Fruit (≥15):** temperate perennials (apple, pear, plum, cherry, apricot, peach), berries, citrus (tender, chill 0), and tropical fruit as `cultivated_year_round`.
- **Herbs (≥8):** basil, parsley, coriander, dill, mint, chives, thyme, rosemary.
- **Mushrooms (≥5):** at least 2 `cultivated_year_round` and at least 3 `wild_mushroom`.
- **Seafood (≥15):** species landed in different oceans and hemispheres, with ecoregion IDs found as in the guide above.

Run `go test ./content/ -run 'Launch|NearDuplicate|InSeasonSomewhere' -count=1` after each batch of about 10 ingredients.

- [ ] **Step 5: Run the catalog gates**

Run: `go test ./content/ -run 'Launch|NearDuplicate|InSeasonSomewhere' -count=1 -v`
Expected: all three PASS. (`TestFixtureCoverage` still fails until Task 2.)

- [ ] **Step 6: Commit**

```bash
git add content
git commit -m "content: launch catalog (≥60 land, ≥15 seafood) with acceptance gates

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Calibration fixtures from published guides

**Files:**
- Create: `calibration/fixtures/<location>.json` (about 15 files)

**Interfaces:**
- Consumes: the fixture format from Plan 1 (`id`, `name`, `lat`, `lng`, `source`, `months`).
- Produces: embedded fixtures scored by the admin preview and the daily drift check.

- [ ] **Step 1: Choose locations**

Pick about 15 cities that each have a published seasonal produce guide (government agriculture or health departments, city or regional farmers' market associations, sustainable-food charities). Required spread, which the gate enforces:
- at least 4 in the southern hemisphere;
- at least 2 in the tropics (|lat| < 23.5°);
- at least 2 at high latitude (|lat| > 50°);
- several climate types: oceanic, Mediterranean, humid continental, subtropical, tropical, highland.

For each candidate, confirm the guide gives months per ingredient and note its title and URL. Check its terms: record facts (which months) in your own words and format, and don't copy the guide's text or design.

- [ ] **Step 2: Write one fixture per location**

Use this shape (values here are illustrative; take the real months from your guide):

```json
{
  "id": "melbourne-au",
  "name": "Melbourne, Australia",
  "lat": -37.8136,
  "lng": 144.9631,
  "source": "<Guide title>, <publisher>, <URL>, accessed <YYYY-MM-DD>",
  "months": {
    "tomato": [1, 2, 3, 12],
    "apple": [2, 3, 4, 5],
    "kale": [5, 6, 7, 8, 9]
  }
}
```

Rules:
- Use catalog slugs exactly. Map the guide's names to them (courgette → `zucchini`, whichever slug the catalog uses).
- List only ingredients the guide covers. Aim for 15–30 per location.
- Months are 1–12. Include months the guide marks as "in season" or "peak"; leave out "imported" or "available but not local".

- [ ] **Step 3: Run the fixture gate**

Run: `go test ./content/ ./internal/calibration/ -count=1 -v`
Expected: all PASS, including `TestFixtureCoverage` and `TestEmbeddedFixturesLoad`.

- [ ] **Step 4: Commit**

```bash
git add calibration/fixtures
git commit -m "content: calibration fixtures from published seasonal guides

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Import, baseline score and tuning

**Files:**
- Modify: `content/catalog/launch.json` (parameter tuning only)
- Create: `docs/calibration-baseline.md`

**Interfaces:**
- Consumes: a stack with all three reference datasets live (Plan 3 via `docs/operations.md`), and the admin UI (Plan 7).

- [ ] **Step 1: Import into staging and read the preview**

On a staging deployment (same stack; a separate VPS or local Compose with real reference data), sign in to `/admin/import` and upload `content/catalog/launch.json`. The preview must show every item under Added and no validation problems. An unknown ecoregion ID shows as `regions[i].ecoregion_id … does not exist`, so fix it in the file and upload again.

- [ ] **Step 2: Record the baseline and find dead ingredients (Review Focus 3)**

Before applying, copy from the preview page the overall score after import, and every ingredient whose per-ingredient score is below 0.3. Then apply. After applying, run this on the staging DB to see ingredients never in season at any fixture location:

```bash
docker compose exec db psql -U tp -d thymeandplace -c "SELECT data_version, score, compared FROM calibration_run ORDER BY id DESC LIMIT 1"
```

For zero-month ingredients, open `/ingredient/<slug>?cell=<cell of a fixture city>` for two or three fixture cities and check the 12-month bar is empty. Either their parameters are wrong (fix them) or they really are regional (add a `note`).

- [ ] **Step 3: Tune**

For each low-scoring ingredient, adjust one parameter at a time in the admin form and read the preview's per-ingredient score and bars before applying. Aim for an overall score of **0.6 or higher** (a working target; the spec asks for broad agreement) with no ingredient below 0.3 unless it has a written reason. Export the tuned catalog from `/admin/export` and save it back to `content/catalog/launch.json`, so the repo matches what's live.

- [ ] **Step 4: Write down the baseline**

Create `docs/calibration-baseline.md`:

```markdown
# Calibration baseline

- Date: <YYYY-MM-DD>
- Data version: <from calibration_run>
- Score: <0.xxx> over <n> compared (fixture, ingredient) pairs
- Fixtures: <count> (<south> southern, <tropics> tropical, <high> high-latitude)

## Ingredients below 0.3 and why
| Ingredient | Score | Reason / follow-up |
|---|---|---|
```

- [ ] **Step 5: Verify and commit**

Run: `go test ./content/ -count=1`
Expected: PASS (the exported catalog still meets the gates).

```bash
git add content/catalog/launch.json docs/calibration-baseline.md
git commit -m "content: tuned launch catalog and recorded calibration baseline

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 6: Production import**

On production, import the committed `launch.json` through `/admin/import`. The preview should show the same score as the baseline (same reference data versions) before you apply.

---

## Plan completion check

- [ ] `go test ./content/ -count=1` passes.
- [ ] `docs/calibration-baseline.md` records the score, and the production `calibration_run` table has a row within 0.02 of it.
