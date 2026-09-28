package dataversion_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/dataversion"
	"thymeandplace/internal/dbtest"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestWatcherNotifyAndPoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)

	changes := make(chan string, 10)
	w := dataversion.NewWatcher(pool, 300*time.Millisecond, quiet(), func(_ context.Context, v string) error { changes <- v; return nil })
	if err := w.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	first := w.Current()
	if first != dataversion.Compute(0, nil) {
		t.Fatalf("initial = %q", first)
	}
	select { // onChange runs before the first publish too
	case v := <-changes:
		if v != first {
			t.Fatalf("first onChange got %q, want %q", v, first)
		}
	default:
		t.Fatal("onChange did not run before the first publish")
	}
	go w.Run(ctx)

	// A catalog write sends NOTIFY.
	if _, err := pool.Exec(ctx, `INSERT INTO ingredient (slug, name, category, kind, tip_choose, tip_store)
		VALUES ('banana','Banana','fruit','cultivated_year_round','c','s')`); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-changes:
		if v == first {
			t.Fatal("version unchanged")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no change after NOTIFY")
	}

	// A bump with no NOTIFY (a missed notification) is caught by the poll.
	before := w.Current()
	if _, err := pool.Exec(ctx, `UPDATE catalog_meta SET version = version + 100`); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-changes:
		if v == before {
			t.Fatal("poll produced the same version")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("poll did not catch the missed notification")
	}
}

// listeners counts sessions in this test's database whose last statement
// was the watcher's LISTEN.
func listeners(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
		WHERE datname = current_database() AND query = 'LISTEN data_version_changed'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The LISTEN session uses its own connection: it takes nothing from the pool,
// and cancelling Run closes it so pool.Close() never waits on it.
func TestWatcherListensOnDedicatedConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)
	changes := make(chan string, 10)
	w := dataversion.NewWatcher(pool, time.Minute, quiet(), func(_ context.Context, v string) error { changes <- v; return nil })
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	deadline := time.Now().Add(10 * time.Second)
	for listeners(t, pool) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("watcher never started listening")
		}
		time.Sleep(20 * time.Millisecond)
	}
	select { // the session's first Refresh publishes the initial version, then is done with the pool
	case <-changes:
	case <-time.After(5 * time.Second):
		t.Fatal("no initial version")
	}
	if n := pool.Stat().AcquiredConns(); n != 0 {
		t.Fatalf("pool has %d acquired connections while the watcher listens; want 0", n)
	}

	if _, err := pool.Exec(ctx, `SELECT pg_notify('data_version_changed', 'dataset')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE catalog_meta SET version = version + 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT pg_notify('data_version_changed', 'catalog')`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
	case <-time.After(5 * time.Second):
		t.Fatal("pg_notify did not trigger a reload")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	for listeners(t, pool) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the LISTEN connection stayed open after Run returned")
		}
		time.Sleep(20 * time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { pool.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("pool.Close() blocked after the watcher stopped")
	}
}

func TestRefreshCallsOnChangeBeforePublishing(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	var w *dataversion.Watcher
	calls := 0
	var seenDuringCallback string
	w = dataversion.NewWatcher(pool, time.Minute, quiet(), func(_ context.Context, v string) error {
		calls++
		seenDuringCallback = w.Current()
		return nil
	})
	if err := w.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || seenDuringCallback != "" {
		t.Fatalf("first Refresh: calls=%d saw %q; onChange must run before the first publish", calls, seenDuringCallback)
	}
	old := w.Current()
	if old == "" {
		t.Fatal("first version not published")
	}
	_, _ = pool.Exec(ctx, `UPDATE catalog_meta SET version = version + 1`)
	_ = w.Refresh(ctx)
	if seenDuringCallback != old {
		t.Fatalf("callback saw %q; the old version must still be published during reload", seenDuringCallback)
	}
	if w.Current() == old {
		t.Fatal("new version not published")
	}
	if _ = w.Refresh(ctx); calls != 2 {
		t.Fatalf("onChange ran %d times; an unchanged version must not re-run it", calls)
	}
}

// TestRefreshRetriesAfterOnChangeFailure verifies the controller ruling: if
// onChange fails, the new version must NOT be published (so a later NOTIFY,
// poll or startup retry re-runs the reload), and Refresh must surface the
// error. This holds for the first publish too. Once onChange succeeds, the
// version is published.
func TestRefreshRetriesAfterOnChangeFailure(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)

	fail := true
	w := dataversion.NewWatcher(pool, time.Minute, quiet(), func(_ context.Context, v string) error {
		if fail {
			return errors.New("boom")
		}
		return nil
	})
	if err := w.Refresh(ctx); err == nil {
		t.Fatal("expected the first Refresh to return the onChange error")
	}
	if w.Current() != "" {
		t.Fatalf("Current() = %q; nothing may be published before onChange succeeds", w.Current())
	}
	fail = false
	if err := w.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	old := w.Current()
	if old == "" {
		t.Fatal("first version not published after onChange succeeded")
	}

	if _, err := pool.Exec(ctx, `UPDATE catalog_meta SET version = version + 1`); err != nil {
		t.Fatal(err)
	}

	fail = true
	if err := w.Refresh(ctx); err == nil {
		t.Fatal("expected Refresh to return the onChange error")
	}
	if w.Current() != old {
		t.Fatalf("Current() = %q; want unchanged %q after a failed onChange", w.Current(), old)
	}

	fail = false
	if err := w.Refresh(ctx); err != nil {
		t.Fatalf("Refresh with succeeding onChange: %v", err)
	}
	if w.Current() == old {
		t.Fatal("new version not published after onChange succeeded")
	}
}
