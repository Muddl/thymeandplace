# Thyme & Place — Plan 8: Deployment, Backups, End-to-End Tests and CI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the system to one VPS with Docker Compose (Caddy + app + Postgres/PostGIS, plus on-demand ingest/transform/backup tools), with nightly backups and a tested restore, Playwright end-to-end tests for the five spec scenarios, and CI.

**Architecture:** Two Go images (app: `server` + `admin`, distroless; tools: `ingest` + `devseed`), the Python transform image, the official PostGIS image, and Caddy. The server refuses to listen until a catalog snapshot has loaded, and Caddy routes on `/healthz`, so a database outage shows the app's own retry panel instead of taking the site down. Caddy serves a maintenance page only when the app process is unreachable. End-to-end tests run the real stack with a Compose override (local TLS, synthetic Lisbon calibration fixture, seeded reference data) and drive Chromium's virtual WebAuthn authenticator.

**Tech Stack:** Docker Compose v2 (≥ 2.24), Caddy 2, `gcr.io/distroless/static-debian12`, Postgres 17 client tools + MinIO client `mc`, Playwright (Chromium), GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (§4.1, §13.9, §13.10, §14)

**Depends on:** Plans 1–7.

## Global Constraints

- Compose services: `caddy`, `app`, `db` always; `ingest`, `transform`, `backup` under profile `tools` (run with `docker compose --profile tools run --rm …`).
- Images: `app` is distroless and contains `/app/server` and `/app/admin`; `ingest` contains `/app/ingest` and `/app/devseed`; `transform` is Python + xarray; `postgis/postgis:17-3.5`; `caddy:2`.
- Secrets (`ADMIN_SECRET_KEY`, DB credentials, bucket credentials) come from an env file on the host with mode 600, default path `/etc/thymeandplace/app.env`, overridable with `TP_ENV_FILE`. Never commit real secrets. `deploy/e2e.env` holds throwaway test values only.
- Migrations run at app startup before listening, and must stay backward-compatible with the previous app version (§14).
- Backups: nightly `pg_dump` (custom format) to the S3-compatible bucket under `backups/daily/`, plus `backups/weekly/` on Sundays. Keep **7 daily and 4 weekly**. Run a restore test before launch.
- Health: Docker health check on `/healthz` (the `server -healthcheck` flag). **Spec conflict:** §14 says to gate traffic on `/readyz`, but `/readyz` includes a DB ping, so gating on it would pull the only replica out of rotation during a DB outage. The §11.6 retry panel and E2E scenario 3 could then never be seen. This plan gates Caddy on `/healthz` and enforces "not served until a snapshot is loaded" in the server itself: it does not listen until the first snapshot loads. `/readyz` stays exactly as specified, for monitoring.
- Logs: JSON `slog` to stdout; public requests log `cell_id`, never coordinates.
- CI: unit, scoring, auth, store integration and pipeline tests on every push; end-to-end on `main` (§13.10).
- Commit after every task. Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **First boot on an empty database** (no reference data, no catalog): the site comes up, the home page renders, and `/season` shows the "can't estimate here yet" panel. No crash or 500. Pinned in Task 1.
2. **Database stopped while the app runs**: visitors get the app's retry panel through Caddy, not a maintenance page or a blank 502, and "Try again" works once the DB is back. Pinned in Task 4 (E2E scenario 3).
3. **App container down or restarting**: Caddy serves the maintenance page ("Thyme & Place is updating…") instead of a bare 502. Pinned in Task 2.
4. **Start order**: the app starting before Postgres is ready exits and is restarted until it succeeds, and it never serves before the catalog snapshot loads. Pinned in Task 1.
5. **A backup that is empty or partial**: the restore test compares row counts of the key tables between the live DB and the restored copy, and fails loudly on any difference. Pinned in Task 5.

---

## File Structure

```
.dockerignore
deploy/Dockerfile.app
deploy/Dockerfile.ingest
deploy/compose.yaml
deploy/compose.e2e.yaml
deploy/Caddyfile
deploy/Caddyfile.local
deploy/maintenance.html
deploy/env.example
deploy/e2e.env
deploy/smoke.sh
deploy/backup/Dockerfile, backup.sh, restore.sh, restore-test.sh
internal/devdata/reference.go         # fixture data moved out of dbtest (no testing deps)
internal/dbtest/reference.go          # now a thin wrapper around devdata
cmd/devseed/main.go
cmd/server/main.go, app.go            # wait for snapshot before listening
e2e/package.json, playwright.config.ts, helpers.ts, run.sh
e2e/fixtures/lisbon.json
e2e/tests/public.spec.ts, admin.spec.ts
.github/workflows/ci.yml
docs/operations.md
```

---

### Task 1: Serve only after the catalog loads; container images

**Files:**
- Create: `.dockerignore`, `deploy/Dockerfile.app`, `deploy/Dockerfile.ingest`
- Modify: `cmd/server/app.go` (add `waitForSnapshot`), `cmd/server/main.go` (call it before listening), `cmd/server/app_test.go`

**Interfaces:**
- Consumes: `app.holder`, `catalog.Holder.Reload`.
- Produces: `func (a *app) waitForSnapshot(ctx context.Context, every time.Duration) error` (returns once a snapshot is loaded, or `ctx.Err()`)

- [ ] **Step 1: Write the failing tests**

Append to `cmd/server/app_test.go`:

```go
// Review Focus 1: an empty database still gives a working site.
func TestEmptyDatabaseServes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)
	a, err := newApp(ctx, config{DatabaseURL: pool.Config().ConnString()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	if err := a.waitForSnapshot(ctx, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.handler)
	defer srv.Close()
	if code, body := get(t, srv.URL+"/"); code != 200 || !strings.Contains(body, "Find your town") {
		t.Fatalf("home: %d", code)
	}
	code, body := get(t, srv.URL+"/season?lat=38.7&lng=-9.1")
	if code != 200 || !strings.Contains(body, "We can't estimate seasons here yet.") {
		t.Fatalf("season on empty DB: %d\n%s", code, body)
	}
}

// Review Focus 4: waitForSnapshot keeps retrying until a load succeeds.
func TestWaitForSnapshotRetries(t *testing.T) {
	calls := 0
	a := &app{holder: catalog.NewHolder(func(context.Context) (*catalog.Snapshot, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("db not ready")
		}
		return catalog.NewSnapshot(1, nil)
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.waitForSnapshot(ctx, 5*time.Millisecond); err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	never := &app{holder: catalog.NewHolder(func(context.Context) (*catalog.Snapshot, error) { return nil, errors.New("down") },
		slog.New(slog.NewTextHandler(io.Discard, nil)))}
	short, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	if err := never.waitForSnapshot(short, 5*time.Millisecond); err == nil {
		t.Fatal("must give up when the context ends")
	}
}
```

Add `"errors"` to the test imports.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/server/ -run 'EmptyDatabase|WaitForSnapshot'`
Expected: FAIL to compile: `a.waitForSnapshot undefined`.

- [ ] **Step 3: Implement**

Append to `cmd/server/app.go`:

```go
// waitForSnapshot blocks until a catalog snapshot is loaded, retrying every
// `every`. The server does not listen before this returns (spec §11.6: the
// site is not served without a snapshot).
func (a *app) waitForSnapshot(ctx context.Context, every time.Duration) error {
	for a.holder.Current() == nil {
		if err := a.holder.Reload(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
	return nil
}
```

In `start`, remove the retry goroutine (the loop above replaces it). In `cmd/server/main.go`, call it right after `a.start(ctx)`:

```go
	if err := a.waitForSnapshot(ctx, 5*time.Second); err != nil {
		log.Error("no catalog snapshot; not serving", "err", err)
		os.Exit(1)
	}
```

- [ ] **Step 4: Write the images**

Create `.dockerignore`:

```text
.git
**/node_modules
e2e/test-results
e2e/playwright-report
pipeline/.venv
**/__pycache__
.blob
.work
*.env
```

Create `deploy/Dockerfile.app`:

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# End-to-end builds swap in synthetic calibration fixtures.
ARG CALIBRATION_FIXTURES=
RUN if [ -n "$CALIBRATION_FIXTURES" ]; then rm -f calibration/fixtures/*.json && cp "$CALIBRATION_FIXTURES"/*.json calibration/fixtures/; fi
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/admin ./cmd/admin

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /out/admin /app/
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/app/server"]
```

Create `deploy/Dockerfile.ingest`:

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ingest ./cmd/ingest \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/devseed ./cmd/devseed

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ingest /out/devseed /app/
USER nonroot
ENTRYPOINT ["/app/ingest"]
```

`cmd/devseed` is created in Task 3. Until then, build only the app image.

- [ ] **Step 5: Run the tests and build the app image**

Run: `go test ./cmd/server/ -count=1 && docker build -f deploy/Dockerfile.app -t thymeandplace-app .`
Expected: `ok`; image builds. `docker run --rm thymeandplace-app -healthcheck http://127.0.0.1:1/healthz; echo $?` prints `1`, proving the flag works in the distroless image.

- [ ] **Step 6: Commit**

```bash
git add .dockerignore deploy/Dockerfile.app deploy/Dockerfile.ingest cmd/server
git commit -m "feat(deploy): distroless images; serve only after the first catalog snapshot

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Compose stack, Caddy and the maintenance page

**Files:**
- Create: `deploy/compose.yaml`, `deploy/Caddyfile`, `deploy/Caddyfile.local`, `deploy/maintenance.html`, `deploy/env.example`, `deploy/smoke.sh`

**Interfaces:**
- Produces: `docker compose -f deploy/compose.yaml` (production), and `deploy/smoke.sh`, which brings up a local-TLS stack and checks health, the maintenance page and shutdown.

- [ ] **Step 1: Write the Compose file**

Create `deploy/compose.yaml`:

```yaml
name: thymeandplace

x-env: &env
  env_file:
    - ${TP_ENV_FILE:-/etc/thymeandplace/app.env}

services:
  db:
    image: postgis/postgis:17-3.5
    restart: unless-stopped
    <<: *env
    volumes:
      - dbdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U $${POSTGRES_USER} -d $${POSTGRES_DB}"]
      interval: 5s
      retries: 20

  app:
    build:
      context: ..
      dockerfile: deploy/Dockerfile.app
    image: thymeandplace-app:latest
    restart: unless-stopped
    <<: *env
    environment:
      ADDR: ":8080"
      TRUST_PROXY_HEADERS: "true"
    depends_on:
      db:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "/app/server", "-healthcheck", "http://127.0.0.1:8080/healthz"]
      interval: 15s
      timeout: 5s
      retries: 3
      start_period: 60s

  caddy:
    image: caddy:2
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./maintenance.html:/srv/maintenance.html:ro
      - caddy_data:/data
      - caddy_config:/config
    depends_on:
      - app

  ingest:
    profiles: [tools]
    build:
      context: ..
      dockerfile: deploy/Dockerfile.ingest
    image: thymeandplace-ingest:latest
    <<: *env
    environment:
      INGEST_WORKDIR: /work
    volumes:
      - ingest_work:/work
    depends_on:
      db:
        condition: service_healthy

  transform:
    profiles: [tools]
    build:
      context: ../pipeline/transform
    image: thymeandplace-transform:latest
    volumes:
      - ingest_work:/work

  backup:
    profiles: [tools]
    build:
      context: ./backup
    image: thymeandplace-backup:latest
    <<: *env
    depends_on:
      db:
        condition: service_healthy

volumes:
  dbdata: {}
  caddy_data: {}
  caddy_config: {}
  ingest_work: {}
```

- [ ] **Step 2: Write the Caddy config, maintenance page and env example**

Create `deploy/Caddyfile`:

```caddyfile
www.thymeand.place {
	redir https://thymeand.place{uri} permanent
}

thymeand.place {
	encode zstd gzip
	header {
		Strict-Transport-Security "max-age=31536000; includeSubDomains"
		-Server
	}
	# Route on liveness. The app itself refuses to listen until its catalog
	# snapshot loads and shows its own retry panel during DB outages.
	reverse_proxy app:8080 {
		health_uri /healthz
		health_interval 10s
		health_timeout 2s
	}
	handle_errors 502 503 504 {
		root * /srv
		rewrite * /maintenance.html
		file_server
	}
}
```

Create `deploy/Caddyfile.local`:

```caddyfile
localhost {
	tls internal
	encode zstd gzip
	reverse_proxy app:8080 {
		health_uri /healthz
		health_interval 2s
		health_timeout 1s
	}
	handle_errors 502 503 504 {
		root * /srv
		rewrite * /maintenance.html
		file_server
	}
}
```

Create `deploy/maintenance.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Updating · Thyme &amp; Place</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;color:#1f2a1f}</style>
</head>
<body>
<h1>Thyme &amp; Place is updating</h1>
<p>We'll be back in a minute. Refresh the page to try again.</p>
</body>
</html>
```

Create `deploy/env.example`:

```bash
# Copy to /etc/thymeandplace/app.env and `chmod 600` it. Never commit the real file.
POSTGRES_USER=tp
POSTGRES_PASSWORD=change-me-long-random
POSTGRES_DB=thymeandplace
DATABASE_URL=postgres://tp:change-me-long-random@db:5432/thymeandplace?sslmode=disable

# openssl rand -base64 32
ADMIN_SECRET_KEY=
ADMIN_RP_ID=thymeand.place
ADMIN_ORIGIN=https://thymeand.place
PUBLIC_BASE_URL=https://thymeand.place
# ADMIN_BACKUP_LOGIN=off

# S3-compatible bucket for raw datasets and backups
BUCKET_ENDPOINT=
BUCKET_NAME=
BUCKET_ACCESS_KEY=
BUCKET_SECRET_KEY=
BUCKET_USE_SSL=true
```

Add `!deploy/env.example` and `!deploy/e2e.env` to `.gitignore` if the Plan 1 negation doesn't already cover them.

- [ ] **Step 3: Write the smoke script**

Create `deploy/smoke.sh`:

```bash
#!/usr/bin/env bash
# Brings up db+app+caddy with local TLS and checks health, the maintenance page and shutdown.
set -euo pipefail
cd "$(dirname "$0")"
export TP_ENV_FILE=./e2e.env
C="docker compose -f compose.yaml -f compose.e2e.yaml"
trap '$C down -v >/dev/null 2>&1 || true' EXIT

$C up -d --build --wait db app caddy
for i in $(seq 1 30); do
  curl -ksf https://localhost/healthz >/dev/null && break
  sleep 1
done
curl -ksf https://localhost/healthz | grep -q ok
curl -ksf https://localhost/readyz | grep -q ready
curl -ksf https://localhost/ | grep -q "Find your town"

# Review Focus 3: app down → maintenance page, not a bare 502.
$C stop app
sleep 3
body=$(curl -ks https://localhost/ || true)
echo "$body" | grep -q "Thyme &amp; Place is updating" || { echo "maintenance page missing"; echo "$body"; exit 1; }
$C start app
echo "smoke ok"
```

```bash
chmod +x deploy/smoke.sh
```

`compose.e2e.yaml` and `e2e.env` are created in Task 3. Run the smoke script at the end of Task 3.

- [ ] **Step 4: Validate the Compose file**

Run: `TP_ENV_FILE=./env.example docker compose -f deploy/compose.yaml --profile tools config >/dev/null && echo valid`
Expected: `valid`. (`TP_ENV_FILE` resolves relative to `deploy/`.)

- [ ] **Step 5: Commit**

```bash
git add deploy .gitignore
git commit -m "feat(deploy): compose stack, Caddy with liveness routing and maintenance page

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Dev seed data and the end-to-end Compose override

**Files:**
- Create: `internal/devdata/reference.go`, `cmd/devseed/main.go`, `deploy/compose.e2e.yaml`, `deploy/e2e.env`, `e2e/fixtures/lisbon.json`
- Modify: `internal/dbtest/reference.go` (delegate to `devdata`)

**Interfaces:**
- Consumes: the fixture data from Plan 3 Task 1, `catalogtest.SmallCatalog`, `catalog.Write`.
- Produces:
  - `devdata.Point`, points `devdata.Lisbon`, `Madrid`, `Tromso`, `Sydney`, `MidAtlantic`, `FallbackPoint`
  - `func devdata.SeedReference(ctx context.Context, db store.DBTX) error` (idempotent: `ON CONFLICT DO NOTHING`)
  - `dbtest.Point = devdata.Point` (type alias) and `dbtest.Lisbon = devdata.Lisbon` etc.; `dbtest.LoadReference` calls `devdata.SeedReference`
  - `devseed` binary: `DATABASE_URL` → seeds reference data and `SmallCatalog` (idempotent), exits 0

- [ ] **Step 1: Move the fixture data**

Create `internal/devdata/reference.go` by moving the `Point` type, the point variables, `cells`, `ecoregions`, `namedPlaces` and `placeFixture` from `internal/dbtest/reference.go` unchanged, then adding:

```go
// Package devdata holds the small reference fixture used by tests, local
// development and end-to-end runs.
package devdata

// (Point, points, cellFixture, cells, ecoregions, placeFixture, namedPlaces moved here verbatim.)

// SeedReference inserts the fixture into the *_v0 tables. Safe to re-run.
func SeedReference(ctx context.Context, db store.DBTX) error {
	for _, c := range cells {
		if _, err := db.Exec(ctx, `INSERT INTO climate_cell_v0 VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
			climate.CellID(c.at.Lat, c.at.Lng), c.tmin[:], c.tmax[:], c.precip[:]); err != nil {
			return fmt.Errorf("cell: %w", err)
		}
	}
	for _, e := range ecoregions {
		if _, err := db.Exec(ctx, `INSERT INTO ocean_ecoregion_v0 VALUES ($1,$2,ST_GeogFromText($3)) ON CONFLICT DO NOTHING`, e.id, e.name, e.wkt); err != nil {
			return fmt.Errorf("ecoregion: %w", err)
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
		if _, err := db.Exec(ctx, `INSERT INTO place_v0 VALUES ($1,$2,$3,$4,$5,$6, ST_SetSRID(ST_MakePoint($8,$7),4326)::geography) ON CONFLICT DO NOTHING`,
			p.id, p.name, p.ascii, admin1, p.country, p.pop, p.at.Lat, p.at.Lng); err != nil {
			return fmt.Errorf("place %s: %w", p.name, err)
		}
	}
	return nil
}
```

(imports: `context`, `fmt`, `thymeandplace/internal/climate`, `thymeandplace/internal/store`.)

Replace `internal/dbtest/reference.go` with:

```go
package dbtest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/devdata"
)

type Point = devdata.Point

var (
	Lisbon        = devdata.Lisbon
	Madrid        = devdata.Madrid
	Tromso        = devdata.Tromso
	Sydney        = devdata.Sydney
	MidAtlantic   = devdata.MidAtlantic
	FallbackPoint = devdata.FallbackPoint
)

// LoadReference inserts the small reference fixture behind the live views.
func LoadReference(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	if err := devdata.SeedReference(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
}
```

Run: `go test ./... -count=1`
Expected: every package still `ok` (pure refactor).

- [ ] **Step 2: Write devseed**

Create `cmd/devseed/main.go`:

```go
// Command devseed loads the small reference fixture and the test catalog
// into DATABASE_URL. For local development and end-to-end tests only.
package main

import (
	"context"
	"fmt"
	"os"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/db"
	"thymeandplace/internal/devdata"
	"thymeandplace/internal/store"
)

func main() {
	if err := run(context.Background(), os.Getenv("DATABASE_URL")); err != nil {
		fmt.Fprintln(os.Stderr, "devseed:", err)
		os.Exit(1)
	}
	fmt.Println("devseed ok")
}

func run(ctx context.Context, url string) error {
	if err := db.Migrate(ctx, url); err != nil {
		return err
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := devdata.SeedReference(ctx, pool); err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor', 'devseed', true)`); err != nil {
		return err
	}
	if err := catalog.Write(ctx, store.New(tx), catalogtest.SmallCatalog(), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

- [ ] **Step 3: Write the e2e override, env and fixture**

Create `deploy/compose.e2e.yaml`:

```yaml
services:
  app:
    build:
      args:
        CALIBRATION_FIXTURES: e2e/fixtures
  caddy:
    volumes:
      - ./Caddyfile.local:/etc/caddy/Caddyfile:ro
      - ./maintenance.html:/srv/maintenance.html:ro
      - caddy_data:/data
      - caddy_config:/config
```

Create `deploy/e2e.env` (throwaway values, safe to commit):

```bash
# END-TO-END TESTS ONLY. Not secret, never used in production.
POSTGRES_USER=tp
POSTGRES_PASSWORD=tp-e2e-only
POSTGRES_DB=thymeandplace
DATABASE_URL=postgres://tp:tp-e2e-only@db:5432/thymeandplace?sslmode=disable
ADMIN_SECRET_KEY=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
ADMIN_RP_ID=localhost
ADMIN_ORIGIN=https://localhost
PUBLIC_BASE_URL=https://localhost
ADMIN_BACKUP_LOGIN=off
BLOB_DIR=/work/blob
```

Create `e2e/fixtures/lisbon.json`:

```json
{
  "id": "e2e-lisbon",
  "name": "Lisbon (e2e)",
  "lat": 38.7167,
  "lng": -9.1333,
  "source": "Synthetic fixture for end-to-end tests only; not from a published guide",
  "months": { "tomato": [7, 8, 9, 10] }
}
```

With the devdata Lisbon normals, tomato (16–30 °C, 3 months) grows Apr–Oct and is harvested Jul–Oct, so the score is 1.000. Setting `months_to_harvest` to 1 gives May–Oct (4/6 = 0.667). The admin E2E test relies on both numbers.

- [ ] **Step 4: Build and smoke-test the full stack**

```bash
docker build -f deploy/Dockerfile.ingest -t thymeandplace-ingest .
./deploy/smoke.sh
```

Expected: `smoke ok`.

Then seed and look:

```bash
cd deploy && export TP_ENV_FILE=./e2e.env
docker compose -f compose.yaml -f compose.e2e.yaml up -d --build --wait db app caddy
docker compose -f compose.yaml -f compose.e2e.yaml --profile tools run --rm --entrypoint /app/devseed ingest
curl -ks "https://localhost/season?lat=38.7167&lng=-9.1333&date=2026-10-15" | grep -o "Mackerel"
docker compose -f compose.yaml -f compose.e2e.yaml down -v
```

Expected: `devseed ok`, then `Mackerel`.

- [ ] **Step 5: Commit**

```bash
git add internal/devdata internal/dbtest cmd/devseed deploy e2e/fixtures
git commit -m "feat(e2e): devdata seed, devseed command, local-TLS compose override

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Playwright end-to-end suite

**Files:**
- Create: `e2e/package.json`, `e2e/playwright.config.ts`, `e2e/helpers.ts`, `e2e/tests/public.spec.ts`, `e2e/tests/admin.spec.ts`, `e2e/run.sh`

**Interfaces:**
- Consumes: the running e2e stack (Task 3).
- Produces: `e2e/run.sh` (build, up, seed, test, down; exit code = test result)

- [ ] **Step 1: Scaffold**

Create `e2e/package.json`:

```json
{
  "name": "thymeandplace-e2e",
  "private": true,
  "scripts": { "test": "playwright test" },
  "devDependencies": {
    "@playwright/test": "^1.55.0",
    "@types/node": "^22.0.0",
    "typescript": "^5.6.0"
  }
}
```

Create `e2e/playwright.config.ts`:

```ts
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1, // tests stop the database and edit the shared catalog
  timeout: 60_000,
  retries: 0,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: "https://localhost",
    ignoreHTTPSErrors: true, // Caddy's internal CA
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }], // CDP WebAuthn is Chromium-only
});
```

Create `e2e/helpers.ts`:

```ts
import { execSync } from "node:child_process";
import path from "node:path";
import { APIRequestContext, Page, expect } from "@playwright/test";

const deploy = path.resolve(__dirname, "..", "deploy");

export function compose(args: string): string {
  return execSync(`docker compose -f compose.yaml -f compose.e2e.yaml ${args}`, {
    cwd: deploy,
    env: { ...process.env, TP_ENV_FILE: "./e2e.env" },
    stdio: ["ignore", "pipe", "pipe"],
  }).toString();
}

export async function waitReady(request: APIRequestContext) {
  for (let i = 0; i < 90; i++) {
    try {
      const r = await request.get("/readyz");
      if (r.ok()) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error("stack never became ready");
}

// enrollAndSignIn registers a virtual passkey via a fresh enrollment link and leaves the page signed in.
export async function enrollAndSignIn(page: Page) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: {
      protocol: "ctap2", transport: "internal", hasResidentKey: true,
      hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true,
    },
  });
  const out = compose("exec -T app /app/admin enroll");
  const link = out.match(/https:\/\/localhost\/admin\/enroll\?token=\S+/)?.[0];
  if (!link) throw new Error("no enrollment link in:\n" + out);
  await page.goto(link);
  await page.getByLabel("Name for this passkey").fill("E2E key");
  await page.getByRole("button", { name: "Register passkey" }).click();
  await expect(page).toHaveURL(/\/admin$/);
}
```

Create `e2e/run.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
export TP_ENV_FILE=./e2e.env
C="docker compose -f ../deploy/compose.yaml -f ../deploy/compose.e2e.yaml"
trap '$C down -v >/dev/null 2>&1 || true' EXIT
$C build
$C up -d --wait db app caddy
$C --profile tools run --rm --entrypoint /app/devseed ingest
npm ci
npx playwright install --with-deps chromium
npx playwright test
```

```bash
chmod +x e2e/run.sh
cd e2e && npm install && cd ..
```

(`npm install` creates `package-lock.json`; commit it so `npm ci` works in CI.)

- [ ] **Step 2: Write the public scenarios (spec §13.9 items 1–3)**

Create `e2e/tests/public.spec.ts`:

```ts
import { test, expect } from "@playwright/test";
import { compose, waitReady } from "../helpers";

test("1. search → select → results render", async ({ page }) => {
  await page.goto("/");
  // Type key by key: the search input triggers on keyup, which fill() doesn't fire.
  await page.getByLabel("Find your town").pressSequentially("lisb", { delay: 50 });
  await page.getByRole("link", { name: "Lisbon, PT" }).click();
  const results = page.locator("#results");
  await expect(results).toContainText("Lisbon, PT");
  await expect(results.locator("h3").first()).toBeVisible();
  await expect(page).toHaveURL(/\/season\?lat=38\.7167&lng=-9\.1333&place=1/);
});

// Spec §1 success criterion 1: warm-cache /season within about 1 s.
test("warm /season responds within 1 s", async ({ request }) => {
  const url = "/season?lat=38.7167&lng=-9.1333&date=2026-06-15";
  expect((await request.get(url)).ok()).toBeTruthy(); // warms the cache
  const start = Date.now();
  const res = await request.get(url);
  expect(res.ok()).toBeTruthy();
  expect(Date.now() - start).toBeLessThan(1000);
});

test("2. geolocation denied → search fallback", async ({ browser }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, permissions: [] });
  const page = await context.newPage();
  await page.goto("/");
  await page.getByRole("button", { name: "Use my location" }).click();
  await expect(page.getByText("Location access is off, so search for your town instead.")).toBeVisible();
  await expect(page.getByLabel("Find your town")).toBeFocused();
  await context.close();
});

// Review Focus 2: DB outage shows the app's retry panel through Caddy.
test("3. Postgres stopped → retry panel → started → Try again succeeds", async ({ page, request }) => {
  await page.goto("/");
  compose("stop db");
  try {
    // A location and month not yet cached.
    await page.goto("/season?lat=40.4165&lng=-3.7026&date=2026-03-10");
    await expect(page.getByText("Couldn't load seasonal picks right now. Try again in a moment.")).toBeVisible();
    await expect(page.getByText("40.42°N, 3.70°W")).toBeVisible(); // location header stays
  } finally {
    compose("start db");
  }
  await waitReady(request);
  await page.getByRole("link", { name: "Try again" }).click();
  await expect(page.locator("#results")).toContainText("Madrid");
  await expect(page.getByText("Couldn't load seasonal picks")).toHaveCount(0);
});
```

- [ ] **Step 3: Write the admin scenarios (spec §13.9 items 4–5)**

Create `e2e/tests/admin.spec.ts`:

```ts
import { test, expect } from "@playwright/test";
import { promises as fs } from "node:fs";
import { enrollAndSignIn } from "../helpers";

const maySeason = "/season?lat=38.7167&lng=-9.1333&place=1&date=2026-05-15";

test("4. passkey login → edit → preview → apply → public page reflects change", async ({ page }) => {
  await page.goto(maySeason);
  await expect(page.locator("#results")).not.toContainText("Tomato"); // May: not yet in season

  await enrollAndSignIn(page);
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.getByRole("button", { name: "Sign in with a passkey" }).click();
  await expect(page).toHaveURL(/\/admin$/);

  await page.getByRole("link", { name: "Tomato" }).click();
  await page.getByLabel("Months from planting to harvest").fill("1");
  await page.getByRole("button", { name: "Preview changes" }).click();

  await expect(page.getByRole("heading", { name: "Calibration score" })).toBeVisible();
  await expect(page.getByText("Before 1.000 → after 0.667 (-0.333).")).toBeVisible();
  await expect(page.locator(".bars").first()).toBeVisible();
  await page.getByLabel("Apply despite lower calibration score").check();
  await page.getByRole("button", { name: "Apply" }).click();
  await expect(page.getByText("Changes applied.")).toBeVisible();

  await expect(async () => {
    await page.goto(maySeason);
    await expect(page.locator("#results")).toContainText("Tomato", { timeout: 1000 });
  }).toPass({ timeout: 10_000 }); // live reload via NOTIFY, no restart
});

const chard = {
  slug: "chard", name: "Chard", category: "vegetable", kind: "cultivated_year_round",
  storage_months: 0, tips: { choose: "Glossy leaves.", store: "Fridge, wrapped." }, pairings: ["kale"], note: "",
};

test("5. import → preview → apply → export matches", async ({ page }) => {
  await enrollAndSignIn(page);
  await page.goto("/admin/import");
  await page.setInputFiles('input[name="file"]', {
    name: "chard.json", mimeType: "application/json",
    buffer: Buffer.from(JSON.stringify({ format: "thymeandplace-catalog/v1", ingredients: [chard] })),
  });
  await page.getByRole("button", { name: "Preview import" }).click();
  await expect(page.getByText("1 added item")).toBeVisible();
  await page.getByRole("button", { name: "Apply" }).click();
  await expect(page.getByText("Changes applied.")).toBeVisible();

  const [download] = await Promise.all([page.waitForEvent("download"), page.getByRole("link", { name: "Export" }).click()]);
  const doc = JSON.parse(await fs.readFile((await download.path())!, "utf8"));
  expect(doc.format).toBe("thymeandplace-catalog/v1");
  expect(doc.ingredients.find((i: { slug: string }) => i.slug === "chard")).toEqual(chard);
});
```

- [ ] **Step 4: Run the suite**

Run: `./e2e/run.sh`
Expected: `6 passed` (five spec scenarios plus the warm-latency check). On failure, open `e2e/playwright-report/index.html` and the retained traces.

- [ ] **Step 5: Commit**

```bash
git add e2e
git commit -m "test(e2e): Playwright scenarios for search, geolocation, DB outage, admin edit and import

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Backups, restore test and operations runbook

**Files:**
- Create: `deploy/backup/Dockerfile`, `deploy/backup/backup.sh`, `deploy/backup/restore.sh`, `deploy/backup/restore-test.sh`, `docs/operations.md`

**Interfaces:**
- Produces:
  - `docker compose --profile tools run --rm backup`: nightly dump and retention
  - `docker compose --profile tools run --rm --entrypoint restore.sh backup <key> <database-url>`
  - `docker compose --profile tools run --rm --entrypoint restore-test.sh backup`: restores the newest daily dump into a scratch database and compares row counts

- [ ] **Step 1: Write the backup image and scripts**

Create `deploy/backup/Dockerfile`:

```dockerfile
FROM postgres:17-bookworm
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* \
 && curl -fsSL -o /usr/local/bin/mc https://dl.min.io/client/mc/release/linux-amd64/mc \
 && chmod +x /usr/local/bin/mc
COPY backup.sh restore.sh restore-test.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/*.sh
ENTRYPOINT ["/usr/local/bin/backup.sh"]
```

Create `deploy/backup/backup.sh`:

```sh
#!/bin/sh
# Nightly pg_dump to the bucket. Keeps 7 daily and 4 weekly copies.
set -eu
: "${DATABASE_URL:?}" "${BUCKET_ENDPOINT:?}" "${BUCKET_NAME:?}" "${BUCKET_ACCESS_KEY:?}" "${BUCKET_SECRET_KEY:?}"
scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null

day=$(date -u +%Y-%m-%d)
file="/tmp/thymeandplace-$day.dump"
pg_dump --format=custom --no-owner --dbname="$DATABASE_URL" --file="$file"
[ -s "$file" ] || { echo "backup: dump is empty" >&2; exit 1; }

mc cp --quiet "$file" "store/$BUCKET_NAME/backups/daily/$day.dump"
if [ "$(date -u +%u)" = "7" ]; then
  mc cp --quiet "$file" "store/$BUCKET_NAME/backups/weekly/$(date -u +%G-W%V).dump"
fi
mc rm --recursive --force --older-than 7d "store/$BUCKET_NAME/backups/daily/" >/dev/null 2>&1 || true
mc rm --recursive --force --older-than 28d "store/$BUCKET_NAME/backups/weekly/" >/dev/null 2>&1 || true
rm -f "$file"
echo "backup ok: backups/daily/$day.dump"
```

Create `deploy/backup/restore.sh`:

```sh
#!/bin/sh
# usage: restore.sh <object key, e.g. backups/daily/2026-10-01.dump> <target DATABASE_URL>
set -eu
[ $# -eq 2 ] || { echo "usage: restore.sh <key> <target-database-url>" >&2; exit 2; }
: "${BUCKET_ENDPOINT:?}" "${BUCKET_NAME:?}" "${BUCKET_ACCESS_KEY:?}" "${BUCKET_SECRET_KEY:?}"
scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null
mc cp --quiet "store/$BUCKET_NAME/$1" /tmp/restore.dump
pg_restore --no-owner --clean --if-exists --exit-on-error --dbname="$2" /tmp/restore.dump
rm -f /tmp/restore.dump
echo "restore ok: $1 → target database"
```

Create `deploy/backup/restore-test.sh`:

```sh
#!/bin/sh
# Restores the newest daily dump into a scratch database next to the live one
# and compares row counts of the tables that matter. Drops the scratch DB after.
set -eu
: "${DATABASE_URL:?}" "${BUCKET_ENDPOINT:?}" "${BUCKET_NAME:?}" "${BUCKET_ACCESS_KEY:?}" "${BUCKET_SECRET_KEY:?}"
scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null

latest=$(mc ls "store/$BUCKET_NAME/backups/daily/" | awk '{print $NF}' | sort | tail -n 1)
[ -n "$latest" ] || { echo "restore-test: no daily backups found" >&2; exit 1; }
scratch_db=thymeandplace_restore_test
scratch_url=$(echo "$DATABASE_URL" | sed -E "s#/[^/?]+(\?|$)#/$scratch_db\1#")

psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -qc "DROP DATABASE IF EXISTS $scratch_db WITH (FORCE)" -c "CREATE DATABASE $scratch_db"
trap 'psql "$DATABASE_URL" -qc "DROP DATABASE IF EXISTS '"$scratch_db"' WITH (FORCE)" >/dev/null 2>&1 || true' EXIT
restore.sh "backups/daily/$latest" "$scratch_url"

status=0
for t in ingredient ingredient_pairing seafood_region catalog_history dataset_versions admin_passkey admin_backup climate_cell ocean_ecoregion place; do
  live=$(psql "$DATABASE_URL" -Atqc "SELECT count(*) FROM $t")
  copy=$(psql "$scratch_url" -Atqc "SELECT count(*) FROM $t")
  printf '%-20s live=%-10s restored=%s\n' "$t" "$live" "$copy"
  [ "$live" = "$copy" ] || status=1
done
[ $status -eq 0 ] && echo "restore-test ok ($latest)" || echo "restore-test FAILED: counts differ ($latest)" >&2
exit $status
```

A backup taken during the day can legitimately trail the live `catalog_history` count if edits happened since. Run the restore test right after a fresh `backup` run.

- [ ] **Step 2: Verify against a local MinIO**

```bash
docker network create tp-backup-test
docker run -d --rm --name tp-minio --network tp-backup-test -e MINIO_ROOT_USER=minio -e MINIO_ROOT_PASSWORD=minio12345 minio/minio server /data
cd deploy && export TP_ENV_FILE=./e2e.env
C="docker compose -f compose.yaml -f compose.e2e.yaml"
$C up -d --build --wait db app
$C --profile tools run --rm --entrypoint /app/devseed ingest
docker network connect tp-backup-test "$($C ps -q db)"
docker run --rm --network tp-backup-test --entrypoint sh minio/mc -c "mc alias set m http://tp-minio:9000 minio minio12345 && mc mb m/tp-backups"
BK="-e BUCKET_ENDPOINT=tp-minio:9000 -e BUCKET_NAME=tp-backups -e BUCKET_ACCESS_KEY=minio -e BUCKET_SECRET_KEY=minio12345 -e BUCKET_USE_SSL=false"
$C --profile tools build backup
docker run --rm --network tp-backup-test --env-file e2e.env $BK -e DATABASE_URL="postgres://tp:tp-e2e-only@$($C ps -q db | cut -c1-12):5432/thymeandplace?sslmode=disable" thymeandplace-backup:latest
docker run --rm --network tp-backup-test --env-file e2e.env $BK -e DATABASE_URL="postgres://tp:tp-e2e-only@$($C ps -q db | cut -c1-12):5432/thymeandplace?sslmode=disable" --entrypoint restore-test.sh thymeandplace-backup:latest
$C down -v; docker rm -f tp-minio; docker network rm tp-backup-test
```

Expected: `backup ok: …`, then a table of equal counts and `restore-test ok`. (The container ID prefix works as a hostname on the shared network.)

- [ ] **Step 3: Write the operations runbook**

Create `docs/operations.md`:

````markdown
# Operations

## First deploy (one VPS)

1. Install Docker Engine + Compose v2 (≥ 2.24). Point `thymeand.place` and `www.thymeand.place` A/AAAA records at the VPS.
2. `git clone` the repo to `/opt/thymeandplace`.
3. Create the env file: `sudo install -m 600 -o root deploy/env.example /etc/thymeandplace/app.env`, then fill in real values (`openssl rand -base64 32` for `ADMIN_SECRET_KEY`; long random DB password in both `POSTGRES_PASSWORD` and `DATABASE_URL`). Check with `stat -c %a /etc/thymeandplace/app.env` → `600`.
4. `cd /opt/thymeandplace/deploy && docker compose up -d --build`. Caddy obtains certificates automatically.
5. Owner access: `docker compose exec app /app/admin enroll`. Open the link on your phone, register a passkey, repeat for a second device. Then `docker compose exec app /app/admin set-backup` and scan the QR code.
6. Reference data, per dataset (`places`, `ocean`, `climate`):
   ```bash
   docker compose --profile tools run --rm ingest places fetch        # note the version N and read the license it prints
   docker compose --profile tools run --rm ingest places approve-license --version N --yes
   docker compose --profile tools run --rm ingest places all --version N
   ```
   Climate needs the transform container once per version:
   ```bash
   docker compose --profile tools run --rm ingest climate fetch
   docker compose --profile tools run --rm ingest climate transform --version N   # exits 3 and prints the next command
   docker compose --profile tools run --rm transform /work/climate/vN
   docker compose --profile tools run --rm ingest climate all --version N
   ```
7. Import the launch catalog in `/admin/import` (see Plan 9) and check the preview's calibration score.
8. Backups: add to root's crontab:
   `15 3 * * * cd /opt/thymeandplace/deploy && docker compose --profile tools run --rm backup >> /var/log/thymeandplace-backup.log 2>&1`
9. **Before launch:** run `docker compose --profile tools run --rm backup` then `docker compose --profile tools run --rm --entrypoint restore-test.sh backup`. It must print `restore-test ok`.

## Updating the app

`git pull && docker compose up -d --build app`. Migrations run at startup and must be backward-compatible with the previous version (add columns/tables; never drop or rename in the same release that stops using them).

## Rolling back reference data

`docker compose --profile tools run --rm ingest <dataset> rollback`. It takes one transaction and the site picks it up via `NOTIFY` within seconds.

Old versions are kept. You may drop tables older than the last two versions of a dataset by hand, for example: `docker compose exec db psql -U tp -d thymeandplace -c "SELECT id, table_name, is_live FROM dataset_versions WHERE dataset = 'climate' ORDER BY id"`, then `DROP TABLE climate_cell_v<old id>;` for versions that are neither live nor one of the two most recent. (Rollback refuses to return to a dropped version.)

## Restoring from backup

1. `docker compose stop app`
2. `docker compose --profile tools run --rm --entrypoint restore.sh backup backups/daily/<date>.dump "$DATABASE_URL"`
3. `docker compose start app`

## Health and logs

- `docker compose ps` shows health; `curl -s https://thymeand.place/readyz` returns `ready` when the DB is reachable and the catalog is loaded.
- Logs are JSON on stdout: `docker compose logs -f app`. Public requests record `cell_id`, never coordinates. Successful backup logins log at `WARN`, and so do calibration drops of more than 0.02.
- Docker Compose does not restart containers just for being unhealthy. The app exits on fatal errors, and `restart: unless-stopped` brings it back. If you want automatic restarts on failed `/healthz` checks, add a supervisor; it isn't included.
````

- [ ] **Step 4: Commit**

```bash
git add deploy/backup docs/operations.md
git commit -m "feat(ops): nightly backups with retention, restore and restore-test, runbook

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Continuous integration

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Produces: jobs `go` and `python` on every push and PR; `e2e` on `main` after both pass.

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/ci.yml`:

```yaml
name: ci
on:
  push:
    branches: ["**"]
  pull_request:

jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Generated code is current
        run: |
          go tool templ generate
          ./scripts/sqlc.sh generate
          git diff --exit-code
      - run: go vet ./...
      - name: Unit, scoring, auth, store integration and ingestion tests
        run: go test ./... -count=1

  python:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.12"
      - run: pip install -r pipeline/transform/requirements-dev.txt
      - run: python -m pytest pipeline/tests -q

  e2e:
    if: github.ref == 'refs/heads/main'
    needs: [go, python]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: "22"
      - run: ./e2e/run.sh
      - if: failure()
        uses: actions/upload-artifact@v4
        with:
          name: playwright-report
          path: e2e/playwright-report
```

- [ ] **Step 2: Validate locally**

Run: `docker run --rm -v "$(pwd -W 2>/dev/null || pwd):/repo" -w /repo rhysd/actionlint:latest`
Expected: no output (the workflow is valid). In Git Bash, prefix with `MSYS_NO_PATHCONV=1`.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: go/python tests on every push, end-to-end on main

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Plan completion check

- [ ] `./deploy/smoke.sh` prints `smoke ok`.
- [ ] `./e2e/run.sh` passes all five scenarios and the warm-latency check.
- [ ] The Task 5 MinIO procedure prints `restore-test ok`.
- [ ] `go test ./... -count=1` and `python -m pytest pipeline/tests` pass.
