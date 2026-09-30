# Operations

**Run as root.** The secrets live in root-only env files under `/etc/thymeandplace/` (each mode `600`, in a mode-`700` directory), one per service (`env_file` in `deploy/compose.yaml`; see "Database roles"), and `docker compose` reads them, so run every command from a root shell (`sudo -i`, then `cd /opt/thymeandplace/deploy`) or prefix each with `sudo`. All `docker compose` commands below run from `/opt/thymeandplace/deploy`. The backup cron job and systemd units below already run as root.

## First deploy (one VPS)

1. Install Docker Engine + Compose v2 (≥ 2.24). Point `thymeand.place` and `www.thymeand.place` A/AAAA records at the VPS. (On Hetzner Cloud, `docs/hosting-hetzner.md` covers the server, firewall, DNS, host hardening and buckets.)
2. `git clone` the repo to `/opt/thymeandplace`.
3. Create the six env files from the templates in `deploy/env/`:
   ```bash
   sudo install -d -m 700 -o root -g root /etc/thymeandplace
   for f in db migrate app ingest backup restore; do
     sudo install -m 600 -o root -g root /opt/thymeandplace/deploy/env/$f.env.example /etc/thymeandplace/$f.env
   done
   openssl rand -base64 32   # → ADMIN_SECRET_KEY (app.env)
   openssl rand -hex 24      # run four times → POSTGRES_PASSWORD, TP_OWNER_PASSWORD, TP_APP_PASSWORD, TP_BACKUP_PASSWORD
   sudoedit /etc/thymeandplace/*.env
   ```
   Hex passwords, because base64's `/` and `+` would break the `DATABASE_URL`s. All four go in `db.env`; each also goes in the `DATABASE_URL` of the files that connect as that role: `POSTGRES_PASSWORD` → `restore.env`, `TP_OWNER_PASSWORD` → `migrate.env` and `ingest.env`, `TP_APP_PASSWORD` → `app.env`, `TP_BACKUP_PASSWORD` → `backup.env`. Put the dataset bucket's keys in `ingest.env` and the backup bucket's in `backup.env` and `restore.env`. Check with `sudo stat -c '%a %U %n' /etc/thymeandplace /etc/thymeandplace/*.env` → `700 root` for the directory and `600 root` for every file.
4. `cd /opt/thymeandplace/deploy && sudo docker compose up -d --build`. On the empty volume, `db` runs `deploy/db/initdb/20-roles.sh` once to create the roles; then the one-shot `migrate` service applies the migrations as `tp_owner` and exits, and `app` starts as `tp_app`. Caddy obtains certificates automatically.
5. Owner access: `docker compose exec app /app/admin enroll`. Open the link on your phone, register a passkey, repeat for a second device. Then `docker compose exec app /app/admin set-backup` and scan the QR code.
6. Reference data, per dataset (`places`, `ocean`, `climate`). Before approving any license, check its row in `docs/reference-data-licenses.md`. **Skip ocean for launch:** its MEOW terms are unresolved, so `approve-license` refuses it (see "Ocean dataset" below), and the site launches with seafood off ("Launching without seafood").
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
7. Import the launch catalog in `/admin/import` (see Plan 9; without a live ocean version, import the no-seafood file from "Launching without seafood") and check the preview's calibration score. After any later admin edit, export the catalog to `content/catalog/launch.json` and run `go test ./content/`: the calibration score can't see an ingredient in a city whose guide doesn't list it, and the content gates can (`content/AUTHORING.md`, "After editing in the admin").
8. Backups: see "Nightly backups" below for the cron entry.
9. **Before launch:** run `docker compose --profile tools run --rm backup` then `docker compose --profile tools run --rm --entrypoint restore-test.sh restore`. It must print `restore-test ok`.

### Pre-launch checklist

- [ ] `restore-test ok` (step 9).
- [ ] Seafood is off: `FEATURE_SEAFOOD` is unset or `off` in `app.env`, `docker compose logs app | grep 'seafood feature'` shows `enabled=false`, and `/about/data` doesn't mention seafood or marine data. (Only once the MEOW row in `docs/reference-data-licenses.md` is confirmed and an ocean version is live may it be `on`; see "Turning seafood on".)
- [ ] `SELECT id, license, license_reviewed, is_live, promoted_at FROM dataset_versions WHERE dataset = 'ocean'` shows no approved or promoted row whose license starts with `UNRESOLVED:`. If one exists, withdraw it ("Withdrawing a dataset").

### Ocean dataset: two sources, two licenses

`ingest ocean` combines two independent downloads (`internal/ingest/ocean.go`):

- **MEOW ecoregions** (`OCEAN_SOURCE_URL`, defaults to the Esri Oceans ArcGIS Online feature service, item `74b6ac5c8fc24dcb8abaad6428a5dfa4`) — Marine Ecoregions of the World (Spalding et al., 2007, *BioScience*). The ArcGIS item states no licence, and the terms have **not** been confirmed. `Ocean.License()` therefore starts with `UNRESOLVED:`, and until the resolution commit removes it, `ingest ocean approve-license` fails with "license terms not confirmed" and `validate`/`swap` report `license unresolved`, even if `license_reviewed` is set by hand. The procedure for confirming the terms is in `docs/reference-data-licenses.md`.
- **Natural Earth land mask** (optional `OCEAN_LAND_URL`, defaults to `ne_50m_land.geojson`, the 1:50m land polygon layer) — recorded as public domain (naturalearthdata.com). Used only to clip the MEOW polygons to water, since the raw MEOW polygons run well inland along coastlines.

Both licenses are printed together by `ingest ocean fetch`. Once the terms are resolved, both must be read before `approve-license --version N --yes`. A review carries over to later fetches with the same license text and source URLs, so it must only ever be given for confirmed terms.

### Launching without seafood

The site launches with seafood off, because the MEOW terms aren't confirmed (`docs/reference-data-licenses.md`, "Launch decision"). The switch is `FEATURE_SEAFOOD` in `app.env`: unset or `off` (the default) serves a land-only site, `on` serves seafood. Any other value stops the app at startup. With seafood off, the public site:

- makes no ocean lookups;
- leaves seafood items out of `/season`, answers `/ingredient/{slug}` for them with 404, and drops pairings that point at them;
- says estimates cover land areas only, and leaves the seafood paragraph and marine credits off `/about/data`.

The admin UI is unchanged: seafood items can still be edited there, and they stay hidden until the flag is on.

The catalog still needs one change: import checks every seafood region against the live ecoregions, so without ocean data `content/catalog/launch.json` is rejected. Import a derived file instead, which drops the seafood items and any pairing that points at one:

```bash
jq '([.ingredients[] | select(.kind == "seafood") | .slug]) as $sea
    | .ingredients |= map(select(.kind != "seafood") | .pairings -= $sea)' \
  content/catalog/launch.json > /tmp/launch-no-seafood.json
```

Keep the seafood items in `content/catalog/launch.json`; they're what gets imported when seafood comes back.

### Turning seafood on

1. Resolve MEOW ("Resolving MEOW" in `docs/reference-data-licenses.md`; that commit also updates the About page's marine credits).
2. Ingest ocean: `fetch`, read the license, `approve-license --version N --yes`, `all --version N`.
3. Import `content/catalog/launch.json` in `/admin/import`. Import only upserts, so this adds the seafood items and pairings back.
4. Set `FEATURE_SEAFOOD=on` in `/etc/thymeandplace/app.env` and run `sudo docker compose up -d app` (the flag is read at startup). The log should show `seafood feature enabled=true`.

To turn it off again, set it back to `off` and restart `app`; nothing else changes. If the terms are withdrawn, also follow "Withdrawing a dataset".

### Withdrawing a dataset

If an ocean version was approved or made live before its terms were confirmed (or a licensor withdraws permission), take it offline. From `/opt/thymeandplace/deploy`, run `docker compose exec db psql -U tp -d thymeandplace`:

```sql
BEGIN;
CREATE OR REPLACE VIEW ocean_ecoregion AS SELECT * FROM ocean_ecoregion_v0;
UPDATE dataset_versions SET is_live = false, license_reviewed = false, promoted_at = NULL
 WHERE dataset = 'ocean';
SELECT pg_notify('data_version_changed', 'dataset');
COMMIT;
```

This does what `ingest ... swap` does in reverse, and the site picks it up within seconds. Clearing `promoted_at` stops `ingest ocean rollback` from returning to a withdrawn version. Seafood items already in the catalog stay but never show, and admin edits to them fail the ecoregion check until ocean is back. If the terms also forbid keeping copies, delete `raw/ocean/` and `transformed/ocean/` from the bucket and drop each `ocean_ecoregion_v{N}` table; nightly dumps age out within the 28-day backup retention.

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

`git pull && docker compose up -d --build app`. Compose runs the `migrate` service first (as `tp_owner`) and starts the new app only once it has exited successfully; the app refuses to start while any migration is pending ("database has pending migrations; run the migrate service"). If that happens, `docker compose logs migrate` says why. Migrations must be backward-compatible with the previous version (add columns/tables; never drop or rename in the same release that stops using them).

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

- `docker compose --profile tools run --rm backup` — dumps `DATABASE_URL` (as `tp_backup`, which can read everything and write nothing) with `pg_dump --format=custom`, uploads it to `s3://$BUCKET_NAME/backups/daily/<date>.dump` via `mc`, additionally copies it to `backups/weekly/<iso-week>.dump` on Sundays, then prunes daily copies older than 7 days and weekly copies older than 28. It fails loudly (non-zero exit) if the dump comes out empty.

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
docker compose --profile tools run --rm --entrypoint restore-test.sh restore
```

Restores run in the `restore` service (the backup image, with `restore.env`), which connects as the bootstrap superuser: dropping and recreating the database and reinstalling PostGIS need it. The nightly `backup` job never gets that password.

`restore-test.sh` restores the newest daily dump into a scratch database (`thymeandplace_restore_test`) on the same Postgres server by calling `restore.sh` on it (the exact drop/create/single-transaction path a real restore takes, see "Restoring from backup"), compares row counts of `ingredient`, `ingredient_pairing`, `seafood_region`, `catalog_history`, `dataset_versions`, `admin_passkey`, `admin_backup`, `climate_cell`, `ocean_ecoregion`, and `place` against the live database, prints a table of `live=`/`restored=` counts, and drops the scratch database afterwards even if the comparison fails (it's under a `trap ... EXIT`). It exits non-zero — "fail loudly" — on any count mismatch or if no daily backup is found. Because a backup taken mid-day can legitimately trail the live `catalog_history` count if edits happened since, run it immediately after a fresh `backup` run rather than at an arbitrary time.

### Verifying locally before relying on it

The scripts were exercised end to end against a local S3-compatible container standing in for the production bucket, using the e2e stack seeded via `devseed` (which itself refuses to run without `DEVSEED_ALLOW=1` — dev/e2e only, never set in production).

`minio/minio` and `minio/mc` are both pull-access-denied on Docker Hub and Quay as of this writing (MinIO gated them behind their commercial subscription), so this recipe uses `zenko/cloudserver` — a freely-pullable S3-compatible server — as the bucket stand-in, and drives it with the `mc` already baked into the `backup` image itself (sha256-pinned in `deploy/backup/Dockerfile`) instead of pulling a second `minio/mc` image. The bucket container joins the e2e stack's network, so `backup`, `restore.sh` and `restore-test.sh` run through the same `docker compose ... run backup` commands as production, with the bucket settings passed as `-e` overrides. The commands below were run as written, in this order, in a Git Bash shell from the repo root:

```bash
cd deploy && export TP_ENV_DIR=./e2e MSYS_NO_PATHCONV=1
C="docker compose -p thymeandplace-e2e -f compose.yaml -f compose.e2e.yaml"   # never the production project
$C up -d --build --wait db app
$C --profile tools run --rm --entrypoint /app/devseed ingest

docker run -d --name tp-minio -e ENDPOINT=tp-minio -e REMOTE_MANAGEMENT_DISABLE=1 zenko/cloudserver:latest
docker network connect thymeandplace-e2e_default tp-minio
$C --profile tools build backup
$C --profile tools run --rm --entrypoint sh backup -c "mc alias set m http://tp-minio:8000 accessKey1 verySecretKey1 && mc mb m/tp-backups"

BK="-e BUCKET_ENDPOINT=tp-minio:8000 -e BUCKET_NAME=tp-backups -e BUCKET_ACCESS_KEY=accessKey1 -e BUCKET_SECRET_KEY=verySecretKey1 -e BUCKET_USE_SSL=false"
$C --profile tools run --rm $BK backup                                   # pg_dump as tp_backup
$C --profile tools run --rm $BK --entrypoint restore-test.sh restore     # as the superuser

# Full restore over the (e2e) live database, exactly as in "Restoring from backup":
$C stop app
$C --profile tools run --rm $BK -e RESTORE_CONFIRM=thymeandplace restore backups/daily/<date>.dump
$C start app

docker rm -f tp-minio; $C --profile tools down -v
```

`compose.e2e.yaml` sets the project name `thymeandplace-e2e`, so this stack's containers and volumes (`thymeandplace-e2e_*`) are separate from production's (`thymeandplace_*`) and the final `down -v` (like the ones in `deploy/smoke.sh` and `e2e/run.sh`) cannot touch production data, even on the VPS. Always pass both `-f compose.yaml -f compose.e2e.yaml` for test stacks.

Expected output: `backup ok: backups/daily/<date>.dump`, then a table of equal `live=`/`restored=` counts per table, then `restore-test ok (<date>.dump)`, then `restore ok: backups/daily/<date>.dump → thymeandplace`. When this was verified, a table with a PostGIS `geography` column (plus a view on it) created after the backup was gone after the restore, and every catalog count matched the counts at backup time. `accessKey1`/`verySecretKey1` are `zenko/cloudserver`'s built-in defaults, used only for this throwaway local container — never real credentials.

**Windows / Git Bash note:** Git Bash rewrites a leading `/` in arguments as a Windows path, which breaks `--entrypoint /app/devseed` and similar. `MSYS_NO_PATHCONV=1` (exported above) turns that off; it's a no-op on Linux.

## Restoring from backup

A restore **replaces the whole database** with the dump. `restore.sh <key> [target-url]` (the `restore` service's entrypoint; the target defaults to its own `DATABASE_URL` from `restore.env`, i.e. the live database, as the bootstrap superuser):

1. refuses unless `RESTORE_CONFIRM` is set to the exact database name in the target URL (the last path segment before any `?params`), before anything is downloaded or touched — this stops a stale or copy-pasted URL from overwriting the wrong database;
2. downloads the dump and checks it is non-empty and a readable `pg_dump` archive, so a missing, empty or corrupt backup exits non-zero with the database untouched;
3. connects to the `postgres` maintenance database on the same server and runs `DROP DATABASE <db> WITH (FORCE)` then `CREATE DATABASE <db>` (the name is quoted as an identifier);
4. runs `pg_restore --single-transaction --exit-on-error --no-owner` into the empty database. Nothing created after the backup survives (tables, views, PostGIS-dependent objects), and there is no object-by-object `--clean` that could fail halfway through the live data;
5. hands the database and its objects back to `tp_owner` (`deploy/backup/own-objects.sql`), since `--no-owner` leaves them owned by the superuser. The grants to `tp_app` come back with the dump.

Dumps live in the bucket at `backups/daily/<YYYY-MM-DD>.dump` (last 7 days) and `backups/weekly/<YYYY>-W<ww>.dump` (last 4 weeks). From `/opt/thymeandplace/deploy`:

```bash
docker compose stop app
docker compose --profile tools run --rm -e RESTORE_CONFIRM=thymeandplace restore backups/daily/<date>.dump
docker compose start app
```

`thymeandplace` is `POSTGRES_DB` from `/etc/thymeandplace/db.env`; if it refuses with "refusing to restore over ...", the name after `-e RESTORE_CONFIRM=` doesn't match the database name in `restore.env`'s `DATABASE_URL`. The app must be stopped first: `WITH (FORCE)` terminates its connections, and it must not write to the database mid-restore. After `start app`, check `curl -s https://thymeand.place/readyz` returns `ready`.

## Database roles

The app never connects as a superuser. Each service connects as the least-privileged role that can do its job, and gets only its own env file:

| Role | Attributes | Used by (env file) | Can |
|---|---|---|---|
| bootstrap (`POSTGRES_USER`, `tp`) | superuser | `db` init (`db.env`), `restore`/`restore-test` (`restore.env`), `psql` via `docker compose exec db` | everything |
| `tp_owner` | login only | `migrate` (`migrate.env`), `ingest` and `devseed` (`ingest.env`) | owns the database, the `public` schema and every object in it: migrations, reference-table swaps |
| `tp_app` | login only | `app` and the admin CLI via `docker compose exec app /app/admin` (`app.env`) | the grants in `migrations/00008_privileges.sql`: read and edit the catalog, admin tables and changesets; read reference data; append to `admin_login_log`. No DDL, no file or program access, no `UPDATE`/`DELETE` on `catalog_history` or `admin_login_log` |
| `tp_backup` | login, member of `pg_read_all_data` | `backup` (`backup.env`) | read every table, for `pg_dump` |

- **Audit tables are append-only for the app.** The catalog history and version triggers are `SECURITY DEFINER` functions owned by `tp_owner`, so the app writes history only through them. The only way it can delete login-log rows is `prune_admin_login_log(retention)`, which refuses any retention under 30 days.
- **New reference tables** that ingest creates as `tp_owner` are readable by `tp_app` with no extra grant (`ALTER DEFAULT PRIVILEGES`).
- **Secrets per container.** `db` gets the four database passwords and nothing else; `app` gets its own `DATABASE_URL` and the `ADMIN_*` settings, but no bucket keys and no other role's password; `backup` gets the backup role and bucket; `restore` gets the superuser URL. The CI `deploy` job checks this with `docker compose config`, and after a deploy `docker inspect --format '{{.Config.Env}}' $(docker compose ps -q app)` shows what the app actually received.
- **Rotating a role's password:** `docker compose exec db psql -U tp -d thymeandplace -c "ALTER ROLE tp_app PASSWORD '<new hex password>'"`, then update that role's password in `db.env` and in the one or two files that connect as it (see "First deploy", step 3), and recreate those services (`docker compose up -d app`, for example). For the bootstrap superuser, change `POSTGRES_PASSWORD` in `db.env` and `restore.env` the same way (the postgres image only reads it on an empty volume).
- **Check the grants** with `docker compose exec db psql -U tp -d thymeandplace -c '\dp admin_login_log'`: `tp_app` must have only `r` and `a` (read and append).

### Adopting an existing database

A database created before the roles existed (everything owned by the bootstrap superuser) is adopted once, before deploying the release that adds `migrations/00008_privileges.sql`. If there's no data worth keeping yet, the simpler path is a fresh volume: `docker compose down`, remove the `thymeandplace_dbdata` volume, write the six env files, then `docker compose up -d --build`.

1. Take a backup: `docker compose --profile tools run --rm backup` (with the old single env file still in place).
2. Write the six env files (see "First deploy", step 3), keeping the existing `POSTGRES_PASSWORD`, and recreate `db` with its new env file: `docker compose up -d db`. The data volume is kept, so `20-roles.sh` doesn't run.
3. Rehearse on a scratch copy first: restore the backup into a scratch database, then run step 4 against it with `-d thymeandplace_restore_test` in place of `-d "$POSTGRES_DB"`.
4. Create the roles and hand every object to `tp_owner`:
   ```bash
   cat db/adopt-roles.sql backup/own-objects.sql | docker compose exec -T db sh -c \
     'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -v owner_password="$TP_OWNER_PASSWORD" \
      -v app_password="$TP_APP_PASSWORD" -v backup_password="$TP_BACKUP_PASSWORD" -f -'
   ```
   It's idempotent. PostGIS's own objects stay with the superuser that installed the extension.
5. Deploy the release: `docker compose up -d --build`. `migrate` applies the privileges migration as `tp_owner`, then the app starts as `tp_app`.
6. Check: `docker compose exec app /app/admin revoke-sessions` prints `Ended N session(s)` (the CLI can write admin tables as `tp_app`), and `\dp admin_login_log` shows `tp_app` with only `r` and `a`.

**Rollback:** the previous release migrates at startup as whatever its `DATABASE_URL` says, and its compose file reads the single `app.env` for every service. Point `app.env`'s `DATABASE_URL` at `tp_owner` temporarily (never at the superuser), add `POSTGRES_USER`, `POSTGRES_PASSWORD` and `POSTGRES_DB` back to it for the `db` health check, and redeploy the old release. Leave the roles and grants in place: they're additive, and the old code works as `tp_owner`.

## Reverse proxy trust and logging

- The app trusts `X-Forwarded-*` headers (`TRUST_PROXY_HEADERS=true`, set in `deploy/compose.yaml`) because Caddy is always the first hop in front of it. **Never publish the app's own port** (`8080`) alongside Caddy's 80/443 — if a client could reach the app directly, it could forge those headers and spoof its source IP past the admin login-attempt rate limiter. `deploy/compose.yaml` intentionally has no `ports:` entry under `app`; only `caddy` publishes ports.
- **Security headers are set by the app, not Caddy.** `web.SecurityHeaders` (`internal/web/middleware.go`, applied in `web.Common`) puts a strict Content-Security-Policy (self only, no inline script or style), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: same-origin`, a `Permissions-Policy` that allows only geolocation for this origin, and `Cross-Origin-Opener-Policy`/`Cross-Origin-Resource-Policy: same-origin` on every response, including static files, 404s, 429s and panic pages. `/admin` replaces the CSP (it also allows `data:` images) and adds `Cache-Control: no-store`, `X-Robots-Tag: noindex` and `Referrer-Policy: no-referrer` (`internal/admin/admin.go`). Both HTMX configs turn off `allowEval` and `allowScriptTags` and set `selfRequestsOnly`. Caddy sets only HSTS and drops `Server`. Adding a third-party script, font, image host or embed means changing `SecurityHeaders` first. Check after a deploy with `curl -sI https://thymeand.place/ | grep -i -e content-security -e x-frame -e referrer -e permissions`, and look for CSP errors in the browser console.
- Caddy keeps **no access log** (there is no `log` directive in `deploy/Caddyfile` or `deploy/Caddyfile.local`). Request URIs carry public coordinates and search text, headers carry `Referer`/`Hx-Current-Url`, and every access-log line would record the client IP, while the spec (§14, §15) allows public requests to be logged only as `cell_id`, which the app already logs per request. The admin enrollment link's single-use `?token=` stays out of logs for the same reason. Caddy's own runtime and error log (certificate renewals, upstream health failures) still goes to `docker compose logs caddy`. Don't add an access log, or another logger in front of Caddy (a CDN, an nginx), without the same restriction.

## Test-only settings — never set in production

- `TP_PUBLIC_TODAY` pins the public site's "today" to a fixed calendar date (keeping the time of day). It exists for e2e scenarios that need a specific month (e.g. a season boundary crossing in May). The server refuses to start with it unless `ADMIN_RP_ID=localhost` (the e2e stack's value; `cmd/server/app.go`, `loadConfig`), and logs a `WARN` when it is accepted. If the production app exits at startup with `TP_PUBLIC_TODAY is test-only and requires ADMIN_RP_ID=localhost`, the env file has leaked a test setting; remove `TP_PUBLIC_TODAY` and start the app again. It is not present in `deploy/env/app.env.example`.
- `DEVSEED_ALLOW=1` is required for `devseed` (the seed-data command shipped in the `ingest` image) to run at all; without it, `devseed` refuses and exits 2. It's set in `deploy/e2e/ingest.env` for end-to-end tests and local dev only — never set it in `/etc/thymeandplace/ingest.env`. `devseed` resets the sample catalog, so running it against a real database would destroy real data.

## Admin access management

- `docker compose exec app /app/admin enroll` — prints a single-use link (`/admin/enroll?token=...`, valid 15 minutes) to register a new passkey. Use it once per device/owner; run it again for each additional device.
- `docker compose exec app /app/admin set-backup` — replaces the backup login (password + TOTP secret), printing a QR code for your authenticator app. This is the fallback login when no passkey is available; running it again discards the previous backup password/secret.
- `docker compose exec app /app/admin revoke-sessions` — ends every active admin session immediately (all devices logged out). Use it if a device with an active session is lost or compromised.
- `ADMIN_SECRET_KEY` is 32 random bytes, base64-encoded (`openssl rand -base64 32`), generated once at first deploy and stored in `/etc/thymeandplace/app.env`. It's the AES-256-GCM key that seals the backup TOTP secret at rest (`internal/auth/crypto.go`, `internal/auth/backup.go`) and seals in-flight WebAuthn ceremony state (the short-lived cookie used during passkey login/registration/enrollment).
  **Rotation consequences:** if you rotate `ADMIN_SECRET_KEY` (e.g. after a suspected compromise), every currently-sealed value becomes permanently unreadable with the new key — any in-progress passkey ceremony is invalidated (harmless; ceremonies are short-lived and the user just retries), but the existing backup-login TOTP secret becomes unreadable too. You must run `admin set-backup` again immediately after rotating the key, or backup login will be broken until you do. Passkeys themselves are unaffected (their credentials aren't sealed with this key).

### Sign-in limits

- **Per-client attempt budget** (in the database, so it survives restarts and is shared by replicas): 5 failed attempts per 15 minutes per client, across passkey sign-in, re-auth and backup login. A client is an IPv4 address or an IPv6 /64. Enrollment (`/admin/enroll/finish`) has its own budget of 5, so failed logins can't block enrolling a device. Over the budget the answer is 429 "Too many sign-in attempts", and the refusal is recorded once per client and budget per window, with one `WARN "admin sign-in budget reached"` naming the IP. If the budget check itself fails (database down), the answer is 503 and nothing is written.
- **Request limiter** (in memory, per process, before any database work): sign-in and enrollment routes 1 request/s with a burst of 20; every other `/admin` route 10/s, burst 100. Over it, 429 with `Retry-After: 1` and "Too many requests. Wait a few seconds and try again."; no audit row.
- If you lock yourself out, wait 15 minutes or sign in from another network. Passkey sign-in keeps working during a backup lockout, as before.

## Admin table pruning

The app prunes the admin tables itself, once at startup and then every 24 hours (`auth.Service.Prune`, started from `cmd/server`):

- `admin_login_log`: rows older than **90 days** are deleted.
- `admin_session`: sessions idle for more than 30 minutes or older than 12 hours (the app already refuses them).
- `admin_enrollment_token`: used or expired tokens.

Each run logs `INFO "admin prune"` with the three deleted counts; a failure logs `ERROR "admin prune failed"` and is retried a day later. If more than 10,000 login-log rows were written in the last 24 hours, it also logs `WARN "admin login log growing fast"`: the per-client budget bounds each client, but not a flood from many addresses, which belongs in an upstream firewall. After a deploy, `docker compose logs app | grep "admin prune"` shows the startup run.

If the app is down for a long time (or rolled back to a version without pruning), run the same deletes by hand against `DATABASE_URL`:

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

Wrap these in `docker compose exec db psql -U tp -d thymeandplace -c "..."`. Backups keep pruned rows for up to four more weeks (7 daily, 4 weekly copies).

## Capacity

**Connection budget.** Each binary sizes its own pool (`internal/db/db.go`): the server 16 connections (2 kept open), overridable with `DB_MAX_CONNS`; `admin`, `ingest` and `devseed` 4 each. The server's data-version listener holds one more, outside the pool. With a backup (`pg_dump`, 1) and a couple of `psql` sessions that's about 30, well under Postgres's default `max_connections` of 100, with room for a second app replica. A `pool_max_conns` or `pool_min_conns` parameter in `DATABASE_URL` wins over these defaults.

**Public DB gate.** Public requests that need the database (a `/season` or `/ingredient` cache miss, a `/places` search, a `/season?place=` label) share three quarters of the server pool: 12 of 16 slots. The rest stays free for `/admin`, the drift check, catalog reloads and Apply's second connection, so a public spike can't lock the owner out. A request that can't get a slot within 250 ms gets the retry panel with `503` and `Retry-After: 5` straight away instead of queueing until the 2 s deadline. Cache hits, `/healthz`, `/readyz`, static files and `/admin` never wait on the gate. `/ingredient` caches each cell's climate normals and ecoregions per data version (20,000 cells), so repeat clicks cost no queries.

**Changing the pool size.** Set `DB_MAX_CONNS` in the app env file and restart the app (`docker compose up -d app`); the gate follows at three quarters. On a VPS with less than 4 GB RAM use 10 (7 public slots). Re-run the load test afterwards.

**The `public DB gate full` log line.** When requests are shed, the app logs `INFO "public DB gate full"` at most once a minute, with `shed` (requests shed since the last line) and `slots`. Sustained 503s with this line mean the site is at capacity, not that the database is down: `/readyz` still says `ready`. Check `SELECT count(*), usename, state FROM pg_stat_activity GROUP BY 2, 3`; the app should hold between 2 and 17 connections.

**Load test.** `deploy/loadtest/` has a k6 script and a README. Run it on the production VPS before launch and after any change to `DB_MAX_CONNS`, the VPS size or the reference data, and record the results and the ocean query timings in `docs/capacity-baseline.md`.

## Health and logs

- `docker compose ps` shows health; `curl -s https://thymeand.place/readyz` returns `ready` when the DB is reachable and the catalog is loaded. The database part of `/readyz` is shared: one ping answers every request for 1 s, so after the database recovers, `/readyz` can say `database unavailable` for up to a second longer. Sustained 503s while `/readyz` is `ready` and the log shows `public DB gate full` mean load shedding (see "Capacity"), not an outage.
- Logs are JSON on stdout: `docker compose logs -f app`. Every service uses Docker's `json-file` driver with rotation (`max-size: 10m`, `max-file: 5`, set once in `deploy/compose.yaml`'s `x-logging` anchor), so each container keeps at most ~50 MB of logs; older lines are gone after rotation. Public requests record `cell_id`, never coordinates. Successful backup logins log at `WARN`, and so do calibration drops of more than 0.02.
- Docker Compose does not restart containers just for being unhealthy. The app exits on fatal errors, and `restart: unless-stopped` brings it back. If you want automatic restarts on failed `/healthz` checks, add a supervisor; it isn't included.
