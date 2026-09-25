package catalog_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHolderStartsEmpty(t *testing.T) {
	h := catalog.NewHolder(func(context.Context) (*catalog.Snapshot, error) { return nil, errors.New("down") }, quiet())
	if h.Current() != nil {
		t.Fatal("Current before load must be nil")
	}
	if err := h.Reload(context.Background()); err == nil || h.Current() != nil {
		t.Fatal("failed first load must leave Current nil and return an error")
	}
}

// Review Focus 4: a failed reload keeps the previous snapshot.
func TestHolderKeepsPreviousOnFailure(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)

	h := catalog.NewHolder(func(ctx context.Context) (*catalog.Snapshot, error) { return catalog.Load(ctx, pool) }, quiet())
	if err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	first := h.Current()
	if first == nil || first.Len() != 11 {
		t.Fatalf("first = %v", first)
	}

	pool.Close() // database unreachable
	if err := h.Reload(ctx); err == nil {
		t.Fatal("reload against a closed pool must fail")
	}
	if h.Current() != first {
		t.Fatal("failed reload replaced the live snapshot")
	}
}

// Review finding 1 (Plan 2 whole-plan review): Reload must serialise
// concurrent calls and never let a slower, older load overwrite a newer
// snapshot. A fake loader blocks its first call on a channel (returning
// version 1) while its second call returns version 2 immediately.
func TestHolderSerialisesConcurrentReloads(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	var calls atomic.Int32

	h := catalog.NewHolder(func(context.Context) (*catalog.Snapshot, error) {
		if calls.Add(1) == 1 {
			<-release // first call blocks until released
			return catalog.NewSnapshot(1, nil)
		}
		return catalog.NewSnapshot(2, nil) // second call returns immediately
	}, quiet())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = h.Reload(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first Reload never entered the loader")
		}
		time.Sleep(time.Millisecond)
	}

	go func() { defer wg.Done(); _ = h.Reload(ctx) }()
	time.Sleep(20 * time.Millisecond) // let the second Reload queue up behind the mutex

	close(release)
	wg.Wait()

	if h.Current() == nil || h.Current().Version() != 2 {
		t.Fatalf("Current().Version() = %v, want 2", h.Current())
	}
}
