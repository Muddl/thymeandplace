# shellcheck shell=sh
# Sourced by backup.sh and restore-test.sh. hc <suffix> [body] POSTs body to
# $HEARTBEAT_URL<suffix> (a Healthchecks.io check; suffix "" for success,
# /start or /fail). Empty HEARTBEAT_URL → no-op. It never fails its caller,
# and never prints the URL (a secret).
hc() {
  [ -n "${HEARTBEAT_URL:-}" ] || return 0
  curl -fsS -m 10 --retry 3 -o /dev/null --data-raw "${2:-}" "${HEARTBEAT_URL}$1" >/dev/null 2>&1 \
    || echo "heartbeat ping failed (${1:-success})" >&2
  return 0
}
