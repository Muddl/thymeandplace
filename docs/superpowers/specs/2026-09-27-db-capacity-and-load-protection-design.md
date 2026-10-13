# Thyme & Place - Database capacity and load protection - Design

- **Status:** Proposed
- **Date:** 2026-09-27
- **Source:** Architectural audit, 2026-09-27 (finding F01)
- **Related issues:** [#1](https://github.com/Muddl/thymeandplace/issues/1) (F01)
- **Amends:** core spec §11.2 (lookups go through a shared DB gate), §11.3 (per-cell input cache), §11.4 (load shedding), §11.6 (new "overloaded" row), §14 (connection budget), §16 (new rows: pool size, public DB slots, gate wait, input cache size)

---

## 1. Problem

Under modest load, the public site will run out of database connections before it runs out of CPU. Requests will then queue until the 2 s deadline and show the 503 retry panel.

Evidence at HEAD (`22befca`):

- **Pool size is never set.** `db.Open` calls `pgxpool.New(ctx, url)` (`internal/db/db.go:18-28`). `deploy/env.example:8` has no `pool_max_conns`. So the pool uses the pgx default of `max(4, runtime.NumCPU())`, checked in the pgx docs via Context7. On a 1–2 vCPU VPS, that is 4 connections. The admin CLI, `ingest` and `devseed` use the same `db.Open`, with the same default.
- **One connection is always taken.** The data-version watcher calls `pool.Acquire` for its `LISTEN` session (`internal/dataversion/watcher.go:106`). It holds the connection until the session ends on an error or at shutdown (`:110`). That leaves 3 connections for everything else.
- **Apply used two connections at once.** It held a transaction with `catalog_meta` locked `FOR UPDATE`, then called `catalog.Load(ctx, s.pool)`, which took a second connection (`internal/changeset/service.go`). Apply now reads the catalog and checks ecoregions on that same transaction.
- **Every public cache miss makes several queries in a row.** A `/season` miss runs climate `CellsIn`, then ocean `NearbyEcoregions` (`ST_DWithin` on geography multipolygons, 100 km), then `NearestPlace` (`internal/web/season.go:137-155`). `/ingredient` is never cached: every request runs `ForCell` and `Nearby` (`internal/web/ingredient.go:36-48`). `/places` runs a trigram search on every keystroke pause.
- **Waiting for a connection eats the request budget.** The public timeout is 2 s (`internal/web/server.go:56`, applied at `:92`). Waiting for a free pool connection counts against it.
- **Nothing limits total load.** Rate limits are per IP (`internal/web/middleware.go:162-171`). There is no global limit and no load shedding. A spike from many addresses queues on the pool, not in front of it.
- **The ocean query cost is unknown.** Ocean polygons are MEOW clipped to Natural Earth 1:50m land (`internal/ingest/ocean.go`), so coastal polygons have many vertices. Geography `ST_DWithin` against them hasn't been measured on production data.

Result: a burst of about 10 concurrent cold requests (one link share) is enough to push the queue past 2 s. The owner can't use `/admin` while it lasts, because admin shares the same 3 connections.

## 2. Goals and non-goals

Goals:

1. The pool size is explicit and sized for the VPS. The connection budget across all clients fits `max_connections`.
2. `LISTEN` doesn't use a pool connection.
3. Public traffic can't take every connection. Admin, the drift check, Apply and catalog reloads always have room.
4. When public DB capacity is full, requests fail fast with the existing retry panel and `Retry-After` instead of waiting 2 s.
5. `/ingredient` makes at most one pair of lookups per cell and data version.
6. A repeatable load test shows the core spec §1 targets are met (p95 about 1 s warm, 2 s cold) at an agreed traffic level, before launch.

Non-goals:

- Horizontal scaling, read replicas and connection poolers such as PgBouncer.
- Rewriting the ocean lookup unless measurement shows it's needed (§4.5).
- Changing the per-IP public limits (core spec §11.4).
- Changing what `/season` computes from. That is the season cache consistency design (`2026-09-27-season-cache-consistency-design.md`, "S5"). This design is written to work before and after S5 (§4.4).

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| Where the pool size is set | `db.Open` takes an options struct. Each binary passes its own default (server 16, CLIs 4). A `pool_max_conns` in `DATABASE_URL` still wins | URL only (easy to forget, and every binary would share one value). A hard-coded constant (can't be tuned per VPS) |
| `LISTEN` connection | A dedicated `pgx.Conn` built from the pool's config copy | Keep `pool.Acquire` (permanently removes one pooled connection, and `pool.Close` waits on it at shutdown) |
| Protecting admin from public load | A public "DB gate": a semaphore of 12 slots out of the 16 connections, with a 250 ms wait | A second pool for admin (two pools to size, and it doubles idle connections). Rely on the per-IP limits (they don't bound many IPs) |
| When the gate is full | Fail fast: 503 retry panel, `Retry-After: 5` | Queue until the 2 s deadline (the problem today). A 429 (not the client's fault) |
| `/ingredient` caching | Cache the per-cell inputs (normals and ecoregions), keyed by cell and data version | Cache whole rendered pages (the month and date change the bars, so the key space grows 12×). No cache (every click is 2 queries) |
| Ocean query | Measure first. Subdivide polygons at ingest only if p95 is over 25 ms | Subdivide now (adds a second view to the ingest pipeline before knowing it's needed) |
| Load test tool | k6 in Docker, run inside the compose network against `app:8080` with a varied `X-Forwarded-For` | Load test through Caddy from one machine (the per-IP limit caps it at 2 req/s). A hosted load-test service (cost, and it tests the network rather than the app) |

## 4. Design

### 4.1 Pool sizing

`internal/db/db.go`:

```go
type PoolOptions struct {
	MaxConns int32 // used when DATABASE_URL has no pool_max_conns
	MinConns int32 // used when DATABASE_URL has no pool_min_conns
}

func Open(ctx context.Context, url string, o PoolOptions) (*pgxpool.Pool, error)
```

`Open` calls `pgxpool.ParseConfig(url)`. For each option, it applies the value only when the URL doesn't carry the matching `pool_*` parameter, checked by parsing the URL's query. Then it calls `pgxpool.NewWithConfig` and pings, as today.

| Caller | MaxConns | MinConns |
|---|---|---|
| `cmd/server` | 16, overridable with `DB_MAX_CONNS` | 2 |
| `cmd/admin`, `cmd/ingest`, `cmd/devseed` | 4 | 0 |

Connection budget, which goes into core spec §14: app 16 + app `LISTEN` 1 + ingest or devseed 4 + admin CLI 4 + backup 1 + `psql` sessions, about 30. That is well under Postgres's default `max_connections` of 100, with room for a second app replica. If S10's `internal/config` has landed, `DB_MAX_CONNS` is read there.

### 4.2 Dedicated `LISTEN` connection

`dataversion.NewWatcher` gains a `connect func(ctx) (*pgx.Conn, error)` field. `cmd/server/app.go` builds it from `pool.Config().ConnConfig.Copy()` and `pgx.ConnectConfig`. `session()` uses it in place of `pool.Acquire` and closes the connection when it returns. `Refresh` keeps using the pool, because each refresh is two short queries. Reconnect backoff is unchanged.

The shutdown ordering comment in `start()` (`cmd/server/app.go:173-178`) is kept. Cancelling still stops the loop first, but `pool.Close` no longer depends on it.

### 4.3 Public DB gate

`web.Server` gets a gate: a buffered channel of `Deps.DBSlots` tokens. The default of 12 leaves 4 of the server's 16 connections for admin, background work and Apply.

```go
// withDB runs f while holding a DB slot. It waits at most gateWait (250 ms,
// or until ctx ends) for a slot, then returns errBusy without calling f.
func (s *Server) withDB(ctx context.Context, f func(context.Context) error) error
```

What goes through the gate:

- `/season` on a cache miss: the whole `compute` call, one slot for its sequential queries.
- `/ingredient` on an input-cache miss (§4.4).
- `/places` searches.
- The `/season` place label (`ByID`, and `Nearest` if S5 moves it out of the cache). When the gate is busy, the label falls back to coordinates. It never fails the request.

What does not: cache hits, `/healthz`, `/readyz`, static files and everything under `/admin`.

On `errBusy`:
- `/season` and `/ingredient` render the existing `LoadError` panel with status 503 and `Retry-After: 5`.
- `/places` renders `PlacesUnavailable` with 503.

These count as 5xx in the observability design's window (`2026-09-27-observability-and-alerting-design.md` §4.3), so sustained shedding alerts the owner. The shed path logs nothing per request. A sampled `INFO "public DB gate full"` goes out at most once a minute, with the count of shed requests.

Waiting 250 ms instead of failing at once smooths small bursts. The gate is a plain channel, so tests can fill it directly.

### 4.4 Per-cell input cache

`internal/cache` becomes generic over its key: `Cache[K comparable, V any]`. The existing season cache uses `Key` as today. A new `CellKey{CellID int; DataVersion string}` keys a second LRU of 20,000 entries in `web.Server`. It holds `cellInputs{Normals *climate.MonthlyNormals; Ecoregions []ocean.EcoregionID}`, including cells with no normals.

`/ingredient` flow:
1. Read the data version before the snapshot, with the same ordering argument as `season.go:108-112`.
2. Look up `CellKey`.
3. On a miss, run `ForCell(cell)` and `Nearby(CellCenter(cell))` inside `withDB`. Both are already pure functions of the cell. Add the result only on success.
4. Evaluate `season.Year` as today.

Compatibility with S5: S5 replaces those two calls with `cellinput.Resolver.Resolve(cell)`. When S5 lands, the cached value becomes `cellinput.Inputs`, and `/season` misses also read through this cache. After that, a cell already seen in another month costs no reference-data queries at all. Whichever design lands second makes that one-line switch.

### 4.5 Ocean lookup cost

This step is a measurement, not code. Once the production ocean dataset is live:

1. Run `EXPLAIN (ANALYZE, BUFFERS)` of the `NearbyEcoregions` query (`internal/store/handwritten.go:54-75`) at the 10 coastal and 4 inland ocean spot-check points (`internal/ingest/ocean.go` `NewOcean`), plus 10 more coastal points.
2. Record p50 and p95 in `docs/capacity-baseline.md`.

If p95 is at most 25 ms, stop there. If not, follow-up work (a separate issue) adds a subdivided table built at ingest from `ST_Subdivide(geom::geometry, 255)::geography` with its own GiST index. That table needs the `Dataset` interface to repoint a second view. That is why it isn't done speculatively.

### 4.6 Load test

`deploy/loadtest/season.js` is a k6 script. `deploy/loadtest/README.md` explains how to run it:

```bash
docker run --rm -i --network thymeandplace_default grafana/k6 run - < deploy/loadtest/season.js
```

It targets `http://app:8080` directly, inside the compose network. The app trusts `X-Forwarded-For` there (`TRUST_PROXY_HEADERS=true`), so each virtual user sends its own address and the per-IP limit doesn't cap the test. Caddy and TLS are outside the test. The external monitor in the observability design covers them.

The script uses a fixed list of 500 land coordinates, generated once from the live `climate_cell` view and committed. It covers four scenarios:

| Scenario | Mix | Notes |
|---|---|---|
| Cold `/season` | 30% | A coordinate not yet requested in this run |
| Warm `/season` | 40% | Revisits a coordinate already requested |
| `/ingredient` | 20% | Follows an item link from a previous response |
| `/places` | 10% | Search prefixes of 2–6 letters |

Pass criteria, checked by k6 thresholds:
- Sustained load (default 50 req/s for 5 minutes; §8 question 1): p95 under 1 s for warm and under 2 s for cold requests, and fewer than 0.5% 503s.
- Spike (150 req/s for 1 minute): 503s allowed, but the p95 of 503 responses is under 400 ms, and the error rate is back under 0.5% within 30 s after the spike.

It runs on the production VPS before launch, at a quiet time, with results recorded in `docs/capacity-baseline.md`. It runs again after any change to pool size, VPS size or reference data.

### 4.7 Values (core spec §16)

| Item | Value |
|---|---|
| Server pool | 16 max, 2 min (`DB_MAX_CONNS` overrides) |
| CLI pools | 4 max |
| Public DB slots / wait | 12 / 250 ms |
| Shed response | 503 + `Retry-After: 5` |
| Per-cell input cache | 20,000 entries |
| Ocean query target | p95 ≤ 25 ms |

## 5. Implementation outline

1. `internal/db/db.go`: `PoolOptions` and the override rule. Update the four callers (`cmd/server/app.go:112`, `cmd/admin/main.go`, `cmd/ingest/main.go`, `cmd/devseed/main.go`).
2. `internal/dataversion/watcher.go`: the `connect` field. `cmd/server/app.go` passes it.
3. `internal/cache/cache.go`: generic key. Update `internal/web/server.go` and its tests.
4. `internal/web/server.go`: `Deps.DBSlots` (0 means 12), the gate, `withDB` and `errBusy`. `internal/web/season.go`, `ingredient.go` and `places.go` go through the gate. `/ingredient` uses the input cache.
5. `deploy/loadtest/` with the script, the coordinate list and the README. Add `docs/capacity-baseline.md`.
6. Docs: `deploy/env.example` (a commented `DB_MAX_CONNS`), `docs/operations.md` (§7), and the core spec amendments.

## 6. Testing

- **`db` (unit, no DB):** `poolConfig(url, opts)` is split out so it can be tested without a database:
  - Options apply when the URL has no `pool_*` parameters.
  - `pool_max_conns=7` in the URL wins over `MaxConns: 16`.
  - A bad URL returns an error.
- **`dataversion` (testcontainers):**
  - While `Run` is listening, `pool.Stat().AcquiredConns()` is 0.
  - A `pg_notify` still triggers `onChange`, extending the existing watcher tests.
  - Cancelling the context closes the dedicated connection, and `pool.Close()` returns promptly.
- **`web` gate (httptest, fakes):**
  - `fakeClimate` gains a `block chan struct{}`. With `DBSlots: 2`, two concurrent `/season` misses block. A third, at a different cell, gets 503 with `Retry-After: 5` in under 400 ms. Closing `block` lets the first two finish with 200.
  - While the gate is full, a cache hit for a warmed cell still returns 200.
  - `/places` gets 503 with the "unavailable" partial.
  - With `?place=`, a `/season` hit uses the coordinates label when the gate is full.
- **`web` input cache:**
  - Two `/ingredient` requests for one cell and version make one `ForCell` call and one `Nearby` call (the fakes count calls).
  - A new data version refetches.
  - A failed lookup isn't cached.
  - A cell with no normals is cached as such.
- **`cache`:** the existing tests pass with the generic key, plus one test for `CellKey`.
- **Load test:** manual, as in §4.6. Its results file is the evidence for the issue's acceptance criteria.
- **E2E:** no new scenario. The existing public and admin flows must pass unchanged.
- **`changeset` Apply (testcontainers):** `TestApplyUsesOneConnection` applies a seafood upsert on a pool with `MaxConns: 1` within 5 s. If Apply takes a second connection for `catalog.Load` or `ocean.Missing`, the call hits the deadline instead of returning.

## 7. Rollout, operations and rollback

- **Ship before launch.** No migration. `DB_MAX_CONNS` is optional.
- **`docs/operations.md`:**
  - A new "Capacity" section: the connection budget, how to change `DB_MAX_CONNS`, the meaning of the sampled `public DB gate full` log line, and how to run the load test (§4.6).
  - "Health and logs": sustained 503s with that line mean capacity, not a DB outage. `/readyz` still says `ready`.
- **After deploy:** `SELECT count(*), usename, state FROM pg_stat_activity GROUP BY 2, 3` shows the app holding between 2 and 17 connections.
- **Rollback:** redeploy the previous image. There's no data or schema change.

## 8. Open questions for the owner

1. **Launch traffic target.** Which sustained request rate must the site handle? Recommended: 50 req/s sustained and 150 req/s spike, as in §4.6, then revise once the first month of traffic is in.
2. **VPS size.** The values assume 2 vCPU and 4 GB RAM. With less memory, lower `DB_MAX_CONNS` to 10 and the DB slots to 7. Recommended: confirm the VPS plan before running the load test.
3. **Postgres tuning.** The `postgis/postgis` defaults are `shared_buffers` 128 MB and `work_mem` 4 MB. Recommended: leave them unless the load test shows disk reads dominate. Then set `shared_buffers` to 25% of RAM through `command:` in `deploy/compose.yaml`.
4. **Ocean subdivision.** Recommended: decide from the §4.5 measurement, not now.
