package monitor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Watchdog samples readiness every Sample and reports to the app and
// admin-auth checks every Every samples, and at once on the sample where
// readiness reaches notReadyAfter consecutive failures. That is at most one
// report per check per sample. On shutdown it stops without pinging.
type Watchdog struct {
	Ready     func(context.Context) error
	Window    *Window
	Errors    *ErrorCounter
	App       Notifier
	AdminAuth Notifier
	Sample    time.Duration
	Every     int

	samples, fails int
	lastNotReady   string

	mu     sync.Mutex
	events map[string]int
}

const notReadyAfter = 3

// Event records a security event (auth.Config.OnEvent).
func (w *Watchdog) Event(kind string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.events == nil {
		w.events = map[string]int{}
	}
	w.events[kind]++
}

func (w *Watchdog) Run(ctx context.Context) {
	every := w.Sample
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.step(ctx)
		}
	}
}

// step takes one readiness sample and reports when due.
func (w *Watchdog) step(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if err := w.Ready(ctx); err != nil {
		w.fails++
		w.lastNotReady = err.Error()
	} else {
		w.fails = 0
	}
	w.samples++
	every := w.Every
	if every <= 0 {
		every = 5
	}
	if w.samples%every == 0 || w.fails == notReadyAfter {
		w.report(ctx)
	}
}

func (w *Watchdog) report(ctx context.Context) {
	var reasons []string
	if w.fails >= notReadyAfter {
		reasons = append(reasons, "not ready: "+w.lastNotReady)
	}
	n, e := w.Window.Take()
	if e >= 5 && e*20 >= n {
		reasons = append(reasons, fmt.Sprintf("5xx: %d of %d requests", e, n))
	}
	if cnt, msgs := w.Errors.Take(); cnt > 0 {
		reasons = append(reasons, fmt.Sprintf("errors: %d (%s)", cnt, strings.Join(msgs, "; ")))
	}
	if len(reasons) > 0 {
		w.App.Fail(ctx, strings.Join(reasons, "\n"))
	} else {
		w.App.Success(ctx, fmt.Sprintf("ok requests=%d 5xx=%d", n, e))
	}

	w.mu.Lock()
	ev := w.events
	w.events = nil
	w.mu.Unlock()
	if len(ev) > 0 {
		w.AdminAuth.Fail(ctx, fmt.Sprintf("backup login used: %d; backup lockout: %d; login log growth: %d",
			ev["backup_login"], ev["backup_lockout"], ev["login_log_growth"]))
	} else {
		w.AdminAuth.Success(ctx, "ok")
	}
}
