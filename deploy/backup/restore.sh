#!/bin/sh
# usage: RESTORE_CONFIRM=<dbname> restore.sh <object key, e.g. backups/daily/2026-10-01.dump> <target DATABASE_URL>
set -eu
[ $# -eq 2 ] || { echo "usage: restore.sh <key> <target-database-url>" >&2; exit 2; }

# --clean below drops and recreates every object in the target database, so
# require the caller to name the database they intend to overwrite before
# anything is downloaded or touched. This catches a copy-pasted or stale
# DATABASE_URL pointed at the wrong database before it does any damage.
dbname=${2##*/}
dbname=${dbname%%\?*}
if [ "${RESTORE_CONFIRM:-}" != "$dbname" ]; then
  echo "refusing to restore over $dbname: set RESTORE_CONFIRM=$dbname to confirm" >&2
  exit 2
fi

: "${BUCKET_ENDPOINT:?}" "${BUCKET_NAME:?}" "${BUCKET_ACCESS_KEY:?}" "${BUCKET_SECRET_KEY:?}"
scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null
tmp=/tmp/restore.dump
trap 'rm -f "$tmp"' EXIT
mc cp --quiet "store/$BUCKET_NAME/$1" "$tmp"
pg_restore --no-owner --clean --if-exists --exit-on-error --dbname="$2" "$tmp"
echo "restore ok: $1 → target database"
