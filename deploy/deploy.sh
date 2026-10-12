#!/usr/bin/env bash
# usage (as root, from /opt/thymeandplace/deploy): ./deploy.sh <40-hex commit SHA>
# Pulls the images CI built for that commit, starts them (migrate first, then
# app, waiting for its health check), and records the deploy. Rollback is
# the same command with the previous SHA from /var/lib/thymeandplace/deploys.log.
set -euo pipefail
sha=${1:-}
[[ $sha =~ ^[0-9a-f]{40}$ ]] || { echo "deploy: give the full 40-character commit SHA (got '${sha}')" >&2; exit 2; }
cd "$(dirname "$0")"
export TP_IMAGE_TAG=$sha
C="docker compose -f compose.yaml -f compose.release.yaml --profile tools"
$C pull migrate app ingest transform backup restore
start=$(date +%s)
$C up -d --wait app
elapsed=$(( $(date +%s) - start ))
digest=$(docker image inspect --format '{{index .RepoDigests 0}}' "ghcr.io/muddl/thymeandplace-app:$sha")
mkdir -p /var/lib/thymeandplace
echo "$(date -u +%FT%TZ) $sha $digest ${elapsed}s" >> /var/lib/thymeandplace/deploys.log
echo "deploy ok: $sha ($digest); maintenance window about ${elapsed}s"
