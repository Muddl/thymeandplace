#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
# Relative to deploy/, where the compose files live.
export TP_ENV_DIR=./e2e
# Git Bash on Windows (MSYS) rewrites an argument that looks like an absolute
# Unix path (e.g. "/app/devseed" in --entrypoint /app/devseed) into a Windows
# path before docker ever sees it. This opts out; it's a no-op on Linux CI.
export MSYS_NO_PATHCONV=1
C="docker compose -p thymeandplace-e2e -f ../deploy/compose.yaml -f ../deploy/compose.e2e.yaml"

BROWSER="${BROWSER:-chromium}"
SHARD="${SHARD:-}"
GREP="${GREP:-}"
E2E_BASE_URL="${E2E_BASE_URL:-https://localhost}"
export E2E_BASE_URL
export BROWSER
export SHARD

start_stack() {
  if [ "${E2E_SKIP_STACK:-}" = 1 ]; then
    return 1
  fi
  case "$E2E_BASE_URL" in
    https://localhost | https://localhost:* | https://localhost/* | \
    http://localhost | http://localhost:* | http://localhost/* | \
    https://127.0.0.1 | https://127.0.0.1:* | https://127.0.0.1/* | \
    http://127.0.0.1 | http://127.0.0.1:* | http://127.0.0.1/*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

if start_stack; then
  # compose.e2e.yaml names the project thymeandplace-e2e, so this down -v only
  # ever removes thymeandplace-e2e_* volumes, never production's thymeandplace_*.
  # --profile tools makes it remove the ingest_work volume the devseed run creates.
  trap '$C --profile tools down -v >/dev/null 2>&1 || true' EXIT
  $C build
  $C up -d --wait db app caddy
  $C --profile tools run --rm --entrypoint /app/devseed ingest
fi

npm ci
npx playwright install --with-deps "$BROWSER"

args=(test --project="$BROWSER")
if [ -n "$SHARD" ]; then
  args+=(--shard="$SHARD")
fi
if [ -n "$GREP" ]; then
  args+=(--grep="$GREP")
fi
npx playwright "${args[@]}"
