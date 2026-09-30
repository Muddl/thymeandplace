# Thyme & Place - Season result consistency and cache keys - Design

- **Status:** Proposed
- **Date:** 2026-09-27
- **Source:** Architectural audit, 2026-09-27 (finding F06)
- **Related issues:** [#6](https://github.com/Muddl/thymeandplace/issues/6) (F06)
- **Amends:** core spec §4.2, §4.3, §6.1, §6.2, §11.1, §11.2, §11.3, §11.6, §13.2, §13.7 (text only; no §16 value changes)

---

## 1. Problem

`/season` caches one value per `(query cell, month, data version)`, but it builds that value from the exact coordinates of whichever request missed the cache. Every later request in the same ~28 km cell gets the first visitor's result until the data version changes or the entry is evicted.

Evidence at HEAD (`22befca`):

- **Key vs. inputs.** `internal/web/season.go:105` computes `queryCell`, `:117` builds `cache.Key{CellID: queryCell, Month: month, DataVersion: version}`, `:121` calls `compute(ctx, lat, lng, month, snap)` with the exact point, and `:127` stores the result under the cell key. The key type (`internal/cache/cache.go:12-18`) holds only the cell, month and version.
- **Point-dependent inputs inside the cached value** (`compute`, `season.go:137-155`):
  - *Normals fallback.* `:138` calls `climate.Service.Normals` (`internal/climate/service.go:56-76`), which calls `PickNearest` (`:21-39`). When the query cell has no row, `PickNearest` takes the neighbour nearest the query point (`:33`), so two points in one cell can get different neighbours' normals.
  - *Ocean reach.* `:142` calls `Ocean.Nearby(lat, lng)`, which runs `ST_DWithin` from the exact point (`internal/store/handwritten.go:54-57`). Two points in one cell can be a cell diagonal apart (~39 km at the equator), so one can be within 100 km of an ecoregion when the other is not.
  - *`Supported`* (`:149`) depends on both, so a whole cell can be cached as supported or unsupported because of one point.
  - *The "near X" label.* `:151-153` calls `Places.Nearest(lat, lng)` with the 50 km cut-off and stores the result in `seasonCore.NearLabel` (`:31`). `label()` serves it to every later request in the cell that has no usable `?place=` (`:161-176`, fallback at `:172-174`). Geolocated requests never carry `?place=` (`static/app.js:38-39`), so in a cell that holds several towns, cooks in town B are told they are "near A".
- **Which point wins.** The first miss after a restart, an eviction or a version change. With concurrent misses, whichever `Add` runs last. Logs record only `cell_id` (core spec §14, §15), so a report like "my neighbour sees a different list" can't be reproduced.
- **List and detail disagree.** Item links carry `cell=core.Cell` (`season.go:197`). That is the *normals* cell, which can be a neighbour of the query cell. `/ingredient` reads that cell's normals with no fallback (`internal/web/ingredient.go:36`) and measures reach from its centre (`:42-43`). A seafood item listed on `/season`, where reach was measured from the point, can show "Not grown or landed near here" (`internal/web/views/ingredient.templ:16`) on its own detail page. Pairing links (`ingredient.go:64-68`) reuse the same cell.
- **Back moves the cook.** `ingredient.go:56` links back to the centre of the normals cell. After a fallback, that centre is in a different query cell, so "Back" can show a different list from the one the cook came from.
- **Calibration is a third variant.** `internal/calibrun/cases.go:27` and `:31` resolve fixtures from the exact fixture point. That matches neither what `/season` caches nor what `/ingredient` computes.

The core spec contradicts itself here. §6.1 measures the fallback from "the query point" and §6.2 measures reach from "point", but §11.2 and §11.3 cache on `(cell_id, month, dataVersion)`. A cache keyed by cell can't hold a value defined per point. §11.2 step 4 doesn't say whether the label is cached.

How often it happens: the label affects every geolocated request in a cell with more than one town. Reach affects places roughly 80–120 km from an ecoregion. The fallback affects only query cells with no land data. The transform keeps every 0.25° cell with at least one land pixel (`pipeline/transform/transform.py:66`, `:89`), so these are open-water cells next to a coast.

## 2. Goals and non-goals

Goals:

1. The cached `/season` value depends only on its key, plus the snapshot and reference data that the data version names. Two requests in the same cell get the same results, in any order and under any concurrency.
2. For a given cell, the `/season` list, the `/ingredient` detail page, its pairings and its "Back" link agree.
3. Calibration and the admin preview resolve fixture locations the same way public pages do.
4. The heading label stays specific to the request.
5. The cache key, cache size and hit rate don't change. A warm result is still served during a database outage.

Non-goals:

- Changing the grid resolution, the 100 km seafood reach (§16), or the 50 km "near" distance (`season.go:22`).
- Canonical URLs, such as rounding or redirecting `/season` coordinates to the cell. `static/app.js:39` keeps 2 decimals and search links keep 4 (`internal/web/places.go:11-13`). The URL, not the device position, picks the cell. That was already true.
- Carrying `?place=` through ingredient links and "Back".
- Calling `Adjuster` on `/ingredient`. It is a no-op in core. When sub-project 2 adds corrections, it must apply them on both paths, using the `NormalsCell` from §4.2.
- The ingest spot checks (`internal/ingest/climate.go:139`, `internal/ingest/ocean.go:260-266`). They validate data at fixed reference points and aren't public output.
- Adding a cache for `/ingredient`. The capacity design (`2026-09-27-db-capacity-and-load-protection-design.md` §4.4, "S1") adds one, keyed by cell and data version. This design keeps it valid, because `/ingredient`'s inputs stay a function of the cell. Whichever design lands second switches that cache's value to `cellinput.Inputs`, and `/season` misses then read through it too.
- DB load control. S1's public DB gate also covers the per-request label lookup in §4.3. When the gate is full, the label falls back to coordinates and the request still succeeds.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| What the cached value is computed from | **(a)** Every reference-data lookup behind it uses the query cell's centre: the normals fallback, ocean reach, and so `Supported` | **(b) Key on a rounded point:** at 0.01° that is up to ~625 keys per cell, so the 50,000-entry LRU covers far fewer places and the hit rate drops. `/ingredient` would need the point in its URL, and results would still differ within a cell. **(c) Cache land results per cell and query ocean reach from the exact point on every request:** adds a query to every hit, and a warm hit during an outage would lose seafood or fail. `/ingredient` would still need the point, and the fallback would stay point-dependent |
| Place label | Per request, from the exact point, outside the cache, under one 300 ms budget. Coordinates on failure | **Cache a label computed from the cell centre:** no query on a hit, but it names the town nearest a grid point. That town can be up to ~70 km (50 km + half a cell diagonal) from the cook. **Keep caching the first visitor's label:** that is the bug |
| Where the rule lives | New package `internal/cellinput`, used by `/season`, `/ingredient` and `calibrun` | **Each caller converts to the centre itself:** three copies of one rule is how three variants arose. **Cell-ID methods on `climate.Service` and `ocean.Service`:** splits the rule across two packages and still leaves each caller to combine them |
| Cell carried in ingredient links | The query cell | **The normals cell (current):** the detail page can't reproduce the list's ocean reach or `Supported` |
| Normals on `/ingredient` | Resolve with fallback from the query cell | **`ForCell` with no fallback (current):** only correct when given the normals cell. Old links still work under the new rule, because a land cell resolves to itself |
| Fallback tie-break | Keep "lower cell ID" (`service.go:34`) | Ties are now routine. From a centre, the east and west neighbours are exactly equidistant, and so are north and south. Centres are exact multiples of 0.125°, so the float64 distances compare equal, except across the antimeridian, where the result is still deterministic. **Averaging tied neighbours:** changes normals values and calibration, so it is out of scope |
| Cell passed to `Adjuster.Adjust` | Keep the normals cell (`season.go:147`) | **The query cell:** also a function of the key, but the normals cell is where the estimate's data came from, and §12.1 stays unchanged |
| Calibration inputs | Resolve each fixture through `cellinput` from the fixture's cell | **Keep the exact point:** the score and preview bars would describe inputs no public page uses |
| Concurrent misses | No coalescing | **`singleflight`:** two concurrent computes now produce the same value, so a duplicate costs two small queries and nothing else |

## 4. Design

### 4.1 Rule

A public result for a location depends only on the 0.25° cell that contains it. Every reference-data lookup that feeds results uses that cell's centre. Only the heading label uses the exact requested point, and it's never cached.

### 4.2 `internal/cellinput`

```go
// Inputs are the engine inputs for one query cell. Every field is a function
// of the cell and the live reference data, never of a point inside the cell.
type Inputs struct {
	QueryCell   int
	NormalsCell int                     // QueryCell, or the neighbour the fallback chose
	Normals     *climate.MonthlyNormals // nil when no land cell is within reach
	Ecoregions  []ocean.EcoregionID     // ascending; empty inland
}

func (in Inputs) Supported() bool                  { return in.Normals != nil || len(in.Ecoregions) > 0 }
func (in Inputs) Engine(m time.Month) season.Input { return season.Input{Normals: in.Normals, Ecoregions: in.Ecoregions, Month: m} }

type NormalsFinder interface { Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error) }
type EcoregionFinder interface { Nearby(ctx context.Context, lat, lng float64) ([]ocean.EcoregionID, error) }

type Resolver struct {
	Climate NormalsFinder
	Ocean   EcoregionFinder
}

// Resolve returns an error for an invalid cell ID without querying.
func (r Resolver) Resolve(ctx context.Context, cell int) (Inputs, error)
```

`Resolve` gets the centre from `climate.CellCenter(cell)` and passes it to the existing `Normals` and `Nearby`. It returns `Inputs{QueryCell: cell, NormalsCell: l.Cell, …}`. Neither service needs a new signature. `CellID(CellCenter(id)) == id` holds for every cell, because centres sit mid-cell on exact binary fractions. `TestCellCenterRoundTrip` (`internal/climate/grid_test.go:59-70`) pins it at the grid corners. So `own` in `Normals` is the query cell, and `PickNearest` measures from its centre. The package imports `climate`, `ocean` and `season` for types only and does no I/O of its own. The identical interfaces in `internal/calibrun/cases.go:15-21` become aliases of these.

### 4.3 `/season`

- `seasonCore` becomes `{QueryCell, NormalsCell int; HasNormals, Supported bool; Results []season.Result}`. `NearLabel` is removed.
- `compute(ctx, cell, month, snap)` calls `in, err := s.inputs.Resolve(ctx, cell)`, then `s.d.Adjuster.Adjust(ctx, in.NormalsCell, season.Estimate(in.Engine(month), snap))`. The cache key and the version-before-snapshot ordering (`season.go:108-112`) don't change.
- Item links use `ingredientURL(slug, core.QueryCell, date)`.
- The label runs per request, only for supported results (as today, `season.go:129-133`), under **one** `placeLookupTimeout` (300 ms) context:

```go
func (s *Server) label(ctx context.Context, placeParam string, lat, lng float64) string {
	pctx, cancel := context.WithTimeout(ctx, placeLookupTimeout)
	defer cancel()
	if id, err := strconv.ParseInt(placeParam, 10, 32); err == nil {
		if p, ok, err := s.d.Places.ByID(pctx, int32(id)); err == nil && ok && climate.DistanceKm(lat, lng, p.Lat, p.Lng) <= nearKm {
			return p.Label()
		}
	}
	if pctx.Err() == nil {
		if p, ok, err := s.d.Places.Nearest(pctx, lat, lng); err == nil && ok && p.DistanceKm <= nearKm {
			return "near " + p.Label()
		}
	}
	return coords(lat, lng)
}
```

The shared budget caps the label at 300 ms even when `ByID` hangs. The explicit `pctx.Err()` check keeps that independent of the driver and of test fakes. Context7's pgx docs say a cancelled context returns at once, but the design doesn't depend on it. `NearestPlace` (`handwritten.go:122-139`) orders by `geom <-> point` on the GiST-indexed geography column (core spec §6.3). PostGIS docs (via Context7) confirm that `<->` in `ORDER BY` is an index-assisted nearest-neighbour search that uses spherical distance for geography. The query already runs on every miss today. It isn't benchmarked on production data. The Playwright check "warm /season responds within 1 s" (`e2e/tests/public.spec.ts:20-27`) covers it on the e2e stack, because that request has no `?place=`.

### 4.4 `/ingredient`

- The `cell` parameter means the query cell. The `ValidCellID` check stays (`ingredient.go:27-31`).
- `in, err := s.inputs.Resolve(ctx, cell)` replaces `ForCell` plus `Nearby` (`:36-48`). `season.Year(in.Engine(month), ing)` replaces `:49`.
- `BackURL` stays `CellCenter(cell)` (`:56`). Because `cell` is now the query cell, "Back" returns to the same cache key.
- Pairing links (`:64-68`) keep passing `cell`, which is now the query cell.
- Compatibility: links issued before this change carry the normals cell, which is a land cell whenever normals existed. A land cell resolves to itself, and reach from its centre is what `/ingredient` already computed, so old links render exactly as before.
- `ForCell` is removed from the web `Climate` interface (`internal/web/server.go:26-29`). `climate.Service.ForCell` (`service.go:78-93`) has no other caller and is deleted, together with its assertions in `internal/climate/service_test.go:69-75`.

### 4.5 Calibration and admin preview

`calibrun.Cases` resolves each fixture with `cellinput.Resolver{Climate: cl, Ocean: oc}.Resolve(ctx, climate.CellID(f.Lat, f.Lng))` and uses `in.Engine(0)`, which leaves the month unset as today. Its signature doesn't change, so the wiring in `cmd/server/app.go:135-137` doesn't either.

Expected effect on the score: none.

- `PickNearest` returns the own cell whenever it has a row (`service.go:24-26`), whatever the reference point. Fixture cities are on land, so their cells are expected to have rows under the any-land-pixel mask. **Not verified against the live database.** Step 7 of §5 checks it.
- Ecoregions feed only seafood (`internal/season/rules.go:16-17`), and no fixture lists a seafood slug. Checked: `content/catalog/launch.json` has 21 seafood slugs, and none appears in `calibration/fixtures/*.json`. `calibration.Score` scores only the slugs a fixture lists (`internal/calibration/calibration.go:94-140`). `docs/calibration-baseline.md` also records "Seafood is not scored".

The admin preview's season bars (`internal/changeset/preview.go:60-69`) use `Case.Input.Ecoregions`, so **seafood** bars may change for a fixture 80–120 km from an ecoregion. Santiago, about 100 km inland, is the only likely case, and no catalog species uses its ecoregion yet (`docs/calibration-baseline.md`, "Seafood coverage gaps"). This changes the display only, not the score.

### 4.6 What changes for cooks

| Input | Before | After | Largest difference |
|---|---|---|---|
| Normals fallback | Neighbour nearest the first visitor's point | Neighbour nearest the cell centre; exact ties go to the lower ID | One neighbour cell, only in cells with no land data |
| Seafood reach | 100 km from the first visitor's point | 100 km from the cell centre | Half a cell diagonal (≤ ~20 km). A point can be in reach at roughly 80–120 km from an ecoregion |
| `Supported` | From the first visitor's point | From the cell centre | As above |
| Heading label | The first visitor's nearest place, shown to the whole cell | This request's nearest place; coordinates on failure | None; this is the fix. During an outage, a warm hit shows coordinates instead of a stale name |
| Ingredient links, "Back" | Normals cell | Query cell | The detail page now matches the list |

### 4.7 Core spec text changes

| § | Change |
|---|---|
| §4.2, §4.3 | Add `cellinput/`: "query cell → engine inputs (normals with fallback, nearby ecoregions), looked up at the cell centre". Depends on `climate`, `ocean`, `season` types |
| §6.1 | Fallback: "…use the one whose centre is nearest **the centre of the query cell** (ties to the lower cell ID)" |
| §6.2 | "Nearby means `ST_DWithin(geom, centre, 100000)`, where centre is the centre of the query cell" |
| §11.1 | `/ingredient/{slug}?cell=`: "`cell` is the query cell of the `/season` page that linked it" |
| §11.2 | Step 3: "resolve the query cell's inputs at its centre (`cellinput`)…". Step 4: "Place label, per request and never cached: the search selection if it is within 50 km of the point, else `places.Nearest` of the requested point within 50 km, else coordinates; one 300 ms budget" |
| §11.3 | Add: "The cached value depends only on its key. No lookup behind it uses the request's exact coordinates" |
| §11.6 | New row: "DB unavailable on cache hit / 200 / Results as normal; the heading shows coordinates" |
| §13.2 | Add: "Each fixture's point selects its cell. Inputs are resolved exactly as for `/season`" |
| §13.7 | Add: "Two points in one cell get identical results in either request order" |

## 5. Implementation outline

1. `internal/cellinput/cellinput.go` and `cellinput_test.go` (unit tests with fakes, plus `dbtest` integration tests in the same file, as in `internal/climate/service_test.go`).
2. `internal/web/server.go`: add `inputs cellinput.Resolver` to `Server`, built in `New` from `d.Climate` and `d.Ocean`. Drop `ForCell` from `Climate`.
3. `internal/web/season.go`: the new `seasonCore`, `compute` taking the cell, `label()` as in §4.3, and links using `QueryCell`.
4. `internal/web/ingredient.go`: use `Resolve` and remove the `ForCell`/`Nearby` pair.
5. `internal/web/fakes_test.go`: the fakes record every coordinate they receive. `fakeClimate` takes an optional `cells map[int]climate.MonthlyNormals` and, when it is set, answers through the real `climate.PickNearest`. `fakeOcean` takes an optional `reach func(lat, lng float64) []ocean.EcoregionID`, and `fakePlaces` an optional `nearest func(lat, lng float64) (places.Place, bool)`. Remove the `ForCell` fake.
6. `internal/calibrun/cases.go`: use `Resolver`. `internal/climate/service.go`: delete `ForCell`, and add to the `PickNearest` comment that public callers pass the query cell's centre.
7. On the dev stack with the baseline data (`docs/calibration-baseline.md`, "How this was measured"), run `calibrun.Cases` before and after the change. Log each fixture's `QueryCell` and `NormalsCell`, and the score. Expected: `NormalsCell == QueryCell` for all 15 fixtures, and a score of 0.671 over 528 pairs.
8. Docs: the core spec edits (§4.7), `docs/operations.md` and `docs/calibration-baseline.md` (§7), and the About copy per open question 2.

## 6. Testing

- **`cellinput` unit (table-driven, fakes):** `Resolve` passes exactly `CellCenter(cell)` to both finders, for cells in row 0, row 719, column 0 and column 1439. `QueryCell` and `NormalsCell` propagate. A truth table for `Supported`. Finder errors are wrapped and returned. An invalid cell ID returns an error and makes no calls.
- **`cellinput` integration (testcontainers, `dbtest.LoadReference`):** Lisbon's cell resolves to its own normals and ecoregion 30 (its centre is ~24 km from the test Iberian Shelf edge). `FallbackPoint`'s cell resolves to `NormalsCell == CellID(Lisbon)` (`internal/devdata/reference.go:21-23`). The MidAtlantic cell is unsupported.
- **`climate`:** `TestPickNearest` (`service_test.go:19-42`) gains cases with the reference point at the cell centre. With east and west neighbours both present, west (the lower ID) wins. With north and south only, north wins. This makes the tie-break part of the contract.
- **`web` `/season` (httptest):**
  - `TestSeasonSameCellSameResults`: a table of point pairs A and B in one cell (for example lat 40.1, lng −8.2 and −8.05, in the cell spanning −8.25 to −8.0 with centre −8.125). Each row puts a fake boundary between A and B: ocean reach, fallback choice (west-half vs east-half point with different E/W neighbour normals), or supported vs unsupported (no normals, reach boundary inside the cell). Run A→B and B→A in fresh harnesses. The results region, meaning the body without its `<h2>` heading, must be identical in all four responses. This test fails at HEAD.
  - `TestSeasonLookupsUseCellCentre`: every coordinate the fakes saw in `Normals` and `Nearby` equals `CellCenter(queryCell)`.
  - `TestSeasonLabelPerRequest`: warm the cell with point A (nearest place X), then request B in the same cell (nearest Y). The heading is "near Y". With places down on a cache hit, the heading is B's coordinates, the status is 200, and the results render.
  - `TestSeasonHungPlaceLookupFallsBack` (`season_test.go:182-192`) now expects the coordinate label `38.72°N, 9.14°W`, because the shared budget runs out inside `ByID`.
  - `TestSeasonItemLinksCarryCellAndDate` (`season_test.go:161-167`) asserts `cell=` equals `CellID(point)`, including for a fallback cell whose normals cell differs.
  - `TestSeasonWarmCacheSurvivesOutage` (`season_test.go:127-142`) stays unchanged and must pass.
- **`web` `/ingredient`:**
  - `TestIngredientAgreesWithSeasonList`: for every point in the same-cell table, follow every item link on its `/season` response. The detail page never says "Not grown or landed near here", and its current-month bar has the list item's status. The reach row puts the boundary between A and the centre, so this fails at HEAD.
  - `TestIngredientOldLandCellLink`: `cell` set to a land normals cell renders that cell's normals and reach from its centre, as today.
  - `CellID` of the Back URL equals the `cell` parameter, and pairing links carry the same cell.
- **`calibrun`:** the existing `TestCases` (`internal/calibrun/cases_test.go:13-35`) passes unchanged. A new fake-finder test, `TestCasesResolveAtCellCentre`, needs no DB and asserts that each fixture is looked up at its cell centre.
- **Playwright:** no new test. Fakes can put a boundary inside a cell, but the e2e reference data can't without new fixtures. The existing "warm /season responds within 1 s" and "search → select → results render" tests cover the per-request label.

## 7. Rollout, operations and rollback

- **Deploy.** Code only: no migration, config or data change. Deploy as in `docs/operations.md`, "Updating the app". The LRU is in-process, so the restart drops every entry computed under the old rule, and old and new entries never mix.
- **Check after deploy.** The drift check runs once at startup (`cmd/server/app.go:183`, `internal/calibrun/drift.go:59-65`), and a code deploy doesn't change the data version. Confirm that the `calibration drift check` log line shows the previous score and that there is no `calibration score dropped` warning. A drop would mean some fixture cell falls back, and it needs investigating before new catalog edits. Once #23 lands, the drift check runs 24 h after the last recorded run instead of at startup, so read the next scheduled run (plan Task 7 Step 3).
- **`docs/operations.md`, "Health and logs":** add that a logged `cell_id`, with the month and the live data version, now determines everything a cook saw except the heading. To reproduce it, open `/season?lat=<90 − (⌊id/1440⌋ + 0.5) × 0.25>&lng=<−180 + (id mod 1440 + 0.5) × 0.25>`, or `/ingredient/{slug}?cell=<id>`.
- **`docs/calibration-baseline.md`, "How this was measured":** add one line saying that fixtures resolve at their cell centre as of the implementing commit, with the before and after scores from step 7.
- **Rollback.** Redeploy the previous image. Links issued by the new version carry the query cell. The old `/ingredient` reads that cell with no fallback, so in the rare query cells without land data, land items show "Not grown or landed near here" until the cook goes back to `/season`. Links for land cells are unaffected. There is no data to restore.

## 8. Open questions for the owner

1. **Label source.** Should the "near X" heading name the place nearest the cook's point, at the cost of one indexed query per request and a coordinate heading during a database outage? Or the place nearest the cell centre, which is cached and needs no query on a hit, but sometimes names a neighbouring town up to ~70 km away? **Recommended: the cook's point** (§4.3).
2. **About page copy.** `internal/web/views/about.templ:14` says "marine regions within 100 km of the coast". In practice reach is measured from the location, and after this change from its grid cell. **Recommended:** "marine regions within about 100 km of the grid cell around your location", which matches the wording at `about.templ:7`.
