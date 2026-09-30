# Observability and Alerting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Alert the owner within about 10 minutes when any of these happens: the site stops serving, the app loses readiness or throws 5xx/`ERROR`s, the drift check drops or fails, the backup login is used or locks, or a backup or monthly restore test fails or doesn't run. No visitor data may leave the VPS.

**Architecture:** A new package, `internal/monitor`, holds a heartbeat `Pinger` (outbound POSTs to Healthchecks.io), a response `Window` (5xx counts), an `ErrorCounter` `slog.Handler` wrapper, and a `Watchdog` that samples readiness each minute and reports to the `app` and `admin-auth` checks every 5 minutes. `calibrun.Drift` pings its own check once per run. `deploy/backup/heartbeat.sh` gives the backup and restore-test scripts `/start`, success and `/fail` pings from their `EXIT` traps. The external uptime monitor, check schedules and channels are vendor configuration, documented in `docs/operations.md`. Probe lines leave the access log, and container logs keep 20 compressed files.

**Tech Stack:** Go 1.26 (`log/slog`, `net/http`, `httptest`), POSIX `sh` (dash) with `curl`, Docker Compose `json-file` logging, Healthchecks.io (hosted).

**Spec:** `docs/superpowers/specs/2026-09-27-observability-and-alerting-design.md`. Issue [#7](https://github.com/Muddl/thymeandplace/issues/7).

**Spec drift at HEAD:** Env files are now per service (`deploy/env/*.env.example`; the least-privilege work in `620cf2b`), not one `deploy/env.example`. So the app's three `HEARTBEAT_*` URLs go in `app.env`, `HEARTBEAT_BACKUP_URL` in `backup.env`, and `HEARTBEAT_RESTORE_TEST_URL` in `restore.env` (restore-test runs in the `restore` service). The S4 prune (`auth.Service.Prune`) already exists, so its growth warning is hooked here as `login_log_growth`. The drift ping body names only a fixed failure category, never the raw error text, because a pgx connection error can carry a host address (§4.7 forbids IPs in ping bodies).

## Global Constraints

- Monitoring is off unless configured: an empty `HEARTBEAT_*` variable means that ping is a no-op. Dev, CI and e2e set none.
- A bad ping URL logs `ERROR "heartbeat URL invalid; check disabled" check=<name>`, disables that pinger, and never stops the app.
- Ping URLs must be absolute `https`, or `http` to a loopback host (tests only). They are secrets, so they are never logged: log `errors.Unwrap`-style causes only.
- A ping is a `POST` with a `text/plain` body cut to 10 kB. Each attempt has a 5 s timeout; there are 3 attempts with 1 s then 2 s backoff; pings never run on a request path.
- A failed ping logs at `WARN`, never `ERROR`, so a ping outage can't feed the `ERROR` counter.
- Ping bodies never carry paths, `cell_id`, coordinates, IP addresses, log attributes or secrets. They may carry counts, reason labels, allow-listed `ERROR` messages, calibration scores, data-version strings, backup object keys and table row counts.
- `ERROR` messages are kept verbatim only if they match `^[A-Za-z :;,'_-]+$`. Otherwise they are kept as `(message withheld)`. At most 3 distinct messages are kept per report.
- Values (spec §4.9): sample every 1 min; report every 5 min. Not-ready fires after 3 consecutive failed samples. The 5xx alert needs ≥ 5 and ≥ 5% of non-probe responses in a report; the `ERROR` alert needs ≥ 1 record. Logs: `json-file`, 10 MB × 20 files, compressed.
- Client-caused problems are not `ERROR`s: an anonymous caller must never be able to page the owner.

## Review Focus

1. **A ping URL that leaks into logs through `*url.Error`.** `http.Client.Do` errors embed the full URL, including the secret UUID. Task 1's `TestPingerNeverLogsURL` covers a refused connection and a timeout.
2. **An `ERROR` message built from request data**, such as `lookup failed for 51.5,-0.1`. It must reach the ping body only as `(message withheld)`. Task 3 covers it, and Task 7's privacy test checks every report body against IP/coordinate patterns.
3. **Shutdown during a report.** Cancelling the context must stop the watchdog without sending a ping, and a ping in flight must return promptly. Task 7 covers both.
4. **The backup script's `set -e` path.** A failing `pg_dump` must still reach the `EXIT` trap and send `/fail` naming `dump`, and the trap must keep the exit code. Task 9's local recipe runs a real failure (`-e DATABASE_URL=`) and a missing bucket.
5. **The app starts before its first snapshot.** The watchdog must report `not ready: catalog not loaded` without panicking on a nil snapshot. Task 4's `CheckReady` test and Task 7's not-ready case cover it.

---

### Task 1: `monitor.Pinger`

**Files:**
- Create: `internal/monitor/pinger.go`
- Test: `internal/monitor/pinger_test.go`

**Interfaces:**
- Produces:
  ```go
  type Notifier interface {
      Success(ctx context.Context, body string)
      Fail(ctx context.Context, body string)
  }
  func NewPinger(check, rawURL string, log *slog.Logger) *Pinger // "" → disabled no-op
  func (p *Pinger) Enabled() bool
  func (p *Pinger) Success(ctx context.Context, body string) // POST <url>
  func (p *Pinger) Fail(ctx context.Context, body string)    // POST <url>/fail
  ```
  `*Pinger` implements `Notifier`.

- [ ] **Step 1: Write the failing tests**

Create `internal/monitor/pinger_test.go`:

```go
package monitor

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testUUID = "5b2c1a9e-0000-4000-8000-00000000abcd"

type recorded struct{ method, path, body, ctype string }

type receiver struct {
	mu   sync.Mutex
	got  []recorded
	code []int // status per request, then 200
}

func (rc *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	rc.mu.Lock()
	rc.got = append(rc.got, recorded{r.Method, r.URL.Path, string(b), r.Header.Get("Content-Type")})
	status := http.StatusOK
	if len(rc.code) > 0 {
		status, rc.code = rc.code[0], rc.code[1:]
	}
	rc.mu.Unlock()
	w.WriteHeader(status)
}

func (rc *receiver) requests() []recorded {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]recorded(nil), rc.got...)
}

// fast shrinks timeouts and backoff so failure tests stay quick.
func fast(p *Pinger) *Pinger {
	p.client.Timeout = 200 * time.Millisecond
	p.backoff = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	return p
}

func TestPingerSuccessAndFail(t *testing.T) {
	rc := &receiver{}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	p := NewPinger("app", srv.URL+"/"+testUUID, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Success(context.Background(), "ok requests=3 5xx=0")
	p.Fail(context.Background(), strings.Repeat("x", 20_000))
	got := rc.requests()
	if len(got) != 2 {
		t.Fatalf("requests: %+v", got)
	}
	if got[0].method != "POST" || got[0].path != "/"+testUUID || got[0].body != "ok requests=3 5xx=0" || !strings.HasPrefix(got[0].ctype, "text/plain") {
		t.Fatalf("success ping: %+v", got[0])
	}
	if got[1].path != "/"+testUUID+"/fail" || len(got[1].body) != 10<<10 {
		t.Fatalf("fail ping: path %q, body %d bytes", got[1].path, len(got[1].body))
	}
}

func TestPingerDisabled(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	if p := NewPinger("app", "", log); p.Enabled() {
		t.Fatal("empty URL must disable the pinger")
	}
	if logs.Len() != 0 {
		t.Fatalf("an empty URL is not an error: %s", logs.String())
	}
	for _, bad := range []string{"http://hc-ping.com/" + testUUID, "::not a url", "ftp://127.0.0.1/x", "/relative/" + testUUID} {
		logs.Reset()
		p := NewPinger("drift", bad, log)
		if p.Enabled() {
			t.Fatalf("%q must be refused", bad)
		}
		if n := strings.Count(logs.String(), "heartbeat URL invalid; check disabled"); n != 1 || !strings.Contains(logs.String(), "level=ERROR") || strings.Contains(logs.String(), testUUID) {
			t.Fatalf("%q: want one ERROR without the URL, got:\n%s", bad, logs.String())
		}
		p.Success(context.Background(), "x") // must not panic or send
	}
	if !NewPinger("app", "https://hc-ping.com/"+testUUID, log).Enabled() || !NewPinger("app", "http://localhost:1/"+testUUID, log).Enabled() {
		t.Fatal("https and loopback http must be accepted")
	}
}

func TestPingerRetries(t *testing.T) {
	rc := &receiver{code: []int{500}}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	p := fast(NewPinger("app", srv.URL+"/"+testUUID, slog.New(slog.NewTextHandler(io.Discard, nil))))
	p.Success(context.Background(), "ok")
	if n := len(rc.requests()); n != 2 {
		t.Fatalf("a 500 then a 200 must take 2 requests, got %d", n)
	}
}

func TestPingerNeverLogsURL(t *testing.T) {
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer hang.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	for _, base := range []string{hang.URL, closedURL} {
		var logs bytes.Buffer
		p := fast(NewPinger("backup", base+"/"+testUUID, slog.New(slog.NewTextHandler(&logs, nil))))
		start := time.Now()
		p.Fail(context.Background(), "x")
		if took := time.Since(start); took > 2*time.Second {
			t.Fatalf("ping took %v; attempts must time out", took)
		}
		out := logs.String()
		if !strings.Contains(out, "heartbeat ping failed") || !strings.Contains(out, "level=WARN") || strings.Contains(out, testUUID) || strings.Contains(out, "level=ERROR") {
			t.Fatalf("want one WARN without the URL:\n%s", out)
		}
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/monitor/ -count=1`
Expected: FAIL to build (no non-test files).

- [ ] **Step 3: Implement**

Create `internal/monitor/pinger.go`:

```go
// Package monitor sends heartbeat pings to Healthchecks.io and watches the
// app's own health (readiness, 5xx, ERROR logs, security events). Nothing
// it sends carries visitor data (core spec §15; docs/operations.md,
// "Monitoring and alerts").
package monitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Notifier is one heartbeat check.
type Notifier interface {
	Success(ctx context.Context, body string)
	Fail(ctx context.Context, body string)
}

const maxPingBody = 10 << 10

// Pinger POSTs to one Healthchecks.io check. Its URL is a secret: it is
// never logged.
type Pinger struct {
	check   string
	url     string // "" → disabled
	client  *http.Client
	backoff []time.Duration // waits before attempts 2 and 3
	log     *slog.Logger
}

// NewPinger returns a pinger for check. An empty rawURL disables it quietly.
// An unusable one (not https, or http to a non-loopback host) disables it
// and logs an ERROR, so a monitoring typo never stops the site; the missed
// pings then raise the alert.
func NewPinger(check, rawURL string, log *slog.Logger) *Pinger {
	p := &Pinger{check: check, client: &http.Client{Timeout: 5 * time.Second},
		backoff: []time.Duration{time.Second, 2 * time.Second}, log: log}
	if rawURL == "" {
		return p
	}
	u, err := url.Parse(rawURL)
	if err != nil || !pingURLAllowed(u) {
		log.Error("heartbeat URL invalid; check disabled", "check", check)
		return p
	}
	p.url = strings.TrimRight(rawURL, "/")
	return p
}

func pingURLAllowed(u *url.URL) bool {
	if u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		h := u.Hostname()
		ip := net.ParseIP(h)
		return h == "localhost" || (ip != nil && ip.IsLoopback())
	}
	return false
}

func (p *Pinger) Enabled() bool                             { return p.url != "" }
func (p *Pinger) Success(ctx context.Context, body string) { p.send(ctx, p.url, body) }
func (p *Pinger) Fail(ctx context.Context, body string)    { p.send(ctx, p.url+"/fail", body) }

func (p *Pinger) send(ctx context.Context, target, body string) {
	if p.url == "" {
		return
	}
	if len(body) > maxPingBody {
		body = body[:maxPingBody]
	}
	var last error
	for attempt := 0; attempt <= len(p.backoff); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(p.backoff[attempt-1]):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
		if err != nil {
			last = errors.New("building request failed")
			break
		}
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		resp, err := p.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			last = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
		resp.Body.Close()
		if resp.StatusCode < 300 {
			return
		}
		last = fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// *url.Error prints the request URL, which holds the check's secret
	// UUID: log only its cause.
	var ue *url.Error
	if errors.As(last, &ue) {
		last = ue.Err
	}
	p.log.Warn("heartbeat ping failed", "check", p.check, "err", last)
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/monitor/ -count=1 -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/monitor/
git commit -m "monitor: heartbeat pinger that never logs its URL (#7)"
```

### Task 2: `monitor.Window` and `CountResponses`

**Files:**
- Create: `internal/monitor/window.go`
- Test: `internal/monitor/window_test.go`

**Interfaces:**
- Produces:
  ```go
  type Window struct{ /* unexported */ }
  func (w *Window) Record(path string, status int) // ignores /healthz and /readyz
  func (w *Window) Take() (requests, errors5xx int)  // returns and resets
  func CountResponses(w *Window) func(http.Handler) http.Handler
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/monitor/window_test.go`:

```go
package monitor

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"thymeandplace/internal/web"
)

func TestWindowCountsAndResets(t *testing.T) {
	var w Window
	for _, rec := range []struct {
		path   string
		status int
	}{{"/season", 200}, {"/season", 503}, {"/healthz", 500}, {"/readyz", 503}, {"/admin", 500}, {"/static/app.js", 304}} {
		w.Record(rec.path, rec.status)
	}
	if n, e := w.Take(); n != 4 || e != 2 {
		t.Fatalf("got %d requests, %d 5xx; want 4, 2 (probes ignored)", n, e)
	}
	if n, e := w.Take(); n != 0 || e != 0 {
		t.Fatal("Take must reset")
	}
}

func TestCountResponsesSeesRecoveredPanicAs500(t *testing.T) {
	var w Window
	panicky := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := CountResponses(&w)(web.Common(slog.New(slog.NewTextHandler(io.Discard, nil)))(panicky))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/season", nil))
	quiet := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	CountResponses(&w)(quiet).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if n, e := w.Take(); n != 2 || e != 1 {
		t.Fatalf("got %d requests, %d 5xx; want 2, 1", n, e)
	}
}
```

The breach thresholds in spec §6 (4 of 10 no, 5 of 50 yes, 5 of 200 no) are the watchdog's rule and are tested in Task 7.

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/monitor/ -run 'TestWindow|TestCountResponses' -count=1`
Expected: FAIL to compile (`undefined: Window`).

- [ ] **Step 3: Implement**

Create `internal/monitor/window.go`:

```go
package monitor

import (
	"net/http"
	"sync"
)

// Window counts responses and 5xx between reports. Probe paths are ignored:
// the watchdog samples readiness directly. It stores counts only.
type Window struct {
	mu            sync.Mutex
	requests, e5x int
}

func (w *Window) Record(path string, status int) {
	if path == "/healthz" || path == "/readyz" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.requests++
	if status >= 500 {
		w.e5x++
	}
}

// Take returns the counts since the last Take and resets them.
func (w *Window) Take() (requests, errors5xx int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	requests, errors5xx = w.requests, w.e5x
	w.requests, w.e5x = 0, 0
	return
}

// CountResponses records every response's status in w. Wrap it outside
// web.Common, whose recovery turns a panic into a 500 that this then sees.
func CountResponses(w *Window) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rec := &statusWriter{ResponseWriter: rw}
			next.ServeHTTP(rec, r)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			w.Record(r.URL.Path, rec.status)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/monitor/ -count=1 -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/monitor/
git commit -m "monitor: count responses and 5xx outside web.Common (#7)"
```

### Task 3: `monitor.ErrorCounter`

**Files:**
- Create: `internal/monitor/errors.go`
- Test: `internal/monitor/errors_test.go`

**Interfaces:**
- Produces:
  ```go
  func NewErrorCounter(next slog.Handler) *ErrorCounter // implements slog.Handler
  func (h *ErrorCounter) Take() (n int, msgs []string)   // returns and resets; ≤ 3 distinct, filtered messages
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/monitor/errors_test.go`:

```go
package monitor

import (
	"bytes"
	"log/slog"
	"reflect"
	"testing"
)

func TestErrorCounter(t *testing.T) {
	var out bytes.Buffer
	ec := NewErrorCounter(slog.NewTextHandler(&out, nil))
	log := slog.New(ec)
	log.Warn("season lookup failed", "cell_id", 1)
	log.Error("admin prune failed", "err", "secret detail 10.0.0.1")
	log.With("request_id", "r1").Error("admin prune failed")
	log.WithGroup("g").Error("lookup failed for 51.5,-0.1")
	log.Error("catalog reload failed; keeping previous snapshot")
	log.Error("apply failed")
	n, msgs := ec.Take()
	want := []string{"admin prune failed", "(message withheld)", "catalog reload failed; keeping previous snapshot"}
	if n != 5 || !reflect.DeepEqual(msgs, want) {
		t.Fatalf("n=%d msgs=%q; want 5, %q", n, msgs, want)
	}
	if n, msgs := ec.Take(); n != 0 || msgs != nil {
		t.Fatal("Take must reset")
	}
	if !bytes.Contains(out.Bytes(), []byte("secret detail")) {
		t.Fatal("records must still reach the wrapped handler unchanged")
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/monitor/ -run TestErrorCounter -count=1`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

Create `internal/monitor/errors.go`:

```go
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
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/monitor/ -count=1 -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/monitor/
git commit -m "monitor: count ERROR records and keep only constant messages (#7)"
```

### Task 4: `web.CheckReady` and quieter probe logging

**Files:**
- Modify: `internal/web/server.go:344-359` (`readyz`)
- Modify: `internal/web/middleware.go` (`AccessLog`)
- Test: `internal/web/middleware_test.go` (`TestAccessLogSkipsHealthyProbes`), `internal/web/server_test.go` (`TestCheckReady`)

**Interfaces:**
- Produces:
  ```go
  var ErrCatalogNotLoaded = errors.New("catalog not loaded")
  var ErrDatabaseUnavailable = errors.New("database unavailable")
  func CheckReady(ctx context.Context, snapshot func() *catalog.Snapshot, ping func(context.Context) error) error
  ```
  The DB ping runs under a 500 ms timeout. The error is one of the two sentinels.

- [ ] **Step 1: Write the failing tests**

Append to `internal/web/middleware_test.go` (add `"bytes"`, `"io"` and `"log/slog"` to the imports if missing):

```go
// Healthy probes don't reach the log; failing ones do.
func TestAccessLogSkipsHealthyProbes(t *testing.T) {
	var out bytes.Buffer
	status := http.StatusOK
	h := AccessLog(slog.New(slog.NewTextHandler(&out, nil)))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	for _, p := range []string{"/healthz", "/readyz"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
	}
	if out.Len() != 0 {
		t.Fatalf("healthy probes logged:\n%s", out.String())
	}
	status = http.StatusServiceUnavailable
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/readyz", nil))
	status = http.StatusOK
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/season", nil))
	if s := out.String(); !strings.Contains(s, "path=/readyz") || !strings.Contains(s, "status=503") || !strings.Contains(s, "path=/season") {
		t.Fatalf("failing probe or page request not logged:\n%s", s)
	}
}
```

Append to `internal/web/server_test.go`:

```go
func TestCheckReady(t *testing.T) {
	ctx := context.Background()
	snap, _ := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	ok := func(context.Context) error { return nil }
	down := func(context.Context) error { return errors.New("x") }
	hang := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	none := func() *catalog.Snapshot { return nil }
	have := func() *catalog.Snapshot { return snap }
	if err := CheckReady(ctx, none, ok); !errors.Is(err, ErrCatalogNotLoaded) {
		t.Fatalf("no snapshot: %v", err)
	}
	if err := CheckReady(ctx, have, down); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("db down: %v", err)
	}
	start := time.Now()
	if err := CheckReady(ctx, have, hang); !errors.Is(err, ErrDatabaseUnavailable) || time.Since(start) > time.Second {
		t.Fatalf("hung ping: %v after %v", err, time.Since(start))
	}
	if err := CheckReady(ctx, have, ok); err != nil {
		t.Fatalf("ready: %v", err)
	}
}
```

Add any missing imports (`context`, `errors`, `time`, `catalog`, `catalogtest`) to `server_test.go`.

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/web/ -run 'TestAccessLogSkipsHealthyProbes|TestCheckReady' -count=1`
Expected: FAIL (`undefined: CheckReady`; probe lines logged).

- [ ] **Step 3: Implement**

In `internal/web/server.go`, replace `readyz` with:

```go
var (
	ErrCatalogNotLoaded    = errors.New("catalog not loaded")
	ErrDatabaseUnavailable = errors.New("database unavailable")
)

// CheckReady is /readyz's check, shared with the monitoring watchdog so the
// two can't drift apart: a catalog snapshot is loaded and the database
// answers a ping within 500 ms.
func CheckReady(ctx context.Context, snapshot func() *catalog.Snapshot, ping func(context.Context) error) error {
	if snapshot() == nil {
		return ErrCatalogNotLoaded
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := ping(ctx); err != nil {
		return ErrDatabaseUnavailable
	}
	return nil
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := CheckReady(r.Context(), s.d.Snapshot, s.d.Ping); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(err.Error()))
		return
	}
	_, _ = w.Write([]byte("ready"))
}
```

In `internal/web/middleware.go`, in `AccessLog`, right after the `if rec.status == 0 { ... }` block, add:

```go
			// Caddy and Docker probe every 10–15 s; healthy probes would
			// otherwise be most of the log. Failing probes are still logged.
			if (r.URL.Path == "/healthz" || r.URL.Path == "/readyz") && rec.status < 300 {
				return
			}
```

- [ ] **Step 4: Run the web tests**

Run: `go test ./internal/web/ -count=1`
Expected: PASS, including the unchanged `TestHealthAndReady`.

- [ ] **Step 5: Commit**

```bash
git add internal/web/
git commit -m "web: share CheckReady with monitoring and skip healthy probe lines (#7)"
```

### Task 5: Security events from auth; enrollment body errors are not `ERROR`s

**Files:**
- Modify: `internal/auth/service.go` (`Config.OnEvent`, `Service.onEvent`, `LogAttempt`)
- Modify: `internal/auth/backup.go:91-111` (the lockout `WARN` and event)
- Modify: `internal/auth/prune.go:52-54` (the growth event)
- Modify: `internal/admin/login.go:285-300` (`enrollFinish`)
- Test: `internal/auth/backup_test.go` (`TestSecurityEvents`), `internal/auth/prune_test.go` (extend the growth test), `internal/admin/login_test.go` (`TestEnrollFinishOversizeBodyIsNotError`)

**Interfaces:**
- Produces: `auth.Config.OnEvent func(kind string)`, with kinds `"backup_login"`, `"backup_lockout"` and `"login_log_growth"`. A nil hook is a no-op.

- [ ] **Step 1: Write the failing tests**

Append to `internal/auth/backup_test.go`:

```go
// Monitoring hook: backup login use and a new lockout fire events; plain
// failures, attempts while locked and passkey outcomes don't.
func TestSecurityEvents(t *testing.T) {
	ctx := context.Background()
	s, c, secret := setup(t)
	var events []string
	s.onEvent = func(k string) { events = append(events, k) }

	s.LogAttempt(ctx, v4("192.0.2.1"), "passkey", "success")
	s.LogAttempt(ctx, v4("192.0.2.1"), "passkey", "failure")
	s.LogAttempt(ctx, v4("192.0.2.1"), "backup", "failure")
	if len(events) != 0 {
		t.Fatalf("no event expected yet: %v", events)
	}
	s.LogAttempt(ctx, v4("192.0.2.1"), "backup", "success")
	if len(events) != 1 || events[0] != "backup_login" {
		t.Fatalf("backup login: %v", events)
	}
	events = nil
	for i := 0; i < LockoutFailures-1; i++ {
		_ = s.BackupLogin(ctx, "nope", "000000")
	}
	if len(events) != 0 {
		t.Fatalf("failures before the lockout: %v", events)
	}
	_ = s.BackupLogin(ctx, "nope", "000000")
	if len(events) != 1 || events[0] != "backup_lockout" {
		t.Fatalf("lockout: %v", events)
	}
	_ = s.BackupLogin(ctx, pw, code(t, secret, c.now())) // refused while locked
	if len(events) != 1 {
		t.Fatalf("an attempt while locked must not fire: %v", events)
	}
}
```

In `internal/auth/prune_test.go`, in `TestPruneWarnsWhenLoginLogGrowsFast`, set `var fired []string; s.onEvent = func(k string) { fired = append(fired, k) }` before the call that should warn. After it, assert `len(fired) == 1 && fired[0] == "login_log_growth"`. Where no warning is expected, assert `len(fired) == 0`. Match the existing test's variable name for the service.

Append to `internal/admin/login_test.go`:

```go
// An anonymous caller must not be able to cause an ERROR (which pages the
// owner): an oversized enrollment body is a client error.
func TestEnrollFinishOversizeBodyIsNotError(t *testing.T) {
	e := newEnv(t, false)
	c := e.client(t)
	rec := c.postJSON("/admin/enroll/finish?token=x", `{"x":"`+strings.Repeat("a", 70<<10)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(e.logs.String(), "level=ERROR") {
		t.Fatalf("client error logged at ERROR:\n%s", e.logs.String())
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/auth/ ./internal/admin/ -run 'TestSecurityEvents|TestPruneWarnsWhenLoginLogGrowsFast|TestEnrollFinishOversizeBodyIsNotError' -count=1`
Expected: FAIL (`s.onEvent undefined`; the oversize body logs `level=ERROR`).

- [ ] **Step 3: Implement**

`internal/auth/service.go`:
- In `Config`, add:
  ```go
	// OnEvent, when set, is told about security events the owner must hear
	// about: "backup_login", "backup_lockout", "login_log_growth". It must
	// not block. Monitoring hooks it (internal/monitor).
	OnEvent func(kind string)
  ```
- In `Service`, add `onEvent func(string)`. In `NewService`, set `onEvent: cfg.OnEvent`.
- Add:
  ```go
func (s *Service) event(kind string) {
	if s.onEvent != nil {
		s.onEvent(kind)
	}
}
  ```
- In `LogAttempt`, inside `if method == "backup" && outcome == "success" {`, after the `Warn`, add `s.event("backup_login")`.

`internal/auth/backup.go`: after the failure-path `tx.Commit` succeeds (just before `return ErrBadCredentials`), add:

```go
	if until.Valid {
		s.log.Warn("backup login locked", "until", until.Time)
		s.event("backup_lockout")
	}
```

`internal/auth/prune.go`: inside `if r.LoginLogLastDay > s.growthWarn {`, after the `Warn`, add `s.event("login_log_growth")`.

`internal/admin/login.go`, in `enrollFinish`: replace

```go
	err := limitCeremonyBody(w, r)
	if err == nil {
		err = a.d.Auth.FinishEnrollment(ctx, state, r, r.URL.Query().Get("name"), r.URL.Query().Get("token"))
	}
```

with

```go
	// A body the ceremony can't read (over 64 KiB, or cut off) is the
	// caller's fault: WARN, never ERROR, since ERROR pages the owner.
	if err := limitCeremonyBody(w, r); err != nil {
		a.d.Auth.LogAttempt(ctx, c, "enroll", "failure")
		a.d.Log.Warn("enrollment body rejected", "err", err, "request_id", web.RequestIDFrom(ctx))
		jsonError(w, http.StatusBadRequest, "The passkey couldn't be registered. Try again.")
		return
	}
	err := a.d.Auth.FinishEnrollment(ctx, state, r, r.URL.Query().Get("name"), r.URL.Query().Get("token"))
```

- [ ] **Step 4: Run the auth and admin tests**

Run: `go test ./internal/auth/ ./internal/admin/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/ internal/admin/
git commit -m "auth: security-event hook for monitoring; enroll body errors log at WARN (#7)"
```

### Task 6: Drift reports to its own check

**Files:**
- Modify: `internal/calibrun/drift.go`
- Test: `internal/calibrun/drift_test.go`

**Interfaces:**
- Produces:
  - `Drift.Notify func(ctx context.Context, failed bool, summary string)`. It is called exactly once per `RunOnce`, unless `ctx` was cancelled. A nil hook is a no-op.
  - `var ErrNoSnapshot = errors.New("drift check: no catalog snapshot loaded")`.
  - Summaries:
    - On a drop: `calibration score dropped %.3f → %.3f (data version %s → %s)`.
    - On no snapshot: `drift check failed: no catalog snapshot loaded`.
    - On any other error: `drift check failed: database or scoring error (see the app log)`.
    - Otherwise: `calibration score %.3f over %d pairs (data version %s)`.

- [ ] **Step 1: Write the failing tests**

In `internal/calibrun/drift_test.go`, `TestDriftRecordsAndWarns`: add a recorder to the `Drift` literal:

```go
	type note struct {
		failed  bool
		summary string
	}
	var notes []note
	// in the Drift literal:
	Notify: func(_ context.Context, failed bool, summary string) { notes = append(notes, note{failed, summary}) },
```

After the first (good) `RunOnce`, assert `len(notes) == 1 && !notes[0].failed && strings.Contains(notes[0].summary, "calibration score")`. After the run that warns about a drop, assert that the last note has `failed == true` and a summary containing `calibration score dropped`, both scores formatted `%.3f`, and both data versions.

In `TestDriftNeedsSnapshot`, add the same recorder and assert exactly one note with `failed == true` and summary `drift check failed: no catalog snapshot loaded`. Also add:

```go
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	notes = nil
	_, _ = d.RunOnce(cctx)
	if len(notes) != 0 {
		t.Fatalf("a cancelled run must not notify: %v", notes)
	}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/calibrun/ -run 'TestDrift' -count=1`
Expected: FAIL to compile (`unknown field Notify`).

- [ ] **Step 3: Implement**

In `internal/calibrun/drift.go`:
- Add the `ErrNoSnapshot` var. Have `RunOnce` return it when `snap == nil`.
- Add the field, with the comment:
  ```go
	// Notify, when set, reports each run's outcome to the drift heartbeat
	// check. Summaries never carry raw error text (it can name a DB host).
	Notify func(ctx context.Context, failed bool, summary string)
  ```
- Change `RunOnce` to named results `(rep calibration.Report, err error)`. At the top, declare `var summary string; dropped := false`, then add:
  ```go
	defer func() {
		if d.Notify == nil || ctx.Err() != nil {
			return
		}
		switch {
		case errors.Is(err, ErrNoSnapshot):
			d.Notify(ctx, true, "drift check failed: no catalog snapshot loaded")
		case err != nil:
			d.Notify(ctx, true, "drift check failed: database or scoring error (see the app log)")
		default:
			d.Notify(ctx, dropped, summary)
		}
	}()
  ```
- In the drop branch, set `dropped = true` and `summary = fmt.Sprintf("calibration score dropped %.3f → %.3f (data version %s → %s)", prev.Score, rep.Score, prev.DataVersion, dv)`.
- In the else branch, set `summary = fmt.Sprintf("calibration score %.3f over %d pairs (data version %s)", rep.Score, rep.Compared, dv)`.
- Add `"fmt"` to the imports. Keep the existing log lines unchanged.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/calibrun/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/calibrun/
git commit -m "calibrun: report each drift run to a heartbeat hook (#7)"
```

### Task 7: `monitor.Watchdog`

**Files:**
- Create: `internal/monitor/watchdog.go`
- Test: `internal/monitor/watchdog_test.go`

**Interfaces:**
- Consumes: `Notifier`, `*Window` (Task 2), `*ErrorCounter` (Task 3).
- Produces:
  ```go
  type Watchdog struct {
      Ready     func(context.Context) error // web.CheckReady bound to the app
      Window    *Window
      Errors    *ErrorCounter
      App       Notifier
      AdminAuth Notifier
      Sample    time.Duration // 0 → 1 min
      Every     int           // report every Nth sample; 0 → 5
  }
  func (w *Watchdog) Event(kind string) // pass as auth.Config.OnEvent
  func (w *Watchdog) Run(ctx context.Context)
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/monitor/watchdog_test.go`:

```go
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
	h.tick(2)
	if len(h.app.take()) != 0 {
		t.Fatal("2 failed samples must not report")
	}
	h.tick(1) // the 3rd consecutive failure reports at once
	if got := h.app.take(); len(got) != 1 || !got[0].fail || got[0].body != "not ready: catalog not loaded" {
		t.Fatalf("%+v", got)
	}
	h.ready = nil
	h.tick(2) // 5th sample: recovered
	if got := h.app.take(); len(got) != 1 || got[0].fail {
		t.Fatalf("recovery: %+v", got)
	}
	h.ready = errors.New("x")
	h.tick(2)
	h.ready = nil
	h.tick(3)
	if got := h.app.take(); len(got) != 1 || got[0].fail {
		t.Fatalf("2 failures then ready must not fail: %+v", got)
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
	if a, s := h.app.take(), h.sec.take(); a[0].fail || s[0].fail {
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
	leak := regexp.MustCompile(`\d+\.\d+\.\d+\.\d+|[0-9a-f]*:[0-9a-f]*:[0-9a-f:]+|lat|lng|cell`)
	for _, p := range append(h.app.take(), h.sec.take()...) {
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
	if len(h.app.take())+len(h.sec.take()) != 0 {
		t.Fatal("shutdown must not ping")
	}
}
```

The tests call the unexported `step`, one sample at a time, so the ticks stay deterministic.

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/monitor/ -run TestWatchdog -count=1`
Expected: FAIL to compile (`undefined: Watchdog`).

- [ ] **Step 3: Implement**

Create `internal/monitor/watchdog.go`:

```go
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
```

`w.lastNotReady` is `err.Error()` from `web.CheckReady`, which is always one of the two constant sentinels, so no private data can enter. `TestWatchdogNotReady` passes `"catalog not loaded"` directly.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/monitor/ -count=1 -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/monitor/
git commit -m "monitor: watchdog reports readiness, 5xx, errors and security events (#7)"
```

### Task 8: Wire monitoring into the server

**Files:**
- Modify: `cmd/server/app.go` (`config`, `loadConfig`, `app`, `newApp`, `start`)
- Test: `cmd/server/app_test.go` (`TestLoadConfigHeartbeats`, `TestAppSendsHeartbeat`)

**Interfaces:**
- Consumes: `monitor.NewPinger`, `monitor.NewErrorCounter`, `monitor.Window`, `monitor.CountResponses`, `monitor.Watchdog` (Tasks 1-3, 7); `web.CheckReady` (Task 4); `auth.Config.OnEvent` (Task 5); `calibrun.Drift.Notify` (Task 6).
- Produces:
  - `config.HeartbeatApp`, `config.HeartbeatAdminAuth` and `config.HeartbeatDrift` (strings), read from `HEARTBEAT_APP_URL`, `HEARTBEAT_ADMIN_AUTH_URL` and `HEARTBEAT_DRIFT_URL`.
  - An unexported `config.watchdogSample time.Duration` for tests.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/server/app_test.go` (reuse its imports; add `"io"`, `"net/http"` and `"net/http/httptest"` if missing):

```go
func TestLoadConfigHeartbeats(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "postgres://x/y", "HEARTBEAT_APP_URL": "https://hc-ping.com/a",
		"HEARTBEAT_ADMIN_AUTH_URL": "https://hc-ping.com/b", "HEARTBEAT_DRIFT_URL": "https://hc-ping.com/c"}
	c, err := loadConfig(func(k string) string { return env[k] }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || c.HeartbeatApp != env["HEARTBEAT_APP_URL"] || c.HeartbeatAdminAuth != env["HEARTBEAT_ADMIN_AUTH_URL"] || c.HeartbeatDrift != env["HEARTBEAT_DRIFT_URL"] {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestAppSendsHeartbeat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)
	got := make(chan string, 16)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r.URL.Path + " " + string(b)
	}))
	defer recv.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": dbtest.URLAs(pool, dbtest.App), "HEARTBEAT_APP_URL": recv.URL + "/app-check"}[k]
	}, log)
	if err != nil {
		t.Fatal(err)
	}
	cfg.watchdogSample = 20 * time.Millisecond
	a, err := newApp(ctx, cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	a.start(ctx)
	select {
	case p := <-got:
		if !strings.HasPrefix(p, "/app-check") {
			t.Fatalf("unexpected ping %q", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no app heartbeat")
	}
}
```

An empty database has no catalog snapshot, so the ping may be `/app-check/fail` with `not ready: catalog not loaded`. The test accepts either, because it proves the wiring. With the variable unset, no pinger is enabled; `TestLoadConfig` already covers the empty defaults.

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./cmd/server/ -run 'TestLoadConfigHeartbeats|TestAppSendsHeartbeat' -count=1`
Expected: FAIL to compile (`c.HeartbeatApp undefined`).

- [ ] **Step 3: Implement**

In `cmd/server/app.go`:
- `config` gains:
  ```go
	// Healthchecks.io ping URLs (secrets; empty → that ping is off).
	HeartbeatApp, HeartbeatAdminAuth, HeartbeatDrift string

	watchdogSample time.Duration // 0 → 1 min (tests shorten it)
  ```
- In `loadConfig`, next to `c.AdminBackup = ...`, add:
  ```go
	c.HeartbeatApp = getenv("HEARTBEAT_APP_URL")
	c.HeartbeatAdminAuth = getenv("HEARTBEAT_ADMIN_AUTH_URL")
	c.HeartbeatDrift = getenv("HEARTBEAT_DRIFT_URL")
  ```
- `app` gains `watchdog *monitor.Watchdog`.
- In `newApp`, as the first statements (before `db.RequireMigrated`):
  ```go
	// Count ERROR records from everything built here, for the watchdog.
	errs := monitor.NewErrorCounter(log.Handler())
	log = slog.New(errs)
  ```
- After `a.drift = &calibrun.Drift{...}`, add:
  ```go
	drift := monitor.NewPinger("drift", cfg.HeartbeatDrift, log)
	a.drift.Notify = func(ctx context.Context, failed bool, summary string) {
		if failed {
			drift.Fail(ctx, summary)
		} else {
			drift.Success(ctx, summary)
		}
	}
	window := &monitor.Window{}
	a.watchdog = &monitor.Watchdog{
		Ready:     func(ctx context.Context) error { return web.CheckReady(ctx, a.holder.Current, pool.Ping) },
		Window:    window,
		Errors:    errs,
		App:       monitor.NewPinger("app", cfg.HeartbeatApp, log),
		AdminAuth: monitor.NewPinger("admin-auth", cfg.HeartbeatAdminAuth, log),
		Sample:    cfg.watchdogSample,
	}
  ```
- In the `auth.Config{...}` literal, add `OnEvent: a.watchdog.Event,`.
- Replace `a.handler = web.Common(log)(a.mux)` with `a.handler = monitor.CountResponses(window)(web.Common(log)(a.mux))`.
- In `start`, after `go a.drift.Loop(ctx, 24*time.Hour)` (`go a.drift.Loop(ctx, jobSchedule)` once #23 has landed), add `go a.watchdog.Run(ctx)`.
- Import `"thymeandplace/internal/monitor"`.

- [ ] **Step 4: Run the server tests**

Run: `go test ./cmd/server/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/server/
git commit -m "server: wire heartbeats, the watchdog and response counting (#7)"
```

### Task 9: Backup and restore-test heartbeats

**Files:**
- Create: `deploy/backup/heartbeat.sh`
- Modify: `deploy/backup/backup.sh` (whole file), `deploy/backup/restore-test.sh`, `deploy/backup/Dockerfile` (`COPY` line)
- Modify: `.github/workflows/ci.yml` (`deploy` job: `sh -n`)

**Interfaces:**
- Produces:
  - `hc <suffix> [body]` (a shell function). It is a no-op when `HEARTBEAT_URL` is empty and never fails its caller.
  - `backup.sh` has a `step` variable (`env`, `alias`, `dump`, `upload`, `weekly`, `prune`) and one `on_exit` trap. The backup-encryption plan builds on both.

- [ ] **Step 1: Create `deploy/backup/heartbeat.sh`**

```sh
# shellcheck shell=sh
# Sourced by backup.sh and restore-test.sh. hc <suffix> [body] POSTs body to
# $HEARTBEAT_URL<suffix> (a Healthchecks.io check; suffix "" for success,
# /start or /fail). Empty HEARTBEAT_URL → no-op. It never fails its caller,
# and never prints the URL (a secret).
hc() {
  [ -n "${HEARTBEAT_URL:-}" ] || return 0
  curl -fsS -m 10 --retry 3 -o /dev/null --data-raw "${2:-}" "${HEARTBEAT_URL}$1" >/dev/null 2>&1 \
    || echo "heartbeat ping failed (${1:-success})" >&2
  return 0
}
```

- [ ] **Step 2: Replace `deploy/backup/backup.sh`**

```sh
#!/bin/sh
# Nightly pg_dump to the bucket. Keeps 7 daily and 4 weekly copies.
# With HEARTBEAT_BACKUP_URL set, pings /start, then success, or /fail naming
# the step that failed (docs/operations.md, "Monitoring and alerts").
set -eu
. /usr/local/bin/heartbeat.sh
HEARTBEAT_URL=${HEARTBEAT_BACKUP_URL:-}

step=env
file=
prune_err=
day=$(date -u +%Y-%m-%d)

# One EXIT trap: delete the dump and send exactly one final ping, keeping
# the exit status.
on_exit() {
  rc=$?
  [ -n "$file" ] && rm -f "$file"
  if [ "$rc" -ne 0 ]; then
    hc /fail "backup failed during $step (exit $rc)"
  elif [ -n "$prune_err" ]; then
    hc /fail "dump uploaded; pruning failed: $prune_err"
  else
    hc "" "backup ok: backups/daily/$day.dump"
  fi
  exit "$rc"
}
trap on_exit EXIT

for v in DATABASE_URL BUCKET_ENDPOINT BUCKET_NAME BUCKET_ACCESS_KEY BUCKET_SECRET_KEY; do
  eval "val=\${$v:-}"
  [ -n "$val" ] || { echo "backup: $v is not set" >&2; exit 2; }
done
hc /start

step=alias
scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null

step=dump
file="/tmp/thymeandplace-$day.dump"
pg_dump --format=custom --no-owner --dbname="$DATABASE_URL" --file="$file"
[ -s "$file" ] || { echo "backup: dump is empty" >&2; exit 1; }

step=upload
mc cp --quiet "$file" "store/$BUCKET_NAME/backups/daily/$day.dump"
if [ "$(date -u +%u)" = "7" ]; then
  step=weekly
  mc cp --quiet "$file" "store/$BUCKET_NAME/backups/weekly/$(date -u +%G-W%V).dump"
fi

# Pruning failures must not fail the backup itself (today's dump is already
# uploaded) but must not be silent either: they fail the heartbeat check.
step=prune
if ! err=$(mc rm --recursive --force --older-than 7d "store/$BUCKET_NAME/backups/daily/" 2>&1 >/dev/null); then
  echo "backup: warning: pruning backups/daily failed: $err" >&2
  prune_err="backups/daily: $(printf %s "$err" | head -c 300)"
fi
if ! err=$(mc rm --recursive --force --older-than 28d "store/$BUCKET_NAME/backups/weekly/" 2>&1 >/dev/null); then
  echo "backup: warning: pruning backups/weekly failed: $err" >&2
  prune_err="${prune_err:+$prune_err; }backups/weekly: $(printf %s "$err" | head -c 300)"
fi

echo "backup ok: backups/daily/$day.dump"
```

- [ ] **Step 3: Add the ping to `restore-test.sh`**

- After `set -eu`, add:
  ```sh
. /usr/local/bin/heartbeat.sh
HEARTBEAT_URL=${HEARTBEAT_RESTORE_TEST_URL:-}
latest=
report=
  ```
- Replace the `: "${DATABASE_URL:?}" ...` line with the same `for v in ...; exit 2` check used in `backup.sh`, naming `restore-test` in the message.
- Replace the existing `trap '...' EXIT` line with:
  ```sh
on_exit() {
  rc=$?
  psql "$DATABASE_URL" -X -qc "DROP DATABASE IF EXISTS $scratch_db WITH (FORCE)" >/dev/null 2>&1 || true
  if [ "$rc" -eq 0 ]; then
    hc "" "restore-test ok ($latest)"
  else
    hc /fail "restore-test failed (exit $rc, ${latest:-no dump})
$report"
  fi
  exit "$rc"
}
trap on_exit EXIT
  ```
  Move `scratch_db=thymeandplace_restore_test` above the trap, and put `hc /start` right after the trap line.
- In the count loop, after the `printf` line, append the same line to the report: `report="$report$(printf '%-20s live=%-10s restored=%s' "$t" "$live" "$copy")
"`.

- [ ] **Step 4: Ship the helper in the image, and parse-check the scripts in CI**

In `deploy/backup/Dockerfile`, change the `COPY` line to `COPY backup.sh restore.sh restore-test.sh heartbeat.sh /usr/local/bin/`.

In `.github/workflows/ci.yml`, `deploy` job, after "Role bootstrap script parses", add:

```yaml
      - name: Backup scripts parse
        run: for f in deploy/backup/*.sh; do sh -n "$f"; done
```

- [ ] **Step 5: Run the local recipe**

Run the "Verifying locally" recipe in `docs/operations.md` (lines 188-215) with the e2e stack and `zenko/cloudserver`. Point the heartbeat at a local catcher: `docker run -d --name hc --network <e2e network> mendhak/http-https-echo:31`, then pass `-e HEARTBEAT_BACKUP_URL=http://hc:8080/backup`. The catcher isn't loopback from inside the container. That is fine here, because the shell `hc` doesn't apply the Go pinger's URL rule.
Expected:
- A normal run logs `POST /backup/start`, then `POST /backup` with `backup ok: ...`.
- `-e BUCKET_NAME=missing` gives `/backup/fail` with `backup failed during upload (exit 1)`.
- `-e DATABASE_URL=` gives `/backup/fail` with `backup failed during env (exit 2)`.
- The exit codes match those numbers.

Also run `sh -n deploy/backup/*.sh`. Paste the outputs into the pull request.

- [ ] **Step 6: Commit**

```bash
git add deploy/backup/ .github/workflows/ci.yml
git commit -m "backup: heartbeat pings from the EXIT trap, naming the failed step (#7)"
```

### Task 10: Log retention, env templates and runbook

**Files:**
- Modify: `deploy/compose.yaml:9-14` (`x-logging`)
- Modify: `deploy/env/app.env.example`, `deploy/env/backup.env.example`, `deploy/env/restore.env.example`
- Modify: `docs/operations.md` (new "Monitoring and alerts" section; "First deploy", "Nightly backups", "Health and logs")
- Modify: `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (§10.4, §11.4, §13.2, §14, §15, §16 per spec §5 step 10)

**Interfaces:** configuration and documentation only.

- [ ] **Step 1: Log options**

In `deploy/compose.yaml`, set `x-logging` options to:

```yaml
    options:
      max-size: "10m"
      max-file: "20"
      compress: "true"
```

and update its comment: `# Rotate container logs: 20 files of up to 10 MB, rotated files compressed.`

- [ ] **Step 2: Env templates**

Append to `deploy/env/app.env.example`:

```
# Monitoring (docs/operations.md, "Monitoring and alerts"). Per-check
# Healthchecks.io ping URLs; each is a SECRET (anyone holding it can flip
# the check). Unset → that ping is off.
#HEARTBEAT_APP_URL=https://hc-ping.com/<uuid>
#HEARTBEAT_ADMIN_AUTH_URL=https://hc-ping.com/<uuid>
#HEARTBEAT_DRIFT_URL=https://hc-ping.com/<uuid>
```

Append `#HEARTBEAT_BACKUP_URL=https://hc-ping.com/<uuid>` (with the same one-line secret note) to `backup.env.example`, and `#HEARTBEAT_RESTORE_TEST_URL=https://hc-ping.com/<uuid>` to `restore.env.example`.

- [ ] **Step 3: Validate compose**

Run the two `docker compose ... config -q` commands from the CI `deploy` job locally (copy the templates to a temp dir as the job does).
Expected: no output, exit 0.

- [ ] **Step 4: `docs/operations.md`**

- Add a new section "## Monitoring and alerts" with:
  - the six checks and their schedules (spec §4.1 table), and that they are POST-only, with the VPS time zone for the cron checks;
  - where the URLs live (`app.env`, `backup.env`, `restore.env`), and that they are secrets;
  - the privacy rules (spec §4.7 table);
  - pausing checks for planned work longer than 10 minutes;
  - that the drift check recovering the next day means "no further drop", not "fixed";
  - the runbook table from spec §7 (Alert → first step), with `journalctl -u thymeandplace-backup` or the log file for `backup`;
  - the external monitor requirements (spec §4.6).
- "First deploy": add a "Set up monitoring" step before step 9 that points at the new section.
- "Nightly backups": mention the pings, add the monthly restore-test cron line `15 4 1 * * cd /opt/thymeandplace/deploy && docker compose --profile tools run --rm --entrypoint restore-test.sh restore >> /var/log/thymeandplace-backup.log 2>&1`, and add the logrotate snippet:
  ```
  # /etc/logrotate.d/thymeandplace-backup
  /var/log/thymeandplace-backup.log {
      weekly
      rotate 8
      compress
      missingok
      notifempty
  }
  ```
- "Health and logs":
  - Change the retention text to 20 compressed files of 10 MB, and say that healthy `/healthz` and `/readyz` lines aren't logged.
  - Replace the "add a supervisor" sentence with: "Nothing restarts an unhealthy container. The `app` and `site` alerts tell you to (see "Monitoring and alerts")."
  - Say that `calibration_run` and `admin_login_log` are the durable records.

- [ ] **Step 5: Core spec amendments**

Apply spec §5 step 10 exactly:
- §10.4: backup-login use and lockout alert the owner.
- §11.4: 2xx probe lines are skipped.
- §13.2: drift also reports to its heartbeat.
- §14: replace the Health bullet with "Docker and Caddy probe `/healthz`, and Caddy routes on it. Nothing restarts an unhealthy container; alerts do. `/readyz` is for monitoring." Add a Logs retention sentence, a new Monitoring bullet, and "the monthly restore test runs by cron with its own check" in Backups.
- §15: the payload rule.
- §16: the spec §4.9 rows.

- [ ] **Step 6: Commit**

```bash
git add deploy/compose.yaml deploy/env/ docs/
git commit -m "ops: compressed log retention, heartbeat env templates and the alert runbook (#7)"
```

### Task 11: Whole-repo check

- [ ] **Step 1:** Run `go vet ./... && go test ./... -count=1 -race`. Expected: PASS.
- [ ] **Step 2:** Run `./e2e/run.sh`. Expected: PASS. The e2e env files set no `HEARTBEAT_*`, so no pings are sent.
- [ ] **Step 3:** Before merging, list for the owner the rollout steps from spec §7 that are vendor-side and can't be done in the repo: the Healthchecks.io project, channels, the external monitor, VPS provider alerts, and the 12-minute stop drill.
