# Thyme & Place - Historical plans and comment policy - Design

- **Status:** Proposed
- **Date:** 2026-09-28
- **Source:** Architectural audit, 2026-09-27 (finding F21)
- **Related issues:** [#21](https://github.com/Muddl/thymeandplace/issues/21) (F21)
- **Amends:** `CONTRIBUTING.md` ("House rules" gains a "Comments" rule). No core spec change.

---

## 1. Problem

Many comments describe past bugs, earlier code or review rounds instead of current behaviour, and some cite review notes that exist nowhere in the repo. They go stale once the history is forgotten, and commit messages are the place for history.

- **Narrating the past:**
  - `internal/catalog/catalogjson/catalogjson.go:84` ("as the old []Ingredient field did")
  - `cmd/server/app_test.go:99` ("previously left zero, …")
  - `internal/changeset/changeset_test.go:132` ("Validate used to return …")
- **Stale:** `cmd/server/app.go:125`, `mux *http.ServeMux // Plans 6-7 mount /admin/ here`. Admin is already mounted in `newApp`.
- **Review references:** about 80 comments across Go, TypeScript and shell.
  - `Review Focus N` points to the `## Review Focus` section of the plan whose task wrote the test.
  - `Final review N`, `Review finding N` and `fix round 1` have no source in the repo at all.
- **`csrf()` in `internal/admin/admin.go`:** four comment blocks. Their content is the kind to keep: ordering and invariants, not history. But they are long enough to hide those invariants.

The ten `docs/superpowers/plans/2026-09-25-*` files (~20k lines) record how sub-project 1 was built against core spec revision 2 on 2026-09-25. Nothing marks them as historical, so a reader or coding agent can mistake them for the current design. They already diverge from what shipped:
- the Ruling G winter-kill rule (core spec §12.3) is in no plan;
- plan 05's About copy differs from `internal/web/views/about.templ`;
- every change from the 2026-09-27 audit (the public DB gate, seafood feature flag, least-privilege roles, security headers, admin abuse resistance, the license gate) postdates them.

Since the audit, `CONTRIBUTING.md` and `README.md` have been added (`ac652c1`). `CONTRIBUTING.md` has "House rules" but no rule about comments.

## 2. Goals and non-goals

Goals:

1. Anyone opening `docs/superpowers/plans/` learns that the 2026-09-25 plans are historical, and where the current contract is.
2. `Review Focus N` comments in tests can be traced to their source.
3. The repo has a short, written comment policy where contributors already look.
4. The comments the audit cited no longer narrate history, and `csrf()` states its three invariants briefly.
5. No behaviour changes.

Non-goals:

- Rewriting every review-reference comment. The policy applies to a file when it is next touched.
- Editing applied migrations (for example `migrations/00002_reference.sql:18`). Shipped migrations are never edited (`CONTRIBUTING.md`).
- Updating the historical plans to match the code.
- Marking the live `2026-09-28-*` plans as historical. They are the current work queue, and are marked when executed.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| Where the policy lives | A "Comments" bullet in `CONTRIBUTING.md` "House rules" | A new file (the issue predates `CONTRIBUTING.md`; one place is easier to find). `README.md` (for users of the repo, not contributors) |
| How plans are marked | A `docs/superpowers/plans/README.md` **and** a status line under each historical plan's title | README only (someone who opens a plan directly never sees it). Moving the plans to an `archive/` directory (breaks every link and `Review Focus` pointer) |
| Listing divergences | Name the known ones and point at `git log --since=2026-09-26` for the rest | A complete list (it would itself go stale) |
| `Review Focus N` comments | Keep them. The README explains how to trace them | Rewrite all ~56 (a mass comment rewrite the policy itself advises against) |
| Unsourced review comments (`Final review N`, …) | Left, and fixed under the policy when their file is next touched | Fixing all now (same reason) |

## 4. Design

### 4.1 `docs/superpowers/plans/README.md`

Contents:

- **Historical plans.** The ten `2026-09-25-thymeandplace-*` plans are the historical record of how sub-project 1 was built on 2026-09-25, against core spec revision 2. They aren't maintained.
- **The contract.** The contract is the core spec (`docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md`) plus the design specs in `docs/superpowers/specs/`.
- **Known divergences:**
  - the Ruling G winter-kill rule (core spec §12.3);
  - the About page copy;
  - everything from the 2026-09-27 audit fixes onward (`git log --since=2026-09-26`).
- **Tracing `Review Focus N`.** A test comment `Review Focus N` refers to item N of the `## Review Focus` section of the plan whose task created that test. `git log --follow` on the test file finds the commit, and so the plan.
- **Live plans.** Plans dated after the audit (`2026-09-28-*`) are live work, each linked to its design spec and issue. They get the historical status line once executed.

### 4.2 Status line

Under the title of each `2026-09-25-*` plan, add:

```markdown
- **Status:** Historical (executed 2026-09-25, not maintained). The contract is the core spec.
```

### 4.3 `CONTRIBUTING.md`

Add to "House rules":

```markdown
- **Comments.** Say why, and state the invariants the code relies on; don't restate the code. Don't narrate history ("used to", "previously", "the old X", review rounds): that goes in the commit message. Cite only durable sources: a core spec section, a design spec, or an issue or PR number. A test comment is one line naming the behaviour under test. Apply this to a file when you touch it; don't rewrite comments en masse.
```

### 4.4 Comment fixes

| Location | Change |
|---|---|
| `internal/catalog/catalogjson/catalogjson.go:84` | Describe why the current code decodes the way it does, without "the old field" |
| `cmd/server/app.go:125` | `mux *http.ServeMux // public site at /, admin at /admin/ (when enabled)`, or drop the comment |
| `cmd/server/app_test.go:97-102` | Name the behaviour: with `ADMIN_SECRET_KEY` set, `newApp` gives admin its catalog dependencies, and a signed-in `GET /admin` renders the catalog. Keep the note that the session is minted directly rather than through WebAuthn. Drop "(previously left zero, …)" |
| `internal/changeset/changeset_test.go:132` | One line naming the behaviour: `Validate` reports field problems and cross-reference problems together |
| `csrf()` comment blocks, `internal/admin/admin.go` | At most 12 lines in total, keeping the three invariants: a known Content-Length over the limit is refused before any parsing; the form is parsed before the token check, so an oversized chunked body answers 413, not 403; logout without a session is exempt |

Read each comment's surrounding code before rewording, so the new text describes what the code does now.

## 5. Implementation outline

1. `docs/superpowers/plans/README.md`.
2. The status line in each of the ten `2026-09-25-*` plans.
3. `CONTRIBUTING.md` "Comments" rule.
4. The five comment fixes.

## 6. Testing

- `gofmt -l cmd internal content` prints nothing, and `go vet ./...` and `go test ./...` pass. The diff to `.go` files touches only comment lines.
- `grep -L 'Status:\*\* Historical' docs/superpowers/plans/2026-09-25-*.md` prints nothing.
- `grep -n 'old \[\]Ingredient\|previously left zero\|Validate used to\|Plans 6-7' -r cmd internal` prints nothing.
- The `csrf()` function's comment lines number 12 or fewer. Check by reading the diff; it's small enough.

## 7. Rollout, operations and rollback

Documentation and comments only. There is nothing to deploy or roll back.

## 8. Open questions for the owner

None.
