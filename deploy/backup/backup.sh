#!/bin/sh
# Nightly pg_dump to the bucket. Keeps 7 daily and 4 weekly copies.
# With HEARTBEAT_BACKUP_URL set, pings /start, then success, or /fail naming
# the step that failed (docs/operations.md, "Monitoring and alerts").
set -eu
. /usr/local/bin/heartbeat.sh
HEARTBEAT_URL=${HEARTBEAT_BACKUP_URL:-}

step=env
file=
prune_err=
day=$(date -u +%Y-%m-%d)

# One EXIT trap: delete the dump and send exactly one final ping, keeping
# the exit status.
on_exit() {
  rc=$?
  [ -n "$file" ] && rm -f "$file"
  if [ "$rc" -ne 0 ]; then
    hc /fail "backup failed during $step (exit $rc)"
  elif [ -n "$prune_err" ]; then
    hc /fail "dump uploaded; pruning failed: $prune_err"
  else
    hc "" "backup ok: backups/daily/$day.dump"
  fi
  exit "$rc"
}
trap on_exit EXIT

for v in DATABASE_URL BUCKET_ENDPOINT BUCKET_NAME BUCKET_ACCESS_KEY BUCKET_SECRET_KEY; do
  eval "val=\${$v:-}"
  [ -n "$val" ] || { echo "backup: $v is not set" >&2; exit 2; }
done
hc /start

step=alias
scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null

step=dump
file="/tmp/thymeandplace-$day.dump"
pg_dump --format=custom --no-owner --dbname="$DATABASE_URL" --file="$file"
[ -s "$file" ] || { echo "backup: dump is empty" >&2; exit 1; }

step=upload
mc cp --quiet "$file" "store/$BUCKET_NAME/backups/daily/$day.dump"
if [ "$(date -u +%u)" = "7" ]; then
  step=weekly
  mc cp --quiet "$file" "store/$BUCKET_NAME/backups/weekly/$(date -u +%G-W%V).dump"
fi

# Pruning failures must not fail the backup itself (today's dump is already
# uploaded) but must not be silent either: they fail the heartbeat check.
step=prune
if ! err=$(mc rm --recursive --force --older-than 7d "store/$BUCKET_NAME/backups/daily/" 2>&1 >/dev/null); then
  echo "backup: warning: pruning backups/daily failed: $err" >&2
  prune_err="backups/daily: $(printf %s "$err" | head -c 300)"
fi
if ! err=$(mc rm --recursive --force --older-than 28d "store/$BUCKET_NAME/backups/weekly/" 2>&1 >/dev/null); then
  echo "backup: warning: pruning backups/weekly failed: $err" >&2
  prune_err="${prune_err:+$prune_err; }backups/weekly: $(printf %s "$err" | head -c 300)"
fi

echo "backup ok: backups/daily/$day.dump"
