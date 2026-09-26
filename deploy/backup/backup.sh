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
