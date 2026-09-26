#!/usr/bin/env bash
# Brings up db+app+caddy with local TLS and checks health, the maintenance page and shutdown.
set -euo pipefail
cd "$(dirname "$0")"
export TP_ENV_FILE=./e2e.env
C="docker compose -p thymeandplace-e2e -f compose.yaml -f compose.e2e.yaml"
# compose.e2e.yaml names the project thymeandplace-e2e, so this down -v only
# ever removes thymeandplace-e2e_* volumes, never production's thymeandplace_*.
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
resp=$(curl -ks -D - https://localhost/ || true)
echo "$resp" | head -1 | grep -q " 503" || { echo "expected 503 during outage"; echo "$resp"; exit 1; }
echo "$resp" | grep -qi "^retry-after: 30" || { echo "missing Retry-After header"; echo "$resp"; exit 1; }
echo "$resp" | grep -q "Thyme &amp; Place is updating" || { echo "maintenance page missing"; echo "$resp"; exit 1; }
$C start app
echo "smoke ok"
