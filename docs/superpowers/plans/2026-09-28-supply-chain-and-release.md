# Supply Chain and Release Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pin every third-party action and base image to an immutable reference, with automatic update pull requests. Make CI fail on reachable Go vulnerabilities, staticcheck findings and data races. Deploy production from CI-built images identified by commit SHA, so rollback is a redeploy with no rebuild.

**Architecture:**
- **CI hardening.** Every `uses:` is SHA-pinned. `go.mod` `tool` directives pin staticcheck and govulncheck, and the `go` job runs them plus `go test -race`. The `deploy` job adds shellcheck. Dependabot sends weekly grouped updates for gomod, github-actions, docker, pip and npm.
- **Release workflow.** It triggers on a successful `ci` run on `main`, builds the four images at the tested commit, and pushes them to GHCR with SBOM and provenance attestations.
- **Deploy.** `deploy/compose.release.yaml` overlays `compose.yaml`, swapping `build:` for `image: ghcr.io/muddl/thymeandplace-<name>:${TP_IMAGE_TAG}`. `deploy/deploy.sh <sha>` pulls, starts, waits for health, and logs the digest and the downtime window.
- **Source builds stay.** `docker compose up --build`, e2e and smoke tests keep building from source.

**Tech Stack:** GitHub Actions, Dependabot, GHCR, `docker/build-push-action`, Docker Compose ≥ 2.24 (`!reset`), Go 1.26 `tool` directives, staticcheck, govulncheck, shellcheck, bash.

**Spec:** `docs/superpowers/specs/2026-09-27-supply-chain-and-release-pipeline-design.md` (all of it). Issues [#8](https://github.com/Muddl/thymeandplace/issues/8) (F08: Tasks 1-7) and [#22](https://github.com/Muddl/thymeandplace/issues/22) (F22: Task 8, spec §4.5-§4.6). Both are **Todo** on the project board. #22's `priority: backlog` label predates the board.

**Spec drift at HEAD:** The least-privilege work (`620cf2b`) added the `migrate` and `restore` services. They get the same overlay treatment (spec §4.4, "once S2 lands"): `migrate` runs the app image, and `restore` runs the backup image. CI now also has a `Role bootstrap script parses` step and per-service env files.

## Global Constraints

- Actions: `uses: owner/repo@<40-hex commit SHA> # vX.Y.Z`. Never a tag or branch alone. This applies to every workflow, including jobs added by other plans (such as `backup` from the backup-encryption plan).
- Images: `name:tag@sha256:<digest>` in every `FROM` and `image:`. `caddy:2` first narrows to a minor tag (`caddy:2.<minor>`). Take digests from `docker buildx imagetools inspect <name>:<tag>` at implementation time, using the index digest shown as `Digest:`.
- `internal/dbtest/dbtest.go` `Image` uses the same pinned PostGIS reference as `deploy/compose.yaml`.
- Tools come in through `go.mod` `tool` directives and run as `go tool staticcheck ./...` and `go tool govulncheck ./...`. Nothing is installed with `@latest` in CI.
- Tests run with `go test -race -count=1 ./...` on every push (spec §8 question 1, recommended: yes).
- Registry images are `ghcr.io/muddl/thymeandplace-{app,ingest,transform,backup}:<head_sha>` and `:main`. They are private, pushed by `GITHUB_TOKEN` with `packages: write`, and built only after `ci` on `main` concluded `success`.
- Platform: `linux/amd64` only (spec §8 question 2), unless the owner says the VPS is ARM.
- Release builds leave `CALIBRATION_FIXTURES` empty.
- `deploy.sh` refuses anything but a 40-hex SHA. Migrations stay backward-compatible with the previous release, which is what makes rollback-by-SHA safe.
- Staticcheck or race findings on the first run are fixed in the same pull request, or listed in its description with one issue each. Never suppress them silently.

## Review Focus

1. **The release workflow running for a failed or non-`main` CI run.** `workflow_run` fires on every completion. The job must check `conclusion == 'success'` and build `head_sha`, never `github.sha` (which is the default branch's HEAD). Task 5's checklist verifies both by reading the first run.
2. **`compose.release.yaml` without `TP_IMAGE_TAG`.** It must fail config, not silently pull `:latest` or build. Task 6's CI check covers both the positive and the negative case.
3. **`deploy.sh` given a short or tag-like argument** (`main`, `abc123`, a branch name). It must refuse before pulling anything. Task 6 tests it with shellcheck plus a dry negative run.
4. **The e2e and smoke paths still building from source after digest pinning.** A pinned digest for the wrong platform, or a typo, breaks every build. Task 4 runs `docker compose build` and `deploy/smoke.sh` locally.
5. **govulncheck failing on a vulnerability in an unreachable dependency.** govulncheck reports only reachable symbols by default. The step must not add flags such as `-show verbose` that change the exit status. Task 2 documents this, and a CI run proves it.

---

### Task 1: Pin actions and add Dependabot

**Files:**
- Modify: `.github/workflows/ci.yml` (every `uses:`)
- Create: `.github/dependabot.yml`

**Interfaces:**
- Produces: SHA-pinned `uses:` lines. Later tasks copy the same SHAs.

- [ ] **Step 1: Resolve the SHAs**

For each action, take the newest release in the major version already used, and resolve it to a commit:

```bash
for a in actions/checkout actions/setup-go actions/setup-python actions/setup-node actions/upload-artifact \
         docker/setup-buildx-action docker/login-action docker/build-push-action; do
  tag=$(gh api "repos/$a/releases/latest" --jq .tag_name)
  sha=$(gh api "repos/$a/commits/$tag" --jq .sha)
  echo "$a $tag $sha"
done
```

Keep the output: Task 5 needs the three `docker/*` SHAs. If `releases/latest` jumps to a new major version for an action CI already uses (for example `actions/checkout` v5), stop and ask whether to take the major bump in this pull request. Otherwise use `gh api "repos/$a/tags" --jq '.[].name'` to take the newest tag in the current major.

- [ ] **Step 2: Pin them in `ci.yml`**

Replace each `uses: <action>@vN` with `uses: <action>@<sha> # <tag>`, for example `uses: actions/checkout@<sha> # v4.2.2`.

- [ ] **Step 3: Create `.github/dependabot.yml`**

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule: { interval: weekly }
    groups: { gomod: { patterns: ["*"] } }
  - package-ecosystem: github-actions
    directory: /
    schedule: { interval: weekly }
    groups: { actions: { patterns: ["*"] } }
  - package-ecosystem: docker
    directories: [/deploy, /deploy/backup, /pipeline/transform]
    schedule: { interval: weekly }
    groups: { docker: { patterns: ["*"] } }
  - package-ecosystem: pip
    directory: /pipeline/transform
    schedule: { interval: weekly }
    groups: { pip: { patterns: ["*"] } }
  - package-ecosystem: npm
    directory: /e2e
    schedule: { interval: weekly }
    groups: { npm: { patterns: ["*"] } }
```

- [ ] **Step 4: Check nothing unpinned remains**

Run: `grep -nE 'uses: [^@]+@(v[0-9]|main|master)' .github/workflows/*.yml`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add .github/
git commit -m "ci: pin actions to commit SHAs and add grouped Dependabot updates (#8)"
```

### Task 2: staticcheck, govulncheck and the race detector

**Files:**
- Modify: `go.mod`, `go.sum` (tool directives)
- Modify: `.github/workflows/ci.yml` (`go` job)
- Modify: any Go files staticcheck or `-race` flags

**Interfaces:**
- Produces: `go tool staticcheck` and `go tool govulncheck`, pinned by `go.mod`.

- [ ] **Step 1: Add the tools**

Run:

```bash
go get -tool honnef.co/go/tools/cmd/staticcheck@latest
go get -tool golang.org/x/vuln/cmd/govulncheck@latest
go mod tidy
```

Expected: `go.mod` gains two `tool` lines next to `tool github.com/a-h/templ/cmd/templ`, and `go.sum` updates. The version `@latest` resolves to now gets recorded in `go.mod`; that is the pin.

- [ ] **Step 2: Run them locally**

Run: `go tool staticcheck ./... ; go tool govulncheck ./...`
Expected: both run. Record every finding.

- [ ] **Step 3: Fix the findings**

Fix each staticcheck finding in place. For a finding that is intentional, add a `//lint:ignore <check> <reason>` on that line, with a reason a reviewer can accept. For govulncheck, bump the affected module (`go get <module>@<fixed version>`). If no fix exists, open an issue and list it in the pull request description. Don't skip the step.

- [ ] **Step 4: Run the race detector**

Run: `go test -race -count=1 ./...` (needs Docker for the DB-backed packages).
Expected: PASS. A reported race is a real bug. Fix it, or list it in the pull request description with an issue. Don't drop `-race`.

- [ ] **Step 5: CI steps**

In the `go` job of `.github/workflows/ci.yml`, after `- run: go vet ./...`, add:

```yaml
      - name: staticcheck
        run: go tool staticcheck ./...
      - name: govulncheck (reachable vulnerabilities only)
        run: go tool govulncheck ./...
```

Change the test step's command to `go test -race -count=1 ./...`.

- [ ] **Step 6: Prove `-race` can fail the job**

On a scratch branch that is never merged, add a test with a deliberate data race:

```go
func TestRaceProbe(t *testing.T) {
	n := 0
	done := make(chan struct{})
	go func() { n++; close(done) }()
	n++
	<-done
}
```

Push it and confirm the `go` job fails with `WARNING: DATA RACE`. Link that run in the pull request, then delete the branch.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum .github/workflows/ci.yml $(git diff --name-only -- '*.go')
git commit -m "ci: staticcheck, govulncheck and go test -race on every push (#8)"
```

### Task 3: shellcheck

**Files:**
- Modify: `.github/workflows/ci.yml` (`deploy` job)
- Modify: shell scripts it flags: `deploy/*.sh`, `deploy/backup/*.sh`, `deploy/db/initdb/*.sh`, `e2e/run.sh`

- [ ] **Step 1: Run it locally**

Run: `docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:stable deploy/*.sh deploy/backup/*.sh deploy/db/initdb/*.sh e2e/run.sh`
Expected: a list of findings, possibly empty.

- [ ] **Step 2: Fix them**

Fix each finding. For intentional word splitting (such as the backup script's `$recipients`), keep or add a `# shellcheck disable=SCxxxx # reason` on that line.

- [ ] **Step 3: CI step**

In the `deploy` job, add:

```yaml
      - name: shellcheck
        run: shellcheck deploy/*.sh deploy/backup/*.sh deploy/db/initdb/*.sh e2e/run.sh
```

shellcheck is preinstalled on GitHub's `ubuntu-latest` runners. Confirm it in the job log (`shellcheck --version`). If it's missing, add `sudo apt-get install -y shellcheck` before the step.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/ci.yml deploy/ e2e/run.sh
git commit -m "ci: shellcheck the deploy, backup and e2e scripts (#8)"
```

### Task 4: Digest-pin every base image

**Files:**
- Modify: `deploy/Dockerfile.app:2,13`, `deploy/Dockerfile.ingest:2,11`, `deploy/backup/Dockerfile:2`, `pipeline/transform/Dockerfile:1`
- Modify: `deploy/compose.yaml` (`db` image, `caddy` image), `deploy/compose.dev.yaml:10`
- Modify: `internal/dbtest/dbtest.go:25` (`Image`)

- [ ] **Step 1: Resolve the digests**

```bash
for ref in golang:1.26 gcr.io/distroless/static-debian12:nonroot postgres:17-bookworm python:3.12-slim postgis/postgis:17-3.5; do
  printf '%s ' "$ref"; docker buildx imagetools inspect "$ref" | awk '/^Digest:/ {print $2}'
done
docker run --rm caddy:2 caddy version   # note the 2.<minor>.<patch>, then:
docker buildx imagetools inspect caddy:2.<minor> | awk '/^Digest:/ {print $2}'
docker run --rm golang:1.26 go version    # note the 1.26.<patch>, then pin golang:1.26.<patch>
```

Use the full patch tag for Go (`golang:1.26.<patch>`), because Dependabot bumps it. Keep the others' tags as they are.

- [ ] **Step 2: Apply them**

Every reference becomes `<name>:<tag>@sha256:<digest>`, for example `FROM golang:1.26.<patch>@sha256:<digest> AS build` and `image: postgis/postgis:17-3.5@sha256:<digest>`. `caddy:2` becomes `caddy:2.<minor>@sha256:<digest>`. Set `internal/dbtest/dbtest.go`'s `const Image` to the same PostGIS reference as `compose.yaml`, with the comment `// Same pinned image as deploy/compose.yaml's db service.`

- [ ] **Step 3: Build and test everything**

Run:

```bash
docker build -f deploy/Dockerfile.app . && docker build -f deploy/Dockerfile.ingest . \
  && docker build deploy/backup && docker build pipeline/transform
go test -race -count=1 ./internal/dbtest/ ./internal/climate/
./deploy/smoke.sh
./e2e/run.sh
```

Expected: every build succeeds, the tests pass on the pinned PostGIS image, and smoke and e2e pass.

- [ ] **Step 4: Commit**

```bash
git add deploy/ pipeline/transform/Dockerfile internal/dbtest/dbtest.go
git commit -m "deploy: pin every base image by digest (#8)"
```

### Task 5: Release workflow

**Files:**
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Produces: the images `ghcr.io/muddl/thymeandplace-<name>:<sha>` and `:main`, with each digest in the job summary.

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/release.yml`, using the SHAs from Task 1:

```yaml
name: release
# Builds release images only for commits whose ci run passed on main,
# including e2e, and only from that exact commit.
on:
  workflow_run:
    workflows: [ci]
    types: [completed]
    branches: [main]

permissions:
  contents: read
  packages: write

jobs:
  images:
    if: github.event.workflow_run.conclusion == 'success' && github.event.workflow_run.event == 'push'
    runs-on: ubuntu-latest
    timeout-minutes: 45
    strategy:
      fail-fast: false
      matrix:
        include:
          - { name: app, context: ., file: deploy/Dockerfile.app }
          - { name: ingest, context: ., file: deploy/Dockerfile.ingest }
          - { name: transform, context: pipeline/transform, file: pipeline/transform/Dockerfile }
          - { name: backup, context: deploy/backup, file: deploy/backup/Dockerfile }
    steps:
      - uses: actions/checkout@<sha> # <tag>
        with:
          ref: ${{ github.event.workflow_run.head_sha }}
      - uses: docker/setup-buildx-action@<sha> # <tag>
      - uses: docker/login-action@<sha> # <tag>
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - id: build
        uses: docker/build-push-action@<sha> # <tag>
        with:
          context: ${{ matrix.context }}
          file: ${{ matrix.file }}
          platforms: linux/amd64
          push: true
          provenance: mode=max
          sbom: true
          tags: |
            ghcr.io/muddl/thymeandplace-${{ matrix.name }}:${{ github.event.workflow_run.head_sha }}
            ghcr.io/muddl/thymeandplace-${{ matrix.name }}:main
      - name: Digest
        run: echo "| ${{ matrix.name }} | \`${{ steps.build.outputs.digest }}\` |" >> "$GITHUB_STEP_SUMMARY"
```

- [ ] **Step 2: Check the syntax**

Run: `docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:latest -color`
Expected: no findings for `release.yml`. Pre-existing findings in `ci.yml` should be fixed too, in this commit.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: build and push release images after ci passes on main (#8)"
```

- [ ] **Step 4: After merging, verify the first run**

These checks run in GitHub and on the VPS after merge:
- The `release` run for the merge commit shows four successful matrix jobs and a digest table.
- `docker buildx imagetools inspect ghcr.io/muddl/thymeandplace-app:<sha>` lists `linux/amd64` and the attestation manifests.
- The run's `head_sha` equals the merge commit, not an older one.
- A branch push produced no `release` run.

### Task 6: Deploying by SHA

**Files:**
- Create: `deploy/compose.release.yaml`
- Create: `deploy/deploy.sh` (mode 755)
- Modify: `.github/workflows/ci.yml` (`deploy` job: overlay config checks)

**Interfaces:**
- Produces: `deploy/deploy.sh <40-hex sha>`, which appends `<UTC date> <sha> <app repo digest> <seconds>s` to `/var/lib/thymeandplace/deploys.log`.

- [ ] **Step 1: The overlay**

Create `deploy/compose.release.yaml`:

```yaml
# Production runs images CI built and tested, by commit SHA:
#   TP_IMAGE_TAG=<sha> docker compose -f compose.yaml -f compose.release.yaml ...
# deploy/deploy.sh does this. Without the overlay, compose.yaml still builds
# from source (local dev, e2e, smoke tests, and the emergency path).
services:
  migrate:   { build: !reset null, image: "ghcr.io/muddl/thymeandplace-app:${TP_IMAGE_TAG:?set TP_IMAGE_TAG}" }
  app:       { build: !reset null, image: "ghcr.io/muddl/thymeandplace-app:${TP_IMAGE_TAG:?set TP_IMAGE_TAG}" }
  ingest:    { build: !reset null, image: "ghcr.io/muddl/thymeandplace-ingest:${TP_IMAGE_TAG:?set TP_IMAGE_TAG}" }
  transform: { build: !reset null, image: "ghcr.io/muddl/thymeandplace-transform:${TP_IMAGE_TAG:?set TP_IMAGE_TAG}" }
  backup:    { build: !reset null, image: "ghcr.io/muddl/thymeandplace-backup:${TP_IMAGE_TAG:?set TP_IMAGE_TAG}" }
  restore:   { build: !reset null, image: "ghcr.io/muddl/thymeandplace-backup:${TP_IMAGE_TAG:?set TP_IMAGE_TAG}" }
```

- [ ] **Step 2: The deploy script**

Create `deploy/deploy.sh`:

```bash
#!/usr/bin/env bash
# usage (as root, from /opt/thymeandplace/deploy): ./deploy.sh <40-hex commit SHA>
# Pulls the images CI built for that commit, starts them (migrate first, then
# app, waiting for its health check), and records the deploy. Rollback is
# the same command with the previous SHA from /var/lib/thymeandplace/deploys.log.
set -euo pipefail
sha=${1:-}
[[ $sha =~ ^[0-9a-f]{40}$ ]] || { echo "deploy: give the full 40-character commit SHA (got '${sha}')" >&2; exit 2; }
cd "$(dirname "$0")"
export TP_IMAGE_TAG=$sha
C="docker compose -f compose.yaml -f compose.release.yaml --profile tools"
$C pull migrate app ingest transform backup restore
start=$(date +%s)
$C up -d --wait app
elapsed=$(( $(date +%s) - start ))
digest=$(docker image inspect --format '{{index .RepoDigests 0}}' "ghcr.io/muddl/thymeandplace-app:$sha")
mkdir -p /var/lib/thymeandplace
echo "$(date -u +%FT%TZ) $sha $digest ${elapsed}s" >> /var/lib/thymeandplace/deploys.log
echo "deploy ok: $sha ($digest); maintenance window about ${elapsed}s"
```

- [ ] **Step 3: CI checks for the overlay**

In the `deploy` job, after "Compose files are valid", add:

```yaml
      - name: Release overlay resolves only with TP_IMAGE_TAG
        run: |
          env_dir="$RUNNER_TEMP/env"
          TP_ENV_DIR="$env_dir" TP_IMAGE_TAG=0000000000000000000000000000000000000000 \
            docker compose -f deploy/compose.yaml -f deploy/compose.release.yaml --profile tools config -q
          if TP_ENV_DIR="$env_dir" docker compose -f deploy/compose.yaml -f deploy/compose.release.yaml --profile tools config -q 2>/dev/null; then
            echo "compose.release.yaml must fail without TP_IMAGE_TAG"; exit 1
          fi
      - name: deploy.sh refuses anything but a full SHA
        run: |
          for bad in "" main abc123 0000000000000000000000000000000000000000x; do
            if ./deploy/deploy.sh "$bad" 2>/dev/null; then echo "deploy.sh accepted '$bad'"; exit 1; fi
          done
```

Add `deploy/deploy.sh` to the shellcheck step's file list if its glob doesn't already match. `deploy/*.sh` does.

- [ ] **Step 4: Run the checks locally**

Run the two new CI steps' commands locally (with a temp env dir built as in "Compose files are valid"), plus `shellcheck deploy/deploy.sh`.
Expected: the positive config passes, the negative fails as intended, every bad SHA is refused, and shellcheck is clean.

- [ ] **Step 5: Commit**

```bash
git add deploy/compose.release.yaml deploy/deploy.sh .github/workflows/ci.yml
git commit -m "deploy: run CI-built images by commit SHA with deploy.sh (#8)"
```

### Task 7: Documentation

**Files:**
- Modify: `docs/operations.md` ("First deploy", "Updating the app", a new "Rolling back the app", "Base images and updates")
- Modify: `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` §13.10 and §14

- [ ] **Step 1: `docs/operations.md`**

- **"First deploy":** add a one-time `docker login ghcr.io -u <github user>` step, using a classic token with only `read:packages`. The login is stored in root's Docker config.
- **"Updating the app":**
  - Replace the command with `./deploy.sh <sha>`, run from `/opt/thymeandplace/deploy` after `git pull`.
  - Take the SHA from the green `release` run for that commit.
  - The script pulls, runs `migrate`, starts the app, waits for health, and logs the window.
  - Keep the migration-compatibility paragraph.
  - Keep `git pull && docker compose up -d --build app` as the documented **emergency path** for when GHCR or Actions is down.
  - Transition: keep the old locally built `thymeandplace-app:latest` image until the first registry deploy has run for a day.
- **"Rolling back the app":** `./deploy.sh <previous sha from /var/lib/thymeandplace/deploys.log>`. No rebuild is involved.
- **"Base images and updates":**
  - Dependabot opens weekly grouped pull requests.
  - If Dependabot turns out not to bump the digests in `deploy/compose.yaml` (spec §4.1 left this unverified; check after the first weekly run), bump them quarterly with `docker buildx imagetools inspect <image>:<tag>`, and keep `internal/dbtest/dbtest.go` in step.

- [ ] **Step 2: Core spec**

- §13.10: CI runs staticcheck, govulncheck and the race detector. Release images are built only after CI passes on `main`.
- §14:
  - Production runs registry images by commit SHA (`deploy.sh`), and rollback is a redeploy of the previous SHA.
  - Base images and actions are pinned by digest/SHA, with Dependabot updates.

- [ ] **Step 3: Commit**

```bash
git add docs/
git commit -m "docs: deploy and roll back by commit SHA; pinned images and updates (#8)"
```

### Task 8: Record the single-replica assumptions and deploy downtime (#22)

**Files:**
- Modify: `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` §4.1 (the scaling claim, about line 73)
- Modify: `docs/operations.md` (new "Deploys and downtime" section, after "Rolling back the app")

**Interfaces:** documentation only. It relies on `deploy.sh`'s `deploys.log` window (Task 6).

- [ ] **Step 1: Core spec §4.1**

Replace the sentence saying the app "can be scaled to N replicas later without code changes" with:

```markdown
Stateless apart from per-process caches. It can run as N replicas without code changes. Public rate limits (§11.4) and the season cache (§11.3) are per process, so limits scale with N and cache hits split. Admin sign-in limits (§10.4) are in the database and hold across replicas. Each replica needs its own health alerting (see the observability design).
```

Read the surrounding paragraph first and keep its style. Also check the claim against the code as it is now: the per-cell input cache (`cache.CellSize`), the public DB gate slots (`web.DefaultDBSlots`, per process), and the `/admin` request limiter (`adminLimiterKeys`, per process) are per process too. Name them in the same sentence so the list is complete.

- [ ] **Step 2: "Deploys and downtime" in `docs/operations.md`**

```markdown
## Deploys and downtime

Every deploy restarts the one app container. While the new container runs `migrate` and loads its catalog snapshot, Caddy serves the 503 maintenance page (`deploy/Caddyfile`, `handle_errors`). `deploy.sh` measures this window and writes it to the last column of `/var/lib/thymeandplace/deploys.log`. The external uptime monitor alerts only after two consecutive failures, so a normal deploy doesn't page.

Zero-downtime (blue/green) deploys are deliberately not built. After the first 10 production deploys, take the median window: `awk '{print $4}' /var/lib/thymeandplace/deploys.log | tr -d s | sort -n | awk '{a[NR]=$1} END {print a[int((NR+1)/2)]}'`. If it exceeds 30 s, open an issue to design blue/green: two app services behind Caddy with health-gated switching.

The app is single-replica today. Running more replicas needs no code change, but the per-process limits and caches listed in the core spec §4.1 then multiply or split, and each replica needs its own `app` heartbeat check.
```

- [ ] **Step 3: Check the awk pipeline**

Run: `printf '2026-01-01T00:00:00Z a d 12s\n2026-01-02T00:00:00Z b d 40s\n2026-01-03T00:00:00Z c d 9s\n' > /tmp/d.log && awk '{print $4}' /tmp/d.log | tr -d s | sort -n | awk '{a[NR]=$1} END {print a[int((NR+1)/2)]}'`
Expected: `12`.

- [ ] **Step 4: Commit**

```bash
git add docs/
git commit -m "docs: single-replica assumptions and the measured deploy window (#22)"
```

### Task 9: Whole-repo check and first production deploy

- [ ] **Step 1:** Push the branch and confirm that every CI job passes: `go` (staticcheck, govulncheck, `-race`), `python`, `deploy` (shellcheck, overlay checks), and `e2e` after merge.
- [ ] **Step 2:** After merging, run Task 5 Step 4.
- [ ] **Step 3:** On the VPS, run `./deploy.sh <merge sha>`. Once a second release SHA exists, also do a rollback to the first (`./deploy.sh <first sha>`) and a roll-forward. Confirm that `deploys.log` has a line with a window for each, and that the site serves after each (spec §6).
