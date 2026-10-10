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

   Then the two backup encryption keys (details in "Backup encryption"). On your own machine, not the VPS, run `age-keygen -o recovery.key` and store the file in your password manager and on paper. On the VPS, from a root shell in `/opt/thymeandplace/deploy`, build the backup image, then create the restore-test key. Build first, as its own command: a build that `run` starts by itself prints its progress to standard output, which would end up in the key file.
   ```bash
   docker compose --profile tools build backup
   (umask 077; docker compose --profile tools run --rm --no-deps -T --entrypoint age-keygen backup > /etc/thymeandplace/restore-test.agekey)
   grep -c '^AGE-SECRET-KEY-1' /etc/thymeandplace/restore-test.agekey             # must print 1
   grep -vc -e '^#' -e '^AGE-SECRET-KEY-1' /etc/thymeandplace/restore-test.agekey  # must print 0
   ```
   If either check prints anything else, delete the file and run the two commands again. Put both public keys (`age1…`, space-separated) in `BACKUP_AGE_RECIPIENTS` in `backup.env`. `age-keygen` prints each one, and `grep 'public key' /etc/thymeandplace/restore-test.agekey` shows the restore-test one again. Without the recipients `backup` refuses to start, and without the key file `restore` does.
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
8. Backups: see "Nightly backups" below for the timer or cron entries.
9. **Set up monitoring:** create the checks, put their ping URLs in the env files and configure the external monitor ("Monitoring and alerts" below, "Setting up").
10. **Before launch:** run `docker compose --profile tools run --rm backup` then `docker compose --profile tools run --rm --entrypoint restore-test.sh restore`. It must print `restore-test ok`, and the `backup` and `restore-test` checks should go green. Compose won't start `restore` until `/etc/thymeandplace/restore-test.agekey` exists (step 3).

### Pre-launch checklist

- [ ] `restore-test ok` (step 10).
- [ ] The recovery key is in your password manager and on paper, and not on the VPS ("Backup encryption").
- [ ] Every check in "Monitoring and alerts" is green, and a test notification reached you.
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

This does what `ingest ... swap` does in reverse, and the site picks it up within seconds. Clearing `promoted_at` stops `ingest ocean rollback` from returning to a withdrawn version. Seafood items already in the catalog stay but never show, and admin edits to them fail the ecoregion check until ocean is back. If the terms also forbid keeping copies, delete `raw/ocean/` and `transformed/ocean/` from the bucket and drop each `ocean_ecoregion_v{N}` table; nightly dumps age out within the 28-day backup retention, and the bucket's deleted versions 35 days after that ("Backup deletion protection").

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

`deploy/backup` builds a small `postgres:17-bookworm` image with `pg_dump`/`pg_restore`/`psql`, Debian's `age` and a pinned MinIO client (`mc`) added on top, entrypoint `backup.sh`. Nightly:

- `docker compose --profile tools run --rm backup` — dumps `DATABASE_URL` (as `tp_backup`, which can read everything and write nothing) with `pg_dump --format=custom` to a temporary file, checks it is non-empty and that `pg_restore --list` reads it, encrypts it with `age` to every key in `BACKUP_AGE_RECIPIENTS` ("Backup encryption"), deletes the plaintext, and uploads the ciphertext to `s3://$BUCKET_NAME/backups/daily/<date>.dump.age` via `mc`. On Sundays it also copies it to `backups/weekly/<iso-week>.dump.age`. Then it prunes daily copies older than 7 days and weekly copies older than 28, unless `BACKUP_PRUNE=lifecycle` leaves that to bucket lifecycle rules ("Backup deletion protection"). It fails loudly (non-zero exit) and uploads nothing if the dump comes out empty or unreadable, and it exits 2 before dumping anything if `BACKUP_AGE_RECIPIENTS` is empty or lists anything but `age1…` public keys.

With `HEARTBEAT_BACKUP_URL` set in `backup.env`, each run pings the `backup` check: `/start`, then success (`backup ok: backups/daily/<date>.dump.age`), or `/fail` naming the step that failed (for example `backup failed during dump (exit 1)`; the steps are `env`, `alias`, `dump`, `verify`, `encrypt`, `upload`, `weekly` and `prune`). A prune failure keeps the exit status at 0 but still fails the check (`dump uploaded; pruning failed: backups/daily`, naming each prefix that failed); `mc`'s error message is only in the run's log, since it can carry the object store's IP. A run that never starts (Docker down, image missing) sends nothing, and the missed run alerts. See "Monitoring and alerts".

Schedule it with the systemd timer below (preferred: its output goes to the journal, which rotates itself) or with cron. For cron, add to root's crontab on the VPS (`crontab -e`):

```
15 3 * * * cd /opt/thymeandplace/deploy && docker compose --profile tools run --rm backup >> /var/log/thymeandplace-backup.log 2>&1
15 4 1 * * cd /opt/thymeandplace/deploy && docker compose --profile tools run --rm --entrypoint restore-test.sh restore >> /var/log/thymeandplace-backup.log 2>&1
```

The second line is the monthly restore test (below). Nothing else rotates that log file, so with cron also add:

```
# /etc/logrotate.d/thymeandplace-backup
/var/log/thymeandplace-backup.log {
    weekly
    rotate 8
    compress
    missingok
    notifempty
}
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

Enable with `systemctl enable --now thymeandplace-backup.timer`, and read its output with `journalctl -u thymeandplace-backup`.

On the 1st of each month, an hour after that night's backup, the restore test checks that the newest dump still restores (the `15 4 1 * *` cron line above). With systemd, copy the two units as `thymeandplace-restore-test.service` and `.timer`, with `ExecStart=/usr/bin/docker compose --profile tools run --rm --entrypoint restore-test.sh restore` and `OnCalendar=*-*-01 04:15:00`. To run it by hand:

```bash
docker compose --profile tools run --rm --entrypoint restore-test.sh restore
```

With `HEARTBEAT_RESTORE_TEST_URL` set in `restore.env`, it pings the `restore-test` check: `/start`, then success, or `/fail` with the count table. It restores into a scratch database on the production Postgres, so the server needs disk for one more copy of the database for a few minutes.

Restores run in the `restore` service (the backup image, with `restore.env`), which connects as the bootstrap superuser: dropping and recreating the database and reinstalling PostGIS need it. The nightly `backup` job never gets that password. The restore-test key (`/etc/thymeandplace/restore-test.agekey`) is mounted into `restore` only, and `AGE_IDENTITY` points at it there, so `restore` decrypts `.dump.age` backups without further settings.

`restore-test.sh` restores the newest daily dump (`.dump.age`, or a plaintext `.dump` from before encryption) into a scratch database (`thymeandplace_restore_test`) on the same Postgres server by calling `restore.sh` on it (the exact drop/create/single-transaction path a real restore takes, see "Restoring from backup"), compares row counts of `ingredient`, `ingredient_pairing`, `seafood_region`, `catalog_history`, `dataset_versions`, `admin_passkey`, `admin_backup`, `climate_cell`, `ocean_ecoregion`, and `place` against the live database, prints a table of `live=`/`restored=` counts, and drops the scratch database afterwards even if the comparison fails (it's under a `trap ... EXIT`). It exits non-zero — "fail loudly" — on any count mismatch or if no daily backup is found. Because a backup taken mid-day can legitimately trail the live `catalog_history` count if edits happened since, run it immediately after a fresh `backup` run rather than at an arbitrary time.

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
$C --profile tools run --rm $BK -e RESTORE_CONFIRM=thymeandplace restore backups/daily/<date>.dump.age
$C start app

docker rm -f tp-minio; $C --profile tools down -v
```

`compose.e2e.yaml` sets the project name `thymeandplace-e2e`, so this stack's containers and volumes (`thymeandplace-e2e_*`) are separate from production's (`thymeandplace_*`) and the final `down -v` (like the ones in `deploy/smoke.sh` and `e2e/run.sh`) cannot touch production data, even on the VPS. Always pass both `-f compose.yaml -f compose.e2e.yaml` for test stacks.

As in production, the dumps are encrypted to two recipients, both test-only, listed in `deploy/e2e/backup.env` with extra spaces and a tab between them. `restore` decrypts with the restore-test identity, `deploy/e2e/restore-test.agekey`, mounted the same way as production's key; `deploy/e2e/recovery-test.agekey` stands in for the recovery key. Both key pairs are committed, so they protect nothing: never use them outside the e2e stack.

Expected output: `backup ok: backups/daily/<date>.dump.age`, then a table of equal `live=`/`restored=` counts per table, then `restore-test ok (<date>.dump.age)`, then `restore ok: backups/daily/<date>.dump.age → thymeandplace`. When this was verified, a table with a PostGIS `geography` column (plus a view on it) created after the backup was gone after the restore, and every catalog count matched the counts at backup time. `accessKey1`/`verySecretKey1` are `zenko/cloudserver`'s built-in defaults, used only for this throwaway local container — never real credentials.

`./deploy/backup/verify.sh` runs this recipe with every check (encrypted upload to both recipients, restore-test, a disaster restore over the live database with the stand-in recovery key in exactly the `-v …:/run/secrets/recovery.agekey:ro -e AGE_IDENTITY=…` form of "Disaster restore with the recovery key", refusals without the right key or with a changed byte, legacy restore, bad recipients, a failed dump and a zero-byte dump) and runs in CI on `main`. It prints one `verify: ok:` line per check and `verify: all backup checks passed` at the end. Like the recipe, it uses the `thymeandplace-e2e` project, so don't run it while another e2e stack is up on the same machine.

**Windows / Git Bash note:** Git Bash rewrites a leading `/` in arguments as a Windows path, which breaks `--entrypoint /app/devseed` and similar. `MSYS_NO_PATHCONV=1` (exported above) turns that off; it's a no-op on Linux.

## Restoring from backup

A restore **replaces the whole database** with the dump. `restore.sh <key> [target-url]` (the `restore` service's entrypoint; the target defaults to its own `DATABASE_URL` from `restore.env`, i.e. the live database, as the bootstrap superuser):

1. refuses unless `RESTORE_CONFIRM` is set to the exact database name in the target URL (the last path segment before any `?params`), before anything is downloaded or touched — this stops a stale or copy-pasted URL from overwriting the wrong database;
2. for an encrypted key (`*.age`), refuses before downloading unless `AGE_IDENTITY` names a readable identity file: "encrypted backup; set AGE_IDENTITY to your recovery or restore-test key";
3. downloads the dump, decrypts it if it's encrypted, and checks it is non-empty and a readable `pg_dump` archive, so a missing, empty or corrupt backup exits non-zero with the database untouched. `age` authenticates its ciphertext, so a wrong key or a single changed byte also refuses here ("could not be decrypted with AGE_IDENTITY");
4. connects to the `postgres` maintenance database on the same server and runs `DROP DATABASE <db> WITH (FORCE)` then `CREATE DATABASE <db>` (the name is quoted as an identifier);
5. runs `pg_restore --single-transaction --exit-on-error --no-owner` into the empty database. Nothing created after the backup survives (tables, views, PostGIS-dependent objects), and there is no object-by-object `--clean` that could fail halfway through the live data;
6. hands the database and its objects back to `tp_owner` (`deploy/backup/own-objects.sql`), since `--no-owner` leaves them owned by the superuser. The grants to `tp_app` come back with the dump.

Dumps live in the bucket at `backups/daily/<YYYY-MM-DD>.dump.age` (last 7 days) and `backups/weekly/<YYYY>-W<ww>.dump.age` (last 4 weeks). `mc ls` lists them ("Backup deletion protection" shows how to run it). On the VPS, the `restore` service decrypts with the restore-test key by default. From `/opt/thymeandplace/deploy`:

```bash
docker compose stop app
docker compose --profile tools run --rm -e RESTORE_CONFIRM=thymeandplace restore backups/daily/<date>.dump.age
docker compose start app
```

`thymeandplace` is `POSTGRES_DB` from `/etc/thymeandplace/db.env`; if it refuses with "refusing to restore over ...", the name after `-e RESTORE_CONFIRM=` doesn't match the database name in `restore.env`'s `DATABASE_URL`. The app must be stopped first: `WITH (FORCE)` terminates its connections, and it must not write to the database mid-restore. After `start app`, check `curl -s https://thymeand.place/readyz` returns `ready`.

Plaintext `.dump` keys from before encryption (`backups/daily/<date>.dump`) restore with the same command and need no identity. The prune removes them within 28 days of the switch, but with versioning on (the minimum level) each one stays as a hidden version for 35 more days ("Backup encryption" › "Rollout" step 5).

### Disaster restore with the recovery key

Use the recovery key when the restore-test key can't decrypt the backup: the VPS or its key file is lost, or you're restoring onto a new server.

1. **On a new server**, follow "First deploy" steps 1–4 first, including the restore-test key in step 3. Compose refuses to start `restore` without `/etc/thymeandplace/restore-test.agekey`, even when the recovery key does the decrypting. The new restore-test key can't read the old backups, but list its public key in `BACKUP_AGE_RECIPIENTS` next to the recovery key's, so new backups are encrypted to it.
2. Copy the recovery key to the VPS for the length of the restore. As root, run `(umask 077; cat > /root/recovery.key)`, paste the key file's contents from your password manager, then press Ctrl-D.
3. Restore, from `/opt/thymeandplace/deploy`, then delete the copy straight away, even if the restore failed:
   ```bash
   docker compose stop app
   docker compose --profile tools run --rm -v /root/recovery.key:/run/secrets/recovery.agekey:ro -e AGE_IDENTITY=/run/secrets/recovery.agekey -e RESTORE_CONFIRM=thymeandplace restore backups/daily/<date>.dump.age
   docker compose start app
   shred -u /root/recovery.key
   ```
4. Run a backup and the restore test, as in "First deploy" step 10, so the newest backup is one the on-host key can read.

## Backup encryption

Every dump is encrypted with [`age`](https://age-encryption.org) before it leaves the host, so the bucket holds only ciphertext (`*.dump.age`). A dump holds the admin password hash, the sealed TOTP secret, passkey public keys, session token hashes and `admin_login_log`'s IP addresses, as well as the catalog. Design: `docs/superpowers/specs/2026-09-27-backup-encryption-design.md`.

### Keys

Each dump is encrypted to two X25519 keys, and either private key decrypts it.

| Key | Private key kept | Used for |
|---|---|---|
| **Recovery** | In your password manager and on paper. Never on the VPS, except while a disaster restore runs | Restoring when the VPS or its key is lost ("Disaster restore with the recovery key") |
| **Restore-test** | `/etc/thymeandplace/restore-test.agekey` on the VPS, mode `600`, root-owned. Compose mounts it read-only into the `restore` service only, at `/run/secrets/restore-test.agekey`, and sets `AGE_IDENTITY` to that path | The monthly restore test, and routine restores on the VPS |

1. **Recovery key**, on your own machine (install `age` there, e.g. `apt install age` or `brew install age`): `age-keygen -o recovery.key`. It prints `Public key: age1…`. Save the whole file in your password manager and write it on paper (the `AGE-SECRET-KEY-1…` line is the private key), then delete the file. It never goes on the VPS.
2. **Restore-test key**, on the VPS, from a root shell in `/opt/thymeandplace/deploy`. This runs `age-keygen` from the backup image, so the host needs no `age`, but the image has to be one with `age` in it. Always build it first, as its own command (`git pull` first on a VPS that is already running). On a VPS whose backup image predates encryption, skipping the build makes the keygen fail with "executable file not found" and leave an empty key file. On a fresh VPS, `run` would build the image itself and write the build's progress into the key file:
   ```bash
   docker compose --profile tools build backup
   (umask 077; docker compose --profile tools run --rm --no-deps -T --entrypoint age-keygen backup > /etc/thymeandplace/restore-test.agekey)
   ```
   It prints its public key; `grep 'public key' /etc/thymeandplace/restore-test.agekey` shows it again. Check the file:
   - `grep -c '^AGE-SECRET-KEY-1' /etc/thymeandplace/restore-test.agekey` prints `1`: one private key.
   - `grep -vc -e '^#' -e '^AGE-SECRET-KEY-1' /etc/thymeandplace/restore-test.agekey` prints `0`: nothing but comments and the key.
   - `stat -c '%a %U' /etc/thymeandplace/restore-test.agekey` prints `600 root`.

   If a check fails, delete the file and run both commands again.
3. **Recipients.** Put both public keys, space-separated, in `/etc/thymeandplace/backup.env`: `BACKUP_AGE_RECIPIENTS=age1<recovery> age1<restore-test>`. Only public keys go there. An empty value, or any entry that isn't `age1…` (a pasted `AGE-SECRET-KEY-…`, say), makes `backup` exit 2 and name the variable before it dumps anything. `backup.env` is read on every run, so a change applies from the next backup.

### Threat model

- **Bucket-only compromise** (leaked bucket keys, or an incident at the storage provider): reveals nothing. Every object is ciphertext, and neither private key is in the bucket. The exception is plaintext `.dump` objects from before the switch. The prune removes them within 28 days, but at the minimum level of "Backup deletion protection" that only hides them: each stays readable as a hidden version for 35 more days, about 63 days after the switch in all, to anyone with a bucket key. "Rollout" step 5 shows how to delete them sooner.
- **Host compromise:** the attacker can read the restore-test key and so decrypt the backup history. The live database is exposed in that case anyway, so the extra loss is older rows and deleted history. The recovery key stays off the host, and "Backup deletion protection" keeps the history restorable even if the attacker deletes it with the host's bucket key.

### Key rotation

Add the new recipient to `BACKUP_AGE_RECIPIENTS`, next to the old one, and run a backup by hand. Wait a month (29 days: the prune keeps weekly copies 28 days and the preferred level's lifecycle rule 29), until every kept backup is encrypted to the new key too, then remove the old recipient. Backups made before the removal stay decryptable with the old key until they age out, so keep the old private key until then. Copies that were pruned or expired before then are not gone yet: versioning keeps them hidden for 35 more days, and only the old key decrypts them. If you might need to undelete one, keep the old private key for 64 days after adding the new recipient.

- **Recovery key:** generate the new one as in "Keys" step 1 and store it the same way. Destroy the old copies only after the 29 days, or the 64 if undelete matters.
- **Restore-test key:** an identity file can hold several keys, so append the new one with `>>` in place of `>` in the "Keys" step 2 command (the `grep -c '^AGE-SECRET-KEY-1'` check then prints `2`). After the 29 days, or the 64, delete the old key's three lines (`# created`, `# public key` and its `AGE-SECRET-KEY-1…` line) from the file.
- **A lost key:** list a new key in its place straight away. Backups made before then can only be decrypted with the other key until they age out.

### Rollout

Once, when the encrypting backup image first goes to the VPS (spec §7):

1. Build the image: `git pull && docker compose --profile tools build backup`. `backup` and `restore` share it. The old image has no `age-keygen`, so this comes before the keys. Do steps 1 to 3 in one sitting, well away from the 03:15 nightly run: until step 2 sets the recipients, the new image's `backup` exits 2 naming `BACKUP_AGE_RECIPIENTS` before it dumps anything, so a run in between fails loudly and uploads nothing.
2. Generate both keys and set `BACKUP_AGE_RECIPIENTS` ("Keys" above).
3. Before the next nightly run, run `docker compose --profile tools run --rm backup` (it prints `backup ok: backups/daily/<date>.dump.age`), then `docker compose --profile tools run --rm --entrypoint restore-test.sh restore` (it prints `restore-test ok (<date>.dump.age)`). Both must succeed.
4. Turn on versioning at the provider, at least at the minimum level in "Backup deletion protection". Then consider the preferred level.
5. After 28 days, `mc ls --recursive "b/$BUCKET_NAME/backups/"` ("Backup deletion protection" shows how to run it) lists no plaintext `.dump` objects, only `.dump.age`. That listing shows only current objects. At the minimum level the pruned plaintext dumps remain as hidden versions for 35 more days, readable with the bucket key; `mc ls --versions --recursive "b/$BUCKET_NAME/backups/" | grep '\.dump$'` lists them, and it is empty once they are gone. To delete them sooner, any time after step 3 passed, run this in the same `mc` shell (the key in place at the minimum level can delete), or delete every version of each `.dump` file in the B2 console (Browse Files, show file versions):
   ```sh
   mc ls --versions --recursive "b/$BUCKET_NAME/backups/" | awk '{print $NF}' | grep '\.dump$' | sort -u |
     while read -r k; do mc rm --versions --force "b/$BUCKET_NAME/backups/$k"; done
   ```
   This removes only keys ending in `.dump`; the `.dump.age` backups and their versions stay. Then the `--versions` listing above must be empty. `restore.sh` keeps accepting plaintext dumps (spec §8 question 2).

**Rollback:** redeploy the previous backup image. It uploads plaintext `.dump` files again, and the `.dump.age` backups already uploaded stay restorable with the encrypting image (rebuild it to restore them) and a key.

## Backup deletion protection

`backup.sh`'s bucket key can delete, because the prune uses `mc rm`. Without protection at the bucket, whoever takes the host could wipe every backup along with the live database. There are two levels (spec §4.4):

- **Minimum** (any provider with versioning): versioning on, with noncurrent versions kept for 35 days. A delete, by the prune or anyone else, then only hides the object behind a delete marker, and the previous version can be brought back for 35 days. No script change.
- **Preferred** (the provider supports per-key permissions and lifecycle rules): the backup key can upload under `backups/` but not delete, restores use a separate read-only key, and lifecycle rules expire old copies in place of the prune. `BACKUP_PRUNE=lifecycle` in `backup.env` makes `backup.sh` skip its prune. Expire `backups/daily/` after 8 days and `backups/weekly/` after 29, a day longer than the prune's 7 and 28, so there are never fewer than 7 daily and 4 weekly copies. Versioning stays on, so an overwrite also leaves the earlier version for 35 days.

**In place here (awaiting the owner's confirmation):** Backblaze B2, EU Central (`docs/hosting-hetzner.md`), at the **minimum** level. B2 buckets keep earlier versions by default, and "Buckets on B2" step 2 sets the backups bucket's lifecycle to keep them for 35 days. The backups key in `backup.env` and `restore.env` can delete. Update this paragraph when the level changes.

### Running `mc` against the bucket

From `/opt/thymeandplace/deploy`, open a shell in the backup image with `restore.env`'s bucket settings and alias the bucket as `b`:

```bash
docker compose --profile tools run --rm --no-deps --entrypoint sh restore
mc alias set b "https://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null
mc ls --recursive "b/$BUCKET_NAME/backups/"
```

`exit` leaves the shell. (Use `http://` only if `BUCKET_USE_SSL=false`.)

### Checking versioning

- **B2:** in the web console, the backups bucket's **Lifecycle Settings** must say to keep prior versions for 35 days (or a custom rule with an empty `fileNamePrefix` and `daysFromHidingToDeleting: 35`). B2 buckets keep earlier versions by default, so this setting, which decides how long, is the part to check.
- **Other providers:** `mc version info "b/$BUCKET_NAME"` must report versioning enabled, and the provider's lifecycle settings must expire noncurrent versions after 35 days, not sooner.

### Undelete drill

Run it once after turning versioning on, and again after any change to the bucket's settings. The drill needs a key that can delete. At the minimum level the `restore.env` key can, so run it in the `mc` shell above:

```sh
o="b/$BUCKET_NAME/backups/daily/undelete-drill.txt"
echo drill | mc pipe "$o"
mc rm "$o"
mc ls --versions "$o"   # a delete marker above the earlier version
mc undo "$o"            # removes the delete marker
mc cat "$o"             # prints: drill
mc rm "$o"
```

The drill object doesn't disturb the backups: the restore test only picks `.dump` and `.dump.age` files, and the hidden copy expires with the 35-day rule. If `mc undo` fails against the provider, bring the object back in its console instead (in B2: Browse Files, show file versions, and delete the hide marker).

At the preferred level no key on the VPS can delete: the `restore.env` key is read-only, so the shell above already fails at `mc pipe`, and the backup key can upload but not delete. Run the drill in the B2 console instead. In Browse Files, open `backups/daily/`, upload a small file named `undelete-drill.txt`, hide it, turn on showing file versions, and delete the hide marker; the file must be back and download with its contents. Then delete the file and all its versions. (Or create a temporary key with `deleteFiles`, limited to the backups bucket, run the commands above with it, and delete the key straight after.)

### Moving to the preferred level (B2)

1. Add custom lifecycle rules to the backups bucket (Lifecycle Settings). B2 applies the smallest value of each matching rule, so the three rules combine:

   | `fileNamePrefix` | `daysFromUploadingToHiding` | `daysFromHidingToDeleting` |
   |---|---|---|
   | (empty, the whole bucket) | — | 35 |
   | `backups/daily/` | 8 | 35 |
   | `backups/weekly/` | 29 | 35 |

2. Create two keys, each limited to the backups bucket. The backup key can upload but not delete, and the restore key can only read (with the B2 CLI: `b2 key create --bucket <backups-bucket> --name-prefix backups/ thymeandplace-backup listBuckets,listFiles,writeFiles`, and `... thymeandplace-restore listBuckets,listFiles,readFiles`). Put them in `backup.env` and `restore.env`. If the check in step 4 fails at `alias` or `upload`, add `readFiles` to the backup key, never `deleteFiles`.
3. Set `BACKUP_PRUNE=lifecycle` in `backup.env`. Without it, every nightly prune fails with the new key, and the `backup` check alerts "pruning failed".
4. Check it. Run a backup by hand: it must print `backup ok` and turn the `backup` check green. Then show that the backup key can't delete: `docker compose --profile tools run --rm --no-deps --entrypoint sh backup`, alias the bucket as above, upload `undelete-drill.txt` with `mc pipe`, and check that `mc rm` on it fails with access denied. No key on the VPS can delete that object now, so delete it, with all its versions, in the B2 console (Browse Files, show file versions). Run the restore test (it must print `restore-test ok`).
5. Delete the old key that could delete, and update "In place here" above.

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

- The app trusts `X-Forwarded-*` headers (`TRUST_PROXY_HEADERS=true`, set in `deploy/compose.yaml`) from **exactly one proxy hop, Caddy**, and uses the **rightmost** `X-Forwarded-For` entry as the client IP (`web.ClientIP`). Caddy has no `trusted_proxies`, so it discards any `X-Forwarded-For` a client sends and writes the address it saw. If the rightmost entry isn't a valid IP, the app uses Caddy's own address, which is one shared bucket, so a garbled header can't earn a fresh rate-limit budget. **Before putting a CDN or any other proxy in front of Caddy, change `web.ClientIP` first.** With `trusted_proxies` set, the rightmost entry becomes the CDN edge, and every visitor behind it would share one public rate-limit bucket and one admin sign-in budget. The recommended change is to set both global server options `trusted_proxies static <CDN ranges>` and `trusted_proxies_strict`, have Caddy send `header_up X-Client-IP {client_ip}`, and have the app read only that header. `trusted_proxies_strict` is required: without it Caddy parses `X-Forwarded-For` left to right, so `{client_ip}` becomes the leftmost entry, which the client controls (spec `2026-09-27-admin-abuse-resistance-design.md` §8 question 3). **Never publish the app's own port** (`8080`) alongside Caddy's 80/443. If a client could reach the app directly, it could forge those headers and spoof its source IP past the admin sign-in budget. `deploy/compose.yaml` intentionally has no `ports:` entry under `app`; only `caddy` publishes ports.
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

Each run logs `INFO "admin prune"` with the three deleted counts; a failure logs `ERROR "admin prune failed"` and is retried after 1 min, then 2, 4 … up to 1 h, until a run succeeds. If more than 10,000 login-log rows were written in the last 24 hours, it also logs `WARN "admin login log growing fast"`: the per-client budget bounds each client, but not a flood from many addresses, which belongs in an upstream firewall. After a deploy, `docker compose logs app | grep "admin prune"` shows the startup run.

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

Wrap these in `docker compose exec db psql -U tp -d thymeandplace -c "..."`. Backups keep pruned rows for up to four more weeks (7 daily, 4 weekly copies), and the bucket keeps deleted copies for 35 days after that ("Backup deletion protection").

## Capacity

**Connection budget.** Each binary sizes its own pool (`internal/db/db.go`): the server 16 connections (2 kept open), overridable with `DB_MAX_CONNS`; `admin`, `ingest` and `devseed` 4 each. The server's data-version listener holds one more, outside the pool. With a backup (`pg_dump`, 1) and a couple of `psql` sessions that's about 30, well under Postgres's default `max_connections` of 100, with room for a second app replica. A `pool_max_conns` or `pool_min_conns` parameter in `DATABASE_URL` wins over these defaults.

**Public DB gate.** Public database work (a `/season` or `/ingredient` cache miss, a `/places` search, and the `/season` heading label) shares three quarters of the server pool: 12 of 16 slots. The rest stays free for `/admin`, the drift check, catalog reloads and Apply's second connection, so a public spike can't lock the owner out. A request that can't get a slot within 250 ms gets the retry panel with `503` and `Retry-After: 5` straight away instead of queueing until the 2 s deadline. The heading label ("near X") is looked up on every supported `/season` request, cache hits included, so warm `/season` traffic takes a slot too; when none is free the heading falls back to coordinates and the page still loads with `200`. Other cache hits, `/healthz`, `/readyz`, static files and `/admin` never wait on the gate. `/ingredient` and `/season` misses share a cache of each cell's climate normals and ecoregions per data version (20,000 cells), so repeat clicks cost no queries.

**Changing the pool size.** Set `DB_MAX_CONNS` in the app env file and restart the app (`docker compose up -d app`); the gate follows at three quarters. On a VPS with less than 4 GB RAM use 10 (7 public slots). Re-run the load test afterwards.

**The `public DB gate full` log line.** When requests are shed, the app logs `INFO "public DB gate full"` at most once a minute, with `shed` (lookups the gate turned away since the last line: 503s plus `/season` headings that fell back to coordinates) and `slots`. Sustained 503s with this line mean the site is at capacity, not that the database is down: `/readyz` still says `ready`. The line without 503s means only headings fell back: the gate is busy but pages still load. Check `SELECT count(*), usename, state FROM pg_stat_activity GROUP BY 2, 3`; the app should hold between 2 and 17 connections.

**Load test.** `deploy/loadtest/` has a k6 script and a README. Run it on the production VPS before launch and after any change to `DB_MAX_CONNS`, the VPS size or the reference data, and record the results and the ocean query timings in `docs/capacity-baseline.md`. Warm `/season` requests are not DB-free: each runs one indexed nearest-place query for its heading, so the warm p95 includes it.

## Health and logs

- `docker compose ps` shows health; `curl -s https://thymeand.place/readyz` returns `ready` when the DB is reachable and the catalog is loaded. The database part of `/readyz` is shared: one ping answers every request for 1 s, so `/readyz` can lag a database outage or recovery by up to a second. Sustained 503s while `/readyz` is `ready` and the log shows `public DB gate full` mean load shedding (see "Capacity"), not an outage.
- Logs are JSON on stdout: `docker compose logs -f app`. Every service uses Docker's `json-file` driver with rotation (`max-size: 10m`, `max-file: 20`, `compress: true`, set once in `deploy/compose.yaml`'s `x-logging` anchor), so each container keeps 20 files of up to 10 MB of log each, the rotated ones compressed (at most 200 MB if nothing compressed); older lines are gone after rotation. `docker compose logs` reads the compressed files too. A container keeps the log options it was created with until `docker compose up -d` recreates it; `docker inspect --format '{{.HostConfig.LogConfig}}' $(docker compose ps -q app)` shows what it has. Healthy `/healthz` and `/readyz` lines (2xx) aren't logged, so the probes don't use up the budget; failing probes are. Public requests record `cell_id`, never coordinates. Successful backup logins log at `WARN`, and so do the backup lockout (`backup login locked`) and calibration drops of more than 0.02. Each of these also fails a check ("Monitoring and alerts").
- The logs rotate away, so the durable records are in the database: `calibration_run` has every drift score, and `admin_login_log` has every sign-in attempt (kept 90 days).
- The drift check runs 24 h after the last recorded run, not at each start: `SELECT ran_at FROM calibration_run ORDER BY id DESC LIMIT 1` plus 24 h is the next one. A deploy doesn't trigger it. Until the catalog snapshot loads it waits without logging. Each run also reports to the `drift` check. A failed run logs `calibration drift check failed` and is retried after 1 min, then 2, 4 … up to 1 h, so a string of those lines minutes apart is retries, not separate days. The admin prune still runs at each start, and its failures (`admin prune failed`) retry the same way. With more than one app replica, each schedules its own checks, and an extra `calibration_run` row is harmless.
- A logged `cell_id`, with the month and the live data version, determines everything a cook saw except the heading. To reproduce it, open `/season?lat=<90 − (⌊id/1440⌋ + 0.5) × 0.25>&lng=<−180 + (id mod 1440 + 0.5) × 0.25>`, or `/ingredient/{slug}?cell=<id>`.
- The app exits on fatal errors, and `restart: unless-stopped` brings it back. Nothing restarts an unhealthy container. The `app` and `site` alerts tell you to (see "Monitoring and alerts").

## Monitoring and alerts

The app and the backup jobs ping hosted [Healthchecks.io](https://healthchecks.io) checks, and an external uptime monitor probes the site. A check that misses its ping alerts too, so a dead app, VPS or network still reaches you. Monitoring is off until configured: an unset `HEARTBEAT_*` variable turns that ping off, and dev, CI and e2e set none. A malformed URL logs `ERROR "heartbeat URL invalid; check disabled" check=<name>` and turns that ping off, and the site keeps serving; the check then alerts as missed. Design: `docs/superpowers/specs/2026-09-27-observability-and-alerting-design.md`.

### Checks

| Check | Sent by | Healthchecks.io schedule | Goes down when | Ping URL |
|---|---|---|---|---|
| `app` | the app's watchdog | Period 5 min, grace 5 min | No ping (app, VPS or outbound network down), or the watchdog reports a failure | `HEARTBEAT_APP_URL` in `app.env` |
| `admin-auth` | the app's watchdog | Period 1 day, grace 1 h | The backup login was used, the backup lockout engaged, or the login log grew fast ("Admin table pruning"). The long period means an app outage alerts only through `app` | `HEARTBEAT_ADMIN_AUTH_URL` in `app.env` |
| `drift` | the drift check | Period 1 day, grace 2 h | The score dropped by more than 0.02, the run failed, or no run happened | `HEARTBEAT_DRIFT_URL` in `app.env` |
| `backup` | `backup.sh` | Cron `15 3 * * *`, VPS time zone, grace 1 h | Non-zero exit, prune failure, no success within the grace time after `/start`, or no run | `HEARTBEAT_BACKUP_URL` in `backup.env` |
| `restore-test` | `restore-test.sh` | Cron `15 4 1 * *`, VPS time zone, grace 2 h | Count mismatch, no dump, or no run | `HEARTBEAT_RESTORE_TEST_URL` in `restore.env` |
| `site` | external uptime monitor | Every 5 min or less | Not 200, body not `ready`, TLS error, or certificate expiring within 14 days | the vendor's settings |

Set every Healthchecks.io check to accept `POST` pings only, so a link preview or crawler that opens a ping URL can't flip it. Give the two cron checks the VPS's time zone (`timedatectl` shows it); their schedules match the backup timer and cron entries in "Nightly backups".

The watchdog samples readiness every minute and reports every 5 minutes, so it sends at most one ping per check a minute. A report fails `app`, with one line per reason, when:

- `not ready: catalog not loaded` or `not ready: database unavailable`: 3 samples in a row failed. This one is sent at once, not at the next 5-minute report. A site still waiting for its first catalog snapshot reports it too.
- `5xx: <n> of <m> requests`: at least 5 responses, and at least 5% of the non-probe responses, were 5xx.
- `errors: <n> (<message>; ...)`: the app logged at least one `ERROR`. Up to 3 distinct messages are sent; a message with anything but letters, spaces and `:;,'_-` is sent as `(message withheld)`.

Otherwise it sends `ok requests=<m> 5xx=<n>`. `admin-auth` gets `backup login used: <n>; backup lockout: <n>; login log growth: <n>` when any of those happened, and `ok` otherwise. On shutdown the app stops without pinging, and the grace time covers a deploy. With more than one app replica, give each its own `app` check, or a healthy replica hides a dead one.

### Ping URLs are secrets

Use each check's own UUID ping URL (`https://hc-ping.com/<uuid>`), not the project ping key + slug form, where one leaked key exposes every check. Anyone holding a ping URL can flip its check, so the URLs live only in the root-only env files above, never in the repo or `compose.yaml`, and neither the app nor the scripts print them. The app reads its three at startup (`docker compose up -d app` after changing them); `backup` and `restore-test` read theirs on each run. If one leaks, replace the check with a new one and update its env file.

### Privacy

Monitoring keeps the promise in "Reverse proxy trust and logging" and on the About page: nothing that leaves the VPS identifies a visitor.

| Channel | May carry | Never carries |
|---|---|---|
| App log (stdout) | Unchanged: request ID, path without query, `cell_id`, admin IP on admin events | Coordinates, query strings, public visitor IPs, ping URLs |
| Ping bodies (Healthchecks.io) | Counts, reason labels, constant `ERROR` messages, calibration scores, data-version strings, backup object keys, table row counts | Paths, `cell_id`, coordinates, any IP, log attributes, secrets |
| External monitor | Its own probe of `/readyz` or `/` | Anything about visitors |
| A future metrics endpoint | Status class and fixed route class (`season`, `places`, `ingredient`, `admin`, `static`, `other`) | Raw paths, query values, `cell_id`, IPs or slugs as labels |

A future metrics endpoint must use its own port. Caddy must never proxy that port, and `ports:` must never publish it.

### External uptime monitor

The `site` check runs at an uptime monitoring vendor, not in this repo, and catches what in-app pings can't: Caddy, TLS, DNS and firewall faults. The vendor must support:

- an HTTPS check that `https://thymeand.place/readyz` returns 200 with a body of exactly `ready`, every 5 minutes or less;
- alerting only after 2 consecutive failures, so a deploy's brief maintenance page doesn't page you;
- a certificate-expiry warning at 14 days (Caddy starts renewing with a third of a certificate's lifetime left, so this warning means renewal has been failing for a while);
- email and push notifications.

If `/readyz` is ever taken off the public site, check `/` for the text `Find your town` instead; database readiness then rests on the `app` check.

### Setting up

1. In a Healthchecks.io project, create the five checks above with their schedules, `POST` only, and the cron checks in the VPS time zone. Attach email and one push channel (ntfy, Pushover, Telegram or Signal; SMS and phone calls need a paid plan) to every check, and send a test notification.
2. Put each check's ping URL in its env file (`sudoedit /etc/thymeandplace/app.env` and so on).
3. Configure the external monitor, and turn on the VPS provider's disk and CPU alerts if it offers them.
4. `docker compose up -d`. Compose recreates the containers whose settings changed, so they pick up the new env and log options. `app` and `admin-auth` go green within a few minutes. `drift` goes green at the next scheduled run, 24 h after `SELECT ran_at FROM calibration_run ORDER BY id DESC LIMIT 1`; a deploy doesn't trigger one.
5. Run a backup by hand (`docker compose --profile tools run --rm backup`): the `backup` check shows `/start`, then success. Then run `docker compose --profile tools run --rm -e BUCKET_NAME=missing backup`: expect a failure alert naming `upload`. Run a normal backup again to clear it.
6. Optional drill, on a quiet evening: `docker compose stop app` for 12 minutes. Expect `site` and `app` alerts, then recovery after `docker compose start app`.

### Planned work

Before planned work that keeps the site or a job down for more than 10 minutes (a restore, a VPS upgrade, a long migration), pause the affected checks in Healthchecks.io and the external monitor, and resume them afterwards. Without a ping, `app` alerts after its 5-minute period plus 5 minutes' grace. A normal deploy fits inside that.

### Runbook

Start with the check's event log in Healthchecks.io: the last ping body says what failed, and the alert itself may not include it. Run the commands from `/opt/thymeandplace/deploy`.

| Alert | First step |
|---|---|
| `site` | `docker compose ps`, `docker compose logs --since 30m caddy app`. Run `docker compose restart app` if it is hung |
| TLS warning | `docker compose logs caddy \| grep -i acme` |
| `app` missing ping | Is the VPS up? `docker compose ps`. Is outbound HTTPS open? |
| `app` not ready | `docker compose ps db`, `docker compose logs --since 30m db app` |
| `app` 5xx or errors | Read the check's event log, then `docker compose logs --since 15m app \| grep -E '"level":"ERROR"\|"status":5'` |
| `admin-auth` | `docker compose exec db psql -U tp -d thymeandplace -c "SELECT at, ip, method, outcome FROM admin_login_log ORDER BY at DESC LIMIT 20"`. If it wasn't you: `docker compose exec app /app/admin revoke-sessions`, then `docker compose exec app /app/admin set-backup`. For login log growth, see "Admin table pruning" |
| `drift` | `docker compose exec db psql -U tp -d thymeandplace -c "SELECT * FROM calibration_run ORDER BY id DESC LIMIT 5"`. Consider `docker compose --profile tools run --rm ingest <dataset> rollback`. For a failed run, `docker compose logs --since 25h app \| grep 'calibration drift'`. Recovery the next day means no further drop, not fixed |
| `backup` | `journalctl -u thymeandplace-backup` (systemd timer) or `/var/log/thymeandplace-backup.log` (cron), then rerun by hand. `during env` means a setting in `backup.env` is missing or wrong, such as `BACKUP_AGE_RECIPIENTS` ("Backup encryption"); `during verify` means the dump was empty or unreadable, and nothing was uploaded |
| `restore-test` | Read the count table in the check's event log (or `journalctl -u thymeandplace-restore-test`, or the log file). Don't trust the backups until it passes. An admin edit between the 03:15 backup and the test can make `catalog_history` differ; rerun `backup`, then the test, by hand |

A `drift` alert that clears the next day means the score didn't drop again, not that it recovered: each run compares with the previous run, which is already the lower score. Check `calibration_run` before closing it. A failed drift run also logs `ERROR`, and so does each retry (after 1, 2, 4 … 60 minutes), so `app` fails once per failed attempt while the drift check keeps failing.

The logs that explain an alert rotate away; `calibration_run` (every drift score) and `admin_login_log` (every sign-in attempt, 90 days) are the durable records.
