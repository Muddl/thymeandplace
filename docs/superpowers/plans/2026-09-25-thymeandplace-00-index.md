# Thyme & Place — Sub-project 1 Implementation Plans: Index

**Spec:** `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (revision 2)

The spec covers several independent subsystems, so it is implemented as nine plans. Each plan ends with working, tested software. Run them in order: each plan's tasks list the exact names and types they consume from earlier plans.

| # | Plan | Produces | Depends on |
|---|---|---|---|
| 1 | [Season engine core](2026-09-25-thymeandplace-01-season-engine.md) | Go module; grid math; catalog types, validation and snapshot; season engine; calibration scoring. No database. | — |
| 2 | [Database and catalog persistence](2026-09-25-thymeandplace-02-database-catalog.md) | goose migrations, PostGIS test harness, sqlc, reference/catalog schema, history + version triggers, snapshot holder, data version | 1 |
| 3 | [Reference data and ingestion](2026-09-25-thymeandplace-03-reference-data.md) | climate/ocean/places lookups; `ingest` CLI (fetch → transform → load → validate → swap, rollback); Python/xarray transform | 1, 2 |
| 4 | [Changesets, import/export, preview, drift](2026-09-25-thymeandplace-04-changesets.md) | one edit path with diff, calibration before/after, bars, atomic audited apply; daily drift check | 1–3 |
| 5 | [Public web and server](2026-09-25-thymeandplace-05-public-web.md) | middleware, cache, data-version watcher, all public pages and error states, `server` binary | 1–4 |
| 6 | [Admin authentication](2026-09-25-thymeandplace-06-admin-auth.md) | passkeys, backup password+TOTP, sessions, CSRF, lockout, rate limit, audit, Security page, `admin` CLI | 1–5 |
| 7 | [Admin catalog UI](2026-09-25-thymeandplace-07-admin-catalog-ui.md) | catalog list, kind-aware form, preview/apply, import/export, history | 1–6 |
| 8 | [Deployment, backups, E2E, CI](2026-09-25-thymeandplace-08-deploy-e2e-ci.md) | images, Compose + Caddy, backups with restore test, Playwright scenarios, GitHub Actions, runbook | 1–7 |
| 9 | [Launch content](2026-09-25-thymeandplace-09-launch-content.md) | ≥60 land + ≥15 seafood catalog, ~15 published-guide fixtures, calibration baseline | 1–8 (fixtures can start after 1) |

## Before starting

- **Go ≥ 1.25 on PATH.** This machine has Go 1.22.5 at `C:\Program Files\Go\bin`, not on PATH. Install 1.25+ or rely on `GOTOOLCHAIN=auto`.
- Docker Desktop running (testcontainers, sqlc, images). Python 3.12 (transform tests). Node 22 (E2E).
- The repo isn't a git repository yet; Plan 1 Task 1 runs `git init`.
- Commands are written for Git Bash on Windows and work unchanged on Linux CI.

## Spec issues found while planning, and how the plans resolve them

1. **Per-kind `CHECK`s let NULLs through (§8.1).** `grow_temp_max_c > grow_temp_min_c` is NULL when either side is NULL, and Postgres treats a NULL `CHECK` as passing. Plan 2 adds explicit `IS NOT NULL` to every required per-kind column, and a shared table of invalid examples proves app and DB agree.
2. **Invalid `date`: §11.6 says 400, §12.5 says fall back to the server date.** Plan 5 follows §12.5 (never dead-end). Invalid lat/lng still get 400.
3. **Traffic gating vs the DB-outage retry panel (§14 vs §11.6 and §13.9 scenario 3).** `/readyz` includes a DB ping, so gating the only replica on it would take the whole site down during a DB outage and the retry panel could never appear. Plan 8 gates Caddy on `/healthz`, and the server does not listen until its first catalog snapshot loads, which keeps "not served without a snapshot". `/readyz` is unchanged for monitoring.
4. **Tropical perennial fruit never shows (§12.3).** In climates that never cross `warmup_temp_c` there is no warm-up month, so the fruit is "not local". Plan 1 tests this as specified. Plan 9's authoring guide says to model year-round tropical fruit as `cultivated_year_round`.
5. **Compose doesn't restart unhealthy containers (§14 "`/healthz` (restart)").** Plan 8 documents this. The app exits on fatal errors and `restart: unless-stopped` covers crashes; add a supervisor if you want health-based restarts.
6. **Values the spec leaves open**, now set in the plans: "little in season" = fewer than 3 land items at peak or in season; "near X" labels only within 50 km; login "attempts" = passkey finish and backup POSTs (rate-limited requests are logged but not counted).

## Additions beyond the spec (small, called out in each plan)

- `dataset_versions.promoted_at` (rollback returns only to versions that were live) and a trigram index on `place.name` (accented searches). Plans 2 and 3.
- `ingest <dataset> approve-license --version N --yes`; a new fetch inherits the review only when the license text and source URLs are unchanged. Plan 3.
- Enrollment routes `/admin/enroll…`, which the CLI link needs. WebAuthn ceremony state kept in a short-lived AES-GCM-sealed cookie, so any replica can finish a ceremony. Plan 6.
- Refusing to remove the last passkey; reserving the slug `new`; refusing "new" ingredients whose slug already exists. Plans 6 and 7.
- A maintenance page from Caddy when the app is unreachable. Plan 8.

## Needs a human

- Source URLs were verified on 2026-09-25 and are built into Plan 3: MEOW comes from the Esri Oceans feature service as GeoJSON, and TerraClimate from the THREDDS `climatology` folder. **The MEOW item states no licence**, so confirm its terms before `approve-license`.
- Pick ~15 published seasonal guides and transcribe their months into fixtures (Plan 9). The plan forbids inventing them.
- Author the launch catalog parameters and tips, and look up seafood ecoregion IDs (Plan 9).
- Check that `thymeand.place` is available before public use (spec header).
