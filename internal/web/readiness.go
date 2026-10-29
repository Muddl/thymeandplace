package web

import (
	"context"
	"sync"
	"time"
)

// readyzPingTTL is how long one database ping answers /readyz for everyone.
// /readyz is public and unlimited, so without this every request would take
// a pooled connection (#13).
const readyzPingTTL = time.Second

// pingCache shares one probe result across /readyz requests.
type pingCache struct {
	mu  sync.Mutex
	at  time.Time // when err was measured; zero → never
	err error
}

func (c *pingCache) load(now time.Time, run func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if age := now.Sub(c.at); !c.at.IsZero() && age >= 0 && age < readyzPingTTL {
		return c.err
	}
	c.err = run()
	c.at = now
	return c.err
}

func detachedReady(ctx context.Context, fn func(context.Context) error) error {
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	return fn(pctx)
}

// sharedPing returns the last ping result if it is under readyzPingTTL old
// by the server clock. Otherwise it pings once, while holding the lock, so
// concurrent callers wait for that ping and reuse its result. The ping is
// detached from ctx's cancellation, so a caller that disconnects can't
// cache context.Canceled for everyone; it has its own 500 ms timeout.
func (s *Server) sharedPing(ctx context.Context) error {
	return s.ready.load(s.d.Now(), func() error {
		return detachedReady(ctx, s.d.Ping)
	})
}

// sharedSchema is sharedPing for Deps.Schema. A nil Schema is current.
func (s *Server) sharedSchema(ctx context.Context) error {
	if s.d.Schema == nil {
		return nil
	}
	return s.schema.load(s.d.Now(), func() error {
		return detachedReady(ctx, s.d.Schema)
	})
}
