package calibrun

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/jobs"
	"thymeandplace/internal/season"
)

func TestDriftRecordsAndWarns(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	fx, err := calibration.LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	n := climatetest.Temperate()
	cases := []calibration.Case{{Fixture: fx[0], Input: season.Input{Normals: &n}}}

	good, _ := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	worse := func() *catalog.Snapshot {
		tomato := catalogtest.Annual()
		tomato.Pairings = []string{"basil"}
		tomato.MonthsToHarvest = catalogtest.P(1)
		s, _ := good.With([]catalog.Ingredient{tomato}, nil)
		return s
	}()

	var logs bytes.Buffer
	cur := good
	d := &Drift{
		Pool:        pool,
		Snapshot:    func() *catalog.Snapshot { return cur },
		Cases:       func(context.Context) ([]calibration.Case, error) { return cases, nil },
		DataVersion: func() string { return "abc123" },
		Log:         slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatal("first run must not warn")
	}
	cur = worse
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "calibration score dropped") {
		t.Fatalf("no drift warning in:\n%s", logs.String())
	}
	var runs int
	var dv string
	_ = pool.QueryRow(ctx, `SELECT count(*), max(data_version) FROM calibration_run`).Scan(&runs, &dv)
	if runs != 2 || dv != "abc123" {
		t.Fatalf("runs=%d dv=%q", runs, dv)
	}
}

func TestDriftNeedsSnapshot(t *testing.T) {
	d := &Drift{Pool: dbtest.New(t), Snapshot: func() *catalog.Snapshot { return nil }}
	if _, err := d.RunOnce(context.Background()); err == nil {
		t.Fatal("want error without a snapshot")
	}
}

// syncLog is a log writer safe for the loop's goroutine and the test.
type syncLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *syncLog) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

var fast = jobs.Schedule{Every: time.Hour, RetryMin: 5 * time.Millisecond, RetryMax: 20 * time.Millisecond}

// loopDrift is a Drift over pool whose snapshot is read from snap.
func loopDrift(t *testing.T, pool *pgxpool.Pool, snap *atomic.Pointer[catalog.Snapshot], cases func(context.Context) ([]calibration.Case, error), log *syncLog) *Drift {
	t.Helper()
	return &Drift{
		Pool: pool, Snapshot: snap.Load, Cases: cases,
		DataVersion: func() string { return "v1" },
		Log:         slog.New(slog.NewTextHandler(log, nil)),
	}
}

// oneCase returns a Cases func with one scorable case.
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

// startLoop runs d.Loop until stop, which the test's cleanup also calls
// (a second call is harmless). stop fails the test if Loop doesn't return.
func startLoop(t *testing.T, d *Drift, s jobs.Schedule) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Loop(ctx, s); close(done) }()
	stop = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Loop did not stop after cancel")
		}
	}
	t.Cleanup(stop)
	return stop
}

// No snapshot yet: wait quietly, then run once.
func TestLoopWaitsForSnapshot(t *testing.T) {
	pool := dbtest.New(t)
	var snap atomic.Pointer[catalog.Snapshot]
	var log syncLog
	startLoop(t, loopDrift(t, pool, &snap, oneCase(t), &log), fast)
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
	startLoop(t, loopDrift(t, pool, &snap, func(ctx context.Context) ([]calibration.Case, error) {
		if calls.Add(1) <= 2 {
			return nil, errors.New("cases unavailable")
		}
		return ok(ctx)
	}, &log), fast)
	waitFor(t, func() bool { return runs(t, pool) == 1 })
	if n := strings.Count(log.String(), "calibration drift check failed"); calls.Load() != 3 || n != 2 {
		t.Fatalf("calls=%d failures logged=%d; want 3 and 2\n%s", calls.Load(), n, log.String())
	}
}

// Anchored: a run an hour ago means no run now; one 25 h ago means run now.
// "Now" is Drift.Now, fixed years in the past, so a loop that read the
// wall clock instead would find both runs stale and run at once.
func TestLoopAnchorsToLastRun(t *testing.T) {
	now := time.Date(2020, 6, 1, 12, 0, 0, 0, time.UTC)
	day := jobs.Schedule{Every: 24 * time.Hour, RetryMin: 5 * time.Millisecond, RetryMax: 20 * time.Millisecond}
	for _, tc := range []struct {
		ago  time.Duration
		want int
	}{{time.Hour, 1}, {25 * time.Hour, 2}} {
		t.Run(tc.ago.String(), func(t *testing.T) {
			pool := dbtest.New(t)
			if _, err := pool.Exec(context.Background(), `INSERT INTO calibration_run (ran_at, data_version, score, compared) VALUES ($1, 'v0', 0.5, 1)`, now.Add(-tc.ago)); err != nil {
				t.Fatal(err)
			}
			var snap atomic.Pointer[catalog.Snapshot]
			s, _ := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
			snap.Store(s)
			var log syncLog
			d := loopDrift(t, pool, &snap, oneCase(t), &log)
			d.Now = func() time.Time { return now }
			stop := startLoop(t, d, day)
					if tc.want == 2 {
				waitFor(t, func() bool { return runs(t, pool) == 2 })
			} else {
				time.Sleep(200 * time.Millisecond)
			}
			stop() // count every row the loop ever inserts
			if got := runs(t, pool); got != tc.want {
				t.Fatalf("last run %v ago: %d rows, want %d", tc.ago, got, tc.want)
			}
		})
	}
}

// If the last run can't be read, that is a failure: retry the read, log,
// never run the check blind, and stop cleanly on cancel.
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
	var calls atomic.Int32
	ok := oneCase(t)
	var log syncLog
	startLoop(t, loopDrift(t, broken, &snap, func(ctx context.Context) ([]calibration.Case, error) {
		calls.Add(1)
		return ok(ctx)
	}, &log), fast)
	waitFor(t, func() bool { return strings.Count(log.String(), "calibration drift check failed") >= 2 })
	if n := calls.Load(); n != 0 {
		t.Fatalf("drift check ran %d times with the last run unreadable", n)
	}
}
