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
		// select picks at random when both are ready; ctx wins.
		if ctx.Err() != nil {
			return
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
