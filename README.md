# Thyme & Place

What's in season where you live, how to buy it, and what it goes with. To launch at `thymeand.place`.

A cook searches for a town, or shares their location, and gets this month's in-season fruit, vegetables, herbs and mushrooms. Each ingredient comes with tips on choosing and storing it, what it pairs with, and a 12-month season chart for that place.

Every result is an estimate. The engine looks up 1991–2020 monthly climate normals (TerraClimate) for the 0.25° grid cell around a location and applies simple per-ingredient rules: growing temperatures, frost, months to harvest, winter chill and storage life. A calibration score checks the output against published seasonal guides for about fifteen cities. The About page ([`/about/data`](https://thymeand.place/about/data)) explains the method to visitors.

Seafood (hand-curated months per marine ecoregion) is built but switched off until its dataset's license is confirmed. See `FEATURE_SEAFOOD` below and `docs/reference-data-licenses.md`.

## Stack

- **Go** app serving the public site and `/admin`. Pages are server-rendered with [templ](https://templ.guide), and [htmx](https://htmx.org) swaps in the partials.
- **Postgres + PostGIS + pg_trgm**, accessed with `sqlc` + `pgx`. `goose` runs the migrations.
- **Python + xarray**, used only to turn the climate NetCDF files into grid-cell normals (`pipeline/transform`).
- **Docker Compose and Caddy** on one VPS. Caddy handles TLS and compression.
- **Admin sign-in** is by passkey (WebAuthn), with a password + TOTP backup.

## Repository layout

| Path | What's there |
|---|---|
| `cmd/server` | The web app: the public site, `/admin`, and `-migrate-only` for the migrate service |
| `cmd/ingest` | Reference-data pipeline CLI (climate, ocean, places): fetch, approve license, validate, swap, roll back |
| `cmd/admin` | Admin CLI: enroll a passkey, set the backup login, revoke sessions |
| `cmd/devseed` | Seeds a small synthetic dataset and catalog for local development and e2e (refuses to run without `DEVSEED_ALLOW=1`) |
| `internal/season` | The engine: pure functions, no I/O |
| `internal/catalog`, `internal/changeset` | Ingredient catalog, in-memory snapshot, previewed and audited edits |
| `internal/web`, `internal/admin` | Handlers, middleware and templ views |
| `internal/climate`, `internal/ocean`, `internal/places` | Location lookups against the reference data |
| `internal/ingest` | Versioned dataset ingestion with license gating and atomic swap |
| `calibration/fixtures` | Published seasonal guides the engine is scored against |
| `content/catalog/launch.json` | The launch catalog; see `content/AUTHORING.md` |
| `migrations` | goose SQL migrations |
| `static` | CSS, small JS, vendored htmx |
| `deploy` | Compose files, Caddyfiles, env file templates, backup and restore |
| `e2e` | Playwright tests against the Compose stack |
| `docs` | Operations runbook, license register, design specs and plans |

## Local development

You need Go 1.26, Docker, and optionally Node 22 (e2e) and Python 3.12 (pipeline tests).

```bash
# Postgres with the production roles, on localhost:55432
docker compose -f deploy/compose.dev.yaml up -d

# Migrate, and seed synthetic reference data plus a small catalog (as tp_owner)
DEVSEED_ALLOW=1 DATABASE_URL='postgres://tp_owner:tp_owner@localhost:55432/thymeandplace?sslmode=disable' go run ./cmd/devseed

# Run the app as the least-privilege role
DATABASE_URL='postgres://tp_app:tp_app@localhost:55432/thymeandplace?sslmode=disable' go run ./cmd/server
```

Then open <http://localhost:8080> and search for "Lisbon". To try `/admin` locally, also set `ADMIN_SECRET_KEY` (`openssl rand -base64 32`), `ADMIN_RP_ID=localhost` and `ADMIN_ORIGIN=http://localhost:8080`, then enroll with `go run ./cmd/admin enroll` using the same environment.

After changing a `.templ` file or a SQL query, regenerate and commit the output (CI fails if it's stale):

```bash
go tool templ generate
./scripts/sqlc.sh generate   # runs sqlc in Docker
```

## Tests

```bash
go test ./...                              # unit, engine, calibration, store and pipeline tests; starts PostGIS containers
python -m pytest pipeline/tests -q         # after: pip install -r pipeline/transform/requirements-dev.txt
e2e/run.sh                                 # Playwright against the full Compose stack
deploy/smoke.sh                            # Compose smoke test: roles, migrate, health, maintenance page
```

CI (`.github/workflows/ci.yml`) runs the first two, checks that generated code is current, and validates the Compose layout. The e2e job runs on `main`.

## Configuration

The app reads its settings from the environment. In production they live in `/etc/thymeandplace/app.env` (template: `deploy/env/app.env.example`).

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | required | Postgres URL; production connects as `tp_app` |
| `ADDR` | `:8080` | Listen address |
| `TRUST_PROXY_HEADERS` | off | `true` behind Caddy, so rate limits see the client IP |
| `DB_MAX_CONNS` | `16` | Pool size; public requests may use three quarters of it |
| `FEATURE_SEAFOOD` | `off` | `on` shows seafood; needs a live ocean dataset with a confirmed license |
| `PUBLIC_BASE_URL` | `ADMIN_ORIGIN` | Public origin for enroll links, `robots.txt`, `sitemap.xml` and `llms.txt` |
| `ADMIN_SECRET_KEY` | unset | 32 bytes, base64; `/admin` is disabled without it |
| `ADMIN_RP_ID`, `ADMIN_ORIGIN` | `thymeand.place`, `https://` + RP ID | WebAuthn relying party |
| `ADMIN_BACKUP_LOGIN` | on | `off` disables the password + TOTP backup login |

## Deployment and operations

- `docs/operations.md`: the runbook, covering first deploy, database roles, ingesting reference data, backups and restores, the pre-launch checklist, and turning seafood on.
- `docs/hosting-hetzner.md`: the recommended low-cost Hetzner Cloud setup.
- `docs/reference-data-licenses.md`: the license register for every dataset the pipeline downloads.
- `content/AUTHORING.md`: how to write and check catalog entries.
- `docs/superpowers/specs/`: design specs, starting with the core engine design (`2026-09-24-thymeandplace-core-engine-design.md`).

## License

Copyright © 2026 Kaiser Wade. All rights reserved. See [LICENSE.md](LICENSE.md). Third-party code and data keep their own licenses, listed there. Contributions are by invitation; see [CONTRIBUTING.md](CONTRIBUTING.md).
