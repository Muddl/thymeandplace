# Thyme & Place — Sub-project 1: Core Seasonal Engine — Design

- **Status:** Draft for review (revision 2)
- **Date:** 2026-09-24
- **Name / domain:** Thyme & Place — `thymeand.place` (confirm availability before public use)
- **Repo location:** `J:\DevStuff\GoLang\thymeandplace`
- **Spec path in repo:** `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md`

---

## 1. Purpose

Thyme & Place is a public website that helps home cooks anywhere in the world find out what is in season where they live, how to buy and store it, and what it goes well with.

Sub-project 1 builds the **core seasonal engine**: location in → estimated in-season ingredients out, served as fast server-rendered pages. It also includes a **minimal admin UI** for curating the ingredient catalog, which lives in the database.

### Success criteria

1. A cook anywhere on land or near a coast gets a plausible, clearly labeled in-season list within ~1 second (p95, warm cache) or ~2 seconds (cold).
2. For places with published seasonal guides, the engine's output broadly agrees with them, measured by the calibration score (§12). No catalog change lowers it without an explicit override.
3. Every result is labeled as an estimate, and the site explains how estimates are made.
4. The site never dead-ends: every failure produces a useful, honest message and a next step (§11).
5. The owner can add, edit, bulk-import and export catalog data without a deploy, and sees the effect of each change on seasons before applying it.

## 2. Scope

### In scope

- Location input via city search or browser geolocation.
- Seasonal estimates for fruits, vegetables, herbs, mushrooms (cultivated and wild-at-market), and seafood.
- Per-ingredient buying tips (choose, store) and pairings.
- Ingredient detail view with a 12-month season bar for the user's location.
- Ingredient catalog stored in Postgres with full change history.
- Admin UI (single owner): passkey login with password + TOTP backup; catalog forms; JSON import/export; change preview showing calibration score change and sample-city season bars.
- A data ingestion pipeline for reference datasets.
- An "About the data" page: sources, attribution, and how estimates work.
- Containerized deployment on one VPS.

### Out of scope (later sub-projects, in order)

| # | Sub-project | Hook left in core |
|---|---|---|
| 2 | **Corrections** — user reports; per-area auto-adjust past a threshold; admin review and override | `season.Adjuster` interface (no-op in core); data-version bump + `NOTIFY`; admin shell, auth, and history table to extend |
| 3 | **Where to buy nearby** — markets near the user | Ingestion pipeline supports scheduled sources; PostGIS in place |
| 4 | **Recipes** — recipes using in-season ingredients | `ingredient.slug` is a stable key for foreign keys |

Also out of scope for core: multiple admin users, public accounts, confidence scores, rainfall limits for irrigated crops, and "grows here but isn't sold here" modeling.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| Coverage | Worldwide climate-based estimates from launch, corrected later by users | Curated regions only (limited reach); nearest curated region (fails silently) |
| Land estimation | Rules computed from monthly climate normals per location | Köppen class lookup (too coarse, authoring grows as classes × ingredients); per-location LLM calendars (not reproducible or auditable) |
| Seafood | In core, hand-curated species × marine ecoregion × months | Climate model (cannot predict spawning or fishing regulations) |
| Wild mushrooms | Shown as "look for at markets" with a fixed no-foraging disclaimer | Foraging guidance (misidentification risk) |
| Climate data | TerraClimate (CC0) | WorldClim (non-commercial only, no redistribution) |
| Shape | One containerized Go app + Postgres/PostGIS | Separate frontend/backend services (extra contract and hop without current benefit; can split later along package seams) |
| Stack | Go, `templ`, HTMX, Postgres + PostGIS + `pg_trgm`, `sqlc` + `pgx`, `goose`, Python + xarray (pipeline transform only), Docker Compose, Caddy | Spring Boot + Thymeleaf (heavier footprint); SPA frameworks (unneeded JS/SEO complexity) |
| Hosting | Single small VPS | Managed platforms (not needed at this scale) |
| Reference data ingestion | Versioned, repeatable pipeline with atomic swap | One-off loader (no safe refresh, provenance, or rollback) |
| Catalog storage | Postgres is the source of truth; constraints + trigger-based history | YAML in repo (no relational integrity; edits need deploys; later sub-projects join against it) |
| Catalog authoring | Admin UI in core: forms + JSON import/export + effect preview | CLI import only; SQL by hand |
| Admin access | Public `/admin`, single owner, passkey + password/TOTP backup, CLI enrollment and recovery | Private port via tunnel (less convenient); multiple editors (not needed yet) |

## 4. Architecture

### 4.1 Runtime

Docker Compose on one VPS runs three services:

- **caddy** — TLS termination and reverse proxy to `app`.
- **app** — the stateless Go binary serving both the public site and `/admin`. Can be scaled to N replicas later without code changes.
- **db** — Postgres with PostGIS and `pg_trgm`.

A one-shot **migrate** service (the app image with `-migrate-only`, as `tp_owner`) applies migrations before `app` starts; `app` depends on it completing successfully and refuses to start while migrations are pending. The ingestion pipeline (`ingest`, `transform`), backups, restores and the admin CLI run as on-demand commands via `docker compose run` or `exec`. (Amended by `2026-09-27-least-privilege-deployment-design.md`.)

### 4.2 Repository layout

```
thymeandplace/
  cmd/
    server/            # web app (public + /admin)
    ingest/            # reference-data pipeline CLI
    admin/             # admin CLI: enroll, set-backup, revoke-sessions
  internal/
    catalog/           # domain types, validation, in-memory snapshot loader
    changeset/         # proposed catalog changes: build, validate, preview, apply
    calibration/       # scoring + embedded fixtures (admin-only use)
    climate/           # lat/lng → cell ID → monthly normals
    ocean/             # lat/lng → nearby marine ecoregions
    places/            # city search, reverse lookup
    season/            # pure engine + Adjuster interface
    auth/              # WebAuthn, password+TOTP, sessions, CSRF
    store/             # sqlc-generated queries behind small interfaces
    cache/             # versioned in-memory LRU
    web/               # public handlers, middleware, templ views
    admin/             # admin handlers and templ views
  calibration/
    fixtures/*.json    # published-guide fixtures, embedded into the binary
  pipeline/
    transform/         # Python + xarray (own Dockerfile)
    tests/             # pytest
  migrations/          # goose SQL migrations
  static/              # vendored htmx.min.js, CSS, small JS for geolocation + WebAuthn
  testdata/            # small test catalogs and datasets
  deploy/
    compose.yaml
    Caddyfile
  docs/superpowers/specs/
```

### 4.3 Package responsibilities

| Package | Does | Depends on |
|---|---|---|
| `catalog` | Domain types; app-level validation; loads the DB catalog into an immutable in-memory `Snapshot`, swapped atomically on version change | `store` |
| `changeset` | Represents a set of upserts/deletes; validates against constraints and live ecoregions; computes the field-level diff; runs the preview; applies in one transaction | `catalog`, `calibration`, `season`, `store` |
| `calibration` | Scores a catalog snapshot against fixtures (mean Jaccard) | `season`, embedded fixtures |
| `climate` / `ocean` / `places` | As in §6 | `store` |
| `season` | `Estimate(input, snapshot, month) []Result`; pure, no I/O | `catalog` types only |
| `auth` | Passkey registration and login, backup login, sessions, CSRF, enrollment tokens | `store` |
| `store` | All SQL via `sqlc`; exposes narrow interfaces | `pgx` |
| `cache` | LRU keyed by `(cellID, month, dataVersion)` | nothing |
| `web` / `admin` | Routes, middleware, `templ` views | the above |

`season` and `calibration` must be testable with no database.

## 5. Data sources and licensing

| Dataset | Use | License | Obligation |
|---|---|---|---|
| TerraClimate (Abatzoglou et al., 2018) | Monthly tmin, tmax, precipitation → 1991–2020 normals | CC0 | Cite (courtesy) |
| Marine Ecoregions of the World (Spalding et al., 2007) | Ocean regions for seafood | **Unresolved** (`docs/reference-data-licenses.md`) | Not used while seafood is off (§11.7); citation only until the terms are confirmed |
| Natural Earth 1:50m land | Clipping ocean regions to water | Public domain (as recorded) | None; credited as a courtesy |
| GeoNames cities | City search and reverse lookup | CC BY 4.0 | Attribution on About the data page |

Rule: **no reference dataset can be promoted to live unless its license is recorded in `dataset_versions` and marked reviewed.** The ingestion CLI enforces this. A license whose terms haven't been confirmed carries the `UNRESOLVED:` marker (`ingest.LicenseUnresolved`); such a version can't be approved and never passes validation. `docs/reference-data-licenses.md` is the register of every source file's terms and evidence, and each dataset's `Attribution()` lines must appear on the About page (a test checks this). (Amended by `2026-09-27-reference-data-provenance-design.md`.)

## 6. Reference data model

### 6.1 Climate grid

- Resolution: **0.25°** (~28 km at the equator), land cells only.
- Cell ID: `row = floor((90 - lat) / 0.25)`, `col = floor((lng + 180) / 0.25)`, `cell_id = row * 1440 + col`. Latitude is clamped to [-90, 90); longitude is normalized to [-180, 180).
- Normals period: 1991–2020.

```sql
CREATE TABLE climate_cell_v{N} (
  cell_id   integer PRIMARY KEY,
  tmin_c    real[12] NOT NULL,
  tmax_c    real[12] NOT NULL,
  precip_mm real[12] NOT NULL
);
CREATE VIEW climate_cell AS SELECT * FROM climate_cell_v{N};
```

Nearest-land fallback: if `cell_id` is absent, check the 8 neighboring cells and use the one whose center is nearest the query point. If none exist, the location is unsupported (§11).

### 6.2 Marine ecoregions

```sql
CREATE TABLE ocean_ecoregion_v{N} (
  ecoregion_id integer PRIMARY KEY,
  name         text NOT NULL,
  geom         geography(MultiPolygon, 4326) NOT NULL
);
CREATE INDEX ON ocean_ecoregion_v{N} USING gist (geom);
CREATE VIEW ocean_ecoregion AS SELECT * FROM ocean_ecoregion_v{N};
```

"Nearby" means `ST_DWithin(geom, point, 100000)`, i.e. within **100 km**. Inland points return none, and the seafood section is omitted.

### 6.3 Places

```sql
CREATE TABLE place_v{N} (
  place_id   integer PRIMARY KEY,
  name       text NOT NULL,
  ascii_name text NOT NULL,
  admin1     text,
  country    text NOT NULL,
  population integer NOT NULL,
  geom       geography(Point, 4326) NOT NULL
);
CREATE INDEX ON place_v{N} USING gin (ascii_name gin_trgm_ops);
CREATE INDEX ON place_v{N} USING gist (geom);
CREATE VIEW place AS SELECT * FROM place_v{N};
```

Search returns the top 8 matches by trigram similarity, with ties broken by population.

### 6.4 Dataset versions

```sql
CREATE TABLE dataset_versions (
  id               bigserial PRIMARY KEY,
  dataset          text NOT NULL,          -- 'climate' | 'ocean' | 'places'
  table_name       text NOT NULL,
  source_url       text NOT NULL,
  source_checksum  text NOT NULL,
  license          text NOT NULL,
  license_reviewed boolean NOT NULL DEFAULT false,
  loaded_at        timestamptz NOT NULL DEFAULT now(),
  is_live          boolean NOT NULL DEFAULT false
);
```

## 7. Reference data ingestion pipeline

```
ingest <dataset> fetch|transform|load|validate|swap|all [--version N]
ingest <dataset> rollback
```

1. **Fetch.** Download raw files and record URL, checksum, license, and date. Raw files go to an S3-compatible bucket.
2. **Transform.** Produce load-ready CSV. For climate, this is the Python + xarray container: select 1991–2020, group by month, take the mean, regrid to 0.25°, mask to land, one row per cell. Output is saved to the bucket so it runs once per source version. Ocean and places transforms run in Go.
3. **Load.** Create `*_v{N}` and fill it with `COPY`. The live view is not touched.
4. **Validate.** Any failure stops the pipeline with a non-zero exit:
   - Row counts within ±5% of the previous live version (skipped on the first load).
   - Climate values plausible (tmin/tmax −80 to 60 °C; tmin ≤ tmax; precipitation ≥ 0).
   - At least 10 fixed spot-check coordinates, across both hemispheres, the tropics and high latitudes, fall within stated tolerances.
   - License recorded and `license_reviewed = true`.
   - **Ocean only:** every `seafood_region.ecoregion_id` in the catalog exists in the new version.
5. **Swap.** In one transaction: repoint the view, flip `is_live`, and `NOTIFY data_version_changed`. Previous versions are kept. Versions older than the last 2 may be dropped manually.

`rollback` repoints to the previous version in one transaction and sends the same notification. In core, all runs are manual; sub-project 3 adds scheduling.

## 8. Ingredient catalog (database)

### 8.1 Schema

```sql
CREATE TYPE ingredient_category AS ENUM ('fruit','vegetable','herb','mushroom','seafood');
CREATE TYPE ingredient_kind AS ENUM
  ('annual','perennial_fruit','cultivated_year_round','wild_mushroom','seafood');
CREATE TYPE frost_tolerance AS ENUM ('tender','hardy');

CREATE TABLE ingredient (
  slug       text PRIMARY KEY CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
  name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  category   ingredient_category NOT NULL,
  kind       ingredient_kind NOT NULL,
  -- annual / perennial_fruit
  frost                   frost_tolerance,
  grow_temp_min_c         real,
  grow_temp_max_c         real,
  months_to_harvest       smallint,
  storage_months          smallint NOT NULL DEFAULT 0 CHECK (storage_months BETWEEN 0 AND 9),
  -- perennial_fruit
  chill_months_min        smallint,
  warmup_temp_c           real,
  months_bloom_to_harvest smallint,
  harvest_span_months     smallint,
  -- wild_mushroom
  fruiting_temp_min_c     real,
  fruiting_temp_max_c     real,
  min_precip_mm           real,
  -- content
  tip_choose text NOT NULL,
  tip_store  text NOT NULL,
  note       text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now(),

  CHECK ((kind = 'seafood') = (category = 'seafood')),
  CHECK (kind <> 'wild_mushroom' OR category = 'mushroom'),
  CHECK (kind <> 'annual' OR (
    frost IS NOT NULL AND grow_temp_min_c IS NOT NULL
    AND grow_temp_max_c > grow_temp_min_c
    AND months_to_harvest BETWEEN 1 AND 11)),
  CHECK (kind <> 'perennial_fruit' OR (
    frost IS NOT NULL
    AND chill_months_min BETWEEN 0 AND 12
    AND warmup_temp_c IS NOT NULL
    AND months_bloom_to_harvest BETWEEN 1 AND 11
    AND harvest_span_months BETWEEN 1 AND 12)),
  CHECK (kind <> 'wild_mushroom' OR (
    fruiting_temp_max_c > fruiting_temp_min_c AND min_precip_mm >= 0))
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
```

Pairings are directional as authored; the detail page shows only the ingredient's own list.

`ecoregion_id` cannot be a foreign key because ecoregion tables are versioned. Integrity is enforced in two places: `changeset` validation checks against the live `ocean_ecoregion` view, and ocean ingestion validation (§7) checks the other direction.

The wild-mushroom disclaimer is fixed UI copy, shown for every `wild_mushroom` item. It is not stored per row, so it can't be forgotten.

### 8.2 History and versioning

```sql
CREATE TABLE catalog_history (
  id         bigserial PRIMARY KEY,
  changed_at timestamptz NOT NULL DEFAULT now(),
  actor      text NOT NULL,        -- from current_setting('app.actor', true); 'sql' if unset
  change_id  bigint,               -- groups rows applied by one changeset
  action     text NOT NULL CHECK (action IN ('insert','update','delete')),
  table_name text NOT NULL,
  row_key    jsonb NOT NULL,
  before     jsonb,
  after      jsonb
);

CREATE TABLE catalog_meta (
  id      boolean PRIMARY KEY DEFAULT true CHECK (id),
  version bigint NOT NULL DEFAULT 0
);
```

- Row-level triggers on `ingredient`, `ingredient_pairing`, and `seafood_region` write `catalog_history`.
- A statement-level trigger on the same tables increments `catalog_meta.version` and runs `NOTIFY data_version_changed`.
- The app sets `SET LOCAL app.actor` and `SET LOCAL app.change_id` inside each apply transaction. Changes made directly in SQL are still recorded, with actor `sql`.

### 8.3 Data version

**Data version** = SHA-256 of (`catalog_meta.version` + the IDs of all live `dataset_versions` rows), truncated to 16 hex characters. The app recomputes it at startup and on every `data_version_changed` notification.

### 8.4 In-memory snapshot

At startup, and on each version change, `catalog` loads all three tables into an immutable `Snapshot`, validates it at the app level, and swaps it in atomically. If a reload fails validation, the previous snapshot stays live and an error is logged. At startup there is no previous snapshot, so the server doesn't listen until a valid load succeeds (§11.6).

### 8.5 Launch catalog

**At least 60 land ingredients and 15 seafood species**, with every category represented, entered through JSON import (§9.4).

## 9. Admin UI

### 9.1 Routes (all under `/admin`; `templ` + HTMX)

| Route | Purpose |
|---|---|
| `GET /admin/login` | Passkey sign-in button; "Use backup login" link |
| `POST /admin/login/passkey/*` | WebAuthn begin/finish |
| `GET/POST /admin/login/backup` | Password + TOTP form (only if enabled) |
| `POST /admin/logout` | End session |
| `GET /admin` | Catalog list: search by name/slug, filter by category and kind |
| `GET /admin/ingredients/new`, `GET /admin/ingredients/{slug}` | Form. Changing `kind` swaps in the right field set via HTMX |
| `POST /admin/ingredients/{slug}/preview` | Builds a one-item changeset → preview page |
| `POST /admin/ingredients/{slug}/delete/preview` | Delete changeset → preview (confirmation requires typing the slug) |
| `GET/POST /admin/import` | Upload JSON → changeset → preview |
| `POST /admin/changesets/{id}/apply` | Apply a previewed changeset |
| `GET /admin/export` | Download full catalog JSON |
| `GET /admin/history` | `catalog_history`, newest first, filter by slug |
| `GET /admin/security` | List and rename passkeys; add a passkey; remove a passkey (needs re-auth within 5 min) |

### 9.2 Changesets: one path for every edit

Every change, whether a single form save, a delete, or an import, goes through the same flow:

1. **Build.** A set of upserts and deletes.
2. **Validate.** App-level rules that mirror the DB constraints, plus referential checks: pairing targets must exist in the snapshot or the changeset, and ecoregion IDs must exist in the live data. Errors are shown per item and per field, and nothing is stored.
3. **Preview.** The changeset is stored in `pending_changeset` (id, payload JSON, created_at, expires after 1 hour). The preview page shows:
   - **Field diff:** added, changed and deleted items, with field-level before and after for changes. Unchanged items are counted but collapsed.
   - **Calibration:** overall score before and after, and the change per affected ingredient.
   - **Sample-city season bars:** for each changed ingredient, 12-month bars before and after for every calibration fixture location.
4. **Apply.** One transaction applies every row with `app.actor` and `app.change_id` set, deletes the pending changeset, and fires the version-bump trigger. If the calibration score drops by more than **0.02**, the Apply button requires ticking "Apply despite lower calibration score." The override is recorded in the history row's `after` metadata.

The pending changeset is re-validated at apply time against the current snapshot. If another change landed in between, the apply is rejected with "The catalog changed since this preview. Preview again."

### 9.3 Preview computation

The calibration fixtures (§12.2) are embedded in the binary. They are read by the admin area only and never shown on public pages. Normals for the fixture locations (about 15 cells) are fetched once per preview. `calibration.Score(snapshot)` runs twice, on the current and proposed snapshots, and each run completes in milliseconds.

### 9.4 Import and export format

Export and import use the same JSON document:

```json
{
  "format": "thymeandplace-catalog/v1",
  "exported_at": "2026-09-24T12:00:00Z",
  "ingredients": [
    {
      "slug": "tomato", "name": "Tomato", "category": "vegetable", "kind": "annual",
      "frost": "tender", "grow_temp_c": [16, 30], "months_to_harvest": 3, "storage_months": 0,
      "tips": { "choose": "Heavy for its size, glossy, fragrant at the stem.",
                "store": "Stem-side down at room temperature; refrigerate only when fully ripe." },
      "pairings": ["basil", "olive-oil", "garlic"],
      "note": ""
    },
    {
      "slug": "atlantic-cod", "name": "Atlantic cod", "category": "seafood", "kind": "seafood",
      "regions": [{ "ecoregion_id": 25, "months": [1,2,3,4], "peak_months": [2,3] }],
      "tips": { "choose": "Firm, translucent flesh; clean sea smell.",
                "store": "Coldest part of the fridge; cook within 2 days." },
      "pairings": ["potato", "butter", "dill"]
    }
  ]
}
```

- Import is **upsert by slug only**. It never deletes, so a partial file can never remove data. Deleting is always a separate, deliberate action.
- For an ingredient in the file, its `pairings` and `regions` lists replace the stored lists entirely.
- Maximum upload size: **5 MB**. An unknown `format` value is rejected.

## 10. Admin authentication and security

### 10.1 Methods

- **Primary: passkey (WebAuthn)** via `go-webauthn/webauthn`. The relying-party ID is `thymeand.place` in production and `localhost` in development. User verification is required.
- **Backup: password + TOTP**, both always required. The password is stored as an Argon2id hash. The TOTP secret is encrypted at rest with AES-GCM using `ADMIN_SECRET_KEY` from the environment. It can be disabled with `ADMIN_BACKUP_LOGIN=off`.

### 10.2 Enrollment and recovery (CLI, requires server shell access)

- `admin enroll` prints a single-use passkey registration link, stored hashed and valid for **15 minutes**.
- `admin set-backup` prompts for a new password and prints the TOTP `otpauth://` URI and a terminal QR code. It replaces any existing backup credentials.
- `admin revoke-sessions` ends all sessions.

The expected setup is two passkeys (for example, phone plus hardware key or password manager) and the backup credentials.

### 10.3 Sessions

- 32-byte random token. Only its SHA-256 hash is stored, in `admin_session`.
- Cookie: `__Host-tp_admin`, `HttpOnly`, `Secure`, `SameSite=Strict`, `Path=/`.
- Expiry: **30 minutes idle, 12 hours absolute**.
- Sensitive actions (adding or removing passkeys) require authentication within the last **5 minutes**.

### 10.4 Protections

- **CSRF:** a per-session synchronizer token in every form, and sent by HTMX as a header (`hx-headers` on `<body>`). It's checked on every non-GET admin request.
- **Login rate limit:** 5 failed attempts per 15 minutes per client (an IPv4 address or an IPv6 /64), across the passkey, re-auth and backup sign-in routes, checked before any audited work. Enrollment has its own budget of 5 failed attempts. Refused attempts are recorded once per client per window. If the check itself fails, the answer is 503 and nothing is recorded.
- **Admin request limit:** per client, in memory, before any database work: sign-in and enrollment routes 1 req/s burst 20; other `/admin` routes 10 req/s burst 100; 429 with `Retry-After`.
- **Backup lockout:** after 5 consecutive failures, the backup path locks globally for 15 minutes, doubling each time it repeats, up to 24 hours. Passkey login keeps working during a lockout. A global lock is acceptable because there is one user, and it stops distributed guessing.
- **Audit:** every login attempt that reaches a credential check goes to `admin_login_log` (time, IP, client key, method, outcome), kept 90 days. Successful backup logins also log at `WARN`.
- **Headers on every response** (public and admin, set in the app by `web.SecurityHeaders`): strict CSP (self only, `object-src 'none'`, `frame-ancestors 'none'`, no inline script or style), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: same-origin`, a `Permissions-Policy` allowing only geolocation for self, and `Cross-Origin-Opener-Policy`/`Cross-Origin-Resource-Policy: same-origin`. `/admin` replaces the CSP (adding `data:` images) and adds `Cache-Control: no-store`, `X-Robots-Tag: noindex` and `Referrer-Policy: no-referrer`. (Amended by `2026-09-27-public-origin-hardening-design.md`.)

### 10.5 Tables

`admin_passkey` (credential ID, public key, sign count, name, created_at, last_used_at), `admin_backup` (single row: password hash, encrypted TOTP secret, lockout state), `admin_session`, `admin_enrollment_token`, `admin_login_log`, `pending_changeset`.

The audit tables are append-only for the app's database role: it can read `catalog_history` and `admin_login_log` and insert into the login log, but not update or delete either. History rows are written by `SECURITY DEFINER` triggers owned by `tp_owner`, and old login-log rows are removed only through `prune_admin_login_log(retention)`, which refuses a retention under 30 days.

## 11. Public web layer

### 11.1 Routes

| Route | Returns |
|---|---|
| `GET /` | Full page: search box, "Use my location" button, empty results region |
| `GET /places?q=` | Partial: up to 8 matches (`hx-trigger="keyup changed delay:250ms"`) |
| `GET /season?lat=&lng=&date=` | Partial: location header + in-season list grouped Peak / In season / From storage / Year-round, with seafood as its own group |
| `GET /ingredient/{slug}?cell=&date=` | Partial: tips, pairings, 12-month season bar, disclaimer or note |
| `GET /about/data` | Full page: sources, licenses, attribution, how estimates work, privacy |
| `GET /robots.txt` | Plain text: disallows `/admin`, `/places` and the health checks; points to the sitemap |
| `GET /sitemap.xml` | The home and About pages, plus a season page for each calibration city |
| `GET /llms.txt` | Markdown summary for language models (llmstxt.org): what the site does, how far to trust it, featured season pages and the ingredient list. Follows the seafood flag (§11.7) |
| `GET /healthz` | 200 if the process is up |
| `GET /readyz` | 200 if a catalog snapshot is loaded and a database ping succeeded within the last second (one shared ping, reused for 1 s; each ping times out after 500 ms) |

Requests without an `HX-Request` header get full-page versions of `/season` and `/ingredient`, so URLs are shareable and indexable. The absolute URLs in `robots.txt`, `sitemap.xml` and `llms.txt` use `PUBLIC_BASE_URL` (default: the admin origin, `https://thymeand.place`).

### 11.2 `/season` flow

1. Validate lat/lng; compute `cell_id`.
2. Cache lookup on `(cell_id, month, dataVersion)`. On a hit, render.
3. On a miss: `climate.Normals` (with fallback), `ocean.Nearby`, `season.Estimate` against the current snapshot, `Adjuster.Adjust`, then cache and render. The miss's queries run while holding one public DB slot (§11.4).
4. Place label: from the search selection if present, otherwise `places.Nearest`. The selection lookup also takes a DB slot; if none is free it falls back like any failed lookup.

### 11.3 Caching

- In-process LRU, **50,000 entries**.
- `/ingredient` keeps a second LRU of **20,000** per-cell inputs (climate normals and ecoregions, including cells with no normals), keyed by `(cell_id, dataVersion)`. Failures aren't cached.
- The data version refreshes on `LISTEN data_version_changed`, with a 5-minute poll as a fallback for missed notifications. The `LISTEN` session uses its own connection, outside the pool.
- A version change makes older keys unreachable, and LRU eviction reclaims them.

### 11.4 Middleware

Request ID → structured `slog` access log → panic recovery → per-IP rate limit → per-request timeout (**2 s**).

Public rate limits (token bucket, per IP):
- `/places`: 10 req/s, burst 20.
- `/season` and `/ingredient`: 2 req/s, burst 10.

Over the limit returns 429 with `Retry-After`.

**Load shedding (public DB gate).** Public database work (cache misses on `/season` and `/ingredient`, `/places` searches, the `/season` place label) holds one of a fixed number of slots: three quarters of the server pool, **12 of 16**. A request waits at most **250 ms** for a slot, then gets the retry panel with **503** and `Retry-After: 5`. Cache hits, health checks, static files and `/admin` don't use the gate, so a public spike can't starve the owner. Shedding logs one sampled `INFO "public DB gate full"` line a minute at most.

### 11.5 HTMX

HTMX 2.x, vendored. It's configured to swap 4xx and 5xx responses so error partials render, with `allowEval: false`, `allowScriptTags: false` and `selfRequestsOnly: true` on both the public and admin sides. Templates contain no inline scripts, `style=` attributes, `on*` handlers or `hx-on`; a lint test in each views package enforces this so the CSP (§10.4) never needs `'unsafe-inline'`. Geolocation uses about 20 lines of vanilla JS calling `htmx.ajax` with coordinates and the local date.

### 11.6 Error handling and UX states

Principle: the surrounding page stays usable, and every message says what happened and what to do next.

| Condition | HTTP | What the cook sees |
|---|---|---|
| No city match | 200 | Under the search box: "No places match '…'. Try a nearby larger town or city." |
| Geolocation denied or unavailable | — (client) | Muted hint: "Location access is off, so search for your town instead." Search box focused. No error styling. |
| No land cell after fallback and no ecoregion in reach | 200 | "We can't estimate seasons here yet. Estimates cover land and coastal waters." + "Search another place". With seafood off (§11.7): no land cell, and the copy is "Estimates cover land areas only." |
| Inland | 200 | Seafood group omitted, with no message |
| Harsh climate, little in season | 200 | "Not much grows locally this time of year. Here's what's still around." + year-round, storage, and seafood items |
| Invalid lat/lng/date | 400 | Same panel as the unsupported spot |
| DB unavailable on cache miss | 503 | Warning panel in the results area: "Couldn't load seasonal picks right now. Try again in a moment." + "Try again" (re-issues the same request). Location header stays. |
| Overloaded (no public DB slot within 250 ms) | 503 + `Retry-After: 5` | Same panel as a DB outage, returned at once. `/places` shows "Search isn't available right now. Try again in a moment." |
| Unexpected error or panic | 500 | Same panel as 503. Logged with request ID. |
| Rate limited | 429 | Under the search box: "Search is pausing for a few seconds. Keep typing and results will catch up." Resumes automatically. |
| No catalog snapshot loaded | 503 | The server doesn't listen until a snapshot loads, so Caddy serves the maintenance page. |
| Ingestion validation failure | — | Pipeline exits non-zero naming dataset and step. Live data unchanged. |

Admin errors follow the same style: validation errors appear inline next to their fields, and a stale changeset shows "The catalog changed since this preview. Preview again."

### 11.7 Seafood feature flag

`FEATURE_SEAFOOD` (`on` or `off`, default `off`; any other value is a startup error) switches seafood on the public site (`web.Deps.Seafood`). It exists because the MEOW license (§5) wasn't confirmed by launch, and the site launched with seafood off (`docs/reference-data-licenses.md`, "Launch decision"). With it off:

- `/season` and `/ingredient` make no `ocean.Nearby` call, so no seafood item is ever in season and there's no seafood group;
- seafood items are left out of `/season`, `/ingredient/{slug}` answers 404 for them, and pairings pointing at them are dropped;
- the unsupported panel says estimates cover land areas only, and `/about/data` leaves out the seafood paragraph and the marine credits (MEOW, and Natural Earth as used for ocean clipping).

The admin UI, the ingest pipeline, the calibration drift check and the engine are unaffected. Tests (§13.8) cover both states and check that no public page mentions seafood while it's off.

## 12. Season engine

### 12.1 Interface

```go
type Input struct {
    Normals    *climate.MonthlyNormals // nil if not on land
    Ecoregions []ocean.EcoregionID     // empty if inland
    Month      time.Month
}

type Status int // Peak, InSeason, FromStorage, YearRound

type Result struct {
    Slug     string
    Status   Status
    Months   [12]Status  // full-year view for the detail page; zero value = out of season
    Estimate bool        // true for all land results
    Basis    string      // e.g. "frost-free and warm May–Sep"
}

type Adjuster interface {
    Adjust(ctx context.Context, cellID int, results []Result) []Result
}
```

The core ships `NoopAdjuster`.

### 12.2 Derived monthly values

- `mean[m] = (tmin[m] + tmax[m]) / 2`
- `frostFree[m] = tmin[m] > 2.0`

### 12.3 Rules by kind

**annual**
1. `growable[m] = grow_temp_min_c ≤ mean[m] ≤ grow_temp_max_c && (frost == hardy || frostFree[m])`.
2. If all 12 months are growable, every month is **YearRound**.
3. Otherwise, find maximal runs of consecutive growable months, treating December → January as adjacent.
4. For a run of length L with start S: if L > `months_to_harvest`, the harvest months are `S + months_to_harvest` through the end of the run. Otherwise the run produces no harvest.
5. **Peak** is the middle month of each harvest window if its length is odd, or the middle two if even. Windows of 1–2 months are entirely peak. Other harvest months are **InSeason**.

**perennial_fruit**
1. If the count of months with `mean < 7` is less than `chill_months_min`, the fruit is not local and nothing is shown.
   If `frost == tender` and the coldest month's mean tmin is below 0 °C, the tree does not survive the winter, and nothing is shown. Exactly 0 °C survives. (Ruling G)
2. Warm-up month W is the first month with `mean ≥ warmup_temp_c` that directly follows a month below it.
3. Harvest runs from `W + months_bloom_to_harvest` for `harvest_span_months` months, and must also be frost-free if `frost == tender`.
4. Peak is the middle month or months, as for annuals.

**cultivated_year_round**: every month is **YearRound**.

**wild_mushroom**: **InSeason** in month m if `fruiting_temp_min_c ≤ mean[m] ≤ fruiting_temp_max_c` and `precip[m] ≥ min_precip_mm`. There is no peak. The fixed disclaimer is always displayed.

**Storage (any land kind with `storage_months > 0`)**: the `storage_months` months immediately after each harvest window's end are **FromStorage**, unless they are already a harvest month.

**seafood**: **InSeason** in month m if any listed region is within reach and lists m. **Peak** if it's also in that region's `peak_months`. `Basis = "general guidance; check local fishing rules"`.

### 12.4 Status precedence

Peak > InSeason > FromStorage. YearRound is exclusive.

### 12.5 Month

Taken from the browser's `date` parameter, in local time. If it's missing or invalid, the server's current date in UTC is used.

## 13. Testing

### 13.1 Engine unit tests (Go, table-driven, no DB)

Synthetic climates: tropical, temperate north, temperate south, alpine, polar, and Mediterranean. Every rule in §12.3 has cases covering its thresholds.

Property tests:
- **Hemisphere symmetry.** Rotating all normals by 6 months rotates every `Months` by 6.
- **Wrap invariance.** A run crossing Dec→Jan produces the same harvest as the equivalent unwrapped run.
- **Determinism.** The same input always yields the same output.

### 13.2 Calibration

- About 15 real locations across both hemispheres and several climate types. Each fixture holds per-ingredient months from a published seasonal guide, with the source recorded in the fixture file.
- Score: mean per-ingredient Jaccard overlap between engine months and guide months, over ingredients present in both.
- **Where it runs:** the catalog lives in the database, so calibration gates changes at **apply time** in the admin preview (§9.2), not in CI. CI tests the scoring code itself against a fixed test catalog in `testdata/`.
- **Production drift check:** a daily in-app job scores the live snapshot, stores the result in `calibration_run` (time, data version, score), and logs at `WARN` if the score is lower than the previous run by more than 0.02. This catches score changes that come from reference-data swaps rather than catalog edits.

### 13.3 Catalog and changeset tests

- Every DB `CHECK` and foreign key rejects the invalid rows it's meant to (integration).
- As `tp_app`: catalog load, a full apply (with history rows), login-log inserts, `LISTEN` and the reference views work; updating or deleting audit rows, DDL, `TRUNCATE`, `COPY ... TO PROGRAM` and `pg_read_file` fail with SQLSTATE 42501. A table `tp_owner` creates is readable by `tp_app` without a new grant.
- App-level validation matches the DB constraints, via a shared table of invalid examples run against both.
- History triggers record actor, change ID, and before/after. The version trigger bumps once per statement and sends `NOTIFY`.
- Import: upsert only (never deletes); list replacement for pairings and regions; unknown format rejected; 5 MB limit.
- Apply is atomic: a failure mid-apply leaves no partial changes. A stale changeset is rejected.
- Preview: the calibration change and season bars are correct for a known test catalog. An apply with a score drop above 0.02 requires the override flag and records it.

### 13.4 Auth tests

- Passkey registration and login using a virtual authenticator (unit level through `go-webauthn` test helpers; end-to-end through Playwright's WebAuthn virtual authenticator).
- Backup login with a fixed clock: wrong password, wrong TOTP, replayed TOTP code, lockout and backoff, and the `ADMIN_BACKUP_LOGIN=off` flag.
- Sessions: idle and absolute expiry, `revoke-sessions`, the re-auth window for passkey add/remove.
- CSRF: every non-GET admin route rejects requests missing the token or with a wrong one.
- Enrollment tokens are single-use and expire after 15 minutes.
- Every `/admin` route except login redirects unauthenticated requests.
- The per-client budget buckets IPv6 by /64; refused attempts write at most one row per window; the request limiter answers before any database call; pruning removes only expired rows.

### 13.5 Store integration tests

`testcontainers-go` with the production Postgres/PostGIS image and small fixtures (a few cells, 3 ecoregion polygons, about 50 places, a small catalog). Covers every `sqlc` query, the nearest-land fallback, the 100 km `ST_DWithin` boundary, and trigram ranking.

### 13.6 Pipeline tests

- `pytest`: the xarray transform on a tiny synthetic NetCDF produces the expected normals and grid.
- Go: a failed validation leaves live data unchanged; swap flips `is_live`, repoints the view, and sends `NOTIFY`; rollback restores the previous version; promotion is refused without a reviewed license, and approval and promotion are both refused for an `UNRESOLVED:` license even when `license_reviewed` is set by SQL; every dataset's `Attribution()` appears on the About page; ocean validation fails if catalog ecoregion IDs are missing.

### 13.7 Cache tests

A version change (from a catalog edit or a dataset swap) makes old keys unreachable. A missed notification is corrected by the poll.

### 13.8 Web tests

`httptest` for every public route and every row of §11.6. Full-page versus partial rendering depends on `HX-Request`. Every response, including 404, 429, 500 and static files, carries the §10.4 security headers. Playwright fails a test on any browser console message about Content Security Policy.

### 13.9 End-to-end (Playwright against Compose)

1. Search → select → results render.
2. Geolocation denied → search fallback.
3. Postgres stopped → retry panel → Postgres started → "Try again" succeeds.
4. Admin: passkey login (virtual authenticator) → edit an ingredient → preview shows calibration change and bars → apply → the public page reflects the change without a restart.
5. Admin: import a JSON file → preview → apply → export matches.

### 13.10 CI

Units, scoring, auth, store integration, and pipeline tests run on every push. End-to-end tests run on `main`.

## 14. Deployment and operations

- Images: `app` (distroless; contains `server` and `admin` binaries), `ingest` (Go), `transform` (Python + xarray), official Postgres/PostGIS, Caddy.
- Secrets (`ADMIN_SECRET_KEY`, DB credentials, bucket credentials) come from root-only env files (mode 600) on the host, not stored in the repo: one per service (`db`, `migrate`, `app`, `ingest`, `backup`, `restore`), so no container gets a secret it doesn't use.
- Database roles: the bootstrap superuser only for init and restores; `tp_owner` owns the schema and runs migrations, ingest and devseed; `tp_app` is the runtime (and admin CLI) with no DDL rights, no file or program access and append-only audit tables; `tp_backup` reads everything for `pg_dump` (`pg_read_all_data`).
- Migrations: `goose` runs in the one-shot `migrate` service as `tp_owner`, before the app starts; the app refuses to serve while migrations are pending. Migrations must be backward-compatible with the previous app version.
- Backups: nightly `pg_dump` (including the catalog and admin tables) to the S3-compatible bucket, keeping 7 daily and 4 weekly copies. A restore test is run manually before launch. Catalog JSON exports are a convenience, not the backup.
- Connection budget: app pool 16 (2 kept open; `DB_MAX_CONNS` overrides) + the app's `LISTEN` connection 1 + `ingest` or `devseed` 4 + admin CLI 4 + backup 1 + `psql` sessions, about 30 in all, well under Postgres's default `max_connections` of 100. A `pool_max_conns` in `DATABASE_URL` wins over a binary's default. A k6 load test (`deploy/loadtest/`) checks the §1 latency targets before launch; results go in `docs/capacity-baseline.md`.
- Health: Docker and Caddy probe `/healthz`. `/readyz` is for monitoring and does not gate traffic.
- Logs: JSON `slog` to stdout. Public request lat/lng is logged only as `cell_id`.
- The app prunes admin audit (90 days), session and enrollment-token rows daily, starting at boot.

## 15. Privacy

- No public accounts, no public cookies.
- Public locations are not stored. Logs record `cell_id` (~28 km resolution) only.
- Admin login logs store IP addresses for security auditing, for 90 days.
- The About the data page states this.

## 16. Values chosen in this spec (confirm or change)

| Item | Value |
|---|---|
| Grid resolution | 0.25° |
| Frost-free threshold | mean tmin > 2 °C |
| Chill threshold | monthly mean < 7 °C |
| Winter-kill threshold (tender perennial_fruit) | coldest-month mean tmin < 0 °C |
| Seafood reach | 100 km from ecoregion |
| Launch catalog | ≥ 60 land ingredients, ≥ 15 seafood species |
| Cache size | 50,000 entries |
| Per-cell input cache (`/ingredient`) | 20,000 entries |
| DB pool size | server 16 max, 2 min (`DB_MAX_CONNS`); CLIs 4 |
| Public DB slots / gate wait | 12 (¾ of the pool) / 250 ms; shed with 503 + `Retry-After: 5` |
| Ocean query target | p95 ≤ 25 ms (else subdivide polygons at ingest) |
| Public request timeout | 2 s |
| Public rate limits | `/places` 10/s burst 20; `/season`, `/ingredient` 2/s burst 10 |
| Readiness ping reuse | 1 s |
| Calibration override threshold | score drop > 0.02 |
| Pending changeset expiry | 1 hour |
| Import size limit | 5 MB |
| Enrollment link TTL | 15 minutes |
| Admin session | 30 min idle, 12 h absolute; re-auth window 5 min |
| Login rate limit | 5 failures / 15 min / client (IPv4 or IPv6 /64); enrollment separate |
| Admin request limits | sign-in/enroll 1/s burst 20; other `/admin` 10/s burst 100 |
| Admin login log retention | 90 days (growth warning above 10,000 rows / 24 h) |
| Backup lockout | 5 failures → 15 min, doubling, max 24 h |
| Backup retention | 7 daily, 4 weekly |
