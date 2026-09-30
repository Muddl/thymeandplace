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

// A success resets the backoff: after nine failures and a success, the
// next failure retries after RetryMin (1 ms), not the 512 ms a tenth
// consecutive failure would wait.
func TestRunSuccessResetsBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	var at [12]time.Time // when each call started
	done := make(chan struct{})
	go func() {
		Run(ctx, Schedule{Every: time.Millisecond, RetryMin: time.Millisecond, RetryMax: time.Hour}, 0,
			func(context.Context) error {
				n := calls.Add(1)
				if n <= int32(len(at)) {
					at[n-1] = time.Now()
				}
				if n == 10 {
					return nil
				}
				return errors.New("boom")
			}, nil)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 12 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done // the job's writes to at happen before this
	if calls.Load() < 12 {
		t.Fatalf("calls=%d; want at least 12", calls.Load())
	}
	// Call 11 is the first failure after the success; call 12 is its retry.
	if gap := at[11].Sub(at[10]); gap > 256*time.Millisecond {
		t.Fatalf("retry after the first failure since a success came %v later; backoff was not reset", gap)
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
