package dataversion

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/store"
)

// Watcher keeps the current data version fresh via LISTEN, with a poll
// fallback for missed notifications.
type Watcher struct {
	pool     *pgxpool.Pool
	poll     time.Duration
	log      *slog.Logger
	onChange func(ctx context.Context, version string) error

	mu      sync.Mutex // serialises Refresh
	current sync.Map   // "v" → string
}

func NewWatcher(pool *pgxpool.Pool, poll time.Duration, log *slog.Logger, onChange func(ctx context.Context, version string) error) *Watcher {
	return &Watcher{pool: pool, poll: poll, log: log, onChange: onChange}
}

func (w *Watcher) Current() string {
	v, _ := w.current.Load("v")
	s, _ := v.(string)
	return s
}

// Refresh recomputes the version. Whenever it differs from the published one
// (including the very first publish) it runs onChange (e.g. load the
// snapshot) BEFORE publishing, so readers never see a version paired with an
// older snapshot, or a version with no snapshot at all. If onChange returns
// an error, the version is NOT published and the error is returned, so the
// next Refresh (NOTIFY, poll or a caller's retry) runs onChange again.
func (w *Watcher) Refresh(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	v, err := Current(ctx, store.New(w.pool))
	if err != nil {
		return err
	}
	old := w.Current()
	if v == old {
		return nil
	}
	if w.onChange != nil {
		if err := w.onChange(ctx, v); err != nil {
			return err
		}
	}
	w.current.Store("v", v)
	w.log.Info("data version", "version", v, "previous", old)
	return nil
}

// Reconnect backoff for the LISTEN session: a down database is retried at
// minBackoff, doubling up to maxBackoff, and reset once a session gets as far
// as LISTEN.
const (
	minBackoff = 500 * time.Millisecond
	maxBackoff = 30 * time.Second
)

func nextBackoff(d time.Duration) time.Duration {
	if d *= 2; d > maxBackoff {
		return maxBackoff
	}
	return d
}

// Run listens until ctx ends, reconnecting after errors with backoff.
func (w *Watcher) Run(ctx context.Context) {
	delay := minBackoff
	for ctx.Err() == nil {
		listened, err := w.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if listened {
			delay = minBackoff
		}
		w.log.Warn("data version listener stopped; retrying", "err", err, "retry_in", delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = nextBackoff(delay)
		if err := w.Refresh(ctx); err != nil {
			w.log.Warn("data version refresh failed", "err", err)
		}
	}
}

// session holds one LISTEN connection until an error. listened reports
// whether it got as far as LISTEN (i.e. the database was reachable).
func (w *Watcher) session(ctx context.Context) (listened bool, err error) {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN data_version_changed"); err != nil {
		return false, err
	}
	if err := w.Refresh(ctx); err != nil { // catch anything missed while reconnecting
		w.log.Warn("data version refresh failed", "err", err)
	}
	for {
		wctx, cancel := context.WithTimeout(ctx, w.poll)
		_, err := conn.Conn().WaitForNotification(wctx)
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return true, err
		}
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		// A notification arrived or the poll interval elapsed.
		if err := w.Refresh(ctx); err != nil {
			w.log.Warn("data version refresh failed", "err", err)
		}
	}
}
