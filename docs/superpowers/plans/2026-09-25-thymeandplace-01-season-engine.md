# Thyme & Place — Plan 1: Season Engine Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bootstrap the Go module and build the pure, database-free core: climate grid math, catalog domain types and validation, the immutable catalog snapshot, the season engine, and calibration scoring.

**Architecture:** Five small packages with no I/O. `climate` and `ocean` hold location types and grid math. `catalog` holds ingredient types, validation that mirrors the DB constraints, and an immutable `Snapshot`. `season` turns (climate normals, nearby ecoregions, month) plus a snapshot into per-ingredient month statuses. `calibration` scores a snapshot against published-guide fixtures with mean Jaccard overlap. Test helpers live in `climatetest` and `catalogtest` so later plans reuse them.

**Tech Stack:** Go ≥ 1.25, standard library only (`testing`, `embed`, `encoding/json`).

**Spec:** `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (§4.2, §4.3, §6.1, §8.1, §12, §13.1, §13.2)

## Global Constraints

- Go module path: `thymeandplace`. `go.mod` declares `go 1.25`.
- Go toolchain ≥ 1.25 on PATH. (Local machine has Go 1.22.5 at `C:\Program Files\Go\bin` and it isn't on PATH. Install Go 1.25+ and add `C:\Program Files\Go\bin` to PATH, or rely on `GOTOOLCHAIN=auto` fetching 1.25 when `go.mod` says `go 1.25`.)
- Shell for all commands: Git Bash from the repo root `J:\DevStuff\GoLang\thymeandplace`.
- `season` and `calibration` must be testable with no database (§4.3).
- Grid: 0.25°; `row = floor((90 - lat) / 0.25)`, `col = floor((lng + 180) / 0.25)`, `cell_id = row * 1440 + col`; latitude clamped to [-90, 90), longitude normalized to [-180, 180) (§6.1).
- Frost-free month: `tmin > 2.0 °C`. Chill month: monthly mean `< 7 °C` (§12.2, §16).
- Status precedence: Peak > InSeason > FromStorage; YearRound is exclusive (§12.4).
- Seafood `Basis` text is exactly `general guidance; check local fishing rules` (§12.3).
- Slug pattern `^[a-z0-9]+(-[a-z0-9]+)*$`; name 1–80 characters; `storage_months` 0–9 (§8.1).
- Calibration score = mean per-ingredient Jaccard overlap, over ingredients present in both catalog and fixture (§13.2).
- Commit after every task. Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Coordinates at or beyond the edges** (lat exactly 90 or −90, lng exactly 180, −180 or 540) must map to a valid in-range cell, not panic or produce an ID outside the grid. Pinned in Task 1.
2. **Perennial fruit in a climate with no warm-up transition** (e.g. a flat tropical climate): not shown, no panic, `ok == false`. In a climate with two warm-ups, the first one counting from January wins, every time. Pinned in Task 4.
3. **An invalid month in `Input`** (0 or 13 from a bad caller): `Estimate` returns nothing instead of panicking on an index. Pinned in Task 5.
4. **Snapshot immutability**: mutating the slice passed to `NewSnapshot`, or the slice returned by `All()`, must not change the snapshot other requests are reading. Pinned in Task 3.
5. **A 12-month harvest window** (perennial with `harvest_span_months = 12` in a frost-free climate): peaks are placed relative to the harvest start, storage adds nothing, and the loop terminates. Pinned in Task 4.

---

## File Structure

```
go.mod
.gitignore
internal/climate/grid.go            # MonthlyNormals, CellID, CellCenter, NeighborCells
internal/climate/grid_test.go
internal/climate/climatetest/climates.go   # synthetic climates + Rotate (test helper, importable)
internal/ocean/ocean.go             # EcoregionID
internal/catalog/types.go           # Category, Kind, Frost, Ingredient, SeafoodRegion
internal/catalog/validate.go        # Problem, ValidationError, Ingredient.Validate
internal/catalog/validate_test.go
internal/catalog/snapshot.go        # Snapshot, NewSnapshot, With
internal/catalog/snapshot_test.go
internal/catalog/catalogtest/builders.go   # valid builders, InvalidExamples, SmallCatalog
internal/season/status.go           # Status, Input, Result, Adjuster, NoopAdjuster
internal/season/runs.go             # run finding, peaks, storage, FormatMonths
internal/season/runs_test.go
internal/season/rules.go            # Year: rules by kind
internal/season/rules_test.go
internal/season/estimate.go         # Estimate
internal/season/estimate_test.go
internal/season/property_test.go    # rotation, wrap, determinism
internal/calibration/calibration.go # Fixture, LoadFixtures, Case, Report, Score, Jaccard
internal/calibration/calibration_test.go
calibration/fixtures.go             # package calibfixtures: embeds calibration/fixtures
calibration/fixtures/README.md
testdata/calibration/basic.json
```

---

### Task 1: Repository bootstrap and climate grid

**Files:**
- Create: `go.mod`, `.gitignore`, `internal/climate/grid.go`, `internal/climate/grid_test.go`, `internal/ocean/ocean.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `climate.Resolution = 0.25`, `climate.GridCols = 1440`, `climate.GridRows = 720`
  - `type climate.MonthlyNormals struct { TminC, TmaxC, PrecipMM [12]float64 }` with method `Mean(m int) float64` (m is 0-based)
  - `func climate.CellID(lat, lng float64) int`
  - `func climate.CellCenter(id int) (lat, lng float64)`
  - `func climate.ValidCellID(id int) bool`
  - `func climate.NeighborCells(id int) []int` (up to 8, longitude wraps, rows outside the grid omitted)
  - `type ocean.EcoregionID int32`

- [ ] **Step 1: Initialise the repository and module**

```bash
git init
go mod init thymeandplace
go mod edit -go=1.25
mkdir -p internal/climate internal/ocean
```

Create `.gitignore`:

```gitignore
# build output
/bin/
/dist/
*.exe
# local env and secrets
.env
*.env
!deploy/env.example
# python
__pycache__/
.pytest_cache/
.venv/
# editor
.idea/
.vscode/
# e2e
/e2e/node_modules/
/e2e/test-results/
/e2e/playwright-report/
```

- [ ] **Step 2: Write the failing grid tests**

Create `internal/climate/grid_test.go`:

```go
package climate

import (
	"math"
	"testing"
)

func TestCellIDFormula(t *testing.T) {
	cases := []struct {
		name     string
		lat, lng float64
		want     int
	}{
		{"north-west corner", 89.9, -179.9, 0},
		{"first row second col", 89.9, -179.7, 1},
		{"second row", 89.7, -179.9, 1440},
		{"london", 51.5074, -0.1278, 153*1440 + 719},
		{"sydney", -33.8688, 151.2093, 495*1440 + 1324},
		{"south-east corner", -89.99, 179.99, 719*1440 + 1439},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CellID(c.lat, c.lng); got != c.want {
				t.Fatalf("CellID(%v,%v) = %d, want %d", c.lat, c.lng, got, c.want)
			}
		})
	}
}

// Review Focus 1: edges and out-of-range inputs stay inside the grid.
func TestCellIDEdges(t *testing.T) {
	cases := []struct {
		name     string
		lat, lng float64
		want     int
	}{
		{"lat exactly 90", 90, 0, 0*1440 + 720},
		{"lat exactly -90 clamps to last row", -90, 0, 719*1440 + 720},
		{"lat below -90 clamps", -123, 0, 719*1440 + 720},
		{"lat above 90 clamps", 100, 0, 0*1440 + 720},
		{"lng exactly 180 wraps to -180", 0, 180, 360*1440 + 0},
		{"lng exactly -180", 0, -180, 360*1440 + 0},
		{"lng 540 wraps to -180", 0, 540, 360*1440 + 0},
		{"lng -181 wraps to 179", 0, -181, 360*1440 + 1436},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CellID(c.lat, c.lng)
			if got != c.want {
				t.Fatalf("CellID(%v,%v) = %d, want %d", c.lat, c.lng, got, c.want)
			}
			if !ValidCellID(got) {
				t.Fatalf("CellID(%v,%v) = %d is not a valid cell", c.lat, c.lng, got)
			}
		})
	}
}

func TestCellCenterRoundTrip(t *testing.T) {
	for _, id := range []int{0, 1, 1440, 153*1440 + 719, 719*1440 + 1439} {
		lat, lng := CellCenter(id)
		if got := CellID(lat, lng); got != id {
			t.Fatalf("CellID(CellCenter(%d)) = %d", id, got)
		}
	}
	lat, lng := CellCenter(0)
	if math.Abs(lat-89.875) > 1e-9 || math.Abs(lng+179.875) > 1e-9 {
		t.Fatalf("CellCenter(0) = %v,%v, want 89.875,-179.875", lat, lng)
	}
}

func TestNeighborCells(t *testing.T) {
	mid := 360*1440 + 720
	got := NeighborCells(mid)
	if len(got) != 8 {
		t.Fatalf("mid-grid neighbours = %d, want 8", len(got))
	}
	want := map[int]bool{
		359*1440 + 719: true, 359*1440 + 720: true, 359*1440 + 721: true,
		360*1440 + 719: true, 360*1440 + 721: true,
		361*1440 + 719: true, 361*1440 + 720: true, 361*1440 + 721: true,
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected neighbour %d", id)
		}
	}

	// Longitude wraps: column 0 neighbours column 1439.
	west := 360*1440 + 0
	found := false
	for _, id := range NeighborCells(west) {
		if id == 360*1440+1439 {
			found = true
		}
	}
	if !found {
		t.Fatalf("column 0 should neighbour column 1439")
	}

	// Top row has no row above: 5 neighbours.
	if n := len(NeighborCells(0*1440 + 5)); n != 5 {
		t.Fatalf("top row neighbours = %d, want 5", n)
	}
}

func TestValidCellID(t *testing.T) {
	if ValidCellID(-1) || ValidCellID(720*1440) {
		t.Fatal("out-of-range IDs must be invalid")
	}
	if !ValidCellID(0) || !ValidCellID(720*1440-1) {
		t.Fatal("boundary IDs must be valid")
	}
}

func TestMean(t *testing.T) {
	var n MonthlyNormals
	n.TminC[3], n.TmaxC[3] = 4, 16
	if n.Mean(3) != 10 {
		t.Fatalf("Mean = %v, want 10", n.Mean(3))
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/climate/`
Expected: FAIL to compile with `undefined: CellID` (and the other undefined names).

- [ ] **Step 4: Implement the grid**

Create `internal/climate/grid.go`:

```go
// Package climate holds the 0.25° climate grid and monthly normals.
package climate

import "math"

const (
	Resolution = 0.25
	GridCols   = 1440
	GridRows   = 720
)

// MonthlyNormals are 1991–2020 monthly climate normals for one grid cell.
// Index 0 is January.
type MonthlyNormals struct {
	TminC    [12]float64
	TmaxC    [12]float64
	PrecipMM [12]float64
}

// Mean is the monthly mean temperature, (tmin + tmax) / 2. m is 0-based.
func (n MonthlyNormals) Mean(m int) float64 {
	return (n.TminC[m] + n.TmaxC[m]) / 2
}

// CellID maps a coordinate to its grid cell. lat and lng must be finite;
// callers validate input before calling.
func CellID(lat, lng float64) int {
	row := int(math.Floor((90 - lat) / Resolution))
	row = min(max(row, 0), GridRows-1)
	col := int(math.Floor((normLng(lng) + 180) / Resolution))
	col = min(max(col, 0), GridCols-1)
	return row*GridCols + col
}

// CellCenter returns the centre coordinate of a cell.
func CellCenter(id int) (lat, lng float64) {
	row, col := id/GridCols, id%GridCols
	return 90 - (float64(row)+0.5)*Resolution, -180 + (float64(col)+0.5)*Resolution
}

// ValidCellID reports whether id is inside the grid.
func ValidCellID(id int) bool {
	return id >= 0 && id < GridRows*GridCols
}

// NeighborCells returns the up-to-8 cells around id. Longitude wraps;
// rows beyond the poles are omitted.
func NeighborCells(id int) []int {
	row, col := id/GridCols, id%GridCols
	out := make([]int, 0, 8)
	for dr := -1; dr <= 1; dr++ {
		r := row + dr
		if r < 0 || r >= GridRows {
			continue
		}
		for dc := -1; dc <= 1; dc++ {
			if dr == 0 && dc == 0 {
				continue
			}
			c := (col + dc + GridCols) % GridCols
			out = append(out, r*GridCols+c)
		}
	}
	return out
}

// normLng maps any longitude into [-180, 180).
func normLng(lng float64) float64 {
	l := math.Mod(lng+180, 360)
	if l < 0 {
		l += 360
	}
	return l - 180
}
```

Create `internal/ocean/ocean.go`:

```go
// Package ocean holds marine ecoregion types and lookups.
package ocean

// EcoregionID identifies a Marine Ecoregions of the World region.
type EcoregionID int32
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/climate/ ./internal/ocean/`
Expected: `ok  thymeandplace/internal/climate` and `? thymeandplace/internal/ocean [no test files]`.

- [ ] **Step 6: Commit**

```bash
git add .gitignore go.mod internal/climate internal/ocean docs
git commit -m "feat(climate): bootstrap module and 0.25° grid math

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Catalog types and ingredient validation

**Files:**
- Create: `internal/catalog/types.go`, `internal/catalog/validate.go`, `internal/catalog/validate_test.go`, `internal/catalog/catalogtest/builders.go`

**Interfaces:**
- Consumes: `ocean.EcoregionID`.
- Produces:
  - `type catalog.Category string` with `CategoryFruit "fruit"`, `CategoryVegetable "vegetable"`, `CategoryHerb "herb"`, `CategoryMushroom "mushroom"`, `CategorySeafood "seafood"`, and `var Categories []Category`
  - `type catalog.Kind string` with `KindAnnual "annual"`, `KindPerennialFruit "perennial_fruit"`, `KindYearRound "cultivated_year_round"`, `KindWildMushroom "wild_mushroom"`, `KindSeafood "seafood"`, and `var Kinds []Kind`
  - `type catalog.Frost string` with `FrostTender "tender"`, `FrostHardy "hardy"` (empty string = not set)
  - `type catalog.SeafoodRegion struct { EcoregionID ocean.EcoregionID; Months []int; PeakMonths []int }` (months are 1–12)
  - `type catalog.Ingredient struct` (fields below)
  - `type catalog.Problem struct { Slug, Field, Message string }`
  - `type catalog.ValidationError struct { Problems []Problem }` implementing `error`
  - `func (catalog.Ingredient) Validate() []catalog.Problem`
  - `catalogtest.Annual()`, `Hardy()`, `Perennial()`, `YearRound()`, `WildMushroom()`, `Seafood()` returning valid `catalog.Ingredient`
  - `type catalogtest.InvalidExample struct { Name string; Ingredient catalog.Ingredient; Field string }` and `func catalogtest.InvalidExamples() []InvalidExample`. **Every example must be rejected by both `Validate` and the database constraints** (Plan 2 runs the same list against Postgres).
  - `func catalogtest.P[T any](v T) *T` pointer helper

- [ ] **Step 1: Write the types**

Create `internal/catalog/types.go`:

```go
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
```

- [ ] **Step 2: Write the test builders and the shared invalid-example table**

Create `internal/catalog/catalogtest/builders.go`:

```go
// Package catalogtest provides valid ingredient builders, the shared table
// of invalid ingredients (rejected by both app validation and the DB), and
// a small test catalog.
package catalogtest

import (
	"strings"

	"thymeandplace/internal/catalog"
)

// P returns a pointer to v.
func P[T any](v T) *T { return &v }

func Annual() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "tomato", Name: "Tomato", Category: catalog.CategoryVegetable, Kind: catalog.KindAnnual,
		Frost: catalog.FrostTender, GrowTempMinC: P(16.0), GrowTempMaxC: P(30.0), MonthsToHarvest: P(3),
		TipChoose: "Heavy for its size, glossy, fragrant at the stem.",
		TipStore:  "Stem-side down at room temperature.",
	}
}

func Hardy() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "kale", Name: "Kale", Category: catalog.CategoryVegetable, Kind: catalog.KindAnnual,
		Frost: catalog.FrostHardy, GrowTempMinC: P(4.0), GrowTempMaxC: P(20.0), MonthsToHarvest: P(2),
		TipChoose: "Crisp, deep-coloured leaves.", TipStore: "Wrapped loosely in the fridge.",
	}
}

func Perennial() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "apple", Name: "Apple", Category: catalog.CategoryFruit, Kind: catalog.KindPerennialFruit,
		Frost: catalog.FrostHardy, ChillMonthsMin: P(2), WarmupTempC: P(10.0),
		MonthsBloomToHarvest: P(5), HarvestSpanMonths: P(2), StorageMonths: 4,
		TipChoose: "Firm, no soft spots.", TipStore: "Cold and humid; away from greens.",
	}
}

func YearRound() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "banana", Name: "Banana", Category: catalog.CategoryFruit, Kind: catalog.KindYearRound,
		TipChoose: "Yellow with a few brown flecks.", TipStore: "Room temperature.",
	}
}

func WildMushroom() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "chanterelle", Name: "Chanterelle", Category: catalog.CategoryMushroom, Kind: catalog.KindWildMushroom,
		FruitingTempMinC: P(8.0), FruitingTempMaxC: P(16.0), MinPrecipMM: P(50.0),
		TipChoose: "Dry, firm, fruity smell.", TipStore: "Paper bag in the fridge.",
	}
}

func Seafood() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "atlantic-cod", Name: "Atlantic cod", Category: catalog.CategorySeafood, Kind: catalog.KindSeafood,
		Regions: []catalog.SeafoodRegion{{EcoregionID: 25, Months: []int{1, 2, 3, 4}, PeakMonths: []int{2, 3}}},
		TipChoose: "Firm, translucent flesh; clean sea smell.", TipStore: "Coldest part of the fridge.",
	}
}

type InvalidExample struct {
	Name       string
	Ingredient catalog.Ingredient
	Field      string // field Validate must report
}

func with(base catalog.Ingredient, f func(*catalog.Ingredient)) catalog.Ingredient {
	f(&base)
	return base
}

// InvalidExamples lists ingredients that BOTH catalog validation and the
// database constraints must reject. Plan 2 runs this table against Postgres.
func InvalidExamples() []InvalidExample {
	return []InvalidExample{
		{"uppercase slug", with(Annual(), func(i *catalog.Ingredient) { i.Slug = "Tomato" }), "slug"},
		{"double hyphen slug", with(Annual(), func(i *catalog.Ingredient) { i.Slug = "a--b" }), "slug"},
		{"trailing hyphen slug", with(Annual(), func(i *catalog.Ingredient) { i.Slug = "tomato-" }), "slug"},
		{"empty name", with(Annual(), func(i *catalog.Ingredient) { i.Name = "" }), "name"},
		{"81 char name", with(Annual(), func(i *catalog.Ingredient) { i.Name = strings.Repeat("x", 81) }), "name"},
		{"storage 10", with(Annual(), func(i *catalog.Ingredient) { i.StorageMonths = 10 }), "storage_months"},
		{"storage -1", with(Annual(), func(i *catalog.Ingredient) { i.StorageMonths = -1 }), "storage_months"},
		{"seafood kind, vegetable category", with(Seafood(), func(i *catalog.Ingredient) { i.Category = catalog.CategoryVegetable }), "kind"},
		{"seafood category, annual kind", with(Annual(), func(i *catalog.Ingredient) { i.Category = catalog.CategorySeafood }), "kind"},
		{"wild mushroom in vegetable", with(WildMushroom(), func(i *catalog.Ingredient) { i.Category = catalog.CategoryVegetable }), "kind"},
		{"annual missing frost", with(Annual(), func(i *catalog.Ingredient) { i.Frost = "" }), "frost"},
		{"annual missing grow min", with(Annual(), func(i *catalog.Ingredient) { i.GrowTempMinC = nil }), "grow_temp_min_c"},
		{"annual missing grow max", with(Annual(), func(i *catalog.Ingredient) { i.GrowTempMaxC = nil }), "grow_temp_max_c"},
		{"annual grow max equals min", with(Annual(), func(i *catalog.Ingredient) { i.GrowTempMaxC = P(16.0) }), "grow_temp_max_c"},
		{"annual missing months to harvest", with(Annual(), func(i *catalog.Ingredient) { i.MonthsToHarvest = nil }), "months_to_harvest"},
		{"annual months to harvest 0", with(Annual(), func(i *catalog.Ingredient) { i.MonthsToHarvest = P(0) }), "months_to_harvest"},
		{"annual months to harvest 12", with(Annual(), func(i *catalog.Ingredient) { i.MonthsToHarvest = P(12) }), "months_to_harvest"},
		{"perennial missing frost", with(Perennial(), func(i *catalog.Ingredient) { i.Frost = "" }), "frost"},
		{"perennial missing chill", with(Perennial(), func(i *catalog.Ingredient) { i.ChillMonthsMin = nil }), "chill_months_min"},
		{"perennial chill 13", with(Perennial(), func(i *catalog.Ingredient) { i.ChillMonthsMin = P(13) }), "chill_months_min"},
		{"perennial missing warmup", with(Perennial(), func(i *catalog.Ingredient) { i.WarmupTempC = nil }), "warmup_temp_c"},
		{"perennial bloom 0", with(Perennial(), func(i *catalog.Ingredient) { i.MonthsBloomToHarvest = P(0) }), "months_bloom_to_harvest"},
		{"perennial missing bloom", with(Perennial(), func(i *catalog.Ingredient) { i.MonthsBloomToHarvest = nil }), "months_bloom_to_harvest"},
		{"perennial span 13", with(Perennial(), func(i *catalog.Ingredient) { i.HarvestSpanMonths = P(13) }), "harvest_span_months"},
		{"perennial missing span", with(Perennial(), func(i *catalog.Ingredient) { i.HarvestSpanMonths = nil }), "harvest_span_months"},
		{"mushroom fruiting max below min", with(WildMushroom(), func(i *catalog.Ingredient) { i.FruitingTempMaxC = P(5.0) }), "fruiting_temp_max_c"},
		{"mushroom missing fruiting min", with(WildMushroom(), func(i *catalog.Ingredient) { i.FruitingTempMinC = nil }), "fruiting_temp_min_c"},
		{"mushroom negative precip", with(WildMushroom(), func(i *catalog.Ingredient) { i.MinPrecipMM = P(-1.0) }), "min_precip_mm"},
		{"mushroom missing precip", with(WildMushroom(), func(i *catalog.Ingredient) { i.MinPrecipMM = nil }), "min_precip_mm"},
		{"self pairing", with(Annual(), func(i *catalog.Ingredient) { i.Pairings = []string{"tomato"} }), "pairings"},
		{"region with no months", with(Seafood(), func(i *catalog.Ingredient) { i.Regions[0].Months = nil; i.Regions[0].PeakMonths = nil }), "regions[0].months"},
		{"region month 13", with(Seafood(), func(i *catalog.Ingredient) { i.Regions[0].Months = []int{12, 13}; i.Regions[0].PeakMonths = nil }), "regions[0].months"},
		{"region month 0", with(Seafood(), func(i *catalog.Ingredient) { i.Regions[0].Months = []int{0, 1}; i.Regions[0].PeakMonths = nil }), "regions[0].months"},
		{"region 13 months", with(Seafood(), func(i *catalog.Ingredient) {
			i.Regions[0].Months = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 1}
			i.Regions[0].PeakMonths = nil
		}), "regions[0].months"},
		{"peak outside months", with(Seafood(), func(i *catalog.Ingredient) { i.Regions[0].PeakMonths = []int{9} }), "regions[0].peak_months"},
	}
}

// Mackerel is a second seafood species present in two ecoregions.
func Mackerel() catalog.Ingredient {
	return catalog.Ingredient{
		Slug: "mackerel", Name: "Mackerel", Category: catalog.CategorySeafood, Kind: catalog.KindSeafood,
		Regions: []catalog.SeafoodRegion{
			{EcoregionID: 25, Months: []int{6, 7, 8, 9}, PeakMonths: []int{7, 8}},
			{EcoregionID: 30, Months: []int{10, 11}},
		},
		TipChoose: "Bright eyes, stiff body.", TipStore: "Eat the day you buy it.",
	}
}
```

- [ ] **Step 3: Write the failing validation tests**

Create `internal/catalog/validate_test.go`:

```go
package catalog_test

import (
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
)

func TestValidBuildersPass(t *testing.T) {
	for _, ing := range []catalog.Ingredient{
		catalogtest.Annual(), catalogtest.Hardy(), catalogtest.Perennial(), catalogtest.YearRound(),
		catalogtest.WildMushroom(), catalogtest.Seafood(), catalogtest.Mackerel(),
	} {
		if p := ing.Validate(); len(p) != 0 {
			t.Errorf("%s: unexpected problems %v", ing.Slug, p)
		}
	}
}

func TestInvalidExamplesRejected(t *testing.T) {
	for _, ex := range catalogtest.InvalidExamples() {
		t.Run(ex.Name, func(t *testing.T) {
			problems := ex.Ingredient.Validate()
			for _, p := range problems {
				if p.Field == ex.Field {
					return
				}
			}
			t.Fatalf("want a problem on %q, got %v", ex.Field, problems)
		})
	}
}

func TestAppOnlyRules(t *testing.T) {
	cases := []struct {
		name  string
		ing   catalog.Ingredient
		field string
	}{
		{"unknown category", func() catalog.Ingredient { i := catalogtest.Annual(); i.Category = "grain"; return i }(), "category"},
		{"unknown kind", func() catalog.Ingredient { i := catalogtest.Annual(); i.Kind = "tree"; return i }(), "kind"},
		{"unknown frost", func() catalog.Ingredient { i := catalogtest.Annual(); i.Frost = "brr"; return i }(), "frost"},
		{"seafood without regions", func() catalog.Ingredient { i := catalogtest.Seafood(); i.Regions = nil; return i }(), "regions"},
		{"regions on non-seafood", func() catalog.Ingredient {
			i := catalogtest.Annual()
			i.Regions = catalogtest.Seafood().Regions
			return i
		}(), "regions"},
		{"smallint overflow", func() catalog.Ingredient { i := catalogtest.YearRound(); i.MonthsToHarvest = catalogtest.P(40000); return i }(), "months_to_harvest"},
		// The DB write path uses ON CONFLICT for links, so duplicates are app-only rules.
		{"duplicate pairing", func() catalog.Ingredient { i := catalogtest.Annual(); i.Pairings = []string{"basil", "basil"}; return i }(), "pairings"},
		{"duplicate ecoregion", func() catalog.Ingredient {
			i := catalogtest.Seafood()
			i.Regions = append(i.Regions, catalog.SeafoodRegion{EcoregionID: 25, Months: []int{5}})
			return i
		}(), "regions[1].ecoregion_id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, p := range c.ing.Validate() {
				if p.Field == c.field {
					return
				}
			}
			t.Fatalf("want problem on %q, got %v", c.field, c.ing.Validate())
		})
	}
}

func TestNameLengthCountsCharacters(t *testing.T) {
	i := catalogtest.Annual()
	i.Name = strings.Repeat("é", 80) // 160 bytes, 80 characters
	if p := i.Validate(); len(p) != 0 {
		t.Fatalf("80-character name rejected: %v", p)
	}
}

func TestProblemsCarrySlug(t *testing.T) {
	i := catalogtest.Annual()
	i.Name = ""
	p := i.Validate()
	if len(p) == 0 || p[0].Slug != "tomato" {
		t.Fatalf("problem should carry slug, got %v", p)
	}
}

func TestValidationErrorMessage(t *testing.T) {
	err := &catalog.ValidationError{Problems: []catalog.Problem{{Slug: "a", Field: "name", Message: "is required"}}}
	if !strings.Contains(err.Error(), "a: name is required") {
		t.Fatalf("Error() = %q", err.Error())
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./internal/catalog/...`
Expected: FAIL to compile: `ing.Validate undefined`.

- [ ] **Step 5: Implement validation**

Create `internal/catalog/validate.go`:

```go
package catalog

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

var slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Problem is one validation failure. Field uses the database column name,
// or "regions[i].months"-style paths for nested values.
type Problem struct {
	Slug    string
	Field   string
	Message string
}

func (p Problem) String() string {
	if p.Field == "" {
		return fmt.Sprintf("%s: %s", p.Slug, p.Message)
	}
	return fmt.Sprintf("%s: %s %s", p.Slug, p.Field, p.Message)
}

// ValidationError carries every problem found in a catalog or changeset.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.String()
	}
	return "catalog validation failed: " + strings.Join(parts, "; ")
}

// Validate checks one ingredient against rules that mirror the database
// CHECK constraints (see migrations), plus a few app-only rules noted inline.
// It does not check that pairing targets exist; Snapshot does that.
func (ing Ingredient) Validate() []Problem {
	var ps []Problem
	add := func(field, msg string, args ...any) {
		ps = append(ps, Problem{Slug: ing.Slug, Field: field, Message: fmt.Sprintf(msg, args...)})
	}

	if !slugRE.MatchString(ing.Slug) {
		add("slug", "must be lowercase letters and digits, separated by single hyphens")
	}
	if n := utf8.RuneCountInString(ing.Name); n < 1 || n > 80 {
		add("name", "must be 1 to 80 characters")
	}
	if !slices.Contains(Categories, ing.Category) { // app-only: DB enum rejects too, with a less friendly error
		add("category", "must be one of fruit, vegetable, herb, mushroom, seafood")
	}
	if !slices.Contains(Kinds, ing.Kind) {
		add("kind", "must be one of annual, perennial_fruit, cultivated_year_round, wild_mushroom, seafood")
	}
	if ing.Frost != "" && ing.Frost != FrostTender && ing.Frost != FrostHardy {
		add("frost", "must be tender or hardy")
	}
	if ing.StorageMonths < 0 || ing.StorageMonths > 9 {
		add("storage_months", "must be between 0 and 9")
	}
	if (ing.Kind == KindSeafood) != (ing.Category == CategorySeafood) {
		add("kind", "seafood kind and seafood category must go together")
	}
	if ing.Kind == KindWildMushroom && ing.Category != CategoryMushroom {
		add("kind", "wild_mushroom must be in the mushroom category")
	}

	for field, v := range map[string]*int{
		"months_to_harvest": ing.MonthsToHarvest, "chill_months_min": ing.ChillMonthsMin,
		"months_bloom_to_harvest": ing.MonthsBloomToHarvest, "harvest_span_months": ing.HarvestSpanMonths,
	} {
		if v != nil && (*v < math.MinInt16 || *v > math.MaxInt16) {
			add(field, "is out of range")
		}
	}

	switch ing.Kind {
	case KindAnnual:
		if ing.Frost == "" {
			add("frost", "is required for annuals")
		}
		if ing.GrowTempMinC == nil {
			add("grow_temp_min_c", "is required for annuals")
		}
		if ing.GrowTempMaxC == nil {
			add("grow_temp_max_c", "is required for annuals")
		} else if ing.GrowTempMinC != nil && !(*ing.GrowTempMaxC > *ing.GrowTempMinC) {
			add("grow_temp_max_c", "must be greater than grow_temp_min_c")
		}
		if ing.MonthsToHarvest == nil || *ing.MonthsToHarvest < 1 || *ing.MonthsToHarvest > 11 {
			add("months_to_harvest", "must be between 1 and 11 for annuals")
		}
	case KindPerennialFruit:
		if ing.Frost == "" {
			add("frost", "is required for perennial fruit")
		}
		if ing.ChillMonthsMin == nil || *ing.ChillMonthsMin < 0 || *ing.ChillMonthsMin > 12 {
			add("chill_months_min", "must be between 0 and 12 for perennial fruit")
		}
		if ing.WarmupTempC == nil {
			add("warmup_temp_c", "is required for perennial fruit")
		}
		if ing.MonthsBloomToHarvest == nil || *ing.MonthsBloomToHarvest < 1 || *ing.MonthsBloomToHarvest > 11 {
			add("months_bloom_to_harvest", "must be between 1 and 11 for perennial fruit")
		}
		if ing.HarvestSpanMonths == nil || *ing.HarvestSpanMonths < 1 || *ing.HarvestSpanMonths > 12 {
			add("harvest_span_months", "must be between 1 and 12 for perennial fruit")
		}
	case KindWildMushroom:
		if ing.FruitingTempMinC == nil {
			add("fruiting_temp_min_c", "is required for wild mushrooms")
		}
		if ing.FruitingTempMaxC == nil {
			add("fruiting_temp_max_c", "is required for wild mushrooms")
		} else if ing.FruitingTempMinC != nil && !(*ing.FruitingTempMaxC > *ing.FruitingTempMinC) {
			add("fruiting_temp_max_c", "must be greater than fruiting_temp_min_c")
		}
		if ing.MinPrecipMM == nil || *ing.MinPrecipMM < 0 {
			add("min_precip_mm", "must be 0 or more for wild mushrooms")
		}
	}

	seen := map[string]bool{}
	for _, p := range ing.Pairings {
		switch {
		case p == ing.Slug:
			add("pairings", "cannot include the ingredient itself")
		case seen[p]:
			add("pairings", "lists %q twice", p)
		}
		seen[p] = true
	}

	// App-only: regions belong to seafood, and seafood needs at least one.
	if ing.Kind == KindSeafood && len(ing.Regions) == 0 {
		add("regions", "seafood needs at least one region")
	}
	if ing.Kind != KindSeafood && len(ing.Regions) > 0 {
		add("regions", "only seafood can have regions")
	}
	seenEco := map[int32]bool{}
	for i, r := range ing.Regions {
		if seenEco[int32(r.EcoregionID)] {
			add(fmt.Sprintf("regions[%d].ecoregion_id", i), "is listed twice")
		}
		seenEco[int32(r.EcoregionID)] = true
		if len(r.Months) < 1 || len(r.Months) > 12 {
			add(fmt.Sprintf("regions[%d].months", i), "must list 1 to 12 months")
		}
		for _, m := range r.Months {
			if m < 1 || m > 12 {
				add(fmt.Sprintf("regions[%d].months", i), "must be between 1 and 12")
				break
			}
		}
		for _, m := range r.PeakMonths {
			if !slices.Contains(r.Months, m) {
				add(fmt.Sprintf("regions[%d].peak_months", i), "must be a subset of months")
				break
			}
		}
	}
	return ps
}
```

Note: the map iteration over smallint fields is unordered; `Validate` output order only matters for display, and tests look problems up by field.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go vet ./internal/catalog/... && go test ./internal/catalog/...`
Expected: `ok  thymeandplace/internal/catalog`.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog
git commit -m "feat(catalog): domain types and validation mirroring DB constraints

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Immutable catalog snapshot

**Files:**
- Create: `internal/catalog/snapshot.go`, `internal/catalog/snapshot_test.go`
- Modify: `internal/catalog/catalogtest/builders.go` (add `SmallCatalog`)

**Interfaces:**
- Consumes: `Ingredient.Validate`, `Problem`, `ValidationError` (Task 2).
- Produces:
  - `func catalog.NewSnapshot(version int64, items []Ingredient) (*Snapshot, error)`: validates every item, rejects duplicate slugs and pairings to unknown slugs; returns `*ValidationError`
  - `(*Snapshot).Version() int64`, `Len() int`, `Get(slug string) (Ingredient, bool)`, `All() []Ingredient` (sorted by slug; a fresh copy each call)
  - `(*Snapshot).With(upserts []Ingredient, deletes []string) (*Snapshot, error)`: the proposed catalog. Deleting a slug also strips pairings that point at it (mirrors `ON DELETE CASCADE`); deleting an unknown slug is a problem on field `slug`. Keeps the same `Version()`.
  - `func catalogtest.SmallCatalog() []catalog.Ingredient`: tomato, kale, winter-squash, basil, apple, orange, banana, button-mushroom, chanterelle, atlantic-cod, mackerel.

- [ ] **Step 1: Add the small test catalog**

Append to `internal/catalog/catalogtest/builders.go`:

```go
// SmallCatalog is the fixed test catalog used by engine, calibration and
// changeset tests. Every kind and category is represented.
func SmallCatalog() []catalog.Ingredient {
	tomato := Annual()
	tomato.Pairings = []string{"basil"}

	squash := catalog.Ingredient{
		Slug: "winter-squash", Name: "Winter squash", Category: catalog.CategoryVegetable, Kind: catalog.KindAnnual,
		Frost: catalog.FrostTender, GrowTempMinC: P(15.0), GrowTempMaxC: P(32.0), MonthsToHarvest: P(4), StorageMonths: 3,
		TipChoose: "Hard rind, heavy.", TipStore: "Cool and dry for months.",
	}
	basil := catalog.Ingredient{
		Slug: "basil", Name: "Basil", Category: catalog.CategoryHerb, Kind: catalog.KindAnnual,
		Frost: catalog.FrostTender, GrowTempMinC: P(18.0), GrowTempMaxC: P(32.0), MonthsToHarvest: P(2),
		Pairings:  []string{"tomato"},
		TipChoose: "Perky leaves, no black spots.", TipStore: "Stems in water on the counter.",
	}
	orange := catalog.Ingredient{
		Slug: "orange", Name: "Orange", Category: catalog.CategoryFruit, Kind: catalog.KindPerennialFruit,
		Frost: catalog.FrostTender, ChillMonthsMin: P(0), WarmupTempC: P(20.0),
		MonthsBloomToHarvest: P(7), HarvestSpanMonths: P(4),
		TipChoose: "Heavy, fine-pored skin.", TipStore: "Cool room or fridge.",
	}
	button := catalog.Ingredient{
		Slug: "button-mushroom", Name: "Button mushroom", Category: catalog.CategoryMushroom, Kind: catalog.KindYearRound,
		TipChoose: "Closed caps, dry surface.", TipStore: "Paper bag in the fridge.",
	}
	return []catalog.Ingredient{
		tomato, Hardy(), squash, basil, Perennial(), orange, YearRound(), button,
		WildMushroom(), Seafood(), Mackerel(),
	}
}
```

- [ ] **Step 2: Write the failing snapshot tests**

Create `internal/catalog/snapshot_test.go`:

```go
package catalog_test

import (
	"errors"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
)

func mustSnap(t *testing.T, items []catalog.Ingredient) *catalog.Snapshot {
	t.Helper()
	s, err := catalog.NewSnapshot(7, items)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	return s
}

func TestNewSnapshotSortsAndIndexes(t *testing.T) {
	s := mustSnap(t, catalogtest.SmallCatalog())
	if s.Version() != 7 || s.Len() != 11 {
		t.Fatalf("version/len = %d/%d", s.Version(), s.Len())
	}
	all := s.All()
	for i := 1; i < len(all); i++ {
		if all[i-1].Slug >= all[i].Slug {
			t.Fatalf("All() not sorted: %q before %q", all[i-1].Slug, all[i].Slug)
		}
	}
	if got, ok := s.Get("kale"); !ok || got.Name != "Kale" {
		t.Fatalf("Get(kale) = %v, %v", got, ok)
	}
	if _, ok := s.Get("nope"); ok {
		t.Fatal("Get(nope) should miss")
	}
}

func TestNewSnapshotRejects(t *testing.T) {
	dup := append(catalogtest.SmallCatalog(), catalogtest.Hardy())
	badPair := catalogtest.SmallCatalog()
	badPair[0].Pairings = []string{"unicorn"}
	invalid := catalogtest.SmallCatalog()
	invalid[0].Name = ""

	for name, items := range map[string][]catalog.Ingredient{"duplicate": dup, "unknown pairing": badPair, "invalid item": invalid} {
		t.Run(name, func(t *testing.T) {
			_, err := catalog.NewSnapshot(1, items)
			var ve *catalog.ValidationError
			if !errors.As(err, &ve) || len(ve.Problems) == 0 {
				t.Fatalf("want ValidationError, got %v", err)
			}
		})
	}
}

// Review Focus 4: the snapshot is immutable from the outside.
func TestSnapshotImmutable(t *testing.T) {
	items := catalogtest.SmallCatalog()
	s := mustSnap(t, items)

	items[0].Name = "CHANGED"
	items[0].Pairings[0] = "CHANGED"
	all := s.All()
	all[0].Name = "CHANGED TOO"
	all[0].Regions = nil

	for _, ing := range s.All() {
		if ing.Name == "CHANGED" || ing.Name == "CHANGED TOO" {
			t.Fatalf("snapshot mutated through caller slice: %q", ing.Slug)
		}
	}
	tomato, _ := s.Get("tomato")
	if tomato.Pairings[0] != "basil" {
		t.Fatalf("pairings mutated: %v", tomato.Pairings)
	}
	g, _ := s.Get("tomato")
	g.Pairings[0] = "CHANGED"
	again, _ := s.Get("tomato")
	if again.Pairings[0] != "basil" {
		t.Fatal("Get returned shared pairings slice")
	}
}

func TestWithUpsertAndDelete(t *testing.T) {
	s := mustSnap(t, catalogtest.SmallCatalog())

	changed := catalogtest.Hardy()
	changed.Name = "Curly kale"
	added := catalogtest.Hardy()
	added.Slug, added.Name = "chard", "Chard"

	p, err := s.With([]catalog.Ingredient{changed, added}, []string{"basil"})
	if err != nil {
		t.Fatalf("With: %v", err)
	}
	if p.Version() != s.Version() {
		t.Fatal("With must keep version")
	}
	if k, _ := p.Get("kale"); k.Name != "Curly kale" {
		t.Fatalf("kale not updated: %q", k.Name)
	}
	if _, ok := p.Get("chard"); !ok {
		t.Fatal("chard not added")
	}
	if _, ok := p.Get("basil"); ok {
		t.Fatal("basil not deleted")
	}
	// tomato paired with basil; the cascade strips it.
	if tm, _ := p.Get("tomato"); len(tm.Pairings) != 0 {
		t.Fatalf("pairing to deleted slug kept: %v", tm.Pairings)
	}
	// original untouched
	if _, ok := s.Get("basil"); !ok {
		t.Fatal("With mutated the receiver")
	}
}

func TestWithRejects(t *testing.T) {
	s := mustSnap(t, catalogtest.SmallCatalog())
	if _, err := s.With(nil, []string{"ghost"}); err == nil {
		t.Fatal("deleting an unknown slug must fail")
	}
	bad := catalogtest.Annual()
	bad.Pairings = []string{"ghost"}
	if _, err := s.With([]catalog.Ingredient{bad}, nil); err == nil {
		t.Fatal("pairing to unknown slug must fail")
	}
	// A pairing to an ingredient added in the same change is fine.
	newA := catalogtest.Hardy()
	newA.Slug, newA.Pairings = "chard", []string{"leek"}
	newB := catalogtest.Hardy()
	newB.Slug = "leek"
	if _, err := s.With([]catalog.Ingredient{newA, newB}, nil); err != nil {
		t.Fatalf("pairing within changeset rejected: %v", err)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/catalog/...`
Expected: FAIL to compile: `undefined: catalog.NewSnapshot`.

- [ ] **Step 4: Implement the snapshot**

Create `internal/catalog/snapshot.go`:

```go
package catalog

import (
	"fmt"
	"slices"
	"sort"
)

// Snapshot is an immutable, validated view of the whole catalog.
// It is safe for concurrent use.
type Snapshot struct {
	version int64
	items   []Ingredient // sorted by slug; never handed out without cloning
	index   map[string]int
}

// NewSnapshot validates items and builds a snapshot. It deep-copies items.
func NewSnapshot(version int64, items []Ingredient) (*Snapshot, error) {
	cp := make([]Ingredient, len(items))
	for i, it := range items {
		cp[i] = clone(it)
	}
	sort.Slice(cp, func(i, j int) bool { return cp[i].Slug < cp[j].Slug })

	var problems []Problem
	index := make(map[string]int, len(cp))
	for i, it := range cp {
		problems = append(problems, it.Validate()...)
		if _, dup := index[it.Slug]; dup {
			problems = append(problems, Problem{Slug: it.Slug, Field: "slug", Message: "is used by more than one ingredient"})
		}
		index[it.Slug] = i
	}
	for _, it := range cp {
		for _, p := range it.Pairings {
			if _, ok := index[p]; !ok {
				problems = append(problems, Problem{Slug: it.Slug, Field: "pairings", Message: fmt.Sprintf("refers to unknown ingredient %q", p)})
			}
		}
	}
	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return &Snapshot{version: version, items: cp, index: index}, nil
}

func (s *Snapshot) Version() int64 { return s.version }
func (s *Snapshot) Len() int       { return len(s.items) }

// Get returns a copy of one ingredient.
func (s *Snapshot) Get(slug string) (Ingredient, bool) {
	i, ok := s.index[slug]
	if !ok {
		return Ingredient{}, false
	}
	return clone(s.items[i]), true
}

// All returns copies of every ingredient, sorted by slug.
func (s *Snapshot) All() []Ingredient {
	out := make([]Ingredient, len(s.items))
	for i, it := range s.items {
		out[i] = clone(it)
	}
	return out
}

// With returns the catalog that would result from applying upserts and
// deletes. Deleting strips pairings that point at the deleted slug, as the
// database's ON DELETE CASCADE does.
func (s *Snapshot) With(upserts []Ingredient, deletes []string) (*Snapshot, error) {
	next := make(map[string]Ingredient, len(s.items))
	for _, it := range s.items {
		next[it.Slug] = clone(it)
	}
	var problems []Problem
	deleted := map[string]bool{}
	for _, slug := range deletes {
		if _, ok := next[slug]; !ok {
			problems = append(problems, Problem{Slug: slug, Field: "slug", Message: "does not exist"})
			continue
		}
		delete(next, slug)
		deleted[slug] = true
	}
	for slug, it := range next {
		it.Pairings = slices.DeleteFunc(it.Pairings, func(p string) bool { return deleted[p] })
		next[slug] = it
	}
	for _, u := range upserts {
		next[u.Slug] = clone(u)
	}
	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	items := make([]Ingredient, 0, len(next))
	for _, it := range next {
		items = append(items, it)
	}
	return NewSnapshot(s.version, items)
}

func clone(it Ingredient) Ingredient {
	it.Pairings = slices.Clone(it.Pairings)
	if it.Regions != nil {
		rs := make([]SeafoodRegion, len(it.Regions))
		for i, r := range it.Regions {
			rs[i] = SeafoodRegion{EcoregionID: r.EcoregionID, Months: slices.Clone(r.Months), PeakMonths: slices.Clone(r.PeakMonths)}
		}
		it.Regions = rs
	}
	it.GrowTempMinC = clonePtr(it.GrowTempMinC)
	it.GrowTempMaxC = clonePtr(it.GrowTempMaxC)
	it.MonthsToHarvest = clonePtr(it.MonthsToHarvest)
	it.ChillMonthsMin = clonePtr(it.ChillMonthsMin)
	it.WarmupTempC = clonePtr(it.WarmupTempC)
	it.MonthsBloomToHarvest = clonePtr(it.MonthsBloomToHarvest)
	it.HarvestSpanMonths = clonePtr(it.HarvestSpanMonths)
	it.FruitingTempMinC = clonePtr(it.FruitingTempMinC)
	it.FruitingTempMaxC = clonePtr(it.FruitingTempMaxC)
	it.MinPrecipMM = clonePtr(it.MinPrecipMM)
	return it
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/catalog/...`
Expected: `ok  thymeandplace/internal/catalog`.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog
git commit -m "feat(catalog): immutable validated snapshot with proposed-change view

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Season rules by kind

**Files:**
- Create: `internal/climate/climatetest/climates.go`, `internal/season/status.go`, `internal/season/runs.go`, `internal/season/runs_test.go`, `internal/season/rules.go`, `internal/season/rules_test.go`

**Interfaces:**
- Consumes: `climate.MonthlyNormals`, `ocean.EcoregionID`, `catalog.Ingredient` and constants.
- Produces:
  - `type season.Status int` with `Out` (0), `FromStorage`, `InSeason`, `Peak`, `YearRound`, and `String()` returning `"out"`, `"from storage"`, `"in season"`, `"peak"`, `"year-round"`
  - `type season.Input struct { Normals *climate.MonthlyNormals; Ecoregions []ocean.EcoregionID; Month time.Month }`
  - `type season.Result struct { Slug string; Status Status; Months [12]Status; Estimate bool; Basis string }`
  - `type season.Adjuster interface { Adjust(ctx context.Context, cellID int, results []Result) []Result }` and `season.NoopAdjuster{}`
  - `func season.Year(in Input, ing catalog.Ingredient) (months [12]Status, basis string, ok bool)`: full-year statuses; `ok` is false when no month is in season. `in.Month` is ignored.
  - `func season.FormatMonths(set [12]bool) string`: `""`, `"all year"`, or runs such as `"May, Nov–Feb"` (en dash; runs ordered by their start month; a Dec→Jan run is written from its start)
  - `climatetest.FromMeans(means [12]float64, diurnal float64, precip [12]float64) climate.MonthlyNormals`
  - `climatetest.Flat(meanC, diurnal, precipMM float64) climate.MonthlyNormals`
  - `climatetest.Rotate(n climate.MonthlyNormals, k int) climate.MonthlyNormals` (month m moves to (m+k) mod 12)
  - `climatetest.Tropical()`, `TemperateNorth()`, `TemperateSouth()`, `Alpine()`, `Polar()`, `Mediterranean()`, and `All() map[string]climate.MonthlyNormals`
  - `climatetest.Temperate()`: the explicit-array climate used by rule tests (means `[5,8,12,16,20,24,26,25,21,15,10,6]`, diurnal 8, precip 60 each month except October 40)

- [ ] **Step 1: Write the synthetic climates helper**

Create `internal/climate/climatetest/climates.go`:

```go
// Package climatetest builds synthetic climates for engine and calibration tests.
package climatetest

import (
	"math"

	"thymeandplace/internal/climate"
)

// FromMeans builds normals from explicit monthly means: tmin = mean - diurnal/2,
// tmax = mean + diurnal/2.
func FromMeans(means [12]float64, diurnal float64, precip [12]float64) climate.MonthlyNormals {
	var n climate.MonthlyNormals
	for m := 0; m < 12; m++ {
		n.TminC[m] = means[m] - diurnal/2
		n.TmaxC[m] = means[m] + diurnal/2
		n.PrecipMM[m] = precip[m]
	}
	return n
}

func Flat(meanC, diurnal, precipMM float64) climate.MonthlyNormals {
	var means, precip [12]float64
	for m := range means {
		means[m], precip[m] = meanC, precipMM
	}
	return FromMeans(means, diurnal, precip)
}

// Rotate shifts every monthly value by k months: month m moves to (m+k) mod 12.
func Rotate(n climate.MonthlyNormals, k int) climate.MonthlyNormals {
	var out climate.MonthlyNormals
	for m := 0; m < 12; m++ {
		to := ((m+k)%12 + 12) % 12
		out.TminC[to], out.TmaxC[to], out.PrecipMM[to] = n.TminC[m], n.TmaxC[m], n.PrecipMM[m]
	}
	return out
}

// Temperate is the explicit climate the rule tests reason about by hand.
//
//	month   J  F  M  A  M  J  J  A  S  O  N  D
//	mean    5  8 12 16 20 24 26 25 21 15 10  6
//	tmin    1  4  8 12 16 20 22 21 17 11  6  2   (frost-free: Feb–Nov)
//	precip 60 60 60 60 60 60 60 60 60 40 60 60
func Temperate() climate.MonthlyNormals {
	return FromMeans(
		[12]float64{5, 8, 12, 16, 20, 24, 26, 25, 21, 15, 10, 6}, 8,
		[12]float64{60, 60, 60, 60, 60, 60, 60, 60, 60, 40, 60, 60},
	)
}

// cosine builds a unimodal climate peaking in warmest (0-based month).
func cosine(mean, amplitude float64, warmest int, diurnal float64, precip [12]float64) climate.MonthlyNormals {
	var means [12]float64
	for m := 0; m < 12; m++ {
		means[m] = mean + amplitude*math.Cos(2*math.Pi*float64(m-warmest)/12)
	}
	return FromMeans(means, diurnal, precip)
}

func even(p float64) [12]float64 {
	var a [12]float64
	for i := range a {
		a[i] = p
	}
	return a
}

func Tropical() climate.MonthlyNormals       { return cosine(27, 1, 4, 8, even(200)) }
func TemperateNorth() climate.MonthlyNormals { return cosine(11, 8, 6, 8, even(60)) }
func TemperateSouth() climate.MonthlyNormals { return Rotate(TemperateNorth(), 6) }
func Alpine() climate.MonthlyNormals         { return cosine(3, 9, 6, 10, even(100)) }
func Polar() climate.MonthlyNormals          { return cosine(-12, 14, 6, 6, even(20)) }
func Mediterranean() climate.MonthlyNormals {
	return cosine(17, 7, 7, 10, [12]float64{100, 90, 70, 40, 20, 5, 1, 2, 20, 60, 90, 110})
}

// All returns every named synthetic climate. Each is unimodal, so rotation
// properties hold for them.
func All() map[string]climate.MonthlyNormals {
	return map[string]climate.MonthlyNormals{
		"tropical": Tropical(), "temperate-north": TemperateNorth(), "temperate-south": TemperateSouth(),
		"alpine": Alpine(), "polar": Polar(), "mediterranean": Mediterranean(), "temperate": Temperate(),
	}
}
```

- [ ] **Step 2: Write the status and interface types**

Create `internal/season/status.go`:

```go
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
	seafoodBasis   = "general guidance; check local fishing rules"
)
```

- [ ] **Step 3: Write failing tests for run helpers**

Create `internal/season/runs_test.go`:

```go
package season

import (
	"reflect"
	"testing"
)

func set(months ...int) [12]bool {
	var s [12]bool
	for _, m := range months {
		s[m-1] = true
	}
	return s
}

func TestRuns(t *testing.T) {
	cases := []struct {
		name   string
		in     [12]bool
		anchor int
		want   []run
	}{
		{"none", set(), 0, nil},
		{"single", set(5), 0, []run{{4, 1}}},
		{"wraps dec-jan", set(11, 12, 1, 2), 0, []run{{10, 4}}},
		{"two runs", set(2, 3, 7), 0, []run{{1, 2}, {6, 1}}},
		{"all from anchor", set(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12), 7, []run{{7, 12}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runs(c.in, c.anchor)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("runs = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPeakOffsets(t *testing.T) {
	cases := map[int][]int{1: {0}, 2: {0, 1}, 3: {1}, 4: {1, 2}, 5: {2}, 6: {2, 3}, 12: {5, 6}}
	for n, want := range cases {
		if got := peakOffsets(n); !reflect.DeepEqual(got, want) {
			t.Errorf("peakOffsets(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestFormatMonths(t *testing.T) {
	cases := []struct {
		in   [12]bool
		want string
	}{
		{set(), ""},
		{set(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12), "all year"},
		{set(5, 6, 7, 8, 9), "May–Sep"},
		{set(5), "May"},
		{set(11, 12, 1, 2, 5), "May, Nov–Feb"},
	}
	for _, c := range cases {
		if got := FormatMonths(c.in); got != c.want {
			t.Errorf("FormatMonths = %q, want %q", got, c.want)
		}
	}
}
```

- [ ] **Step 4: Run to verify failure**

Run: `go test ./internal/season/`
Expected: FAIL to compile: `undefined: run`.

- [ ] **Step 5: Implement run helpers**

Create `internal/season/runs.go`:

```go
package season

import (
	"sort"
	"strings"
	"time"
)

// run is a maximal stretch of consecutive months (wrapping Dec→Jan).
type run struct {
	Start int // 0-based month
	Len   int
}

// runs finds maximal runs in set. When every month is set, it returns a
// single 12-month run starting at anchor, so the caller controls where a
// full-year window "begins".
func runs(set [12]bool, anchor int) []run {
	first := -1
	for i := 0; i < 12; i++ {
		if !set[(anchor+i)%12] {
			first = (anchor + i) % 12
			break
		}
	}
	if first < 0 {
		return []run{{anchor, 12}}
	}
	var out []run
	cur := run{Start: -1}
	for i := 1; i <= 12; i++ {
		m := (first + i) % 12
		if set[m] {
			if cur.Start < 0 {
				cur = run{Start: m}
			}
			cur.Len++
			continue
		}
		if cur.Start >= 0 {
			out = append(out, cur)
			cur = run{Start: -1}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// peakOffsets returns the offsets within a window of length n that are peak:
// windows of 1–2 months are all peak; otherwise the middle month (odd n) or
// the middle two (even n).
func peakOffsets(n int) []int {
	switch {
	case n <= 2:
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	case n%2 == 1:
		return []int{n / 2}
	default:
		return []int{n/2 - 1, n / 2}
	}
}

// harvestStatuses marks each harvest run InSeason with Peak in its middle.
func harvestStatuses(harvest [12]bool, anchor int) [12]Status {
	var months [12]Status
	for _, r := range runs(harvest, anchor) {
		if r.Len == 0 {
			continue
		}
		for i := 0; i < r.Len; i++ {
			months[(r.Start+i)%12] = InSeason
		}
		for _, p := range peakOffsets(r.Len) {
			months[(r.Start+p)%12] = Peak
		}
	}
	return months
}

// applyStorage marks the storage months after each harvest run FromStorage,
// never downgrading a harvest month.
func applyStorage(months *[12]Status, harvest [12]bool, anchor, storage int) {
	if storage <= 0 {
		return
	}
	for _, r := range runs(harvest, anchor) {
		if r.Len == 0 || r.Len >= 12 {
			continue
		}
		end := r.Start + r.Len - 1
		for k := 1; k <= storage; k++ {
			m := (end + k) % 12
			if months[m] < FromStorage {
				months[m] = FromStorage
			}
		}
	}
}

// FormatMonths renders a month set as "May–Sep, Nov".
func FormatMonths(set [12]bool) string {
	count := 0
	for _, v := range set {
		if v {
			count++
		}
	}
	switch count {
	case 0:
		return ""
	case 12:
		return "all year"
	}
	var parts []string
	for _, r := range runs(set, 0) {
		start := monthAbbr(r.Start)
		if r.Len == 1 {
			parts = append(parts, start)
			continue
		}
		parts = append(parts, start+"–"+monthAbbr((r.Start+r.Len-1)%12))
	}
	return strings.Join(parts, ", ")
}

func monthAbbr(m int) string { return time.Month(m + 1).String()[:3] }
```

Note that `runs` returns an empty (nil) slice when the set is empty: `first` is 0, and the loop finds no set months.

- [ ] **Step 6: Run to verify the helper tests pass**

Run: `go test ./internal/season/ -run 'TestRuns|TestPeakOffsets|TestFormatMonths'`
Expected: `ok`.

- [ ] **Step 7: Write failing rule tests**

Create `internal/season/rules_test.go`:

```go
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
```

- [ ] **Step 8: Run to verify failure**

Run: `go test ./internal/season/`
Expected: FAIL to compile: `undefined: Year`.

- [ ] **Step 9: Implement the rules**

Create `internal/season/rules.go`:

```go
package season

import (
	"fmt"
	"strconv"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/ocean"
)

// Year computes the full-year statuses for one ingredient at one location.
// ok is false when the ingredient is not in season in any month.
// in.Month is ignored.
func Year(in Input, ing catalog.Ingredient) (months [12]Status, basis string, ok bool) {
	if ing.Kind == catalog.KindSeafood {
		months, basis = seafood(in.Ecoregions, ing)
	} else if in.Normals != nil {
		switch ing.Kind {
		case catalog.KindYearRound:
			months, basis = allYearRound(), "grown year-round"
		case catalog.KindAnnual:
			months, basis = annual(in.Normals, ing)
		case catalog.KindPerennialFruit:
			months, basis = perennial(in.Normals, ing)
		case catalog.KindWildMushroom:
			months, basis = wildMushroom(in.Normals, ing)
		}
	}
	for _, s := range months {
		if s != Out {
			return months, basis, true
		}
	}
	return [12]Status{}, "", false
}

func allYearRound() [12]Status {
	var m [12]Status
	for i := range m {
		m[i] = YearRound
	}
	return m
}

func frostFree(n *climate.MonthlyNormals, m int) bool { return n.TminC[m] > frostFreeTminC }

func annual(n *climate.MonthlyNormals, ing catalog.Ingredient) ([12]Status, string) {
	lo, hi, toHarvest := *ing.GrowTempMinC, *ing.GrowTempMaxC, *ing.MonthsToHarvest
	var grow [12]bool
	all := true
	for m := 0; m < 12; m++ {
		mean := n.Mean(m)
		grow[m] = lo <= mean && mean <= hi && (ing.Frost == catalog.FrostHardy || frostFree(n, m))
		all = all && grow[m]
	}
	if all {
		return allYearRound(), "growing conditions all year"
	}
	var harvest [12]bool
	for _, r := range runs(grow, 0) {
		if r.Len > toHarvest {
			for i := toHarvest; i < r.Len; i++ {
				harvest[(r.Start+i)%12] = true
			}
		}
	}
	months := harvestStatuses(harvest, 0)
	applyStorage(&months, harvest, 0, ing.StorageMonths)
	return months, fmt.Sprintf("growing conditions %s; about %d months to harvest", FormatMonths(grow), toHarvest)
}

func perennial(n *climate.MonthlyNormals, ing catalog.Ingredient) ([12]Status, string) {
	chill := 0
	for m := 0; m < 12; m++ {
		if n.Mean(m) < chillMeanC {
			chill++
		}
	}
	if chill < *ing.ChillMonthsMin {
		return [12]Status{}, ""
	}
	warm := *ing.WarmupTempC
	w := -1
	for m := 0; m < 12; m++ {
		if n.Mean(m) >= warm && n.Mean((m+11)%12) < warm {
			w = m
			break
		}
	}
	if w < 0 {
		return [12]Status{}, ""
	}
	start := (w + *ing.MonthsBloomToHarvest) % 12
	var harvest [12]bool
	for i := 0; i < *ing.HarvestSpanMonths; i++ {
		m := (start + i) % 12
		if ing.Frost == catalog.FrostTender && !frostFree(n, m) {
			continue
		}
		harvest[m] = true
	}
	months := harvestStatuses(harvest, start)
	applyStorage(&months, harvest, start, ing.StorageMonths)
	return months, fmt.Sprintf("warms past %s °C in %s; harvest %s", fmtC(warm), monthAbbr(w), FormatMonths(harvest))
}

func wildMushroom(n *climate.MonthlyNormals, ing catalog.Ingredient) ([12]Status, string) {
	var fruit [12]bool
	for m := 0; m < 12; m++ {
		mean := n.Mean(m)
		fruit[m] = *ing.FruitingTempMinC <= mean && mean <= *ing.FruitingTempMaxC && n.PrecipMM[m] >= *ing.MinPrecipMM
	}
	var months [12]Status
	for m, f := range fruit {
		if f {
			months[m] = InSeason
		}
	}
	applyStorage(&months, fruit, 0, ing.StorageMonths)
	return months, "fruiting weather " + FormatMonths(fruit)
}

func seafood(reach []ocean.EcoregionID, ing catalog.Ingredient) ([12]Status, string) {
	near := make(map[ocean.EcoregionID]bool, len(reach))
	for _, id := range reach {
		near[id] = true
	}
	var months [12]Status
	for _, r := range ing.Regions {
		if !near[r.EcoregionID] {
			continue
		}
		for _, m := range r.Months {
			if months[m-1] < InSeason {
				months[m-1] = InSeason
			}
		}
		for _, m := range r.PeakMonths {
			months[m-1] = Peak
		}
	}
	return months, seafoodBasis
}

func fmtC(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
```

- [ ] **Step 10: Run the rule tests**

Run: `go test ./internal/season/`
Expected: `ok  thymeandplace/internal/season`.

- [ ] **Step 11: Commit**

```bash
git add internal/climate/climatetest internal/season
git commit -m "feat(season): per-kind season rules, storage, peaks and month formatting

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Estimate and engine property tests

**Files:**
- Create: `internal/season/estimate.go`, `internal/season/estimate_test.go`, `internal/season/property_test.go`

**Interfaces:**
- Consumes: `Year`, `Input`, `Result`, `catalog.Snapshot` (Tasks 3–4).
- Produces: `func season.Estimate(in Input, snap *catalog.Snapshot) []Result`: every ingredient whose status in `in.Month` is not `Out`, in slug order. `Estimate` is `true` for land kinds and `false` for seafood. An invalid `in.Month` returns `nil`.

- [ ] **Step 1: Write the failing Estimate tests**

Create `internal/season/estimate_test.go`:

```go
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
```

- [ ] **Step 2: Write the failing property tests**

Create `internal/season/property_test.go`:

```go
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
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/season/`
Expected: FAIL to compile: `undefined: Estimate`.

- [ ] **Step 4: Implement Estimate**

Create `internal/season/estimate.go`:

```go
package season

import (
	"time"

	"thymeandplace/internal/catalog"
)

// Estimate returns every ingredient in season in in.Month, in slug order.
func Estimate(in Input, snap *catalog.Snapshot) []Result {
	if in.Month < time.January || in.Month > time.December {
		return nil
	}
	var out []Result
	for _, ing := range snap.All() {
		months, basis, ok := Year(in, ing)
		if !ok {
			continue
		}
		st := months[in.Month-1]
		if st == Out {
			continue
		}
		out = append(out, Result{
			Slug:     ing.Slug,
			Status:   st,
			Months:   months,
			Estimate: ing.Kind != catalog.KindSeafood,
			Basis:    basis,
		})
	}
	return out
}
```

- [ ] **Step 5: Run the whole engine suite**

Run: `go test ./internal/season/ -count=1`
Expected: `ok  thymeandplace/internal/season`. If `TestRotationEquivariance` fails for a cosine climate, print the means for that climate: a failure means a climate is not unimodal against that ingredient's thresholds, which is a bug in the helper, not the engine. `climatetest.All()` is designed to avoid that.

- [ ] **Step 6: Commit**

```bash
git add internal/season
git commit -m "feat(season): Estimate plus rotation, wrap and determinism properties

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Calibration scoring and embedded fixtures

**Files:**
- Create: `internal/calibration/calibration.go`, `internal/calibration/calibration_test.go`, `calibration/fixtures.go`, `calibration/fixtures/README.md`, `testdata/calibration/basic.json`

**Interfaces:**
- Consumes: `season.Year`, `season.Input`, `season.Out`, `catalog.Snapshot`.
- Produces:
  - `type calibration.Fixture struct { ID, Name string; Lat, Lng float64; Source string; Months map[string][]int }` (JSON keys `id`, `name`, `lat`, `lng`, `source`, `months`)
  - `func calibration.LoadFixtures(fsys fs.FS) ([]Fixture, error)`: reads every `*.json` at the root of `fsys`, validates, sorts by ID
  - `type calibration.Case struct { Fixture Fixture; Input season.Input }`
  - `type calibration.Report struct { Score float64; Compared int; PerIngredient map[string]float64; PerFixture map[string]float64 }`
  - `func calibration.Score(snap *catalog.Snapshot, cases []Case) Report`: `Score` is the mean Jaccard over all compared (fixture, ingredient) pairs; `PerIngredient[slug]` is that ingredient's mean across fixtures; `PerFixture[id]` is that fixture's mean. `Compared == 0` gives `Score == 0`.
  - `func calibration.Jaccard(a, b [12]bool) float64`: two empty sets score 1
  - `func calibfixtures.FS() fs.FS`: the embedded `calibration/fixtures` directory

- [ ] **Step 1: Write the test fixture**

Create `testdata/calibration/basic.json`:

```json
{
  "id": "temperate-test",
  "name": "Temperate test town",
  "lat": 51.5,
  "lng": -0.1,
  "source": "Hand-built fixture for unit tests",
  "months": {
    "tomato": [7, 8, 9],
    "apple": [8, 9, 10, 11],
    "banana": [],
    "not-in-catalog": [1, 2]
  }
}
```

Expected scores under `climatetest.Temperate()` with `SmallCatalog`:
- tomato: engine Jul–Sep, guide Jul–Sep → 1.0
- apple: engine Aug–Jan (Aug–Sep harvest + Oct–Jan storage = 6 months), guide Aug–Nov (4) → intersection 4, union 6 → 0.6667
- banana: engine all 12, guide empty → 0/12 = 0.0
- not-in-catalog: skipped
- Score = (1 + 0.6667 + 0) / 3 = 0.5556

- [ ] **Step 2: Write the failing tests**

Create `internal/calibration/calibration_test.go`:

```go
package calibration

import (
	"math"
	"os"
	"testing"
	"testing/fstest"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/season"
	calibfixtures "thymeandplace/calibration"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

func TestJaccard(t *testing.T) {
	var none, all [12]bool
	for i := range all {
		all[i] = true
	}
	half := [12]bool{true, true, true, true, true, true}
	if Jaccard(none, none) != 1 {
		t.Fatal("two empty sets must score 1")
	}
	if Jaccard(all, none) != 0 {
		t.Fatal("disjoint must score 0")
	}
	if !near(Jaccard(all, half), 0.5) {
		t.Fatalf("all vs half = %v", Jaccard(all, half))
	}
}

func TestLoadFixtures(t *testing.T) {
	fx, err := LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fx) != 1 || fx[0].ID != "temperate-test" || len(fx[0].Months["apple"]) != 4 {
		t.Fatalf("got %+v", fx)
	}
}

func TestLoadFixturesRejectsBadFiles(t *testing.T) {
	bad := map[string]string{
		"missing source": `{"id":"a","name":"A","lat":1,"lng":1,"months":{}}`,
		"bad month":      `{"id":"a","name":"A","lat":1,"lng":1,"source":"s","months":{"x":[13]}}`,
		"bad lat":        `{"id":"a","name":"A","lat":91,"lng":1,"source":"s","months":{}}`,
		"unknown field":  `{"id":"a","name":"A","lat":1,"lng":1,"source":"s","months":{},"extra":1}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			fsys := fstest.MapFS{"f.json": {Data: []byte(body)}}
			if _, err := LoadFixtures(fsys); err == nil {
				t.Fatal("want error")
			}
		})
	}
	dup := fstest.MapFS{
		"a.json": {Data: []byte(`{"id":"x","name":"A","lat":1,"lng":1,"source":"s","months":{}}`)},
		"b.json": {Data: []byte(`{"id":"x","name":"B","lat":1,"lng":1,"source":"s","months":{}}`)},
	}
	if _, err := LoadFixtures(dup); err == nil {
		t.Fatal("duplicate IDs must fail")
	}
}

func TestScore(t *testing.T) {
	fx, err := LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	if err != nil {
		t.Fatal(err)
	}
	n := climatetest.Temperate()
	rep := Score(snap, []Case{{Fixture: fx[0], Input: season.Input{Normals: &n}}})

	if rep.Compared != 3 {
		t.Fatalf("Compared = %d, want 3", rep.Compared)
	}
	if !near(rep.Score, (1+4.0/6+0)/3) {
		t.Fatalf("Score = %v", rep.Score)
	}
	if !near(rep.PerIngredient["tomato"], 1) || !near(rep.PerIngredient["apple"], 4.0/6) || !near(rep.PerIngredient["banana"], 0) {
		t.Fatalf("PerIngredient = %v", rep.PerIngredient)
	}
	if _, ok := rep.PerIngredient["not-in-catalog"]; ok {
		t.Fatal("slug missing from catalog must be skipped")
	}
	if !near(rep.PerFixture["temperate-test"], rep.Score) {
		t.Fatalf("PerFixture = %v", rep.PerFixture)
	}
}

func TestScoreEmpty(t *testing.T) {
	snap, _ := catalog.NewSnapshot(1, nil)
	if rep := Score(snap, nil); rep.Compared != 0 || rep.Score != 0 {
		t.Fatalf("got %+v", rep)
	}
}

func TestEmbeddedFixturesLoad(t *testing.T) {
	if _, err := LoadFixtures(calibfixtures.FS()); err != nil {
		t.Fatalf("embedded fixtures invalid: %v", err)
	}
}
```

- [ ] **Step 3: Create the embedded fixtures package**

Create `calibration/fixtures/README.md`:

```markdown
# Calibration fixtures

One JSON file per location, taken from a published seasonal guide.
Format: see `internal/calibration/calibration.go` (`Fixture`).
Every file must record its `source` (guide title and URL).
Fixtures are embedded into the binary and are used only by the admin
preview and the daily drift check. They are never shown on public pages.
```

Create `calibration/fixtures.go`:

```go
// Package calibfixtures embeds the published-guide calibration fixtures.
package calibfixtures

import (
	"embed"
	"io/fs"
)

//go:embed fixtures
var files embed.FS

// FS returns the fixtures directory as a filesystem rooted at fixtures/.
func FS() fs.FS {
	sub, err := fs.Sub(files, "fixtures")
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return sub
}
```

- [ ] **Step 4: Run to verify failure**

Run: `go test ./internal/calibration/`
Expected: FAIL to compile: `undefined: Jaccard`.

- [ ] **Step 5: Implement calibration**

Create `internal/calibration/calibration.go`:

```go
// Package calibration scores a catalog snapshot against published seasonal
// guides using mean per-ingredient Jaccard overlap.
package calibration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/season"
)

// Fixture is one location's months per ingredient from a published guide.
type Fixture struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Lat    float64          `json:"lat"`
	Lng    float64          `json:"lng"`
	Source string           `json:"source"`
	Months map[string][]int `json:"months"`
}

// LoadFixtures reads and validates every *.json file at the root of fsys.
func LoadFixtures(fsys fs.FS) ([]Fixture, error) {
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	var out []Fixture
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		var f Fixture
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := f.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if other, dup := seen[f.ID]; dup {
			return nil, fmt.Errorf("%s: id %q already used by %s", name, f.ID, other)
		}
		seen[f.ID] = path.Base(name)
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f Fixture) validate() error {
	switch {
	case f.ID == "" || f.Name == "":
		return fmt.Errorf("id and name are required")
	case f.Source == "":
		return fmt.Errorf("source is required")
	case f.Lat < -90 || f.Lat > 90 || f.Lng < -180 || f.Lng > 180:
		return fmt.Errorf("lat/lng out of range")
	}
	for slug, months := range f.Months {
		for _, m := range months {
			if m < 1 || m > 12 {
				return fmt.Errorf("%s: month %d out of range", slug, m)
			}
		}
	}
	return nil
}

// Case pairs a fixture with the engine input for its location.
type Case struct {
	Fixture Fixture
	Input   season.Input
}

// Report is the result of scoring one snapshot.
type Report struct {
	Score         float64            // mean over all compared (fixture, ingredient) pairs
	Compared      int                // number of pairs compared
	PerIngredient map[string]float64 // mean per ingredient across fixtures
	PerFixture    map[string]float64 // mean per fixture across ingredients
}

// Score compares engine months with guide months for every ingredient
// present in both the snapshot and a fixture.
func Score(snap *catalog.Snapshot, cases []Case) Report {
	rep := Report{PerIngredient: map[string]float64{}, PerFixture: map[string]float64{}}
	ingSum, ingN := map[string]float64{}, map[string]int{}
	var total float64

	for _, c := range cases {
		slugs := make([]string, 0, len(c.Fixture.Months))
		for s := range c.Fixture.Months {
			slugs = append(slugs, s)
		}
		sort.Strings(slugs)

		var fSum float64
		fN := 0
		for _, slug := range slugs {
			ing, ok := snap.Get(slug)
			if !ok {
				continue
			}
			months, _, _ := season.Year(c.Input, ing)
			var engine, guide [12]bool
			for m, s := range months {
				engine[m] = s != season.Out
			}
			for _, m := range c.Fixture.Months[slug] {
				guide[m-1] = true
			}
			j := Jaccard(engine, guide)
			fSum += j
			fN++
			ingSum[slug] += j
			ingN[slug]++
			total += j
			rep.Compared++
		}
		if fN > 0 {
			rep.PerFixture[c.Fixture.ID] = fSum / float64(fN)
		}
	}
	for slug, sum := range ingSum {
		rep.PerIngredient[slug] = sum / float64(ingN[slug])
	}
	if rep.Compared > 0 {
		rep.Score = total / float64(rep.Compared)
	}
	return rep
}

// Jaccard is |a ∩ b| / |a ∪ b|; two empty sets score 1.
func Jaccard(a, b [12]bool) float64 {
	inter, union := 0, 0
	for i := 0; i < 12; i++ {
		if a[i] && b[i] {
			inter++
		}
		if a[i] || b[i] {
			union++
		}
	}
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}
```

- [ ] **Step 6: Run the calibration tests and the full suite**

Run: `go vet ./... && go test ./... -count=1`
Expected: every package `ok` (or `[no test files]`).

- [ ] **Step 7: Commit**

```bash
git add internal/calibration calibration testdata
git commit -m "feat(calibration): Jaccard scoring and embedded fixture loader

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Plan completion check

- [ ] `go vet ./...` is clean and `go test ./... -count=1` passes with Docker stopped (proves no DB dependency).
- [ ] Every rule in spec §12.3 has a threshold case in `rules_test.go`: annual (inclusive min, run == months_to_harvest, frost split, wrap, year-round), perennial (chill < vs =, warm-up, tender frost filter, no transition, 12-month window), year-round, wild mushroom (temp + precip), storage, seafood (reach, peak, precedence).
- [ ] Property tests cover hemisphere symmetry, wrap invariance and determinism (spec §13.1).
