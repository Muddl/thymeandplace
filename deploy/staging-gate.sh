#!/bin/sh
# Prints true or false. Always exits 0. Missing secrets are a skip.
set -eu
ready=true
for name in STAGING_SSH_KEY STAGING_HOST STAGING_SSH_HOST_KEY STAGING_BASIC_AUTH; do
	eval "v=\${$name:-}"
	if [ -z "$v" ]; then
		echo "staging: $name is unset; skipping deploy" >&2
		ready=false
	fi
done
if [ -n "${GITHUB_OUTPUT:-}" ]; then
	echo "ready=$ready" >>"$GITHUB_OUTPUT"
fi
echo "$ready"
