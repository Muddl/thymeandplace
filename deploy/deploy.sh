#!/usr/bin/env bash
# usage (as root, from /opt/thymeandplace/deploy): ./deploy.sh <40-hex commit SHA>
# Pulls the images CI built for that commit and compose.yaml's pinned db and
# caddy images, starts them (migrate first, then app, waiting for /readyz;
# db and caddy only if their image or settings changed), points plain
# `docker compose` commands here at the release, and records the deploy.
# Rollback is the same command with the previous SHA from
# /var/lib/thymeandplace/deploys.log.
set -euo pipefail
sha=${1:-}
[[ $sha =~ ^[0-9a-f]{40}$ ]] || { echo "deploy: give the full 40-character commit SHA (got '${sha}')" >&2; exit 2; }
cd "$(dirname "$0")"
export TP_IMAGE_TAG=$sha
C="docker compose -f compose.yaml -f compose.release.yaml --profile tools"
$C pull db caddy migrate app ingest transform backup restore
start=$(date +%s)
# Compose recreates a changed dependency of app (db) but never a dependent
# (caddy), so caddy is named too.
$C up -d --wait app caddy
elapsed=$(( $(date +%s) - start ))
# Compose reads .env from the directory it runs in: from now on a plain
# `docker compose ...` here (the backup and restore-test timers, tools runs,
# `up -d`) uses this release instead of the source-built *:latest images.
# A command with its own -f ignores COMPOSE_FILE.
printf '%s\n' "# Written by deploy.sh on every deploy; don't edit. Delete it to go back to source builds." \
  "COMPOSE_PATH_SEPARATOR=:" "COMPOSE_FILE=compose.yaml:compose.release.yaml" "TP_IMAGE_TAG=$sha" > .tmp.env
mv -f .tmp.env .env
digest=$(docker image inspect --format '{{index .RepoDigests 0}}' "ghcr.io/muddl/thymeandplace-app:$sha")
mkdir -p /var/lib/thymeandplace
echo "$(date -u +%FT%TZ) $sha $digest ${elapsed}s" >> /var/lib/thymeandplace/deploys.log
echo "deploy ok: $sha ($digest); maintenance window about ${elapsed}s"
