#!/bin/sh
# usage: restore.sh <object key, e.g. backups/daily/2026-10-01.dump> <target DATABASE_URL>
set -eu
[ $# -eq 2 ] || { echo "usage: restore.sh <key> <target-database-url>" >&2; exit 2; }
: "${BUCKET_ENDPOINT:?}" "${BUCKET_NAME:?}" "${BUCKET_ACCESS_KEY:?}" "${BUCKET_SECRET_KEY:?}"
scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null
mc cp --quiet "store/$BUCKET_NAME/$1" /tmp/restore.dump
pg_restore --no-owner --clean --if-exists --exit-on-error --dbname="$2" /tmp/restore.dump
rm -f /tmp/restore.dump
echo "restore ok: $1 → target database"
