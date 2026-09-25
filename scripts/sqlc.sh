#!/usr/bin/env bash
# Runs sqlc 1.29.0 in Docker from the repo root. Works in Git Bash and on Linux.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
if command -v cygpath >/dev/null 2>&1; then
  root="$(cygpath -m "$root")"
  export MSYS_NO_PATHCONV=1
fi
docker run --rm -v "$root:/src" -w /src sqlc/sqlc:1.29.0 "${@:-generate}"
