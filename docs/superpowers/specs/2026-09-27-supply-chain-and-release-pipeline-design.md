# Thyme & Place - Supply chain and release pipeline - Design

- **Status:** Proposed
- **Date:** 2026-09-27
- **Source:** Architectural audit, 2026-09-27 (findings F08, F22)
- **Related issues:** [#8](https://github.com/Muddl/thymeandplace/issues/8) (F08), [#22](https://github.com/Muddl/thymeandplace/issues/22) (F22)
- **Amends:** core spec §4.1 (the scaling claim is qualified), §13.10 (CI adds vulnerability scanning, lint and the race detector; release images are built only after CI passes on `main`), §14 (images come from a registry and are deployed by commit SHA; base images are pinned)

---

## 1. Problem

**F08: nothing in the supply chain or release process is pinned, scanned or reproducible.**

- **Actions pinned by tag.** CI uses `actions/checkout@v4`, `actions/setup-go@v5`, `actions/setup-python@v5`, `actions/setup-node@v4` and `actions/upload-artifact@v4` (`.github/workflows/ci.yml:19-20`, `:36-37`, `:47`, `:75-76`, `:81`). A tag can be moved to different code.
- **Base images float.**
  - `golang:1.26` and `gcr.io/distroless/static-debian12:nonroot` (`deploy/Dockerfile.app:2`, `:13`; `deploy/Dockerfile.ingest:2`, `:11`).
  - `postgres:17-bookworm` (`deploy/backup/Dockerfile:2`; the `mc` binary in it is already sha256-pinned).
  - `python:3.12-slim` (`pipeline/transform/Dockerfile:1`).
  - `postgis/postgis:17-3.5` and `caddy:2` (`deploy/compose.yaml:18`, `:49`).
  - `caddy:2` follows every Caddy 2.x release on each pull.
- **CI checks less than it could.** CI runs `go vet` and `go test` (`.github/workflows/ci.yml:28-31`). It has no known-vulnerability scan (govulncheck), no linter (staticcheck) and no race detector. There's no Dependabot or Renovate config, so dependency updates only happen by hand.
- **Production builds from source on the VPS.** The update procedure is `git pull && docker compose up -d --build app` (`docs/operations.md:64-66`). The image that runs is never the one CI tested. A rollback means checking out and rebuilding, which pulls whatever the floating base tags point to that day. The e2e job runs only on `main`, after the push (`.github/workflows/ci.yml:69-71`).

**F22: single-replica assumptions and deploy downtime aren't written down.**

- Core spec §4.1 says the app "can be scaled to N replicas later without code changes" (core spec line 73). That is functionally true, but:
  - the public rate limiter (`internal/web/middleware.go:173-216`) and the season cache (`internal/cache/cache.go`, used in `internal/web/server.go:75`) are per process, so effective limits multiply by N and cache hits split;
  - admin sign-in limits live in the database, so they stay correct.
- Every deploy is a hard restart of the only app container. The server doesn't listen until migrations have run and the catalog snapshot has loaded (`cmd/server/main.go:32-43`; `cmd/server/app.go:108-128`, `:194-210`). Meanwhile Caddy serves the 503 maintenance page (`deploy/Caddyfile:29-36`). Nobody has measured how long that window lasts.

## 2. Goals and non-goals

Goals:

1. Every third-party action and base image is pinned to an immutable reference. Update pull requests arrive automatically.
2. CI fails on known-reachable vulnerabilities in Go dependencies, on staticcheck findings and on data races.
3. Production runs images that CI built and tested, identified by commit SHA. Rollback is a redeploy of the previous SHA, with no rebuild.
4. Local development and the e2e stack keep building from source with `docker compose up --build`.
5. The single-replica assumptions and the deploy-downtime window are documented and measured. Zero-downtime deploys are a recorded decision, not an accident.

Non-goals:

- Signing images with cosign or keyless OIDC, and enforcing signatures on the VPS. SBOM and provenance attestations are generated, but nothing verifies them yet (§8 question 3).
- Multiple replicas, blue/green deploys or an orchestrator.
- Moving off GitHub Actions or GHCR.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| Action pinning | Full commit SHA plus a `# vX.Y.Z` comment. Dependabot keeps them current | Tags (mutable). Vendoring actions (maintenance burden) |
| Image pinning | `name:tag@sha256:digest` in every `FROM` and `image:`. `caddy:2` narrows to a minor version first (`caddy:2.x.y`) | Tag only (floats). Digest only (unreadable, and loses the version hint Dependabot needs) |
| Update automation | Dependabot, weekly, grouped per ecosystem: gomod, github-actions, docker, pip, npm | Renovate (more capable, but it's another app to install and configure for a one-owner repo) |
| Vulnerability scan | `govulncheck`, pinned through a `tool` directive in `go.mod` and run as `go tool govulncheck ./...` | `go run ...@latest` (unpinned). A third-party action wrapping it (another dependency to pin) |
| Linter | `staticcheck` through a `tool` directive | golangci-lint (bigger surface and its own config; staticcheck alone covers the valuable checks here) |
| Race detector | `go test -race` in the main `go` job, replacing the plain run | A separate race job on non-DB packages only (most concurrency lives in the DB-backed packages: watcher, auth, changeset) |
| Registry | GHCR, private, pushed by `GITHUB_TOKEN` with `packages: write` | Docker Hub (another account and secret). Building on the VPS (the problem today) |
| When images are built | A `release` workflow on `workflow_run` of `ci` on `main`, only when it concluded `success`, including e2e | On every push (images for commits that failed e2e). Tag-triggered only (an extra manual step per deploy) |
| How the VPS runs them | `compose.release.yaml` overlays `compose.yaml` with `build: !reset null` and `image: ghcr.io/muddl/thymeandplace-<name>:${TP_IMAGE_TAG}`. `deploy/deploy.sh <sha>` pulls, runs, waits for health and records the digest | Editing `compose.yaml` per deploy (drift). A separate production compose file (two files to keep in sync) |
| Zero-downtime deploys | Not now. Measure the window and document it (§4.6) | Blue/green behind Caddy (needs two app services, health-gated switching and migration discipline beyond today's rule, for a site where a short maintenance page is acceptable) |

## 4. Design

### 4.1 CI hardening (`.github/workflows/ci.yml`)

- Pin every `uses:` to a SHA, with the version in a comment.
- `go` job:
  - The step `Unit, scoring, auth, store integration and ingestion tests` runs `go test -race -count=1 ./...`.
  - Add `go tool staticcheck ./...` and `go tool govulncheck ./...` after `go vet`.
  - `go.mod` gains `tool honnef.co/go/tools/cmd/staticcheck` and `tool golang.org/x/vuln/cmd/govulncheck`, next to the existing templ tool.
- `deploy` job: add `shellcheck` for `deploy/*.sh`, `deploy/backup/*.sh` and `e2e/run.sh`. It is preinstalled on GitHub's Ubuntu runners; verify on the pinned image when implementing.
- `.github/dependabot.yml`: weekly, grouped:
  - `gomod` at `/`
  - `github-actions` at `/`
  - `docker` at `/deploy`, `/deploy/backup` and `/pipeline/transform`
  - `pip` at `/pipeline/transform`
  - `npm` at `/e2e`

  Whether Dependabot updates digests inside `deploy/compose.yaml` wasn't verified. If not, `docs/operations.md` documents a quarterly manual bump with `docker buildx imagetools inspect <image>:<tag>`.

A first run of staticcheck and the race detector may find existing issues. They're fixed in the same pull request, or listed in its description with an issue each. They aren't suppressed silently.

### 4.2 Pinned base images

Each `FROM` and `image:` becomes `repo:tag@sha256:…`, with the digest taken at implementation time from `docker buildx imagetools inspect`:
- `golang:1.26.x`
- `gcr.io/distroless/static-debian12:nonroot`
- `postgres:17-bookworm`
- `python:3.12-slim`
- `postgis/postgis:17-3.5`
- `caddy:2.<minor>`

`internal/dbtest/dbtest.go:25` (`Image = "postgis/postgis:17-3.5"`) uses the same pinned reference, so tests run on the production database image.

### 4.3 Release workflow (`.github/workflows/release.yml`)

- **Trigger:** `workflow_run` of `ci`, `types: [completed]`, `branches: [main]`. The job runs only when `github.event.workflow_run.conclusion == 'success'`, and checks out `github.event.workflow_run.head_sha`.
- **Permissions:** `contents: read`, `packages: write`.
- **Build:** for each image (`app` from `deploy/Dockerfile.app`, `ingest` from `deploy/Dockerfile.ingest`, `transform` from `pipeline/transform`, `backup` from `deploy/backup`), run `docker/build-push-action`, SHA-pinned, with:
  - tags `ghcr.io/muddl/thymeandplace-<name>:<head_sha>` and `:main`
  - `provenance: mode=max` and `sbom: true`
  - a platform matching the VPS (§8 question 2)

  The `CALIBRATION_FIXTURES` build argument stays empty for release builds (`deploy/Dockerfile.app:7-9`).
- **Summary:** write each image's digest to the job summary.

GHCR packages published from a private repository are private and can be read with a token that has `read:packages`.

### 4.4 Deploying on the VPS

`deploy/compose.release.yaml`:

```yaml
services:
  app:       { build: !reset null, image: "ghcr.io/muddl/thymeandplace-app:${TP_IMAGE_TAG:?set TP_IMAGE_TAG}" }
  ingest:    { build: !reset null, image: "ghcr.io/muddl/thymeandplace-ingest:${TP_IMAGE_TAG:?set TP_IMAGE_TAG}" }
  transform: { build: !reset null, image: "ghcr.io/muddl/thymeandplace-transform:${TP_IMAGE_TAG:?set TP_IMAGE_TAG}" }
  backup:    { build: !reset null, image: "ghcr.io/muddl/thymeandplace-backup:${TP_IMAGE_TAG:?set TP_IMAGE_TAG}" }
```

If the least-privilege design (`2026-09-27-least-privilege-deployment-design.md`, "S2") has landed, its `migrate` and `restore` services get the same treatment. `!reset` needs Compose 2.24 or later, which `docs/operations.md` already requires.

`deploy/deploy.sh <sha>` runs as root on the VPS, from `/opt/thymeandplace/deploy`:

1. Refuse unless `<sha>` is 40 hex characters.
2. Run `export TP_IMAGE_TAG=<sha>` and `C="docker compose -f compose.yaml -f compose.release.yaml"`.
3. Run `$C pull app ingest transform backup` (plus `migrate` and `restore` once S2 lands), and `db caddy` too, so their `compose.yaml` pins download before the window starts.
4. Run `$C up -d --wait app caddy`. This runs `migrate` first once S2 lands, and waits for the health check. Compose recreates a changed dependency of `app` (`db`) but never a dependent, so `caddy` is named: a merged `docker-compose` update of its pin is applied by the next deploy, and an unchanged `caddy` is left running.
5. Write `deploy/.env` (git-ignored, replaced atomically) with `COMPOSE_FILE=compose.yaml:compose.release.yaml` and `TP_IMAGE_TAG=<sha>`. Compose reads it from the directory it runs in, so every plain `docker compose` command on the VPS, the backup and restore-test timers and the `tools` runs included, uses the deployed release, and a plain `up -d` can't roll `app` back to a source-built image. Commands with their own `-f` ignore it.
6. Append `<date> <sha> <app digest>` to `/var/lib/thymeandplace/deploys.log`. The digest comes from `docker image inspect --format '{{index .RepoDigests 0}}'`.
7. Print the elapsed time from step 4, which is the downtime measurement in §4.6.

**Rollback:** `deploy/deploy.sh <previous sha>` from `deploys.log`. The existing rule that migrations stay backward-compatible with the previous release (`docs/operations.md:66`) is what makes this safe.

**One-time VPS setup:** `docker login ghcr.io` with a token that only has `read:packages`. The login is stored in root's Docker config. Whether fine-grained tokens can read GHCR wasn't verified; a classic token limited to `read:packages` works.

Local development, `deploy/smoke.sh` and `e2e/run.sh` don't use `compose.release.yaml` (they pass their own `-f`, so `deploy/.env` doesn't apply to them either), so `--build` keeps working unchanged.

### 4.5 Documentation of single-replica assumptions (F22)

Core spec §4.1 becomes: "Stateless apart from per-process caches. It can run as N replicas without code changes. Public rate limits (§11.4) and the season cache (§11.3) are per process, so limits scale with N and cache hits split. Admin sign-in limits (§10.4) are in the database and hold across replicas. Each replica needs its own health alerting (see the observability design)."

`docs/operations.md` gets a "Deploys and downtime" section. It says:
- Each deploy restarts the one app container.
- Caddy serves the maintenance page while the new container migrates (or waits for `migrate`) and loads the catalog snapshot.
- `deploy.sh` prints the measured window.
- The external uptime monitor alerts only after two failures, so a normal deploy doesn't page.

### 4.6 Zero-downtime threshold

We don't build zero-downtime deploys now. `deploy.sh` records the window on every deploy. If the median over the first 10 production deploys exceeds 30 s, a follow-up issue designs a blue/green deploy: two app services behind Caddy with health-gated switching.

## 5. Implementation outline

1. SHA-pin actions in `ci.yml`. Add `.github/dependabot.yml`.
2. `go.mod` tool directives. CI steps for staticcheck, govulncheck and `-race`. Fix anything they find.
3. `shellcheck` step and fixes.
4. Digest-pin every base image, including `internal/dbtest/dbtest.go:25`.
5. `.github/workflows/release.yml`.
6. `deploy/compose.release.yaml` and `deploy/deploy.sh`.
7. Docs: `docs/operations.md` ("First deploy" registry login, "Updating the app" via `deploy.sh`, "Rolling back the app", "Deploys and downtime", and the quarterly compose digest bump if needed). Core spec amendments (§4.1, §13.10, §14).

## 6. Testing

- **CI itself:** the PR that adds the steps must pass them. A deliberately racy test, added in a scratch commit and not merged, confirms `-race` fails the job.
- **Release workflow:** the first `main` run after merge pushes four images. Check each in the GHCR UI, and run `docker buildx imagetools inspect ghcr.io/muddl/thymeandplace-app:<sha>` to show the platform and attestations.
- **Compose overlay:** the CI `deploy` job adds `docker compose -f deploy/compose.yaml -f deploy/compose.release.yaml config -q` with `TP_IMAGE_TAG=0000000000000000000000000000000000000000`, and a negative check that it fails without `TP_IMAGE_TAG`.
- **Deploy script:** `shellcheck`, plus one real run each on the VPS: a deploy, a rollback to the previous SHA, and a roll forward again. Record the three windows in `deploys.log`.
- **Pinned images:** the existing e2e job builds from the pinned `Dockerfile`s and runs the whole Playwright suite, and `deploy/smoke.sh` still passes.

## 7. Rollout, operations and rollback

- **Order:** CI hardening first (steps 1–4). It is independent and low-risk. Then the release workflow. Then switch the VPS to `deploy.sh` for the next deploy.
- **Transition:** the first `deploy.sh` run replaces the locally built image with the registry one for the same commit. Keep the old `thymeandplace-app:latest` image until the new one has passed a day.
- **Rollback of the pipeline itself:** the source-build path (`rm -f .env && git pull && docker compose up -d --build app`; removing `deploy/.env` drops the release overlay) keeps working, because `compose.yaml` still has `build:`. It becomes the documented emergency path if GHCR or Actions is unavailable.

## 8. Open questions for the owner

1. **Race detector on every push.** It slows the DB-heavy test packages. Recommended: yes on every push. Test time is dominated by container start-up, which the race detector doesn't change.
2. **VPS CPU architecture.** Recommended: build only the VPS platform (probably `linux/amd64`). Add `linux/arm64` only if the VPS is ARM.
3. **Image signing.** Recommended: not now. Attestations are generated, so enforcing them later is additive.
4. **Blue/green.** Recommended: decide from measured deploy windows (§4.6), not now.
