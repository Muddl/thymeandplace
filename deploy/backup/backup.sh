#!/bin/sh
# Nightly pg_dump to the bucket. Keeps 7 daily and 4 weekly copies.
set -eu
: "${DATABASE_URL:?}" "${BUCKET_ENDPOINT:?}" "${BUCKET_NAME:?}" "${BUCKET_ACCESS_KEY:?}" "${BUCKET_SECRET_KEY:?}"
scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null

day=$(date -u +%Y-%m-%d)
file="/tmp/thymeandplace-$day.dump"
trap 'rm -f "$file"' EXIT
pg_dump --format=custom --no-owner --dbname="$DATABASE_URL" --file="$file"
[ -s "$file" ] || { echo "backup: dump is empty" >&2; exit 1; }

mc cp --quiet "$file" "store/$BUCKET_NAME/backups/daily/$day.dump"
if [ "$(date -u +%u)" = "7" ]; then
  mc cp --quiet "$file" "store/$BUCKET_NAME/backups/weekly/$(date -u +%G-W%V).dump"
fi

# Pruning failures must not fail the backup itself (today's dump is already
# safely uploaded above) but must not be silent either: cron only captures
# stdout/stderr, so a swallowed failure here would mean unbounded storage
# growth with no signal until the bucket fills up.
if ! err=$(mc rm --recursive --force --older-than 7d "store/$BUCKET_NAME/backups/daily/" 2>&1 >/dev/null); then
  echo "backup: warning: pruning backups/daily failed: $err" >&2
fi
if ! err=$(mc rm --recursive --force --older-than 28d "store/$BUCKET_NAME/backups/weekly/" 2>&1 >/dev/null); then
  echo "backup: warning: pruning backups/weekly failed: $err" >&2
fi

echo "backup ok: backups/daily/$day.dump"
