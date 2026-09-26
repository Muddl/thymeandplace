#!/usr/bin/env bash
# Brings up db+app+caddy with local TLS and checks health, the maintenance page and shutdown.
set -euo pipefail
cd "$(dirname "$0")"
export TP_ENV_FILE=./e2e.env
C="docker compose -f compose.yaml -f compose.e2e.yaml"
trap '$C down -v >/dev/null 2>&1 || true' EXIT

$C up -d --build --wait db app caddy
for i in $(seq 1 30); do
  curl -ksf https://localhost/healthz >/dev/null && break
  sleep 1
done
curl -ksf https://localhost/healthz | grep -q ok
curl -ksf https://localhost/readyz | grep -q ready
curl -ksf https://localhost/ | grep -q "Find your town"

# Review Focus 3: app down → maintenance page, not a bare 502.
$C stop app
sleep 3
body=$(curl -ks https://localhost/ || true)
echo "$body" | grep -q "Thyme &amp; Place is updating" || { echo "maintenance page missing"; echo "$body"; exit 1; }
$C start app
echo "smoke ok"
