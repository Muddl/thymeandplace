# Thyme & Place - Observability and alerting - Design

- **Status:** Proposed
- **Date:** 2026-09-27
- **Source:** Architectural audit, 2026-09-27 (finding F07)
- **Related issues:** [#7](https://github.com/Muddl/thymeandplace/issues/7) (F07)
- **Amends:** core spec §10.4 (audit bullet), §11.4 (access log), §13.2 (production drift check), §14 (Backups, Health and Logs bullets; new Monitoring bullet), §15 (monitoring payloads), §16 (new values)
- **Amended in implementation (issue #7):** where this spec and §4.7 disagreed, §4.7 won. Drift and prune ping bodies name a fixed category or prefix, never raw error text (a pgx or `mc` network error can carry an IP). Env files are per service (`deploy/env/{app,backup,restore}.env.example`), not one `deploy/env.example`. A request whose client went away is neither counted by `Window` nor logged at `ERROR`. Later issues changed the backup steps and the rollout: backup encryption (#12) added the `verify` and `encrypt` steps and the `.dump.age` key, and the release pipeline (#8) runs production from registry images through `deploy/.env`, so the rollout recreates containers without `--build`. Each change is marked in place below.

---

## 1. Problem

Nothing tells the owner when the site, the nightly backup or the drift check fails. Every signal the system produces ends in a log that nobody reads, and that log rotates away.

- **Logs are the only output.** The server writes JSON `slog` to stdout (`cmd/server/main.go:23`). Every container uses `json-file` rotation at 10 MB x 5 files (`deploy/compose.yaml:7-13`).
- **Health probes fill that budget.** Caddy probes `/healthz` every 10 s (`deploy/Caddyfile:24-28`) and Docker every 15 s (`deploy/compose.yaml:40-45`). Both go through the access log, which writes one line per request (`internal/web/server.go:102-109`, `internal/web/middleware.go:84-104`). That is 14,400 lines a day before any visitor arrives. At an estimated 250-300 bytes per `json-file` line, about 4 MB a day, so an idle site keeps under two weeks of logs.
- **Drift is only logged.** A calibration drop of more than 0.02 is a `WARN` (`internal/calibrun/drift.go:50-52`). A failed run is an `ERROR`, and the next try is 24 h later (`internal/calibrun/drift.go:59-64`). `calibration_run` keeps the scores, but nothing reads it.
- **Backup-login use is only logged.** A successful password + TOTP login is a `WARN` with the admin IP (`internal/auth/service.go:89-91`). When the global backup lockout engages, nothing is logged at all (`internal/auth/backup.go:91-102`).
- **Some faults are only visible as `ERROR` lines.** A failed catalog reload keeps serving the old snapshot, and `/readyz` still says `ready` (`internal/catalog/holder.go:33-36`). Public DB failures log `WARN` and return 503 (`internal/web/season.go:123-124`, `internal/web/places.go:37-38`), so only the status code shows them.
- **Backups fail silently.** Cron appends to `/var/log/thymeandplace-backup.log`, and nothing reads or rotates that file (`docs/operations.md:91-95`). A missed run goes unnoticed, and so does a prune failure, which `backup.sh` deliberately writes only to stderr (`deploy/backup/backup.sh:20-29`). The restore test is manual, with no reminder (`docs/operations.md:122-126`).
- **A hung app is not restarted.** Compose does not restart unhealthy containers (`docs/operations.md:226`). Docker's restart policy acts only when a container exits (verified in the Docker docs). Core spec §14's "`/healthz` (restart)" is wrong.
- **A metrics endpoint would be public.** Caddy proxies every path to `app:8080` (`deploy/Caddyfile:24`).
- **No outside check exists.** An expired certificate, a Caddy fault or a dead VPS would be found by visitors first. The plans kept `/readyz` "for monitoring" (`docs/superpowers/plans/2026-09-25-thymeandplace-00-index.md:30`), but nothing polls it.

Constraints: core spec §14-15 and the About page (`internal/web/views/about.templ:25`) promise that public requests are logged only as `cell_id`, and that coordinates and public IP addresses never are. Monitoring must keep that promise, including toward any third party.

## 2. Goals and non-goals

**Goals**

1. The owner is alerted by email and phone push within about 10 minutes when:
   - the public site stops serving,
   - the app loses readiness, returns a burst of 5xx or logs an `ERROR`,
   - the drift check drops or fails,
   - the backup login is used or locks,
   - a backup fails or does not run.
2. Each alert names the failing check, and `docs/operations.md` gives the first commands to run for it.
3. Nothing that leaves the VPS carries coordinates, cell IDs, IP addresses, search text, paths with IDs, or secrets.
4. Logs keep enough history to debug an alert days later.
5. Monitoring is off by default (dev, CI, e2e). A bad monitoring setting never stops the site.

**Non-goals**

- Dashboards, Prometheus, tracing and log aggregation (§3).
- Restarting containers automatically.
- Host disk, memory and CPU alerts. The rollout enables the VPS provider's alerts instead; no code is involved.
- The drift schedule (F23), the janitor (S4) and who can reach `/readyz` (F13). §4.8 says how each connects.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| Alert transport | Outbound heartbeat pings to hosted Healthchecks.io (success, `/start`, `/fail`). A missing ping is itself an alert (dead-man's switch) | Email from the app: needs SMTP secrets, and a dead app sends nothing. Prometheus + Alertmanager on the VPS: memory, and it dies with the VPS. Log-shipping SaaS: logs, and the admin IPs in them, leave the VPS |
| Public outage | An external HTTP monitor on `https://thymeand.place/readyz` with a keyword and TLS-expiry check | In-app pings alone miss Caddy, TLS, DNS and firewall faults. Healthchecks.io's docs say it is not for "monitoring website uptime by probing it with HTTP requests" |
| Metrics | None at launch; the alerts need only in-process counters | `/metrics` on `:8080` (public through Caddy's catch-all). A separate internal listener (nothing would scrape it; §4.7 sets the rules for adding one) |
| 5xx signal | In-process counter in a middleware outside `web.Common`, read by a watchdog | Parsing `docker logs` from cron (fragile, needs Docker access) |
| `ERROR` signal | An `slog.Handler` wrapper counts `ERROR` records | Hooks at each of the ~30 `ERROR` call sites (new ones get missed) |
| Security events | A separate `admin-auth` check, so an app alert that is already open can't hide one | Folding them into the `app` check. Healthchecks.io alerts only when a check changes to down (verified), so a second failure during an open alert sends nothing |
| Drift | Its own daily check, pinged once per run | Folding it into `app` (it would recover 5 minutes later and hide the drop) |
| Backups | `backup.sh` pings from its `EXIT` trap: `/start`, then success or `/fail` naming the step | `&& curl` in crontab: cannot name the step or report a prune failure |
| Unhealthy app | Alert; the owner restarts it | An `autoheal` container (needs `docker.sock`, which is root-equivalent). The app exiting when not ready (restart loops during DB outages, against the core spec §11.6 retry panel) |
| Log retention | Drop 2xx probe lines. Keep 20 compressed rotated files. Rotate the backup log | A bigger `max-size` only (costs disk). Switching log driver (json-file already compresses) |
| Bad ping URL | Log `ERROR`, disable that ping, keep serving | Refusing to start (a monitoring typo would take the site down). The missing-ping alert still fires |
| Ping URL storage | Per-check UUID URLs in `/etc/thymeandplace/app.env`, treated as secrets | The repo or `compose.yaml` (anyone holding a URL can flip the check). Project ping-key + slug URLs (one leak exposes every check) |

## 4. Design

### 4.1 Checks

| Check | Sent by | Healthchecks.io schedule (§4.9) | Goes down when |
|---|---|---|---|
| `app` | App watchdog (§4.3) | Period 5 min, grace 5 min | No ping (app, VPS or egress down), or the watchdog reports a failure |
| `admin-auth` | App watchdog | Period 1 day, grace 1 h | Backup login used, backup lockout engaged, or an S4 growth warning. The long period means an app outage alerts only through `app` |
| `drift` | `calibrun.Drift` | Period 1 day, grace 2 h | The score drops by more than 0.02, the run fails, or no run happens |
| `backup` | `deploy/backup/backup.sh` | Cron `15 3 * * *`, VPS time zone, grace 1 h | Non-zero exit, prune failure, no success within grace of `/start`, or no run |
| `restore-test` | `deploy/backup/restore-test.sh` | Cron `15 4 1 * *`, grace 2 h (§8 Q3) | Count mismatch, no dump, or no run |
| `site` | External monitor (§4.6) | ≤ 5 min | Non-200, body not `ready`, TLS error, or certificate expiry within 14 days |

Checked in the Healthchecks.io docs through Context7:
- `/fail` and `/start` behave as used here. After `/start`, a success must arrive within the grace time.
- It stores up to 100,000 bytes of each ping body (`Ping-Body-Limit`).
- Clients should ping one check at most 5 times a minute.
- Checks take cron or `OnCalendar` schedules with a time zone, and can be limited to `POST` pings, so a link preview or crawler can't flip a check.
- The free plan has 20 checks. SMS and phone calls are paid-only. Email, ntfy, Pushover, Telegram and Signal are listed with no paid-only mark.

Not verified: whether the alert message includes the last ping body. The runbook tells the owner to open the check's event log.

### 4.2 Pinger (`internal/monitor/pinger.go`)

```go
func NewPinger(check, rawURL string, log *slog.Logger) *Pinger // "" → disabled no-op
func (p *Pinger) Success(ctx context.Context, body string)
func (p *Pinger) Fail(ctx context.Context, body string)     // POSTs to <url>/fail
```

- It sends a `POST` with a `text/plain` body, cut to 10 kB.
- Each attempt times out after 5 s. It makes 3 attempts, backing off 1 s and then 2 s, and never runs on a request path.
- The URL must be absolute `https`, or `http` to a loopback host (for tests). Otherwise the pinger logs `ERROR "heartbeat URL invalid; check disabled" check=<name>` and does nothing.
- A failed ping logs `WARN "heartbeat ping failed" check=<name> err=<cause>`. `Client.Do` errors are `*url.Error`, and their `URL` field would print the secret (verified in the Go docs). So the pinger logs only `errors.Unwrap(err)`. It logs at `WARN` so that a ping outage doesn't feed the `ERROR` counter.
- The app image is `distroless/static`, which includes CA certificates (verified), so outbound TLS needs no image change.

### 4.3 App watchdog (`internal/monitor/`)

**Inputs**

- **`Window`** (`window.go`). `monitor.CountResponses(w)` wraps `web.Common(...)` in `cmd/server/app.go`. Because `web.Common` recovers panics inside it, a panic is seen as a 500. The window counts requests and 5xx, and ignores `/healthz` and `/readyz` (readiness is sampled directly). It stores counts only. *(Amended in implementation:* it also skips a response whose request context was cancelled, since no client received it; otherwise a client dropping cache-miss requests could fail `app`. `web.Common`'s timeout derives its own context, so a server-side timeout still counts.)
- **`ErrorCounter`** (`errors.go`). An `slog.Handler` wrapper that counts records at `ERROR` or above. It keeps up to 3 distinct messages and never keeps attributes. A message containing anything other than letters, spaces and `:;,'_-` is kept as `(message withheld)`, so a message built from coordinates, IPs or IDs can't leak. Every current `ERROR` message is a constant that passes this rule. `WithAttrs` and `WithGroup` children share the count. `newApp` wraps the handler it receives, so every component built there is counted, and `main.go` does not change.
- **Security events.** A new `auth.Config.OnEvent func(kind string)` hook:
  - `backup_login` fires next to the existing `WARN` in `LogAttempt`.
  - `backup_lockout` fires in `BackupLogin` after a commit that sets `locked_until`, which also logs a new `WARN "backup login locked" until=<time>`.
  - Passkey logins and plain failures fire nothing. IPs stay in `admin_login_log` and the local log.
- **`Ready`.** `web.CheckReady(ctx, snapshot, ping) error` is extracted from `readyz` (`internal/web/server.go:208-223`), so the watchdog and `/readyz` can't drift apart. It checks that a snapshot is loaded and the DB answers a ping within 500 ms.

**Loop.** The watchdog samples `Ready` every minute. It reports every 5th sample, and also at once on the sample where readiness reaches 3 consecutive failures. That is at most one report a minute. A report sends:

- **To `app`:**
  - `Fail` with one line per reason:
    - `not ready: <catalog not loaded | database unavailable>` after 3 consecutive failed samples.
    - `5xx: <n> of <m> requests` when n ≥ 5 and n ≥ 5% of m.
    - `errors: <n> (<msg>; <msg>)` when n ≥ 1.
  - Otherwise `Success("ok requests=<m> 5xx=<n>")`.
- **To `admin-auth`:** `Fail("backup login used: <n>; backup lockout: <n>")` when there were events, otherwise `Success`.

Counters reset after each report. The watchdog starts in `app.start` with the watcher and the drift loop. A site still waiting for its first snapshot (`cmd/server/main.go:37-43`) therefore reports `not ready`. On shutdown it stops without pinging; the 5-minute grace covers a deploy. If the app is ever scaled to several replicas (core spec §4.1), each needs its own `app` check, or a healthy replica hides a dead one.

**Client-caused errors are not `ERROR`s.** Once `ERROR` pages the owner, an anonymous caller must not be able to cause one. `enrollFinish` logs `ERROR` for any non-ceremony error (`internal/admin/login.go:264-270`). That includes a body over 64 KiB, which `limitCeremonyBody` rejects (`internal/admin/login.go:69-77`), and CSRF doesn't stop a script before sign-in. The passkey path already logs these at `WARN` (`internal/admin/login.go:111-118`). `enrollFinish` does the same for body-read errors and keeps `ERROR` for store failures. *(Amended in implementation:* on every route an anonymous caller can reach, and for the sign-in audit writes, a store failure logs `WARN` instead of `ERROR` when the request's own context is already done, so a client that disconnects mid-request can't page the owner. Real store failures stay `ERROR`.)

### 4.4 Drift, backup and restore-test pings

**Drift.** `calibrun.Drift` gets `Notify func(ctx context.Context, failed bool, summary string)`. `RunOnce` calls it exactly once, from a `defer`, and skips the call if `ctx` was cancelled:
- On a drop: `failed=true`, summary `calibration score dropped 0.812 → 0.771 (data version a1b2 → c3d4)`.
- On an error: `failed=true`, summary `drift check failed: no catalog snapshot loaded` or `drift check failed: database or scoring error (see the app log)`. *(Amended in implementation:* a fixed category, never the raw error, because a pgx error can name a host and §4.7 forbids IPs in ping bodies. Don't restore `<err>`.)
- Otherwise: `failed=false` with the score.

The existing log lines stay. The next run compares against the lowered score, so the check recovers a day later. That means "no further drop", not "fixed", and the runbook says so. A failed run also logs `ERROR`, so `app` fails as well, once per failed attempt (since #23 each retry logs `ERROR` again). That duplicate is accepted.

**Backup.** A new `deploy/backup/heartbeat.sh`, sourced by both scripts, defines `hc <suffix> [body]`:
- It does nothing when `HEARTBEAT_URL` is empty.
- Otherwise it runs `curl -fsS -m 10 --retry 3 -o /dev/null --data-raw` and never fails its caller.
- The image already has `curl` (`deploy/backup/Dockerfile:19-21`).

Each script maps its own variable (`HEARTBEAT_BACKUP_URL`, `HEARTBEAT_RESTORE_TEST_URL`) to `HEARTBEAT_URL`.

`backup.sh` changes:
- **One `EXIT` trap.** It replaces the existing trap (`deploy/backup/backup.sh:11`), still deletes the dump, and keeps the exit status (`rc=$?` ... `exit $rc`). It is set before anything else.
- **Explicit variable check.** The `: "${VAR:?}"` line becomes a check that calls `exit 2` naming the missing variable. An explicit `exit` always runs the trap. Whether dash runs it when a `:?` expansion aborts the shell was not verified.
- **Step names.** A `step` variable names the stage: `env`, `alias`, `dump`, `upload`, `weekly`, `prune`. `hc /start` runs after the variable check. *(Amended by #12:* `verify` and `encrypt` come between `dump` and `upload`, so the steps are `env`, `alias`, `dump`, `verify`, `encrypt`, `upload`, `weekly`, `prune`.)
- **The trap sends one of three pings:**
  - `/fail` with `backup failed during <step> (exit <rc>)` when `rc` ≠ 0.
  - `/fail` with `dump uploaded; pruning failed: <prefix>[; <prefix>]` (`backups/daily`, `backups/weekly`) when a prune failed. The exit status stays 0. *(Amended in implementation:* the `mc` message stays in stderr only, since a network error carries the object store's IP and §4.7 forbids IPs in ping bodies.)
  - Otherwise the success ping with `backup ok: backups/daily/<date>.dump`. *(Amended by #12:* `backups/daily/<date>.dump.age`, since dumps are encrypted before upload.)

`restore-test.sh` merges its ping into its existing scratch-DB trap (`deploy/backup/restore-test.sh:21`). It sends the count table as the failure body.

A run that never starts (Docker down, image missing, `db` unhealthy) sends nothing, and the missed-schedule alert fires.

### 4.5 Logs and retention

- `AccessLog` skips `/healthz` and `/readyz` lines when the status is 2xx. Failing probes are still logged. This drops about 10 lines a minute.
- `deploy/compose.yaml` `x-logging` gets `max-file: "20"` and `compress: "true"` (the `json-file` option is verified in the Docker docs). Compression alone doesn't add history, because `max-file` counts files of up to 10 MB of raw log each. Raising it does. The worst case is 200 MB per container if nothing compressed. The rollout measures the real size. `docker logs` decompresses rotated files when it reads them (verified).
- **Backup log.** Recommend the systemd timer variant (journald). For cron, add `/etc/logrotate.d/thymeandplace-backup` (`weekly`, `rotate 8`, `compress`, `missingok`, `notifempty`).
- **Durable records** are in the database, not the logs: `calibration_run` has every drift score, and `admin_login_log` has every sign-in. The runbook points at both.

### 4.6 External uptime monitor

This is configured at a vendor (§8 Q2), not in the repo. The vendor must support:
- an HTTPS check that `/readyz` returns 200 with a body of exactly `ready`, every 5 minutes or less,
- alerting only after 2 consecutive failures, so a deploy's maintenance page doesn't page the owner,
- a certificate-expiry warning at 14 days,
- email and push notifications.

Caddy starts renewing with a third of a certificate's lifetime left (verified), so the 14-day warning means renewal has been failing for a while. If F13 takes `/readyz` off the public site, the monitor checks `/` for `Find your town` (`internal/web/views/layout.templ:22`). DB readiness then rests on the `app` check.

### 4.7 Privacy rules

| Channel | May carry | Never carries |
|---|---|---|
| App log (stdout) | Unchanged: request ID, path without query, `cell_id`, admin IP on admin events (core spec §15) | Coordinates, query strings, public visitor IPs, ping URLs |
| Ping bodies (Healthchecks.io) | Counts, reason labels, constant `ERROR` messages, calibration scores, data-version strings, backup object keys, table row counts | Paths, `cell_id`, coordinates, any IP, log attributes, secrets |
| External monitor | Its own probe of `/readyz` or `/` | Anything about visitors |
| A future metrics endpoint | Status class and fixed route class (`season`, `places`, `ingredient`, `admin`, `static`, `other`) | Raw paths, query values, `cell_id`, IPs or slugs as labels |

A future metrics endpoint must use its own port. Caddy must never proxy that port, and `ports:` must never publish it.

### 4.8 Related findings

- **F13 (`/readyz` exposure).** The watchdog calls `web.CheckReady` in-process and needs no route. Only the external monitor's URL depends on F13 (§4.6).
- **F23 (drift loop timing).** The `drift` check's period follows what F23 ships: 1 day with 2 h grace, or a cron schedule if F23 pins a time of day. Until then, a restart that starts the loop before the snapshot loads sends a truthful `drift check failed: ... no catalog snapshot loaded`, and the check stays down until the next run. Once #23 lands (`2026-09-28-scheduled-jobs-retry-design.md`), the loop waits quietly for the snapshot, runs 24 h after the last recorded run rather than at each start, and retries failures after 1 min, doubling to 1 h. The 1-day period with 2 h grace still fits.
- **S4 (janitor).** The admin abuse resistance design (`2026-09-27-admin-abuse-resistance-design.md` §4.5) proposes it as an in-app daily prune. It needs no new check:
  - A prune failure logs `ERROR`, which fails `app`.
  - Its `WARN "admin login log growing fast"` also calls `OnEvent("login_log_growth")`, which fails `admin-auth`.
  - If the janitor ends up as a host cron job, it sources `heartbeat.sh` and gets its own cron check, like `backup`.
  - Whatever signature F04 gives `LogAttempt`, the `backup_login` hook stays next to the `WARN`.

### 4.9 Values

| Item | Value |
|---|---|
| Watchdog sample / report | 1 min / 5 min |
| Not-ready alert | 3 consecutive failed samples |
| 5xx alert | ≥ 5 and ≥ 5% of non-probe responses in a report |
| `ERROR` alert | ≥ 1 record in a report |
| Check schedules | As in §4.1 |
| Ping timeout / attempts / body cap | 5 s / 3 / 10 kB |
| External monitor | ≤ 5 min, alert after 2 failures, TLS warning at 14 days |
| Container logs | `json-file`, 10 MB x 20 files, rotated files compressed |

## 5. Implementation outline

1. **`internal/monitor/`** (new package):
   - `pinger.go`
   - `window.go` (`Window`, `CountResponses`)
   - `errors.go` (`ErrorCounter`)
   - `watchdog.go`, with injectable sample/report durations and pingers
   - a `_test.go` for each
2. **`internal/web/server.go`:** extract `CheckReady`, and have `readyz` use it. **`internal/web/middleware.go`:** `AccessLog` skips 2xx probe lines.
3. **`internal/auth/service.go`, `internal/auth/backup.go`:** add `Config.OnEvent`, fire `backup_login` and `backup_lockout`, and add the lockout `WARN`.
4. **`internal/admin/login.go`:** in `enrollFinish`, log body-read errors at `WARN`.
5. **`internal/calibrun/drift.go`:** add `Notify` and the once-per-`RunOnce` `defer`.
6. **`cmd/server/app.go`:**
   - `loadConfig` reads `HEARTBEAT_APP_URL`, `HEARTBEAT_ADMIN_AUTH_URL` and `HEARTBEAT_DRIFT_URL`.
   - `newApp` wraps the log handler in `ErrorCounter`, builds the pingers, `Window` and watchdog, wraps `a.handler` in `CountResponses`, and wires `auth.Config.OnEvent` and `Drift.Notify`.
   - `start` launches the watchdog.
7. **`deploy/backup/`:** add `heartbeat.sh`, change `backup.sh` and `restore-test.sh`, and add `heartbeat.sh` to the `COPY` line in `Dockerfile`.
8. **`deploy/compose.yaml`:** change `x-logging`. **Env templates:** add the five `HEARTBEAT_*` lines, commented out and marked as secrets. *(Amended in implementation:* env files are per service: the three app URLs in `deploy/env/app.env.example`, `HEARTBEAT_BACKUP_URL` in `backup.env.example`, `HEARTBEAT_RESTORE_TEST_URL` in `restore.env.example`.)
9. **`.github/workflows/ci.yml`:** in the `deploy-checks` job, add `sh -n deploy/backup/*.sh`.
10. **Docs:**
    - `docs/operations.md` (§7).
    - Core spec §10.4: backup-login use and lockout alert the owner.
    - Core spec §11.4: 2xx probe lines are skipped.
    - Core spec §13.2: drift also reports to its heartbeat.
    - Core spec §14:
      - Health: "Docker and Caddy probe `/healthz`, and Caddy routes on it. Nothing restarts an unhealthy container; alerts do. `/readyz` is for monitoring."
      - Logs: retention.
      - New Monitoring bullet.
      - Backups: the monthly restore test, if §8 Q3 is yes.
    - Core spec §15: the payload rule (§4.7).
    - Core spec §16: the §4.9 rows.

## 6. Testing

- **`pinger_test.go`** (table-driven; an `httptest.Server` records method, path and body):
  - `Success` sends `POST /<id>`, and `Fail` sends `POST /<id>/fail`.
  - An empty URL sends nothing. A non-loopback `http://` URL or an unparseable one disables the pinger and logs `ERROR` once.
  - A 500 followed by a 200 is retried.
  - A hanging server returns within the timeout budget.
  - The log never contains the URL's UUID.
- **`window_test.go`:**
  - `(path, status)` tables: 4 of 10 doesn't breach, 5 of 50 does, 5 of 200 doesn't. Probes are ignored, and counters reset.
  - `web.Common` around a panicking handler is counted as a 500.
- **`errors_test.go`:**
  - `ERROR` is counted and `WARN` is not.
  - Attributes are never kept.
  - `lookup failed for 51.5,-0.1` is kept as `(message withheld)`.
  - `WithAttrs` and `WithGroup` children share the count.
- **`watchdog_test.go`** (fake ticks, fake `Ready`, recording pingers):
  - Healthy: one success per report.
  - 2 failed samples then ready: no fail.
  - 3 failed samples: an immediate fail that names the reason.
  - A 5xx breach, an `ERROR` and a `backup_login` event each fail the right check, and the next clean report succeeds.
  - At most one ping per check per minute.
  - Cancelling the context sends nothing.
  - No body matches an IPv4 or IPv6 pattern or contains `lat`, `lng` or `cell`.
- **`internal/web/middleware_test.go`:** `TestAccessLogSkipsHealthyProbes`. A 200 on `/healthz` is not logged, and a 503 on `/readyz` is. **`server_test.go`:** `TestHealthAndReady` still passes, now through `CheckReady`.
- **`internal/auth/backup_test.go`** (testcontainers):
  - The hook fires `backup_login` on success.
  - It fires `backup_lockout` on the 5th consecutive failure, and not on failures 1-4 or on an attempt while locked.
  - Passkey outcomes fire nothing.
- **`internal/admin/login_test.go`:** `TestEnrollFinishOversizeBodyIsNotError`. A body over 64 KiB returns 400 and logs no `ERROR` record.
- **`internal/calibrun/drift_test.go`:**
  - `TestDriftRecordsAndWarns` records `Notify` calls: `failed=false`, then `failed=true` with both scores and both data versions.
  - `TestDriftNeedsSnapshot` sees `failed=true`.
- **`cmd/server/app_test.go`:**
  - `TestLoadConfig` covers the new variables.
  - A testcontainers test starts `newApp` with `HEARTBEAT_APP_URL` pointing at a loopback `httptest` receiver and short intervals, and asserts a success ping.
  - With the variable unset, nothing is sent.
- **Shell:** CI runs `sh -n`. Then run the "Verifying locally" recipe in `docs/operations.md` with `-e HEARTBEAT_BACKUP_URL=<throwaway check>`:
  - A normal run shows `/start`, then success.
  - `-e BUCKET_NAME=missing` shows `/fail` with `upload`.
  - `-e DATABASE_URL=` shows `/fail` with `env`.
- **e2e:** no change. `deploy/e2e.env` leaves every `HEARTBEAT_*` unset.

## 7. Rollout, operations and rollback

**Rollout**

1. Create a Healthchecks.io project with the five checks and schedules in §4.1.
   - Allow `POST` only.
   - Set the cron checks' time zone to the VPS's (`timedatectl`).
   - Attach email and one push channel (§8 Q1), and send a test notification.
2. Put the per-check UUID URLs in the per-service env files: `app`, `admin-auth` and `drift` in `app.env`, `backup` in `backup.env`, `restore-test` in `restore.env` (amended in implementation; there is no shared env file).
3. Configure the external monitor (§4.6), and turn on the VPS provider's disk and CPU alerts.
4. `docker compose up -d --build`. Compose recreates containers whose configuration changed (verified), so the new log options take effect. *(Amended by #8:* plain `docker compose up -d`, which runs the deployed release through `deploy/.env`; there `--build` builds nothing. See `docs/operations.md`, "Monitoring and alerts" › "Setting up".) Check:
   - `docker inspect --format '{{.HostConfig.LogConfig}}' thymeandplace-app-1` shows `compress:true`.
   - `app` and `admin-auth` go green within a few minutes, and `drift` goes green after the startup run. Once #23 has landed, a deploy doesn't trigger a drift run: `drift` goes green at the next scheduled run, 24 h after `SELECT ran_at FROM calibration_run ORDER BY id DESC LIMIT 1`.
   - A week later, `ls -l /var/lib/docker/containers/*/` shows the real compressed sizes.
5. Run a backup by hand. Expect start, then success. Then run one with `-e BUCKET_NAME=missing` and expect a failure alert.
6. Drill on a quiet evening: `docker compose stop app` for 12 minutes. Expect `site` and `app` alerts, then recovery after `start`.

**`docs/operations.md` changes**

- **New "Monitoring and alerts" section.** It covers:
  - the checks and their settings,
  - where the URLs live (as secrets),
  - the §4.7 privacy rules,
  - pausing checks for planned work longer than 10 minutes,
  - a runbook table:

  | Alert | First step |
  |---|---|
  | `site` | `docker compose ps`, `docker compose logs --since 30m caddy app`. Run `docker compose restart app` if it is hung |
  | TLS warning | `docker compose logs caddy \| grep -i acme` |
  | `app` missing ping | Is the VPS up? `docker compose ps`. Is outbound HTTPS open? |
  | `app` not ready | `docker compose ps db`, `docker compose logs --since 30m db app` |
  | `app` 5xx or errors | Read the check's event log, then `docker compose logs --since 15m app \| grep -E '"level":"ERROR"\|"status":5'` |
  | `admin-auth` | Query `admin_login_log` with `psql`. If it wasn't you: `admin revoke-sessions`, then `admin set-backup` |
  | `drift` | `SELECT * FROM calibration_run ORDER BY id DESC LIMIT 5`. Consider `ingest <dataset> rollback`. Recovery the next day means no further drop, not fixed |
  | `backup` | `journalctl -u thymeandplace-backup` or the log file, then rerun by hand |
  | `restore-test` | Read the table. Don't trust the backups until it passes |

- **"First deploy":** add "Set up monitoring" before step 9.
- **"Nightly backups":** mention the pings, the logrotate snippet and the monthly restore-test cron line.
- **"Health and logs":** describe the new retention and the skipped probe lines, and replace "add a supervisor" with a pointer to the alerts.

**Rollback.** Pause the checks, then delete the `HEARTBEAT_*` lines. An unset variable turns that ping off, and older images ignore the variables. To revert `x-logging`, run `docker compose up -d` so the containers are recreated.

## 8. Open questions for the owner

1. **Alert channels.** Recommended: email plus one free push channel (ntfy, Pushover, Telegram or Signal) on every check, and a daily reminder email while any check is down. SMS and phone calls need a paid Healthchecks.io plan.
2. **External monitor vendor.** Recommended: any free tier that meets §4.6. This spec doesn't name one, because vendor features were not verified.
3. **Automate the monthly restore test.** It restores into a scratch database on the production Postgres, which needs disk for one more copy for a few minutes. Recommended: yes, cron `15 4 1 * *`, with its own check.
4. **Hosted or self-hosted Healthchecks.** Recommended: hosted. Self-hosting on this VPS defeats the dead-man's switch, and hosting it elsewhere adds a second server.
5. **Request counts in `app` pings.** They tell Healthchecks.io roughly how much traffic the site gets. Recommended: send them. They are aggregate counts and help read an alert.
6. **Thresholds (§4.9).** Recommended: as written, reviewed after the first month of traffic.
