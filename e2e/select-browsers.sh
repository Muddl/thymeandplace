#!/usr/bin/env bash
set -euo pipefail

all='["chromium","firefox","webkit"]'
chromium_only='["chromium"]'

ui_path() {
  case "$1" in
    views | views/* | */views | */views/*) return 0 ;;
    static | static/*) return 0 ;;
    e2e | e2e/*) return 0 ;;
  esac
  return 1
}

wants_all_browsers() {
  while IFS= read -r f || [ -n "$f" ]; do
    [ -z "$f" ] && continue
    if ui_path "$f"; then
      return 0
    fi
  done
  return 1
}

changed_files() {
  if [ "${FORCE_DIFF_FAIL:-}" = 1 ]; then
    echo "select-browsers: forced diff failure" >&2
    return 1
  fi
  if [ -n "${CHANGED_FILES+x}" ]; then
    printf '%s' "$CHANGED_FILES"
    return 0
  fi
  if [ -n "${GITHUB_BASE_SHA:-}" ]; then
    git fetch --no-tags --depth=1 origin "${GITHUB_BASE_SHA}" >/dev/null 2>&1 || true
    if git cat-file -e "${GITHUB_BASE_SHA}^{commit}" 2>/dev/null; then
      git diff --name-only "${GITHUB_BASE_SHA}" HEAD
      return 0
    fi
    echo "select-browsers: cannot resolve GITHUB_BASE_SHA ${GITHUB_BASE_SHA}" >&2
    return 1
  fi
  if [ -n "${GITHUB_BASE_REF:-}" ]; then
    if git fetch --no-tags --depth=1 origin "${GITHUB_BASE_REF}" >/dev/null 2>&1; then
      git diff --name-only "origin/${GITHUB_BASE_REF}...HEAD"
      return 0
    fi
    echo "select-browsers: git fetch/diff against origin/${GITHUB_BASE_REF} failed" >&2
    return 1
  fi
  echo "select-browsers: no CHANGED_FILES, GITHUB_BASE_SHA, or GITHUB_BASE_REF" >&2
  return 1
}

select_browsers() {
  local event="${GITHUB_EVENT_NAME:-}"
  local ref="${GITHUB_REF:-}"
  if [ "$event" = push ] && [ "$ref" = refs/heads/main ]; then
    printf '%s\n' "$all"
    return
  fi
  if [ "$event" = pull_request ]; then
    local files
    if ! files=$(changed_files); then
      echo "select-browsers: diff failed, using all engines" >&2
      printf '%s\n' "$all"
      return
    fi
    if printf '%s\n' "$files" | wants_all_browsers; then
      printf '%s\n' "$all"
    else
      printf '%s\n' "$chromium_only"
    fi
    return
  fi
  printf '%s\n' "$chromium_only"
}

self_test() {
  local got
  expect() {
    local name="$1" want="$2"
    got=$(select_browsers)
    if [ "$got" != "$want" ]; then
      echo "select-browsers self-test failed: $name" >&2
      echo "  got:  $got" >&2
      echo "  want: $want" >&2
      exit 1
    fi
  }

  GITHUB_EVENT_NAME=push GITHUB_REF=refs/heads/main expect "push to main" "$all"

  GITHUB_EVENT_NAME=pull_request GITHUB_REF=refs/heads/feature \
    CHANGED_FILES='cmd/server/main.go' \
    expect "PR with Go-only files" "$chromium_only"

  GITHUB_EVENT_NAME=pull_request CHANGED_FILES='static/app.js' \
    expect "PR touching static/" "$all"

  GITHUB_EVENT_NAME=pull_request CHANGED_FILES='internal/web/views/season.templ' \
    expect "PR touching public views/" "$all"

  GITHUB_EVENT_NAME=pull_request CHANGED_FILES='internal/admin/views/login.templ' \
    expect "PR touching admin views/" "$all"

  GITHUB_EVENT_NAME=pull_request CHANGED_FILES='e2e/run.sh' \
    expect "PR touching e2e/" "$all"

  GITHUB_EVENT_NAME=pull_request CHANGED_FILES='views/layout.templ' \
    expect "PR touching top-level views/" "$all"

  GITHUB_EVENT_NAME=pull_request CHANGED_FILES=$'internal/web/server.go\ndocs/operations.md' \
    expect "PR with no UI paths" "$chromium_only"

  GITHUB_EVENT_NAME=pull_request CHANGED_FILES=$'cmd/server/main.go\ne2e/playwright.config.ts' \
    expect "PR with mixed Go and e2e" "$all"

  GITHUB_EVENT_NAME=push GITHUB_REF=refs/heads/feature CHANGED_FILES='static/app.js' \
    expect "push to a feature branch" "$chromium_only"

  GITHUB_EVENT_NAME=pull_request FORCE_DIFF_FAIL=1 expect "diff failure fail-closed" "$all"

  unset GITHUB_EVENT_NAME GITHUB_REF CHANGED_FILES FORCE_DIFF_FAIL
  expect "local default" "$chromium_only"

  echo "select-browsers self-test ok"
}

if [ "${1:-}" = --self-test ]; then
  self_test
  exit 0
fi

select_browsers
