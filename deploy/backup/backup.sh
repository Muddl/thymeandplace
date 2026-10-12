#!/bin/sh
# Nightly pg_dump, encrypted with age, to the bucket. Keeps 7 daily and 4 weekly copies.
# With HEARTBEAT_BACKUP_URL set, pings /start, then success, or /fail naming
# the step that failed (docs/operations.md, "Monitoring and alerts").
set -eu
# The image installs heartbeat.sh next to this script (deploy/backup/Dockerfile).
# shellcheck source=deploy/backup/heartbeat.sh
. /usr/local/bin/heartbeat.sh
HEARTBEAT_URL=${HEARTBEAT_BACKUP_URL:-}

step="env"
file=
prune_err=
day=$(date -u +%Y-%m-%d)

# One EXIT trap: delete the dump and send exactly one final ping, keeping
# the exit status.
on_exit() {
  rc=$?
  [ -n "$file" ] && rm -f "$file" "$file.age"
  if [ "$rc" -ne 0 ]; then
    hc /fail "backup failed during $step (exit $rc)"
  elif [ -n "$prune_err" ]; then
    hc /fail "dump uploaded; pruning failed: $prune_err"
  else
    hc "" "backup ok: backups/daily/$day.dump.age"
  fi
  exit "$rc"
}
trap on_exit EXIT

for v in DATABASE_URL BUCKET_ENDPOINT BUCKET_NAME BUCKET_ACCESS_KEY BUCKET_SECRET_KEY; do
  eval "val=\${$v:-}"
  [ -n "$val" ] || { echo "backup: $v is not set" >&2; exit 2; }
done

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
hc /start

step="alias"
scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null

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

# Pruning failures must not fail the backup itself (today's dump is already
# uploaded) but must not be silent either: they fail the heartbeat check.
# The ping names only the prefix. mc's own message stays in stderr (the
# journal or log file): a network error there carries the store's IP, and
# ping bodies never carry an IP (docs/operations.md, "Privacy").
# With BACKUP_PRUNE=lifecycle, bucket lifecycle rules expire old copies and
# this key may not be allowed to delete (docs/operations.md, "Backup
# deletion protection").
if [ "${BACKUP_PRUNE:-}" != "lifecycle" ]; then
  step=prune
  if ! err=$(mc rm --recursive --force --older-than 7d "store/$BUCKET_NAME/backups/daily/" 2>&1 >/dev/null); then
    echo "backup: warning: pruning backups/daily failed: $err" >&2
    prune_err="backups/daily"
  fi
  if ! err=$(mc rm --recursive --force --older-than 28d "store/$BUCKET_NAME/backups/weekly/" 2>&1 >/dev/null); then
    echo "backup: warning: pruning backups/weekly failed: $err" >&2
    prune_err="${prune_err:+$prune_err; }backups/weekly"
  fi
fi

echo "backup ok: backups/daily/$day.dump.age"
