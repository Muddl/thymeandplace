# Thyme & Place — Plan 3: Reference Data Lookups and Ingestion Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Look up climate normals (with nearest-land fallback), nearby marine ecoregions and places from the live reference tables. Build the versioned `ingest` pipeline (fetch → transform → load → validate → swap, plus rollback) for GeoNames places, MEOW ecoregions and TerraClimate normals, including the Python + xarray climate transform.

**Architecture:** `climate`, `ocean` and `places` each expose a small `Service` over the live views. Queries that need PostGIS or `pg_trgm`, or that target a versioned table by name, are hand-written in `internal/store/handwritten.go` and take a table name, so services (views) and validation (new `*_vN` tables) share them. `internal/ingest` owns the pipeline: a `Blob` store for raw and transformed files, `dataset_versions` bookkeeping, and one `Dataset` implementation per source. The climate transform runs in its own Python container against a shared `/work` volume; the Go pipeline detects its output and uploads it.

**Tech Stack:** Go ≥ 1.25, pgx v5, sqlc, `github.com/minio/minio-go/v7`, Python 3.12 + xarray + dask + netCDF4, pytest.

**Spec:** `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (§5, §6, §7, §11.2 steps 3–4, §13.5, §13.6)

**Depends on:** Plan 1 (`climate` grid, `ocean.EcoregionID`), Plan 2 (`dbtest`, `store`, reference schema, `seafood_region`).

## Global Constraints

- Nearest-land fallback: if the query cell has no row, use the neighbour (of 8) whose centre is nearest the query point; if none exist, there are no normals (§6.1).
- "Nearby" ecoregion: `ST_DWithin(geom, point, 100000)` (100 km) (§6.2).
- Place search: top **8** by trigram similarity, ties broken by population (§6.3). Search considers `ascii_name` and `name`.
- Ingestion CLI: `ingest <dataset> fetch|transform|load|validate|swap|all [--version N]`, `ingest <dataset> rollback`, and `ingest <dataset> approve-license --version N --yes`. Datasets: `climate`, `ocean`, `places`.
- Validation failures exit non-zero and name the dataset and step; live data is unchanged (§7, §11.6).
- Row count must be within ±5% of the previous live version (skipped on the first load). Climate plausibility: tmin/tmax in −80…60 °C, tmin ≤ tmax, precipitation ≥ 0. At least 10 spot-check coordinates per dataset, across both hemispheres, the tropics and high latitudes.
- No version can go live unless `license_reviewed = true`. A new fetch inherits the review only when the license text **and** source URLs are identical to an already reviewed version.
- Ocean validation: every `seafood_region.ecoregion_id` in the catalog must exist in the new version.
- Swap is one transaction: repoint the view, move `is_live`, set `promoted_at`, `NOTIFY data_version_changed 'dataset'`. Rollback returns to the most recent earlier version that was ever promoted.
- `ecoregion_id` = MEOW field `ECO_CODE_X` (1–232). Source: the Esri Oceans MEOW feature service, queried as GeoJSON (`ingest.DefaultOceanURL`; see Task 5).
- Places source: GeoNames `cities1000.zip` + `admin1CodesASCII.txt`, license CC BY 4.0. Climate: TerraClimate 1991–2020 climatology files `TerraClimate_19912020_{tmin,tmax,ppt}.nc` from `http://thredds.northwestknowledge.net:8080/thredds/fileServer/TERRACLIMATE_ALL/climatology/` (verified 2026-09-25; each is 90–150 MB; 12 time steps; `lat` 4320 descending from 89.979, `lon` 8640 from −179.979; the `time` axis is non-CF: units `months`, calendar `months`), CC0. Ocean: MEOW via Esri Oceans; the item records no licence, so a human must confirm terms at `approve-license`.
- Blob keys: `raw/<dataset>/v<N>/<file>` and `transformed/<dataset>/v<N>/data.csv`. Work dir layout: `<INGEST_WORKDIR>/<dataset>/v<N>/{raw/,out/,data.csv}`.
- Environment: `DATABASE_URL`, `INGEST_WORKDIR` (default `/work`), either `BLOB_DIR` or `BUCKET_ENDPOINT` + `BUCKET_NAME` + `BUCKET_ACCESS_KEY` + `BUCKET_SECRET_KEY` (+ `BUCKET_USE_SSL`, default `true`); optional `OCEAN_SOURCE_URL`, `CLIMATE_SOURCE_BASE`.
- Commit after every task. Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Accented or misspelled city names** ("Zürich", "zurich", "lisbn") must find the city. Pinned in Task 2.
2. **A coastal point whose own 0.25° cell is ocean** must use the nearest land neighbour. A mid-ocean point gets no normals, and neither case raises an error. Pinned in Task 1.
3. **Search input that is empty, whitespace, 5,000 characters long, or full of SQL/`LIKE` metacharacters** (`%`, `_`, `'`, `\`) returns results or nothing, never an error or an injection. Pinned in Task 2.
4. **Re-running a pipeline step** (transform or load twice) is idempotent, and reloading the live version is refused rather than dropping the table under the site. Pinned in Tasks 3 and 4.
5. **A swap whose validation fails** leaves the view and `is_live` untouched, and the CLI exits non-zero with the dataset and step in the message. Pinned in Task 4.

---

## File Structure

```
internal/store/handwritten.go        # PostGIS / pg_trgm / dynamic-table queries
internal/store/queries/ingest.sql    # dataset_versions queries (sqlc)
internal/dbtest/reference.go         # LoadReference: small cells, 3 ecoregions, ~50 places
internal/climate/service.go          # Service, Lookup, PickNearest, DistanceKm
internal/climate/service_test.go
internal/ocean/service.go            # Service: Nearby, Missing
internal/ocean/service_test.go
internal/places/places.go            # Place, Service: Search, Nearest, ByID
internal/places/places_test.go
internal/ingest/blob.go              # Blob, FSBlob, S3Blob
internal/ingest/pipeline.go          # Dataset, Pipeline, errors
internal/ingest/places.go            # GeoNames dataset
internal/ingest/ocean.go             # MEOW dataset
internal/ingest/climate.go           # TerraClimate dataset (Go side)
internal/ingest/*_test.go
cmd/ingest/main.go                   # CLI
pipeline/transform/transform.py
pipeline/transform/requirements.txt
pipeline/transform/requirements-dev.txt
pipeline/transform/Dockerfile
pipeline/tests/test_transform.py
```

---

### Task 1: Reference test fixtures and climate lookup

**Files:**
- Create: `internal/store/handwritten.go`, `internal/dbtest/reference.go`, `internal/climate/service.go`, `internal/climate/service_test.go`

**Interfaces:**
- Consumes: `climate.CellID`, `climate.NeighborCells`, `climate.CellCenter`, `climate.MonthlyNormals` (Plan 1); `store.DBTX`, `dbtest.New` (Plan 2).
- Produces:
  - `func store.ValidIdent(name string) bool` (`^[a-z_][a-z0-9_]*$`)
  - `type store.CellRow struct { CellID int32; TminC, TmaxC, PrecipMM []float32 }`
  - `func store.CellsIn(ctx context.Context, db DBTX, table string, ids []int32) ([]CellRow, error)`
  - `func climate.DistanceKm(lat1, lng1, lat2, lng2 float64) float64` (haversine, R = 6371.0088 km)
  - `func climate.PickNearest(lat, lng float64, own int, cells map[int]MonthlyNormals) (int, bool)`
  - `type climate.Lookup struct { QueryCell, Cell int; Normals *MonthlyNormals }`
  - `func climate.NewService(db store.DBTX) *Service`; `(*Service).Normals(ctx, lat, lng float64) (Lookup, error)`; `(*Service).ForCell(ctx, id int) (*MonthlyNormals, error)` (nil, nil when absent)
  - `func climate.CellsToNormals(rows []store.CellRow) (map[int]MonthlyNormals, error)`
  - `dbtest.Point{Lat, Lng float64}` and points `dbtest.Lisbon`, `Madrid`, `Tromso`, `Sydney`, `MidAtlantic`, `FallbackPoint`; `func dbtest.LoadReference(t testing.TB, pool *pgxpool.Pool)`. Fixture ecoregions: `30` (off Lisbon), `25` (North Sea), `204` (off Sydney). Fixture places include Lisbon PT, Porto PT, Madrid ES, Tromsø NO, Sydney AU, London GB, London CA, Paris FR, Paris US, Zürich CH (ascii `Zurich`), São Paulo BR (ascii `Sao Paulo`), plus 39 filler places named `Filler NN`.

- [ ] **Step 1: Write the hand-written query file**

Create `internal/store/handwritten.go`:

```go
package store

// Hand-written queries. sqlc cannot model PostGIS or pg_trgm functions, and
// validation must query versioned tables by name. Table names are checked
// against ValidIdent; every value is a bind parameter.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var identRE = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// ValidIdent reports whether name is a safe unquoted SQL identifier.
func ValidIdent(name string) bool { return identRE.MatchString(name) }

func withTable(sql, table string) (string, error) {
	if !ValidIdent(table) {
		return "", fmt.Errorf("invalid table name %q", table)
	}
	return strings.ReplaceAll(sql, "{table}", table), nil
}

type CellRow struct {
	CellID   int32
	TminC    []float32
	TmaxC    []float32
	PrecipMM []float32
}

func CellsIn(ctx context.Context, db DBTX, table string, ids []int32) ([]CellRow, error) {
	sql, err := withTable(`SELECT cell_id, tmin_c, tmax_c, precip_mm FROM {table} WHERE cell_id = ANY($1)`, table)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, sql, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CellRow
	for rows.Next() {
		var r CellRow
		if err := rows.Scan(&r.CellID, &r.TminC, &r.TmaxC, &r.PrecipMM); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 2: Write the reference fixture loader**

Create `internal/dbtest/reference.go`:

```go
package dbtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/climate"
)

type Point struct{ Lat, Lng float64 }

var (
	Lisbon      = Point{38.7167, -9.1333}
	Madrid      = Point{40.4165, -3.7026}
	Tromso      = Point{69.6496, 18.9560}
	Sydney      = Point{-33.8679, 151.2073}
	MidAtlantic = Point{0, -30}
	// FallbackPoint is the centre of the cell just west of Lisbon's cell.
	// That cell has no row, so lookups must fall back to Lisbon's cell.
	FallbackPoint = Point{38.625, -9.375}
)

type cellFixture struct {
	at                 Point
	tmin, tmax, precip [12]float32
}

var cells = []cellFixture{
	{Lisbon,
		[12]float32{8.3, 9.1, 10.9, 12.0, 14.1, 16.9, 18.3, 18.8, 17.9, 15.2, 11.8, 9.6},
		[12]float32{14.8, 16.2, 18.9, 20.3, 23.0, 26.6, 28.7, 29.0, 26.9, 22.8, 18.3, 15.5},
		[12]float32{100, 90, 60, 70, 50, 15, 5, 5, 30, 100, 110, 125}},
	{Madrid,
		[12]float32{2.7, 3.7, 5.6, 7.2, 10.7, 15.1, 18.1, 17.9, 14.6, 10.5, 6.1, 3.5},
		[12]float32{9.8, 12.0, 16.3, 18.2, 22.2, 28.2, 32.1, 31.3, 26.4, 19.8, 14.0, 10.4},
		[12]float32{33, 35, 25, 45, 50, 20, 10, 10, 25, 60, 50, 55}},
	{Tromso,
		[12]float32{-6.5, -6.7, -5.5, -2.5, 1.5, 5.8, 9.0, 8.4, 4.6, 0.5, -3.3, -5.3},
		[12]float32{-1.6, -1.6, 0.2, 3.3, 7.9, 12.5, 15.8, 14.6, 10.0, 4.6, 1.3, -0.6},
		[12]float32{110, 95, 90, 65, 50, 55, 75, 85, 105, 130, 110, 115}},
	{Sydney,
		[12]float32{18.7, 18.8, 17.6, 14.7, 11.5, 9.3, 8.1, 9.0, 11.1, 13.6, 15.6, 17.5},
		[12]float32{25.9, 25.8, 24.7, 22.4, 19.4, 16.9, 16.3, 17.8, 20.0, 22.1, 23.6, 25.2},
		[12]float32{95, 120, 130, 125, 120, 130, 100, 80, 70, 75, 85, 75}},
}

var ecoregions = []struct {
	id   int32
	name string
	wkt  string
}{
	{30, "Test Iberian Shelf", "MULTIPOLYGON(((-10.5 36.5,-9.4 36.5,-9.4 43.5,-10.5 43.5,-10.5 36.5)))"},
	{25, "Test North Sea", "MULTIPOLYGON(((0 51,8 51,8 58,0 58,0 51)))"},
	{204, "Test Tasman Coast", "MULTIPOLYGON(((151.4 -35,153 -35,153 -32,151.4 -32,151.4 -35)))"},
}

type placeFixture struct {
	id                  int32
	name, ascii, admin1 string
	country             string
	pop                 int32
	at                  Point
}

var namedPlaces = []placeFixture{
	{1, "Lisbon", "Lisbon", "Lisbon", "PT", 506654, Point{38.7167, -9.1333}},
	{2, "Porto", "Porto", "Porto", "PT", 249633, Point{41.1496, -8.611}},
	{3, "Madrid", "Madrid", "Madrid", "ES", 3255944, Point{40.4165, -3.7026}},
	{4, "Tromsø", "Tromso", "Troms", "NO", 38980, Point{69.6496, 18.9560}},
	{5, "Sydney", "Sydney", "New South Wales", "AU", 4627345, Point{-33.8679, 151.2073}},
	{6, "London", "London", "England", "GB", 8961989, Point{51.5085, -0.1257}},
	{7, "London", "London", "Ontario", "CA", 346765, Point{42.9834, -81.2330}},
	{8, "Paris", "Paris", "Île-de-France", "FR", 2138551, Point{48.8534, 2.3488}},
	{9, "Paris", "Paris", "Texas", "US", 24171, Point{33.6609, -95.5555}},
	{10, "Zürich", "Zurich", "Zurich", "CH", 341730, Point{47.3667, 8.55}},
	{11, "São Paulo", "Sao Paulo", "São Paulo", "BR", 10021295, Point{-23.5475, -46.6361}},
}

// LoadReference inserts the small reference fixture into the *_v0 tables
// behind the live views.
func LoadReference(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, c := range cells {
		_, err := pool.Exec(ctx, `INSERT INTO climate_cell_v0 VALUES ($1,$2,$3,$4)`,
			climate.CellID(c.at.Lat, c.at.Lng), c.tmin[:], c.tmax[:], c.precip[:])
		if err != nil {
			t.Fatalf("cell: %v", err)
		}
	}
	for _, e := range ecoregions {
		if _, err := pool.Exec(ctx, `INSERT INTO ocean_ecoregion_v0 VALUES ($1,$2,ST_GeogFromText($3))`, e.id, e.name, e.wkt); err != nil {
			t.Fatalf("ecoregion: %v", err)
		}
	}
	all := append([]placeFixture{}, namedPlaces...)
	for i := 0; i < 39; i++ {
		all = append(all, placeFixture{
			id: int32(100 + i), name: fmt.Sprintf("Filler %02d", i), ascii: fmt.Sprintf("Filler %02d", i),
			country: "ZZ", pop: int32(1000 + i), at: Point{-60 + float64(i)*3, -170 + float64(i)*8},
		})
	}
	for _, p := range all {
		var admin1 any
		if p.admin1 != "" {
			admin1 = p.admin1
		}
		_, err := pool.Exec(ctx, `INSERT INTO place_v0 VALUES ($1,$2,$3,$4,$5,$6, ST_SetSRID(ST_MakePoint($8,$7),4326)::geography)`,
			p.id, p.name, p.ascii, admin1, p.country, p.pop, p.at.Lat, p.at.Lng)
		if err != nil {
			t.Fatalf("place %s: %v", p.name, err)
		}
	}
}
```

- [ ] **Step 3: Write the failing climate tests**

Create `internal/climate/service_test.go`:

```go
package climate_test

import (
	"context"
	"math"
	"testing"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/dbtest"
)

func TestDistanceKm(t *testing.T) {
	// London → Paris ≈ 344 km.
	if d := climate.DistanceKm(51.5074, -0.1278, 48.8566, 2.3522); math.Abs(d-343.5) > 2 {
		t.Fatalf("London–Paris = %.1f km", d)
	}
}

func TestPickNearest(t *testing.T) {
	own := climate.CellID(10, 10)
	var n climate.MonthlyNormals
	cells := map[int]climate.MonthlyNormals{own: n}
	if id, ok := climate.PickNearest(10, 10, own, cells); !ok || id != own {
		t.Fatal("own cell must win when present")
	}
	nb := climate.NeighborCells(own)
	// Point in the east part of its cell: the east neighbour is nearer than the west.
	lat, lng := climate.CellCenter(own)
	lng += 0.1
	east, west := own+1, own-1
	cells = map[int]climate.MonthlyNormals{east: n, west: n}
	if id, ok := climate.PickNearest(lat, lng, own, cells); !ok || id != east {
		t.Fatalf("got %d, want east %d (neighbours %v)", id, east, nb)
	}
	if _, ok := climate.PickNearest(lat, lng, own, map[int]climate.MonthlyNormals{}); ok {
		t.Fatal("no cells → not ok")
	}
	// A far cell that is not a neighbour must be ignored.
	if _, ok := climate.PickNearest(lat, lng, own, map[int]climate.MonthlyNormals{own + 5: n}); ok {
		t.Fatal("non-neighbour used")
	}
}

func TestServiceNormals(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	svc := climate.NewService(pool)

	l, err := svc.Normals(ctx, dbtest.Lisbon.Lat, dbtest.Lisbon.Lng)
	if err != nil || l.Normals == nil || l.Cell != l.QueryCell {
		t.Fatalf("lisbon: %+v %v", l, err)
	}
	if math.Abs(l.Normals.TminC[0]-8.3) > 1e-4 {
		t.Fatalf("tmin Jan = %v", l.Normals.TminC[0])
	}

	// Review Focus 2: coastal cell with no row falls back to the land neighbour.
	fb, err := svc.Normals(ctx, dbtest.FallbackPoint.Lat, dbtest.FallbackPoint.Lng)
	if err != nil || fb.Normals == nil || fb.Cell != l.Cell || fb.QueryCell == fb.Cell {
		t.Fatalf("fallback: %+v %v", fb, err)
	}

	sea, err := svc.Normals(ctx, dbtest.MidAtlantic.Lat, dbtest.MidAtlantic.Lng)
	if err != nil || sea.Normals != nil {
		t.Fatalf("mid-ocean: %+v %v", sea, err)
	}

	n, err := svc.ForCell(ctx, l.Cell)
	if err != nil || n == nil {
		t.Fatalf("ForCell: %v %v", n, err)
	}
	if n, err := svc.ForCell(ctx, sea.QueryCell); err != nil || n != nil {
		t.Fatalf("ForCell(absent) = %v %v", n, err)
	}
}
```

- [ ] **Step 4: Run to verify failure**

Run: `go test ./internal/climate/`
Expected: FAIL to compile: `undefined: climate.DistanceKm`.

- [ ] **Step 5: Implement the climate service**

Create `internal/climate/service.go`:

```go
package climate

import (
	"context"
	"fmt"
	"math"

	"thymeandplace/internal/store"
)

const earthRadiusKm = 6371.0088

// DistanceKm is the great-circle distance between two points.
func DistanceKm(lat1, lng1, lat2, lng2 float64) float64 {
	rad := math.Pi / 180
	dLat, dLng := (lat2-lat1)*rad, (lng2-lng1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKm * math.Asin(math.Min(1, math.Sqrt(a)))
}

// PickNearest returns own if it has data, otherwise the neighbour of own
// whose centre is nearest (lat, lng). Ties go to the lower cell ID.
func PickNearest(lat, lng float64, own int, cells map[int]MonthlyNormals) (int, bool) {
	if _, ok := cells[own]; ok {
		return own, true
	}
	best, bestD := -1, math.Inf(1)
	for _, id := range NeighborCells(own) {
		if _, ok := cells[id]; !ok {
			continue
		}
		clat, clng := CellCenter(id)
		d := DistanceKm(lat, lng, clat, clng)
		if d < bestD || (d == bestD && id < best) {
			best, bestD = id, d
		}
	}
	return best, best >= 0
}

// Lookup is the result of a normals lookup for one point.
type Lookup struct {
	QueryCell int             // cell containing the point
	Cell      int             // cell whose normals were used
	Normals   *MonthlyNormals // nil when no land cell is within reach
}

type Service struct{ db store.DBTX }

func NewService(db store.DBTX) *Service { return &Service{db: db} }

const liveView = "climate_cell"

// Normals looks up the point's cell and its neighbours in one query and
// applies the nearest-land fallback.
func (s *Service) Normals(ctx context.Context, lat, lng float64) (Lookup, error) {
	own := CellID(lat, lng)
	ids := []int32{int32(own)}
	for _, id := range NeighborCells(own) {
		ids = append(ids, int32(id))
	}
	rows, err := store.CellsIn(ctx, s.db, liveView, ids)
	if err != nil {
		return Lookup{}, fmt.Errorf("climate lookup: %w", err)
	}
	cells, err := CellsToNormals(rows)
	if err != nil {
		return Lookup{}, err
	}
	id, ok := PickNearest(lat, lng, own, cells)
	if !ok {
		return Lookup{QueryCell: own, Cell: own}, nil
	}
	n := cells[id]
	return Lookup{QueryCell: own, Cell: id, Normals: &n}, nil
}

// ForCell returns the normals stored for exactly one cell, or nil.
func (s *Service) ForCell(ctx context.Context, id int) (*MonthlyNormals, error) {
	rows, err := store.CellsIn(ctx, s.db, liveView, []int32{int32(id)})
	if err != nil {
		return nil, fmt.Errorf("climate cell: %w", err)
	}
	cells, err := CellsToNormals(rows)
	if err != nil {
		return nil, err
	}
	n, ok := cells[id]
	if !ok {
		return nil, nil
	}
	return &n, nil
}

// CellsToNormals converts rows, rejecting arrays that are not 12 long.
func CellsToNormals(rows []store.CellRow) (map[int]MonthlyNormals, error) {
	out := make(map[int]MonthlyNormals, len(rows))
	for _, r := range rows {
		if len(r.TminC) != 12 || len(r.TmaxC) != 12 || len(r.PrecipMM) != 12 {
			return nil, fmt.Errorf("cell %d: arrays must have 12 values", r.CellID)
		}
		var n MonthlyNormals
		for m := 0; m < 12; m++ {
			n.TminC[m], n.TmaxC[m], n.PrecipMM[m] = float64(r.TminC[m]), float64(r.TmaxC[m]), float64(r.PrecipMM[m])
		}
		out[int(r.CellID)] = n
	}
	return out, nil
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/climate/ -count=1`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/store/handwritten.go internal/dbtest/reference.go internal/climate
git commit -m "feat(climate): normals lookup with nearest-land fallback; reference fixtures

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Ocean and places lookups

**Files:**
- Modify: `internal/store/handwritten.go`, `internal/store/queries/reference.sql`
- Create: `internal/ocean/service.go`, `internal/ocean/service_test.go`, `internal/places/places.go`, `internal/places/places_test.go`

**Interfaces:**
- Consumes: `store.DBTX`, `withTable`, `dbtest.LoadReference`.
- Produces:
  - `func store.NearbyEcoregions(ctx, db DBTX, table string, lat, lng, meters float64) ([]int32, error)`
  - `type store.PlaceRow struct { PlaceID int32; Name string; Admin1 *string; Country string; Population int32; Lat, Lng, DistanceM float64 }`
  - `func store.SearchPlaces(ctx, db DBTX, table, q string, limit int) ([]PlaceRow, error)`
  - `func store.NearestPlace(ctx, db DBTX, table string, lat, lng float64) (PlaceRow, bool, error)`
  - `func store.PlaceByID(ctx, db DBTX, table string, id int32) (PlaceRow, bool, error)`
  - sqlc `(*store.Queries).ExistingEcoregions(ctx, ids []int32) ([]int32, error)`
  - `const ocean.ReachMeters = 100_000`; `func ocean.NewService(db store.DBTX) *Service`; `(*Service).Nearby(ctx, lat, lng float64) ([]EcoregionID, error)`; `(*Service).Missing(ctx, ids []EcoregionID) ([]EcoregionID, error)`
  - `type places.Place struct { ID int32; Name, Admin1, Country string; Population int32; Lat, Lng, DistanceKm float64 }` with `Label() string` (`"Lisbon, PT"`; `"London, England, GB"` when `Admin1` differs from `Name`)
  - `const places.SearchLimit = 8`, `const places.MaxQueryRunes = 100`
  - `func places.NewService(db store.DBTX) *Service`; `(*Service).Search(ctx, q string) ([]Place, error)`; `(*Service).Nearest(ctx, lat, lng float64) (Place, bool, error)`; `(*Service).ByID(ctx, id int32) (Place, bool, error)`

- [ ] **Step 1: Add the queries**

Append to `internal/store/handwritten.go`:

```go
func NearbyEcoregions(ctx context.Context, db DBTX, table string, lat, lng, meters float64) ([]int32, error) {
	sql, err := withTable(`SELECT ecoregion_id FROM {table}
		WHERE ST_DWithin(geom, ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography, $3)
		ORDER BY ecoregion_id`, table)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, sql, lat, lng, meters)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type PlaceRow struct {
	PlaceID    int32
	Name       string
	Admin1     *string
	Country    string
	Population int32
	Lat, Lng   float64
	DistanceM  float64
}

const placeCols = `place_id, name, admin1, country, population, ST_Y(geom::geometry), ST_X(geom::geometry)`

func scanPlaces(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) ([]PlaceRow, error) {
	defer rows.Close()
	var out []PlaceRow
	for rows.Next() {
		var p PlaceRow
		if err := rows.Scan(&p.PlaceID, &p.Name, &p.Admin1, &p.Country, &p.Population, &p.Lat, &p.Lng, &p.DistanceM); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func SearchPlaces(ctx context.Context, db DBTX, table, q string, limit int) ([]PlaceRow, error) {
	sql, err := withTable(`SELECT `+placeCols+`, 0::float8 FROM {table}
		WHERE ascii_name % $1 OR name % $1
		ORDER BY greatest(similarity(ascii_name, $1), similarity(name, $1)) DESC, population DESC, place_id
		LIMIT $2`, table)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, sql, q, limit)
	if err != nil {
		return nil, err
	}
	return scanPlaces(rows)
}

func NearestPlace(ctx context.Context, db DBTX, table string, lat, lng float64) (PlaceRow, bool, error) {
	sql, err := withTable(`SELECT `+placeCols+`,
		ST_Distance(geom, ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography) FROM {table}
		ORDER BY geom <-> ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography LIMIT 1`, table)
	if err != nil {
		return PlaceRow{}, false, err
	}
	rows, err := db.Query(ctx, sql, lat, lng)
	if err != nil {
		return PlaceRow{}, false, err
	}
	ps, err := scanPlaces(rows)
	if err != nil || len(ps) == 0 {
		return PlaceRow{}, false, err
	}
	return ps[0], true, nil
}

func PlaceByID(ctx context.Context, db DBTX, table string, id int32) (PlaceRow, bool, error) {
	sql, err := withTable(`SELECT `+placeCols+`, 0::float8 FROM {table} WHERE place_id = $1`, table)
	if err != nil {
		return PlaceRow{}, false, err
	}
	rows, err := db.Query(ctx, sql, id)
	if err != nil {
		return PlaceRow{}, false, err
	}
	ps, err := scanPlaces(rows)
	if err != nil || len(ps) == 0 {
		return PlaceRow{}, false, err
	}
	return ps[0], true, nil
}
```

Append to `internal/store/queries/reference.sql`:

```sql
-- name: ExistingEcoregions :many
SELECT ecoregion_id FROM ocean_ecoregion WHERE ecoregion_id = ANY(@ids::int[]) ORDER BY ecoregion_id;
```

Run: `./scripts/sqlc.sh generate && go build ./internal/store/`
Expected: compiles; `ExistingEcoregions(ctx, ids []int32) ([]int32, error)` exists.

- [ ] **Step 2: Write the failing tests**

Create `internal/ocean/service_test.go`:

```go
package ocean_test

import (
	"context"
	"reflect"
	"testing"

	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/ocean"
)

func TestNearby(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	svc := ocean.NewService(pool)

	cases := []struct {
		name     string
		lat, lng float64
		want     []ocean.EcoregionID
	}{
		{"lisbon coast", dbtest.Lisbon.Lat, dbtest.Lisbon.Lng, []ocean.EcoregionID{30}},
		{"sydney coast", dbtest.Sydney.Lat, dbtest.Sydney.Lng, []ocean.EcoregionID{204}},
		{"madrid inland", dbtest.Madrid.Lat, dbtest.Madrid.Lng, nil},
		// East edge of ecoregion 30 is lng -9.4. At lat 40, 1° lng ≈ 85.40 km.
		{"97 km inside reach", 40, -9.4 + 97/85.40, []ocean.EcoregionID{30}},
		{"103 km out of reach", 40, -9.4 + 103/85.40, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := svc.Nearby(ctx, c.lat, c.lng)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v %v, want %v", got, err, c.want)
			}
		})
	}
}

func TestMissing(t *testing.T) {
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	got, err := ocean.NewService(pool).Missing(context.Background(), []ocean.EcoregionID{25, 999, 30, 7})
	if err != nil || !reflect.DeepEqual(got, []ocean.EcoregionID{7, 999}) {
		t.Fatalf("got %v %v", got, err)
	}
}
```

Create `internal/places/places_test.go`:

```go
package places_test

import (
	"context"
	"strings"
	"testing"

	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/places"
)

func svc(t *testing.T) *places.Service {
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	return places.NewService(pool)
}

func TestSearchRanking(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	got, err := s.Search(ctx, "london")
	if err != nil || len(got) < 2 || got[0].Country != "GB" || got[1].Country != "CA" {
		t.Fatalf("london: %+v %v", got, err)
	}
	got, _ = s.Search(ctx, "Paris")
	if len(got) < 2 || got[0].Country != "FR" {
		t.Fatalf("paris: %+v", got)
	}
	got, _ = s.Search(ctx, "Filler")
	if len(got) != places.SearchLimit {
		t.Fatalf("limit: got %d", len(got))
	}
}

// Review Focus 1.
func TestSearchAccentsAndTypos(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	for _, q := range []string{"Zürich", "zurich", "São Paulo", "sao paulo", "lisbn"} {
		got, err := s.Search(ctx, q)
		if err != nil || len(got) == 0 {
			t.Fatalf("%q: %v %v", q, got, err)
		}
	}
	got, _ := s.Search(ctx, "lisbn")
	if got[0].Name != "Lisbon" {
		t.Fatalf("typo top = %q", got[0].Name)
	}
}

// Review Focus 3.
func TestSearchHostileInput(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	for _, q := range []string{"", "   ", strings.Repeat("a", 5000), "%", "_", "'; DROP TABLE place; --", `\`, "%_%"} {
		if _, err := s.Search(ctx, q); err != nil {
			t.Fatalf("%q: %v", q[:min(len(q), 20)], err)
		}
	}
	if got, _ := s.Search(ctx, "  "); got != nil {
		t.Fatal("blank query must return nil")
	}
	if got, _ := s.Search(ctx, "London"); len(got) == 0 {
		t.Fatal("table damaged by hostile input")
	}
}

func TestNearestAndByID(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	p, ok, err := s.Nearest(ctx, dbtest.Lisbon.Lat+0.01, dbtest.Lisbon.Lng)
	if err != nil || !ok || p.Name != "Lisbon" || p.DistanceKm > 2 {
		t.Fatalf("nearest: %+v %v %v", p, ok, err)
	}
	if p.Label() != "Lisbon, PT" {
		t.Fatalf("label = %q", p.Label())
	}
	g, ok, _ := s.ByID(ctx, 6)
	if !ok || g.Label() != "London, England, GB" {
		t.Fatalf("byID: %+v", g)
	}
	if _, ok, err := s.ByID(ctx, 424242); ok || err != nil {
		t.Fatalf("missing ID: %v %v", ok, err)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/ocean/ ./internal/places/`
Expected: FAIL to compile: `undefined: ocean.NewService`, `undefined: places.NewService`.

- [ ] **Step 4: Implement the services**

Create `internal/ocean/service.go`:

```go
package ocean

import (
	"context"
	"fmt"
	"slices"

	"thymeandplace/internal/store"
)

// ReachMeters is how far a coast may be from an ecoregion to count as "nearby".
const ReachMeters = 100_000

type Service struct{ db store.DBTX }

func NewService(db store.DBTX) *Service { return &Service{db: db} }

// Nearby returns ecoregions within ReachMeters of the point, ascending.
func (s *Service) Nearby(ctx context.Context, lat, lng float64) ([]EcoregionID, error) {
	ids, err := store.NearbyEcoregions(ctx, s.db, "ocean_ecoregion", lat, lng, ReachMeters)
	if err != nil {
		return nil, fmt.Errorf("ocean lookup: %w", err)
	}
	var out []EcoregionID
	for _, id := range ids {
		out = append(out, EcoregionID(id))
	}
	return out, nil
}

// Missing returns the IDs that do not exist in the live ecoregion data, ascending.
func (s *Service) Missing(ctx context.Context, ids []EcoregionID) ([]EcoregionID, error) {
	req := make([]int32, len(ids))
	for i, id := range ids {
		req[i] = int32(id)
	}
	found, err := store.New(s.db).ExistingEcoregions(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ecoregion check: %w", err)
	}
	var out []EcoregionID
	for _, id := range ids {
		if !slices.Contains(found, int32(id)) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out, nil
}
```

Create `internal/places/places.go`:

```go
// Package places searches cities and finds the nearest one to a point.
package places

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"thymeandplace/internal/store"
)

const (
	SearchLimit   = 8
	MaxQueryRunes = 100
	liveView      = "place"
)

type Place struct {
	ID         int32
	Name       string
	Admin1     string
	Country    string
	Population int32
	Lat, Lng   float64
	DistanceKm float64 // set by Nearest only
}

// Label is the display name: "Lisbon, PT" or "London, England, GB".
func (p Place) Label() string {
	if p.Admin1 != "" && p.Admin1 != p.Name {
		return p.Name + ", " + p.Admin1 + ", " + p.Country
	}
	return p.Name + ", " + p.Country
}

type Service struct{ db store.DBTX }

func NewService(db store.DBTX) *Service { return &Service{db: db} }

// Search returns up to SearchLimit places. Blank queries return nil without
// touching the database; long queries are truncated to MaxQueryRunes.
func (s *Service) Search(ctx context.Context, q string) ([]Place, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(q) > MaxQueryRunes {
		q = string([]rune(q)[:MaxQueryRunes])
	}
	rows, err := store.SearchPlaces(ctx, s.db, liveView, q, SearchLimit)
	if err != nil {
		return nil, fmt.Errorf("place search: %w", err)
	}
	out := make([]Place, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

func (s *Service) Nearest(ctx context.Context, lat, lng float64) (Place, bool, error) {
	r, ok, err := store.NearestPlace(ctx, s.db, liveView, lat, lng)
	if err != nil {
		return Place{}, false, fmt.Errorf("nearest place: %w", err)
	}
	return fromRow(r), ok, nil
}

func (s *Service) ByID(ctx context.Context, id int32) (Place, bool, error) {
	r, ok, err := store.PlaceByID(ctx, s.db, liveView, id)
	if err != nil {
		return Place{}, false, fmt.Errorf("place by id: %w", err)
	}
	return fromRow(r), ok, nil
}

func fromRow(r store.PlaceRow) Place {
	p := Place{ID: r.PlaceID, Name: r.Name, Country: r.Country, Population: r.Population,
		Lat: r.Lat, Lng: r.Lng, DistanceKm: r.DistanceM / 1000}
	if r.Admin1 != nil {
		p.Admin1 = *r.Admin1
	}
	return p
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/ocean/ ./internal/places/ -count=1`
Expected: `ok` for both. If the 97 km case fails, print `ST_Distance` for the point in `psql`: the helper constant 85.40 km/° assumes the WGS84 spheroid at 40° N.

- [ ] **Step 6: Commit**

```bash
git add internal/store internal/ocean internal/places
git commit -m "feat: ocean 100 km reach lookup and trigram place search

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Ingestion framework (blob store, versions, fetch, transform, load) and the places dataset

**Files:**
- Create: `internal/store/queries/ingest.sql`, `internal/ingest/blob.go`, `internal/ingest/pipeline.go`, `internal/ingest/places.go`, `internal/ingest/testutil_test.go`, `internal/ingest/places_test.go`

**Interfaces:**
- Consumes: `store.*` (sqlc + hand-written), `dbtest.New`.
- Produces:
  - sqlc: `CreateDatasetVersion`, `SetDatasetTable`, `GetDatasetVersion`, `LiveDatasetVersion`, `PreviousPromotedVersion`, `LatestDatasetVersion`, `ApproveDatasetLicense`, `LicenseReviewedBefore`, `MarkDatasetLoaded`, `ClearLive`, `MarkLive`
  - `type ingest.Blob interface { PutFile(ctx, key, path string) error; GetFile(ctx, key, path string) error; Exists(ctx, key string) (bool, error) }`, `ingest.FSBlob{Root string}`, `func ingest.NewS3Blob(endpoint, bucket, accessKey, secretKey string, useSSL bool) (*S3Blob, error)`
  - `type ingest.Source struct { URL, File string }`
  - `type ingest.Dataset interface { Name() string; View() string; Prefix() string; License() string; Sources() []Source; Transform(ctx context.Context, versionDir string, w io.Writer) error; CreateSQL(table string) string; Load(ctx context.Context, tx pgx.Tx, table string, csv io.Reader) error; Check(ctx context.Context, db store.DBTX, table string) []string }`
  - `type ingest.Pipeline struct { DS Dataset; Pool *pgxpool.Pool; Blob Blob; WorkDir string; HTTP *http.Client; Log *slog.Logger }` with `Fetch(ctx) (int64, error)`, `Transform(ctx, v int64) error`, `Load(ctx, v int64) error`, `Latest(ctx) (int64, error)`, `ApproveLicense(ctx, v int64) (store.DatasetVersion, error)`
  - `type ingest.ExternalTransformError struct { Cmd string }`
  - `type ingest.Places struct { SourceList []Source; SpotChecks []PlaceCheck }`, `type ingest.PlaceCheck struct { Query, Country string }`, `func ingest.NewPlaces() *Places`

- [ ] **Step 1: Add dependencies and the version queries**

```bash
go get github.com/minio/minio-go/v7@latest
mkdir -p internal/ingest
```

Create `internal/store/queries/ingest.sql`:

```sql
-- name: CreateDatasetVersion :one
INSERT INTO dataset_versions (dataset, table_name, source_url, source_checksum, license, license_reviewed)
VALUES (@dataset, '', @source_url, @source_checksum, @license, @license_reviewed)
RETURNING id;

-- name: SetDatasetTable :exec
UPDATE dataset_versions SET table_name = @table_name WHERE id = @id;

-- name: GetDatasetVersion :one
SELECT * FROM dataset_versions WHERE id = @id;

-- name: LatestDatasetVersion :one
SELECT * FROM dataset_versions WHERE dataset = @dataset ORDER BY id DESC LIMIT 1;

-- name: LiveDatasetVersion :one
SELECT * FROM dataset_versions WHERE dataset = @dataset AND is_live;

-- name: PreviousPromotedVersion :one
SELECT * FROM dataset_versions
WHERE dataset = @dataset AND id < @before AND promoted_at IS NOT NULL
ORDER BY id DESC LIMIT 1;

-- name: ApproveDatasetLicense :exec
UPDATE dataset_versions SET license_reviewed = true WHERE id = @id;

-- name: LicenseReviewedBefore :one
SELECT EXISTS (
  SELECT 1 FROM dataset_versions
  WHERE dataset = @dataset AND license = @license AND source_url = @source_url AND license_reviewed
);

-- name: MarkDatasetLoaded :exec
UPDATE dataset_versions SET loaded_at = now() WHERE id = @id;

-- name: ClearLive :exec
UPDATE dataset_versions SET is_live = false WHERE dataset = @dataset AND is_live;

-- name: MarkLive :exec
UPDATE dataset_versions SET is_live = true, promoted_at = coalesce(promoted_at, now()) WHERE id = @id;
```

Run: `./scripts/sqlc.sh generate && go build ./internal/store/`
Expected: compiles. `models.go` has `DatasetVersion` with fields `ID`, `Dataset`, `TableName`, `SourceUrl`, `SourceChecksum`, `License`, `LicenseReviewed`, `LoadedAt`, `IsLive`, `PromotedAt`.

- [ ] **Step 2: Implement the blob stores**

Create `internal/ingest/blob.go`:

```go
package ingest

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Blob stores raw and transformed files under slash-separated keys.
type Blob interface {
	PutFile(ctx context.Context, key, path string) error
	GetFile(ctx context.Context, key, path string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// FSBlob keeps files under a local directory (dev and tests).
type FSBlob struct{ Root string }

func (b FSBlob) PutFile(_ context.Context, key, path string) error {
	return copyFile(path, filepath.Join(b.Root, filepath.FromSlash(key)))
}

func (b FSBlob) GetFile(_ context.Context, key, path string) error {
	return copyFile(filepath.Join(b.Root, filepath.FromSlash(key)), path)
}

func (b FSBlob) Exists(_ context.Context, key string) (bool, error) {
	_, err := os.Stat(filepath.Join(b.Root, filepath.FromSlash(key)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func copyFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// S3Blob stores files in an S3-compatible bucket.
type S3Blob struct {
	c      *minio.Client
	bucket string
}

func NewS3Blob(endpoint, bucket, accessKey, secretKey string, useSSL bool) (*S3Blob, error) {
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: useSSL})
	if err != nil {
		return nil, err
	}
	return &S3Blob{c: c, bucket: bucket}, nil
}

func (b *S3Blob) PutFile(ctx context.Context, key, path string) error {
	_, err := b.c.FPutObject(ctx, b.bucket, key, path, minio.PutObjectOptions{})
	return err
}

func (b *S3Blob) GetFile(ctx context.Context, key, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return b.c.FGetObject(ctx, b.bucket, key, path, minio.GetObjectOptions{})
}

func (b *S3Blob) Exists(ctx context.Context, key string) (bool, error) {
	_, err := b.c.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return false, nil
	}
	return false, err
}
```

- [ ] **Step 3: Implement the pipeline core (fetch, transform, load)**

Create `internal/ingest/pipeline.go`:

```go
// Package ingest implements the versioned reference-data pipeline:
// fetch → transform → load → validate → swap, plus rollback.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/store"
)

type Source struct{ URL, File string }

// Dataset is one reference source.
type Dataset interface {
	Name() string   // "climate" | "ocean" | "places"
	View() string   // live view, e.g. "place"
	Prefix() string // versioned table prefix, e.g. "place_v"
	License() string
	Sources() []Source
	// Transform reads versionDir/raw and writes load-ready CSV to w.
	Transform(ctx context.Context, versionDir string, w io.Writer) error
	CreateSQL(table string) string
	Load(ctx context.Context, tx pgx.Tx, table string, csv io.Reader) error
	// Check runs dataset-specific validation against table; each string is one failure.
	Check(ctx context.Context, db store.DBTX, table string) []string
}

// ExternalTransformError means the transform must run in another container.
type ExternalTransformError struct{ Cmd string }

func (e *ExternalTransformError) Error() string {
	return "transform output not found; run: " + e.Cmd + " — then run this step again"
}

// ValidationError lists every failed check. Live data is unchanged.
type ValidationError struct {
	Dataset  string
	Failures []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s validation failed: %s", e.Dataset, strings.Join(e.Failures, "; "))
}

type Pipeline struct {
	DS      Dataset
	Pool    *pgxpool.Pool
	Blob    Blob
	WorkDir string
	HTTP    *http.Client
	Log     *slog.Logger
}

func (p *Pipeline) q() *store.Queries { return store.New(p.Pool) }

func (p *Pipeline) versionDir(v int64) string {
	return filepath.Join(p.WorkDir, p.DS.Name(), "v"+strconv.FormatInt(v, 10))
}

func rawKey(ds string, v int64, file string) string { return fmt.Sprintf("raw/%s/v%d/%s", ds, v, file) }
func csvKey(ds string, v int64) string              { return fmt.Sprintf("transformed/%s/v%d/data.csv", ds, v) }

// version loads a dataset_versions row and checks it belongs to this dataset.
func (p *Pipeline) version(ctx context.Context, v int64) (store.DatasetVersion, error) {
	dv, err := p.q().GetDatasetVersion(ctx, v)
	if errors.Is(err, pgx.ErrNoRows) {
		return dv, fmt.Errorf("version %d does not exist", v)
	}
	if err != nil {
		return dv, err
	}
	if dv.Dataset != p.DS.Name() {
		return dv, fmt.Errorf("version %d belongs to %s, not %s", v, dv.Dataset, p.DS.Name())
	}
	return dv, nil
}

// Latest returns the newest version ID for this dataset.
func (p *Pipeline) Latest(ctx context.Context) (int64, error) {
	dv, err := p.q().LatestDatasetVersion(ctx, p.DS.Name())
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("no %s versions yet; run fetch", p.DS.Name())
	}
	return dv.ID, err
}

// Fetch downloads every source, records a new version and uploads the raw files.
func (p *Pipeline) Fetch(ctx context.Context) (int64, error) {
	name := p.DS.Name()
	incoming := filepath.Join(p.WorkDir, name, "incoming")
	_ = os.RemoveAll(incoming)
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		return 0, err
	}
	var urls, sums []string
	for _, src := range p.DS.Sources() {
		sum, err := download(ctx, p.HTTP, src.URL, filepath.Join(incoming, src.File))
		if err != nil {
			return 0, fmt.Errorf("download %s: %w", src.URL, err)
		}
		urls = append(urls, src.URL)
		sums = append(sums, src.File+"="+sum)
	}
	sourceURL := strings.Join(urls, "\n")
	reviewed, err := p.q().LicenseReviewedBefore(ctx, store.LicenseReviewedBeforeParams{
		Dataset: name, License: p.DS.License(), SourceUrl: sourceURL})
	if err != nil {
		return 0, err
	}
	id, err := p.q().CreateDatasetVersion(ctx, store.CreateDatasetVersionParams{
		Dataset: name, SourceUrl: sourceURL, SourceChecksum: "sha256:" + strings.Join(sums, ";"),
		License: p.DS.License(), LicenseReviewed: reviewed,
	})
	if err != nil {
		return 0, err
	}
	table := p.DS.Prefix() + strconv.FormatInt(id, 10)
	if err := p.q().SetDatasetTable(ctx, store.SetDatasetTableParams{ID: id, TableName: table}); err != nil {
		return 0, err
	}
	raw := filepath.Join(p.versionDir(id), "raw")
	if err := os.MkdirAll(raw, 0o755); err != nil {
		return 0, err
	}
	for _, src := range p.DS.Sources() {
		local := filepath.Join(raw, src.File)
		if err := os.Rename(filepath.Join(incoming, src.File), local); err != nil {
			return 0, err
		}
		if err := p.Blob.PutFile(ctx, rawKey(name, id, src.File), local); err != nil {
			return 0, fmt.Errorf("upload %s: %w", src.File, err)
		}
	}
	p.Log.Info("fetched", "dataset", name, "version", id, "license", p.DS.License(), "license_reviewed", reviewed)
	return id, nil
}

// Transform produces the load-ready CSV once per version.
func (p *Pipeline) Transform(ctx context.Context, v int64) error {
	if _, err := p.version(ctx, v); err != nil {
		return err
	}
	name, key := p.DS.Name(), csvKey(p.DS.Name(), v)
	if ok, err := p.Blob.Exists(ctx, key); err != nil {
		return err
	} else if ok {
		p.Log.Info("already transformed", "dataset", name, "version", v)
		return nil
	}
	dir := p.versionDir(v)
	for _, src := range p.DS.Sources() {
		local := filepath.Join(dir, "raw", src.File)
		if _, err := os.Stat(local); errors.Is(err, os.ErrNotExist) {
			if err := p.Blob.GetFile(ctx, rawKey(name, v, src.File), local); err != nil {
				return fmt.Errorf("download raw %s: %w", src.File, err)
			}
		}
	}
	out := filepath.Join(dir, "data.csv")
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	err = p.DS.Transform(ctx, dir, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(out)
		return err
	}
	return p.Blob.PutFile(ctx, key, out)
}

// Load (re)creates the version table and fills it. It refuses the live version.
func (p *Pipeline) Load(ctx context.Context, v int64) error {
	dv, err := p.version(ctx, v)
	if err != nil {
		return err
	}
	if dv.IsLive {
		return fmt.Errorf("refusing to reload live version %d", v)
	}
	if !store.ValidIdent(dv.TableName) {
		return fmt.Errorf("bad table name %q", dv.TableName)
	}
	local := filepath.Join(p.versionDir(v), "data.csv")
	if _, err := os.Stat(local); errors.Is(err, os.ErrNotExist) {
		if err := p.Blob.GetFile(ctx, csvKey(p.DS.Name(), v), local); err != nil {
			return fmt.Errorf("transformed file missing (run transform): %w", err)
		}
	}
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, "DROP TABLE IF EXISTS "+dv.TableName); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, p.DS.CreateSQL(dv.TableName)); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	if err := p.DS.Load(ctx, tx, dv.TableName, f); err != nil {
		return fmt.Errorf("load: %w", err)
	}
	if err := store.New(tx).MarkDatasetLoaded(ctx, v); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	p.Log.Info("loaded", "dataset", p.DS.Name(), "version", v, "table", dv.TableName)
	return nil
}

// ApproveLicense marks a version's license as reviewed by a human.
func (p *Pipeline) ApproveLicense(ctx context.Context, v int64) (store.DatasetVersion, error) {
	dv, err := p.version(ctx, v)
	if err != nil {
		return dv, err
	}
	if strings.TrimSpace(dv.License) == "" {
		return dv, errors.New("no license recorded")
	}
	return dv, p.q().ApproveDatasetLicense(ctx, v)
}

// copyStage streams CSV into a staging table with COPY.
func copyStage(ctx context.Context, tx pgx.Tx, stage string, r io.Reader) error {
	_, err := tx.Conn().PgConn().CopyFrom(ctx, r, "COPY "+stage+" FROM STDIN WITH (FORMAT csv, HEADER true)")
	return err
}

func download(ctx context.Context, client *http.Client, url, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
```

- [ ] **Step 4: Implement the places dataset**

Create `internal/ingest/places.go`:

```go
package ingest

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"

	"thymeandplace/internal/store"
)

type PlaceCheck struct{ Query, Country string }

// Places is the GeoNames cities1000 dataset.
type Places struct {
	SourceList []Source
	SpotChecks []PlaceCheck
}

func NewPlaces() *Places {
	return &Places{
		SourceList: []Source{
			{URL: "https://download.geonames.org/export/dump/cities1000.zip", File: "cities1000.zip"},
			{URL: "https://download.geonames.org/export/dump/admin1CodesASCII.txt", File: "admin1CodesASCII.txt"},
		},
		SpotChecks: []PlaceCheck{
			{"London", "GB"}, {"Paris", "FR"}, {"Tokyo", "JP"}, {"Sydney", "AU"}, {"Cairo", "EG"},
			{"Lagos", "NG"}, {"Lima", "PE"}, {"Toronto", "CA"}, {"Mumbai", "IN"}, {"Moscow", "RU"},
			{"Sao Paulo", "BR"}, {"Reykjavik", "IS"},
		},
	}
}

func (*Places) Name() string        { return "places" }
func (*Places) View() string        { return "place" }
func (*Places) Prefix() string      { return "place_v" }
func (*Places) License() string     { return "CC BY 4.0 — GeoNames (https://www.geonames.org); attribution required" }
func (p *Places) Sources() []Source { return p.SourceList }

func (*Places) Transform(_ context.Context, dir string, w io.Writer) error {
	admin1, err := readAdmin1(filepath.Join(dir, "raw", "admin1CodesASCII.txt"))
	if err != nil {
		return err
	}
	zr, err := zip.OpenReader(filepath.Join(dir, "raw", "cities1000.zip"))
	if err != nil {
		return err
	}
	defer zr.Close()
	var txt *zip.File
	for _, f := range zr.File {
		if f.Name == "cities1000.txt" {
			txt = f
		}
	}
	if txt == nil {
		return fmt.Errorf("cities1000.txt not found in zip")
	}
	rc, err := txt.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"place_id", "name", "ascii_name", "admin1", "country", "population", "lat", "lng"})
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 15 {
			return fmt.Errorf("cities1000.txt line %d: %d fields", line, len(f))
		}
		ascii := f[2]
		if ascii == "" {
			ascii = f[1]
		}
		pop := f[14]
		if pop == "" {
			pop = "0"
		}
		country := f[8]
		if err := cw.Write([]string{f[0], f[1], ascii, admin1[country+"."+f[10]], country, pop, f[4], f[5]}); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func readAdmin1(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) >= 2 {
			out[parts[0]] = parts[1]
		}
	}
	return out, sc.Err()
}

func (*Places) CreateSQL(t string) string {
	return fmt.Sprintf(`CREATE TABLE %[1]s (
  place_id   integer PRIMARY KEY,
  name       text NOT NULL,
  ascii_name text NOT NULL,
  admin1     text,
  country    text NOT NULL,
  population integer NOT NULL,
  geom       geography(Point, 4326) NOT NULL
);
CREATE INDEX ON %[1]s USING gin (ascii_name gin_trgm_ops);
CREATE INDEX ON %[1]s USING gin (name gin_trgm_ops);
CREATE INDEX ON %[1]s USING gist (geom);`, t)
}

func (*Places) Load(ctx context.Context, tx pgx.Tx, t string, r io.Reader) error {
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE place_stage (place_id int, name text, ascii_name text,
		admin1 text, country text, population bigint, lat float8, lng float8) ON COMMIT DROP`); err != nil {
		return err
	}
	if err := copyStage(ctx, tx, "place_stage", r); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s
		SELECT place_id, name, ascii_name, nullif(admin1, ''), country, least(population, 2147483647)::int,
		       ST_SetSRID(ST_MakePoint(lng, lat), 4326)::geography
		FROM place_stage`, t))
	return err
}

func (p *Places) Check(ctx context.Context, db store.DBTX, t string) []string {
	var fails []string
	var bad int
	if err := db.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE population < 0 OR name = '' OR country = ''`, t)).Scan(&bad); err != nil {
		return []string{"plausibility query: " + err.Error()}
	}
	if bad > 0 {
		fails = append(fails, fmt.Sprintf("%d implausible rows", bad))
	}
	for _, c := range p.SpotChecks {
		rows, err := store.SearchPlaces(ctx, db, t, c.Query, 1)
		switch {
		case err != nil:
			fails = append(fails, fmt.Sprintf("spot check %q: %v", c.Query, err))
		case len(rows) == 0:
			fails = append(fails, fmt.Sprintf("spot check %q: no match", c.Query))
		case rows[0].Country != c.Country:
			fails = append(fails, fmt.Sprintf("spot check %q: top result in %s, want %s", c.Query, rows[0].Country, c.Country))
		}
	}
	return fails
}
```

- [ ] **Step 5: Write test helpers and the places tests**

Create `internal/ingest/testutil_test.go`:

```go
package ingest

import (
	"archive/zip"
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// serveFiles serves name → content over HTTP and returns the base URL.
func serveFiles(t *testing.T, files map[string][]byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[filepath.Base(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newPipeline(t *testing.T, pool *pgxpool.Pool, ds Dataset) *Pipeline {
	t.Helper()
	root := t.TempDir()
	return &Pipeline{
		DS: ds, Pool: pool, Blob: FSBlob{Root: filepath.Join(root, "bucket")},
		WorkDir: filepath.Join(root, "work"), HTTP: http.DefaultClient,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
```

Create `internal/ingest/places_test.go`:

```go
package ingest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"thymeandplace/internal/dbtest"
)

// geonamesLine builds one 19-column cities1000.txt row.
func geonamesLine(id int, name, ascii string, lat, lng float64, country, admin1 string, pop int) string {
	f := make([]string, 19)
	f[0], f[1], f[2] = fmt.Sprint(id), name, ascii
	f[4], f[5], f[6], f[7] = fmt.Sprint(lat), fmt.Sprint(lng), "P", "PPL"
	f[8], f[10], f[14] = country, admin1, fmt.Sprint(pop)
	return strings.Join(f, "\t")
}

// placesSource returns a Places dataset served from a local HTTP server with n filler rows.
func placesSource(t *testing.T, n int) *Places {
	lines := []string{
		geonamesLine(1, "London", "London", 51.5085, -0.1257, "GB", "ENG", 8961989),
		geonamesLine(2, "London", "London", 42.9834, -81.233, "CA", "08", 346765),
		geonamesLine(3, "Paris", "Paris", 48.8534, 2.3488, "FR", "11", 2138551),
		geonamesLine(4, "Zürich", "Zurich", 47.3667, 8.55, "CH", "ZH", 341730),
	}
	for i := 0; i < n; i++ {
		lines = append(lines, geonamesLine(100+i, fmt.Sprintf("Town %d", i), fmt.Sprintf("Town %d", i), float64(i%80), float64(i%170), "ZZ", "", 100))
	}
	admin1 := "GB.ENG\tEngland\tEngland\t6269131\nCA.08\tOntario\tOntario\t6093943\nFR.11\tÎle-de-France\tIle-de-France\t3012874\n"
	base := serveFiles(t, map[string][]byte{
		"cities1000.zip":       zipOf(t, map[string][]byte{"cities1000.txt": []byte(strings.Join(lines, "\n") + "\n")}),
		"admin1CodesASCII.txt": []byte(admin1),
	})
	return &Places{
		SourceList: []Source{{base + "/cities1000.zip", "cities1000.zip"}, {base + "/admin1CodesASCII.txt", "admin1CodesASCII.txt"}},
		SpotChecks: []PlaceCheck{{"London", "GB"}, {"Paris", "FR"}},
	}
}

func TestPlacesFetchTransformLoad(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, placesSource(t, 20))

	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	dv, _ := p.version(ctx, v)
	if dv.TableName != fmt.Sprintf("place_v%d", v) || !strings.HasPrefix(dv.SourceChecksum, "sha256:cities1000.zip=") ||
		dv.LicenseReviewed || !strings.Contains(dv.License, "CC BY 4.0") {
		t.Fatalf("version row = %+v", dv)
	}
	if ok, _ := p.Blob.Exists(ctx, rawKey("places", v, "cities1000.zip")); !ok {
		t.Fatal("raw file not uploaded")
	}

	if err := p.Transform(ctx, v); err != nil {
		t.Fatalf("transform: %v", err)
	}
	// Review Focus 4: transform twice is a no-op.
	if err := p.Transform(ctx, v); err != nil {
		t.Fatalf("second transform: %v", err)
	}
	if err := p.Load(ctx, v); err != nil {
		t.Fatalf("load: %v", err)
	}
	// Review Focus 4: load twice rebuilds cleanly.
	if err := p.Load(ctx, v); err != nil {
		t.Fatalf("second load: %v", err)
	}
	var n int
	var admin1 string
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*), max(admin1) FILTER (WHERE place_id = 1) FROM %s`, dv.TableName)).Scan(&n, &admin1); err != nil {
		t.Fatal(err)
	}
	if n != 24 || admin1 != "England" {
		t.Fatalf("rows=%d admin1=%q", n, admin1)
	}
	if fails := p.DS.Check(ctx, pool, dv.TableName); len(fails) != 0 {
		t.Fatalf("check: %v", fails)
	}
}

func TestPlacesCheckReportsSpotFailures(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	ds := placesSource(t, 5)
	ds.SpotChecks = append(ds.SpotChecks, PlaceCheck{"Atlantis", "XX"})
	p := newPipeline(t, pool, ds)
	v, _ := p.Fetch(ctx)
	_ = p.Transform(ctx, v)
	if err := p.Load(ctx, v); err != nil {
		t.Fatal(err)
	}
	dv, _ := p.version(ctx, v)
	fails := ds.Check(ctx, pool, dv.TableName)
	if len(fails) != 1 || !strings.Contains(fails[0], "Atlantis") {
		t.Fatalf("fails = %v", fails)
	}
}
```

- [ ] **Step 6: Run the tests**

Run: `go mod tidy && go test ./internal/ingest/ -count=1`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/store internal/ingest
git commit -m "feat(ingest): blob store, versioned fetch/transform/load, GeoNames places

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Validate, swap, rollback and the `ingest` CLI

**Files:**
- Modify: `internal/ingest/pipeline.go`
- Create: `internal/ingest/swap_test.go`, `cmd/ingest/main.go`, `cmd/ingest/main_test.go`

**Interfaces:**
- Consumes: everything from Task 3.
- Produces:
  - `(*Pipeline).Validate(ctx, v int64) error` (returns `*ValidationError`)
  - `(*Pipeline).Swap(ctx, v int64) error` (validates first)
  - `(*Pipeline).Rollback(ctx) error`
  - `(*Pipeline).All(ctx context.Context, v int64) error` (`v == 0` fetches first)
  - `func main.run(args []string, getenv func(string) string, stdout, stderr io.Writer, datasets map[string]func() ingest.Dataset) int`. Exit codes: 0 ok, 1 failure, 2 usage, 3 external transform needed.

- [ ] **Step 1: Write the failing swap/rollback tests**

Create `internal/ingest/swap_test.go`:

```go
package ingest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/dbtest"
)

func viewTarget(t *testing.T, pool *pgxpool.Pool, view string) string {
	t.Helper()
	var def string
	if err := pool.QueryRow(context.Background(), `SELECT pg_get_viewdef($1::regclass)`, view).Scan(&def); err != nil {
		t.Fatal(err)
	}
	return def
}

func liveID(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	_ = pool.QueryRow(context.Background(), `SELECT coalesce(max(id), 0) FROM dataset_versions WHERE dataset = 'places' AND is_live`).Scan(&id)
	return id
}

func loadVersion(t *testing.T, p *Pipeline) int64 {
	t.Helper()
	ctx := context.Background()
	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Transform(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := p.Load(ctx, v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSwapRequiresReviewedLicense(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, placesSource(t, 20))
	v := loadVersion(t, p)

	err := p.Swap(ctx, v)
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), "license") {
		t.Fatalf("want license failure, got %v", err)
	}
	if liveID(t, pool) != 0 {
		t.Fatal("unreviewed version went live")
	}
}

func TestSwapRollbackAndNotify(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	ds := placesSource(t, 20)
	p := newPipeline(t, pool, ds)

	conn, _ := pool.Acquire(ctx)
	defer conn.Release()
	_, _ = conn.Exec(ctx, "LISTEN data_version_changed")

	v1 := loadVersion(t, p)
	if _, err := p.ApproveLicense(ctx, v1); err != nil {
		t.Fatal(err)
	}
	if err := p.Swap(ctx, v1); err != nil {
		t.Fatalf("swap v1: %v", err)
	}
	if liveID(t, pool) != v1 || !strings.Contains(viewTarget(t, pool, "place"), "place_v") {
		t.Fatalf("live=%d view=%s", liveID(t, pool), viewTarget(t, pool, "place"))
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if n, err := conn.Conn().WaitForNotification(wctx); err != nil || n.Payload != "dataset" {
		t.Fatalf("notify: %+v %v", n, err)
	}

	// Same sources and license: the review is inherited.
	v2 := loadVersion(t, p)
	if dv, _ := p.version(ctx, v2); !dv.LicenseReviewed {
		t.Fatal("identical license/sources should inherit review")
	}
	if err := p.Swap(ctx, v2); err != nil {
		t.Fatalf("swap v2: %v", err)
	}
	// Review Focus 4: the live version cannot be reloaded.
	if err := p.Load(ctx, v2); err == nil || !strings.Contains(err.Error(), "live") {
		t.Fatalf("reloading live version: %v", err)
	}

	if err := p.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if liveID(t, pool) != v1 {
		t.Fatalf("after rollback live = %d, want %d", liveID(t, pool), v1)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM place`).Scan(&n)
	if n != 24 {
		t.Fatalf("view rows after rollback = %d", n)
	}
	if err := p.Rollback(ctx); err == nil {
		t.Fatal("rolling back past the first promoted version must fail")
	}
}

// Review Focus 5: failed validation leaves live data untouched.
func TestFailedValidationLeavesLiveUnchanged(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, placesSource(t, 20))
	v1 := loadVersion(t, p)
	_, _ = p.ApproveLicense(ctx, v1)
	if err := p.Swap(ctx, v1); err != nil {
		t.Fatal(err)
	}
	before := viewTarget(t, pool, "place")

	p.DS = placesSource(t, 2) // 6 rows vs 24: outside ±5%
	v2 := loadVersion(t, p)
	_, _ = p.ApproveLicense(ctx, v2)
	err := p.Swap(ctx, v2)
	if err == nil || !strings.Contains(err.Error(), "row count") {
		t.Fatalf("want row-count failure, got %v", err)
	}
	if liveID(t, pool) != v1 || viewTarget(t, pool, "place") != before {
		t.Fatal("failed swap changed live data")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/ingest/ -run 'Swap|Validation'`
Expected: FAIL to compile: `p.Swap undefined`.

- [ ] **Step 3: Implement validate, swap, rollback and all**

Append to `internal/ingest/pipeline.go`:

```go
// Validate runs the common and dataset-specific checks on version v.
func (p *Pipeline) Validate(ctx context.Context, v int64) error {
	dv, err := p.version(ctx, v)
	if err != nil {
		return err
	}
	var fails []string
	if strings.TrimSpace(dv.License) == "" {
		fails = append(fails, "no license recorded")
	} else if !dv.LicenseReviewed {
		fails = append(fails, fmt.Sprintf("license not reviewed (run: ingest %s approve-license --version %d --yes)", p.DS.Name(), v))
	}
	var exists bool
	if err := p.Pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, dv.TableName).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		fails = append(fails, "table not loaded (run load)")
		return &ValidationError{Dataset: p.DS.Name(), Failures: fails}
	}
	var n int64
	if err := p.Pool.QueryRow(ctx, "SELECT count(*) FROM "+dv.TableName).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		fails = append(fails, "table is empty")
	}
	live, err := p.q().LiveDatasetVersion(ctx, p.DS.Name())
	switch {
	case errors.Is(err, pgx.ErrNoRows): // first load: no row-count comparison
	case err != nil:
		return err
	case live.ID != v:
		var liveN int64
		if err := p.Pool.QueryRow(ctx, "SELECT count(*) FROM "+live.TableName).Scan(&liveN); err != nil {
			return err
		}
		if liveN > 0 {
			diff := float64(n-liveN) / float64(liveN)
			if diff < -0.05 || diff > 0.05 {
				fails = append(fails, fmt.Sprintf("row count %d differs from live %d by %.1f%% (limit ±5%%)", n, liveN, diff*100))
			}
		}
	}
	fails = append(fails, p.DS.Check(ctx, p.Pool, dv.TableName)...)
	if len(fails) > 0 {
		return &ValidationError{Dataset: p.DS.Name(), Failures: fails}
	}
	p.Log.Info("validated", "dataset", p.DS.Name(), "version", v, "rows", n)
	return nil
}

// Swap validates v and makes it live in one transaction.
func (p *Pipeline) Swap(ctx context.Context, v int64) error {
	if err := p.Validate(ctx, v); err != nil {
		return err
	}
	dv, err := p.version(ctx, v)
	if err != nil {
		return err
	}
	return p.point(ctx, dv)
}

// Rollback repoints the view at the most recent earlier promoted version.
func (p *Pipeline) Rollback(ctx context.Context) error {
	live, err := p.q().LiveDatasetVersion(ctx, p.DS.Name())
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("no live version to roll back from")
	}
	if err != nil {
		return err
	}
	prev, err := p.q().PreviousPromotedVersion(ctx, store.PreviousPromotedVersionParams{Dataset: p.DS.Name(), Before: live.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("no earlier promoted version before %d", live.ID)
	}
	if err != nil {
		return err
	}
	var exists bool
	if err := p.Pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, prev.TableName).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("version %d table %s was dropped", prev.ID, prev.TableName)
	}
	return p.point(ctx, prev)
}

func (p *Pipeline) point(ctx context.Context, dv store.DatasetVersion) error {
	if !store.ValidIdent(dv.TableName) || !store.ValidIdent(p.DS.View()) {
		return fmt.Errorf("bad identifier")
	}
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, fmt.Sprintf("CREATE OR REPLACE VIEW %s AS SELECT * FROM %s", p.DS.View(), dv.TableName)); err != nil {
		return fmt.Errorf("repoint view: %w", err)
	}
	q := store.New(tx)
	if err := q.ClearLive(ctx, p.DS.Name()); err != nil {
		return err
	}
	if err := q.MarkLive(ctx, dv.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('data_version_changed', 'dataset')`); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	p.Log.Info("live", "dataset", p.DS.Name(), "version", dv.ID, "table", dv.TableName)
	return nil
}

// All runs every step for v, fetching a new version first when v == 0.
func (p *Pipeline) All(ctx context.Context, v int64) error {
	if v == 0 {
		var err error
		if v, err = p.Fetch(ctx); err != nil {
			return err
		}
	}
	if err := p.Transform(ctx, v); err != nil {
		return err
	}
	if err := p.Load(ctx, v); err != nil {
		return err
	}
	return p.Swap(ctx, v)
}
```

- [ ] **Step 4: Run the pipeline tests**

Run: `go test ./internal/ingest/ -count=1`
Expected: `ok`.

- [ ] **Step 5: Write the CLI and its test**

Create `cmd/ingest/main.go`:

```go
// Command ingest runs the reference-data pipeline.
//
//	ingest <dataset> fetch|transform|load|validate|swap|all [--version N]
//	ingest <dataset> rollback
//	ingest <dataset> approve-license --version N --yes
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"thymeandplace/internal/db"
	"thymeandplace/internal/ingest"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, defaultDatasets(os.Getenv)))
}

func defaultDatasets(getenv func(string) string) map[string]func() ingest.Dataset {
	return map[string]func() ingest.Dataset{
		"places":  func() ingest.Dataset { return ingest.NewPlaces() },
		"ocean":   func() ingest.Dataset { return ingest.NewOcean(getenv("OCEAN_SOURCE_URL")) },
		"climate": func() ingest.Dataset { return ingest.NewClimate(getenv("CLIMATE_SOURCE_BASE")) },
	}
}

const usage = `usage:
  ingest <climate|ocean|places> fetch|transform|load|validate|swap|all [--version N]
  ingest <climate|ocean|places> rollback
  ingest <climate|ocean|places> approve-license --version N --yes`

func run(args []string, getenv func(string) string, stdout, stderr io.Writer, datasets map[string]func() ingest.Dataset) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	name, step := args[0], args[1]
	mk, ok := datasets[name]
	if !ok {
		fmt.Fprintf(stderr, "unknown dataset %q\n%s\n", name, usage)
		return 2
	}
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.Int64("version", 0, "dataset version (default: latest)")
	yes := fs.Bool("yes", false, "confirm license approval")
	if err := fs.Parse(args[2:]); err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fail := func(err error) int {
		fmt.Fprintf(stderr, "ingest %s %s: %v\n", name, step, err)
		var ext *ingest.ExternalTransformError
		if errors.As(err, &ext) {
			return 3
		}
		return 1
	}

	pool, err := db.Open(ctx, getenv("DATABASE_URL"))
	if err != nil {
		return fail(err)
	}
	defer pool.Close()
	blob, err := blobFromEnv(getenv)
	if err != nil {
		return fail(err)
	}
	work := getenv("INGEST_WORKDIR")
	if work == "" {
		work = "/work"
	}
	p := &ingest.Pipeline{
		DS: mk(), Pool: pool, Blob: blob, WorkDir: work,
		HTTP: &http.Client{Timeout: 2 * time.Hour},
		Log:  slog.New(slog.NewJSONHandler(stdout, nil)),
	}

	v := *version
	needsVersion := map[string]bool{"transform": true, "load": true, "validate": true, "swap": true}
	if v == 0 && needsVersion[step] {
		if v, err = p.Latest(ctx); err != nil {
			return fail(err)
		}
	}

	switch step {
	case "fetch":
		v, err = p.Fetch(ctx)
		if err == nil {
			fmt.Fprintf(stdout, "fetched %s version %d\n", name, v)
		}
	case "transform":
		err = p.Transform(ctx, v)
	case "load":
		err = p.Load(ctx, v)
	case "validate":
		err = p.Validate(ctx, v)
	case "swap":
		err = p.Swap(ctx, v)
	case "all":
		err = p.All(ctx, v)
	case "rollback":
		err = p.Rollback(ctx)
	case "approve-license":
		if v == 0 || !*yes {
			fmt.Fprintln(stderr, "approve-license needs --version N and --yes after you have read the license")
			return 2
		}
		dv, aerr := p.ApproveLicense(ctx, v)
		err = aerr
		if err == nil {
			fmt.Fprintf(stdout, "approved license for %s version %d:\n  %s\n  source: %s\n", name, v, dv.License, dv.SourceUrl)
		}
	default:
		fmt.Fprintf(stderr, "unknown step %q\n%s\n", step, usage)
		return 2
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

func blobFromEnv(getenv func(string) string) (ingest.Blob, error) {
	if dir := getenv("BLOB_DIR"); dir != "" {
		return ingest.FSBlob{Root: dir}, nil
	}
	endpoint, bucket := getenv("BUCKET_ENDPOINT"), getenv("BUCKET_NAME")
	if endpoint == "" || bucket == "" {
		return nil, errors.New("set BLOB_DIR, or BUCKET_ENDPOINT and BUCKET_NAME")
	}
	return ingest.NewS3Blob(endpoint, bucket, getenv("BUCKET_ACCESS_KEY"), getenv("BUCKET_SECRET_KEY"), getenv("BUCKET_USE_SSL") != "false")
}
```

Create `cmd/ingest/main_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"

	"thymeandplace/internal/ingest"
)

func TestUsageErrors(t *testing.T) {
	ds := map[string]func() ingest.Dataset{"places": func() ingest.Dataset { return ingest.NewPlaces() }}
	env := func(string) string { return "" }
	for _, args := range [][]string{{}, {"places"}, {"nope", "fetch"}} {
		var out, errb bytes.Buffer
		if code := run(args, env, &out, &errb, ds); code != 2 {
			t.Fatalf("%v: code %d", args, code)
		}
	}
}

// Review Focus 5: failures name the dataset and step and exit non-zero.
func TestFailureNamesDatasetAndStep(t *testing.T) {
	ds := map[string]func() ingest.Dataset{"places": func() ingest.Dataset { return ingest.NewPlaces() }}
	env := func(k string) string {
		if k == "DATABASE_URL" {
			return "postgres://nobody:nothing@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"
		}
		return ""
	}
	var out, errb bytes.Buffer
	code := run([]string{"places", "swap", "--version", "1"}, env, &out, &errb, ds)
	if code != 1 || !strings.Contains(errb.String(), "ingest places swap:") {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
}
```

`NewOcean` and `NewClimate` don't exist until Tasks 5 and 6. Until then, register only `places` in `defaultDatasets` and add the other two lines as those tasks land.

- [ ] **Step 6: Run the CLI tests**

Run: `go test ./cmd/ingest/ -count=1`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/ingest cmd/ingest
git commit -m "feat(ingest): validate, atomic swap with NOTIFY, rollback and CLI

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: MEOW ocean dataset

**Files:**
- Create: `internal/ingest/ocean.go`, `internal/ingest/ocean_test.go`
- Modify: `cmd/ingest/main.go` (register `ocean`)

**Interfaces:**
- Consumes: `Pipeline`, `copyStage`, `store.NearbyEcoregions`, `catalog.Write` (test only).
- Produces:
  - `type ingest.Ocean struct { Source Source; MinCount, MaxCount int; Coastal, Inland []Point }` with `type ingest.Point struct { Name string; Lat, Lng float64 }`
  - `const ingest.DefaultOceanURL` (Esri Oceans MEOW feature service, GeoJSON query)
  - `func ingest.NewOcean(sourceURL string) *Ocean` (empty string = default URL)

Source, verified 2026-09-25: Esri Oceans ArcGIS Online item `74b6ac5c8fc24dcb8abaad6428a5dfa4` ("MEOW"), feature service `https://services.arcgis.com/bDAhvQYMG4WL8O5o/arcgis/rest/services/MEOW/FeatureServer/0`. It has 232 polygons with fields `ECO_CODE_X` (1–232, unique) and `ECOREGION` (e.g. 25 = "North Sea"), and no `TYPE` field. One GeoJSON query returns all 232 features (~1 MB; `maxRecordCount` is 2000). **The item records no licence text**, so the operator must confirm terms before `approve-license`.

- [ ] **Step 1: Write the failing tests (synthetic GeoJSON)**

Create `internal/ingest/ocean_test.go`:

```go
package ingest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

func rect(x0, y0, x1, y1 float64) [][2]float64 {
	return [][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}, {x0, y0}}
}

func polygon(rings ...[][2]float64) map[string]any {
	return map[string]any{"type": "Polygon", "coordinates": rings}
}

func feature(id any, name string, geom map[string]any) map[string]any {
	return map[string]any{"type": "Feature", "properties": map[string]any{"ECO_CODE_X": id, "ECOREGION": name}, "geometry": geom}
}

func geoJSON(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func collection(features ...map[string]any) map[string]any {
	return map[string]any{"type": "FeatureCollection", "features": features}
}

func testOcean(t *testing.T, body []byte) *Ocean {
	base := serveFiles(t, map[string][]byte{"meow.geojson": body})
	return &Ocean{
		Source:   Source{URL: base + "/meow.geojson", File: "meow.geojson"},
		MinCount: 2, MaxCount: 2,
		Coastal:  []Point{{"lisbon", 38.7167, -9.1333}},
		Inland:   []Point{{"madrid", 40.4165, -3.7026}},
	}
}

func goodMEOW(t *testing.T) []byte {
	return geoJSON(t, collection(
		feature(30, "Iberian", polygon(rect(-10.5, 36.5, -9.4, 43.5))),
		feature(25, "North Sea", polygon(rect(0, 51, 8, 58), rect(2, 53, 3, 54))), // outer ring + hole
		feature(25, "North Sea", polygon(rect(8, 55, 9, 56))),                     // second part, same id
		feature(nil, "No code", polygon(rect(-40, -10, -20, 10))),                 // skipped
	))
}

func loadOcean(t *testing.T, p *Pipeline) (int64, error) {
	t.Helper()
	ctx := context.Background()
	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Transform(ctx, v); err != nil {
		return v, err
	}
	return v, p.Load(ctx, v)
}

func TestOceanPipeline(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	ds := testOcean(t, goodMEOW(t))
	p := newPipeline(t, pool, ds)
	v, err := loadOcean(t, p)
	if err != nil {
		t.Fatalf("transform/load: %v", err)
	}
	dv, _ := p.version(ctx, v)
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM "+dv.TableName).Scan(&n)
	if n != 2 {
		t.Fatalf("ecoregions = %d, want 2 (null code skipped, parts merged)", n)
	}
	if ids, _ := store.NearbyEcoregions(ctx, pool, dv.TableName, 55.5, 8.5, 0); len(ids) != 1 || ids[0] != 25 {
		t.Fatalf("second part: %v", ids)
	}
	if ids, _ := store.NearbyEcoregions(ctx, pool, dv.TableName, 53.5, 2.5, 0); len(ids) != 0 {
		t.Fatalf("hole treated as inside: %v", ids)
	}
	if fails := ds.Check(ctx, pool, dv.TableName); len(fails) != 0 {
		t.Fatalf("check: %v", fails)
	}
}

func TestOceanRejectsBadResponses(t *testing.T) {
	cases := map[string][]byte{
		"arcgis error":   []byte(`{"error":{"code":400,"message":"Invalid query"}}`),
		"truncated":      geoJSON(t, map[string]any{"type": "FeatureCollection", "features": []any{}, "properties": map[string]any{"exceededTransferLimit": true}}),
		"no features":    geoJSON(t, collection()),
		"not a polygon":  geoJSON(t, collection(feature(7, "Point", map[string]any{"type": "Point", "coordinates": []float64{1, 2}}))),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			pool := dbtest.New(t)
			if _, err := loadOcean(t, newPipeline(t, pool, testOcean(t, body))); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestOceanCheckCatalogEcoregions(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	ds := testOcean(t, goodMEOW(t))
	p := newPipeline(t, pool, ds)
	v, err := loadOcean(t, p)
	if err != nil {
		t.Fatal(err)
	}
	dv, _ := p.version(ctx, v)

	cod := catalogtest.Seafood()
	cod.Regions = []catalog.SeafoodRegion{{EcoregionID: 77, Months: []int{1}}}
	tx, _ := pool.Begin(ctx)
	if err := catalog.Write(ctx, store.New(tx), []catalog.Ingredient{cod}, nil); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)

	fails := ds.Check(ctx, pool, dv.TableName)
	if len(fails) != 1 || !strings.Contains(fails[0], "77") {
		t.Fatalf("fails = %v", fails)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/ingest/ -run Ocean`
Expected: FAIL to compile: `undefined: Ocean`.

- [ ] **Step 3: Implement the ocean dataset**

Create `internal/ingest/ocean.go`:

```go
package ingest

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"thymeandplace/internal/store"
)

type Point struct {
	Name     string
	Lat, Lng float64
}

// Ocean is Marine Ecoregions of the World (Spalding et al., 2007), served as
// GeoJSON by the Esri Oceans MEOW feature service.
type Ocean struct {
	Source             Source
	MinCount, MaxCount int
	Coastal, Inland    []Point
}

// DefaultOceanURL returns all 232 MEOW polygons as GeoJSON in WGS84 in one
// request (item 74b6ac5c8fc24dcb8abaad6428a5dfa4). Override with OCEAN_SOURCE_URL.
const DefaultOceanURL = "https://services.arcgis.com/bDAhvQYMG4WL8O5o/arcgis/rest/services/MEOW/FeatureServer/0/query" +
	"?where=1%3D1&outFields=ECO_CODE_X,ECOREGION&outSR=4326&f=geojson"

func NewOcean(url string) *Ocean {
	if url == "" {
		url = DefaultOceanURL
	}
	return &Ocean{
		Source:   Source{URL: url, File: "meow.geojson"},
		MinCount: 225, MaxCount: 240, // MEOW has 232 ecoregions
		Coastal: []Point{
			{"Lisbon", 38.72, -9.14}, {"Boston", 42.36, -71.06}, {"Tokyo", 35.68, 139.77},
			{"Sydney", -33.87, 151.21}, {"Cape Town", -33.92, 18.42}, {"Valparaíso", -33.05, -71.62},
			{"Mumbai", 18.94, 72.84}, {"Reykjavik", 64.15, -21.94}, {"Vancouver", 49.28, -123.12},
			{"Singapore", 1.29, 103.85},
		},
		Inland: []Point{
			{"Madrid", 40.42, -3.70}, {"Denver", 39.74, -104.99}, {"Ulaanbaatar", 47.92, 106.92}, {"Bamako", 12.64, -8.0},
		},
	}
}

func (*Ocean) Name() string   { return "ocean" }
func (*Ocean) View() string   { return "ocean_ecoregion" }
func (*Ocean) Prefix() string { return "ocean_ecoregion_v" }
func (*Ocean) License() string {
	return "Marine Ecoregions of the World (Spalding et al., 2007, BioScience), via Esri Oceans ArcGIS Online item " +
		"74b6ac5c8fc24dcb8abaad6428a5dfa4. The item states no licence: confirm terms with the original publishers " +
		"(The Nature Conservancy / WWF) before approving"
}
func (o *Ocean) Sources() []Source { return []Source{o.Source} }

type geoFeatureCollection struct {
	Type     string `json:"type"`
	Features []struct {
		Properties map[string]any  `json:"properties"`
		Geometry   json.RawMessage `json:"geometry"`
	} `json:"features"`
	ExceededTransferLimit bool `json:"exceededTransferLimit"`
	Properties            struct {
		ExceededTransferLimit bool `json:"exceededTransferLimit"`
	} `json:"properties"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Transform turns the GeoJSON FeatureCollection into CSV rows of
// (ecoregion_id, name, geojson geometry).
func (o *Ocean) Transform(_ context.Context, dir string, w io.Writer) error {
	b, err := os.ReadFile(filepath.Join(dir, "raw", o.Source.File))
	if err != nil {
		return err
	}
	var fc geoFeatureCollection
	if err := json.Unmarshal(b, &fc); err != nil {
		return fmt.Errorf("not GeoJSON: %w", err)
	}
	switch {
	case fc.Error != nil:
		return fmt.Errorf("feature service error %d: %s", fc.Error.Code, fc.Error.Message)
	case fc.Type != "FeatureCollection":
		return fmt.Errorf("expected a FeatureCollection, got %q", fc.Type)
	case fc.ExceededTransferLimit || fc.Properties.ExceededTransferLimit:
		return errors.New("feature service truncated the result (exceededTransferLimit); page the query with resultOffset")
	case len(fc.Features) == 0:
		return errors.New("no features in the response")
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"ecoregion_id", "name", "geojson"})
	for i, f := range fc.Features {
		code, ok := f.Properties["ECO_CODE_X"].(float64)
		if !ok || code <= 0 {
			continue // no usable ecoregion code
		}
		var g struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(f.Geometry, &g); err != nil || (g.Type != "Polygon" && g.Type != "MultiPolygon") {
			return fmt.Errorf("feature %d (ECO_CODE_X %v): geometry must be Polygon or MultiPolygon, got %q", i, code, g.Type)
		}
		name, _ := f.Properties["ECOREGION"].(string)
		var compact bytes.Buffer
		if err := json.Compact(&compact, f.Geometry); err != nil {
			return err
		}
		if err := cw.Write([]string{strconv.Itoa(int(code)), strings.TrimSpace(name), compact.String()}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func (*Ocean) CreateSQL(t string) string {
	return fmt.Sprintf(`CREATE TABLE %[1]s (
  ecoregion_id integer PRIMARY KEY,
  name         text NOT NULL,
  geom         geography(MultiPolygon, 4326) NOT NULL
);
CREATE INDEX ON %[1]s USING gist (geom);`, t)
}

func (*Ocean) Load(ctx context.Context, tx pgx.Tx, t string, r io.Reader) error {
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE ocean_stage (ecoregion_id int, name text, geojson text) ON COMMIT DROP`); err != nil {
		return err
	}
	if err := copyStage(ctx, tx, "ocean_stage", r); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s
		SELECT ecoregion_id, min(name),
		       ST_Multi(ST_CollectionExtract(ST_MakeValid(ST_Union(ST_SetSRID(ST_GeomFromGeoJSON(geojson), 4326))), 3))::geography
		FROM ocean_stage GROUP BY ecoregion_id`, t))
	return err
}

func (o *Ocean) Check(ctx context.Context, db store.DBTX, t string) []string {
	var fails []string
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM "+t).Scan(&n); err != nil {
		return []string{"count: " + err.Error()}
	}
	if n < o.MinCount || n > o.MaxCount {
		fails = append(fails, fmt.Sprintf("%d ecoregions, expected %d–%d", n, o.MinCount, o.MaxCount))
	}
	for _, pt := range o.Coastal {
		ids, err := store.NearbyEcoregions(ctx, db, t, pt.Lat, pt.Lng, 100_000)
		if err != nil || len(ids) == 0 {
			fails = append(fails, fmt.Sprintf("spot check %s: no ecoregion within 100 km (%v)", pt.Name, err))
		}
	}
	for _, pt := range o.Inland {
		ids, err := store.NearbyEcoregions(ctx, db, t, pt.Lat, pt.Lng, 100_000)
		if err != nil || len(ids) > 0 {
			fails = append(fails, fmt.Sprintf("spot check %s: expected inland, got %v (%v)", pt.Name, ids, err))
		}
	}
	rows, err := db.Query(ctx, fmt.Sprintf(`SELECT DISTINCT r.ecoregion_id FROM seafood_region r
		WHERE NOT EXISTS (SELECT 1 FROM %s e WHERE e.ecoregion_id = r.ecoregion_id) ORDER BY 1`, t))
	if err != nil {
		return append(fails, "catalog check: "+err.Error())
	}
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err == nil {
			missing = append(missing, strconv.Itoa(int(id)))
		}
	}
	if len(missing) > 0 {
		fails = append(fails, "catalog seafood regions reference missing ecoregions: "+strings.Join(missing, ", "))
	}
	return fails
}
```

Register the dataset in `cmd/ingest/main.go` `defaultDatasets`:

```go
		"ocean":   func() ingest.Dataset { return ingest.NewOcean(getenv("OCEAN_SOURCE_URL")) },
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/ingest/ ./cmd/ingest/ -count=1`
Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
git add internal/ingest cmd/ingest
git commit -m "feat(ingest): MEOW ocean ecoregions from GeoJSON with catalog referential check

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: TerraClimate dataset and the Python transform

**Files:**
- Create: `pipeline/transform/transform.py`, `pipeline/transform/requirements.txt`, `pipeline/transform/requirements-dev.txt`, `pipeline/transform/Dockerfile`, `pipeline/tests/test_transform.py`, `internal/ingest/climate.go`, `internal/ingest/climate_test.go`
- Modify: `cmd/ingest/main.go` (register `climate`)

**Interfaces:**
- Consumes: `Pipeline`, `ExternalTransformError`, `store.CellsIn`, `climate.CellID`, `climate.NeighborCells`, `climate.PickNearest`, `climate.CellsToNormals`.
- Produces:
  - `pipeline/transform/transform.py <version_dir>`: reads `<version_dir>/raw/*{tmin,tmax,ppt}*.nc`, writes `<version_dir>/out/climate.csv` with header `cell_id,tmin_c,tmax_c,precip_mm` and array columns in Postgres literal form (`"{1.00,2.00,…}"`)
  - `type ingest.ClimateCheck struct { Name string; Lat, Lng, AnnualMeanC, JulyMinusJanC float64 }`
  - `type ingest.Climate struct { SourceList []Source; Checks []ClimateCheck; ToleranceC float64 }`, `func ingest.NewClimate(base string) *Climate`

- [ ] **Step 1: Write the failing pytest**

```bash
mkdir -p pipeline/transform pipeline/tests
```

Create `pipeline/transform/requirements.txt`:

```text
numpy>=2.0
pandas>=2.2
xarray>=2024.10
dask[array]>=2024.10
netCDF4>=1.7
```

Create `pipeline/transform/requirements-dev.txt`:

```text
-r requirements.txt
pytest>=8.3
```

Create `pipeline/tests/test_transform.py`:

```python
import csv
import math
import sys
from pathlib import Path

import numpy as np
import pandas as pd
import pytest
import xarray as xr

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "transform"))
import transform  # noqa: E402

STEP = 1 / 24  # TerraClimate native resolution


def fine_grid():
    # 12 x 12 fine cells covering lat 45..44.5, lng 0..0.5 → 2 x 2 coarse cells.
    lat = 45 - STEP / 2 - STEP * np.arange(12)
    lon = STEP / 2 + STEP * np.arange(12)
    return lat, lon


def write_var(path, name, values_by_month, times, mask_block=None, half_block=None):
    lat, lon = fine_grid()
    data = np.empty((len(times), 12, 12), dtype="float32")
    for i, t in enumerate(times):
        data[i] = values_by_month(pd.Timestamp(t).month)
    if mask_block is not None:  # whole coarse block NaN → dropped (ocean)
        r, c = mask_block
        data[:, r * 6:(r + 1) * 6, c * 6:(c + 1) * 6] = np.nan
    if half_block is not None:  # half the fine cells NaN → mean of the rest
        r, c = half_block
        data[:, r * 6:r * 6 + 3, c * 6:(c + 1) * 6] = np.nan
        data[:, r * 6 + 3:(r + 1) * 6, c * 6:(c + 1) * 6] += 10
    xr.Dataset({name: (("time", "lat", "lon"), data)}, coords={"time": times, "lat": lat, "lon": lon}).to_netcdf(path)


def make_inputs(raw: Path, times):
    raw.mkdir(parents=True)
    write_var(raw / "tc_tmin.nc", "tmin", lambda m: 10 + m, times, mask_block=(1, 1), half_block=(0, 1))
    write_var(raw / "tc_tmax.nc", "tmax", lambda m: 20 + m, times, mask_block=(1, 1), half_block=(0, 1))
    write_var(raw / "tc_ppt.nc", "ppt", lambda m: 50.0, times, mask_block=(1, 1), half_block=(0, 1))


def read_rows(path):
    with open(path, newline="") as fh:
        rows = list(csv.DictReader(fh))
    return {int(r["cell_id"]): r for r in rows}


def parse(arr):
    return [float(v) for v in arr.strip("{}").split(",")]


@pytest.mark.parametrize("times", [
    pd.date_range("2000-01-01", periods=12, freq="MS"),        # climatology file
    pd.date_range("1985-01-01", "2023-12-01", freq="MS"),      # yearly files: must select 1991–2020
])
def test_transform(tmp_path, times):
    vdir = tmp_path / "v1"
    make_inputs(vdir / "raw", times)
    transform.run(vdir)
    rows = read_rows(vdir / "out" / "climate.csv")

    row0, col0 = math.floor((90 - 44.875) / 0.25), math.floor((0.125 + 180) / 0.25)
    ids = {(row0 + r) * 1440 + col0 + c for r in (0, 1) for c in (0, 1)}
    masked = (row0 + 1) * 1440 + col0 + 1
    assert set(rows) == ids - {masked}

    full = rows[row0 * 1440 + col0]
    assert parse(full["tmin_c"]) == [10.0 + m for m in range(1, 13)]
    assert parse(full["tmax_c"]) == [20.0 + m for m in range(1, 13)]
    assert parse(full["precip_mm"]) == [50.0] * 12

    half = rows[row0 * 1440 + col0 + 1]  # only the +10 half is land
    assert parse(half["tmin_c"])[0] == 21.0


def test_real_terraclimate_climatology_metadata(tmp_path):
    """Mirror the real files: time = 1..12, units "months", calendar "months"."""
    vdir = tmp_path / "v1"
    raw = vdir / "raw"
    raw.mkdir(parents=True)
    lat, lon = fine_grid()
    time = xr.Variable("time", np.arange(1, 13, dtype="float64"),
                       {"units": "months", "calendar": "months", "description": "months since January"})
    for name, base in (("tmin", 10.0), ("tmax", 20.0), ("ppt", 50.0)):
        step = 0.0 if name == "ppt" else 1.0
        data = np.stack([np.full((12, 12), base + step * m, dtype="float32") for m in range(1, 13)])
        xr.Dataset({name: (("time", "lat", "lon"), data)}, coords={"time": time, "lat": lat, "lon": lon}) \
            .to_netcdf(raw / f"TerraClimate_19912020_{name}.nc")
    rows = read_rows(transform.run(vdir))
    first = rows[min(rows)]
    assert parse(first["tmin_c"]) == [10.0 + m for m in range(1, 13)]
    assert parse(first["precip_mm"]) == [50.0] * 12


def test_rejects_misaligned_grid(tmp_path):
    vdir = tmp_path / "v1"
    raw = vdir / "raw"
    raw.mkdir(parents=True)
    lat, lon = fine_grid()
    times = pd.date_range("2000-01-01", periods=12, freq="MS")
    for name in ("tmin", "tmax", "ppt"):
        xr.Dataset({name: (("time", "lat", "lon"), np.ones((12, 12, 12), "float32"))},
                   coords={"time": times, "lat": lat - 0.01, "lon": lon}).to_netcdf(raw / f"x_{name}.nc")
    with pytest.raises(ValueError, match="aligned"):
        transform.run(vdir)
```

- [ ] **Step 2: Run to verify failure**

```bash
python -m venv pipeline/.venv && source pipeline/.venv/Scripts/activate && pip install -r pipeline/transform/requirements-dev.txt
python -m pytest pipeline/tests -q
```
Expected: FAIL with `ModuleNotFoundError: No module named 'transform'`. (On Linux/CI activate with `source pipeline/.venv/bin/activate`.)

- [ ] **Step 3: Implement the transform**

Create `pipeline/transform/transform.py`:

```python
"""TerraClimate → 0.25° monthly normals (1991–2020) as load-ready CSV.

Usage: transform.py <version_dir>
Reads  <version_dir>/raw/*{tmin,tmax,ppt}*.nc
Writes <version_dir>/out/climate.csv  (cell_id,tmin_c,tmax_c,precip_mm)
"""
import glob
import os
import sys
from pathlib import Path

import numpy as np
import xarray as xr

GRID = 0.25
START, END = "1991-01-01", "2020-12-31"
VARS = (("tmin", "tmin_c"), ("tmax", "tmax_c"), ("ppt", "precip_mm"))


def open_var(raw: Path, var: str) -> xr.DataArray:
    """Open one variable. The TerraClimate climatology files have a non-CF
    time axis (values 1..12, units "months", calendar "months"), so a single
    file is opened without time decoding; multi-year files are decoded."""
    files = sorted(glob.glob(str(raw / f"*{var}*.nc")))
    if not files:
        raise FileNotFoundError(f"no *{var}*.nc in {raw}")
    if len(files) == 1:
        ds = xr.open_dataset(files[0], chunks={"time": 12}, decode_times=False)
        if ds.sizes["time"] != 12:
            ds = xr.open_dataset(files[0], chunks={"time": 12})
    else:
        ds = xr.open_mfdataset(files, combine="by_coords", chunks={"time": 12})
    return ds[var]


def monthly_normals(da: xr.DataArray) -> xr.DataArray:
    """12-step input is already a climatology; otherwise average 1991–2020 by month."""
    if da.sizes["time"] == 12:
        out = da.assign_coords(month=("time", np.arange(1, 13))).swap_dims({"time": "month"}).drop_vars("time")
    else:
        da = da.sel(time=slice(START, END))
        years = np.unique(da["time.year"].values)
        if len(years) != 30:
            raise ValueError(f"expected 30 years 1991–2020, found {len(years)}")
        out = da.groupby("time.month").mean("time")
    return out.transpose("month", "lat", "lon")


def coarsen(da: xr.DataArray) -> xr.DataArray:
    da = da.sortby("lat", ascending=False).sortby("lon")
    step = abs(float(da.lat[0] - da.lat[1]))
    factor = GRID / step
    if abs(factor - round(factor)) > 1e-6:
        raise ValueError(f"grid step {step} does not divide 0.25°")
    factor = int(round(factor))
    top = float(da.lat[0]) + step / 2
    left = float(da.lon[0]) - step / 2
    for edge in ((90 - top) / GRID, (left + 180) / GRID):
        if abs(edge - round(edge)) > 1e-6:
            raise ValueError("fine grid is not aligned to 0.25° cell edges")
    return da.coarsen(lat=factor, lon=factor, boundary="trim").mean(skipna=True)


def run(version_dir) -> Path:
    vdir = Path(version_dir)
    arrays, lat, lon = {}, None, None
    for var, col in VARS:
        c = coarsen(monthly_normals(open_var(vdir / "raw", var))).compute()
        arrays[col] = c.values  # (12, rows, cols)
        lat, lon = c.lat.values, c.lon.values
    rows = np.floor((90 - lat) / GRID).astype(int)
    cols = np.floor((lon + 180) / GRID).astype(int)
    stacked = np.stack([arrays[col] for _, col in VARS])  # (3, 12, R, C)
    land = ~np.isnan(stacked).any(axis=(0, 1))

    out = vdir / "out"
    out.mkdir(parents=True, exist_ok=True)
    path = out / "climate.csv"
    tmp = out / "climate.csv.tmp"
    with open(tmp, "w", newline="") as fh:
        fh.write("cell_id,tmin_c,tmax_c,precip_mm\n")
        for r, c in zip(*np.nonzero(land)):
            cell = int(rows[r]) * 1440 + int(cols[c])
            parts = ['"{' + ",".join(f"{v:.2f}" for v in arrays[col][:, r, c]) + '}"' for _, col in VARS]
            fh.write(f"{cell},{','.join(parts)}\n")
    os.replace(tmp, path)
    return path


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: transform.py <version_dir>")
    print(run(sys.argv[1]))
```

Create `pipeline/transform/Dockerfile`:

```dockerfile
FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY transform.py .
ENTRYPOINT ["python", "/app/transform.py"]
```

- [ ] **Step 4: Run pytest**

Run: `python -m pytest pipeline/tests -q`
Expected: `4 passed`.

Add `pipeline/.venv/` to `.gitignore`.

- [ ] **Step 5: Write the failing Go climate tests**

Create `internal/ingest/climate_test.go`:

```go
package ingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/dbtest"
)

func arr(vals [12]float64) string {
	parts := make([]string, 12)
	for i, v := range vals {
		parts[i] = fmt.Sprintf("%.2f", v)
	}
	return `"{` + strings.Join(parts, ",") + `}"`
}

func climateCSV(rows map[int][3][12]float64) string {
	var b strings.Builder
	b.WriteString("cell_id,tmin_c,tmax_c,precip_mm\n")
	for id, r := range rows {
		fmt.Fprintf(&b, "%d,%s,%s,%s\n", id, arr(r[0]), arr(r[1]), arr(r[2]))
	}
	return b.String()
}

func flat(v float64) (out [12]float64) {
	for i := range out {
		out[i] = v
	}
	return
}

func TestClimateExternalTransform(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	base := serveFiles(t, map[string][]byte{"a_tmin.nc": []byte("x"), "a_tmax.nc": []byte("x"), "a_ppt.nc": []byte("x")})
	ds := &Climate{SourceList: []Source{{base + "/a_tmin.nc", "a_tmin.nc"}, {base + "/a_tmax.nc", "a_tmax.nc"}, {base + "/a_ppt.nc", "a_ppt.nc"}}}
	p := newPipeline(t, pool, ds)
	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = p.Transform(ctx, v)
	var ext *ExternalTransformError
	if !errors.As(err, &ext) || !strings.Contains(ext.Cmd, fmt.Sprintf("/work/climate/v%d", v)) {
		t.Fatalf("want external transform error, got %v", err)
	}

	// Once the container has written its output, transform uploads it.
	out := filepath.Join(p.versionDir(v), "out")
	_ = os.MkdirAll(out, 0o755)
	london := climate.CellID(51.5, -0.1)
	csv := climateCSV(map[int][3][12]float64{london: {flat(7), flat(15), flat(50)}})
	if err := os.WriteFile(filepath.Join(out, "climate.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Transform(ctx, v); err != nil {
		t.Fatalf("transform with output: %v", err)
	}
	if err := p.Load(ctx, v); err != nil {
		t.Fatalf("load: %v", err)
	}
	dv, _ := p.version(ctx, v)
	var got float32
	if err := pool.QueryRow(ctx, "SELECT tmax_c[1] FROM "+dv.TableName+" WHERE cell_id = $1", london).Scan(&got); err != nil || got != 15 {
		t.Fatalf("tmax = %v %v", got, err)
	}
}

func TestClimateCheck(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	ds := &Climate{
		Checks:     []ClimateCheck{{Name: "London", Lat: 51.5, Lng: -0.1, AnnualMeanC: 11, JulyMinusJanC: 13}},
		ToleranceC: 4,
	}
	london := climate.CellID(51.5, -0.1)
	good := [3][12]float64{
		{2, 2, 4, 5, 8, 11, 13, 13, 11, 8, 5, 3},
		{8, 9, 12, 15, 18, 21, 23, 23, 20, 16, 11, 9},
		flat(50),
	}
	badPrecip := good
	badPrecip[2][3] = -1
	flipped := [3][12]float64{good[0], good[1], good[2]}
	for m := 0; m < 12; m++ { // southern-hemisphere pattern at a northern point
		flipped[0][m], flipped[1][m] = good[0][(m+6)%12], good[1][(m+6)%12]
	}

	cases := []struct {
		name string
		rows map[int][3][12]float64
		want string
	}{
		{"good", map[int][3][12]float64{london: good}, ""},
		{"negative precip", map[int][3][12]float64{london: badPrecip}, "implausible"},
		{"hemisphere flipped", map[int][3][12]float64{london: flipped}, "London"},
		{"missing cell", map[int][3][12]float64{london + 7: good}, "no land cell"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			table := fmt.Sprintf("climate_cell_vtest%d", i)
			tx, _ := pool.Begin(ctx)
			if _, err := tx.Exec(ctx, ds.CreateSQL(table)); err != nil {
				t.Fatal(err)
			}
			if err := ds.Load(ctx, tx, table, strings.NewReader(climateCSV(c.rows))); err != nil {
				t.Fatal(err)
			}
			_ = tx.Commit(ctx)
			fails := ds.Check(ctx, pool, table)
			if c.want == "" && len(fails) != 0 {
				t.Fatalf("unexpected failures %v", fails)
			}
			if c.want != "" && (len(fails) == 0 || !strings.Contains(strings.Join(fails, ";"), c.want)) {
				t.Fatalf("want failure containing %q, got %v", c.want, fails)
			}
		})
	}
}
```

- [ ] **Step 6: Implement the climate dataset**

Create `internal/ingest/climate.go`:

```go
package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/store"
)

type ClimateCheck struct {
	Name                        string
	Lat, Lng                    float64
	AnnualMeanC, JulyMinusJanC  float64
}

// Climate is TerraClimate 1991–2020 (Abatzoglou et al., 2018), CC0.
type Climate struct {
	SourceList []Source
	Checks     []ClimateCheck
	ToleranceC float64
}

// DefaultClimateBase is the THREDDS HTTP file server for the TerraClimate
// 1991–2020 climatologies (verified 2026-09-25). Override with CLIMATE_SOURCE_BASE.
const DefaultClimateBase = "http://thredds.northwestknowledge.net:8080/thredds/fileServer/TERRACLIMATE_ALL/climatology"

func NewClimate(base string) *Climate {
	if base == "" {
		base = DefaultClimateBase
	}
	var src []Source
	for _, v := range []string{"tmin", "tmax", "ppt"} {
		f := "TerraClimate_19912020_" + v + ".nc"
		src = append(src, Source{URL: strings.TrimRight(base, "/") + "/" + f, File: f})
	}
	return &Climate{
		SourceList: src,
		ToleranceC: 4,
		Checks: []ClimateCheck{
			{"London", 51.51, -0.13, 11.3, 13}, {"Moscow", 55.76, 37.62, 5.8, 25},
			{"Yakutsk", 62.03, 129.73, -8.8, 58}, {"Anchorage", 61.22, -149.90, 3.0, 23},
			{"Cairo", 30.04, 31.24, 22.0, 14}, {"Delhi", 28.61, 77.21, 25.0, 17},
			{"Singapore", 1.35, 103.82, 27.5, 0}, {"Nairobi", -1.29, 36.82, 18.0, 0},
			{"Sydney", -33.87, 151.21, 18.3, -10}, {"Buenos Aires", -34.60, -58.38, 18.0, -13},
			{"Cape Town", -33.92, 18.42, 16.8, -8}, {"Ushuaia", -54.80, -68.30, 5.8, -8},
		},
	}
}

func (*Climate) Name() string        { return "climate" }
func (*Climate) View() string        { return "climate_cell" }
func (*Climate) Prefix() string      { return "climate_cell_v" }
func (*Climate) License() string     { return "CC0 1.0 — TerraClimate (Abatzoglou et al., 2018); citation requested" }
func (c *Climate) Sources() []Source { return c.SourceList }

// Transform copies the output of the Python transform container.
func (*Climate) Transform(_ context.Context, dir string, w io.Writer) error {
	f, err := os.Open(filepath.Join(dir, "out", "climate.csv"))
	if errors.Is(err, os.ErrNotExist) {
		return &ExternalTransformError{Cmd: "docker compose -f deploy/compose.yaml run --rm transform /work/climate/" + filepath.Base(dir)}
	}
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func (*Climate) CreateSQL(t string) string {
	return fmt.Sprintf(`CREATE TABLE %s (
  cell_id   integer PRIMARY KEY,
  tmin_c    real[12] NOT NULL,
  tmax_c    real[12] NOT NULL,
  precip_mm real[12] NOT NULL
)`, t)
}

func (*Climate) Load(ctx context.Context, tx pgx.Tx, t string, r io.Reader) error {
	_, err := tx.Conn().PgConn().CopyFrom(ctx, r,
		"COPY "+t+" (cell_id, tmin_c, tmax_c, precip_mm) FROM STDIN WITH (FORMAT csv, HEADER true)")
	return err
}

func (c *Climate) Check(ctx context.Context, db store.DBTX, t string) []string {
	var fails []string
	var bad int
	err := db.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s c WHERE
		c.cell_id < 0 OR c.cell_id >= 1036800
		OR cardinality(c.tmin_c) <> 12 OR cardinality(c.tmax_c) <> 12 OR cardinality(c.precip_mm) <> 12
		OR EXISTS (SELECT 1 FROM unnest(c.tmin_c, c.tmax_c, c.precip_mm) AS u(lo, hi, p)
		           WHERE lo IS NULL OR hi IS NULL OR p IS NULL
		              OR lo < -80 OR lo > 60 OR hi < -80 OR hi > 60 OR lo > hi OR p < 0)`, t)).Scan(&bad)
	if err != nil {
		return []string{"plausibility query: " + err.Error()}
	}
	if bad > 0 {
		fails = append(fails, fmt.Sprintf("%d cells with implausible values", bad))
	}
	for _, chk := range c.Checks {
		if msg := c.spotCheck(ctx, db, t, chk); msg != "" {
			fails = append(fails, msg)
		}
	}
	return fails
}

func (c *Climate) spotCheck(ctx context.Context, db store.DBTX, t string, chk ClimateCheck) string {
	own := climate.CellID(chk.Lat, chk.Lng)
	ids := []int32{int32(own)}
	for _, id := range climate.NeighborCells(own) {
		ids = append(ids, int32(id))
	}
	rows, err := store.CellsIn(ctx, db, t, ids)
	if err != nil {
		return fmt.Sprintf("spot check %s: %v", chk.Name, err)
	}
	cells, err := climate.CellsToNormals(rows)
	if err != nil {
		return fmt.Sprintf("spot check %s: %v", chk.Name, err)
	}
	id, ok := climate.PickNearest(chk.Lat, chk.Lng, own, cells)
	if !ok {
		return fmt.Sprintf("spot check %s: no land cell", chk.Name)
	}
	n := cells[id]
	var annual float64
	for m := 0; m < 12; m++ {
		annual += n.Mean(m) / 12
	}
	seasonal := n.Mean(6) - n.Mean(0)
	switch {
	case math.Abs(annual-chk.AnnualMeanC) > c.ToleranceC:
		return fmt.Sprintf("spot check %s: annual mean %.1f °C, expected %.1f ± %.1f", chk.Name, annual, chk.AnnualMeanC, c.ToleranceC)
	case math.Abs(chk.JulyMinusJanC) < 4 && math.Abs(seasonal) > 4:
		return fmt.Sprintf("spot check %s: July−January %.1f °C, expected near 0", chk.Name, seasonal)
	case math.Abs(chk.JulyMinusJanC) >= 4 && (seasonal*chk.JulyMinusJanC <= 0 || math.Abs(seasonal) < math.Abs(chk.JulyMinusJanC)/2):
		return fmt.Sprintf("spot check %s: July−January %.1f °C, expected about %.1f", chk.Name, seasonal, chk.JulyMinusJanC)
	}
	return ""
}
```

Register it in `cmd/ingest/main.go` `defaultDatasets`:

```go
		"climate": func() ingest.Dataset { return ingest.NewClimate(getenv("CLIMATE_SOURCE_BASE")) },
```

- [ ] **Step 7: Run all ingestion tests and the transform image build**

Run: `go test ./internal/ingest/ ./cmd/ingest/ -count=1 && python -m pytest pipeline/tests -q && docker build -t thymeandplace-transform pipeline/transform`
Expected: Go `ok`, `4 passed`, image builds.

- [ ] **Step 8: Commit**

```bash
git add .gitignore pipeline internal/ingest cmd/ingest
git commit -m "feat(ingest): TerraClimate normals via Python/xarray transform and spot checks

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Plan completion check

- [ ] `go vet ./... && go test ./... -count=1` passes (Docker running). `python -m pytest pipeline/tests` passes.
- [ ] Manual smoke against the dev DB (`deploy/compose.dev.yaml`), with `BLOB_DIR=./.blob INGEST_WORKDIR=./.work` (add both to `.gitignore`): run `go run ./cmd/ingest places fetch`, then `approve-license --version N --yes`, then `all --version N`. `SELECT count(*) FROM place` returns more than 100,000 rows. **Before approving, read the GeoNames license text printed by `fetch`.**
- [ ] For ocean, before approving: the Esri Oceans item (`https://oceans-esrioceans.hub.arcgis.com/datasets/EsriOceans::meow`) states no licence. Confirm the terms with the original MEOW publishers (TNC/WWF) and record them. If the service URL changes, set `OCEAN_SOURCE_URL`.
