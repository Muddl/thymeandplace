# Thyme & Place — Plan 2: Database Foundation and Catalog Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up Postgres/PostGIS with goose migrations, a disposable-database test harness, sqlc-generated queries, the reference-data and catalog schemas, trigger-based catalog history and versioning, loading the catalog into an atomically swapped snapshot, and the data-version hash.

**Architecture:** Migrations are embedded SQL files run by goose's Provider API. `internal/dbtest` starts one PostGIS container per test binary, migrates a template database once, and gives each test a fresh clone. `internal/store` is sqlc-generated from the migrations plus hand-written query files. `catalog.Write` and `catalog.Load` translate between the domain types from Plan 1 and the generated rows. `catalog.Holder` keeps the live snapshot and never replaces it with a failed load. `dataversion.Compute` hashes the catalog version and the live dataset IDs.

**Tech Stack:** Go ≥ 1.25, `github.com/jackc/pgx/v5` (+ `pgxpool`, `stdlib`), `github.com/pressly/goose/v3`, sqlc 1.29.0 (run via Docker), `github.com/testcontainers/testcontainers-go/modules/postgres`, Docker image `postgis/postgis:17-3.5`.

**Spec:** `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (§6.1–§6.4, §8.1–§8.4, §13.3, §13.5, §14)

**Depends on:** Plan 1 (`catalog`, `catalogtest`, `climate`, `ocean`).

## Global Constraints

- Postgres image everywhere (dev, tests, prod): `postgis/postgis:17-3.5`. Extensions: `postgis`, `pg_trgm`.
- Migrations: goose SQL files in `migrations/`, embedded via `migrations.FS`, numbered `00001_…`. Plan 4 adds `00005`, Plan 6 adds `00006`.
- sqlc 1.29.0 via `scripts/sqlc.sh` (Docker image `sqlc/sqlc:1.29.0`). Generated files in `internal/store` are committed. **If sqlc generates a different name for a field or type than this plan shows, the generated name is authoritative; adjust the calling code, not the generated code.**
- sqlc does not model PostGIS or `pg_trgm` functions. Queries that need them (ocean, places; Plan 3) are hand-written pgx in `internal/store/geo.go`. Every other query goes through sqlc.
- **Spec fix:** the spec's per-kind `CHECK`s (§8.1) let NULLs through, because `NULL > x` is NULL and a NULL `CHECK` passes. The migration adds explicit `IS NOT NULL` to every required per-kind column. App validation (Plan 1) already requires them.
- `catalog_history.actor` comes from `current_setting('app.actor', true)`, falling back to `'sql'`. `change_id` comes from `app.change_id`. Optional JSON from `app.meta` is merged into `after` under key `_meta`.
- `NOTIFY` channel name: `data_version_changed`.
- Data version = first 16 hex characters of SHA-256 over `catalog:<version>;datasets:<id,id,...>`, with IDs ascending (§8.3).
- Local dev DB: `postgres://tp:tp@localhost:55432/thymeandplace?sslmode=disable`, from env `DATABASE_URL`.
- Tests that need Docker call `dbtest.New(t)`. Setting `TP_SKIP_DB=1` skips them.
- Commit after every task. Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Real-number round trip**: a cook-facing value like `16.1` °C stored in a `real` column comes back as `16.100000381…` unless normalised, so every preview would show a phantom change and exports would drift. After save/load, `16.1` must stay `16.1`. Pinned in Task 3.
2. **Unchanged upsert**: saving an ingredient identical to what is stored must write no `catalog_history` rows, or history fills with noise on every import. Pinned in Task 5.
3. **Deleting an ingredient other ingredients pair with**: the cascade removes those pairings, records history rows for them, and the reloaded snapshot is valid. Pinned in Task 5.
4. **Reload while the database is unreachable**: `Holder.Reload` returns an error and the previous snapshot stays live. Pinned in Task 6.
5. **Rolled-back write**: a transaction that bumps the version and then rolls back must not deliver `NOTIFY data_version_changed`. Pinned in Task 5.

---

## File Structure

```
deploy/compose.dev.yaml             # local Postgres/PostGIS
scripts/sqlc.sh                     # runs sqlc in Docker (Git Bash + Linux)
sqlc.yaml
migrations/embed.go                 # package migrations; FS
migrations/00001_extensions.sql
migrations/00002_reference.sql      # dataset_versions, *_v0 tables, views
migrations/00003_catalog.sql        # enums, ingredient, pairing, seafood_region, catalog_meta
migrations/00004_catalog_history.sql# catalog_history + triggers
internal/db/db.go                   # Open, Migrate
internal/dbtest/dbtest.go           # New(t) → fresh migrated database
internal/dbtest/dbtest_test.go
internal/store/queries/reference.sql
internal/store/queries/catalog.sql
internal/store/*.go                 # sqlc output (generated)
internal/catalog/normalize.go       # Normalize, float32 rounding
internal/catalog/normalize_test.go
internal/catalog/db.go              # Write, Load
internal/catalog/db_test.go
internal/catalog/constraints_test.go
internal/catalog/history_test.go
internal/catalog/holder.go          # Holder
internal/catalog/holder_test.go
internal/dataversion/dataversion.go # Compute, Current
internal/dataversion/dataversion_test.go
```

---

### Task 1: Dev database, migrations runner and test harness

**Files:**
- Create: `deploy/compose.dev.yaml`, `migrations/embed.go`, `migrations/00001_extensions.sql`, `internal/db/db.go`, `internal/dbtest/dbtest.go`, `internal/dbtest/dbtest_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `migrations.FS embed.FS` (all `*.sql` at the package root)
  - `func db.Open(ctx context.Context, url string) (*pgxpool.Pool, error)`: connects and pings
  - `func db.Migrate(ctx context.Context, url string) error`: runs every pending goose migration
  - `const dbtest.Image = "postgis/postgis:17-3.5"`
  - `func dbtest.New(t testing.TB) *pgxpool.Pool`: a fresh, fully migrated database, dropped on cleanup

- [ ] **Step 1: Add dependencies and the dev compose file**

```bash
go get github.com/jackc/pgx/v5@latest github.com/pressly/goose/v3@latest \
  github.com/testcontainers/testcontainers-go@latest github.com/testcontainers/testcontainers-go/modules/postgres@latest
mkdir -p deploy migrations internal/db internal/dbtest
```

Create `deploy/compose.dev.yaml`:

```yaml
name: thymeandplace-dev
services:
  db:
    image: postgis/postgis:17-3.5
    environment:
      POSTGRES_USER: tp
      POSTGRES_PASSWORD: tp
      POSTGRES_DB: thymeandplace
    ports:
      - "55432:5432"
    volumes:
      - dbdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U tp -d thymeandplace"]
      interval: 5s
      retries: 20
volumes:
  dbdata: {}
```

- [ ] **Step 2: Write the first migration and the embed package**

Create `migrations/00001_extensions.sql`:

```sql
-- +goose Up
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- +goose Down
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS postgis;
```

Create `migrations/embed.go`:

```go
// Package migrations embeds the goose SQL migrations.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

- [ ] **Step 3: Write the failing harness test**

Create `internal/dbtest/dbtest_test.go`:

```go
package dbtest_test

import (
	"context"
	"testing"

	"thymeandplace/internal/dbtest"
)

func TestNewGivesMigratedIsolatedDatabases(t *testing.T) {
	ctx := context.Background()
	a := dbtest.New(t)
	b := dbtest.New(t)

	var v string
	if err := a.QueryRow(ctx, "SELECT postgis_version()").Scan(&v); err != nil {
		t.Fatalf("postgis missing: %v", err)
	}
	if _, err := a.Exec(ctx, "CREATE TABLE only_in_a (x int)"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := b.QueryRow(ctx, "SELECT to_regclass('only_in_a') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("databases are not isolated")
	}
	var sim float64
	if err := a.QueryRow(ctx, "SELECT similarity('london','londn')").Scan(&sim); err != nil || sim <= 0 {
		t.Fatalf("pg_trgm missing: %v %v", sim, err)
	}
}
```

- [ ] **Step 4: Run to verify failure**

Run: `go test ./internal/dbtest/`
Expected: FAIL to compile: `undefined: dbtest.New`.

- [ ] **Step 5: Implement `db` and `dbtest`**

Create `internal/db/db.go`:

```go
// Package db opens the Postgres pool and runs migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver for goose
	"github.com/pressly/goose/v3"

	"thymeandplace/migrations"
)

// Open connects a pool and verifies it with a ping.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, url string) error {
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer sqldb.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sqldb, migrations.FS)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	defer p.Close()
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
```

Create `internal/dbtest/dbtest.go`:

```go
// Package dbtest gives each test a fresh, migrated Postgres/PostGIS database.
// One container starts per test binary; a template database is migrated once
// and cloned per test.
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"thymeandplace/internal/db"
)

const Image = "postgis/postgis:17-3.5"

var (
	once     sync.Once
	adminURL string
	startErr error
	seq      atomic.Int64
	createMu sync.Mutex
)

func start() {
	ctx := context.Background()
	c, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("tp_admin"),
		postgres.WithUsername("tp"),
		postgres.WithPassword("tp"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		startErr = fmt.Errorf("start container: %w", err)
		return
	}
	// The container is reaped by testcontainers when the test binary exits.
	adminURL, err = c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		startErr = err
		return
	}
	if err := exec(ctx, adminURL, "CREATE DATABASE tp_template"); err != nil {
		startErr = err
		return
	}
	if err := db.Migrate(ctx, withDB(adminURL, "tp_template")); err != nil {
		startErr = err
	}
}

// New returns a pool on a brand-new migrated database. It is dropped when
// the test ends.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("TP_SKIP_DB") == "1" {
		t.Skip("TP_SKIP_DB=1")
	}
	once.Do(start)
	if startErr != nil {
		t.Fatalf("dbtest: %v", startErr)
	}
	ctx := context.Background()
	name := fmt.Sprintf("t%d_%d", os.Getpid(), seq.Add(1))

	createMu.Lock()
	err := exec(ctx, adminURL, "CREATE DATABASE "+name+" TEMPLATE tp_template")
	createMu.Unlock()
	if err != nil {
		t.Fatalf("dbtest: create database: %v", err)
	}
	pool, err := pgxpool.New(ctx, withDB(adminURL, name))
	if err != nil {
		t.Fatalf("dbtest: pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_ = exec(context.Background(), adminURL, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return pool
}

func exec(ctx context.Context, dsn, sql string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, sql)
	return err
}

func withDB(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + name
	return u.String()
}
```

- [ ] **Step 6: Run the harness test (Docker Desktop must be running)**

Run: `go mod tidy && go test ./internal/dbtest/ -count=1 -v`
Expected: `--- PASS: TestNewGivesMigratedIsolatedDatabases`. The first run pulls `postgis/postgis:17-3.5`, which can take a few minutes.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum deploy/compose.dev.yaml migrations internal/db internal/dbtest
git commit -m "feat(db): goose migrations, pool helper and per-test PostGIS databases

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Reference-data schema and sqlc bootstrap

**Files:**
- Create: `migrations/00002_reference.sql`, `sqlc.yaml`, `scripts/sqlc.sh`, `internal/store/queries/reference.sql`, `internal/store/reference_test.go`
- Generated: `internal/store/db.go`, `internal/store/models.go`, `internal/store/reference.sql.go`

**Interfaces:**
- Consumes: `dbtest.New`.
- Produces:
  - Tables `dataset_versions`, `climate_cell_v0`, `ocean_ecoregion_v0`, `place_v0` and views `climate_cell`, `ocean_ecoregion`, `place` (schemas as spec §6.1–§6.4, plus `dataset_versions.promoted_at` and a trigram index on `place.name`)
  - A partial unique index allowing at most one `is_live` row per dataset
  - `store.New(db store.DBTX) *store.Queries`
  - `(*store.Queries).ListLiveDatasetIDs(ctx) ([]int64, error)`

- [ ] **Step 1: Write the reference migration**

Create `migrations/00002_reference.sql`:

```sql
-- +goose Up
CREATE TABLE dataset_versions (
  id               bigserial PRIMARY KEY,
  dataset          text NOT NULL CHECK (dataset IN ('climate','ocean','places')),
  table_name       text NOT NULL,
  source_url       text NOT NULL,
  source_checksum  text NOT NULL,
  license          text NOT NULL,
  license_reviewed boolean NOT NULL DEFAULT false,
  loaded_at        timestamptz NOT NULL DEFAULT now(),
  is_live          boolean NOT NULL DEFAULT false,
  -- Set the first time a version goes live; rollback only returns to promoted versions.
  promoted_at      timestamptz
);
CREATE UNIQUE INDEX dataset_versions_one_live ON dataset_versions (dataset) WHERE is_live;

-- Version 0 tables are empty placeholders so the views exist from the start.
-- Ingestion (Plan 3) creates *_v{N} tables and repoints the views.
CREATE TABLE climate_cell_v0 (
  cell_id   integer PRIMARY KEY,
  tmin_c    real[12] NOT NULL,
  tmax_c    real[12] NOT NULL,
  precip_mm real[12] NOT NULL
);
CREATE VIEW climate_cell AS SELECT * FROM climate_cell_v0;

CREATE TABLE ocean_ecoregion_v0 (
  ecoregion_id integer PRIMARY KEY,
  name         text NOT NULL,
  geom         geography(MultiPolygon, 4326) NOT NULL
);
CREATE INDEX ON ocean_ecoregion_v0 USING gist (geom);
CREATE VIEW ocean_ecoregion AS SELECT * FROM ocean_ecoregion_v0;

CREATE TABLE place_v0 (
  place_id   integer PRIMARY KEY,
  name       text NOT NULL,
  ascii_name text NOT NULL,
  admin1     text,
  country    text NOT NULL,
  population integer NOT NULL,
  geom       geography(Point, 4326) NOT NULL
);
CREATE INDEX ON place_v0 USING gin (ascii_name gin_trgm_ops);
-- Not in spec §6.3: lets accented queries ("Zürich") match the native name.
CREATE INDEX ON place_v0 USING gin (name gin_trgm_ops);
CREATE INDEX ON place_v0 USING gist (geom);
CREATE VIEW place AS SELECT * FROM place_v0;

-- +goose Down
DROP VIEW place;
DROP TABLE place_v0;
DROP VIEW ocean_ecoregion;
DROP TABLE ocean_ecoregion_v0;
DROP VIEW climate_cell;
DROP TABLE climate_cell_v0;
DROP TABLE dataset_versions;
```

- [ ] **Step 2: Configure sqlc**

Create `sqlc.yaml`:

```yaml
version: "2"
sql:
  - engine: postgresql
    schema: migrations
    queries: internal/store/queries
    gen:
      go:
        package: store
        out: internal/store
        sql_package: pgx/v5
```

Create `scripts/sqlc.sh`:

```bash
#!/usr/bin/env bash
# Runs sqlc 1.29.0 in Docker from the repo root. Works in Git Bash and on Linux.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
if command -v cygpath >/dev/null 2>&1; then
  root="$(cygpath -m "$root")"
  export MSYS_NO_PATHCONV=1
fi
docker run --rm -v "$root:/src" -w /src sqlc/sqlc:1.29.0 "${@:-generate}"
```

```bash
chmod +x scripts/sqlc.sh
mkdir -p internal/store/queries
```

Create `internal/store/queries/reference.sql`:

```sql
-- name: ListLiveDatasetIDs :many
SELECT id FROM dataset_versions WHERE is_live ORDER BY id;
```

- [ ] **Step 3: Generate**

Run: `./scripts/sqlc.sh generate`
Expected: no output; `internal/store/db.go`, `models.go` and `reference.sql.go` exist. Run `go build ./internal/store/`: it compiles.

- [ ] **Step 4: Write the failing schema test**

Create `internal/store/reference_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

func TestReferenceSchema(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	for _, view := range []string{"climate_cell", "ocean_ecoregion", "place"} {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+view).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: n=%d err=%v", view, n, err)
		}
	}
	q := store.New(pool)
	ids, err := q.ListLiveDatasetIDs(ctx)
	if err != nil || len(ids) != 0 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}

	ins := `INSERT INTO dataset_versions (dataset, table_name, source_url, source_checksum, license, is_live)
	        VALUES ('places','place_v1','u','c','CC BY 4.0', $1) RETURNING id`
	var live int64
	if err := pool.QueryRow(ctx, ins, true).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, ins, true); err == nil {
		t.Fatal("second live row for the same dataset must be rejected")
	}
	ids, _ = q.ListLiveDatasetIDs(ctx)
	if len(ids) != 1 || ids[0] != live {
		t.Fatalf("ids = %v, want [%d]", ids, live)
	}
}
```

- [ ] **Step 5: Run the test**

Run: `go test ./internal/store/ -count=1`
Expected: `ok`. It passes as soon as the migration and generated code exist; it guards the schema from regressions.

- [ ] **Step 6: Commit**

```bash
git add migrations/00002_reference.sql sqlc.yaml scripts/sqlc.sh internal/store
git commit -m "feat(store): reference-data schema with versioned tables and views; sqlc setup

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Catalog schema, normalisation, Write and Load

**Files:**
- Create: `migrations/00003_catalog.sql`, `internal/store/queries/catalog.sql`, `internal/catalog/normalize.go`, `internal/catalog/normalize_test.go`, `internal/catalog/db.go`, `internal/catalog/db_test.go`
- Generated: `internal/store/catalog.sql.go`, updated `models.go`

**Interfaces:**
- Consumes: `catalog.Ingredient`, `catalog.NewSnapshot`, `catalogtest.SmallCatalog` (Plan 1); `store.New`, `dbtest.New`.
- Produces:
  - Tables `ingredient`, `ingredient_pairing`, `seafood_region`, `catalog_meta` (one row, version 0)
  - sqlc queries: `GetCatalogVersion`, `ListIngredients`, `ListPairings`, `ListSeafoodRegions`, `UpsertIngredient`, `DeleteIngredient`, `PrunePairings`, `AddPairing`, `PruneSeafoodRegions`, `UpsertSeafoodRegion`
  - `func catalog.Normalize(ing Ingredient) Ingredient`: rounds every real field through float32 (shortest decimal), sorts pairings, sorts regions by ecoregion ID and their months ascending, and turns empty slices into nil
  - `func catalog.Write(ctx context.Context, q *store.Queries, upserts []Ingredient, deletes []string) error`: upserts every ingredient row first, then reconciles pairings and regions, then deletes. Unchanged rows are not rewritten. Run it inside a transaction (`store.New(tx)`).
  - `type catalog.TxBeginner interface { BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) }`
  - `func catalog.Load(ctx context.Context, db TxBeginner) (*Snapshot, error)`: reads all three tables and the version in one read-only repeatable-read transaction

- [ ] **Step 1: Write the catalog migration**

Create `migrations/00003_catalog.sql`:

```sql
-- +goose Up
CREATE TYPE ingredient_category AS ENUM ('fruit','vegetable','herb','mushroom','seafood');
CREATE TYPE ingredient_kind AS ENUM
  ('annual','perennial_fruit','cultivated_year_round','wild_mushroom','seafood');
CREATE TYPE frost_tolerance AS ENUM ('tender','hardy');

-- Per-kind CHECKs include explicit IS NOT NULL: without them a NULL makes
-- the CHECK evaluate to NULL, which Postgres treats as passing.
CREATE TABLE ingredient (
  slug       text PRIMARY KEY CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
  name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  category   ingredient_category NOT NULL,
  kind       ingredient_kind NOT NULL,
  frost                   frost_tolerance,
  grow_temp_min_c         real,
  grow_temp_max_c         real,
  months_to_harvest       smallint,
  storage_months          smallint NOT NULL DEFAULT 0 CHECK (storage_months BETWEEN 0 AND 9),
  chill_months_min        smallint,
  warmup_temp_c           real,
  months_bloom_to_harvest smallint,
  harvest_span_months     smallint,
  fruiting_temp_min_c     real,
  fruiting_temp_max_c     real,
  min_precip_mm           real,
  tip_choose text NOT NULL,
  tip_store  text NOT NULL,
  note       text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now(),

  CHECK ((kind = 'seafood') = (category = 'seafood')),
  CHECK (kind <> 'wild_mushroom' OR category = 'mushroom'),
  CHECK (kind <> 'annual' OR (
    frost IS NOT NULL
    AND grow_temp_min_c IS NOT NULL AND grow_temp_max_c IS NOT NULL
    AND grow_temp_max_c > grow_temp_min_c
    AND months_to_harvest IS NOT NULL AND months_to_harvest BETWEEN 1 AND 11)),
  CHECK (kind <> 'perennial_fruit' OR (
    frost IS NOT NULL
    AND chill_months_min IS NOT NULL AND chill_months_min BETWEEN 0 AND 12
    AND warmup_temp_c IS NOT NULL
    AND months_bloom_to_harvest IS NOT NULL AND months_bloom_to_harvest BETWEEN 1 AND 11
    AND harvest_span_months IS NOT NULL AND harvest_span_months BETWEEN 1 AND 12)),
  CHECK (kind <> 'wild_mushroom' OR (
    fruiting_temp_min_c IS NOT NULL AND fruiting_temp_max_c IS NOT NULL
    AND fruiting_temp_max_c > fruiting_temp_min_c
    AND min_precip_mm IS NOT NULL AND min_precip_mm >= 0))
);

CREATE TABLE ingredient_pairing (
  slug       text REFERENCES ingredient ON DELETE CASCADE,
  pairs_with text REFERENCES ingredient ON DELETE CASCADE,
  PRIMARY KEY (slug, pairs_with),
  CHECK (slug <> pairs_with)
);

CREATE TABLE seafood_region (
  slug         text REFERENCES ingredient ON DELETE CASCADE,
  ecoregion_id integer NOT NULL,
  months       smallint[] NOT NULL CHECK (cardinality(months) BETWEEN 1 AND 12
                                          AND months <@ ARRAY[1,2,3,4,5,6,7,8,9,10,11,12]::smallint[]),
  peak_months  smallint[] NOT NULL DEFAULT '{}' CHECK (peak_months <@ months),
  PRIMARY KEY (slug, ecoregion_id)
);

CREATE TABLE catalog_meta (
  id      boolean PRIMARY KEY DEFAULT true CHECK (id),
  version bigint NOT NULL DEFAULT 0
);
INSERT INTO catalog_meta DEFAULT VALUES;

-- +goose Down
DROP TABLE catalog_meta;
DROP TABLE seafood_region;
DROP TABLE ingredient_pairing;
DROP TABLE ingredient;
DROP TYPE frost_tolerance;
DROP TYPE ingredient_kind;
DROP TYPE ingredient_category;
```

- [ ] **Step 2: Write the catalog queries and generate**

Create `internal/store/queries/catalog.sql`:

```sql
-- name: GetCatalogVersion :one
SELECT version FROM catalog_meta WHERE id;

-- name: ListIngredients :many
SELECT * FROM ingredient ORDER BY slug;

-- name: ListPairings :many
SELECT slug, pairs_with FROM ingredient_pairing ORDER BY slug, pairs_with;

-- name: ListSeafoodRegions :many
SELECT slug, ecoregion_id, months, peak_months FROM seafood_region ORDER BY slug, ecoregion_id;

-- name: UpsertIngredient :exec
INSERT INTO ingredient (
  slug, name, category, kind, frost, grow_temp_min_c, grow_temp_max_c, months_to_harvest,
  storage_months, chill_months_min, warmup_temp_c, months_bloom_to_harvest, harvest_span_months,
  fruiting_temp_min_c, fruiting_temp_max_c, min_precip_mm, tip_choose, tip_store, note
) VALUES (
  @slug, @name, @category, @kind, @frost, @grow_temp_min_c, @grow_temp_max_c, @months_to_harvest,
  @storage_months, @chill_months_min, @warmup_temp_c, @months_bloom_to_harvest, @harvest_span_months,
  @fruiting_temp_min_c, @fruiting_temp_max_c, @min_precip_mm, @tip_choose, @tip_store, @note
)
ON CONFLICT (slug) DO UPDATE SET
  name = EXCLUDED.name, category = EXCLUDED.category, kind = EXCLUDED.kind, frost = EXCLUDED.frost,
  grow_temp_min_c = EXCLUDED.grow_temp_min_c, grow_temp_max_c = EXCLUDED.grow_temp_max_c,
  months_to_harvest = EXCLUDED.months_to_harvest, storage_months = EXCLUDED.storage_months,
  chill_months_min = EXCLUDED.chill_months_min, warmup_temp_c = EXCLUDED.warmup_temp_c,
  months_bloom_to_harvest = EXCLUDED.months_bloom_to_harvest, harvest_span_months = EXCLUDED.harvest_span_months,
  fruiting_temp_min_c = EXCLUDED.fruiting_temp_min_c, fruiting_temp_max_c = EXCLUDED.fruiting_temp_max_c,
  min_precip_mm = EXCLUDED.min_precip_mm, tip_choose = EXCLUDED.tip_choose, tip_store = EXCLUDED.tip_store,
  note = EXCLUDED.note, updated_at = now()
WHERE (ingredient.name, ingredient.category, ingredient.kind, ingredient.frost,
       ingredient.grow_temp_min_c, ingredient.grow_temp_max_c, ingredient.months_to_harvest,
       ingredient.storage_months, ingredient.chill_months_min, ingredient.warmup_temp_c,
       ingredient.months_bloom_to_harvest, ingredient.harvest_span_months,
       ingredient.fruiting_temp_min_c, ingredient.fruiting_temp_max_c, ingredient.min_precip_mm,
       ingredient.tip_choose, ingredient.tip_store, ingredient.note)
  IS DISTINCT FROM
      (EXCLUDED.name, EXCLUDED.category, EXCLUDED.kind, EXCLUDED.frost,
       EXCLUDED.grow_temp_min_c, EXCLUDED.grow_temp_max_c, EXCLUDED.months_to_harvest,
       EXCLUDED.storage_months, EXCLUDED.chill_months_min, EXCLUDED.warmup_temp_c,
       EXCLUDED.months_bloom_to_harvest, EXCLUDED.harvest_span_months,
       EXCLUDED.fruiting_temp_min_c, EXCLUDED.fruiting_temp_max_c, EXCLUDED.min_precip_mm,
       EXCLUDED.tip_choose, EXCLUDED.tip_store, EXCLUDED.note);

-- name: DeleteIngredient :execrows
DELETE FROM ingredient WHERE slug = @slug;

-- name: PrunePairings :exec
DELETE FROM ingredient_pairing
WHERE slug = @slug AND NOT (pairs_with = ANY(coalesce(@keep::text[], '{}'::text[])));

-- name: AddPairing :exec
INSERT INTO ingredient_pairing (slug, pairs_with) VALUES (@slug, @pairs_with)
ON CONFLICT DO NOTHING;

-- name: PruneSeafoodRegions :exec
DELETE FROM seafood_region
WHERE slug = @slug AND NOT (ecoregion_id = ANY(coalesce(@keep::int[], '{}'::int[])));

-- name: UpsertSeafoodRegion :exec
INSERT INTO seafood_region (slug, ecoregion_id, months, peak_months)
VALUES (@slug, @ecoregion_id, @months, @peak_months)
ON CONFLICT (slug, ecoregion_id) DO UPDATE SET months = EXCLUDED.months, peak_months = EXCLUDED.peak_months
WHERE (seafood_region.months, seafood_region.peak_months) IS DISTINCT FROM (EXCLUDED.months, EXCLUDED.peak_months);
```

Run: `./scripts/sqlc.sh generate && go build ./internal/store/`
Expected: compiles. Open `internal/store/models.go` and confirm these generated types, which `db.go` below relies on: `Ingredient` (with `Frost NullFrostTolerance`, `GrowTempMinC pgtype.Float4`, `MonthsToHarvest pgtype.Int2`, `StorageMonths int16`, `MinPrecipMm pgtype.Float4`), `IngredientCategory`, `IngredientKind`, `FrostTolerance`, and `UpsertIngredientParams` in `catalog.sql.go`. If a name differs, use the generated one in Step 5.

- [ ] **Step 3: Write failing normalisation tests**

Create `internal/catalog/normalize_test.go`:

```go
package catalog_test

import (
	"reflect"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
)

func TestNormalizeRoundsThroughFloat32(t *testing.T) {
	i := catalogtest.Annual()
	i.GrowTempMinC = catalogtest.P(16.123456789)
	n := catalog.Normalize(i)
	if *n.GrowTempMinC != 16.123457 {
		t.Fatalf("got %v, want 16.123457", *n.GrowTempMinC)
	}
	i.GrowTempMinC = catalogtest.P(16.1)
	if n := catalog.Normalize(i); *n.GrowTempMinC != 16.1 {
		t.Fatalf("16.1 became %v", *n.GrowTempMinC)
	}
}

func TestNormalizeSortsAndNils(t *testing.T) {
	i := catalogtest.Seafood()
	i.Pairings = []string{"potato", "dill", "butter"}
	i.Regions = []catalog.SeafoodRegion{
		{EcoregionID: 30, Months: []int{11, 10}, PeakMonths: []int{}},
		{EcoregionID: 25, Months: []int{4, 1, 3, 2}, PeakMonths: []int{3, 2}},
	}
	n := catalog.Normalize(i)
	if !reflect.DeepEqual(n.Pairings, []string{"butter", "dill", "potato"}) {
		t.Fatalf("pairings = %v", n.Pairings)
	}
	if n.Regions[0].EcoregionID != 25 || !reflect.DeepEqual(n.Regions[0].Months, []int{1, 2, 3, 4}) ||
		!reflect.DeepEqual(n.Regions[0].PeakMonths, []int{2, 3}) || n.Regions[1].PeakMonths != nil {
		t.Fatalf("regions = %+v", n.Regions)
	}
	empty := catalogtest.Annual()
	empty.Pairings = []string{}
	if catalog.Normalize(empty).Pairings != nil {
		t.Fatal("empty pairings should normalise to nil")
	}
	// The input is not modified.
	if i.Pairings[0] != "potato" {
		t.Fatal("Normalize mutated its input")
	}
}
```

- [ ] **Step 4: Run to verify failure**

Run: `go test ./internal/catalog/ -run Normalize`
Expected: FAIL to compile: `undefined: catalog.Normalize`.

- [ ] **Step 5: Implement Normalize, Write and Load**

Create `internal/catalog/normalize.go`:

```go
package catalog

import (
	"slices"
	"sort"
	"strconv"
)

// Normalize puts an ingredient in canonical form so that values survive a
// database round trip unchanged and equal ingredients compare equal:
// real fields are rounded to float32 precision (shortest decimal form),
// pairings and regions are sorted, and empty slices become nil.
func Normalize(ing Ingredient) Ingredient {
	ing = clone(ing)
	for _, p := range []**float64{
		&ing.GrowTempMinC, &ing.GrowTempMaxC, &ing.WarmupTempC,
		&ing.FruitingTempMinC, &ing.FruitingTempMaxC, &ing.MinPrecipMM,
	} {
		if *p != nil {
			v := Real32(**p)
			*p = &v
		}
	}
	slices.Sort(ing.Pairings)
	if len(ing.Pairings) == 0 {
		ing.Pairings = nil
	}
	sort.Slice(ing.Regions, func(i, j int) bool { return ing.Regions[i].EcoregionID < ing.Regions[j].EcoregionID })
	for i := range ing.Regions {
		slices.Sort(ing.Regions[i].Months)
		slices.Sort(ing.Regions[i].PeakMonths)
		if len(ing.Regions[i].Months) == 0 {
			ing.Regions[i].Months = nil
		}
		if len(ing.Regions[i].PeakMonths) == 0 {
			ing.Regions[i].PeakMonths = nil
		}
	}
	if len(ing.Regions) == 0 {
		ing.Regions = nil
	}
	return ing
}

// Real32 rounds v to the nearest float32 and returns its shortest decimal
// representation as a float64 (16.1 stays 16.1).
func Real32(v float64) float64 {
	out, _ := strconv.ParseFloat(strconv.FormatFloat(float64(float32(v)), 'g', -1, 32), 64)
	return out
}
```

Create `internal/catalog/db.go`:

```go
package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"thymeandplace/internal/ocean"
	"thymeandplace/internal/store"
)

// TxBeginner is satisfied by *pgxpool.Pool and pgx.Tx.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// Write persists upserts and deletes. Call it inside a transaction.
// Ingredient rows are written first so pairings between new ingredients
// satisfy their foreign keys; unchanged rows are left untouched.
func Write(ctx context.Context, q *store.Queries, upserts []Ingredient, deletes []string) error {
	norm := make([]Ingredient, len(upserts))
	for i, ing := range upserts {
		norm[i] = Normalize(ing)
		if err := q.UpsertIngredient(ctx, toParams(norm[i])); err != nil {
			return fmt.Errorf("upsert %s: %w", ing.Slug, err)
		}
	}
	for _, ing := range norm {
		if err := q.PrunePairings(ctx, store.PrunePairingsParams{Slug: ing.Slug, Keep: ing.Pairings}); err != nil {
			return fmt.Errorf("prune pairings %s: %w", ing.Slug, err)
		}
		for _, p := range ing.Pairings {
			if err := q.AddPairing(ctx, store.AddPairingParams{Slug: ing.Slug, PairsWith: p}); err != nil {
				return fmt.Errorf("pairing %s→%s: %w", ing.Slug, p, err)
			}
		}
		keep := make([]int32, len(ing.Regions))
		for i, r := range ing.Regions {
			keep[i] = int32(r.EcoregionID)
		}
		if err := q.PruneSeafoodRegions(ctx, store.PruneSeafoodRegionsParams{Slug: ing.Slug, Keep: keep}); err != nil {
			return fmt.Errorf("prune regions %s: %w", ing.Slug, err)
		}
		for _, r := range ing.Regions {
			err := q.UpsertSeafoodRegion(ctx, store.UpsertSeafoodRegionParams{
				Slug: ing.Slug, EcoregionID: int32(r.EcoregionID),
				Months: toInt16s(r.Months), PeakMonths: toInt16s(r.PeakMonths),
			})
			if err != nil {
				return fmt.Errorf("region %s/%d: %w", ing.Slug, r.EcoregionID, err)
			}
		}
	}
	for _, slug := range deletes {
		if _, err := q.DeleteIngredient(ctx, slug); err != nil {
			return fmt.Errorf("delete %s: %w", slug, err)
		}
	}
	return nil
}

// Load reads the whole catalog in one consistent read-only transaction.
func Load(ctx context.Context, db TxBeginner) (*Snapshot, error) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only
	q := store.New(tx)

	version, err := q.GetCatalogVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("catalog version: %w", err)
	}
	rows, err := q.ListIngredients(ctx)
	if err != nil {
		return nil, fmt.Errorf("ingredients: %w", err)
	}
	pairs, err := q.ListPairings(ctx)
	if err != nil {
		return nil, fmt.Errorf("pairings: %w", err)
	}
	regions, err := q.ListSeafoodRegions(ctx)
	if err != nil {
		return nil, fmt.Errorf("regions: %w", err)
	}

	bySlug := make(map[string]*Ingredient, len(rows))
	items := make([]Ingredient, len(rows))
	for i, r := range rows {
		items[i] = fromRow(r)
		bySlug[r.Slug] = &items[i]
	}
	for _, p := range pairs {
		if ing := bySlug[p.Slug]; ing != nil {
			ing.Pairings = append(ing.Pairings, p.PairsWith)
		}
	}
	for _, r := range regions {
		if ing := bySlug[r.Slug]; ing != nil {
			ing.Regions = append(ing.Regions, SeafoodRegion{
				EcoregionID: ocean.EcoregionID(r.EcoregionID),
				Months:      fromInt16s(r.Months), PeakMonths: fromInt16s(r.PeakMonths),
			})
		}
	}
	for i := range items {
		items[i] = Normalize(items[i])
	}
	return NewSnapshot(version, items)
}

func toParams(ing Ingredient) store.UpsertIngredientParams {
	return store.UpsertIngredientParams{
		Slug: ing.Slug, Name: ing.Name,
		Category: store.IngredientCategory(ing.Category), Kind: store.IngredientKind(ing.Kind),
		Frost:                store.NullFrostTolerance{FrostTolerance: store.FrostTolerance(ing.Frost), Valid: ing.Frost != ""},
		GrowTempMinC:         f4(ing.GrowTempMinC), GrowTempMaxC: f4(ing.GrowTempMaxC),
		MonthsToHarvest:      i2(ing.MonthsToHarvest), StorageMonths: int16(ing.StorageMonths),
		ChillMonthsMin:       i2(ing.ChillMonthsMin), WarmupTempC: f4(ing.WarmupTempC),
		MonthsBloomToHarvest: i2(ing.MonthsBloomToHarvest), HarvestSpanMonths: i2(ing.HarvestSpanMonths),
		FruitingTempMinC:     f4(ing.FruitingTempMinC), FruitingTempMaxC: f4(ing.FruitingTempMaxC),
		MinPrecipMm:          f4(ing.MinPrecipMM),
		TipChoose:            ing.TipChoose, TipStore: ing.TipStore, Note: ing.Note,
	}
}

func fromRow(r store.Ingredient) Ingredient {
	ing := Ingredient{
		Slug: r.Slug, Name: r.Name, Category: Category(r.Category), Kind: Kind(r.Kind),
		GrowTempMinC: unf4(r.GrowTempMinC), GrowTempMaxC: unf4(r.GrowTempMaxC),
		MonthsToHarvest: uni2(r.MonthsToHarvest), StorageMonths: int(r.StorageMonths),
		ChillMonthsMin: uni2(r.ChillMonthsMin), WarmupTempC: unf4(r.WarmupTempC),
		MonthsBloomToHarvest: uni2(r.MonthsBloomToHarvest), HarvestSpanMonths: uni2(r.HarvestSpanMonths),
		FruitingTempMinC: unf4(r.FruitingTempMinC), FruitingTempMaxC: unf4(r.FruitingTempMaxC),
		MinPrecipMM: unf4(r.MinPrecipMm),
		TipChoose: r.TipChoose, TipStore: r.TipStore, Note: r.Note,
	}
	if r.Frost.Valid {
		ing.Frost = Frost(r.Frost.FrostTolerance)
	}
	return ing
}

func f4(p *float64) pgtype.Float4 {
	if p == nil {
		return pgtype.Float4{}
	}
	return pgtype.Float4{Float32: float32(*p), Valid: true}
}

func unf4(v pgtype.Float4) *float64 {
	if !v.Valid {
		return nil
	}
	f := Real32(float64(v.Float32))
	return &f
}

func i2(p *int) pgtype.Int2 {
	if p == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*p), Valid: true}
}

func uni2(v pgtype.Int2) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int16)
	return &i
}

func toInt16s(ms []int) []int16 {
	out := make([]int16, len(ms))
	for i, m := range ms {
		out[i] = int16(m)
	}
	return out
}

func fromInt16s(ms []int16) []int {
	out := make([]int, len(ms))
	for i, m := range ms {
		out[i] = int(m)
	}
	return out
}
```

- [ ] **Step 6: Write the round-trip tests**

Create `internal/catalog/db_test.go`:

```go
package catalog_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

// writeTx runs catalog.Write in its own committed transaction.
func writeTx(t *testing.T, pool *pgxpool.Pool, upserts []catalog.Ingredient, deletes []string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := catalog.Write(ctx, store.New(tx), upserts, deletes); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWriteLoadRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)

	snap, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.Len() != 11 || snap.Version() == 0 {
		t.Fatalf("len=%d version=%d", snap.Len(), snap.Version())
	}
	for _, want := range catalogtest.SmallCatalog() {
		got, ok := snap.Get(want.Slug)
		if !ok {
			t.Fatalf("%s missing", want.Slug)
		}
		if !reflect.DeepEqual(got, catalog.Normalize(want)) {
			t.Errorf("%s:\n got %+v\nwant %+v", want.Slug, got, catalog.Normalize(want))
		}
	}
}

// Review Focus 1: 16.1 survives the real column.
func TestRealPrecisionRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	i := catalogtest.Annual()
	i.GrowTempMinC, i.GrowTempMaxC = catalogtest.P(16.1), catalogtest.P(29.9)
	writeTx(t, pool, []catalog.Ingredient{i}, nil)
	snap, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := snap.Get("tomato")
	if *got.GrowTempMinC != 16.1 || *got.GrowTempMaxC != 29.9 {
		t.Fatalf("got %v / %v", *got.GrowTempMinC, *got.GrowTempMaxC)
	}
}

func TestWriteReplacesLinks(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)

	tomato := catalogtest.Annual()
	tomato.Pairings = []string{"kale"} // basil removed, kale added
	cod := catalogtest.Seafood()
	cod.Regions = []catalog.SeafoodRegion{{EcoregionID: 30, Months: []int{12}}}
	writeTx(t, pool, []catalog.Ingredient{tomato, cod}, []string{"banana"})

	snap, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := snap.Get("tomato"); !reflect.DeepEqual(got.Pairings, []string{"kale"}) {
		t.Fatalf("tomato pairings = %v", got.Pairings)
	}
	if got, _ := snap.Get("atlantic-cod"); len(got.Regions) != 1 || got.Regions[0].EcoregionID != 30 {
		t.Fatalf("cod regions = %+v", got.Regions)
	}
	if _, ok := snap.Get("banana"); ok {
		t.Fatal("banana not deleted")
	}
}

func TestWritePairingsBetweenNewIngredients(t *testing.T) {
	pool := dbtest.New(t)
	a := catalogtest.Hardy()
	a.Slug, a.Pairings = "chard", []string{"leek"}
	b := catalogtest.Hardy()
	b.Slug, b.Pairings = "leek", []string{"chard"}
	writeTx(t, pool, []catalog.Ingredient{a, b}, nil) // fails on FK if rows aren't written first
}
```

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/catalog/ -count=1`
Expected: `ok`.

- [ ] **Step 8: Commit**

```bash
git add migrations/00003_catalog.sql internal/store internal/catalog
git commit -m "feat(catalog): catalog schema, normalisation, Write and consistent Load

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Shared invalid examples rejected by the database

**Files:**
- Create: `internal/catalog/constraints_test.go`

**Interfaces:**
- Consumes: `catalogtest.InvalidExamples()` (Plan 1), `catalog.Write`, `dbtest.New`.
- Produces: nothing new. This task proves that the app rules and the DB constraints agree (spec §13.3).

- [ ] **Step 1: Write the test**

Create `internal/catalog/constraints_test.go`:

```go
package catalog_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

// Every shared invalid example must be rejected by a DB constraint
// (SQLSTATE class 23: integrity constraint violation).
func TestInvalidExamplesRejectedByDatabase(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	// basil is a pairing target used by some examples.
	var basil catalog.Ingredient
	for _, i := range catalogtest.SmallCatalog() {
		if i.Slug == "basil" {
			basil = i
			basil.Pairings = nil
		}
	}
	writeTx(t, pool, []catalog.Ingredient{basil}, nil)

	for _, ex := range catalogtest.InvalidExamples() {
		t.Run(ex.Name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx) //nolint:errcheck
			err = catalog.Write(ctx, store.New(tx), []catalog.Ingredient{ex.Ingredient}, nil)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || !strings.HasPrefix(pgErr.Code, "23") {
				t.Fatalf("want integrity violation, got %v", err)
			}
		})
	}
}

// Every valid builder is accepted by the database.
func TestValidBuildersAcceptedByDatabase(t *testing.T) {
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/catalog/ -run Database -count=1 -v`
Expected: every subtest PASSes. If one fails with `got <nil>`, the DB is missing a constraint the app enforces: fix the migration in Task 3 (add a new migration if Task 3 has been merged elsewhere), not the example.

- [ ] **Step 3: Commit**

```bash
git add internal/catalog/constraints_test.go
git commit -m "test(catalog): shared invalid examples are rejected by DB constraints

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Catalog history and version triggers

**Files:**
- Create: `migrations/00004_catalog_history.sql`, `internal/catalog/history_test.go`

**Interfaces:**
- Consumes: `catalog.Write`, `dbtest.New`.
- Produces:
  - Table `catalog_history` (spec §8.2) with index on `(row_key->>'slug', id DESC)`
  - Row triggers on `ingredient`, `ingredient_pairing`, `seafood_region` writing history
  - Statement triggers bumping `catalog_meta.version` and running `pg_notify('data_version_changed', 'catalog')`
  - Session settings used by Plan 4: `app.actor` (text), `app.change_id` (bigint as text), `app.meta` (JSON object text)

- [ ] **Step 1: Write the migration**

Create `migrations/00004_catalog_history.sql`:

```sql
-- +goose Up
CREATE TABLE catalog_history (
  id         bigserial PRIMARY KEY,
  changed_at timestamptz NOT NULL DEFAULT now(),
  actor      text NOT NULL,
  change_id  bigint,
  action     text NOT NULL CHECK (action IN ('insert','update','delete')),
  table_name text NOT NULL,
  row_key    jsonb NOT NULL,
  before     jsonb,
  after      jsonb
);
CREATE INDEX catalog_history_slug ON catalog_history ((row_key->>'slug'), id DESC);

-- +goose StatementBegin
CREATE FUNCTION catalog_history_row() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_actor  text  := coalesce(nullif(current_setting('app.actor', true), ''), 'sql');
  v_change bigint := nullif(current_setting('app.change_id', true), '')::bigint;
  v_meta   text  := nullif(current_setting('app.meta', true), '');
  v_before jsonb;
  v_after  jsonb;
  v_row    jsonb;
  v_key    jsonb;
BEGIN
  IF TG_OP IN ('UPDATE', 'DELETE') THEN v_before := to_jsonb(OLD); END IF;
  IF TG_OP IN ('INSERT', 'UPDATE') THEN v_after := to_jsonb(NEW); END IF;
  v_row := coalesce(v_after, v_before);
  v_key := CASE TG_TABLE_NAME
    WHEN 'ingredient' THEN jsonb_build_object('slug', v_row->'slug')
    WHEN 'ingredient_pairing' THEN jsonb_build_object('slug', v_row->'slug', 'pairs_with', v_row->'pairs_with')
    ELSE jsonb_build_object('slug', v_row->'slug', 'ecoregion_id', v_row->'ecoregion_id')
  END;
  IF v_meta IS NOT NULL THEN
    v_after := coalesce(v_after, '{}'::jsonb) || jsonb_build_object('_meta', v_meta::jsonb);
  END IF;
  INSERT INTO catalog_history (actor, change_id, action, table_name, row_key, before, after)
  VALUES (v_actor, v_change, lower(TG_OP), TG_TABLE_NAME, v_key, v_before, v_after);
  RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION catalog_bump_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE catalog_meta SET version = version + 1;
  PERFORM pg_notify('data_version_changed', 'catalog');
  RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE TRIGGER ingredient_history AFTER INSERT OR UPDATE OR DELETE ON ingredient
  FOR EACH ROW EXECUTE FUNCTION catalog_history_row();
CREATE TRIGGER ingredient_pairing_history AFTER INSERT OR UPDATE OR DELETE ON ingredient_pairing
  FOR EACH ROW EXECUTE FUNCTION catalog_history_row();
CREATE TRIGGER seafood_region_history AFTER INSERT OR UPDATE OR DELETE ON seafood_region
  FOR EACH ROW EXECUTE FUNCTION catalog_history_row();

CREATE TRIGGER ingredient_version AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON ingredient
  FOR EACH STATEMENT EXECUTE FUNCTION catalog_bump_version();
CREATE TRIGGER ingredient_pairing_version AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON ingredient_pairing
  FOR EACH STATEMENT EXECUTE FUNCTION catalog_bump_version();
CREATE TRIGGER seafood_region_version AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON seafood_region
  FOR EACH STATEMENT EXECUTE FUNCTION catalog_bump_version();

-- +goose Down
DROP TRIGGER seafood_region_version ON seafood_region;
DROP TRIGGER ingredient_pairing_version ON ingredient_pairing;
DROP TRIGGER ingredient_version ON ingredient;
DROP TRIGGER seafood_region_history ON seafood_region;
DROP TRIGGER ingredient_pairing_history ON ingredient_pairing;
DROP TRIGGER ingredient_history ON ingredient;
DROP FUNCTION catalog_bump_version();
DROP FUNCTION catalog_history_row();
DROP TABLE catalog_history;
```

Run `./scripts/sqlc.sh generate` so `models.go` gains `CatalogHistory`.

- [ ] **Step 2: Write the tests**

Create `internal/catalog/history_test.go`:

```go
package catalog_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

type histRow struct {
	Actor    string
	ChangeID *int64
	Action   string
	Table    string
	Key      map[string]any
	Before   map[string]any
	After    map[string]any
}

func history(t *testing.T, pool *pgxpool.Pool) []histRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT actor, change_id, action, table_name, row_key, before, after FROM catalog_history ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []histRow
	for rows.Next() {
		var r histRow
		var key, before, after []byte
		if err := rows.Scan(&r.Actor, &r.ChangeID, &r.Action, &r.Table, &key, &before, &after); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(key, &r.Key)
		_ = json.Unmarshal(before, &r.Before)
		_ = json.Unmarshal(after, &r.After)
		out = append(out, r)
	}
	return out
}

func version(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	v, err := store.New(pool).GetCatalogVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func writeAs(t *testing.T, pool *pgxpool.Pool, actor string, changeID int64, meta string, ups []catalog.Ingredient, dels []string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor', $1, true), set_config('app.change_id', $2, true), set_config('app.meta', $3, true)`,
		actor, itoa(changeID), meta); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Write(ctx, store.New(tx), ups, dels); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func itoa(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestHistoryInsertUpdate(t *testing.T) {
	pool := dbtest.New(t)
	writeAs(t, pool, "owner", 42, "", []catalog.Ingredient{catalogtest.Hardy()}, nil)

	h := history(t, pool)
	if len(h) != 1 || h[0].Actor != "owner" || h[0].ChangeID == nil || *h[0].ChangeID != 42 ||
		h[0].Action != "insert" || h[0].Table != "ingredient" || h[0].Key["slug"] != "kale" || h[0].After["name"] != "Kale" {
		t.Fatalf("insert history = %+v", h)
	}

	k := catalogtest.Hardy()
	k.Name = "Curly kale"
	writeAs(t, pool, "owner", 43, "", []catalog.Ingredient{k}, nil)
	h = history(t, pool)
	last := h[len(h)-1]
	if last.Action != "update" || last.Before["name"] != "Kale" || last.After["name"] != "Curly kale" {
		t.Fatalf("update history = %+v", last)
	}
}

// Review Focus 2.
func TestUnchangedUpsertWritesNoHistory(t *testing.T) {
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)
	before := len(history(t, pool))
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)
	if after := len(history(t, pool)); after != before {
		t.Fatalf("unchanged upsert wrote %d history rows", after-before)
	}
}

func TestDirectSQLActor(t *testing.T) {
	pool := dbtest.New(t)
	writeTx(t, pool, []catalog.Ingredient{catalogtest.Hardy()}, nil)
	if _, err := pool.Exec(context.Background(), `UPDATE ingredient SET note = 'x' WHERE slug = 'kale'`); err != nil {
		t.Fatal(err)
	}
	h := history(t, pool)
	if h[len(h)-1].Actor != "sql" || h[len(h)-1].ChangeID != nil {
		t.Fatalf("got %+v", h[len(h)-1])
	}
}

// Review Focus 3.
func TestDeleteCascadesPairingsWithHistory(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil) // tomato ↔ basil
	writeAs(t, pool, "owner", 7, "", nil, []string{"basil"})

	var cascaded int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM catalog_history
		WHERE change_id = 7 AND table_name = 'ingredient_pairing' AND action = 'delete'`).Scan(&cascaded)
	if err != nil || cascaded != 2 {
		t.Fatalf("cascaded pairing history rows = %d (%v), want 2", cascaded, err)
	}
	snap, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatalf("reload after cascade: %v", err)
	}
	if tm, _ := snap.Get("tomato"); len(tm.Pairings) != 0 {
		t.Fatalf("tomato still pairs: %v", tm.Pairings)
	}
}

func TestMetaMergedIntoAfter(t *testing.T) {
	pool := dbtest.New(t)
	writeAs(t, pool, "owner", 9, `{"override":true}`, []catalog.Ingredient{catalogtest.Hardy()}, nil)
	writeAs(t, pool, "owner", 10, `{"override":true}`, nil, []string{"kale"})
	h := history(t, pool)
	meta := h[0].After["_meta"].(map[string]any)
	if meta["override"] != true {
		t.Fatalf("insert meta = %v", h[0].After)
	}
	del := h[len(h)-1]
	if del.Action != "delete" || del.After["_meta"].(map[string]any)["override"] != true {
		t.Fatalf("delete meta = %+v", del)
	}
}

func TestVersionBumpsOncePerStatement(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	v0 := version(t, pool)
	writeTx(t, pool, []catalog.Ingredient{catalogtest.Hardy(), catalogtest.YearRound()}, nil)
	if _, err := pool.Exec(ctx, `UPDATE ingredient SET note = 'n'`); err != nil { // one statement, two rows
		t.Fatal(err)
	}
	v1 := version(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE ingredient SET note = 'm'`); err != nil {
		t.Fatal(err)
	}
	if v2 := version(t, pool); v2 != v1+1 {
		t.Fatalf("two-row statement bumped %d times", v2-v1)
	}
	if v1 <= v0 {
		t.Fatal("writes did not bump version")
	}
}

// Review Focus 5: NOTIFY on commit only.
func TestNotifyOnCommitOnly(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN data_version_changed"); err != nil {
		t.Fatal(err)
	}

	tx, _ := pool.Begin(ctx)
	if err := catalog.Write(ctx, store.New(tx), []catalog.Ingredient{catalogtest.Hardy()}, nil); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	wctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if n, err := conn.Conn().WaitForNotification(wctx); err == nil {
		t.Fatalf("rolled-back write notified: %+v", n)
	}

	writeTx(t, pool, []catalog.Ingredient{catalogtest.Hardy()}, nil)
	wctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	n, err := conn.Conn().WaitForNotification(wctx2)
	if err != nil || n.Channel != "data_version_changed" || n.Payload != "catalog" {
		t.Fatalf("commit notification = %+v, %v", n, err)
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/catalog/ -count=1`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
git add migrations/00004_catalog_history.sql internal/store internal/catalog/history_test.go
git commit -m "feat(catalog): trigger-based history, version bump and NOTIFY

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Snapshot holder and data version

**Files:**
- Create: `internal/catalog/holder.go`, `internal/catalog/holder_test.go`, `internal/dataversion/dataversion.go`, `internal/dataversion/dataversion_test.go`

**Interfaces:**
- Consumes: `catalog.Load`, `store.Queries.GetCatalogVersion`, `store.Queries.ListLiveDatasetIDs`.
- Produces:
  - `func catalog.NewHolder(load func(context.Context) (*Snapshot, error), log *slog.Logger) *Holder`
  - `(*Holder).Current() *Snapshot`: nil until the first successful load
  - `(*Holder).Reload(ctx context.Context) error`: on failure, logs at ERROR, keeps the previous snapshot and returns the error
  - `func dataversion.Compute(catalogVersion int64, liveDatasetIDs []int64) string`: 16 hex characters
  - `type dataversion.Reader interface { GetCatalogVersion(ctx context.Context) (int64, error); ListLiveDatasetIDs(ctx context.Context) ([]int64, error) }` (`*store.Queries` satisfies it)
  - `func dataversion.Current(ctx context.Context, r Reader) (string, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/dataversion/dataversion_test.go`:

```go
package dataversion_test

import (
	"context"
	"testing"

	"thymeandplace/internal/dataversion"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

func TestCompute(t *testing.T) {
	a := dataversion.Compute(3, []int64{9, 2})
	if len(a) != 16 {
		t.Fatalf("len = %d", len(a))
	}
	if a != dataversion.Compute(3, []int64{2, 9}) {
		t.Fatal("ID order must not matter")
	}
	if a == dataversion.Compute(4, []int64{2, 9}) || a == dataversion.Compute(3, []int64{2, 10}) {
		t.Fatal("different inputs must differ")
	}
	// Known value guards the format: sha256("catalog:0;datasets:")[:16].
	if got := dataversion.Compute(0, nil); got != "9073c2c64186c6b3" {
		t.Fatalf("Compute(0, nil) = %q", got)
	}
}

func TestCurrent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	v, err := dataversion.Current(ctx, store.New(pool))
	if err != nil || v != dataversion.Compute(0, nil) {
		t.Fatalf("v=%q err=%v", v, err)
	}
}
```

Create `internal/catalog/holder_test.go`:

```go
package catalog_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHolderStartsEmpty(t *testing.T) {
	h := catalog.NewHolder(func(context.Context) (*catalog.Snapshot, error) { return nil, errors.New("down") }, quiet())
	if h.Current() != nil {
		t.Fatal("Current before load must be nil")
	}
	if err := h.Reload(context.Background()); err == nil || h.Current() != nil {
		t.Fatal("failed first load must leave Current nil and return an error")
	}
}

// Review Focus 4: a failed reload keeps the previous snapshot.
func TestHolderKeepsPreviousOnFailure(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)

	h := catalog.NewHolder(func(ctx context.Context) (*catalog.Snapshot, error) { return catalog.Load(ctx, pool) }, quiet())
	if err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	first := h.Current()
	if first == nil || first.Len() != 11 {
		t.Fatalf("first = %v", first)
	}

	pool.Close() // database unreachable
	if err := h.Reload(ctx); err == nil {
		t.Fatal("reload against a closed pool must fail")
	}
	if h.Current() != first {
		t.Fatal("failed reload replaced the live snapshot")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/dataversion/ ./internal/catalog/ -run 'Compute|Current|Holder'`
Expected: FAIL to compile: `undefined: dataversion.Compute` / `catalog.NewHolder`.

- [ ] **Step 3: Implement**

Create `internal/dataversion/dataversion.go`:

```go
// Package dataversion computes the data version that keys caches:
// a hash of the catalog version and the live reference-dataset IDs.
package dataversion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Compute returns the first 16 hex characters of
// sha256("catalog:<v>;datasets:<ids ascending, comma-separated>").
func Compute(catalogVersion int64, liveDatasetIDs []int64) string {
	ids := slices.Clone(liveDatasetIDs)
	slices.Sort(ids)
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("catalog:%d;datasets:%s", catalogVersion, strings.Join(parts, ","))))
	return hex.EncodeToString(sum[:])[:16]
}

// Reader is satisfied by *store.Queries.
type Reader interface {
	GetCatalogVersion(ctx context.Context) (int64, error)
	ListLiveDatasetIDs(ctx context.Context) ([]int64, error)
}

// Current reads the inputs from the database and computes the data version.
func Current(ctx context.Context, r Reader) (string, error) {
	v, err := r.GetCatalogVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("catalog version: %w", err)
	}
	ids, err := r.ListLiveDatasetIDs(ctx)
	if err != nil {
		return "", fmt.Errorf("live datasets: %w", err)
	}
	return Compute(v, ids), nil
}
```

Create `internal/catalog/holder.go`:

```go
package catalog

import (
	"context"
	"log/slog"
	"sync/atomic"
)

// Holder owns the live snapshot and swaps it atomically on reload.
type Holder struct {
	cur  atomic.Pointer[Snapshot]
	load func(context.Context) (*Snapshot, error)
	log  *slog.Logger
}

func NewHolder(load func(context.Context) (*Snapshot, error), log *slog.Logger) *Holder {
	return &Holder{load: load, log: log}
}

// Current returns the live snapshot, or nil before the first successful load.
func (h *Holder) Current() *Snapshot { return h.cur.Load() }

// Reload loads a new snapshot. On failure the previous one stays live.
func (h *Holder) Reload(ctx context.Context) error {
	s, err := h.load(ctx)
	if err != nil {
		h.log.Error("catalog reload failed; keeping previous snapshot", "err", err)
		return err
	}
	h.cur.Store(s)
	h.log.Info("catalog snapshot loaded", "version", s.Version(), "items", s.Len())
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/dataversion/ ./internal/catalog/ -count=1`
Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/holder.go internal/catalog/holder_test.go internal/dataversion
git commit -m "feat: atomic snapshot holder and data-version hash

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Plan completion check

- [ ] `./scripts/sqlc.sh generate && git diff --exit-code internal/store` shows no diff (generated code is current).
- [ ] `go vet ./... && go test ./... -count=1` passes with Docker running; `TP_SKIP_DB=1 go test ./...` passes without Docker.
- [ ] `docker compose -f deploy/compose.dev.yaml up -d` followed by `go run` of a one-off `db.Migrate` against `DATABASE_URL` succeeds. (The real entry point is added in Plan 5.)
