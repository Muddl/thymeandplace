#!/usr/bin/env bash
# The local backup recipe (docs/operations.md, "Verifying locally") with
# assertions, on the e2e stack and a throwaway S3-compatible bucket. Never
# touches production: the e2e project name keeps every volume apart.
set -euo pipefail
cd "$(dirname "$0")/.." # deploy/
export TP_ENV_DIR=./e2e MSYS_NO_PATHCONV=1
C="docker compose -p thymeandplace-e2e -f compose.yaml -f compose.e2e.yaml"
S3=tp-verify-s3
BK=(-e "BUCKET_ENDPOINT=$S3:8000" -e BUCKET_NAME=tp-backups -e BUCKET_ACCESS_KEY=accessKey1 -e BUCKET_SECRET_KEY=verySecretKey1 -e BUCKET_USE_SSL=false)
cleanup() { docker rm -f "$S3" >/dev/null 2>&1 || true; $C --profile tools down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT
fail() { echo "verify: FAIL: $*" >&2; exit 1; }
ok() { echo "verify: ok: $*"; }
# mcx runs a shell snippet in the backup image with the bucket aliased as m.
mcx() { $C --profile tools run --rm "${BK[@]}" --entrypoint sh backup -c "mc alias set m http://$S3:8000 accessKey1 verySecretKey1 >/dev/null && $1"; }
# sentinel is t while verify_sentinel exists. The table is created after
# every dump the refusal checks use, so a restore that wrongly ran drops it.
sentinel() { $C exec -T db psql -U tp -d thymeandplace -Atc "SELECT to_regclass('public.verify_sentinel') IS NOT NULL" | tr -d '\r'; }
# bucket lists every object with a hash of its content. age encrypts with a
# fresh file key each run, so any upload, even over today's key, changes it.
# shellcheck disable=SC2016 # the snippet expands inside the backup container
bucket() { mcx 'for o in $(mc ls --recursive m/tp-backups/backups/ | awk "{print \$NF}"); do echo "$o $(mc cat "m/tp-backups/backups/$o" | sha256sum)"; done' | tr -d '\r'; }

$C up -d --build --wait db app
$C --profile tools run --rm --entrypoint /app/devseed ingest
docker run -d --name "$S3" -e ENDPOINT="$S3" -e REMOTE_MANAGEMENT_DISABLE=1 zenko/cloudserver:latest@sha256:b53e57829cf7df357323e60a19c9f98d2218f1b7ccb1d7cea5761a5a227a9ee3 >/dev/null
docker network connect thymeandplace-e2e_default "$S3"
$C --profile tools build backup
for _ in $(seq 30); do mcx "mc mb --ignore-existing m/tp-backups" >/dev/null 2>&1 && break; sleep 2; done

day=$(date -u +%F)
key="backups/daily/$day.dump.age"

# 1. Encrypted upload, including the weekly copy.
out=$($C --profile tools run --rm "${BK[@]}" -e BACKUP_FORCE_WEEKLY=1 backup 2>&1) || fail "backup: $out"
[[ $out == *"backup ok: $key"* ]] || fail "backup: $out"
[[ $(mcx "mc cat m/tp-backups/$key | head -c 21") == "age-encryption.org/v1" ]] || fail "daily object is not age ciphertext"
[[ $(mcx "mc ls --recursive m/tp-backups/backups/weekly/") == *".dump.age"* ]] || fail "weekly copy missing"
ok "encrypted daily and weekly upload"

# 2. Restore test with the on-host key.
out=$($C --profile tools run --rm "${BK[@]}" --entrypoint restore-test.sh restore 2>&1) || fail "restore-test: $out"
[[ $out == *"restore-test ok ($day.dump.age)"* ]] || fail "restore-test: $out"
ok "restore-test"

$C stop app
restore() { $C --profile tools run --rm "${BK[@]}" -e RESTORE_CONFIRM=thymeandplace "$@" 2>&1; }

# 3. Disaster restore with the recovery key, over the live database, in exactly
# the form docs/operations.md gives ("Disaster restore with the recovery key").
# The -e override replaces the compose-set restore-test identity, so this only
# passes if backup.sh encrypted to the second recipient too. A table created
# after the backup must be gone afterwards.
$C exec -T db psql -U tp -d thymeandplace -qc "CREATE TABLE verify_disaster ()"
out=$(restore -v ./e2e/recovery-test.agekey:/run/secrets/recovery.agekey:ro -e AGE_IDENTITY=/run/secrets/recovery.agekey restore "$key") || fail "disaster restore: $out"
[[ $out == *"restore ok: $key"* ]] || fail "disaster restore: $out"
[[ $($C exec -T db psql -U tp -d thymeandplace -Atc "SELECT to_regclass('public.verify_disaster') IS NULL" | tr -d '\r') == t ]] || fail "disaster restore left the database unchanged"
ok "disaster restore with the recovery key"

# Refusals below must leave the live database untouched. The app is stopped,
# so a refusal can only come from the check under test.
$C exec -T db psql -U tp -d thymeandplace -qc "CREATE TABLE verify_sentinel ()"
[[ $(sentinel) == t ]] || fail "could not create the sentinel table"

# 4. No identity: refused before downloading.
if out=$(restore -e AGE_IDENTITY= restore "$key"); then fail "restore without identity succeeded"; fi
[[ $out == *"set AGE_IDENTITY"* ]] || fail "no-identity message: $out"
[[ $(sentinel) == t ]] || fail "database changed after no-identity refusal"
ok "no identity refused"

# 5. Wrong identity.
if out=$(restore --entrypoint sh restore -c "age-keygen -o /tmp/wrong.key 2>/dev/null && AGE_IDENTITY=/tmp/wrong.key restore.sh $key"); then fail "wrong identity succeeded"; fi
[[ $out == *"could not be decrypted"* ]] || fail "wrong-identity message: $out"
[[ $(sentinel) == t ]] || fail "database changed after wrong identity"
ok "wrong identity refused"

# 6. One changed byte: offset 300, past the age header, becomes (b+1) mod 256.
# shellcheck disable=SC2016 # the snippet expands inside the backup container
mcx "k=$key; "'mc cp --quiet "m/tp-backups/$k" /tmp/o && cp /tmp/o /tmp/t &&
  b=$(dd if=/tmp/o bs=1 skip=300 count=1 2>/dev/null | od -An -tu1 | tr -d " ") &&
  printf %b "\\0$(printf %03o $(( (b + 1) % 256 )))" | dd of=/tmp/t bs=1 seek=300 count=1 conv=notrunc 2>/dev/null &&
  [ "$(cmp -l /tmp/o /tmp/t | wc -l)" = 1 ] &&
  mc cp --quiet /tmp/t m/tp-backups/backups/tampered/t.dump.age' || fail "could not change one byte of $key"
if out=$(restore restore backups/tampered/t.dump.age); then fail "tampered backup restored"; fi
[[ $out == *"could not be decrypted"* ]] || fail "tamper message: $out"
[[ $(sentinel) == t ]] || fail "database changed after tampered restore"
ok "tampered backup refused"

# 7. A legacy plaintext dump still restores.
$C --profile tools run --rm "${BK[@]}" --entrypoint sh backup -c \
  "pg_dump --format=custom --no-owner --dbname=\"\$DATABASE_URL\" --file=/tmp/l.dump && mc alias set m http://$S3:8000 accessKey1 verySecretKey1 >/dev/null && mc cp --quiet /tmp/l.dump m/tp-backups/backups/legacy/l.dump"
out=$(restore restore backups/legacy/l.dump) || fail "legacy restore: $out"
[[ $out == *"restore ok"* ]] || fail "legacy restore: $out"
ok "legacy plaintext restore"

# 8. Missing, empty or private-key recipients: exit 2, nothing uploaded.
before=$(bucket)
[[ $before == *"daily/$day.dump.age "* ]] || fail "bucket listing: $before"
for bad in "" "AGE-SECRET-KEY-1QQQQ" "age1ok notakey"; do
  set +e; $C --profile tools run --rm "${BK[@]}" -e BACKUP_AGE_RECIPIENTS="$bad" backup >/dev/null 2>&1; rc=$?; set -e
  [[ $rc == 2 ]] || fail "recipients '$bad': exit $rc, want 2"
done
[[ $(bucket) == "$before" ]] || fail "a refused backup uploaded something"
ok "bad recipients refused"

# 9. A dump that can't be taken fails loudly and uploads nothing.
set +e; $C --profile tools run --rm "${BK[@]}" -e DATABASE_URL="postgres://tp_backup:wrong@db:5432/thymeandplace?sslmode=disable" backup >/dev/null 2>&1; rc=$?; set -e
[[ $rc != 0 ]] || fail "backup with a failing pg_dump exited 0"
[[ $(bucket) == "$before" ]] || fail "a failed dump uploaded something"
ok "failed dump uploads nothing"

# 10. A zero-byte dump fails at verify and uploads nothing: a pg_dump shim
# first on PATH truncates its --file target and exits 0.
set +e
# shellcheck disable=SC2016 # the snippet expands inside the backup container
out=$($C --profile tools run --rm "${BK[@]}" --entrypoint sh backup -c 'mkdir -p /tmp/shim && cat >/tmp/shim/pg_dump <<"EOF"
#!/bin/sh
for a; do case $a in --file=*) : > "${a#--file=}"; echo "shim: wrote a zero-byte ${a#--file=}" >&2;; esac; done
exit 0
EOF
chmod +x /tmp/shim/pg_dump && PATH=/tmp/shim:$PATH backup.sh' 2>&1)
rc=$?
set -e
[[ $rc == 1 ]] || fail "backup with an empty dump: exit $rc, want 1: $out"
[[ $out == *"shim: wrote a zero-byte /tmp/thymeandplace-$day.dump"* ]] || fail "pg_dump shim did not run: $out"
[[ $out == *"backup: dump is empty"* ]] || fail "empty-dump message: $out"
[[ $(bucket) == "$before" ]] || fail "an empty dump uploaded something"
ok "empty dump uploads nothing"

echo "verify: all backup checks passed"
