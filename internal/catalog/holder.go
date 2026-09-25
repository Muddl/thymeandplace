package catalog

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// Holder owns the live snapshot and swaps it atomically on reload.
type Holder struct {
	cur      atomic.Pointer[Snapshot]
	load     func(context.Context) (*Snapshot, error)
	log      *slog.Logger
	reloadMu sync.Mutex // serialises Reload so an older load can't win a race against a newer one
}

func NewHolder(load func(context.Context) (*Snapshot, error), log *slog.Logger) *Holder {
	return &Holder{load: load, log: log}
}

// Current returns the live snapshot, or nil before the first successful load.
func (h *Holder) Current() *Snapshot { return h.cur.Load() }

// Reload loads a new snapshot. On failure the previous one stays live.
// Concurrent calls are serialised, and a load that resolves to a version
// older than what's already live is discarded rather than replacing it.
func (h *Holder) Reload(ctx context.Context) error {
	h.reloadMu.Lock()
	defer h.reloadMu.Unlock()

	s, err := h.load(ctx)
	if err != nil {
		h.log.Error("catalog reload failed; keeping previous snapshot", "err", err)
		return err
	}
	if cur := h.cur.Load(); cur != nil && s.Version() < cur.Version() {
		h.log.Info("catalog reload produced an older snapshot; keeping current",
			"current_version", cur.Version(), "loaded_version", s.Version())
		return nil
	}
	h.cur.Store(s)
	h.log.Info("catalog snapshot loaded", "version", s.Version(), "items", s.Len())
	return nil
}
