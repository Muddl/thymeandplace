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

// pingCache shares one database ping across /readyz requests.
type pingCache struct {
	mu  sync.Mutex
	at  time.Time // when err was measured; zero → never
	err error
}

// sharedPing returns the last ping result if it is under readyzPingTTL old
// by the server clock. Otherwise it pings once, while holding the lock, so
// concurrent callers wait for that ping and reuse its result. The ping is
// detached from ctx's cancellation, so a caller that disconnects can't
// cache context.Canceled for everyone; it has its own 500 ms timeout.
func (s *Server) sharedPing(ctx context.Context) error {
	c := &s.ready
	c.mu.Lock()
	defer c.mu.Unlock()
	now := s.d.Now()
	if age := now.Sub(c.at); !c.at.IsZero() && age >= 0 && age < readyzPingTTL {
		return c.err
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	c.err = s.d.Ping(pctx)
	c.at = now
	return c.err
}
