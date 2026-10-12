package monitor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type ping struct {
	fail bool
	body string
}

type recNotifier struct {
	mu  sync.Mutex
	got []ping
}

func (r *recNotifier) Success(_ context.Context, b string) { r.add(ping{false, b}) }
func (r *recNotifier) Fail(_ context.Context, b string)    { r.add(ping{true, b}) }
func (r *recNotifier) add(p ping)                          { r.mu.Lock(); r.got = append(r.got, p); r.mu.Unlock() }
func (r *recNotifier) count() int                          { r.mu.Lock(); defer r.mu.Unlock(); return len(r.got) }
func (r *recNotifier) take() []ping {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.got
	r.got = nil
	return g
}

type wdHarness struct {
	wd       *Watchdog
	app, sec *recNotifier
	ready    error
	log      *slog.Logger
}

func newWD() *wdHarness {
	h := &wdHarness{app: &recNotifier{}, sec: &recNotifier{}}
	ec := NewErrorCounter(slog.NewTextHandler(io.Discard, nil))
	h.log = slog.New(ec)
	h.wd = &Watchdog{
		Ready: func(context.Context) error { return h.ready }, Window: &Window{}, Errors: ec,
		App: h.app, AdminAuth: h.sec, Every: 5,
	}
	return h
}

// tick runs n samples synchronously through the watchdog's step function.
func (h *wdHarness) tick(n int) {
	for i := 0; i < n; i++ {
		h.wd.step(context.Background())
	}
}

// tickAtMostOnce is tick, and fails if any one sample pings a check more
// than once (at most one ping per check per minute).
func (h *wdHarness) tickAtMostOnce(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		a, s := h.app.count(), h.sec.count()
		h.wd.step(context.Background())
		if da, ds := h.app.count()-a, h.sec.count()-s; da > 1 || ds > 1 {
			t.Fatalf("one sample sent %d app and %d admin-auth pings", da, ds)
		}
	}
}

func TestWatchdogHealthyReports(t *testing.T) {
	h := newWD()
	h.tick(4)
	if len(h.app.take()) != 0 {
		t.Fatal("no report before the 5th sample")
	}
	h.tick(1)
	if got := h.app.take(); len(got) != 1 || got[0].fail || !strings.HasPrefix(got[0].body, "ok requests=0 5xx=0") {
		t.Fatalf("app: %+v", got)
	}
	if got := h.sec.take(); len(got) != 1 || got[0].fail {
		t.Fatalf("admin-auth: %+v", got)
	}
}

func TestWatchdogNotReady(t *testing.T) {
	h := newWD()
	h.ready = errors.New("catalog not loaded")
	h.tickAtMostOnce(t, 2)
	if len(h.app.take()) != 0 {
		t.Fatal("2 failed samples must not report")
	}
	h.tickAtMostOnce(t, 1) // the 3rd consecutive failure reports at once
	if got := h.app.take(); len(got) != 1 || !got[0].fail || got[0].body != "not ready: catalog not loaded" {
		t.Fatalf("%+v", got)
	}
	h.ready = nil
	h.tickAtMostOnce(t, 2) // 5th sample: recovered
	if got := h.app.take(); len(got) != 1 || got[0].fail {
		t.Fatalf("recovery: %+v", got)
	}
	h.ready = errors.New("x")
	h.tickAtMostOnce(t, 2)
	h.ready = nil
	h.tickAtMostOnce(t, 3)
	if got := h.app.take(); len(got) != 1 || got[0].fail {
		t.Fatalf("2 failures then ready must not fail: %+v", got)
	}
	// Samples 11-15: the 3rd consecutive failure lands on a scheduled
	// report, and both reasons to report still send one ping per check.
	h.sec.take()
	h.tickAtMostOnce(t, 2)
	h.ready = errors.New("x")
	h.tickAtMostOnce(t, 3)
	if got := h.app.take(); len(got) != 1 || !got[0].fail || got[0].body != "not ready: x" {
		t.Fatalf("not ready on a scheduled report: %+v", got)
	}
	if got := h.sec.take(); len(got) != 1 {
		t.Fatalf("admin-auth: %+v", got)
	}
}

func TestWatchdog5xxThreshold(t *testing.T) {
	for _, tc := range []struct {
		n, errs int
		fail    bool
	}{{10, 4, false}, {50, 5, true}, {200, 5, false}} {
		h := newWD()
		for i := 0; i < tc.n; i++ {
			status := 200
			if i < tc.errs {
				status = 500
			}
			h.wd.Window.Record("/season", status)
		}
		h.tick(5)
		got := h.app.take()
		if len(got) != 1 || got[0].fail != tc.fail {
			t.Fatalf("%d of %d: %+v", tc.errs, tc.n, got)
		}
		if tc.fail && !strings.Contains(got[0].body, "5xx: 5 of 50 requests") {
			t.Fatalf("body: %q", got[0].body)
		}
	}
}

func TestWatchdogErrorsAndEvents(t *testing.T) {
	h := newWD()
	h.log.Error("admin prune failed")
	h.wd.Event("backup_login")
	h.tick(5)
	if got := h.app.take(); len(got) != 1 || !got[0].fail || got[0].body != "errors: 1 (admin prune failed)" {
		t.Fatalf("app: %+v", got)
	}
	if got := h.sec.take(); len(got) != 1 || !got[0].fail || !strings.Contains(got[0].body, "backup login used: 1") {
		t.Fatalf("admin-auth: %+v", got)
	}
	h.tick(5)
	if a, s := h.app.take(), h.sec.take(); len(a) != 1 || len(s) != 1 || a[0].fail || s[0].fail {
		t.Fatalf("the next clean report must succeed: %+v %+v", a, s)
	}
}

// No report body may carry an IP, coordinates or cell IDs.
func TestWatchdogBodiesArePrivate(t *testing.T) {
	h := newWD()
	h.ready = errors.New("database unavailable")
	h.log.Error("lookup failed for 51.5,-0.1 from 203.0.113.9 cell 12345")
	h.wd.Event("backup_lockout")
	h.wd.Window.Record("/season", 500)
	h.tick(5)
	app, sec := h.app.take(), h.sec.take()
	if len(app) == 0 || len(sec) == 0 {
		t.Fatalf("each check must report before its bodies are checked: app %+v, admin-auth %+v", app, sec)
	}
	leak := regexp.MustCompile(`\d+\.\d+\.\d+\.\d+|[0-9a-f]*:[0-9a-f]*:[0-9a-f:]+|lat|lng|cell`)
	for _, p := range append(app, sec...) {
		if leak.MatchString(p.body) {
			t.Fatalf("private data in %q", p.body)
		}
	}
}

func TestWatchdogStopsWithoutPinging(t *testing.T) {
	h := newWD()
	h.wd.Sample = time.Hour // no tick can arrive before the cancel
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.wd.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	// A sample taken after the cancel, even one due to report, sends nothing.
	h.ready = errors.New("catalog not loaded")
	h.wd.Event("backup_login")
	for i := 0; i < 5; i++ {
		h.wd.step(ctx)
	}
	if len(h.app.take())+len(h.sec.take()) != 0 {
		t.Fatal("shutdown must not ping")
	}
}
