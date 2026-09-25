package catalog

import (
	"context"
	"log/slog"
	"sync/atomic"
)

// Holder owns the live snapshot and swaps it atomically on reload.
type Holder struct {
	cur  atomic.Pointer[Snapshot]
	load func(context.Context) (*Snapshot, error)
	log  *slog.Logger
}

func NewHolder(load func(context.Context) (*Snapshot, error), log *slog.Logger) *Holder {
	return &Holder{load: load, log: log}
}

// Current returns the live snapshot, or nil before the first successful load.
func (h *Holder) Current() *Snapshot { return h.cur.Load() }

// Reload loads a new snapshot. On failure the previous one stays live.
func (h *Holder) Reload(ctx context.Context) error {
	s, err := h.load(ctx)
	if err != nil {
		h.log.Error("catalog reload failed; keeping previous snapshot", "err", err)
		return err
	}
	h.cur.Store(s)
	h.log.Info("catalog snapshot loaded", "version", s.Version(), "items", s.Len())
	return nil
}
