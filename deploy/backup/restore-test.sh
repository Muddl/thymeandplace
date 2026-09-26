#!/bin/sh
# Restores the newest daily dump into a scratch database next to the live one
# and compares row counts of the tables that matter. Drops the scratch DB after,
# even on failure (trap).
#
# A backup taken during the day can legitimately trail the live catalog_history
# count if edits happened since. Run this right after a fresh `backup` run.
set -eu
: "${DATABASE_URL:?}" "${BUCKET_ENDPOINT:?}" "${BUCKET_NAME:?}" "${BUCKET_ACCESS_KEY:?}" "${BUCKET_SECRET_KEY:?}"
scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null

latest=$(mc ls "store/$BUCKET_NAME/backups/daily/" | awk '{print $NF}' | sort | tail -n 1)
[ -n "$latest" ] || { echo "restore-test: no daily backups found" >&2; exit 1; }
scratch_db=thymeandplace_restore_test
scratch_url=$(echo "$DATABASE_URL" | sed -E "s#/[^/?]+(\?|$)#/$scratch_db\1#")

psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -qc "DROP DATABASE IF EXISTS $scratch_db WITH (FORCE)" -c "CREATE DATABASE $scratch_db"
trap 'psql "$DATABASE_URL" -qc "DROP DATABASE IF EXISTS '"$scratch_db"' WITH (FORCE)" >/dev/null 2>&1 || true' EXIT
# restore.sh refuses to run without RESTORE_CONFIRM naming the target
# database; we own the scratch database here, so confirm it ourselves.
RESTORE_CONFIRM="$scratch_db" restore.sh "backups/daily/$latest" "$scratch_url"

status=0
for t in ingredient ingredient_pairing seafood_region catalog_history dataset_versions admin_passkey admin_backup climate_cell ocean_ecoregion place; do
  live=$(psql "$DATABASE_URL" -Atqc "SELECT count(*) FROM $t")
  copy=$(psql "$scratch_url" -Atqc "SELECT count(*) FROM $t")
  printf '%-20s live=%-10s restored=%s\n' "$t" "$live" "$copy"
  [ "$live" = "$copy" ] || status=1
done
[ $status -eq 0 ] && echo "restore-test ok ($latest)" || echo "restore-test FAILED: counts differ ($latest)" >&2
exit $status
