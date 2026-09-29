# Thyme & Place - Drift check schedule and retry - Design

- **Status:** Proposed
- **Date:** 2026-09-28
- **Source:** Architectural audit, 2026-09-27 (finding F23)
- **Related issues:** [#23](https://github.com/Muddl/thymeandplace/issues/23) (F23)
- **Amends:** core spec §13.2 (drift check schedule and retry), §14 (admin prune retries), §16 (new value: drift schedule)

---

## 1. Problem

The daily drift check can fail at startup and then not run again for 24 hours.

- **Startup order.** `main` calls `a.start(ctx)` before `a.waitForSnapshot` (`cmd/server/main.go`). `start` launches `a.drift.Loop(ctx, 24*time.Hour)`, and a failed initial catalog load is only logged (`cmd/server/app.go`).
- **What a failure costs.** `Loop` calls `RunOnce` straight away. `RunOnce` returns an error when no snapshot is loaded (`internal/calibrun/drift.go`), and the failure is logged at `ERROR` and never retried. The next attempt comes on a 24 h ticker. Any other failed run behaves the same way, for example when Postgres is restarting at the scheduled moment.
- **Restarts reset the cadence.** Every process start runs a check and inserts a `calibration_run` row. So the time of day moves with each deploy, and each restart adds a row, even though `calibration_run.ran_at` records when the last run happened.

The admin prune loop (`pruneLoop`, `cmd/server/app.go`, from the admin-abuse work) copies the same shape: run now, then tick every 24 h, and never retry. A prune that fails because the database is briefly down waits a day.

Startup, after a deploy or a VPS reboot, is exactly when a drift check matters most. With the observability design (`2026-09-27-observability-and-alerting-design.md` §4.4, §4.8), a failure there would hold the `drift` check down for a day.

## 2. Goals and non-goals

Goals:

1. The drift check never fails merely because the catalog snapshot hasn't loaded yet. It waits for it, quietly.
2. A failed drift run or prune is retried within minutes, with backoff, not a day later.
3. The drift check runs every 24 h **from the last recorded run**. A restart or deploy neither triggers an extra run nor moves the time of day.
4. The schedule is pure, table-testable code, shared by both jobs.
5. The existing log lines stay: the drift check's `ERROR` on failure, and the prune's `INFO`/`ERROR` lines.

Non-goals:

- A general job runner, persistent job queue or cron syntax.
- Anchoring the prune. It is cheap and idempotent, so it keeps running at startup.
- Coordinating replicas. Two replicas may both run a check. Each run just adds a row, which is harmless.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| When the drift check first runs | Anchored: 24 h after the latest `calibration_run.ran_at`, or at once if there is none or it is older (owner decision, 2026-09-28) | Run at every start plus the retry (simpler, but the time of day drifts with deploys and each restart adds a row) |
| Retry | `RetryMin` 1 min, doubling up to `RetryMax` 1 h, and back to `Every` after a success | A fixed 1 min (hammers a database that is down for an hour). No retry (the bug) |
| Waiting for the snapshot | Check `Snapshot()` every `RetryMin` before the first run. No `RunOnce`, no `ERROR`, no heartbeat | Treating it as a failed run (logs an `ERROR` and, with observability, pages the owner on every boot) |
| Where the loop lives | A new `internal/jobs` package with the schedule helpers and `Run`, used by `calibrun.Drift.Loop` and `cmd/server`'s prune | Copy the loop into both places (the drift in the two copies is how the prune loop inherited the bug) |
| Clock skew | A `ran_at` in the future waits `Every`, never longer | Trusting it (a clock set far ahead would stop the check indefinitely) |
| Failure to read the last run | Counts as a failed run and is retried | Running at once anyway (the run would most likely fail the same way) |

## 4. Design

### 4.1 `internal/jobs`

```go
// Schedule says how often a job runs and how it retries failures.
type Schedule struct{ Every, RetryMin, RetryMax time.Duration }

// FirstDelay is how long to wait before the first run of a job last run at
// last (ok=false: never). It is 0 when the last run is at least every ago,
// and never more than every (a future timestamp is clock skew).
func FirstDelay(last time.Time, ok bool, now time.Time, every time.Duration) time.Duration

// RetryDelay is the wait before retry attempt n (1-based): min, 2·min, 4·min,
// …, capped at max.
func RetryDelay(attempt int, min, max time.Duration) time.Duration

// Run waits first, then runs job repeatedly: every s.Every after a success,
// or RetryDelay after each consecutive failure, calling onErr with the
// error. It returns when ctx ends and never runs job after that.
func Run(ctx context.Context, s Schedule, first time.Duration, job func(context.Context) error, onErr func(error))
```

`Run` waits with a `time.Timer`, stopped and drained on exit.

### 4.2 `calibrun.Drift.Loop`

`Loop(ctx context.Context, s jobs.Schedule)` replaces `Loop(ctx, every)`:

1. **Wait for a snapshot.** While `d.Snapshot() == nil`, wait `s.RetryMin` (or return on `ctx.Done()`). The check in `RunOnce` stays as a guard.
2. **Find the first delay.** Read `store.LatestCalibrationRun`:
   - `pgx.ErrNoRows` means `first = 0`.
   - Success means `first = jobs.FirstDelay(row.RanAt.Time, true, now(), s.Every)`.
   - Any other error is logged at `ERROR` as `calibration drift check failed` and retried after `jobs.RetryDelay(n, …)`, as a failed run would be.
3. `jobs.Run(ctx, s, first, func(ctx) error { _, err := d.RunOnce(ctx); return err }, onErr)`, where `onErr` logs `calibration drift check failed` at `ERROR` unless `ctx` is done. That is the existing line.

`Drift` gains `Now func() time.Time` (nil means `time.Now`) so tests can place runs relative to "now".

### 4.3 `cmd/server`

- `start` calls `a.drift.Loop(ctx, jobSchedule)` with `jobSchedule = jobs.Schedule{Every: 24 * time.Hour, RetryMin: time.Minute, RetryMax: time.Hour}`.
- `pruneLoop` becomes `jobs.Run(ctx, jobSchedule, 0, prune, onErr)`. It still runs at startup, and still logs `INFO "admin prune"` after success and `ERROR "admin prune failed"` after failure, but failures now retry.
- `pruneLoop(ctx, every)` is kept for its test, taking a `jobs.Schedule` instead of a duration.

### 4.4 Interaction with other work

- **Observability** (`2026-09-28-observability-and-alerting.md` Task 6). `Notify` is still called once per `RunOnce`, so retries ping at most once a minute, within Healthchecks.io's guidance of 5 pings a minute. Waiting for the snapshot sends nothing. The `drift` check's period (1 day, 2 h grace) still fits an anchored schedule.
- **Season-cache consistency** (`2026-09-28-season-cache-consistency.md` Task 6 and its spec §7). A code-only deploy no longer triggers a drift run. The post-deploy check therefore reads the next scheduled run from `calibration_run` (within 24 h), or the owner can compare scores with the one-off measurement that plan already makes before merging.

### 4.5 Core spec text

| § | Change |
|---|---|
| §13.2 | "A daily in-app drift check runs 24 h after the last recorded run (or at once if none is that recent), waits for the catalog snapshot, and retries failures after 1 min, doubling to 1 h" |
| §14 | The prune bullet gains "failures retry after 1 min, doubling to 1 h" |
| §16 | New row: "Drift check: every 24 h from the last recorded run; retry 1 min doubling to 1 h" |

## 5. Implementation outline

1. `internal/jobs/jobs.go` and `jobs_test.go`.
2. `internal/calibrun/drift.go`: `Now`, and the new `Loop`. `drift_test.go`: the new integration tests (§6).
3. `cmd/server/app.go`: `jobSchedule`, the `start` wiring, and `pruneLoop` on `jobs.Run`. `cmd/server/app_test.go`: `TestStartPrunesAdminTables` passes a `jobs.Schedule`.
4. Update the season-cache plan's post-deploy check (§4.4).
5. Docs: the core spec (§4.5), and `docs/operations.md` "Health and logs" (when the drift check runs, and that repeated `calibration drift check failed` lines a minute or more apart are retries).

## 6. Testing

- **`TestFirstDelay`** (table-driven): no previous run gives 0. Ran 1 h ago gives 23 h. Ran 24 h ago gives 0. Ran 25 h ago gives 0. Ran 1 h in the future gives 24 h.
- **`TestRetryDelay`** (table-driven, min 1 min, max 1 h): attempts 1-7 give 1, 2, 4, 8, 16, 32 and 60 min, then stay at 60 min. Large attempt numbers don't overflow.
- **`TestRunRetriesAndStops`** (millisecond schedule, no database):
  - a job failing twice then succeeding runs 3 times, with `onErr` called twice;
  - after a success, the next run waits `Every`;
  - cancelling the context mid-wait returns promptly without another run.
- **`internal/calibrun` (testcontainers):**
  - With `Snapshot` nil for the first few checks and then set, `Loop` inserts exactly one `calibration_run` row, and the log never contains `calibration drift check failed`.
  - With `Cases` failing twice and then succeeding (millisecond schedule), there are 3 attempts, 2 `ERROR` lines and 1 row.
  - A row `ran_at` 1 h ago means `Loop` (`Every` 24 h) inserts nothing before the context ends.
  - A row from 25 h ago means one row is inserted promptly.
  - `TestDriftRecordsAndWarns` and `TestDriftNeedsSnapshot` pass unchanged.
- **`cmd/server`:** `TestStartPrunesAdminTables` still sees the expired session pruned at start, and the loop stops on cancel.

## 7. Rollout, operations and rollback

- Code only: no migration or config change.
- After deploy, the logs show no `calibration drift check` line unless the last run is more than 24 h old. `SELECT ran_at FROM calibration_run ORDER BY id DESC LIMIT 1` shows when the next run is due (24 h after that).
- Rollback: redeploy the previous image. The old binary runs a check at startup again.

## 8. Open questions for the owner

None. Anchoring was chosen on 2026-09-28.
