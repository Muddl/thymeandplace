# Operations

**Run as root.** The secrets file `/etc/thymeandplace/app.env` is root-only (mode `600` in a mode-`700` directory), and every `docker compose` command in this document reads it (`env_file` in `deploy/compose.yaml`), so run them from a root shell (`sudo -i`, then `cd /opt/thymeandplace/deploy`) or prefix each with `sudo`. All `docker compose` commands below run from `/opt/thymeandplace/deploy`. The backup cron job and systemd units below already run as root.

## First deploy (one VPS)

1. Install Docker Engine + Compose v2 (≥ 2.24). Point `thymeand.place` and `www.thymeand.place` A/AAAA records at the VPS.
2. `git clone` the repo to `/opt/thymeandplace`.
3. Create the env file:
   ```bash
   sudo install -d -m 700 -o root -g root /etc/thymeandplace
   sudo install -m 600 -o root -g root /opt/thymeandplace/deploy/env.example /etc/thymeandplace/app.env
   openssl rand -base64 32   # → ADMIN_SECRET_KEY
   openssl rand -hex 24      # → DB password (hex: base64's / and + would break DATABASE_URL)
   sudoedit /etc/thymeandplace/app.env
   ```
   Put the DB password in both `POSTGRES_PASSWORD` and `DATABASE_URL`. Check with `sudo stat -c '%a %U' /etc/thymeandplace /etc/thymeandplace/app.env` → `700 root` and `600 root`.
4. `cd /opt/thymeandplace/deploy && sudo docker compose up -d --build`. Caddy obtains certificates automatically.
5. Owner access: `docker compose exec app /app/admin enroll`. Open the link on your phone, register a passkey, repeat for a second device. Then `docker compose exec app /app/admin set-backup` and scan the QR code.
6. Reference data, per dataset (`places`, `ocean`, `climate`):
   ```bash
   docker compose --profile tools run --rm ingest places fetch        # note the version N and read the license it prints
   docker compose --profile tools run --rm ingest places approve-license --version N --yes
   docker compose --profile tools run --rm ingest places all --version N
   ```
   Climate needs the transform container once per version (all from `/opt/thymeandplace/deploy`):
   ```bash
   docker compose --profile tools run --rm ingest climate fetch
   docker compose --profile tools run --rm ingest climate transform --version N   # exits 3 and prints the next command
   docker compose --profile tools run --rm transform /work/climate/vN
   docker compose --profile tools run --rm ingest climate all --version N
   ```
7. Import the launch catalog in `/admin/import` (see Plan 9) and check the preview's calibration score.
8. Backups: see "Nightly backups" below for the cron entry.
9. **Before launch:** run `docker compose --profile tools run --rm backup` then `docker compose --profile tools run --rm --entrypoint restore-test.sh backup`. It must print `restore-test ok`.

### Ocean dataset: two sources, two licenses

`ingest ocean` combines two independent downloads (`internal/ingest/ocean.go`):

- **MEOW ecoregions** (`OCEAN_SOURCE_URL`, defaults to the Esri Oceans ArcGIS Online feature service, item `74b6ac5c8fc24dcb8abaad6428a5dfa4`) — Marine Ecoregions of the World (Spalding et al., 2007, *BioScience*). The ArcGIS item states no explicit licence; confirm terms with the original publishers (The Nature Conservancy / WWF) before running `approve-license`.
- **Natural Earth land mask** (optional `OCEAN_LAND_URL`, defaults to `ne_50m_land.geojson`, the 1:50m land polygon layer) — public domain (naturalearthdata.com). Used only to clip the MEOW polygons to water, since the raw MEOW polygons run well inland along coastlines.

Both licenses are printed together by `ingest ocean fetch` and must both be read before `approve-license --version N --yes`.

### First TerraClimate ingest (deferred to first production run)

`ingest climate` downloads three full-globe NetCDF climatologies (tmin, tmax, precipitation; TerraClimate 1991–2020, Abatzoglou et al. 2018, CC0) from `CLIMATE_SOURCE_BASE` (default: NorthWest Knowledge Network's THREDDS server) and runs them through the Python `transform` container to grid-average them per cell. This is a large download (each `.nc` file is on the order of a gigabyte) and a compute-heavy transform (minutes, not seconds) — it was intentionally not run in CI or e2e, only against small fixtures. The first real run happens here, on the VPS, as part of step 6 above:

```bash
docker compose --profile tools run --rm ingest climate fetch
docker compose --profile tools run --rm ingest climate transform --version N
docker compose --profile tools run --rm transform /work/climate/vN
docker compose --profile tools run --rm ingest climate all --version N
```

What to check:
- `fetch` prints the license (CC0, citation requested) and the version number `N` — note it.
- `transform` (step run through the `ingest` binary) exits `3` and prints the exact `docker compose --profile tools run --rm transform /work/climate/vN` command to run next; run that literally from `/opt/thymeandplace/deploy` (the `deploy/` directory, next to `compose.yaml`), where every `docker compose` command in this document runs.
- The transform container's own log should finish without error and leave `out/climate.csv` under the shared `ingest_work` volume.
- `ingest climate all --version N` runs load/validate/swap; validate compares interpolated values at twelve fixed reference cities (London, Moscow, Cairo, Singapore, …) against known climatology figures within a 4°C tolerance (`internal/ingest/climate.go`). If validate fails, do not approve the version — check the download completed and the transform didn't silently truncate.
- Watch disk and memory on the VPS during this step; the raw NetCDF files plus the transform's intermediate arrays are the heaviest thing this system ever does in one step.

## Updating the app

`git pull && docker compose up -d --build app`. Migrations run at startup and must be backward-compatible with the previous version (add columns/tables; never drop or rename in the same release that stops using them).

### The shared `ingest_work` volume and file ownership

`ingest` (distroless `nonroot`) and `transform` both run as uid/gid `65532` and share the `ingest_work` volume at `/work`. Both images ship a `65532`-owned `/work`, and Docker seeds a new (or empty) named volume from the image directory it mounts over, ownership included, so a fresh volume is writable by both with no manual step.

A volume that already holds files from before this was fixed (created root-owned, or with root-owned files left by an older `transform` image that ran as root) makes `ingest ... fetch` fail with `mkdir /work/<dataset>/incoming: permission denied`. Fix it once, from `/opt/thymeandplace/deploy`:

```bash
docker compose --profile tools build ingest transform
docker compose --profile tools run --rm --user 0 --entrypoint chown transform -R 65532:65532 /work
```

## Rolling back reference data

`docker compose --profile tools run --rm ingest <dataset> rollback`. It takes one transaction and the site picks it up via `NOTIFY` within seconds.

Old versions are kept. You may drop tables older than the last two versions of a dataset by hand, for example: `docker compose exec db psql -U tp -d thymeandplace -c "SELECT id, table_name, is_live FROM dataset_versions WHERE dataset = 'climate' ORDER BY id"`, then `DROP TABLE climate_cell_v<old id>;` for versions that are neither live nor one of the two most recent. (Rollback refuses to return to a dropped version.)

## Nightly backups

`deploy/backup` builds a small `postgres:17-bookworm` image with `pg_dump`/`pg_restore`/`psql` and a pinned MinIO client (`mc`) added on top, entrypoint `backup.sh`. Nightly:

- `docker compose --profile tools run --rm backup` — dumps `DATABASE_URL` with `pg_dump --format=custom`, uploads it to `s3://$BUCKET_NAME/backups/daily/<date>.dump` via `mc`, additionally copies it to `backups/weekly/<iso-week>.dump` on Sundays, then prunes daily copies older than 7 days and weekly copies older than 28. It fails loudly (non-zero exit) if the dump comes out empty.

Add to root's crontab on the VPS (`crontab -e`):

```
15 3 * * * cd /opt/thymeandplace/deploy && docker compose --profile tools run --rm backup >> /var/log/thymeandplace-backup.log 2>&1
```

Or, with systemd, a timer + oneshot service:

```ini
# /etc/systemd/system/thymeandplace-backup.service
[Unit]
Description=thymeandplace nightly backup
[Service]
Type=oneshot
WorkingDirectory=/opt/thymeandplace/deploy
ExecStart=/usr/bin/docker compose --profile tools run --rm backup
```

```ini
# /etc/systemd/system/thymeandplace-backup.timer
[Unit]
Description=Run thymeandplace-backup nightly
[Timer]
OnCalendar=*-*-* 03:15:00
Persistent=true
[Install]
WantedBy=timers.target
```

Enable with `systemctl enable --now thymeandplace-backup.timer`.

Periodically (e.g. monthly, right after a successful backup) verify restores still work:

```bash
docker compose --profile tools run --rm --entrypoint restore-test.sh backup
```

`restore-test.sh` restores the newest daily dump into a scratch database (`thymeandplace_restore_test`) on the same Postgres server by calling `restore.sh` on it (the exact drop/create/single-transaction path a real restore takes, see "Restoring from backup"), compares row counts of `ingredient`, `ingredient_pairing`, `seafood_region`, `catalog_history`, `dataset_versions`, `admin_passkey`, `admin_backup`, `climate_cell`, `ocean_ecoregion`, and `place` against the live database, prints a table of `live=`/`restored=` counts, and drops the scratch database afterwards even if the comparison fails (it's under a `trap ... EXIT`). It exits non-zero — "fail loudly" — on any count mismatch or if no daily backup is found. Because a backup taken mid-day can legitimately trail the live `catalog_history` count if edits happened since, run it immediately after a fresh `backup` run rather than at an arbitrary time.

### Verifying locally before relying on it

The scripts were exercised end to end against a local S3-compatible container standing in for the production bucket, using the e2e stack seeded via `devseed` (which itself refuses to run without `DEVSEED_ALLOW=1` — dev/e2e only, never set in production).

`minio/minio` and `minio/mc` are both pull-access-denied on Docker Hub and Quay as of this writing (MinIO gated them behind their commercial subscription), so this recipe uses `zenko/cloudserver` — a freely-pullable S3-compatible server — as the bucket stand-in, and drives it with the `mc` already baked into the `backup` image itself (sha256-pinned in `deploy/backup/Dockerfile`) instead of pulling a second `minio/mc` image. The bucket container joins the e2e stack's network, so `backup`, `restore.sh` and `restore-test.sh` run through the same `docker compose ... run backup` commands as production, with the bucket settings passed as `-e` overrides. The commands below were run as written, in this order, in a Git Bash shell from the repo root:

```bash
cd deploy && export TP_ENV_FILE=./e2e.env MSYS_NO_PATHCONV=1
C="docker compose -f compose.yaml -f compose.e2e.yaml"   # project thymeandplace-e2e
$C up -d --build --wait db app
$C --profile tools run --rm --entrypoint /app/devseed ingest

docker run -d --name tp-minio -e ENDPOINT=tp-minio -e REMOTE_MANAGEMENT_DISABLE=1 zenko/cloudserver:latest
docker network connect thymeandplace-e2e_default tp-minio
$C --profile tools build backup
$C --profile tools run --rm --entrypoint sh backup -c   "mc alias set m http://tp-minio:8000 accessKey1 verySecretKey1 && mc mb m/tp-backups"

BK="-e BUCKET_ENDPOINT=tp-minio:8000 -e BUCKET_NAME=tp-backups -e BUCKET_ACCESS_KEY=accessKey1 -e BUCKET_SECRET_KEY=verySecretKey1 -e BUCKET_USE_SSL=false"
$C --profile tools run --rm $BK backup
$C --profile tools run --rm $BK --entrypoint restore-test.sh backup

# Full restore over the (e2e) live database, exactly as in "Restoring from backup":
$C stop app
$C --profile tools run --rm $BK -e RESTORE_CONFIRM=thymeandplace --entrypoint restore.sh backup backups/daily/<date>.dump
$C start app

$C --profile tools down -v; docker rm -f tp-minio
```

`compose.e2e.yaml` sets the project name `thymeandplace-e2e`, so this stack's containers and volumes (`thymeandplace-e2e_*`) are separate from production's (`thymeandplace_*`) and the final `down -v` (like the ones in `deploy/smoke.sh` and `e2e/run.sh`) cannot touch production data, even on the VPS. Always pass both `-f compose.yaml -f compose.e2e.yaml` for test stacks.

Expected output: `backup ok: backups/daily/<date>.dump`, then a table of equal `live=`/`restored=` counts per table, then `restore-test ok (<date>.dump)`, then `restore ok: backups/daily/<date>.dump → thymeandplace`. When this was verified, a table with a PostGIS `geography` column (plus a view on it) created after the backup was gone after the restore, and every catalog count matched the counts at backup time. `accessKey1`/`verySecretKey1` are `zenko/cloudserver`'s built-in defaults, used only for this throwaway local container — never real credentials.

**Windows / Git Bash note:** Git Bash rewrites a leading `/` in arguments as a Windows path, which breaks `--entrypoint /app/devseed` and similar. `MSYS_NO_PATHCONV=1` (exported above) turns that off; it's a no-op on Linux.

## Restoring from backup

A restore **replaces the whole database** with the dump. `restore.sh <key> [target-url]` (the target defaults to the `backup` container's own `DATABASE_URL`, i.e. the live database from `app.env`):

1. refuses unless `RESTORE_CONFIRM` is set to the exact database name in the target URL (the last path segment before any `?params`), before anything is downloaded or touched — this stops a stale or copy-pasted URL from overwriting the wrong database;
2. downloads the dump and checks it is non-empty and a readable `pg_dump` archive, so a missing, empty or corrupt backup exits non-zero with the database untouched;
3. connects to the `postgres` maintenance database on the same server and runs `DROP DATABASE <db> WITH (FORCE)` then `CREATE DATABASE <db>` (the name is quoted as an identifier);
4. runs `pg_restore --single-transaction --exit-on-error --no-owner` into the empty database. Nothing created after the backup survives (tables, views, PostGIS-dependent objects), and there is no object-by-object `--clean` that could fail halfway through the live data.

Dumps live in the bucket at `backups/daily/<YYYY-MM-DD>.dump` (last 7 days) and `backups/weekly/<YYYY>-W<ww>.dump` (last 4 weeks). From `/opt/thymeandplace/deploy`:

```bash
docker compose stop app
docker compose --profile tools run --rm -e RESTORE_CONFIRM=thymeandplace --entrypoint restore.sh backup backups/daily/<date>.dump
docker compose start app
```

`thymeandplace` is `POSTGRES_DB` from `/etc/thymeandplace/app.env`; if it refuses with "refusing to restore over ...", the name after `-e RESTORE_CONFIRM=` doesn't match the database name in that file's `DATABASE_URL`. The app must be stopped first: `WITH (FORCE)` terminates its connections, and it must not write to the database mid-restore. After `start app`, check `curl -s https://thymeand.place/readyz` returns `ready`.

## Reverse proxy trust and logging

- The app trusts `X-Forwarded-*` headers (`TRUST_PROXY_HEADERS=true`, set in `deploy/compose.yaml`) because Caddy is always the first hop in front of it. **Never publish the app's own port** (`8080`) alongside Caddy's 80/443 — if a client could reach the app directly, it could forge those headers and spoof its source IP past the admin login-attempt rate limiter. `deploy/compose.yaml` intentionally has no `ports:` entry under `app`; only `caddy` publishes ports.
- Caddy keeps **no access log** (there is no `log` directive in `deploy/Caddyfile` or `deploy/Caddyfile.local`). Request URIs carry public coordinates and search text, headers carry `Referer`/`Hx-Current-Url`, and every access-log line would record the client IP, while the spec (§14, §15) allows public requests to be logged only as `cell_id`, which the app already logs per request. The admin enrollment link's single-use `?token=` stays out of logs for the same reason. Caddy's own runtime and error log (certificate renewals, upstream health failures) still goes to `docker compose logs caddy`. Don't add an access log, or another logger in front of Caddy (a CDN, an nginx), without the same restriction.

## Test-only settings — never set in production

- `TP_PUBLIC_TODAY` pins the public site's "today" to a fixed calendar date (keeping the time of day). It exists for e2e scenarios that need a specific month (e.g. a season boundary crossing in May). The server logs a `WARN` at startup if it's set (`cmd/server/app.go`, `loadConfig`) — if you ever see that warning on the production instance, the env file has leaked a test setting; remove `TP_PUBLIC_TODAY` and restart. It is not present in `deploy/env.example`.
- `DEVSEED_ALLOW=1` is required for `devseed` (the seed-data command shipped in the `ingest` image) to run at all; without it, `devseed` refuses and exits 2. It's set in `deploy/e2e.env` for end-to-end tests and local dev only — never set it in `/etc/thymeandplace/app.env`. `devseed` resets the sample catalog, so running it against a real database would destroy real data.

## Admin access management

- `docker compose exec app /app/admin enroll` — prints a single-use link (`/admin/enroll?token=...`, valid 15 minutes) to register a new passkey. Use it once per device/owner; run it again for each additional device.
- `docker compose exec app /app/admin set-backup` — replaces the backup login (password + TOTP secret), printing a QR code for your authenticator app. This is the fallback login when no passkey is available; running it again discards the previous backup password/secret.
- `docker compose exec app /app/admin revoke-sessions` — ends every active admin session immediately (all devices logged out). Use it if a device with an active session is lost or compromised.
- `ADMIN_SECRET_KEY` is 32 random bytes, base64-encoded (`openssl rand -base64 32`), generated once at first deploy and stored in `/etc/thymeandplace/app.env`. It's the AES-256-GCM key that seals the backup TOTP secret at rest (`internal/auth/crypto.go`, `internal/auth/backup.go`) and seals in-flight WebAuthn ceremony state (the short-lived cookie used during passkey login/registration/enrollment).
  **Rotation consequences:** if you rotate `ADMIN_SECRET_KEY` (e.g. after a suspected compromise), every currently-sealed value becomes permanently unreadable with the new key — any in-progress passkey ceremony is invalidated (harmless; ceremonies are short-lived and the user just retries), but the existing backup-login TOTP secret becomes unreadable too. You must run `admin set-backup` again immediately after rotating the key, or backup login will be broken until you do. Passkeys themselves are unaffected (their credentials aren't sealed with this key).

## Periodic prune of admin audit/session tables

Nothing in the app prunes these automatically; run this periodically (e.g. weekly, alongside the backup cron, or by hand) against `DATABASE_URL`:

```sql
-- admin_login_log: keep 90 days of sign-in history.
DELETE FROM admin_login_log WHERE at < now() - interval '90 days';

-- admin_session: the app already treats sessions idle >30m or older than
-- 12h as expired (internal/auth/session.go); this just reclaims the rows.
DELETE FROM admin_session
  WHERE last_seen_at < now() - interval '30 minutes'
     OR created_at    < now() - interval '12 hours';

-- admin_enrollment_token: drop used tokens and any that expired unused.
DELETE FROM admin_enrollment_token WHERE used_at IS NOT NULL OR expires_at < now();
```

Wrap these in `docker compose exec db psql -U tp -d thymeandplace -c "..."`, or add them to a small monthly cron job next to the backup one.

## Health and logs

- `docker compose ps` shows health; `curl -s https://thymeand.place/readyz` returns `ready` when the DB is reachable and the catalog is loaded.
- Logs are JSON on stdout: `docker compose logs -f app`. Every service uses Docker's `json-file` driver with rotation (`max-size: 10m`, `max-file: 5`, set once in `deploy/compose.yaml`'s `x-logging` anchor), so each container keeps at most ~50 MB of logs; older lines are gone after rotation. Public requests record `cell_id`, never coordinates. Successful backup logins log at `WARN`, and so do calibration drops of more than 0.02.
- Docker Compose does not restart containers just for being unhealthy. The app exits on fatal errors, and `restart: unless-stopped` brings it back. If you want automatic restarts on failed `/healthz` checks, add a supervisor; it isn't included.
