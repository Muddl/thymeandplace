#!/usr/bin/env bash
# usage (as root, from a checkout of the SHA): ./deploy/staging-remote.sh <40-hex commit SHA>
set -euo pipefail
sha=${1:-}
[[ $sha =~ ^[0-9a-f]{40}$ ]] || { echo "deploy: give the full 40-character commit SHA (got '${sha}')" >&2; exit 2; }
cd "$(dirname "$0")"
wait_image() {
	local name=$1 i=0
	while [ "$i" -lt 120 ]; do
		if docker manifest inspect "ghcr.io/muddl/thymeandplace-${name}:${sha}" >/dev/null 2>&1; then
			return 0
		fi
		i=$((i + 1))
		sleep 20
	done
	echo "staging: ghcr.io/muddl/thymeandplace-${name}:${sha} is not in GHCR yet" >&2
	return 1
}
wait_image app
wait_image ingest
wait_image transform
wait_image backup
export TP_COMPOSE_OVERLAY=compose.staging.yaml
exec ./deploy.sh "$sha"
