package monitor

import (
	"context"
	"log/slog"
	"regexp"
	"sync"
)

// safeMessage is what an ERROR message may look like to be sent in a ping:
// constant text only. Anything with digits, dots, slashes or colons-with-
// data (coordinates, IPs, IDs, paths) is withheld.
var safeMessage = regexp.MustCompile(`^[A-Za-z :;,'_-]+$`)

const maxMessages = 3

// ErrorCounter wraps a handler and counts records at ERROR or above. It
// keeps up to 3 distinct messages and never keeps attributes. Children from
// WithAttrs and WithGroup share the count.
type ErrorCounter struct {
	next  slog.Handler
	state *errorState
}

type errorState struct {
	mu   sync.Mutex
	n    int
	msgs []string
}

// NewErrorCounter returns an ErrorCounter that passes every record on to next.
func NewErrorCounter(next slog.Handler) *ErrorCounter {
	return &ErrorCounter{next: next, state: &errorState{}}
}

func (h *ErrorCounter) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelError || h.next.Enabled(ctx, l)
}

func (h *ErrorCounter) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		h.state.add(r.Message)
	}
	if !h.next.Enabled(ctx, r.Level) {
		return nil
	}
	return h.next.Handle(ctx, r)
}

func (h *ErrorCounter) WithAttrs(as []slog.Attr) slog.Handler {
	return &ErrorCounter{next: h.next.WithAttrs(as), state: h.state}
}

func (h *ErrorCounter) WithGroup(name string) slog.Handler {
	return &ErrorCounter{next: h.next.WithGroup(name), state: h.state}
}

// Take returns the count and kept messages since the last Take, and resets.
func (h *ErrorCounter) Take() (int, []string) {
	s := h.state
	s.mu.Lock()
	defer s.mu.Unlock()
	n, msgs := s.n, s.msgs
	s.n, s.msgs = 0, nil
	return n, msgs
}

func (s *errorState) add(msg string) {
	if !safeMessage.MatchString(msg) {
		msg = "(message withheld)"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	if len(s.msgs) >= maxMessages {
		return
	}
	for _, m := range s.msgs {
		if m == msg {
			return
		}
	}
	s.msgs = append(s.msgs, msg)
}
