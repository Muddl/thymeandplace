# Operations

**Run as root.** The secrets live in root-only env files under `/etc/thymeandplace/` (each mode `600`, in a mode-`700` directory), one per service (`env_file` in `deploy/compose.yaml`; see "Database roles"), and `docker compose` reads them, so run every command from a root shell (`sudo -i`, then `cd /opt/thymeandplace/deploy`) or prefix each with `sudo`. All `docker compose` commands below run from `/opt/thymeandplace/deploy`. The backup cron job and systemd units below already run as root.

## First deploy (one VPS)

1. Install Docker Engine + Compose v2 (≥ 2.24). Point `thymeand.place` and `www.thymeand.place` A/AAAA records at the VPS. (On Hetzner Cloud, `docs/hosting-hetzner.md` covers the server, firewall, DNS, host hardening and buckets.)
2. `git clone` the repo to `/opt/thymeandplace`. Then, once, log Docker in to GitHub's container registry, where CI publishes the images that `deploy.sh` runs ("Updating the app"). Create a classic personal access token with only the `read:packages` scope, run `docker login ghcr.io -u <github user>` as root, and paste the token at the password prompt. The login is stored in root's Docker config. (A fine-grained token wasn't verified to read GHCR; the classic token works.)
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

   Then the two backup encryption keys (details in "Backup encryption"). On your own machine, not the VPS, run `age-keygen -o recovery.key` and store the file in your password manager and on paper. On the VPS, from a root shell in `/opt/thymeandplace/deploy`, build the backup image, then create the restore-test key. `age-keygen` writes the key file itself, mode `600`, through a bind mount, so the private key never passes through the container's output or its log.
   ```bash
   docker compose --profile tools build backup
   docker compose --profile tools run --rm --no-deps -v /etc/thymeandplace:/out --entrypoint age-keygen backup -o /out/restore-test.agekey
   grep -c '^AGE-SECRET-KEY-1' /etc/thymeandplace/restore-test.agekey             # must print 1
   grep -vc -e '^#' -e '^AGE-SECRET-KEY-1' /etc/thymeandplace/restore-test.agekey  # must print 0
   ```
   If either check prints anything else, delete the file and run the two commands again (`age-keygen` won't overwrite an existing file). Put both public keys (`age1…`, space-separated) in `BACKUP_AGE_RECIPIENTS` in `backup.env`, and keep them nowhere else except your password manager ("Backup encryption" › "Threat model"). `age-keygen` prints each one, and `grep 'public key' /etc/thymeandplace/restore-test.agekey` shows the restore-test one again. Without the recipients `backup` refuses to start, and without the key file `restore` does.
4. `cd /opt/thymeandplace/deploy && sudo docker compose up -d --build`. On the empty volume, `db` runs `deploy/db/initdb/20-roles.sh` once to create the roles; then the one-shot `migrate` service applies the migrations as `tp_owner` and exits, and `app` starts as `tp_app`. Caddy obtains certificates automatically. This first start builds the images from the checkout; every later update runs the images CI built ("Updating the app").
5. Owner access: `docker compose exec app /app/admin enroll`. Open the link on your phone, register a passkey, repeat for a second device. Then `docker compose exec app /app/admin set-backup` and scan the QR code.
6. Reference data, per dataset (`places`, `ocean`, `climate`). Before approving any license, check its row in `docs/reference-data-licenses.md`. **Skip ocean for launch:** its MEOW terms are unresolved, so `approve-license` refuses it (see "Ocean dataset" below), and the site launches with seafood off ("Launching without seafood").
   ```bash
   docker compose --profile tools run --rm ingest places fetch        # note the version N and read the per-file summary it prints
   docker compose --profile tools run --rm ingest places approve-license --version N         # dry run: repeats the summary, changes nothing, exits 2
   docker compose --profile tools run --rm ingest places approve-license --version N --yes
   docker compose --profile tools run --rm ingest places all --version N
   ```
   `fetch` prints the license and, for each file, its URL, its sha256, whether it is `pinned` or `unpinned: <reason>`, and how it compares with the live version (`same as live vK`, `changed (live vK: …)` or `no live version`). A file whose sha256 differs from its pin fails the fetch; see "When a pinned source changes". The labels describe the version's recorded checksums, so a version fetched before the pins existed can show `pin mismatch (pin …)` or `sha256 (no record)`: don't approve it; fetch again. `approve-license --version N` without `--yes` prints the same summary and approves nothing; with `--yes` it prints it again as the record and approves. A review carries over to a later fetch only when the license, URLs and every file's checksum are the same. GeoNames regenerates its dumps at fixed URLs, so places is unpinned and every places refresh needs `approve-license` again.

   Climate needs the transform container once per version (all from `/opt/thymeandplace/deploy`):
   ```bash
   docker compose --profile tools run --rm ingest climate fetch
   docker compose --profile tools run --rm ingest climate approve-license --version N --yes   # after reading the summary
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

Both licenses are printed together by `ingest ocean fetch`. Once the terms are resolved, both must be read before `approve-license --version N --yes`. A review carries over only to later fetches of identical bytes (same license text, source URLs and file checksums), so it must only ever be given for confirmed terms.

### Launching without seafood

The site launches with seafood off, because the MEOW terms aren't confirmed (`docs/reference-data-licenses.md`, "Launch decision"). The switch is `FEATURE_SEAFOOD` in `app.env`: unset or `off` (the default) serves a land-only site, `on` serves seafood. Any other value stops the app at startup. With seafood off, the public site:

- makes no ocean lookups;
- leaves seafood items out of `/season`, answers `/ingredient/{slug}` for them with 404, and drops pairings that point at them;
- says estimates cover land areas only, and leaves the seafood paragraph and marine credits off `/about/data`.

Every climate-estimated `/season` row (`Estimate: true`) shows the word Estimate in normal case next to a decorative ≈ in an `aria-hidden` span. Generated content must not paint ≈, so the mark never enters an accessible name. The "Estimates from local climate: how we estimate" link under the place name stays free of ≈. Hand-curated rows (`Estimate: false`, including seafood) omit the badge.

`main` has one fixed site `h1` ("What's in season") above `#results`. The place name stays the results `h2` and must not be copied into that `h1`. Dark mode follows `prefers-color-scheme` only.

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
2. Ingest ocean: `fetch`, read the license and per-file summary it prints, `approve-license --version N --yes`, `all --version N`.
3. Import `content/catalog/launch.json` in `/admin/import`. Import only upserts, so this adds the seafood items and pairings back.
4. Set `FEATURE_SEAFOOD=on` in `/etc/thymeandplace/app.env` and recreate `app` with the release overlay ("Recreating `app` after a settings change" in "Updating the app"); the flag is read at startup. The log should show `seafood feature enabled=true`.

To turn it off again, set it back to `off` and recreate `app` the same way; nothing else changes. If the terms are withdrawn, also follow "Withdrawing a dataset".

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
docker compose --profile tools run --rm ingest climate approve-license --version N --yes   # after reading the summary
docker compose --profile tools run --rm ingest climate transform --version N
docker compose --profile tools run --rm transform /work/climate/vN
docker compose --profile tools run --rm ingest climate all --version N
```

What to check:
- `fetch` prints the version number `N` (note it), the license (CC0, citation requested) and a per-file summary: each `.nc` file's URL and sha256, `pinned`, and how it compares with the live version. A file that doesn't match its pin fails the fetch (see "When a pinned source changes"). Approve with `ingest climate approve-license --version N --yes` before `all`.
- `transform` (step run through the `ingest` binary) exits `3` and prints the exact `docker compose --profile tools run --rm transform /work/climate/vN` command to run next; run that literally from `/opt/thymeandplace/deploy` (the `deploy/` directory, next to `compose.yaml`), where every `docker compose` command in this document runs.
- The transform container's own log should finish without error and leave `out/climate.csv` under the shared `ingest_work` volume.
- `ingest climate all --version N` runs load/validate/swap; validate compares interpolated values at twelve fixed reference cities (London, Moscow, Cairo, Singapore, …) against known climatology figures within a 4°C tolerance (`internal/ingest/climate.go`). If validate fails, do not swap it in by hand — check the download completed and the transform didn't silently truncate.
- Watch disk and memory on the VPS during this step; the raw NetCDF files plus the transform's intermediate arrays are the heaviest thing this system ever does in one step.

## Updating the app

Production runs the images CI built and tested, by commit SHA. Wait for the commit's `release` run in GitHub Actions to go green (it runs only after `ci` passed on `main`, and its summary lists each image's digest), then, from `/opt/thymeandplace/deploy`:

```bash
git pull
./deploy.sh <sha>
```

`<sha>` is that commit's full 40-character SHA, as shown on the green `release` run. `deploy.sh` refuses anything else (a short SHA, `main`, a tag) and exits 2 before pulling. `git pull` brings `compose.yaml`, `compose.release.yaml` and `deploy.sh` itself up to date. The script pulls the images built for that commit (`ghcr.io/muddl/thymeandplace-{app,ingest,transform,backup}:<sha>`) and the `db` and `caddy` images pinned in `compose.yaml`, all before the maintenance window starts. It then runs the one-shot `migrate` service (as `tp_owner`), starts the new app only once `migrate` has exited successfully, waits for the app's health check, and starts `caddy`. Compose recreates `db` and `caddy` only when their image or settings changed, so a `compose.yaml` digest update is applied by the next deploy; a new PostGIS image restarts the database inside the window. The script then writes `deploy/.env` (below), appends `<time> <sha> <app image digest> <seconds>s` to `/var/lib/thymeandplace/deploys.log` and prints `deploy ok: <sha> (<digest>); maintenance window about Ns`, the time the start step took, migrations included.

The app refuses to start while any migration is pending ("database has pending migrations; run the migrate service"). If that happens, or `deploy.sh` stops at `migrate`, `docker compose logs migrate` says why. Migrations must be backward-compatible with the previous version (add columns/tables; never drop or rename in the same release that stops using them). That is what makes "Rolling back the app" safe.

**`deploy/.env`.** After a successful start, `deploy.sh` writes `/opt/thymeandplace/deploy/.env` (root-owned and git-ignored) with `COMPOSE_FILE=compose.yaml:compose.release.yaml` and `TP_IMAGE_TAG=<sha>`. Compose reads `.env` from the directory it runs in, so from then on every plain `docker compose` command in this document runs the deployed release: `up -d`, `exec`, `ps`, the `--profile tools run` commands, and the backup and restore-test timers. `cat .env` shows which release that is, and every deploy or rollback rewrites it. Don't edit it. A command that names its own `-f` files (`smoke.sh`, `e2e/run.sh`, `deploy/backup/verify.sh`) ignores `COMPOSE_FILE`. Before the first `deploy.sh` there is no `.env`, and the same commands build and run from source as before.

**Recreating `app` after a settings change.** The app reads its env file only at startup, and `docker compose restart app` keeps the old environment, so a change to `app.env` (or a `compose.yaml` setting such as log options) needs the container recreated:

```bash
docker compose up -d app
```

Compose runs the one-shot `migrate` again first (it finds nothing to apply), recreates `app` only if its settings changed, uses the release named in `.env`, and writes nothing to `deploys.log`. `docker compose ps app` shows what runs: an `IMAGE` of `ghcr.io/muddl/thymeandplace-app:<sha>` is a release, `thymeandplace-app:latest` a source build.

**Emergency path.** If GHCR or GitHub Actions is unavailable, build from the checkout:

```bash
rm -f .env && git pull
docker compose --profile tools build
docker compose up -d app
```

Removing `.env` puts every plain command back on the local `thymeandplace-*:latest` images, and the `build` line rebuilds all of them from the checkout: `app` (which `migrate` shares) and the `ingest`, `transform` and `backup` images that the tools and the backup and restore-test timers run. Don't skip it or narrow it to `app`. Nothing else rebuilds the tool images, and on a VPS that has run `deploy.sh` the local ones are whatever was last built from source, possibly before backup encryption (an old backup image uploads plaintext dumps and can't restore `.dump.age`). With `.env` in place, `build` has nothing to build and Compose keeps the release. `up -d app` runs `migrate` first in the same way. Nothing is written to `deploys.log`. Go back to `deploy.sh` with the next release; it writes `.env` again.

**Moving over from source builds.** The first `deploy.sh` run replaces the locally built app with the registry image for the same commit. Keep the old `thymeandplace-app:latest` image (no `docker image rm`, no `docker image prune -a`) until the registry deploy has run for a day. Until then, this puts it back, and rebuilds the tool images from the checkout so the timers don't fall back to old local ones (naming the three leaves `thymeandplace-app:latest` as it is):

```bash
rm -f .env
docker compose --profile tools build ingest transform backup
docker compose up -d app
```

**First update of a running VPS to `deploy.sh`.** The release that brings `deploy.sh` also brings backup encryption and the heartbeat checks, and the three one-time procedures depend on each other. Once `deploy.sh` has run, the nightly timer uses the release's encrypting backup image, which exits 2 until `BACKUP_AGE_RECIPIENTS` is set. Nothing alerts on that until `HEARTBEAT_BACKUP_URL` is set, which is also new in this release. So, in order and in one sitting, well away from the 03:15 backup:

1. Log Docker in to GHCR, as in "First deploy" step 2 (once per VPS).
2. Wait for the release's green `release` run, then `git pull && ./deploy.sh <sha>` ("Updating the app"). This is "Backup encryption" › "Rollout" step 1.
3. "Rollout" steps 2 and 3: generate both keys, set `BACKUP_AGE_RECIPIENTS`, then run `backup` and the restore test by hand. Both must succeed before you stop.
4. "Monitoring and alerts" › "Setting up", so a failed nightly backup alerts from now on.
5. Later: "Rollout" step 4 (versioning) and, after 28 days, step 5.

### The shared `ingest_work` volume and file ownership

`ingest` (distroless `nonroot`) and `transform` both run as uid/gid `65532` and share the `ingest_work` volume at `/work`. Both images ship a `65532`-owned `/work`, and Docker seeds a new (or empty) named volume from the image directory it mounts over, ownership included, so a fresh volume is writable by both with no manual step.

A volume that already holds files from before this was fixed (created root-owned, or with root-owned files left by an older `transform` image that ran as root) makes `ingest ... fetch` fail with `mkdir /work/<dataset>/incoming: permission denied`. Fix it once, from `/opt/thymeandplace/deploy`:

```bash
docker compose --profile tools build ingest transform
docker compose --profile tools run --rm --user 0 --entrypoint chown transform -R 65532:65532 /work
```

After the first `deploy.sh`, the `build` line prints `No services to build`: the release images already have the fix.

## Rolling back the app

From `/opt/thymeandplace/deploy`:

```bash
tail -n 5 /var/lib/thymeandplace/deploys.log   # one line per deploy: time, SHA, app digest, window
./deploy.sh <previous sha>
```

The previous SHA is the second field of the newest line whose SHA differs from the release running now (`docker compose ps app` shows that one in its image tag). Lines can repeat a SHA, and an emergency source build writes no line at all. No rebuild is involved: every release's images stay in the registry (and usually on the host), so `deploy.sh` starts the previous release's images like any deploy and logs its own line. Its `migrate` finds nothing to apply. The newer migrations stay in the database, and the previous app works with them because migrations are backward-compatible ("Updating the app"). Roll forward again the same way, with the newer SHA.

`compose.yaml` and `deploy.sh` come from the checkout, not from the images. If the release you're leaving changed `deploy/compose.yaml` in a way the previous images can't run with, run `git checkout <previous sha>` first, then `git checkout main` before the next `git pull`.

## Base images and updates

Everything the builds and CI pull is pinned:

- GitHub Actions by commit SHA, with the version in a comment.
- Every `FROM` and `image:` by digest (`name:tag@sha256:…`).
- The release build's BuildKit and SBOM scanner images by digest (`.github/workflows/release.yml`).
- The Go tools (`templ`, `staticcheck`, `govulncheck`) by `go.mod` `tool` lines.

Pull request process lives in `docs/WAYS-OF-WORKING.md`. The `pr-governance` workflow runs on `pull_request` events only. It checks out the base SHA, runs that `cmd/prcheck`, and reads the pull request through the API. A sign-off comment is not a trigger. Apply a required label, or push, so the check re-runs on the head SHA. The checker requires a `Closes #N`, `Fixes #N`, or `Refs #N` link to an issue with a `## Acceptance criteria` heading, plus path sign-offs from `.github/pr-signoffs.json`. Dependabot pull requests skip the issue link. Only PM Farmer applies the `no-issue` label. Until the shared GitHub App lands, only comments by `Muddl` count.

Dependabot (`.github/dependabot.yml`) opens weekly grouped pull requests, one per ecosystem:

- `gomod`
- `github-actions`
- `docker`: the Dockerfiles in `deploy/`, `deploy/backup/` and `pipeline/transform/`
- `docker-compose`: `deploy/compose.yaml`
- `pip`
- `npm`

Merge one when CI is green, then deploy the release it produces as usual. That includes `docker-compose` updates: `deploy.sh` pulls and applies the `db` and `caddy` images too. Read the release notes first for a new major version. The actions are pinned to the majors CI already used, so the first `github-actions` pull request is likely to propose majors.

## CI end-to-end tests

The `e2e` job in `.github/workflows/ci.yml` runs on every pull request and on every push to `main`. It does not run on a feature-branch push that has no pull request. Branch protection should require `e2e-result`, not the matrix cell names. That job `needs: e2e` with `if: always()` on pull requests and on `main`, and it fails if the matrix failed, was cancelled, or was skipped.

`e2e/select-browsers.sh` chooses the browser list. A push to `main`, or a pull request that touches `views/`, `static/` or `e2e/`, runs Chromium, Firefox and WebKit. Other pull requests run Chromium only. If `git diff` fails, the script prints all three engines rather than falling back to Chromium. The `deploy` job runs `./e2e/select-browsers.sh --self-test`, `node e2e/check-tags.mjs`, and `node e2e/check-capture.mjs` on every push so a broken path, tag, or capture rule fails CI even when e2e itself is skipped. Each matrix cell installs only its browser and restores `~/.cache/ms-playwright` from `actions/cache`.

The matrix is `browser × shard` (`1/2` and `2/2`). Each cell starts its own Compose stack, because the suite stops Postgres and edits the catalog (`workers: 1` in `e2e/playwright.config.ts`). Admin specs (`e2e/tests/admin.spec.ts`) use CDP WebAuthn, which is Chromium-only; the Firefox and WebKit projects set `testIgnore` on that file.

CI runs the full suite (no `GREP`). To run only the smoke subset locally, or against production:

```bash
GREP=@readonly ./e2e/run.sh
E2E_BASE_URL=https://thymeand.place GREP=@readonly BROWSER=chromium ./e2e/run.sh
```

`GREP=@mutating` is `--grep-invert @readonly`, so untagged tests run in the mutating subset. `run.sh` exits 2 if the origin is not localhost unless `GREP=@readonly`. Every spec is tagged `@readonly` or `@mutating`. `check-tags.mjs` fails on any untagged test. Specs search for Lisbon by name and pass today's UTC date. The 1 s warm `/season` test skips when `E2E_BASE_URL` is not localhost. The Tomato flow is `@mutating` and skips unless the origin is local and `TP_PUBLIC_TODAY` is set. Playwright `trace`, `screenshot`, and `video` are off for any non-local `E2E_BASE_URL`. Local runs keep `trace: retain-on-failure`. That split lives in `e2e/capture.mjs` and is checked by `e2e/check-capture.mjs`. See `e2e/README.md`.

On failure, each local-CI shard uploads its blob report and `e2e-report` merges them into the `playwright-report` artifact. The `e2e-remote-config` workflow runs `GREP=@readonly` against `https://example.com` (config load and capture check, not the live site) and has no `upload-artifact` step. A job that points Playwright at a remote origin must not upload artifacts.

The suite still builds app and ingest from the current tree. It does not pull the GHCR images a production deploy runs. Release images exist only after `ci` on `main` succeeds, pull-request commits are never published, and `compose.e2e.yaml` injects calibration fixtures at build time. A follow-up would run `GREP=@readonly` against `compose.release.yaml` for the last green `main`. Until then, `E2E_BASE_URL` is how a `@readonly` smoke points at prod.

**Compose digests.** Whether Dependabot bumps the digests in `deploy/compose.yaml` wasn't verified (spec §4.1). Check after its first weekly run. If it doesn't, bump them by hand every quarter. Check `deploy/compose.dev.yaml` either way: its name isn't a standard compose file name, so Dependabot may skip it. To bump a digest, take the top `Digest:` line (the multi-platform index) from

```bash
docker buildx imagetools inspect <image>:<tag>     # e.g. postgis/postgis:17-3.5, caddy:2.11
```

and replace the `@sha256:…` part. The PostGIS reference must be identical in three places, so that tests run on the production database image:

- `deploy/compose.yaml`
- `deploy/compose.dev.yaml`
- `Image` in `internal/dbtest/dbtest.go`

`go test -run TestImageMatchesCompose ./internal/dbtest/` fails if they differ, and so does CI.

**Pins Dependabot doesn't update.** Bump these by hand on the same quarterly pass, and run CI on the change:

| Pin | Where | How |
|---|---|---|
| `go 1.26.x` | `go.mod` | Dependabot bumps the `golang:1.26.x` image in `deploy/Dockerfile.app` and `deploy/Dockerfile.ingest`, but not this line. Raise it to the same version in that pull request. CI's `setup-go` installs exactly this version, so govulncheck checks the standard library CI builds with. A govulncheck failure on a standard-library fix means raising both |
| PostGIS `Image` | `internal/dbtest/dbtest.go` | Copy the reference from `deploy/compose.yaml` (above) |
| `zenko/cloudserver:latest@sha256:…` | `deploy/backup/verify.sh` and its recipe in "Verifying locally before relying on it" | `imagetools inspect zenko/cloudserver:latest`. It has no maintained version tags, so the tag stays `latest` and the digest is the pin |
| `sqlc/sqlc:<version>@sha256:…` | `scripts/sqlc.sh` | `imagetools inspect sqlc/sqlc:<version>`, then `./scripts/sqlc.sh generate` and commit any change |
| `# syntax=docker/dockerfile:1.<minor>@sha256:…` | First line of `deploy/Dockerfile.app`, `deploy/Dockerfile.ingest` and `deploy/backup/Dockerfile` | `imagetools inspect docker/dockerfile:1.<minor>`. Keep the three lines identical |
| `koalaman/shellcheck:<version>@sha256:…` | `.github/workflows/ci.yml`, `deploy` job | `imagetools inspect koalaman/shellcheck:<version>`. It's a `run:` step, so the `github-actions` updates skip it |
| `moby/buildkit:v<version>@sha256:…` | `.github/workflows/release.yml`, `driver-opts` of `setup-buildx-action` | The BuildKit that builds and pushes the release images. `imagetools inspect moby/buildkit:buildx-stable-1` shows the current stable digest; pin the `v…` tag with that digest. It's an action input, so the `github-actions` updates skip it |
| `docker/buildkit-syft-scanner:<version>@sha256:…` | `.github/workflows/release.yml`, `sbom: generator=…` | The SBOM scanner that runs inside the release build. `imagetools inspect docker/buildkit-syft-scanner:stable-1`, then pin the matching version tag with that digest |
| `rhysd/actionlint:1.7.12@sha256:b1934ee5f1c509618f2508e6eb47ee0d3520686341fec936f3b79331f9315667` | Run by hand on workflow changes: `docker run --rm -v "$PWD:/repo" -w /repo <that reference> -color` | `imagetools inspect rhysd/actionlint:<version>` |
| MinIO client (`MC_VERSION`, `MC_SHA256_*`) | `deploy/backup/Dockerfile` | As its comment says |

## Rolling back reference data

`docker compose --profile tools run --rm ingest <dataset> rollback`. It takes one transaction and the site picks it up via `NOTIFY` within seconds.

Old versions are kept. You may drop tables older than the last two versions of a dataset by hand, for example: `docker compose exec db psql -U tp -d thymeandplace -c "SELECT id, table_name, is_live FROM dataset_versions WHERE dataset = 'climate' ORDER BY id"`, then `DROP TABLE climate_cell_v<old id>;` for versions that are neither live nor one of the two most recent. (Rollback refuses to return to a dropped version.)

## When a pinned source changes

Every climate and ocean file has a reviewed sha256 pin in `internal/ingest/<dataset>.go` (the values recorded in `docs/calibration-baseline.md`). The pin belongs to the content, so it still applies when `OCEAN_SOURCE_URL`, `OCEAN_LAND_URL` or `CLIMATE_SOURCE_BASE` points somewhere else. GeoNames (places) is the only unpinned source. Plain `http://` is allowed only for a pinned source, and a redirect from HTTPS to HTTP is refused.

1. **The fetch fails and live data is untouched.** The error names the file and both hashes: `<file>: sha256 <got> does not match the pin <want>; live data unchanged; …`. No `dataset_versions` row is written and nothing is uploaded, so there is nothing to clean up.
2. **Download the file by hand and compare it with the archived copy.** Every fetched version keeps its raw files in the reference-data bucket (`BUCKET_NAME` in `ingest.env`) at `raw/<dataset>/v<live>/<file>`, where `<live>` is the live version's id (`SELECT id, source_checksum FROM dataset_versions WHERE dataset = '<dataset>' AND is_live`). Copy it out with any S3 client (for example `mc cp`), `curl -fsSL` the source URL, and compare `sha256sum` and the contents. A changed header, reordered features or a re-encoded file with the same data is still a change: the pin covers bytes, not meaning.
3. **If the change is legitimate, update the pin in a commit that says why**: what the publisher changed, the old and new sha256, and how you checked it. Keep that commit to the pin (and `docs/calibration-baseline.md`'s checksum row). If the change isn't legitimate (an unexpected mirror, a truncated or tampered file), leave the pin and find out why before fetching again.
4. **Re-run calibration, then fetch, approve and swap.** On staging (the local dev stack), fetch with the new pin, approve and run `all`, then check the calibration score as in `docs/calibration-baseline.md` ("How this was measured"): it should stay within 0.02 of the baseline, or the baseline gets updated with the reason. Then merge the new pin and deploy its release (`./deploy.sh <sha>`, "Updating the app"; before the first `deploy.sh`, `docker compose --profile tools build ingest`) and run `fetch`, `approve-license --version N` (read the summary: only the changed file should say `changed`), `approve-license --version N --yes` and `all --version N` in production. Because the bytes changed, the earlier review doesn't carry over, so the approval is needed.

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

`restore-test.sh` restores today's daily dump, or yesterday's if today's isn't there yet (`backups/daily/<YYYY-MM-DD>.dump.age`, dates in UTC as `backup.sh` names them, matched exactly; never a plaintext `.dump` or a later-dated name, since anyone who can write to the bucket can add one, see "Threat model"), into a scratch database (`thymeandplace_restore_test`) on the same Postgres server by calling `restore.sh` on it (the exact drop/create/single-transaction path a real restore takes, see "Restoring from backup"), compares row counts of `ingredient`, `ingredient_pairing`, `seafood_region`, `catalog_history`, `dataset_versions`, `admin_passkey`, `admin_backup`, `climate_cell`, `ocean_ecoregion`, and `place` against the live database, prints a table of `live=`/`restored=` counts, and drops the scratch database afterwards even if the comparison fails (it's under a `trap ... EXIT`). It exits non-zero — "fail loudly" — on any count mismatch or if neither today's nor yesterday's backup is in the bucket. Because a backup taken mid-day can legitimately trail the live `catalog_history` count if edits happened since, run it immediately after a fresh `backup` run rather than at an arbitrary time.

### Verifying locally before relying on it

The scripts were exercised end to end against a local S3-compatible container standing in for the production bucket, using the e2e stack seeded via `devseed` (which itself refuses to run without `DEVSEED_ALLOW=1` — dev/e2e only, never set in production).

`minio/minio` and `minio/mc` are both pull-access-denied on Docker Hub and Quay as of this writing (MinIO gated them behind their commercial subscription), so this recipe uses `zenko/cloudserver` — a freely-pullable S3-compatible server — as the bucket stand-in, and drives it with the `mc` already baked into the `backup` image itself (sha256-pinned in `deploy/backup/Dockerfile`) instead of pulling a second `minio/mc` image. The bucket container joins the e2e stack's network, so `backup`, `restore.sh` and `restore-test.sh` run through the same `docker compose ... run backup` commands as production, with the bucket settings passed as `-e` overrides. The commands below were run as written, in this order, in a Git Bash shell from the repo root:

```bash
cd deploy && export TP_ENV_DIR=./e2e MSYS_NO_PATHCONV=1
C="docker compose -p thymeandplace-e2e -f compose.yaml -f compose.e2e.yaml"   # never the production project
$C up -d --build --wait db app
$C --profile tools run --rm --entrypoint /app/devseed ingest

docker run -d --name tp-minio -e ENDPOINT=tp-minio -e REMOTE_MANAGEMENT_DISABLE=1 zenko/cloudserver:latest@sha256:b53e57829cf7df357323e60a19c9f98d2218f1b7ccb1d7cea5761a5a227a9ee3   # the pin in deploy/backup/verify.sh
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

`./deploy/backup/verify.sh` runs this recipe with every check (encrypted upload to both recipients, restore-test, a disaster restore over the live database with the stand-in recovery key in exactly the `-v …:/run/secrets/recovery.agekey:ro -e AGE_IDENTITY=…` form of "Disaster restore with the recovery key", refusals without the right key or with a changed byte, restore-test ignoring planted later-dated objects, a plaintext dump refused without `RESTORE_ALLOW_PLAINTEXT=1` and restored with it, bad recipients, a failed dump and a zero-byte dump) and runs in CI on `main`. It prints one `verify: ok:` line per check and `verify: all backup checks passed` at the end. Like the recipe, it uses the `thymeandplace-e2e` project, so don't run it while another e2e stack is up on the same machine.

**Windows / Git Bash note:** Git Bash rewrites a leading `/` in arguments as a Windows path, which breaks `--entrypoint /app/devseed` and similar. `MSYS_NO_PATHCONV=1` (exported above) turns that off; it's a no-op on Linux.

## Restoring from backup

A restore **replaces the whole database** with the dump. `restore.sh <key> [target-url]` (the `restore` service's entrypoint; the target defaults to its own `DATABASE_URL` from `restore.env`, i.e. the live database, as the bootstrap superuser):

1. refuses unless `RESTORE_CONFIRM` is set to the exact database name in the target URL (the last path segment before any `?params`), before anything is downloaded or touched — this stops a stale or copy-pasted URL from overwriting the wrong database;
2. for an encrypted key (`*.age`), refuses before downloading unless `AGE_IDENTITY` names a readable identity file: "encrypted backup; set AGE_IDENTITY to your recovery or restore-test key". A plaintext key is refused unless `RESTORE_ALLOW_PLAINTEXT=1` is set (see below);
3. downloads the dump, decrypts it if it's encrypted, and checks it is non-empty and a readable `pg_dump` archive, so a missing, empty or corrupt backup exits non-zero with the database untouched. `age` authenticates its ciphertext, so a wrong key or a single changed byte also refuses here ("could not be decrypted with AGE_IDENTITY"). It doesn't prove who encrypted the file ("Backup encryption" › "Threat model");
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

Plaintext `.dump` keys from before encryption (`backups/daily/<date>.dump`) need no identity, but `restore.sh` refuses them unless you add `-e RESTORE_ALLOW_PLAINTEXT=1` to the command. Add it only for a dump you know your own backup job wrote: the restore runs the dump's SQL as the database superuser, and nothing in a plaintext object shows who uploaded it ("Threat model"). The prune removes them within 28 days of the switch, but with versioning on (the minimum level) each one stays as a hidden version for 35 more days ("Backup encryption" › "Rollout" step 5).

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
2. **Restore-test key**, on the VPS, from a root shell in `/opt/thymeandplace/deploy`. This runs `age-keygen` from the backup image, so the host needs no `age`, but the image has to be one with `age` in it. Always build it first, as its own command (`git pull` first on a VPS that is already running). On a VPS whose backup image predates encryption, skipping the build makes the keygen fail with "executable file not found". After the first `deploy.sh`, the deployed release's backup image is used instead (it has `age`), and the `build` line prints `No services to build`. `age-keygen -o` writes the file itself, mode `600`, into `/etc/thymeandplace` through a bind mount, and refuses to overwrite an existing file. Don't redirect its standard output into the file instead: Docker's `json-file` log driver would also write the private key to the container's log on disk.
   ```bash
   docker compose --profile tools build backup
   docker compose --profile tools run --rm --no-deps -v /etc/thymeandplace:/out --entrypoint age-keygen backup -o /out/restore-test.agekey
   ```
   It prints its public key; `grep 'public key' /etc/thymeandplace/restore-test.agekey` shows it again. Check the file:
   - `grep -c '^AGE-SECRET-KEY-1' /etc/thymeandplace/restore-test.agekey` prints `1`: one private key.
   - `grep -vc -e '^#' -e '^AGE-SECRET-KEY-1' /etc/thymeandplace/restore-test.agekey` prints `0`: nothing but comments and the key.
   - `stat -c '%a %U' /etc/thymeandplace/restore-test.agekey` prints `600 root`.

   If a check fails, delete the file and run both commands again.
3. **Recipients.** Put both public keys, space-separated, in `/etc/thymeandplace/backup.env`: `BACKUP_AGE_RECIPIENTS=age1<recovery> age1<restore-test>`. Only public keys go there. Keep the public keys confidential too: anyone who has one can make a `.dump.age` that a restore accepts ("Threat model"). They belong only in `backup.env`, the restore-test key file and your password manager, never in the repo, an issue or a chat. An empty value, or any entry that isn't `age1…` (a pasted `AGE-SECRET-KEY-…`, say), makes `backup` exit 2 and name the variable before it dumps anything. `backup.env` is read on every run, so a change applies from the next backup.

### Threat model

- **Bucket-only compromise** (leaked bucket keys, or an incident at the storage provider): reveals nothing. Every object is ciphertext, and neither private key is in the bucket. The exception is plaintext `.dump` objects from before the switch. The prune removes them within 28 days, but at the minimum level of "Backup deletion protection" that only hides them: each stays readable as a hidden version for 35 more days, about 63 days after the switch in all, to anyone with a bucket key. "Rollout" step 5 shows how to delete them sooner.
- **Bucket write access** (the same leaked keys: at the minimum level of "Backup deletion protection" both keys can upload and delete): the attacker can't read the backups but can add or replace objects. A restore runs the dump's SQL as the database superuser on the production server, so a planted dump that got restored would run the attacker's SQL there. Three things keep that from happening:
  - `restore.sh` refuses a plaintext `.dump` key unless `RESTORE_ALLOW_PLAINTEXT=1` is set ("Restoring from backup"), and the monthly restore test never sets it.
  - `restore-test.sh` restores only today's or yesterday's `<YYYY-MM-DD>.dump.age`, by exact name, so a planted `backups/daily/2099-12-31.dump` or a later-dated `.dump.age` is never picked.
  - A `.dump.age` decrypts only if it was encrypted to one of your keys, which needs that key's public half. `age` checks that the file wasn't changed, not who made it, so the public keys are confidential: they live only in `backup.env`, the restore-test key file and your password manager. If one leaks, replace that key ("Key rotation", "A lost key").
- **Host compromise:** the attacker can read the restore-test key and so decrypt the backup history. The live database is exposed in that case anyway, so the extra loss is older rows and deleted history. The recovery key stays off the host, and "Backup deletion protection" keeps the history restorable even if the attacker deletes it with the host's bucket key.

### Key rotation

Add the new recipient to `BACKUP_AGE_RECIPIENTS`, next to the old one, and run a backup by hand. Wait a month (29 days: the prune keeps weekly copies 28 days and the preferred level's lifecycle rule 29), until every kept backup is encrypted to the new key too, then remove the old recipient. Backups made before the removal stay decryptable with the old key until they age out, so keep the old private key until then. Copies that were pruned or expired before then are not gone yet: versioning keeps them hidden for 35 more days, and only the old key decrypts them. If you might need to undelete one, keep the old private key for 64 days after adding the new recipient.

- **Recovery key:** generate the new one as in "Keys" step 1 and store it the same way. Destroy the old copies only after the 29 days, or the 64 if undelete matters.
- **Restore-test key:** an identity file can hold several keys. Generate the new one into a second file with the "Keys" step 2 command, `-o /out/restore-test.agekey.new` in place of `-o /out/restore-test.agekey`, then append it: `cat /etc/thymeandplace/restore-test.agekey.new >> /etc/thymeandplace/restore-test.agekey && shred -u /etc/thymeandplace/restore-test.agekey.new` (the `grep -c '^AGE-SECRET-KEY-1'` check then prints `2`). After the 29 days, or the 64, delete the old key's three lines (`# created`, `# public key` and its `AGE-SECRET-KEY-1…` line) from the file.
- **A lost key:** list a new key in its place straight away. Backups made before then can only be decrypted with the other key until they age out.

### Rollout

Once, when the encrypting backup image first goes to the VPS (spec §7):

1. Build the image: `git pull && docker compose --profile tools build backup`. After the first `deploy.sh`, deploy the release that adds encryption instead (`./deploy.sh <sha>`); on a VPS moving to `deploy.sh` with this release, follow "First update of a running VPS to `deploy.sh`" in "Updating the app", which puts these steps in order with the GHCR login and monitoring. `backup` and `restore` share the image. The old image has no `age-keygen`, so this comes before the keys. Do steps 1 to 3 in one sitting, well away from the 03:15 nightly run: until step 2 sets the recipients, the new image's `backup` exits 2 naming `BACKUP_AGE_RECIPIENTS` before it dumps anything, so a run in between fails loudly and uploads nothing.
2. Generate both keys and set `BACKUP_AGE_RECIPIENTS` ("Keys" above).
3. Before the next nightly run, run `docker compose --profile tools run --rm backup` (it prints `backup ok: backups/daily/<date>.dump.age`), then `docker compose --profile tools run --rm --entrypoint restore-test.sh restore` (it prints `restore-test ok (<date>.dump.age)`). Both must succeed.
4. Turn on versioning at the provider, at least at the minimum level in "Backup deletion protection". Then consider the preferred level.
5. After 28 days, `mc ls --recursive "b/$BUCKET_NAME/backups/"` ("Backup deletion protection" shows how to run it) lists no plaintext `.dump` objects, only `.dump.age`. That listing shows only current objects. At the minimum level the pruned plaintext dumps remain as hidden versions for 35 more days, readable with the bucket key; `mc ls --versions --recursive "b/$BUCKET_NAME/backups/" | grep '\.dump$'` lists them, and it is empty once they are gone. To delete them sooner, any time after step 3 passed, run this in the same `mc` shell (the key in place at the minimum level can delete), or delete every version of each `.dump` file in the B2 console (Browse Files, show file versions):
   ```sh
   mc ls --versions --recursive "b/$BUCKET_NAME/backups/" | awk '{print $NF}' | grep '\.dump$' | sort -u |
     while read -r k; do mc rm --versions --force "b/$BUCKET_NAME/backups/$k"; done
   ```
   This removes only keys ending in `.dump`; the `.dump.age` backups and their versions stay. Then the `--versions` listing above must be empty. `restore.sh` keeps accepting plaintext dumps, but only with `RESTORE_ALLOW_PLAINTEXT=1` (spec §8 question 2; "Restoring from backup").

**Rollback:** fix forward. Every release image encrypts, and `deploy.sh` deploys all of a release's images together, so redeploying an older release can't bring plaintext backups back; fix the problem in a new release and deploy that. Until then a failing nightly run uploads nothing and the `backup` check alerts, and the `.dump.age` backups already uploaded stay restorable with any release's `restore` and either key. (Before the first `deploy.sh`, with no `deploy/.env`, the old image can be rebuilt from source: `git checkout <commit before encryption> && docker compose --profile tools build backup && git checkout main`. It uploads plaintext `.dump` files again and, since `restore` shares it, can't restore a `.dump.age`; run `docker compose --profile tools build backup` on `main` again before restoring one.)

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
- **Rotating a role's password:** `docker compose exec db psql -U tp -d thymeandplace -c "ALTER ROLE tp_app PASSWORD '<new hex password>'"`, then update that role's password in `db.env` and in the one or two files that connect as it (see "First deploy", step 3), and recreate the services that stay running: for `tp_app`, recreate `app` with the release overlay ("Recreating `app` after a settings change" in "Updating the app"). The one-shot services (`migrate`, `backup`, `restore`, `ingest`) read their env file on their next run. For the bootstrap superuser, change `POSTGRES_PASSWORD` in `db.env` and `restore.env` the same way (the postgres image only reads it on an empty volume).
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

## Schema migrations

Goose SQL files live in `migrations/`. Never edit a file that has already shipped. Add a new numbered file.

`migrations/checksums` pins the sha256 of every `*.sql` file. `go test ./migrations` fails if a pinned file changes, if a new file has no pin, or if a pin names a missing file. After you add a migration, append its line:

```bash
sha256sum migrations/000NN_name.sql >> migrations/checksums
```

An Up-section `DROP`, `RENAME`, column `TYPE` change, or `SET NOT NULL` needs a `-- contract:` marker on that line or the previous non-empty line. The marker says why the change is safe. Down-section drops do not need one. Adding a nullable column and `DROP NOT NULL` do not need a marker. Example:

```sql
-- contract: preview_cache is unused after 00012
DROP TABLE preview_cache;
```

Tests apply `deploy/db/roles.sql` (the same file `20-roles.sh` runs on a fresh volume) instead of a separate copy of the `CREATE ROLE` statements.

## Reverse proxy trust and logging

- The app trusts `X-Forwarded-*` headers (`TRUST_PROXY_HEADERS=true`, set in `deploy/compose.yaml`) from **exactly one proxy hop, Caddy**, and uses the **rightmost** `X-Forwarded-For` entry as the client IP (`web.ClientIP`). Caddy has no `trusted_proxies`, so it discards any `X-Forwarded-For` a client sends and writes the address it saw. If the rightmost entry isn't a valid IP, the app uses Caddy's own address, which is one shared bucket, so a garbled header can't earn a fresh rate-limit budget. **Before putting a CDN or any other proxy in front of Caddy, change `web.ClientIP` first.** With `trusted_proxies` set, the rightmost entry becomes the CDN edge, and every visitor behind it would share one public rate-limit bucket and one admin sign-in budget. The recommended change is to set both global server options `trusted_proxies static <CDN ranges>` and `trusted_proxies_strict`, have Caddy send `header_up X-Client-IP {client_ip}`, and have the app read only that header. `trusted_proxies_strict` is required: without it Caddy parses `X-Forwarded-For` left to right, so `{client_ip}` becomes the leftmost entry, which the client controls (spec `2026-09-27-admin-abuse-resistance-design.md` §8 question 3). **Never publish the app's own port** (`8080`) alongside Caddy's 80/443. If a client could reach the app directly, it could forge those headers and spoof its source IP past the admin sign-in budget. `deploy/compose.yaml` intentionally has no `ports:` entry under `app`; only `caddy` publishes ports.
- **Security headers are set by the app, not Caddy.** `web.SecurityHeaders` (`internal/web/middleware.go`, applied in `web.Common`) puts a strict Content-Security-Policy (self only, no inline script or style), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: same-origin`, a `Permissions-Policy` that allows only geolocation for this origin, and `Cross-Origin-Opener-Policy`/`Cross-Origin-Resource-Policy: same-origin` on every response, including static files, 404s, 429s and panic pages. `/admin` replaces the CSP (it also allows `data:` images) and adds `Cache-Control: no-store`, `X-Robots-Tag: noindex` and `Referrer-Policy: no-referrer` (`internal/admin/admin.go`). Both HTMX configs turn off `allowEval` and `allowScriptTags` and set `selfRequestsOnly`. Public pages have one `#announce` live region (`role="status"`); after an HTMX swap, `static/app.js` copies the fragment's `data-announce` into it and sets `aria-busy` on the swap target for the request. The site `h1` is the fixed copy `CopySiteHeading` ("What's in season"); the place name is the `#results` `h2` so a swap cannot leave a stale heading. Search focus and announcements live in `static/app.js` and `static/app.css` (no inline script or style). Caddy sets only HSTS and drops `Server`. Adding a third-party script, font, image host or embed means changing `SecurityHeaders` first. Check after a deploy with `curl -sI https://thymeand.place/ | grep -i -e content-security -e x-frame -e referrer -e permissions`, and look for CSP errors in the browser console. Playwright `watchCSP` records `securitypolicyviolation` (`directive`, `blockedURI`, `sourceFile`, `sample`, `lineNumber`) plus CSP console text as a backup. The auto `cspGuard` fails on those events. Home baselines pause the watch only around each `toHaveScreenshot` call, not `emulateMedia`, and assert the event list is empty after the pause. WebKit injects `<style>body {}</style>` on every screenshot, including with `caret: 'initial'` and `animations: 'allow'`. `e2e/tests/csp.spec.ts` loads a fixture with an inline `<style>` and a `style` attribute and asserts the watch still reports both.
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

**Public DB gate.** Public database work (a `/season` or `/ingredient` cache miss, a `/places` search, and the `/season` heading label) shares three quarters of the server pool: 12 of 16 slots. The rest stays free for `/admin`, the drift check, catalog reloads and Apply, so a public spike can't lock the owner out. A request that can't get a slot within 250 ms gets the retry panel with `503` and `Retry-After: 5` straight away instead of queueing until the 2 s deadline. The heading label ("near X") is looked up on every supported `/season` request, cache hits included, so warm `/season` traffic takes a slot too; when none is free the heading falls back to coordinates and the page still loads with `200`. Other cache hits, `/healthz`, `/readyz`, static files and `/admin` never wait on the gate. `/ingredient` and `/season` misses share a cache of each cell's climate normals and ecoregions per data version (20,000 cells), so repeat clicks cost no queries.

**Changing the pool size.** Set `DB_MAX_CONNS` in the app env file and recreate `app` with the release overlay ("Recreating `app` after a settings change" in "Updating the app"); the gate follows at three quarters. On a VPS with less than 4 GB RAM use 10 (7 public slots). Re-run the load test afterwards.

**The `public DB gate full` log line.** When requests are shed, the app logs `INFO "public DB gate full"` at most once a minute, with `shed` (lookups the gate turned away since the last line: 503s plus `/season` headings that fell back to coordinates) and `slots`. Sustained 503s with this line mean the site is at capacity, not that the database is down: `/readyz` still says `ready`. The line without 503s means only headings fell back: the gate is busy but pages still load. Check `SELECT count(*), usename, state FROM pg_stat_activity GROUP BY 2, 3`; the app should hold between 2 and 17 connections.

**Load test.** `deploy/loadtest/` has a k6 script and a README. Run it on the production VPS before launch and after any change to `DB_MAX_CONNS`, the VPS size or the reference data, and record the results and the ocean query timings in `docs/capacity-baseline.md`. Warm `/season` requests are not DB-free: each runs one indexed nearest-place query for its heading, so the warm p95 includes it.

## Health and logs

- `docker compose ps` shows health; `curl -s https://thymeand.place/readyz` returns `ready` when the DB is reachable and the catalog is loaded. The database part of `/readyz` is shared: one ping answers every request for 1 s, so `/readyz` can lag a database outage or recovery by up to a second. Sustained 503s while `/readyz` is `ready` and the log shows `public DB gate full` mean load shedding (see "Capacity"), not an outage.
- Logs are JSON on stdout: `docker compose logs -f app`. Every service uses Docker's `json-file` driver with rotation (`max-size: 10m`, `max-file: 20`, `compress: true`, set once in `deploy/compose.yaml`'s `x-logging` anchor), so each container keeps 20 files of up to 10 MB of log each, the rotated ones compressed (at most 200 MB if nothing compressed); older lines are gone after rotation. `docker compose logs` reads the compressed files too. A container keeps the log options it was created with until Compose recreates it ("Recreating `app` after a settings change"); `docker inspect --format '{{.HostConfig.LogConfig}}' $(docker compose ps -q app)` shows what it has. Healthy `/healthz` and `/readyz` lines (2xx) aren't logged, so the probes don't use up the budget; failing probes are. Public requests record `cell_id`, never coordinates. Successful backup logins log at `WARN`, and so do the backup lockout (`backup login locked`) and calibration drops of more than 0.02. Each of these also fails a check ("Monitoring and alerts").
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

Use each check's own UUID ping URL (`https://hc-ping.com/<uuid>`), not the project ping key + slug form, where one leaked key exposes every check. Anyone holding a ping URL can flip its check, so the URLs live only in the root-only env files above, never in the repo or `compose.yaml`, and neither the app nor the scripts print them. The app reads its three at startup, so after changing them recreate `app` with the release overlay ("Recreating `app` after a settings change" in "Updating the app"); `backup` and `restore-test` read theirs on each run. If one leaks, replace the check with a new one and update its env file.

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
4. Recreate the running services (from the deployed release, through `deploy/.env`; see "Updating the app"):
   ```bash
   docker compose up -d
   ```
   Compose recreates the containers whose settings changed, so they pick up the new env and log options. `app` and `admin-auth` go green within a few minutes. `drift` goes green at the next scheduled run, 24 h after `SELECT ran_at FROM calibration_run ORDER BY id DESC LIMIT 1`; a deploy doesn't trigger one.
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
