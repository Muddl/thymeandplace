package dataversion_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

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

func TestRefreshCallsOnChangeBeforePublishing(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	var w *dataversion.Watcher
	var seenDuringCallback string
	w = dataversion.NewWatcher(pool, time.Minute, quiet(), func(_ context.Context, v string) error {
		seenDuringCallback = w.Current()
		return nil
	})
	_ = w.Refresh(ctx)
	old := w.Current()
	_, _ = pool.Exec(ctx, `UPDATE catalog_meta SET version = version + 1`)
	_ = w.Refresh(ctx)
	if seenDuringCallback != old {
		t.Fatalf("callback saw %q; the old version must still be published during reload", seenDuringCallback)
	}
	if w.Current() == old {
		t.Fatal("new version not published")
	}
}

// TestRefreshRetriesAfterOnChangeFailure verifies the controller ruling: if
// onChange fails, the new version must NOT be published (so a later NOTIFY
// or poll retries the reload), and Refresh must surface the error. Once
// onChange succeeds, the version is published.
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
	if err := w.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	old := w.Current()

	if _, err := pool.Exec(ctx, `UPDATE catalog_meta SET version = version + 1`); err != nil {
		t.Fatal(err)
	}

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
