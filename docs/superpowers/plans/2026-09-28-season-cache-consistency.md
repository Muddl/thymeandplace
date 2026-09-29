# Season Result Consistency and Cache Keys Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every cached `/season` result depend only on its key: query cell, month and data version. Also make the `/season` list, the `/ingredient` detail page, its pairings, its "Back" link and calibration agree for a cell.

**Architecture:** A new package, `internal/cellinput`, turns a query cell into engine inputs by looking up normals (with the nearest-land fallback) and ecoregions at the cell's **centre**. `/season` and `/ingredient` both read those inputs through the existing per-cell cache (`Server.cells`, added by the capacity work in `620cf2b`), whose value type becomes `cellinput.Inputs`. Only the heading label uses the request's exact point. It is computed per request, never cached, under one 300 ms budget. `calibrun.Cases` resolves fixtures through the same resolver.

**Tech Stack:** Go 1.26, pgx/v5, testcontainers via `internal/dbtest`, templ.

**Spec:** `docs/superpowers/specs/2026-09-27-season-cache-consistency-design.md`. Issue [#6](https://github.com/Muddl/thymeandplace/issues/6).

**Spec drift at HEAD:** The spec was written before the capacity fix (`620cf2b`). That fix added `Server.cells` (`cache.Cache[cache.CellKey, cellInputs]`), `inputsFor` and the public DB gate (`withDB`), plus the seafood flag (`Deps.Seafood`), which skips ocean lookups when off. As spec §2 says ("whichever design lands second switches that cache's value to `cellinput.Inputs`, and `/season` misses then read through it too"), this plan does exactly that. It also deviates from the spec's code in two ways. `compute` takes resolved `Inputs` rather than a cell ID. The label lookup goes through `withDB`, so a full gate falls back to coordinates.

## Global Constraints

- Rule (spec §4.1): a public result for a location depends only on the 0.25° cell that contains it. Every reference-data lookup feeding results uses the cell centre (`climate.CellCenter`). Only the heading label uses the exact point, and it is never cached.
- The cache keys (`cache.Key`, `cache.CellKey`), the cache sizes and the version-before-snapshot read order don't change.
- The fallback tie-break stays "lower cell ID" (`climate.PickNearest`).
- `Adjuster.Adjust` gets the **normals** cell (`Inputs.NormalsCell`).
- Ingredient links, pairings and `/ingredient?cell=` carry the **query** cell.
- With `Deps.Seafood` off, no ocean lookup is made: `cellinput.Resolver.Ocean` is nil.
- The label: the `?place=` place if it is within 50 km of the point, else `places.Nearest` within 50 km as `near <label>`, else coordinates. It uses one `placeLookupTimeout` (300 ms) context and one DB gate slot, and never fails the request.
- No migration, no config change, no new dependency.

## Review Focus

1. **A cache hit during a DB outage.** The results must still render with status 200, and the heading falls back to coordinates. Task 4 updates `TestSeasonWarmCacheSurvivesOutage` and adds a places-down case to `TestSeasonLabelPerRequest`.
2. **The public DB gate is full on a cache hit.** The per-request label now needs a slot. It must fall back to coordinates within about the gate wait, never 503. Task 4 updates `TestDBGateShedsWhenFull`.
3. **Old `/ingredient?cell=<normals cell>` links from before the deploy.** A land cell resolves to itself, so they must render as before. Task 3 adds `TestIngredientOldLandCellLink`.
4. **Seafood off.** No ocean lookups may happen on either path, including the new shared resolver. Task 3 adds `TestSeafoodOffNeverQueriesOcean`.
5. **Cells at the grid edges** (row 0, row 719, column 0, column 1439). `Resolve` must pass their exact centres and not panic. Task 1's table pins this.

---

### Task 1: `internal/cellinput`

**Files:**
- Create: `internal/cellinput/cellinput.go`
- Test: `internal/cellinput/cellinput_test.go`

**Interfaces:**
- Consumes: `climate.CellCenter(id int) (lat, lng float64)`, `climate.ValidCellID(id int) bool`, `climate.Lookup{QueryCell, Cell int; Normals *climate.MonthlyNormals}`, `ocean.EcoregionID`, `season.Input{Normals, Ecoregions, Month}`.
- Produces:
  ```go
  type Inputs struct {
      QueryCell   int
      NormalsCell int
      Normals     *climate.MonthlyNormals
      Ecoregions  []ocean.EcoregionID
  }
  func (in Inputs) Supported() bool
  func (in Inputs) Engine(m time.Month) season.Input
  type NormalsFinder interface { Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error) }
  type EcoregionFinder interface { Nearby(ctx context.Context, lat, lng float64) ([]ocean.EcoregionID, error) }
  type Resolver struct { Climate NormalsFinder; Ocean EcoregionFinder } // Ocean nil → no ecoregions
  func (r Resolver) Resolve(ctx context.Context, cell int) (Inputs, error)
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/cellinput/cellinput_test.go`:

```go
package cellinput_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"thymeandplace/internal/cellinput"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/ocean"
)

type seen struct{ lat, lng float64 }

type fakeNormals struct {
	got     []seen
	lookup  climate.Lookup
	err     error
}

func (f *fakeNormals) Normals(_ context.Context, lat, lng float64) (climate.Lookup, error) {
	f.got = append(f.got, seen{lat, lng})
	return f.lookup, f.err
}

type fakeEco struct {
	got []seen
	ids []ocean.EcoregionID
	err error
}

func (f *fakeEco) Nearby(_ context.Context, lat, lng float64) ([]ocean.EcoregionID, error) {
	f.got = append(f.got, seen{lat, lng})
	return f.ids, f.err
}

func TestResolveLooksUpAtCellCentre(t *testing.T) {
	ctx := context.Background()
	last := climate.GridRows*climate.GridCols - 1
	for _, cell := range []int{0, climate.GridCols - 1, climate.CellID(40.1, -8.2), (climate.GridRows - 1) * climate.GridCols, last} {
		n := climatetest.Temperate()
		cl := &fakeNormals{lookup: climate.Lookup{QueryCell: cell, Cell: cell + 1, Normals: &n}}
		oc := &fakeEco{ids: []ocean.EcoregionID{25}}
		in, err := cellinput.Resolver{Climate: cl, Ocean: oc}.Resolve(ctx, cell)
		if err != nil {
			t.Fatalf("cell %d: %v", cell, err)
		}
		lat, lng := climate.CellCenter(cell)
		want := []seen{{lat, lng}}
		if len(cl.got) != 1 || cl.got[0] != want[0] || len(oc.got) != 1 || oc.got[0] != want[0] {
			t.Fatalf("cell %d: normals at %v, ocean at %v; want centre %v", cell, cl.got, oc.got, want[0])
		}
		if in.QueryCell != cell || in.NormalsCell != cell+1 || in.Normals != &n || len(in.Ecoregions) != 1 {
			t.Fatalf("cell %d: %+v", cell, in)
		}
	}
}

func TestResolveWithoutOceanSkipsEcoregions(t *testing.T) {
	cl := &fakeNormals{}
	in, err := cellinput.Resolver{Climate: cl}.Resolve(context.Background(), 100)
	if err != nil || in.Ecoregions != nil {
		t.Fatalf("%+v %v", in, err)
	}
}

func TestResolveErrors(t *testing.T) {
	ctx := context.Background()
	down := errors.New("down")
	cl, oc := &fakeNormals{}, &fakeEco{}
	for _, bad := range []int{-1, climate.GridRows * climate.GridCols} {
		if _, err := (cellinput.Resolver{Climate: cl, Ocean: oc}).Resolve(ctx, bad); err == nil {
			t.Fatalf("cell %d: want error", bad)
		}
	}
	if len(cl.got)+len(oc.got) != 0 {
		t.Fatal("an invalid cell must not query")
	}
	if _, err := (cellinput.Resolver{Climate: &fakeNormals{err: down}, Ocean: oc}).Resolve(ctx, 5); !errors.Is(err, down) {
		t.Fatalf("normals error not wrapped: %v", err)
	}
	if _, err := (cellinput.Resolver{Climate: &fakeNormals{}, Ocean: &fakeEco{err: down}}).Resolve(ctx, 5); !errors.Is(err, down) {
		t.Fatalf("ocean error not wrapped: %v", err)
	}
}

func TestSupportedAndEngine(t *testing.T) {
	n := climatetest.Temperate()
	for _, tc := range []struct {
		in   cellinput.Inputs
		want bool
	}{
		{cellinput.Inputs{}, false},
		{cellinput.Inputs{Normals: &n}, true},
		{cellinput.Inputs{Ecoregions: []ocean.EcoregionID{1}}, true},
		{cellinput.Inputs{Normals: &n, Ecoregions: []ocean.EcoregionID{1}}, true},
	} {
		if got := tc.in.Supported(); got != tc.want {
			t.Errorf("%+v: Supported = %v", tc.in, got)
		}
	}
	e := cellinput.Inputs{Normals: &n, Ecoregions: []ocean.EcoregionID{3}}.Engine(time.March)
	if e.Normals != &n || e.Month != time.March || len(e.Ecoregions) != 1 {
		t.Fatalf("Engine = %+v", e)
	}
}

// Integration: the real services behind the resolver.
func TestResolveAgainstReferenceData(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	r := cellinput.Resolver{Climate: climate.NewService(pool), Ocean: ocean.NewService(pool)}

	lisbon := climate.CellID(dbtest.Lisbon.Lat, dbtest.Lisbon.Lng)
	in, err := r.Resolve(ctx, lisbon)
	if err != nil || in.Normals == nil || in.NormalsCell != lisbon || len(in.Ecoregions) != 1 || in.Ecoregions[0] != 30 {
		t.Fatalf("lisbon: %+v %v", in, err)
	}
	fb := climate.CellID(dbtest.FallbackPoint.Lat, dbtest.FallbackPoint.Lng)
	if in, err := r.Resolve(ctx, fb); err != nil || in.Normals == nil || in.NormalsCell != lisbon || in.QueryCell != fb {
		t.Fatalf("fallback: %+v %v", in, err)
	}
	sea := climate.CellID(dbtest.MidAtlantic.Lat, dbtest.MidAtlantic.Lng)
	if in, err := r.Resolve(ctx, sea); err != nil || in.Supported() {
		t.Fatalf("mid-Atlantic: %+v %v", in, err)
	}
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/cellinput/ -count=1`
Expected: FAIL to build (`package thymeandplace/internal/cellinput is not in std` / no non-test Go files).

- [ ] **Step 3: Implement the package**

Create `internal/cellinput/cellinput.go`:

```go
// Package cellinput turns a query cell into engine inputs. Every lookup uses
// the cell's centre, so the inputs are a function of the cell and the live
// reference data, never of a point inside the cell. /season, /ingredient and
// calibration all resolve through it (core spec §11.3).
package cellinput

import (
	"context"
	"fmt"
	"time"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/season"
)

// Inputs are the engine inputs for one query cell.
type Inputs struct {
	QueryCell   int
	NormalsCell int                     // QueryCell, or the neighbour the fallback chose
	Normals     *climate.MonthlyNormals // nil when no land cell is within reach
	Ecoregions  []ocean.EcoregionID     // ascending; empty inland or with seafood off
}

// Supported reports whether the engine can say anything for the cell.
func (in Inputs) Supported() bool { return in.Normals != nil || len(in.Ecoregions) > 0 }

// Engine is the season-engine input for month m.
func (in Inputs) Engine(m time.Month) season.Input {
	return season.Input{Normals: in.Normals, Ecoregions: in.Ecoregions, Month: m}
}

type NormalsFinder interface {
	Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error)
}

type EcoregionFinder interface {
	Nearby(ctx context.Context, lat, lng float64) ([]ocean.EcoregionID, error)
}

// Resolver looks inputs up. A nil Ocean means no ecoregion is ever in reach
// (the public site with seafood off).
type Resolver struct {
	Climate NormalsFinder
	Ocean   EcoregionFinder
}

// Resolve returns the cell's inputs, looked up at its centre. CellID of a
// centre is the cell itself (climate.TestCellCenterRoundTrip), so the
// fallback measures from the centre of the query cell. An invalid cell ID is
// an error, and nothing is queried.
func (r Resolver) Resolve(ctx context.Context, cell int) (Inputs, error) {
	if !climate.ValidCellID(cell) {
		return Inputs{}, fmt.Errorf("cellinput: invalid cell %d", cell)
	}
	lat, lng := climate.CellCenter(cell)
	l, err := r.Climate.Normals(ctx, lat, lng)
	if err != nil {
		return Inputs{}, fmt.Errorf("cell %d normals: %w", cell, err)
	}
	in := Inputs{QueryCell: cell, NormalsCell: l.Cell, Normals: l.Normals}
	if r.Ocean != nil {
		if in.Ecoregions, err = r.Ocean.Nearby(ctx, lat, lng); err != nil {
			return Inputs{}, fmt.Errorf("cell %d ecoregions: %w", cell, err)
		}
	}
	return in, nil
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/cellinput/ -count=1`
Expected: PASS. The integration test needs Docker.

- [ ] **Step 5: Commit**

```bash
git add internal/cellinput/
git commit -m "cellinput: resolve a query cell's engine inputs at its centre (#6)"
```

### Task 2: Pin the fallback tie-break at cell centres

**Files:**
- Test: `internal/climate/service_test.go` (extend `TestPickNearest`)
- Modify: `internal/climate/service.go:20-21` (the `PickNearest` comment)

**Interfaces:**
- Consumes: `climate.PickNearest(lat, lng float64, own int, cells map[int]MonthlyNormals) (int, bool)`.
- Produces: no API change; the tie-break becomes tested contract.

- [ ] **Step 1: Add the tie cases**

Append inside `TestPickNearest`, before the closing brace:

```go
	// From the centre, opposite neighbours are exactly equidistant (centres
	// are multiples of 0.125°): ties go to the lower cell ID. Public callers
	// always pass the query cell's centre (cellinput), so this is routine.
	clat, clng := climate.CellCenter(own)
	if id, ok := climate.PickNearest(clat, clng, own, map[int]climate.MonthlyNormals{east: n, west: n}); !ok || id != west {
		t.Fatalf("east/west tie: got %d, want west %d", id, west)
	}
	north, south := own-climate.GridCols, own+climate.GridCols
	if id, ok := climate.PickNearest(clat, clng, own, map[int]climate.MonthlyNormals{north: n, south: n}); !ok || id != north {
		t.Fatalf("north/south tie: got %d, want north %d", id, north)
	}
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/climate/ -run TestPickNearest -count=1`
Expected: PASS. This pins current behaviour. If it fails, the tie-break isn't what the spec assumes. Stop and report; don't change `PickNearest`.

- [ ] **Step 3: Update the comment**

Replace the `PickNearest` comment with:

```go
// PickNearest returns own if it has data, otherwise the neighbour of own
// whose centre is nearest (lat, lng). Ties go to the lower cell ID. Public
// callers pass the centre of the query cell (internal/cellinput), so the
// result depends only on the cell.
```

- [ ] **Step 4: Commit**

```bash
git add internal/climate/service.go internal/climate/service_test.go
git commit -m "climate: pin the fallback tie-break at cell centres (#6)"
```

### Task 3: One per-cell input cache for both pages; `/ingredient` on the resolver

**Files:**
- Create: `internal/web/inputs.go` (moves `inputsFor` out of `ingredient.go`)
- Modify: `internal/web/ingredient.go:18-54` (delete `cellInputs` and `inputsFor`), `:79-93` (use `in.Engine`)
- Modify: `internal/web/server.go:27-30` (drop `ForCell` from `Climate`), `:94` and `:124` (cache value type), `New` (build `s.inputs`)
- Modify: `internal/cache/cache.go:13` (comment)
- Modify: `internal/web/fakes_test.go` (fakes record points; optional `cells`, `reach`, `nearest`; drop `ForCell`)
- Modify: `internal/web/gate_test.go:98-129` (messages say `Normals`, not `ForCell`)
- Test: `internal/web/ingredient_test.go` (new tests)

**Interfaces:**
- Consumes: `cellinput.Resolver`, `cellinput.Inputs` (Task 1).
- Produces:
  - `func (s *Server) inputsFor(ctx context.Context, cell int, version string) (cellinput.Inputs, error)`. It goes through `withDB` on a miss, caches successes (including cells with no normals), and returns `errBusy` when the gate is full.
  - `Server.inputs cellinput.Resolver`.
  - Test fakes: `fakeClimate.cells map[int]climate.MonthlyNormals`, `(*fakeClimate).points() [][2]float64`, `fakeOcean.reach func(lat, lng float64) []ocean.EcoregionID`, `(*fakeOcean).points() [][2]float64`, `fakePlaces.nearest func(lat, lng float64) (places.Place, bool)`.

- [ ] **Step 1: Extend the fakes**

In `internal/web/fakes_test.go`:
- Add `"sync"` to the imports.
- Add fields to `fakeClimate`:
  ```go
	// cells, when set, holds the only cells with normals, and Normals
	// applies the real nearest-land fallback (climate.PickNearest) to them.
	cells map[int]climate.MonthlyNormals
	mu    sync.Mutex
	seen  [][2]float64 // every point Normals was asked about
  ```
- In `(*fakeClimate).Normals`, right after `f.calls.Add(1)`:
  ```go
	f.mu.Lock()
	f.seen = append(f.seen, [2]float64{lat, lng})
	f.mu.Unlock()
  ```
  After the `if f.down { ... }` block, and before `cell := climate.CellID(lat, lng)`:
  ```go
	if f.cells != nil {
		own := climate.CellID(lat, lng)
		id, ok := climate.PickNearest(lat, lng, own, f.cells)
		if !ok {
			return climate.Lookup{QueryCell: own, Cell: own}, nil
		}
		n := f.cells[id]
		return climate.Lookup{QueryCell: own, Cell: id, Normals: &n}, nil
	}
  ```
- Add:
  ```go
func (f *fakeClimate) points() [][2]float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]float64(nil), f.seen...)
}
  ```
- Delete `(*fakeClimate).ForCell`.
- Replace `fakeOcean` and its `Nearby` with:
  ```go
// fakeOcean: ecoregions 25 and 30 west of the prime meridian, unless reach
// is set.
type fakeOcean struct {
	down  bool
	reach func(lat, lng float64) []ocean.EcoregionID
	calls atomic.Int32
	mu    sync.Mutex
	seen  [][2]float64
}

func (f *fakeOcean) Nearby(_ context.Context, lat, lng float64) ([]ocean.EcoregionID, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.seen = append(f.seen, [2]float64{lat, lng})
	f.mu.Unlock()
	if f.down {
		return nil, errDown
	}
	if f.reach != nil {
		return f.reach(lat, lng), nil
	}
	if lng < 0 {
		return []ocean.EcoregionID{25, 30}, nil
	}
	return nil, nil
}

func (f *fakeOcean) points() [][2]float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]float64(nil), f.seen...)
}
  ```
- Add `nearest func(lat, lng float64) (places.Place, bool)` to `fakePlaces`. In `Nearest`, after the `down` check:
  ```go
	if f.nearest != nil {
		p, ok := f.nearest(lat, lng)
		return p, ok, nil
	}
  ```

- [ ] **Step 2: Write the failing `/ingredient` tests**

Append to `internal/web/ingredient_test.go` (add imports as needed: `"regexp"`, `"strconv"`, `"time"`, `"thymeandplace/internal/climate/climatetest"`, `"thymeandplace/internal/ocean"`):

```go
// A cell with no land row whose west and east neighbours both have normals.
// From its centre the two are tied, so the west (lower ID) wins.
var fallbackCell = climate.CellID(40.1, -8.2) // lat 40.0–40.25, lng −8.25 to −8.0

func withFallbackCells(hs *harness) {
	hs.climate.cells = map[int]climate.MonthlyNormals{fallbackCell - 1: climatetest.Temperate(), fallbackCell + 1: climatetest.Polar()}
}

var backURLRE = regexp.MustCompile(`href="/season\?lat=(-?[0-9.]+)&lng=(-?[0-9.]+)`)

// /ingredient?cell= is the query cell: it resolves with the fallback, and
// Back and the pairings keep the same cell.
func TestIngredientUsesQueryCell(t *testing.T) {
	hs := newHarness(t)
	withFallbackCells(hs)
	code, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d&date=2026-08-15", fallbackCell), true)
	if code != 200 || strings.Contains(body, views.CopyNotLocal) {
		t.Fatalf("fallback cell must use the west neighbour's normals: %d\n%s", code, body)
	}
	m := backURLRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no Back link:\n%s", body)
	}
	lat, _ := strconv.ParseFloat(m[1], 64)
	lng, _ := strconv.ParseFloat(m[2], 64)
	if climate.CellID(lat, lng) != fallbackCell {
		t.Fatalf("Back goes to cell %d, want %d", climate.CellID(lat, lng), fallbackCell)
	}
	if !strings.Contains(body, fmt.Sprintf("/ingredient/basil?cell=%d", fallbackCell)) {
		t.Fatalf("pairing must carry the query cell:\n%s", body)
	}
	lat0, lng0 := climate.CellCenter(fallbackCell)
	for _, p := range append(hs.climate.points(), hs.ocean.points()...) {
		if p != [2]float64{lat0, lng0} {
			t.Fatalf("lookup at %v, want the cell centre (%v, %v)", p, lat0, lng0)
		}
	}
}

// Links issued before this change carry the normals cell, a land cell,
// which resolves to itself: they render exactly as before.
func TestIngredientOldLandCellLink(t *testing.T) {
	hs := newHarness(t)
	withFallbackCells(hs)
	west := fallbackCell - 1
	code, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d&date=2026-08-15", west), true)
	if code != 200 || strings.Contains(body, views.CopyNotLocal) || !strings.Contains(body, fmt.Sprintf("/ingredient/basil?cell=%d", west)) {
		t.Fatalf("%d\n%s", code, body)
	}
}

// With seafood off, neither page ever asks the ocean.
func TestSeafoodOffNeverQueriesOcean(t *testing.T) {
	hs := newHarness(t, seafoodOff)
	hs.get(lisbonQ+"&date=2026-08-15", true)
	hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d", climate.CellID(10, 10)), true)
	if n := hs.ocean.calls.Load(); n != 0 {
		t.Fatalf("ocean queried %d times with seafood off", n)
	}
}
```

- [ ] **Step 3: Run the tests and confirm the new ones fail**

Run: `go test ./internal/web/ -run 'TestIngredientUsesQueryCell|TestIngredientOldLandCellLink|TestSeafoodOffNeverQueriesOcean' -count=1`
Expected: FAIL to compile. `fakeClimate` no longer has `ForCell`, so it doesn't satisfy `web.Climate`.

- [ ] **Step 4: Switch the web package to the resolver**

Create `internal/web/inputs.go`:

```go
package web

import (
	"context"

	"thymeandplace/internal/cache"
	"thymeandplace/internal/cellinput"
)

// inputsFor returns the query cell's engine inputs from the per-cell cache,
// or resolves them (at the cell centre) through the DB gate and caches them.
// /season misses and /ingredient both read through it, so the list and the
// detail page always see the same inputs for a cell. Cells with no normals
// are cached too; failures are not.
func (s *Server) inputsFor(ctx context.Context, cell int, version string) (cellinput.Inputs, error) {
	key := cache.CellKey{CellID: cell, DataVersion: version}
	if in, ok := s.cells.Get(key); ok {
		return in, nil
	}
	var in cellinput.Inputs
	err := s.withDB(ctx, func(ctx context.Context) error {
		var err error
		in, err = s.inputs.Resolve(ctx, cell)
		return err
	})
	if err != nil {
		return cellinput.Inputs{}, err
	}
	s.cells.Add(key, in)
	return in, nil
}
```

In `internal/web/ingredient.go`:
- Delete the `cellInputs` type and the old `inputsFor` (lines 18-54). Remove the `cache` and `ocean` imports if nothing else uses them.
- Replace `months, basis, local := season.Year(season.Input{Normals: in.Normals, Ecoregions: in.Ecoregions, Month: month}, ing)` with `months, basis, local := season.Year(in.Engine(month), ing)`.
- Above `SetCellID(ctx, cell)`, add the comment: `// cell is the query cell of the /season page that linked here (core spec §11.1).`

In `internal/web/server.go`:
- `Climate` interface becomes:
  ```go
type Climate interface {
	Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error)
}
  ```
- In `Server`: `cells  *cache.Cache[cache.CellKey, cellinput.Inputs]` and a new field `inputs cellinput.Resolver`.
- In `New`, the struct literal uses `cells: cache.New[cache.CellKey, cellinput.Inputs](cache.CellSize)`. After the literal, add:
  ```go
	s.inputs = cellinput.Resolver{Climate: d.Climate}
	// With seafood off there is no ocean lookup: no ecoregion is ever in
	// reach, so only land cells are supported.
	if d.Seafood {
		s.inputs.Ocean = d.Ocean
	}
  ```
- Add the import `"thymeandplace/internal/cellinput"`.

In `internal/cache/cache.go:13`: `// CellSize is the capacity of the per-cell input cache (/season misses and /ingredient).`

In `internal/web/gate_test.go` (`TestIngredientInputsCachedPerCell`), replace `ForCell %d` with `Normals %d` in the four failure messages. The counts don't change: one `Normals` call per resolve.

Leave `internal/web/season.go` as it is in this task. It still calls `s.d.Climate.Normals` and `s.d.Ocean.Nearby` directly, and Task 4 moves it.

- [ ] **Step 5: Run the web tests**

Run: `go test ./internal/web/ -count=1`
Expected: PASS, including the three new tests.

- [ ] **Step 6: Commit**

```bash
git add internal/web/ internal/cache/cache.go
git commit -m "web: /ingredient resolves the query cell through cellinput (#6)"
```

### Task 4: `/season` on the resolver; the label per request

**Files:**
- Modify: `internal/web/season.go` (`seasonCore`, the miss path in `season`, `compute`, `label`, `seasonVM`)
- Test: `internal/web/season_test.go` (new tests; update `TestSeasonHungPlaceLookupFallsBack` and `TestSeasonItemLinksCarryCellAndDate`)
- Test: `internal/web/gate_test.go:63-67` (the full-gate cache hit now shows coordinates)
- Test: `internal/web/ingredient_test.go` (`TestIngredientAgreesWithSeasonList`)

**Interfaces:**
- Consumes: `(*Server).inputsFor` and `Server.inputs` (Task 3); `cellinput.Inputs.Engine`, `.Supported()` (Task 1).
- Produces:
  - `type seasonCore struct { QueryCell, NormalsCell int; HasNormals, Supported bool; Results []season.Result }`
  - `func (s *Server) compute(ctx context.Context, in cellinput.Inputs, month time.Month, snap *catalog.Snapshot) seasonCore`
  - `func (s *Server) label(ctx context.Context, placeParam string, lat, lng float64) string`

- [ ] **Step 1: Write the failing tests**

Append to `internal/web/season_test.go` (add imports: `"regexp"`, `"time"`, `"thymeandplace/internal/climate"`, `"thymeandplace/internal/climate/climatetest"`, `"thymeandplace/internal/ocean"`, `"thymeandplace/internal/places"`, skipping any already present):

```go
// Two points in fallbackCell (centre 40.125, −8.125): A in the west half,
// B in the east half. February, so the small catalog's cod (months 1–4) is
// in season wherever its ecoregion 25 is in reach.
const (
	qA = "/season?lat=40.1&lng=-8.2&date=2026-02-15"
	qB = "/season?lat=40.1&lng=-8.05&date=2026-02-15"
)

func february(hs *harness) { hs.now = time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC) }

// reachWestOf puts the edge of seafood reach at lng inside fallbackCell.
func reachWestOf(edge float64) func(float64, float64) []ocean.EcoregionID {
	return func(_, lng float64) []ocean.EcoregionID {
		if lng < edge {
			return []ocean.EcoregionID{25}
		}
		return nil
	}
}

// Each row puts a boundary between A and B, which a result computed from
// the first visitor's point would expose.
var sameCellRows = []struct {
	name  string
	setup func(*harness)
}{
	{"fallback neighbour", withFallbackCells},
	{"seafood reach", func(hs *harness) {
		hs.climate.cells = map[int]climate.MonthlyNormals{fallbackCell: climatetest.Temperate()}
		hs.ocean.reach = reachWestOf(-8.1) // centre and A in reach, B not
	}},
	{"supported", func(hs *harness) {
		hs.climate.cells = map[int]climate.MonthlyNormals{}
		hs.ocean.reach = reachWestOf(-8.1)
	}},
}

var headingRE = regexp.MustCompile(`<h2>[^<]*</h2>`)

// Spec §6: two points in one cell get identical results in either order.
func TestSeasonSameCellSameResults(t *testing.T) {
	for _, row := range sameCellRows {
		t.Run(row.name, func(t *testing.T) {
			var bodies []string
			for _, order := range [][2]string{{qA, qB}, {qB, qA}} {
				hs := newHarness(t)
				february(hs)
				row.setup(hs)
				for _, q := range order {
					code, body, _ := hs.get(q, true)
					if code != 200 {
						t.Fatalf("%s: %d", q, code)
					}
					bodies = append(bodies, headingRE.ReplaceAllString(body, ""))
				}
			}
			for i, b := range bodies[1:] {
				if b != bodies[0] {
					t.Fatalf("response %d differs from response 0:\n%s\n----\n%s", i+1, bodies[0], b)
				}
			}
		})
	}
}

// Every reference-data lookup behind a result is at the query cell's centre.
func TestSeasonLookupsUseCellCentre(t *testing.T) {
	for _, p := range [][2]float64{{40.1, -8.2}, {40.1, -8.05}, {-33.87, 151.21}, {69.65, 18.96}, {0.01, -179.99}} {
		hs := newHarness(t)
		if code, _, _ := hs.get(fmt.Sprintf("/season?lat=%v&lng=%v&date=2026-08-15", p[0], p[1]), true); code != 200 {
			t.Fatalf("%v: %d", p, code)
		}
		lat, lng := climate.CellCenter(climate.CellID(p[0], p[1]))
		for _, got := range append(hs.climate.points(), hs.ocean.points()...) {
			if got != [2]float64{lat, lng} {
				t.Fatalf("point %v: lookup at %v, want centre (%v, %v)", p, got, lat, lng)
			}
		}
	}
}

// The heading is this request's nearest place, never the first visitor's.
func TestSeasonLabelPerRequest(t *testing.T) {
	hs := newHarness(t)
	x := places.Place{ID: 7, Name: "Xville", Country: "PT", Lat: 40.1, Lng: -8.2}
	y := places.Place{ID: 8, Name: "Yton", Country: "PT", Lat: 40.1, Lng: -8.05}
	hs.places.nearest = func(lat, lng float64) (places.Place, bool) {
		p := y
		if lng < -8.125 {
			p = x
		}
		p.DistanceKm = climate.DistanceKm(lat, lng, p.Lat, p.Lng)
		return p, true
	}
	if _, body, _ := hs.get("/season?lat=40.1&lng=-8.2&date=2026-08-15", true); !strings.Contains(body, "near Xville, PT") {
		t.Fatalf("A:\n%s", body)
	}
	_, body, _ := hs.get("/season?lat=40.1&lng=-8.05&date=2026-08-15", true)
	if !strings.Contains(body, "near Yton, PT") || strings.Contains(body, "Xville") {
		t.Fatalf("B must get its own label:\n%s", body)
	}
	hs.places.down = true
	code, body, _ := hs.get("/season?lat=40.1&lng=-8.05&date=2026-08-15", true)
	if code != 200 || !strings.Contains(body, "40.10°N, 8.05°W") || !strings.Contains(body, "Tomato") {
		t.Fatalf("places down on a cache hit: %d\n%s", code, body)
	}
}
```

Replace the body of `TestSeasonItemLinksCarryCellAndDate` with:

```go
	hs := newHarness(t)
	_, body, _ := hs.get(lisbonQ+"&date=2026-08-15", true)
	if !strings.Contains(body, fmt.Sprintf("/ingredient/tomato?cell=%d&date=2026-08-15", lisbonCell)) {
		t.Fatalf("item link:\n%s", body)
	}
	// A cell that falls back still links its own (query) cell.
	hs = newHarness(t)
	withFallbackCells(hs)
	_, body, _ = hs.get("/season?lat=40.1&lng=-8.2&date=2026-08-15", true)
	if !strings.Contains(body, fmt.Sprintf("?cell=%d&", fallbackCell)) || strings.Contains(body, fmt.Sprintf("?cell=%d&", fallbackCell-1)) {
		t.Fatalf("fallback item link must carry the query cell:\n%s", body)
	}
```

In `TestSeasonHungPlaceLookupFallsBack`, replace `!strings.Contains(body, "near Lisbon, PT")` with `!strings.Contains(body, "38.72°N, 9.14°W")`. The shared 300 ms budget runs out inside `ByID`, so `Nearest` is never tried.

In `internal/web/gate_test.go`, replace the "cache hit while the gate is full" check with:

```go
	// A warmed cell is still served. Its label needs the gate too, so it
	// falls back to coordinates instead of failing the request.
	code, body, _ = hs.get(lisbonQ+"&date=2026-08-15", true)
	if code != 200 || !strings.Contains(body, "Tomato") || !strings.Contains(body, "38.72°N, 9.14°W") {
		t.Fatalf("cache hit while the gate is full: %d\n%s", code, body)
	}
```

Append to `internal/web/ingredient_test.go`:

```go
var listItemRE = regexp.MustCompile(`<li class="item ([^"]+)">\s*<a href="([^"]+)"`)

// Spec §6: every item on /season opens a detail page that agrees with it:
// never "not local", and the current month has the list's status.
func TestIngredientAgreesWithSeasonList(t *testing.T) {
	rows := []struct {
		name  string
		setup func(*harness)
	}{
		{"fallback neighbour", withFallbackCells},
		{"reach edge between A and the centre", func(hs *harness) {
			hs.climate.cells = map[int]climate.MonthlyNormals{fallbackCell: climatetest.Temperate()}
			hs.ocean.reach = reachWestOf(-8.15) // A in reach, centre and B not
		}},
	}
	for _, row := range rows {
		for _, q := range []string{qA, qB} {
			t.Run(row.name+" "+q, func(t *testing.T) {
				hs := newHarness(t)
				february(hs)
				row.setup(hs)
				_, list, _ := hs.get(q, true)
				items := listItemRE.FindAllStringSubmatch(list, -1)
				if len(items) == 0 {
					t.Fatalf("no items:\n%s", list)
				}
				for _, it := range items {
					status, link := it[1], it[2]
					code, detail, _ := hs.get(link, true)
					if code != 200 || strings.Contains(detail, views.CopyNotLocal) || !strings.Contains(detail, `class="m `+status+` now"`) {
						t.Fatalf("%s (list status %s): %d\n%s", link, status, code, detail)
					}
				}
			})
		}
	}
}
```

- [ ] **Step 2: Run the tests and confirm the new ones fail**

Run: `go test ./internal/web/ -run 'TestSeasonSameCellSameResults|TestSeasonLookupsUseCellCentre|TestSeasonLabelPerRequest|TestSeasonItemLinksCarryCellAndDate|TestSeasonHungPlaceLookupFallsBack|TestDBGateShedsWhenFull|TestIngredientAgreesWithSeasonList' -count=1 -v`
Expected: FAIL. `/season` still looks up at the exact point, caches the first visitor's label and links the normals cell. If `TestIngredientAgreesWithSeasonList` fails only on its regexp ("no items"), print one `/season` body and fix the pattern to match the generated `<li class="item ...">` markup before going on.

- [ ] **Step 3: Implement**

In `internal/web/season.go`, replace `seasonCore` with:

```go
// seasonCore is the cached part of /season. It depends only on its key
// (query cell, month, data version): every input comes from the query cell
// (internal/cellinput), never from the request's exact point. The heading
// label is not part of it; it is looked up per request.
type seasonCore struct {
	QueryCell   int
	NormalsCell int
	HasNormals  bool
	Supported   bool
	Results     []season.Result
}
```

Replace the `if !hit { ... }` block in `season()` with:

```go
	if !hit {
		in, err := s.inputsFor(ctx, queryCell, version)
		if errors.Is(err, errBusy) {
			s.overloaded(w, r, "Seasons", coords(lat, lng), r.URL.RequestURI())
			return
		}
		if err != nil {
			s.d.Log.Warn("season lookup failed", "err", err, "request_id", RequestIDFrom(ctx), "cell_id", queryCell)
			s.render(w, r, http.StatusServiceUnavailable, "Seasons", "", nil, views.LoadError(coords(lat, lng), r.URL.RequestURI()))
			return
		}
		core = s.compute(ctx, in, month, snap)
		s.cache.Add(key, core)
	}
```

If the retry-URL plan (#15) has already landed, keep its `retry` variable in place of `r.URL.RequestURI()` in these two lines.

Replace `compute` with:

```go
// compute runs the engine on a cell's inputs. It makes no lookups of its
// own, so the result is a function of the cache key.
func (s *Server) compute(ctx context.Context, in cellinput.Inputs, month time.Month, snap *catalog.Snapshot) seasonCore {
	results := s.d.Adjuster.Adjust(ctx, in.NormalsCell, season.Estimate(in.Engine(month), snap))
	return seasonCore{
		QueryCell: in.QueryCell, NormalsCell: in.NormalsCell,
		HasNormals: in.Normals != nil, Supported: in.Supported(), Results: results,
	}
}
```

Replace `label` (keep the `placeLookupTimeout` const and its comment) with:

```go
// label names the requested point: the ?place= selection if it is within
// nearKm of the point, else the nearest place within nearKm ("near X"), else
// coordinates. It runs per request and is never cached. Both lookups share
// one placeLookupTimeout and one DB gate slot. A full gate, a hung query or
// an outage falls back to coordinates and never fails the request.
func (s *Server) label(ctx context.Context, placeParam string, lat, lng float64) string {
	pctx, cancel := context.WithTimeout(ctx, placeLookupTimeout)
	defer cancel()
	name := ""
	_ = s.withDB(pctx, func(ctx context.Context) error {
		if id, err := strconv.ParseInt(placeParam, 10, 32); err == nil {
			// Only a place near the requested point may label it (a crafted
			// or mismatched URL must not call mid-ocean results "Lisbon").
			if p, ok, err := s.d.Places.ByID(ctx, int32(id)); err == nil && ok && climate.DistanceKm(lat, lng, p.Lat, p.Lng) <= nearKm {
				name = p.Label()
				return nil
			}
		}
		if ctx.Err() != nil { // the shared budget is spent; don't start another query
			return nil
		}
		if p, ok, err := s.d.Places.Nearest(ctx, lat, lng); err == nil && ok && p.DistanceKm <= nearKm {
			name = "near " + p.Label()
		}
		return nil
	})
	if name == "" {
		return coords(lat, lng)
	}
	return name
}
```

In `seasonVM`, call `s.label(ctx, placeParam, lat, lng)` and build item URLs with `ingredientURL(res.Slug, core.QueryCell, date)`.

Fix the imports: add `"thymeandplace/internal/cellinput"`, and remove `"thymeandplace/internal/ocean"` and `"thymeandplace/internal/places"` if they are now unused. `go build ./...` will say.

- [ ] **Step 4: Run the web tests**

Run: `go test ./internal/web/ -count=1`
Expected: PASS. `TestSeasonWarmCacheSurvivesOutage` passes unchanged. It asserts results, not the heading.

- [ ] **Step 5: Commit**

```bash
git add internal/web/
git commit -m "web: /season caches only cell-derived results; label per request (#6)"
```

### Task 5: Calibration resolves fixtures like the public site; delete `ForCell`

**Files:**
- Modify: `internal/calibrun/cases.go`
- Test: `internal/calibrun/cases_test.go` (add `TestCasesResolveAtCellCentre`)
- Modify: `internal/climate/service.go:78-93` (delete `ForCell`)
- Modify: `internal/climate/service_test.go:69-75` (delete the `ForCell` assertions)

**Interfaces:**
- Consumes: `cellinput.Resolver`, `cellinput.NormalsFinder`, `cellinput.EcoregionFinder`.
- Produces: `calibrun.NormalsFinder = cellinput.NormalsFinder`, `calibrun.EcoregionFinder = cellinput.EcoregionFinder` (type aliases). `Cases` keeps its signature, so `cmd/server/app.go` doesn't change.

- [ ] **Step 1: Write the failing test**

Append to `internal/calibrun/cases_test.go`:

```go
type centreProbe struct{ got [][2]float64 }

func (c *centreProbe) Normals(_ context.Context, lat, lng float64) (climate.Lookup, error) {
	c.got = append(c.got, [2]float64{lat, lng})
	return climate.Lookup{QueryCell: climate.CellID(lat, lng), Cell: climate.CellID(lat, lng)}, nil
}

func (c *centreProbe) Nearby(_ context.Context, lat, lng float64) ([]ocean.EcoregionID, error) {
	c.got = append(c.got, [2]float64{lat, lng})
	return nil, nil
}

// Spec §4.5: each fixture's point selects its cell, and inputs are resolved
// at the cell centre exactly as /season does.
func TestCasesResolveAtCellCentre(t *testing.T) {
	fx := []calibration.Fixture{{ID: "a", Name: "A", Lat: 40.1, Lng: -8.2, Source: "s"}, {ID: "b", Name: "B", Lat: -33.87, Lng: 151.21, Source: "s"}}
	p := &centreProbe{}
	if _, err := Cases(context.Background(), fx, p, p); err != nil {
		t.Fatal(err)
	}
	if len(p.got) != 4 {
		t.Fatalf("lookups: %v", p.got)
	}
	for i, f := range fx {
		lat, lng := climate.CellCenter(climate.CellID(f.Lat, f.Lng))
		for _, got := range p.got[2*i : 2*i+2] {
			if got != [2]float64{lat, lng} {
				t.Fatalf("fixture %s looked up at %v, want (%v, %v)", f.ID, got, lat, lng)
			}
		}
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/calibrun/ -run TestCasesResolveAtCellCentre -count=1`
Expected: FAIL. The lookups are at `(40.1, -8.2)`, not the centre.

- [ ] **Step 3: Implement**

Replace `internal/calibrun/cases.go` from the `NormalsFinder` declaration to the end of the file with:

```go
type (
	NormalsFinder   = cellinput.NormalsFinder
	EcoregionFinder = cellinput.EcoregionFinder
)

// Cases resolves every fixture to an engine input (once per call). Each
// fixture's point selects its cell, and the inputs are resolved at the cell
// centre, exactly as /season does (core spec §13.2). The month is left unset.
func Cases(ctx context.Context, fixtures []calibration.Fixture, cl NormalsFinder, oc EcoregionFinder) ([]calibration.Case, error) {
	r := cellinput.Resolver{Climate: cl, Ocean: oc}
	out := make([]calibration.Case, 0, len(fixtures))
	for _, f := range fixtures {
		in, err := r.Resolve(ctx, climate.CellID(f.Lat, f.Lng))
		if err != nil {
			return nil, fmt.Errorf("fixture %s: %w", f.ID, err)
		}
		out = append(out, calibration.Case{Fixture: f, Input: in.Engine(0)})
	}
	return out, nil
}
```

Update the imports: add `"thymeandplace/internal/cellinput"`, and drop `"thymeandplace/internal/ocean"` and `"thymeandplace/internal/season"` if unused.

Delete `(*Service).ForCell` from `internal/climate/service.go`, and the `n, err := svc.ForCell(...)` block through the `ForCell(absent)` check in `internal/climate/service_test.go`.

- [ ] **Step 4: Run the affected packages**

Run: `go build ./... && go test ./internal/calibrun/ ./internal/climate/ ./internal/changeset/ ./cmd/server/ -count=1`
Expected: PASS. `TestCases` passes unchanged: Lisbon, Madrid and the mid-Atlantic point all resolve their own cells.

- [ ] **Step 5: Commit**

```bash
git add internal/calibrun/ internal/climate/
git commit -m "calibrun: resolve fixtures at their cell centre; drop climate.ForCell (#6)"
```

### Task 6: Baseline check and documentation

**Files:**
- Modify: `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (§4.2, §4.3, §6.1, §6.2, §11.1, §11.2, §11.3, §11.6, §13.2, §13.7 per spec §4.7)
- Modify: `docs/operations.md` ("Health and logs")
- Modify: `docs/calibration-baseline.md` ("How this was measured")
- Modify: `internal/web/views/about.templ:18` (then `go tool templ generate`)

**Interfaces:** documentation only.

- [ ] **Step 1: Measure the calibration score before and after (spec §5 step 7)**

On the dev stack with the baseline reference data (`docs/calibration-baseline.md`, "How this was measured"), score the fixtures once on the commit before Task 1 and once on this branch. Call `calibrun.Drift.RunOnce` directly from the throwaway test or script below. Don't restart the app to trigger a run: once the drift-schedule plan (#23, `2026-09-28-scheduled-jobs-retry.md`) has landed, the in-app check runs 24 h after the last recorded run, not at startup. Record each fixture's `QueryCell` and `NormalsCell` with a throwaway test or script that calls `cellinput.Resolver.Resolve` for each fixture in `calibration/fixtures/*.json`. Don't commit it.
Expected: `NormalsCell == QueryCell` for all 15 fixtures, and a score of 0.671 over 528 pairs both times. If any fixture falls back or the score moves, stop and report the numbers before going on.

- [ ] **Step 2: Apply the core spec text changes**

Make exactly the edits in the spec's §4.7 table. For example, §6.1's fallback sentence becomes "…use the one whose centre is nearest **the centre of the query cell** (ties to the lower cell ID)". §11.3 gains "The cached value depends only on its key. No lookup behind it uses the request's exact coordinates". §11.6 gains the row "DB unavailable on cache hit / 200 / Results as normal; the heading shows coordinates".

- [ ] **Step 3: Operations and baseline docs**

Append this bullet to "## Health and logs" in `docs/operations.md`:

```markdown
- A logged `cell_id`, with the month and the live data version, determines everything a cook saw except the heading. To reproduce it, open `/season?lat=<90 − (⌊id/1440⌋ + 0.5) × 0.25>&lng=<−180 + (id mod 1440 + 0.5) × 0.25>`, or `/ingredient/{slug}?cell=<id>`.
```

Append to "## How this was measured" in `docs/calibration-baseline.md` (fill in the scores from Step 1):

```markdown
- As of the season-consistency change (#6), fixtures resolve at their cell centre, exactly as `/season` does. Score before: <score> over <pairs> pairs; after: <score> over <pairs> pairs.
```

- [ ] **Step 4: About copy (spec §8 question 2, recommended wording)**

In `internal/web/views/about.templ:18`, replace `marine regions within 100 km of the coast` with `marine regions within about 100 km of the grid cell around your location`. Then run `go tool templ generate && go test ./internal/web/... -count=1`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add docs/ internal/web/views/about.templ internal/web/views/about_templ.go
git commit -m "docs: results depend only on the query cell (#6)"
```

### Task 7: Whole-repo check

- [ ] **Step 1:** Run `go vet ./... && go test ./... -count=1`. Expected: PASS.
- [ ] **Step 2:** Run `./e2e/run.sh`. Expected: PASS, including "warm /season responds within 1 s", which now includes the per-request `Nearest` query.
- [ ] **Step 3: After the production deploy (spec §7), confirm the score held.**
  - If the drift-schedule plan (#23) has landed, the deploy doesn't trigger a check. Within 24 h of the last run, `SELECT ran_at, score, data_version FROM calibration_run ORDER BY id DESC LIMIT 2` shows the next scheduled run. Its score must be within 0.02 of the previous one, with no `calibration score dropped` warning in `docker compose logs app`.
  - If #23 hasn't landed, the startup run gives the same answer at once.
  - A drop means a fixture cell now falls back. Investigate it before making catalog edits.
