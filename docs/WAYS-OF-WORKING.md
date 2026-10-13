# Ways of working

This page is the team's process for issues, pull requests, vetoes, and merge. It builds on the roles in [`docs/RACI.md`](RACI.md) (PR #77). Read that page for who is Responsible and Accountable. Read this page for how a change moves.

## Veto rights

A blocking review keeps a pull request from going to Wade until the reviewer resolves it or Wade overrides it. Each veto has a limited scope. A review outside that scope is advice, not a block.

| Role | May block | May not block |
|---|---|---|
| Architect Farmer | Package boundaries, contracts, migrations, and security (CSP, passkeys, secrets, headers) | Style, or design that stays inside one module |
| QA Farmer | Launch-scoped work that has no tests or no verify evidence. Launch sign-off | How a non-launch change is built |
| UX Review Farmer | Accessibility or readability regressions on a user-facing pull request | Non-user-facing diffs |
| DevOps Farmer | CI, GitHub workflows, and Dockerfiles | Application code that does not change those files |
| LiveOps Farmer | Deploy scripts, Compose, Caddy, and backups | Application code that does not change those files |
| PM Farmer | Scope only. No linked ready issue, or a diff that goes past the issue's acceptance criteria | How the work is built |
| Feature farmer | Their area on that epic | Other epics |

Architect Farmer and LiveOps Farmer share `deploy/`. Either may veto a change there. `deploy/db/**` and Caddyfiles stay with Architect Farmer. Caddyfiles set TLS and headers, so LiveOps approval is not enough.

A required sign-off label is valid only with a pull request comment of the form `<Role> Farmer: approved <head-sha-prefix>` that matches the current head SHA. A push makes an old comment stale. See [Path sign-offs](#path-sign-offs).

## Disputes

The two farmers try once to settle the disagreement on the pull request. If they still disagree, Farmer Manager decides. If Farmer Manager cannot decide, Farmer Manager escalates to Wade with both positions in three lines.

## Definition of Ready

An issue is ready for a pull request when it has all of the following.

- A parent epic (or the issue is the epic).
- A `## Acceptance criteria` heading with checkbox items.
- A `Blocked by #N` line, or the line `Blocked by: none`.
- A role label for the farmer who owns the work.

## Definition of Done

A pull request is done when all of the following are true.

- CI is green on the head commit. That includes the `pr-governance` check once branch protection requires it.
- Every required path sign-off is present on the head SHA. See [Path sign-offs](#path-sign-offs).
- The pull request body has verify evidence, and each acceptance criterion links to its evidence.

Evidence for an acceptance criterion is one of these.

- A test name plus a green CI run on the head commit.
- A recorded manual run whose log prints `git rev-parse HEAD` matching the head SHA, for work CI cannot cover. Examples are database stop and start timing, and a screen-reader pass.

A mutation check counts only when the pull request shows the failing output.

QA Farmer may rerun key tests and mutation checks for a `launch-scope` pull request on its own cloud agent before signing off.

## Merges and GitHub

Farmers never merge. Farmers never enable auto-merge. Farmers never ask Wade to post on GitHub for them. Farmer Manager asks Wade to merge when the pull request meets Definition of Done and no veto is open.

## Work in progress

A farmer may have at most two open pull requests by default. Count one per issue stream, so stacked pull requests on one issue count as one. Farmer Manager can grant an exception.

## Path sign-offs

The `pr-governance` workflow runs on `pull_request` events (`opened`, `edited`, `synchronize`, `reopened`, `labeled`, `unlabeled`) and on `merge_group`. On a `pull_request` event, the workflow checks out the pull request's base SHA and runs that `cmd/prcheck`. The pull request is input through the GitHub API. On `merge_group`, the workflow passes without a checkout and without running `prcheck`. The skip is `github.event_name == 'merge_group'` only. A branch name, label, pull request body, or extra environment variable cannot trigger it. The workflow never runs on `issue_comment`, so a comment cannot attach a check to `main` or execute head code in the default-branch context.

If the base commit has no `cmd/prcheck` yet, the introducing pull request checks out the head for that first merge only.

A sign-off comment does not re-run the check by itself. Re-run the governance job, edit the pull request, or toggle a label afterwards.

The pull request body must contain `Closes #N`, `Fixes #N`, or `Refs #N`. The linked issue must contain a `## Acceptance criteria` heading. Dependabot pull requests skip the issue link. The `no-issue` label also skips the issue link. Only PM Farmer applies `no-issue`, and only for dependency bumps, docs-only changes, and typo fixes.

A path match requires its label and a matching approval comment on the current head SHA. The last matching approval line per role wins. Lines that start with `>` are ignored. Until the shared GitHub App (issue or PR #79) lands, only comments authored by `Muddl` count. That list lives in `trustedAuthors` in the map.

| Label | Role in the comment | Paths |
|---|---|---|
| `arch-approved` | Architect | `migrations/**`, `internal/store/**`, `deploy/db/**`, `deploy/Caddyfile*`, `internal/httpx/**`, `internal/config/**`, `internal/archtest/**`, `cmd/*/app.go`, `go.mod`, `go.sum`, `**/views/lint_test.go`, `internal/web/middleware.go`, `internal/web/headers_test.go`, `internal/admin/admin.go`, and `internal/admin/security*.go` |
| `arch-approved` or `manager-approved` | Architect or Manager | `.github/pr-signoffs.json`, `cmd/prcheck/**`, `internal/prcheck/**` |
| `liveops-approved` or `arch-approved` | LiveOps or Architect | Other `deploy/**` files and Compose files. Dockerfiles and Caddyfiles are excluded |
| `devops-approved` | DevOps | `.github/workflows/**` and Dockerfiles |
| `ux-approved` | UX Review | `**/views/**` and `static/**` |
| `qa-approved` | QA | `e2e/**`, `**/playwright.config.*`, and any pull request labeled `launch-scope` |

Example comment:

```
Architect Farmer: approved 7bf2a16
```

Manager sign-off uses `Manager Farmer: approved 7bf2a16`.

The SHA prefix must be at least seven hex characters and must match the current head commit.

## Known limitation

Everyone posts through Wade's GitHub account. Labels and comments can be spoofed until the shared GitHub App (issue or PR #79) lands. Treat a sign-off as a process record, not as authentication.

## Branch protection (Wade)

Do these steps in the GitHub UI. Do not change repository settings from an agent or a pull request.

1. Open the repository **Settings**, then **Branches**.
2. Add a branch protection rule for `main`.
3. Enable **Require a pull request before merging**, so nobody pushes to `main` directly.
4. Enable **Require status checks to pass before merging**. Add the checks `go`, `python`, `deploy-checks`, `governance`, and `e2e-result`. Those checks must run on `merge_group`. Do not require `backup`. That job runs only on `main`. #89 renames the compose job from `deploy` to `deploy-checks`. Until it merges, the check name is still `deploy`. When #89 rebases, keep the `merge_group` trigger.
5. Enable **Do not allow bypassing the above settings**.
6. Leave auto-merge disabled.
7. After the first pull request that adds `pr-governance` is merged, confirm that the `governance` check appears on the next pull request before you add it to the required list.

## Merge queue (Wade)

Do these steps in the GitHub UI. Do not change repository settings from this pull request.

1. Open **Settings**, then **Branches**, then edit the `main` rule.
2. Enable **Require merge queue**.
3. Keep the required checks `go`, `python`, `deploy-checks`, `governance`, and `e2e-result`.
4. Do not require `backup`.
5. Until #89 merges, the compose job check is still named `deploy`. After #89, require `deploy-checks` instead. When #89 rebases onto this change, keep the `merge_group` trigger. #86 also edits `ci.yml`, and it has already merged.
6. Leave **Allow auto-merge** off.
7. Disable **Require this branch to be up to date before merging**. The queue already rebuilds each entry against `main`.
