# Contributing

Thyme & Place is a private, proprietary project (see [LICENSE.md](LICENSE.md)). Contributions are by invitation: if you have access to this repository, you're welcome to open issues and pull requests.

## Your rights in a contribution

By opening a pull request, you confirm that:

1. the work is yours to give, or you have the right to submit it; and
2. it contains no third-party code, text or data unless that material's license allows this use and you've recorded it in the PR (and, for data, in `docs/reference-data-licenses.md`).

You also grant the copyright holder a perpetual, worldwide, irrevocable, royalty-free license to use, copy, modify, distribute, sublicense and relicense your contribution under any terms. That includes proprietary terms and any open-source license the project may adopt later. For substantial contributions, you may be asked to sign a written contributor agreement before your change is merged.

## Before you start

- For anything larger than a fix, open an issue first, or write a short design spec in `docs/superpowers/specs/` (see the existing ones for the shape). The core design is `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md`.
- Branch from `main` and keep each pull request to one change.
- To report a security problem, tell the owner directly, not in an issue.

## Before you open a pull request

```bash
go tool templ generate            # after editing a .templ file
./scripts/sqlc.sh generate        # after editing a query or migration
gofmt -l cmd internal content     # prints nothing when formatted
go vet ./...
go tool staticcheck ./...         # lint
go tool govulncheck ./...         # fails only on vulnerabilities your code can reach
go test ./...                     # needs Docker for the PostGIS test containers
python -m pytest pipeline/tests -q   # when you touch pipeline/
e2e/run.sh                        # when you change pages, scripts or the Compose stack
```

`e2e/run.sh` defaults to Chromium against the local Compose stack. Tag and browser selection are in `e2e/README.md`: `GREP=@readonly` is the smoke subset, `GREP=@mutating` is `--grep-invert @readonly`, and `E2E_BASE_URL` points the runner at another origin only with `GREP=@readonly`.

Commit the generated files. CI fails when they're stale.

CI also runs the Go tests with the race detector (`go test -race -count=1 ./...`). `-race` needs cgo, so run it on Linux or macOS with a C compiler, or in the `golang` image on Windows (mount the Docker socket for the test containers). When you change a shell script under `deploy/`, `e2e/` or `scripts/`, run shellcheck the way CI does (the pinned image and file list are in the `shellcheck` step of `.github/workflows/ci.yml`):

```bash
docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:v0.11.0 -x deploy/*.sh deploy/backup/*.sh deploy/db/initdb/*.sh e2e/*.sh scripts/*.sh
```

## House rules

These are enforced by tests where they can be. Where they can't, reviewers check them.

- **Honest copy.** Every result is labeled as an estimate, and every error message says what happened and what to do next (core spec §11.6). Never give foraging advice for wild mushrooms.
- **No seafood while it's off.** With `FEATURE_SEAFOOD` off, no public page may mention seafood or marine data (`internal/web/seafood_test.go`). New public copy goes through the same check.
- **Content Security Policy.** No inline `<script>`, `style=` attributes, `on*=` handlers or `hx-on` in templates (`internal/web/views/lint_test.go`). Put behavior in `static/app.js` and styles in `static/app.css`.
- **Least privilege.** The app runs as `tp_app`. A migration that adds a table or function also grants `tp_app` exactly what the app needs, and nothing more. Never edit a migration that has already shipped; add a new one.
- **Reference data.** A new dataset or source file needs a row in `docs/reference-data-licenses.md` and an `Attribution()` that the About page shows. A license with the `UNRESOLVED:` marker is never approved.
- **Catalog content.** Follow `content/AUTHORING.md`. After editing in the admin, export to `content/catalog/launch.json` and run `go test ./content/`.
- **Docs.** When behavior changes, update `docs/operations.md` and the relevant spec in the same pull request.

## Commit messages

Write in the imperative ("Add sitemap.xml", not "Added…"). Say why in the body when the diff doesn't make it obvious.
