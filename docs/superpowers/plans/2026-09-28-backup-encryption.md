# Backup Encryption and Retention Protection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Encrypt every nightly dump with `age` before it leaves the host, so the bucket only ever holds ciphertext. Full recovery needs an off-host key. The monthly restore test keeps running unattended, and a host compromise can't silently delete backup history.

**Architecture:** `backup.sh` dumps to a file, verifies it with `pg_restore --list`, encrypts it to two X25519 recipients (an off-host **recovery** key and an on-host **restore-test** key), deletes the plaintext, then uploads `*.dump.age`. `restore.sh` decrypts `*.age` objects with `AGE_IDENTITY` before its existing checks, and still restores legacy plaintext `*.dump`. The restore-test key is bind-mounted read-only into the `restore` service only. Deletion protection is bucket-side (versioning, and optionally lifecycle rules plus a no-delete writer key), documented rather than coded. `BACKUP_PRUNE=lifecycle` skips the script's own prune. A new `deploy/backup/verify.sh` automates the local recipe, and a `backup` CI job runs it on `main`.

**Tech Stack:** POSIX `sh` (dash), `age` 1.x (Debian bookworm package), `pg_dump`/`pg_restore` 17, MinIO `mc`, Docker Compose, `zenko/cloudserver` for tests, bash for `verify.sh`.

**Spec:** `docs/superpowers/specs/2026-09-27-backup-encryption-design.md`. Issue [#12](https://github.com/Muddl/thymeandplace/issues/12).

**Depends on:** `docs/superpowers/plans/2026-09-28-observability-and-alerting.md` **Task 9**. That task rewrites `backup.sh` with the `step` variable, the single `on_exit` trap and heartbeat pings, which this plan extends (spec §4.2 builds "on top of the observability design's `EXIT` trap and step names"). Execute that task first. If it hasn't landed, stop and do it first rather than inventing a different trap.

**Spec drift at HEAD:** The least-privilege work (`620cf2b`) already split `backup` (as `tp_backup`) from a separate `restore` service (as the superuser), each with its own env file. So the identity mount goes on `restore`, and the e2e files are `deploy/e2e/backup.env` and `deploy/e2e/restore-test.agekey`.

## Global Constraints

- The bucket only ever receives ciphertext from the new image: `backups/daily/<YYYY-MM-DD>.dump.age` and, on Sundays, `backups/weekly/<GGGG-Www>.dump.age`.
- Order: dump → verify (non-empty and `pg_restore --list`) → encrypt → delete plaintext → upload. A bad dump is never uploaded.
- `BACKUP_AGE_RECIPIENTS` holds one or more space-separated `age1…` public keys. It is unset, empty, or has a non-`age1` entry → `exit 2`, naming the variable, before anything is dumped or uploaded.
- The recovery private key never goes on the VPS, except temporarily during a disaster restore.
- The restore-test key is `/etc/thymeandplace/restore-test.agekey`, mode 600, root-owned, mounted read-only at `/run/secrets/restore-test.agekey` into `restore` only. `AGE_IDENTITY` defaults to that path there.
- `restore.sh` with an `.age` key and no readable `AGE_IDENTITY` refuses **before downloading**. A decryption failure refuses before the database is touched. `age` authenticates its ciphertext, so tampering is caught.
- Legacy plaintext `*.dump` keys still restore (spec §8 question 2, recommended: keep).
- Retention is unchanged: 7 daily and 4 weekly copies. `BACKUP_PRUNE=lifecycle` skips the prune only when bucket lifecycle rules do it.
- The e2e key pair is committed, clearly marked test-only, and never used in production.
- Every script must still pass `sh -n`, which CI runs.

## Review Focus

1. **A recipients value with stray whitespace or a newline** (copied from `age-keygen` output). It must split into keys, not fail as one garbled key. It must also refuse a pasted **private** key (`AGE-SECRET-KEY-…`), which would be a serious leak. Task 2's recipe covers both.
2. **The weekly copy on a Sunday.** It must upload the same `.age` file, never the deleted plaintext. Task 2 reorders the steps so the plaintext is gone before either upload. `verify.sh` (Task 5) forces the Sunday branch with the test-only `BACKUP_FORCE_WEEKLY=1` and checks for a `.dump.age` weekly object.
3. **`restore-test.sh` during the 28-day transition,** when `backups/daily/` holds both `.dump` and `.dump.age`. It must pick the newest by date and restore either kind. Task 3 handles the selection, and `verify.sh` restores both kinds.
4. **The identity file missing on the host.** Compose must fail loudly, not bind-mount an empty directory that `age` then reports as a confusing error. Task 4 uses `create_host_path: false` and checks the error by hand.
5. **A dump that can't be taken at all** (wrong credentials, database down). The run must exit non-zero at `dump` and upload nothing. `verify.sh` check 8 covers this.

---

### Task 1: `age` in the backup image, and the e2e test key pair

**Files:**
- Modify: `deploy/backup/Dockerfile`
- Create: `deploy/e2e/restore-test.agekey` (test-only private key)
- Modify: `deploy/e2e/backup.env` (`BACKUP_AGE_RECIPIENTS`)

**Interfaces:**
- Produces: `age` and `age-keygen` on `PATH` in `thymeandplace-backup:latest`, plus the committed e2e identity and its public recipient.

- [ ] **Step 1: Install `age` and check it at build time**

In `deploy/backup/Dockerfile`, change `apt-get install -y --no-install-recommends ca-certificates curl` to `apt-get install -y --no-install-recommends ca-certificates curl age`. After the `mc` `RUN`, add:

```dockerfile
# age encrypts dumps before upload (docs/operations.md, "Backup encryption").
# From Debian's package; fails the build if missing.
RUN age --version && age-keygen --version
```

If `age` isn't in bookworm's repositories at build time, fall back to the pinned release tarball the same way `mc` is pinned: an `AGE_VERSION` and per-arch `AGE_SHA256_*` build args, `curl -fsSL`, and `sha256sum -c`.

- [ ] **Step 2: Build it**

Run: `docker build -t thymeandplace-backup:latest deploy/backup`
Expected: the build succeeds and prints an `age` version such as `1.1.1`.

- [ ] **Step 3: Generate and commit the e2e key pair**

Run: `docker run --rm --entrypoint age-keygen thymeandplace-backup:latest > deploy/e2e/restore-test.agekey`

Prepend these two lines to the file (keep `age-keygen`'s own `# created:` and `# public key:` comment lines):

```
# END-TO-END TESTS ONLY. A throwaway age identity for the local backup recipe
# and the CI backup job. Never used in production; not a secret.
```

Copy the `age1…` value from its `# public key:` line and append it to `deploy/e2e/backup.env`:

```
# Test-only recipient; its identity is deploy/e2e/restore-test.agekey.
BACKUP_AGE_RECIPIENTS=age1<the public key>
```

- [ ] **Step 4: Commit**

```bash
git add deploy/backup/Dockerfile deploy/e2e/restore-test.agekey deploy/e2e/backup.env
git commit -m "backup: install age; add a test-only e2e key pair (#12)"
```

### Task 2: Encrypt in `backup.sh`

**Files:**
- Modify: `deploy/backup/backup.sh` (the post-observability version)

**Interfaces:**
- Consumes: `hc`, `step`, `on_exit` and `prune_err` from the observability plan's `backup.sh`.
- Produces:
  - Steps: `env`, `alias`, `dump`, `verify`, `encrypt`, `upload`, `weekly`, `prune`.
  - Objects: `backups/daily/<day>.dump.age` and `backups/weekly/<week>.dump.age`.
  - Env: `BACKUP_AGE_RECIPIENTS` (required), `BACKUP_PRUNE` (`lifecycle` skips the prune), `BACKUP_FORCE_WEEKLY=1` (test-only: take the Sunday branch).
  - Success body: `backup ok: backups/daily/<day>.dump.age`.

- [ ] **Step 1: Apply the changes**

Starting from the observability plan's `backup.sh`:

1. Change the header comment to: `# Nightly pg_dump, encrypted with age, to the bucket. Keeps 7 daily and 4 weekly copies.`
2. In `on_exit`, replace `[ -n "$file" ] && rm -f "$file"` with `[ -n "$file" ] && rm -f "$file" "$file.age"`, and change the success ping body to `"backup ok: backups/daily/$day.dump.age"`.
3. After the required-variables loop and before `hc /start`, add:

```sh
# Every entry must be an age public key. A pasted private key
# (AGE-SECRET-KEY-…) or any other text is refused before anything runs.
recipients=
for r in ${BACKUP_AGE_RECIPIENTS:-}; do
  case "$r" in
    age1*) recipients="$recipients -r $r" ;;
    *) echo "backup: BACKUP_AGE_RECIPIENTS must list only age1… public keys" >&2; exit 2 ;;
  esac
done
[ -n "$recipients" ] || { echo "backup: BACKUP_AGE_RECIPIENTS is not set" >&2; exit 2; }
```

4. Replace everything from `step=dump` up to (not including) the prune comment with:

```sh
step=dump
file="/tmp/thymeandplace-$day.dump"
pg_dump --format=custom --no-owner --dbname="$DATABASE_URL" --file="$file"

# Check the archive before it leaves the host, as restore.sh would: a bad
# dump must never be uploaded as a success.
step=verify
[ -s "$file" ] || { echo "backup: dump is empty" >&2; exit 1; }
pg_restore --list "$file" >/dev/null || { echo "backup: dump is not a readable archive" >&2; exit 1; }

step=encrypt
# shellcheck disable=SC2086 # $recipients is a list of -r flags on purpose
age -e $recipients -o "$file.age" "$file"
rm -f "$file"

step=upload
mc cp --quiet "$file.age" "store/$BUCKET_NAME/backups/daily/$day.dump.age"
if [ "$(date -u +%u)" = "7" ] || [ "${BACKUP_FORCE_WEEKLY:-}" = "1" ]; then
  step=weekly
  mc cp --quiet "$file.age" "store/$BUCKET_NAME/backups/weekly/$(date -u +%G-W%V).dump.age"
fi
```

5. Wrap the two prune `if` blocks in:

```sh
# With BACKUP_PRUNE=lifecycle, bucket lifecycle rules expire old copies and
# this key may not be allowed to delete (docs/operations.md, "Backup
# deletion protection").
if [ "${BACKUP_PRUNE:-}" != "lifecycle" ]; then
  step=prune
  ...the two existing if blocks...
fi
```

6. Change the final line to `echo "backup ok: backups/daily/$day.dump.age"`.

- [ ] **Step 2: Parse-check**

Run: `sh -n deploy/backup/backup.sh && docker build -t thymeandplace-backup:latest deploy/backup`
Expected: no output from `sh -n`, and the build succeeds.

- [ ] **Step 3: Smoke-run the recipe's backup step by hand**

Using the "Verifying locally" setup in `docs/operations.md` (e2e stack plus `zenko/cloudserver`), run `$C --profile tools run --rm $BK backup`.
Expected: `backup ok: backups/daily/<today>.dump.age`. `mc cat m/tp-backups/backups/daily/<today>.dump.age | head -c 21` prints `age-encryption.org/v1` and not `PGDMP`. Then run it with `-e BACKUP_AGE_RECIPIENTS="AGE-SECRET-KEY-1XYZ"`; expected: exit 2 and nothing new in the bucket. Task 5 automates all of this.

- [ ] **Step 4: Commit**

```bash
git add deploy/backup/backup.sh
git commit -m "backup: verify, then encrypt dumps with age before upload (#12)"
```

### Task 3: Decrypt in `restore.sh`; `restore-test.sh` takes either kind

**Files:**
- Modify: `deploy/backup/restore.sh`
- Modify: `deploy/backup/restore-test.sh`

**Interfaces:**
- Consumes: `AGE_IDENTITY` (a path to an age identity file).
- Produces: `restore.sh <key.dump.age|key.dump> [target]` behaviour as described in the Global Constraints.

- [ ] **Step 1: `restore.sh`**

1. Extend the usage comment: `# Encrypted keys (*.age) need AGE_IDENTITY: the path to the recovery or restore-test age identity.`
2. After the `RESTORE_CONFIRM` check, before the `: "${BUCKET_ENDPOINT:?}"...` line, add:

```sh
# An encrypted backup needs an identity: refuse before downloading anything.
encrypted=false
case "$key" in
  *.age)
    encrypted=true
    [ -n "${AGE_IDENTITY:-}" ] || { echo "restore: encrypted backup; set AGE_IDENTITY to your recovery or restore-test key; database untouched" >&2; exit 2; }
    [ -r "$AGE_IDENTITY" ] || { echo "restore: AGE_IDENTITY ($AGE_IDENTITY) is not a readable file; database untouched" >&2; exit 2; }
    ;;
esac
```

3. Replace `trap 'rm -f "$tmp"' EXIT` with `trap 'rm -f "$tmp" "$tmp.dump"' EXIT`.
4. Right after `mc cp --quiet "store/$BUCKET_NAME/$key" "$tmp"`, add:

```sh
# age authenticates its ciphertext: a wrong key or a changed byte fails
# here, before the database is touched.
if [ "$encrypted" = true ]; then
  age -d -i "$AGE_IDENTITY" -o "$tmp.dump" "$tmp" || { echo "restore: $key could not be decrypted with AGE_IDENTITY (wrong key or damaged file); database untouched" >&2; exit 1; }
  mv "$tmp.dump" "$tmp"
fi
```

The existing `-s`, `pg_restore --list` and full-read checks then run on the plaintext unchanged.

- [ ] **Step 2: `restore-test.sh`**

Replace the `latest=$(mc ls ... | tail -n 1)` line with:

```sh
# During the switch to encryption both kinds exist; names sort by date, and
# on the same date .dump.age sorts after .dump.
latest=$(mc ls "store/$BUCKET_NAME/backups/daily/" | awk '{print $NF}' | grep -E '\.dump(\.age)?$' | sort | tail -n 1)
```

`AGE_IDENTITY` passes through the environment to `restore.sh`, so nothing else changes.

- [ ] **Step 3: Parse-check and rebuild**

Run: `sh -n deploy/backup/restore.sh && sh -n deploy/backup/restore-test.sh && docker build -t thymeandplace-backup:latest deploy/backup`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add deploy/backup/restore.sh deploy/backup/restore-test.sh
git commit -m "backup: restore decrypts .age dumps with AGE_IDENTITY, refusing before any change (#12)"
```

### Task 4: Mount the restore-test key; env templates

**Files:**
- Modify: `deploy/compose.yaml` (the `restore` service)
- Modify: `deploy/env/backup.env.example`, `deploy/env/restore.env.example`
- Modify: `.github/workflows/ci.yml` (`deploy` job: a placeholder identity for `config -q`)

**Interfaces:**
- Produces: in `restore`, `AGE_IDENTITY=/run/secrets/restore-test.agekey`, bind-mounted read-only from `${TP_ENV_DIR:-/etc/thymeandplace}/restore-test.agekey`.

- [ ] **Step 1: Compose**

Add to the `restore` service in `deploy/compose.yaml`:

```yaml
    environment:
      # The on-host restore-test key. A disaster restore overrides this with
      # the recovery key (docs/operations.md, "Restoring from backup").
      AGE_IDENTITY: /run/secrets/restore-test.agekey
    volumes:
      # Only this service gets it. A missing file fails loudly instead of
      # Docker creating an empty directory in its place.
      - type: bind
        source: ${TP_ENV_DIR:-/etc/thymeandplace}/restore-test.agekey
        target: /run/secrets/restore-test.agekey
        read_only: true
        bind:
          create_host_path: false
```

- [ ] **Step 2: Env templates**

Append to `deploy/env/backup.env.example`:

```
# age public keys (space-separated) every dump is encrypted to: the off-host
# recovery key and the on-host restore-test key (docs/operations.md,
# "Backup encryption"). Public keys only, never an AGE-SECRET-KEY.
BACKUP_AGE_RECIPIENTS=age1RECOVERY age1RESTORETEST
# Set to "lifecycle" only when bucket lifecycle rules expire old backups and
# this key can't delete (docs/operations.md, "Backup deletion protection").
#BACKUP_PRUNE=lifecycle
```

Append to `deploy/env/restore.env.example`:

```
# AGE_IDENTITY is set by compose.yaml to the restore-test key
# (/etc/thymeandplace/restore-test.agekey, mounted read-only). For a
# disaster restore, pass the recovery key with -v and -e instead.
```

- [ ] **Step 3: CI placeholder**

In `.github/workflows/ci.yml`, in the `deploy` job's "Compose files are valid" step, after the `for f in deploy/env/*.env.example` loop, add `touch "$env_dir/restore-test.agekey"`.

- [ ] **Step 4: Validate**

Run the two `docker compose ... config -q` commands from the CI step locally, with a temp env dir containing the copied templates and an empty `restore-test.agekey`.
Expected: exit 0. Then, with the temp file removed, run `docker compose -p tp-check -f deploy/compose.yaml --profile tools run --rm --entrypoint true restore`. Expected: a clear error that the bind source doesn't exist. Stop any stray containers afterwards with `docker compose -p tp-check down`.

- [ ] **Step 5: Commit**

```bash
git add deploy/compose.yaml deploy/env/ .github/workflows/ci.yml
git commit -m "deploy: mount the restore-test age key into restore only (#12)"
```

### Task 5: Automated backup recipe and CI job

**Files:**
- Create: `deploy/backup/verify.sh`
- Modify: `.github/workflows/ci.yml` (new `backup` job on `main`)

**Interfaces:**
- Consumes: Tasks 1-4.
- Produces: `deploy/backup/verify.sh`, which exits 0 only when every check below passes.

- [ ] **Step 1: Write the script**

Create `deploy/backup/verify.sh` (mode 755):

```bash
#!/usr/bin/env bash
# The local backup recipe (docs/operations.md, "Verifying locally") with
# assertions, on the e2e stack and a throwaway S3-compatible bucket. Never
# touches production: the e2e project name keeps every volume apart.
set -euo pipefail
cd "$(dirname "$0")/.." # deploy/
export TP_ENV_DIR=./e2e MSYS_NO_PATHCONV=1
C="docker compose -p thymeandplace-e2e -f compose.yaml -f compose.e2e.yaml"
S3=tp-verify-s3
BK=(-e BUCKET_ENDPOINT=$S3:8000 -e BUCKET_NAME=tp-backups -e BUCKET_ACCESS_KEY=accessKey1 -e BUCKET_SECRET_KEY=verySecretKey1 -e BUCKET_USE_SSL=false)
cleanup() { docker rm -f "$S3" >/dev/null 2>&1 || true; $C --profile tools down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT
fail() { echo "verify: FAIL: $*" >&2; exit 1; }
ok() { echo "verify: ok: $*"; }
# mcx runs a shell snippet in the backup image with the bucket aliased as m.
mcx() { $C --profile tools run --rm "${BK[@]}" --entrypoint sh backup -c "mc alias set m http://$S3:8000 accessKey1 verySecretKey1 >/dev/null && $1"; }
count() { $C exec -T db psql -U tp -d thymeandplace -Atc "SELECT count(*) FROM catalog_history"; }
objects() { mcx "mc ls --recursive m/tp-backups/backups/daily/ | wc -l" | tr -d ' \r'; }

$C up -d --build --wait db app
$C --profile tools run --rm --entrypoint /app/devseed ingest
docker run -d --name "$S3" -e ENDPOINT="$S3" -e REMOTE_MANAGEMENT_DISABLE=1 zenko/cloudserver:latest >/dev/null
docker network connect thymeandplace-e2e_default "$S3"
$C --profile tools build backup
for _ in $(seq 30); do mcx "mc mb --ignore-existing m/tp-backups" >/dev/null 2>&1 && break; sleep 2; done

day=$(date -u +%F)
key="backups/daily/$day.dump.age"

# 1. Encrypted upload, including the weekly copy.
out=$($C --profile tools run --rm "${BK[@]}" -e BACKUP_FORCE_WEEKLY=1 backup)
[[ $out == *"backup ok: $key"* ]] || fail "backup: $out"
[[ $(mcx "mc cat m/tp-backups/$key | head -c 21") == "age-encryption.org/v1" ]] || fail "daily object is not age ciphertext"
[[ $(mcx "mc ls --recursive m/tp-backups/backups/weekly/" ) == *".dump.age"* ]] || fail "weekly copy missing"
ok "encrypted daily and weekly upload"

# 2. Restore test with the on-host key.
out=$($C --profile tools run --rm "${BK[@]}" --entrypoint restore-test.sh restore)
[[ $out == *"restore-test ok ($day.dump.age)"* ]] || fail "restore-test: $out"
ok "restore-test"

# Refusals below must leave the live database untouched. Stop the app so a
# refusal can only come from the check under test.
$C stop app
before=$(count)
restore() { $C --profile tools run --rm "${BK[@]}" -e RESTORE_CONFIRM=thymeandplace "$@" 2>&1; }

# 3. No identity: refused before downloading.
if out=$(restore -e AGE_IDENTITY= restore "$key"); then fail "restore without identity succeeded"; fi
[[ $out == *"set AGE_IDENTITY"* ]] || fail "no-identity message: $out"
[[ $(count) == "$before" ]] || fail "database changed after no-identity refusal"
ok "no identity refused"

# 4. Wrong identity.
if out=$(restore --entrypoint sh restore -c "age-keygen -o /tmp/wrong.key 2>/dev/null && AGE_IDENTITY=/tmp/wrong.key restore.sh $key"); then fail "wrong identity succeeded"; fi
[[ $out == *"could not be decrypted"* ]] || fail "wrong-identity message: $out"
[[ $(count) == "$before" ]] || fail "database changed after wrong identity"
ok "wrong identity refused"

# 5. One changed byte.
mcx "mc cp --quiet m/tp-backups/$key /tmp/t && printf 'XYZ' | dd of=/tmp/t bs=1 seek=300 conv=notrunc 2>/dev/null && mc cp --quiet /tmp/t m/tp-backups/backups/tampered/t.dump.age"
if out=$(restore restore backups/tampered/t.dump.age); then fail "tampered backup restored"; fi
[[ $out == *"could not be decrypted"* ]] || fail "tamper message: $out"
[[ $(count) == "$before" ]] || fail "database changed after tampered restore"
ok "tampered backup refused"

# 6. A legacy plaintext dump still restores.
$C --profile tools run --rm "${BK[@]}" --entrypoint sh backup -c \
  "pg_dump --format=custom --no-owner --dbname=\"\$DATABASE_URL\" --file=/tmp/l.dump && mc alias set m http://$S3:8000 accessKey1 verySecretKey1 >/dev/null && mc cp --quiet /tmp/l.dump m/tp-backups/backups/legacy/l.dump"
out=$(restore restore backups/legacy/l.dump) || fail "legacy restore: $out"
[[ $out == *"restore ok"* ]] || fail "legacy restore: $out"
ok "legacy plaintext restore"

# 7. Missing, empty or private-key recipients: exit 2, nothing uploaded.
n=$(objects)
for bad in "" "AGE-SECRET-KEY-1QQQQ" "age1ok notakey"; do
  set +e; $C --profile tools run --rm "${BK[@]}" -e BACKUP_AGE_RECIPIENTS="$bad" backup >/dev/null 2>&1; rc=$?; set -e
  [[ $rc == 2 ]] || fail "recipients '$bad': exit $rc, want 2"
done
[[ $(objects) == "$n" ]] || fail "a refused backup uploaded something"
ok "bad recipients refused"

# 8. A dump that can't be taken fails loudly and uploads nothing.
set +e; $C --profile tools run --rm "${BK[@]}" -e DATABASE_URL="postgres://tp_backup:wrong@db:5432/thymeandplace?sslmode=disable" backup >/dev/null 2>&1; rc=$?; set -e
[[ $rc != 0 ]] || fail "backup with a failing pg_dump exited 0"
[[ $(objects) == "$n" ]] || fail "a failed dump uploaded something"
ok "failed dump uploads nothing"

echo "verify: all backup checks passed"
```

- [ ] **Step 2: Run it**

Run: `./deploy/backup/verify.sh` (Git Bash on Windows, or Linux).
Expected: eight `verify: ok:` lines, then `verify: all backup checks passed`, and exit 0. If a check fails, fix the script under test rather than the assertion, unless the assertion contradicts the spec.

- [ ] **Step 3: CI job**

Append to `.github/workflows/ci.yml`:

```yaml
  backup:
    if: github.ref == 'refs/heads/main'
    needs: [deploy]
    runs-on: ubuntu-latest
    timeout-minutes: 25
    steps:
      - uses: actions/checkout@v4
      - name: Backup, encryption and restore recipe
        run: ./deploy/backup/verify.sh
```

If the supply-chain plan has already pinned actions, use the same pinned `actions/checkout` SHA as the other jobs.

- [ ] **Step 4: Commit**

```bash
git add deploy/backup/verify.sh .github/workflows/ci.yml
git commit -m "ci: run the backup encryption and restore recipe on main (#12)"
```

### Task 6: Documentation

**Files:**
- Modify: `docs/operations.md` ("Nightly backups", "Restoring from backup", "Verifying locally", new "Backup encryption" and "Backup deletion protection" sections)
- Modify: `docs/superpowers/specs/2026-09-27-backup-encryption-design.md` (Status → Accepted)
- Modify: `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` §14 and §16

**Interfaces:** documentation only.

- [ ] **Step 1: "Backup encryption" section in `docs/operations.md`**

Cover:
- **Keys.**
  - Run `age-keygen -o recovery.key` on your own machine. Store the private key in the password manager and on paper, never on the VPS.
  - Generate the restore-test key on the VPS: `docker run --rm --entrypoint age-keygen thymeandplace-backup:latest > /etc/thymeandplace/restore-test.agekey && chmod 600 /etc/thymeandplace/restore-test.agekey`.
  - Put both `age1…` public keys in `BACKUP_AGE_RECIPIENTS` in `backup.env`.
- **Threat model** (spec §4.1).
  - A bucket-only compromise reveals nothing.
  - A host compromise reveals history through the restore-test key. The recovery key and bucket protection still allow a restore.
- **Key rotation.** Add the new recipient, deploy, wait 28 days, then remove the old recipient.
- **Rollout** (spec §7).
  1. Generate the keys.
  2. Deploy the image.
  3. Run `backup`, then `restore-test`, before the next nightly run.
  4. Enable versioning.
  5. After 28 days, `mc ls` shows no plaintext `.dump` left.

- [ ] **Step 2: "Restoring from backup"**

- Object keys now end `.dump.age`.
- Add the disaster-restore command, then `shred -u /root/recovery.key` afterwards:
  `docker compose --profile tools run --rm -v /root/recovery.key:/run/secrets/recovery.agekey:ro -e AGE_IDENTITY=/run/secrets/recovery.agekey -e RESTORE_CONFIRM=thymeandplace restore backups/daily/<date>.dump.age`
- Legacy `.dump` keys restore without an identity.

- [ ] **Step 3: "Backup deletion protection" section**

Describe the two levels (spec §4.4), the provider question (spec §8 question 1: record which provider and level are in place here), how to check versioning, and the undelete drill: `mc rm` a test object under `backups/daily/`, then restore its previous version with `mc undo` or the provider console. Explain `BACKUP_PRUNE=lifecycle` with the rules `backups/daily/` 8 days and `backups/weekly/` 29 days.

- [ ] **Step 4: "Verifying locally"**

Replace the hand-run recipe's expected output with `.dump.age` keys. Add: "`./deploy/backup/verify.sh` runs this recipe with every check (encrypted upload, restore-test, refusals without the right key or with a changed byte, legacy restore, bad recipients, failed dump) and runs in CI on `main`."

- [ ] **Step 5: Core spec**

- §14 Backups: "Dumps are verified, encrypted with `age` to an off-host recovery key and an on-host restore-test key, then uploaded. The bucket keeps deleted versions for 35 days (versioning), or expires copies by lifecycle rules with a no-delete writer key."
- §16: add the row "Backup encryption: age, two X25519 recipients (recovery off-host, restore-test on-host)".

- [ ] **Step 6: Commit**

```bash
git add docs/
git commit -m "docs: backup encryption, keys, disaster restore and deletion protection (#12)"
```

### Task 7: Whole-repo check

- [ ] **Step 1:** Run `for f in deploy/backup/*.sh; do sh -n "$f"; done && bash -n deploy/backup/verify.sh`. Expected: no output.
- [ ] **Step 2:** Run `./deploy/backup/verify.sh`. Expected: `verify: all backup checks passed`.
- [ ] **Step 3:** Run `go test ./... -count=1`. Expected: PASS. No Go changes, but this confirms the compose and env edits broke nothing the tests read.
- [ ] **Step 4:** Give the owner the rollout steps that happen outside the repo: key generation, putting the recipients in `/etc/thymeandplace/backup.env`, enabling versioning, and the undelete drill.
