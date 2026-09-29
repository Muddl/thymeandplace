# Drift Check Schedule and Retry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The drift check waits for the catalog snapshot, runs 24 h after the last recorded run, and retries failures within minutes. The admin prune retries failures too, instead of waiting a day.

**Architecture:** A new `internal/jobs` package holds two pure schedule helpers (`FirstDelay`, `RetryDelay`) and a timer loop (`Run`). `calibrun.Drift.Loop` waits for a snapshot, then reads `LatestCalibrationRun.ran_at` to find its first delay, then hands over to `jobs.Run`. `cmd/server` runs both jobs on one `jobs.Schedule{24h, 1m, 1h}`. The prune keeps running at startup (`first = 0`).

**Tech Stack:** Go 1.26 (`time`, `context`), pgx/v5, testcontainers via `internal/dbtest`.

**Spec:** `docs/superpowers/specs/2026-09-28-scheduled-jobs-retry-design.md`. Issue [#23](https://github.com/Muddl/thymeandplace/issues/23). The anchored schedule was the owner's choice on 2026-09-28.

**Other plans:**
- The observability plan's Task 6 adds `Drift.Notify` inside `RunOnce`. It lands cleanly before or after this plan, because `Loop` only calls `RunOnce`.
- The season-cache plan's post-deploy check already reads the next scheduled run.

## Global Constraints

- `jobSchedule = jobs.Schedule{Every: 24 * time.Hour, RetryMin: time.Minute, RetryMax: time.Hour}`, used for both the drift check and the prune.
- Retry: `RetryMin`, doubling on each consecutive failure, capped at `RetryMax`. A success resets the backoff, and the next run is `Every` later.
- Drift first run: at once if there is no previous run, or if the last `ran_at` is at least `Every` ago. Otherwise at `ran_at + Every`. A future `ran_at` waits exactly `Every`.
- While `Snapshot()` is nil, the loop checks again every `RetryMin`, with no `RunOnce` call, no `ERROR` log and no heartbeat.
- The log lines stay: `ERROR "calibration drift check failed"`, `INFO "admin prune"` and `ERROR "admin prune failed"`. None is logged after the context ends.
- The prune isn't anchored: it runs at startup (`first = 0`).
- No migration, no new dependency.

## Review Focus

1. **A database outage long enough for many retries.** The backoff must reach 1 h and stay there, and never overflow `time.Duration` however many failures pile up. Task 1 tests attempt 10,000.
2. **Shutdown while a job is waiting to retry.** `Run` must return promptly, without running the job again or logging an error. Task 1's cancel test covers the waiting case, and Task 2 covers cancellation during the snapshot wait.
3. **The first read of `LatestCalibrationRun` failing** (the database is down at boot). It must retry with backoff and log `ERROR`, not run at once and not give up. Task 2 covers it with a closed pool.
4. **A `ran_at` from a clock set far ahead.** It must wait exactly `Every`, never longer. Task 1's `FirstDelay` table covers it.
5. **Two replicas of the app.** Each schedules independently, and at worst both insert a row at about the same time. That's harmless, and needs no code, only the note in `docs/operations.md` (Task 3).

---

### Task 1: `internal/jobs`

**Files:**
- Create: `internal/jobs/jobs.go`
- Test: `internal/jobs/jobs_test.go`

**Interfaces:**
- Produces:
  ```go
  type Schedule struct{ Every, RetryMin, RetryMax time.Duration }
  func FirstDelay(last time.Time, ok bool, now time.Time, every time.Duration) time.Duration
  func RetryDelay(attempt int, lo, hi time.Duration) time.Duration
  func Run(ctx context.Context, s Schedule, first time.Duration, job func(context.Context) error, onErr func(error))
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/jobs/jobs_test.go`:

```go
package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestFirstDelay(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	for _, tc := range []struct {
		name string
		last time.Time
		ok   bool
		want time.Duration
	}{
		{"no previous run", time.Time{}, false, 0},
		{"ran 1 h ago", now.Add(-time.Hour), true, 23 * time.Hour},
		{"ran exactly a day ago", now.Add(-day), true, 0},
		{"ran 25 h ago", now.Add(-25 * time.Hour), true, 0},
		{"ran in the future (clock skew)", now.Add(time.Hour), true, day},
		{"ran far in the future", now.Add(1000 * time.Hour), true, day},
	} {
		if got := FirstDelay(tc.last, tc.ok, now, day); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRetryDelay(t *testing.T) {
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60}
	for i, w := range want {
		if got := RetryDelay(i+1, time.Minute, time.Hour); got != w*time.Minute {
			t.Errorf("attempt %d: got %v, want %v", i+1, got, w*time.Minute)
		}
	}
	if got := RetryDelay(10_000, time.Minute, time.Hour); got != time.Hour {
		t.Fatalf("attempt 10000: got %v; must stay capped without overflow", got)
	}
	if got := RetryDelay(0, time.Minute, time.Hour); got != time.Minute {
		t.Fatalf("attempt 0 is treated as 1: got %v", got)
	}
}

func TestRunRetriesThenWaitsEvery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls, errs atomic.Int32
	done := make(chan struct{})
	go func() {
		Run(ctx, Schedule{Every: time.Hour, RetryMin: time.Millisecond, RetryMax: 4 * time.Millisecond}, 0,
			func(context.Context) error {
				if calls.Add(1) <= 2 {
					return errors.New("boom")
				}
				return nil
			},
			func(error) { errs.Add(1) })
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // a 4th call would come only after Every (1 h)
	if calls.Load() != 3 || errs.Load() != 2 {
		t.Fatalf("calls=%d errs=%d; want 3 and 2", calls.Load(), errs.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunStopsWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		Run(ctx, Schedule{Every: time.Hour, RetryMin: time.Hour, RetryMax: time.Hour}, time.Hour,
			func(context.Context) error { calls.Add(1); return nil }, nil)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return while waiting for its first run")
	}
	if calls.Load() != 0 {
		t.Fatal("no run may happen after cancel")
	}
}

// A job that fails because the context ended isn't reported as a failure.
func TestRunNoErrorAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var errs atomic.Int32
	Run(ctx, Schedule{Every: time.Hour, RetryMin: time.Millisecond, RetryMax: time.Millisecond}, 0,
		func(ctx context.Context) error { cancel(); return ctx.Err() },
		func(error) { errs.Add(1) })
	if errs.Load() != 0 {
		t.Fatalf("onErr called %d times after cancel", errs.Load())
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/jobs/ -count=1`
Expected: FAIL to build (no non-test files).

- [ ] **Step 3: Implement**

Create `internal/jobs/jobs.go`:

```go
// Package jobs runs the app's periodic background work (the drift check,
// the admin prune) on a schedule that retries failures with backoff.
package jobs

import (
	"context"
	"time"
)

// Schedule says how often a job runs and how it retries failures.
type Schedule struct{ Every, RetryMin, RetryMax time.Duration }

// FirstDelay is how long to wait before the first run of a job last run at
// last (ok=false: never). It is 0 when the last run is at least every ago,
// and never more than every: a future timestamp is clock skew.
func FirstDelay(last time.Time, ok bool, now time.Time, every time.Duration) time.Duration {
	if !ok {
		return 0
	}
	since := now.Sub(last)
	switch {
	case since < 0:
		return every
	case since >= every:
		return 0
	}
	return every - since
}

// RetryDelay is the wait before retry attempt n (1-based; lower counts as
// 1): lo, 2·lo, 4·lo, …, capped at hi. It never overflows.
func RetryDelay(attempt int, lo, hi time.Duration) time.Duration {
	d := lo
	for i := 1; i < attempt; i++ {
		if d >= hi/2 {
			return hi
		}
		d *= 2
	}
	return min(d, hi)
}

// Run waits first, then runs job repeatedly: s.Every after a success, or
// RetryDelay after each consecutive failure, calling onErr (if set) with
// the error. It returns when ctx ends, and never runs job or calls onErr
// after that.
func Run(ctx context.Context, s Schedule, first time.Duration, job func(context.Context) error, onErr func(error)) {
	t := time.NewTimer(first)
	defer t.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		wait := s.Every
		if err := job(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			failures++
			if onErr != nil {
				onErr(err)
			}
			wait = RetryDelay(failures, s.RetryMin, s.RetryMax)
		} else {
			failures = 0
		}
		t.Reset(wait)
	}
}
```

From Go 1.23, `Timer.Reset` after the channel has been received from needs no drain, so this is safe on Go 1.26.

- [ ] **Step 4: Run the tests with the race detector**

Run: `go test -race ./internal/jobs/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/jobs/
git commit -m "jobs: a schedule with backoff retry for periodic background work (#23)"
```

### Task 2: Anchored, retrying drift loop; the prune on `jobs.Run`

**Files:**
- Modify: `internal/calibrun/drift.go` (`Now`, `now()`, a new `Loop`)
- Test: `internal/calibrun/drift_test.go` (four `TestLoop*` tests)
- Modify: `cmd/server/app.go` (`jobSchedule`, `start`, `pruneLoop`)
- Modify: `cmd/server/app_test.go` (`TestStartPrunesAdminTables` calls `pruneLoop` with a `jobs.Schedule`)

**Interfaces:**
- Consumes: `jobs.Schedule`, `jobs.FirstDelay`, `jobs.RetryDelay`, `jobs.Run` (Task 1).
- Produces:
  - `Drift.Now func() time.Time` (nil means `time.Now`)
  - `func (d *Drift) Loop(ctx context.Context, s jobs.Schedule)`, replacing `Loop(ctx, every time.Duration)`
  - `func (a *app) pruneLoop(ctx context.Context, s jobs.Schedule)`

- [ ] **Step 1: Write the failing tests**

Append to `internal/calibrun/drift_test.go` (add imports `"errors"`, `"sync"`, `"sync/atomic"`, `"time"` and `"thymeandplace/internal/jobs"` as needed):

```go
// syncLog is a log writer safe for the loop's goroutine and the test.
type syncLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *syncLog) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

var fast = jobs.Schedule{Every: time.Hour, RetryMin: 5 * time.Millisecond, RetryMax: 20 * time.Millisecond}

// loopDrift is a Drift over pool with one scorable case.
func loopDrift(t *testing.T, pool *pgxpool.Pool, snap *atomic.Pointer[catalog.Snapshot], cases func(context.Context) ([]calibration.Case, error), log *syncLog) *Drift {
	t.Helper()
	return &Drift{
		Pool: pool, Snapshot: snap.Load, Cases: cases,
		DataVersion: func() string { return "v1" },
		Log:         slog.New(slog.NewTextHandler(log, nil)),
	}
}

func oneCase(t *testing.T) func(context.Context) ([]calibration.Case, error) {
	t.Helper()
	fx, err := calibration.LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	n := climatetest.Temperate()
	cases := []calibration.Case{{Fixture: fx[0], Input: season.Input{Normals: &n}}}
	return func(context.Context) ([]calibration.Case, error) { return cases, nil }
}

func runs(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM calibration_run`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func startLoop(d *Drift, s jobs.Schedule) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Loop(ctx, s); close(done) }()
	return func() { cancel(); <-done }
}

// No snapshot yet: wait quietly, then run once.
func TestLoopWaitsForSnapshot(t *testing.T) {
	pool := dbtest.New(t)
	var snap atomic.Pointer[catalog.Snapshot]
	var log syncLog
	stop := startLoop(loopDrift(t, pool, &snap, oneCase(t), &log), fast)
	defer stop()
	time.Sleep(40 * time.Millisecond) // several RetryMin periods with no snapshot
	if runs(t, pool) != 0 {
		t.Fatal("ran without a snapshot")
	}
	s, _ := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	snap.Store(s)
	waitFor(t, func() bool { return runs(t, pool) == 1 })
	time.Sleep(40 * time.Millisecond)
	if runs(t, pool) != 1 || strings.Contains(log.String(), "calibration drift check failed") {
		t.Fatalf("runs=%d log:\n%s", runs(t, pool), log.String())
	}
}

// Failures retry with backoff until one succeeds.
func TestLoopRetriesFailures(t *testing.T) {
	pool := dbtest.New(t)
	var snap atomic.Pointer[catalog.Snapshot]
	s, _ := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	snap.Store(s)
	var calls atomic.Int32
	ok := oneCase(t)
	var log syncLog
	stop := startLoop(loopDrift(t, pool, &snap, func(ctx context.Context) ([]calibration.Case, error) {
		if calls.Add(1) <= 2 {
			return nil, errors.New("cases unavailable")
		}
		return ok(ctx)
	}, &log), fast)
	defer stop()
	waitFor(t, func() bool { return runs(t, pool) == 1 })
	if n := strings.Count(log.String(), "calibration drift check failed"); calls.Load() != 3 || n != 2 {
		t.Fatalf("calls=%d failures logged=%d; want 3 and 2\n%s", calls.Load(), n, log.String())
	}
}

// Anchored: a run an hour ago means no run now; one 25 h ago means run now.
func TestLoopAnchorsToLastRun(t *testing.T) {
	ctx := context.Background()
	day := jobs.Schedule{Every: 24 * time.Hour, RetryMin: 5 * time.Millisecond, RetryMax: 20 * time.Millisecond}
	for _, tc := range []struct {
		ago  string
		want int
	}{{"1 hour", 1}, {"25 hours", 2}} {
		pool := dbtest.New(t)
		if _, err := pool.Exec(ctx, `INSERT INTO calibration_run (ran_at, data_version, score, compared) VALUES (now() - $1::interval, 'v0', 0.5, 1)`, tc.ago); err != nil {
			t.Fatal(err)
		}
		var snap atomic.Pointer[catalog.Snapshot]
		s, _ := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
		snap.Store(s)
		var log syncLog
		stop := startLoop(loopDrift(t, pool, &snap, oneCase(t), &log), day)
		if tc.want == 2 {
			waitFor(t, func() bool { return runs(t, pool) == 2 })
		} else {
			time.Sleep(200 * time.Millisecond)
		}
		stop()
		if got := runs(t, pool); got != tc.want {
			t.Fatalf("last run %s ago: %d rows, want %d", tc.ago, got, tc.want)
		}
	}
}

// If the last run can't be read, that is a failure: retry, log, and stop
// cleanly on cancel.
func TestLoopRetriesUnreadableLastRun(t *testing.T) {
	pool := dbtest.New(t)
	broken, err := pgxpool.New(context.Background(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	broken.Close() // every call now fails at once
	var snap atomic.Pointer[catalog.Snapshot]
	s, _ := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	snap.Store(s)
	var log syncLog
	stop := startLoop(loopDrift(t, broken, &snap, oneCase(t), &log), fast)
	waitFor(t, func() bool { return strings.Count(log.String(), "calibration drift check failed") >= 2 })
	stop() // must return promptly
}
```

Adjust imports to what the file already has. `pgxpool`, `catalog`, `catalogtest`, `calibration`, `climatetest`, `season`, `os`, `bytes`, `slog` and `strings` are used by the existing tests already, or must be added.

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/calibrun/ -run TestLoop -count=1`
Expected: FAIL to compile (`Loop` takes a `time.Duration`, not a `jobs.Schedule`).

- [ ] **Step 3: Implement the drift loop**

In `internal/calibrun/drift.go`:
- Add `Now func() time.Time // nil → time.Now; tests place runs relative to it` to `Drift`.
- Add:
  ```go
func (d *Drift) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}
  ```
- Replace `Loop` with:
  ```go
// Loop runs the drift check on schedule s (core spec §13.2). It waits,
// quietly, until a catalog snapshot is loaded; then runs s.Every after the
// last recorded run (at once if there is none that recent); and retries
// failures after s.RetryMin, doubling to s.RetryMax. It returns when ctx ends.
func (d *Drift) Loop(ctx context.Context, s jobs.Schedule) {
	for d.Snapshot() == nil {
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.RetryMin):
		}
	}
	logFail := func(err error) { d.Log.Error("calibration drift check failed", "err", err) }
	var first time.Duration
	for failures := 1; ; failures++ {
		last, err := store.New(d.Pool).LatestCalibrationRun(ctx)
		if err == nil {
			first = jobs.FirstDelay(last.RanAt.Time, true, d.now(), s.Every)
			break
		}
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if ctx.Err() != nil {
			return
		}
		logFail(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(jobs.RetryDelay(failures, s.RetryMin, s.RetryMax)):
		}
	}
	jobs.Run(ctx, s, first, func(ctx context.Context) error {
		_, err := d.RunOnce(ctx)
		return err
	}, logFail)
}
  ```
- Import `"thymeandplace/internal/jobs"`.

- [ ] **Step 4: Run the calibrun tests**

Run: `go test -race ./internal/calibrun/ -count=1`
Expected: PASS, including the unchanged `TestDriftRecordsAndWarns` and `TestDriftNeedsSnapshot`.

- [ ] **Step 5: Wire `cmd/server`**

In `cmd/server/app.go`:

```go
// jobSchedule runs the drift check and the admin prune: daily, retrying
// failures after 1 min, doubling to 1 h.
var jobSchedule = jobs.Schedule{Every: 24 * time.Hour, RetryMin: time.Minute, RetryMax: time.Hour}
```

In `start`:
- `go a.drift.Loop(ctx, 24*time.Hour)` becomes `go a.drift.Loop(ctx, jobSchedule)`.
- `go a.pruneLoop(ctx, 24*time.Hour)` becomes `go a.pruneLoop(ctx, jobSchedule)`.

Replace `pruneLoop` with:

```go
// pruneLoop prunes the admin audit, session and enrollment-token tables at
// once, then every s.Every, retrying failures with backoff, until ctx ends.
func (a *app) pruneLoop(ctx context.Context, s jobs.Schedule) {
	jobs.Run(ctx, s, 0, func(ctx context.Context) error {
		r, err := a.auth.Prune(ctx)
		if err != nil {
			return err
		}
		a.log.Info("admin prune", "login_log", r.LoginLog, "sessions", r.Sessions, "enrollment_tokens", r.EnrollmentTokens)
		return nil
	}, func(err error) { a.log.Error("admin prune failed", "err", err) })
}
```

Update the `start` doc comment to say the drift check runs on `jobSchedule`, anchored to the last run. Import `"thymeandplace/internal/jobs"`.

In `cmd/server/app_test.go` `TestStartPrunesAdminTables`, change `a.pruneLoop(stop, time.Hour)` to `a.pruneLoop(stop, jobs.Schedule{Every: time.Hour, RetryMin: time.Hour, RetryMax: time.Hour})`, adding the import.

- [ ] **Step 6: Run the server tests**

Run: `go test -race ./cmd/server/ -count=1`
Expected: PASS. An empty test database has no `calibration_run` row, so the drift check runs as soon as a snapshot loads, as before. `TestStartPrunesAdminTables` still sees the prune at start.

- [ ] **Step 7: Commit**

```bash
git add internal/calibrun/ cmd/server/
git commit -m "calibrun: drift check waits for a snapshot, anchors to the last run and retries; prune retries (#23)"
```

### Task 3: Docs

**Files:**
- Modify: `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` §13.2, §14, §16
- Modify: `docs/operations.md` ("Health and logs")

**Interfaces:** documentation only.

- [ ] **Step 1: Core spec**

Apply the spec's §4.5 table:
- §13.2: "A daily in-app drift check runs 24 h after the last recorded run (or at once if none is that recent), waits for the catalog snapshot, and retries failures after 1 min, doubling to 1 h".
- §14: the prune bullet gains "failures retry after 1 min, doubling to 1 h".
- §16: the new row "Drift check: every 24 h from the last recorded run; retry 1 min doubling to 1 h".

- [ ] **Step 2: Runbook**

In `docs/operations.md` "Health and logs", add:

```markdown
- The drift check runs 24 h after the last recorded run, not at each start: `SELECT ran_at FROM calibration_run ORDER BY id DESC LIMIT 1` plus 24 h is the next one. A deploy doesn't trigger it. Until the catalog snapshot loads it waits without logging. A failed run logs `calibration drift check failed` and is retried after 1 min, then 2, 4 … up to 1 h, so a string of those lines minutes apart is retries, not separate days. The admin prune still runs at each start, and its failures (`admin prune failed`) retry the same way. With more than one app replica, each schedules its own checks, and an extra `calibration_run` row is harmless.
```

- [ ] **Step 3: Commit**

```bash
git add docs/
git commit -m "docs: drift check schedule and retries (#23)"
```

### Task 4: Whole-repo check

- [ ] **Step 1:** Run `go vet ./... && go test -race ./... -count=1`. Expected: PASS.
- [ ] **Step 2:** Run `./e2e/run.sh`. Expected: PASS. The e2e stack starts empty, so the first drift run happens once the snapshot loads.
