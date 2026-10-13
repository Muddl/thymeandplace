#!/bin/sh
set -eu

fail() {
	echo "staging-ci-check: $*" >&2
	exit 1
}

overlay=
C="docker compose -f compose.yaml -f compose.release.yaml${overlay:+ -f $overlay} --profile tools"
compose_file="compose.yaml:compose.release.yaml${overlay:+:$overlay}"
[ "$C" = "docker compose -f compose.yaml -f compose.release.yaml --profile tools" ] ||
	fail "unset overlay changed the compose command: $C"
[ "$compose_file" = "compose.yaml:compose.release.yaml" ] ||
	fail "unset overlay changed COMPOSE_FILE: $compose_file"
overlay=compose.staging.yaml
C="docker compose -f compose.yaml -f compose.release.yaml${overlay:+ -f $overlay} --profile tools"
compose_file="compose.yaml:compose.release.yaml${overlay:+:$overlay}"
[ "$C" = "docker compose -f compose.yaml -f compose.release.yaml -f compose.staging.yaml --profile tools" ] ||
	fail "staging overlay compose command is $C"
[ "$compose_file" = "compose.yaml:compose.release.yaml:compose.staging.yaml" ] ||
	fail "staging COMPOSE_FILE is $compose_file"
grep -Fq 'TP_COMPOSE_OVERLAY' deploy/deploy.sh ||
	fail "deploy.sh must honor TP_COMPOSE_OVERLAY"

gate=./deploy/staging-gate.sh
[ -x "$gate" ] || fail "$gate is not executable"

for missing in STAGING_SSH_KEY STAGING_HOST STAGING_SSH_HOST_KEY STAGING_BASIC_AUTH; do
	out=$(
		STAGING_SSH_KEY=k STAGING_HOST=h STAGING_SSH_HOST_KEY=hk STAGING_BASIC_AUTH=u:p
		eval "$missing="
		export STAGING_SSH_KEY STAGING_HOST STAGING_SSH_HOST_KEY STAGING_BASIC_AUTH
		"$gate" 2>/dev/null
	) || fail "$gate exited $? when $missing is empty"
	[ "$out" = false ] || fail "$gate printed '$out' when $missing is empty, want false"
done

out=$(
	GITHUB_OUTPUT=$(mktemp)
	export GITHUB_OUTPUT
	STAGING_SSH_KEY=k STAGING_HOST=h STAGING_SSH_HOST_KEY=hk STAGING_BASIC_AUTH=u:p \
		"$gate" 2>/dev/null
	grep -qx 'ready=true' "$GITHUB_OUTPUT" || fail "GITHUB_OUTPUT was not ready=true"
	rm -f "$GITHUB_OUTPUT"
) || fail "$gate failed when every secret is set"

out=$(STAGING_SSH_KEY=k STAGING_HOST=h STAGING_SSH_HOST_KEY=hk STAGING_BASIC_AUTH=u:p "$gate" 2>/dev/null) ||
	fail "$gate exited $? when every secret is set"
[ "$out" = true ] || fail "$gate printed '$out' when every secret is set, want true"

wf=.github/workflows/staging.yml
[ -f "$wf" ] || fail "$wf is missing"
grep -q 'workflow_run:' "$wf" || fail "$wf must trigger on workflow_run"
grep -q 'workflow_dispatch:' "$wf" || fail "$wf must allow workflow_dispatch"
grep -q 'cancel-in-progress: false' "$wf" || fail "$wf must not cancel an in-flight deploy"
grep -q 'contents: read' "$wf" || fail "$wf must use contents: read"
grep -q 'actions/checkout@11d5960a326750d5838078e36cf38b85af677262' "$wf" ||
	fail "$wf must pin actions/checkout to the same SHA as ci.yml"
if grep -n 'StrictHostKeyChecking=no' "$wf"; then
	fail "$wf must not disable StrictHostKeyChecking"
fi
if grep -E ': write' "$wf"; then
	fail "$wf must not request write permissions"
fi

if grep -n 'staging.thymeand.place' deploy/Caddyfile deploy/compose.yaml deploy/compose.release.yaml; then
	fail "production compose/Caddyfile must not name staging.thymeand.place"
fi
if grep -nE 'basic_auth|basicauth' deploy/Caddyfile; then
	fail "production Caddyfile must not use basic auth"
fi

grep -q '^staging.thymeand.place {' deploy/Caddyfile.staging ||
	fail "Caddyfile.staging must serve staging.thymeand.place"
grep -q 'import /etc/caddy/basicauth' deploy/Caddyfile.staging ||
	fail "Caddyfile.staging must import the basic-auth snippet"
if grep -nE '^thymeand\.place \{|^www\.thymeand\.place \{' deploy/Caddyfile.staging; then
	fail "Caddyfile.staging must not serve the production hostnames"
fi

echo "staging-ci-check ok"
