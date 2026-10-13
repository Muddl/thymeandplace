<!--
  Use Closes #N, Fixes #N, or Refs #N in this body.
  The linked issue must have a "## Acceptance criteria" heading.
  Only PM Farmer applies the no-issue label (dependency bumps, docs-only, typos).
  Ways of working: docs/WAYS-OF-WORKING.md
-->

## Linked issue

Closes #

## Acceptance criteria

Copy the checkboxes from the issue. Link each item to its evidence.

- [ ] Criterion. Evidence: `TestName` on CI run (URL) for this head SHA.
- [ ] Criterion. Evidence: recorded manual run whose log prints `git rev-parse HEAD` matching this head SHA. Use this only for work CI cannot cover, such as database stop and start timing, or a screen-reader pass.

A mutation check counts only when this pull request shows the failing output.

## Verify evidence

<!-- Test name plus a green CI run on the head commit, or a recorded manual run. -->

## Sign-offs

Required labels are listed in `.github/pr-signoffs.json`. A label counts only with a comment `<Role> Farmer: approved <head-sha-prefix>` that matches this head SHA. The last matching line per role wins. Until #79, only `Muddl` comments count. Apply the label after the comment so `governance` re-runs on this head SHA.

- [ ] `arch-approved` (migrations, store, `deploy/db/**`, Caddyfile, httpx, config, archtest, `cmd/*/app.go`, `go.mod`, `go.sum`, CSP and security headers)
- [ ] `arch-approved` or `manager-approved` (`.github/pr-signoffs.json`, `cmd/prcheck/**`, `internal/prcheck/**`)
- [ ] `liveops-approved` or `arch-approved` (other `deploy/`, Compose)
- [ ] `devops-approved` (workflows, Dockerfiles)
- [ ] `ux-approved` (`views/`, `static/`)
- [ ] `qa-approved` (`e2e/**`, `playwright.config.*`, and when this pull request has `launch-scope`)

QA Farmer may rerun key tests and mutation checks for a `launch-scope` pull request on its own cloud agent before signing off.
