# End-to-end tests

Playwright drives the Compose stack behind Caddy (`https://localhost` by default). `./e2e/run.sh` builds that stack, seeds it, installs one browser, and runs the suite.

## Commands

```bash
./e2e/run.sh                          # chromium, full suite, local compose stack
BROWSER=firefox ./e2e/run.sh          # firefox (public specs only; see below)
GREP=@readonly ./e2e/run.sh           # smoke subset
GREP=@mutating ./e2e/run.sh           # tests that write or take the database down
```

From `e2e/` after `npm ci`:

```bash
npx playwright test --project=chromium
npm run test:readonly                 # --grep @readonly
npm run test:mutating                 # --grep @mutating
```

## Tags

QA tags each spec `@readonly` or `@mutating`. Untagged tests count as mutating.

| Tag | Meaning | Typical use |
|---|---|---|
| `@readonly` | GET-only. No catalog writes, no admin enrollment, no `compose stop`. Safe against a live site. | Smoke: `GREP=@readonly` |
| `@mutating` | Stops Postgres, enrolls a passkey, or applies a catalog change. Local compose stack only. | Default CI run (no `GREP`, so both tags) |

CI does not pass `GREP`, so it runs the whole suite. A prod smoke is:

```bash
E2E_BASE_URL=https://thymeand.place GREP=@readonly BROWSER=chromium ./e2e/run.sh
```

`run.sh` skips Compose when `E2E_BASE_URL` is not localhost (or when `E2E_SKIP_STACK=1`).

The specs themselves are tagged in a separate change. Until then, `--grep @readonly` matches nothing.

## Browsers

Chromium, Firefox and WebKit are Playwright projects. Admin specs (`admin.spec.ts`) use CDP WebAuthn, which is Chromium-only, so the Firefox and WebKit projects set `testIgnore` on that file.

CI picks the matrix with `e2e/select-browsers.sh`:

- Push to `main`: chromium, firefox, webkit.
- Pull request that touches `views/`, `static/`, or `e2e/`: all three.
- Any other pull request: chromium only.

Each job installs only its browser and caches `~/.cache/ms-playwright`. `./e2e/select-browsers.sh --self-test` checks the path rules.

## Shards

CI is `browser × shard` (`SHARD=1/2` and `SHARD=2/2`). Each shard brings up its own compose project because the suite stops the database and edits the catalog (`workers: 1`). Blob reports are merged and uploaded when a shard fails.

## Environment

| Variable | Default | Role |
|---|---|---|
| `E2E_BASE_URL` | `https://localhost` | Playwright `baseURL`. Point this at prod for a `@readonly` smoke. |
| `BROWSER` | `chromium` | Playwright project to run and to `playwright install`. |
| `SHARD` | unset | Playwright `--shard`, for example `1/2`. |
| `GREP` | unset | Playwright `--grep`, for example `@readonly`. |
| `E2E_SKIP_STACK` | unset | Set to `1` to skip Compose even on localhost. |
| `CI` | unset | Switches the reporter to blob (for the merge job). |

## TODO: full suite against release images

The suite still builds app and ingest from the current tree (`compose.yaml` + `compose.e2e.yaml`). It does not pull the GHCR images `compose.release.yaml` uses.

Doing that in this workflow is blocked:

1. The `release` workflow runs only after `ci` succeeds on `main`, and e2e is part of `ci`. Images for the commit under test do not exist yet.
2. Pull-request commits are never published to GHCR.
3. `compose.e2e.yaml` injects `CALIBRATION_FIXTURES` at image build time. Release images are built without those fixtures.
4. Mutating tests need a disposable database, `DEVSEED_ALLOW`, and `TP_PUBLIC_TODAY`. They cannot run against production.

A follow-up is a post-release job that pulls `ghcr.io/muddl/thymeandplace-*:$sha` for the last green `main`, overlays e2e env files, and runs `GREP=@readonly`. Prod can already be probed with `E2E_BASE_URL` as above.
