#!/bin/sh
# Restores the newest daily dump into a scratch database next to the live one,
# through restore.sh (the same drop/create/single-transaction path a real
# restore takes), and compares row counts of the tables that matter. Drops the
# scratch DB after, even on failure (trap).
#
# A backup taken during the day can legitimately trail the live catalog_history
# count if edits happened since. Run this right after a fresh `backup` run.
#
# With HEARTBEAT_RESTORE_TEST_URL set, pings /start, then success, or /fail
# with the count table (docs/operations.md, "Monitoring and alerts").
set -eu
# The image installs heartbeat.sh next to this script (deploy/backup/Dockerfile).
# shellcheck source=deploy/backup/heartbeat.sh
. /usr/local/bin/heartbeat.sh
HEARTBEAT_URL=${HEARTBEAT_RESTORE_TEST_URL:-}
scratch_db=thymeandplace_restore_test
latest=
report=

# One EXIT trap, set before anything can fail: drop the scratch database and
# send exactly one final ping, keeping the exit status.
on_exit() {
  rc=$?
  [ -n "${DATABASE_URL:-}" ] && psql "$DATABASE_URL" -X -qc "DROP DATABASE IF EXISTS $scratch_db WITH (FORCE)" >/dev/null 2>&1 || true
  if [ "$rc" -eq 0 ]; then
    hc "" "restore-test ok ($latest)"
  else
    hc /fail "restore-test failed (exit $rc, ${latest:-no dump})
$report"
  fi
  exit "$rc"
}
trap on_exit EXIT

for v in DATABASE_URL BUCKET_ENDPOINT BUCKET_NAME BUCKET_ACCESS_KEY BUCKET_SECRET_KEY; do
  eval "val=\${$v:-}"
  [ -n "$val" ] || { echo "restore-test: $v is not set" >&2; exit 2; }
done
hc /start

scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null

# During the switch to encryption both kinds exist; names sort by date, and
# on the same date .dump.age sorts after .dump.
latest=$(mc ls "store/$BUCKET_NAME/backups/daily/" | awk '{print $NF}' | grep -E '\.dump(\.age)?$' | sort | tail -n 1)
[ -n "$latest" ] || { echo "restore-test: no daily backups found" >&2; exit 1; }
base=${DATABASE_URL%%\?*}
scratch_url="${base%/*}/$scratch_db${DATABASE_URL#"$base"}"

# restore.sh refuses to run without RESTORE_CONFIRM naming the target
# database; we own the scratch database here, so confirm it ourselves.
# restore.sh creates it (after dropping any leftover from an earlier run).
RESTORE_CONFIRM="$scratch_db" restore.sh "backups/daily/$latest" "$scratch_url"

status=0
for t in ingredient ingredient_pairing seafood_region catalog_history dataset_versions admin_passkey admin_backup climate_cell ocean_ecoregion place; do
  live=$(psql "$DATABASE_URL" -X -Atqc "SELECT count(*) FROM $t")
  copy=$(psql "$scratch_url" -X -Atqc "SELECT count(*) FROM $t")
  printf '%-20s live=%-10s restored=%s\n' "$t" "$live" "$copy"
  report="$report$(printf '%-20s live=%-10s restored=%s' "$t" "$live" "$copy")
"
  [ "$live" = "$copy" ] || status=1
done
# Fail inside the if, not with a trailing `exit $status`: shellcheck 0.11
# misreads a final top-level exit as making the EXIT trap's on_exit
# unreachable (SC2329).
if [ "$status" -ne 0 ]; then
  echo "restore-test FAILED: counts differ ($latest)" >&2
  exit 1
fi
echo "restore-test ok ($latest)"
