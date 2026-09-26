#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
export TP_ENV_FILE=./e2e.env
# Git Bash on Windows (MSYS) rewrites an argument that looks like an absolute
# Unix path (e.g. "/app/devseed" in --entrypoint /app/devseed) into a Windows
# path before docker ever sees it. This opts out; it's a no-op on Linux CI.
export MSYS_NO_PATHCONV=1
C="docker compose -f ../deploy/compose.yaml -f ../deploy/compose.e2e.yaml"
# compose.e2e.yaml names the project thymeandplace-e2e, so this down -v only
# ever removes thymeandplace-e2e_* volumes, never production's thymeandplace_*.
# --profile tools makes it remove the ingest_work volume the devseed run creates.
trap '$C --profile tools down -v >/dev/null 2>&1 || true' EXIT
$C build
$C up -d --wait db app caddy
$C --profile tools run --rm --entrypoint /app/devseed ingest
npm ci
npx playwright install --with-deps chromium
npx playwright test
