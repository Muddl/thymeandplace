# AGENTS.md

Thyme & Place: a Go + templ + Postgres/PostGIS site that shows what's in season near you. Layout, stack and config are in `README.md`. `CONTRIBUTING.md` is the full contributor guide, and this file is its short form for agents.

## How to work

- Route non-trivial work through `/poteto-mode`. Give it a goal, a done-check (the command or observation that proves it's finished), and the proof you'll show (test output, a screenshot, a trace). The skills it uses are vendored in `.cursor/skills/`, so they work on cloud agents. See `.cursor/skills/PSTACK_VENDORED.md` for what's there, the model fallback when `~/.cursor/rules/pstack-models.mdc` is missing, and how to update.
- Verify UI changes with `/verify-thymeandplace` once it exists. Until then, drive the real app yourself (see "Dev stack") and attach screenshots. `/create-verification-skill` generates it.
- Branch from `main` and keep each PR to one change.
- Never merge your own PR and never enable auto-merge. A human merges.

## House rules

Tests enforce some of these. Reviewers check the rest.

- **Estimates.** Every result is labeled an estimate. An error message says what happened and what to do next. Never give foraging advice for wild mushrooms.
- **No seafood while `FEATURE_SEAFOOD` is off.** No public page may mention seafood or marine data (`internal/web/seafood_test.go`). New public copy goes through the same check.
- **CSP.** No inline `<script>`, `style=` attributes, `on*=` handlers or `hx-on` in templates (`internal/web/views/lint_test.go`). Behavior goes in `static/app.js`, styles in `static/app.css`.
- **Least-privilege DB roles.** The app runs as `tp_app`. A migration that adds a table or function grants `tp_app` exactly what the app needs, and nothing more.
- **Never edit a shipped migration.** Add a new one. `migrations/checksums` and `go test ./migrations` enforce that. An Up-section `DROP` or `RENAME` needs `-- contract:` (`docs/operations.md`, "Schema migrations").
- **Docs in the same PR.** When behavior changes, update `docs/operations.md` and the relevant spec under `docs/superpowers/specs/`.
- **Reference data.** A new dataset needs a row in `docs/reference-data-licenses.md` and an `Attribution()` the About page shows. Never approve a license marked `UNRESOLVED:`.
- **Third-party code.** Record it in the PR and in `LICENSE.md`.
- **Catalog content.** Follow `content/AUTHORING.md`, then run `go test ./content/`.

## Before opening a PR

```bash
go tool templ generate               # after editing a .templ file
./scripts/sqlc.sh generate           # after editing a query or migration (needs Docker)
gofmt -l cmd internal content        # must print nothing
go vet ./...
go test ./...                        # needs Docker for the PostGIS test containers
python -m pytest pipeline/tests -q   # when you touch pipeline/
e2e/run.sh                           # when you change pages, scripts or the Compose stack
```

Commit the generated files. CI fails when they're stale. Write commit messages in the imperative.

## Dev stack

Needs Go 1.26 and Docker. Node 22 (e2e) and Python 3.12 (pipeline tests) are optional.

```bash
# Postgres with the production roles, on localhost:55432
docker compose -f deploy/compose.dev.yaml up -d

# Migrate and seed synthetic reference data and a small catalog, as tp_owner
DEVSEED_ALLOW=1 DATABASE_URL='postgres://tp_owner:tp_owner@localhost:55432/thymeandplace?sslmode=disable' go run ./cmd/devseed

# Run the app as the least-privilege role, then open http://localhost:8080 and search "Lisbon"
DATABASE_URL='postgres://tp_app:tp_app@localhost:55432/thymeandplace?sslmode=disable' go run ./cmd/server
```

For `/admin`, also set `ADMIN_SECRET_KEY` (`openssl rand -base64 32`), `ADMIN_RP_ID=localhost` and `ADMIN_ORIGIN=http://localhost:8080`.

## Skills and subagents

- Skills live in `.cursor/skills/` and subagents in `.cursor/agents/`. They are vendored from upstream, so don't hand-edit them. Run `scripts/sync-pstack.sh` instead.
- Project-specific skills (for example `verify-thymeandplace`) go in their own `.cursor/skills/<name>/` folder and are not part of the pstack sync.
- Without a per-user model rule, use the defaults in `.cursor/skills/PSTACK_VENDORED.md`.
- The session's skill list is built at startup. If a skill or subagent named here isn't in it, read `.cursor/skills/<name>/SKILL.md` directly and follow it. For `poteto-agent`, spawn a `generalPurpose` subagent and tell it to read `.cursor/skills/poteto-mode/SKILL.md` in full before anything else.
