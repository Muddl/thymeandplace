# Historical Plans and Comment Policy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mark the 2026-09-25 implementation plans as historical, add a comment policy to `CONTRIBUTING.md`, and fix the stale comments the audit cited. There is no behaviour change.

**Architecture:** Documentation and comment edits only.
- A new `docs/superpowers/plans/README.md` explains what the plans are, and a status line under each historical plan's title points at it.
- A "Comments" bullet joins `CONTRIBUTING.md`'s house rules.
- Five comment sites in Go code are reworded to describe current behaviour.

**Tech Stack:** Markdown, Go (comment lines only).

**Spec:** `docs/superpowers/specs/2026-09-28-historical-plans-and-comment-policy-design.md`. Issue [#21](https://github.com/Muddl/thymeandplace/issues/21).

## Global Constraints

- The `.go` diff touches comment lines only. `go build ./...` and `go test ./...` pass with no other change.
- Only the ten `2026-09-25-thymeandplace-*` plans are marked historical. The `2026-09-28-*` plans stay live.
- The status line, exactly: `- **Status:** Historical (executed 2026-09-25, not maintained). The contract is the core spec.`
- Applied migrations aren't edited.
- There is no mass comment rewrite. Change only the five sites in Task 2.

## Review Focus

1. **A reworded comment that no longer matches the code.** Each rewrite must describe what the code does **now**. Read the code under each comment before changing it. Task 2's steps quote the code each new comment must match.
2. **The status line breaking a plan's front matter.** It goes after the `# Title` line, and before the "For agentic workers" block or the `**Spec:**` line, so each plan still starts with its title. Task 1 checks the first three lines of every plan.
3. **The `csrf()` rewrite dropping one of its three invariants.** Task 2 lists all three, and a reviewer checks the diff against them.
4. **`gofmt` changes from a comment edit** (for example trailing spaces). Task 2 runs `gofmt -l`.
5. **README links that don't resolve.** Paths in `docs/superpowers/plans/README.md` must point at files that exist. Task 1 checks each with `ls`.

---

### Task 1: Mark the historical plans, and add the comment policy

**Files:**
- Create: `docs/superpowers/plans/README.md`
- Modify: the ten `docs/superpowers/plans/2026-09-25-thymeandplace-*.md` (a status line under each title)
- Modify: `CONTRIBUTING.md` ("House rules")

**Interfaces:** none.

- [ ] **Step 1: Write the README**

Create `docs/superpowers/plans/README.md`:

```markdown
# Implementation plans

## Historical: sub-project 1 (2026-09-25)

The ten `2026-09-25-thymeandplace-*` plans are the record of how sub-project 1 was built, task by task, on 2026-09-25, against revision 2 of the core spec. They are **not maintained** and don't describe the current code.

The contract is the core spec, `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md`, plus the design specs next to it in `docs/superpowers/specs/`.

Known divergences from what shipped:

- The winter-kill rule for frost-tender fruit (core spec §12.3, "Ruling G") is in no plan.
- The About page copy differs from plan 05. The shipped text is `internal/web/views/about.templ`.
- Everything from the 2026-09-27 architectural audit onward: the public DB gate and per-cell cache, the seafood feature flag, least-privilege database roles and the migrate service, site-wide security headers, admin abuse resistance and the reference-data license gate. `git log --since=2026-09-26` lists these changes.

### `Review Focus N` in tests

A test comment such as `// Review Focus 3.` refers to item 3 of the `## Review Focus` section of the plan whose task created that test. To find the plan, `git log --follow --diff-filter=A -- <test file>` gives the commit that added the file, and its message names the plan and task.

## Live plans

Plans dated after the audit (`2026-09-28-*`) are current work. Each names its design spec and GitHub issue. A plan gets the historical status line once it has been executed.
```

- [ ] **Step 2: Check the README's paths**

Run: `ls docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md internal/web/views/about.templ && ls docs/superpowers/plans/2026-09-25-thymeandplace-*.md | wc -l`
Expected: both paths listed, then `10`.

- [ ] **Step 3: Add the status line to each historical plan**

For each `docs/superpowers/plans/2026-09-25-thymeandplace-*.md`, insert a blank line and then the status line directly after line 1, the `# …` title:

```bash
for f in docs/superpowers/plans/2026-09-25-thymeandplace-*.md; do
  sed -i '1a\
\
- **Status:** Historical (executed 2026-09-25, not maintained). The contract is the core spec.' "$f"
done
```

- [ ] **Step 4: Check every plan**

Run: `grep -L 'Status:\*\* Historical' docs/superpowers/plans/2026-09-25-*.md; for f in docs/superpowers/plans/2026-09-25-*.md; do head -3 "$f"; echo; done`
Expected: the `grep -L` prints nothing. Each plan starts with its `# …` title, a blank line and the status line. If `sed` added CRLF or stray characters on Windows, fix them by hand.

- [ ] **Step 5: Comment policy in `CONTRIBUTING.md`**

In "## House rules", after the "**Docs.**" bullet, add:

```markdown
- **Comments.** Say why, and state the invariants the code relies on; don't restate the code. Don't narrate history ("used to", "previously", "the old X", review rounds): that goes in the commit message. Cite only durable sources: a core spec section, a design spec, or an issue or PR number. A test comment is one line naming the behaviour under test. Apply this to a file when you touch it; don't rewrite comments en masse.
```

- [ ] **Step 6: Commit**

```bash
git add docs/superpowers/plans/ CONTRIBUTING.md
git commit -m "docs: mark the 2026-09-25 plans historical and add a comment policy (#21)"
```

### Task 2: Fix the cited comments

**Files:**
- Modify: `internal/catalog/catalogjson/catalogjson.go:83-87`
- Modify: `cmd/server/app.go:125`
- Modify: `cmd/server/app_test.go:97-102`
- Modify: `internal/changeset/changeset_test.go:132-134`
- Modify: `internal/admin/admin.go` (the comment blocks inside `csrf()`, about lines 191-243)

**Interfaces:** none. Comments only.

- [ ] **Step 1: `catalogjson.go`**

Replace the comment above `var doc struct` with:

```go
	// Decode the document with ingredients left as raw JSON, then each item
	// with its own decoder, so a type error (such as a non-integer
	// months_to_harvest) can name the ingredient it came from. Decoding the
	// whole tree at once reports such errors with no item index or slug.
```

- [ ] **Step 2: `cmd/server/app.go:125`**

`mux     *http.ServeMux // Plans 6-7 mount /admin/ here` becomes:

```go
	mux     *http.ServeMux // the public site at /, and /admin when ADMIN_SECRET_KEY is set
```

Check `newApp` first: it mounts `public` at `/`, and `adminH` at `/admin` and `/admin/` only when the key is 32 bytes. If that has changed, describe what it does now.

- [ ] **Step 3: `cmd/server/app_test.go`**

Replace the comment above `TestAdminMountedWithCatalogDeps` with:

```go
// TestAdminMountedWithCatalogDeps: with ADMIN_SECRET_KEY set, newApp gives
// the admin handler its catalog dependencies, so a signed-in GET /admin
// renders the catalog. The session is minted with auth.Service.CreateSession
// rather than a WebAuthn ceremony.
```

- [ ] **Step 4: `changeset_test.go`**

Replace the comment above `TestValidateReportsFieldAndMissingEcoregionTogether` with:

```go
// Validate reports field problems and missing ecoregions together, even when
// they are on different items in the same change.
```

- [ ] **Step 5: `csrf()` in `internal/admin/admin.go`**

Replace the four comment blocks inside `csrf()`, 29 lines today, with these 12 lines, each placed where its block was. Code lines don't change.

- Above `if r.ContentLength > maxBody {`:
  ```go
		// A known Content-Length over maxBody is refused before anything reads the body.
  ```
- Above the logout exemption `if _, ok := SessionFrom(...); !ok && ... "/admin/logout" {`:
  ```go
		// Logout without a session has nothing to protect, so it passes and
		// redirects to sign-in. With a session, logout needs the token.
  ```
- Above `if err := r.ParseMultipartForm(maxBody); err != nil {`:
  ```go
		// Parse before the token check, so a chunked body over maxBody (which
		// http.MaxBytesHandler cuts off) answers 413, not a misleading 403.
		// maxBody is also the in-memory limit, so nothing spills to a temp
		// file; RemoveAll below is defensive.
  ```
- Above `if _, ok := SessionFrom(r.Context()); !ok && r.Header.Get("HX-Request") == "true" {`:
  ```go
			// A signed-out HTMX request is sent to sign-in rather than given a
			// plain-text 403 to swap into the page (as requireAuth does).
  ```

These keep the three invariants the spec names:
1. Content-Length is checked first.
2. The form is parsed before the token check (413, not 403).
3. Logout without a session is exempt.

- [ ] **Step 6: Verify**

Run:

```bash
gofmt -l cmd internal content
go vet ./... && go build ./...
go test ./cmd/server/ ./internal/admin/ ./internal/changeset/ ./internal/catalog/... -count=1
grep -rn 'old \[\]Ingredient\|previously left zero\|Validate used to\|Plans 6-7' cmd internal
git diff -U0 -- '*.go' | grep '^[+-][^+-]' | grep -v '^\s*[+-]\s*//' | grep -v '^[+-]\s*$'
```

Expected:
- `gofmt` prints nothing.
- vet, build and tests pass.
- The stale-phrase `grep` prints nothing.
- The last command prints nothing, so every changed Go line is a comment or blank.
- `sed -n '/func (a \*Admin) csrf/,/^}/p' internal/admin/admin.go | grep -c '//'` prints 12 or fewer.

- [ ] **Step 7: Commit**

```bash
git add internal/ cmd/
git commit -m "comments: describe current behaviour in the comments the audit cited (#21)"
```
