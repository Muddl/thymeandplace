# Shared Readiness Ping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** However many clients poll `/readyz`, the app pings the database at most once per second, with at most one ping in flight. Status codes and bodies don't change.

**Architecture:** A mutex-guarded `pingCache` on `web.Server` holds the time and result of the last ping. `sharedPing` reuses a result under 1 s old by the server clock. Otherwise it pings while holding the lock, with a 500 ms timeout detached from the request's cancellation. `readyz` checks the snapshot first (never cached), then calls `sharedPing`.

**Tech Stack:** Go 1.26, `sync`, `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-09-28-readyz-shared-ping-design.md`. Issue [#13](https://github.com/Muddl/thymeandplace/issues/13).

**With the observability plan:** If `2026-09-28-observability-and-alerting.md` Task 4 (`web.CheckReady`) has landed, `readyz` becomes `CheckReady(r.Context(), s.d.Snapshot, s.sharedPing)` instead of the body in Task 1 Step 3. The tests don't change.

## Global Constraints

- Reuse window: `readyzPingTTL = time.Second`. Success and failure are both cached.
- The ping runs on `context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)`.
- Expiry uses `s.d.Now`. A cached result is valid only when `!at.IsZero() && age >= 0 && age < readyzPingTTL`.
- Bodies stay exactly `ready`, `database unavailable` and `catalog not loaded`. No body may contain `not ready`.
- The snapshot check is never cached, and when it fails no ping happens.
- No new dependency.

## Review Focus

1. **Many clients at once.** 50 concurrent requests at one instant must produce exactly one ping, and every one of them must get the right answer. Task 1's `TestReadyzSharesPing` blocks the first ping until all 50 have started.
2. **A client disconnecting mid-ping.** It must not cache `context.Canceled` as "database unavailable" for everyone else. Task 1 covers this.
3. **The server clock jumping backwards.** The cached result must count as expired, so a backward jump can't pin a stale answer. Task 1 covers this.
4. **A database that recovers within the window.** It keeps answering 503 until the 1 s window passes, then 200. The runbook must say so, so an operator doesn't chase a phantom outage. Task 1's failure test pins the timing, and Task 2 documents it.
5. **The smoke test and the e2e `waitReady` helper.** Both poll `/readyz` in a loop, and must still see `ready` promptly once the database is up. Task 2 runs both.

---

### Task 1: `sharedPing` and `readyz`

**Files:**
- Create: `internal/web/readiness.go`
- Modify: `internal/web/server.go` (`Server` gains `ready pingCache`; `readyz` uses `sharedPing`)
- Modify: `internal/web/fakes_test.go` (the harness counts pings and can hold one)
- Test: `internal/web/server_test.go` (`TestHealthAndReady` advances the clock; new `TestReadyz*` tests)

**Interfaces:**
- Produces:
  - `const readyzPingTTL = time.Second`
  - `type pingCache struct{ mu sync.Mutex; at time.Time; err error }`
  - `func (s *Server) sharedPing(ctx context.Context) error`
- Harness additions: `harness.pings atomic.Int32`, `harness.pingHold chan struct{}` (when set, `Ping` waits on it or on its context), and `harness.pingEntered chan struct{}` (receives once per ping that starts waiting).

- [ ] **Step 1: Let the harness count and hold pings**

In `internal/web/fakes_test.go`, add three fields to `harness`:

```go
	pings       atomic.Int32
	pingHold    chan struct{} // when set, Ping waits until it is closed (or its context ends)
	pingEntered chan struct{} // when set, receives once per Ping that starts waiting on pingHold
```

Replace the `Ping:` line in `newHarness`'s `Deps` literal with:

```go
		Ping: func(ctx context.Context) error {
			hs.pings.Add(1)
			if err := ctx.Err(); err != nil { // like pgx: a done context fails at once
				return err
			}
			if hs.pingHold != nil {
				if hs.pingEntered != nil {
					select { // never block: without sharing, 50 pings would arrive here
					case hs.pingEntered <- struct{}{}:
					default:
					}
				}
				select {
				case <-hs.pingHold:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return hs.pingErr
		},
```

- [ ] **Step 2: Write the failing tests**

In `internal/web/server_test.go`, change `TestHealthAndReady` so each case after the first moves the clock past the reuse window. Add `hs.now = hs.now.Add(time.Second)` right before `hs.pingErr = errors.New("down")` and right before `hs.pingErr = nil`.

Then append the following tests (add `"sync"` and `"time"` to the imports if missing):

```go
// #13: one ping answers every /readyz for a second, however many callers.
func TestReadyzSharesPing(t *testing.T) {
	hs := newHarness(t)
	hs.pingHold, hs.pingEntered = make(chan struct{}), make(chan struct{}, 1)
	const n = 50
	codes := make([]int, n)
	bodies := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], bodies[i], _ = hs.get("/readyz", false)
		}()
	}
	select {
	case <-hs.pingEntered: // the first request is pinging; the rest queue on the lock
	case <-time.After(5 * time.Second):
		t.Fatal("no ping started")
	}
	time.Sleep(50 * time.Millisecond) // let the other 49 reach the lock
	close(hs.pingHold)
	wg.Wait()
	for i := range n {
		if codes[i] != 200 || bodies[i] != "ready" {
			t.Fatalf("request %d: %d %q", i, codes[i], bodies[i])
		}
	}
	if p := hs.pings.Load(); p != 1 {
		t.Fatalf("pings = %d, want 1", p)
	}
	hs.pingHold, hs.pingEntered = nil, nil

	hs.now = hs.now.Add(999 * time.Millisecond)
	hs.get("/readyz", false)
	if p := hs.pings.Load(); p != 1 {
		t.Fatalf("within the window: pings = %d, want 1", p)
	}
	hs.now = hs.now.Add(time.Millisecond)
	hs.get("/readyz", false)
	if p := hs.pings.Load(); p != 2 {
		t.Fatalf("after the window: pings = %d, want 2", p)
	}
}

// A failure is shared for the window too, then the next ping recovers.
func TestReadyzCachesFailure(t *testing.T) {
	hs := newHarness(t)
	hs.pingErr = errors.New("down")
	if code, body, _ := hs.get("/readyz", false); code != 503 || body != "database unavailable" {
		t.Fatalf("%d %q", code, body)
	}
	hs.pingErr = nil
	if code, _, _ := hs.get("/readyz", false); code != 503 {
		t.Fatal("the failure must be reused within the window")
	}
	hs.now = hs.now.Add(time.Second)
	if code, body, _ := hs.get("/readyz", false); code != 200 || body != "ready" {
		t.Fatalf("after the window: %d %q", code, body)
	}
}

// A client that has gone away can't poison the shared result.
func TestReadyzIgnoresCancelledCaller(t *testing.T) {
	hs := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/readyz", nil).WithContext(ctx)
	hs.h.ServeHTTP(httptest.NewRecorder(), r)
	if code, body, _ := hs.get("/readyz", false); code != 200 || body != "ready" {
		t.Fatalf("after a cancelled caller: %d %q", code, body)
	}
}

// A clock that moves backwards expires the cached result.
func TestReadyzClockBackwards(t *testing.T) {
	hs := newHarness(t)
	hs.get("/readyz", false)
	hs.now = hs.now.Add(-time.Hour)
	hs.get("/readyz", false)
	if p := hs.pings.Load(); p != 2 {
		t.Fatalf("pings = %d, want 2 (a backwards clock must not pin a result)", p)
	}
}

// With no snapshot the answer is immediate and there is no ping.
func TestReadyzNoSnapshotNoPing(t *testing.T) {
	hs := newHarness(t)
	hs.snap = nil
	if code, body, _ := hs.get("/readyz", false); code != 503 || body != "catalog not loaded" || hs.pings.Load() != 0 {
		t.Fatalf("%d %q pings=%d", code, body, hs.pings.Load())
	}
}
```

`hs.get` returns the body HTML-unescaped. For `/readyz` that is the plain text itself. `hs.now` is read through `Deps.Now` on each request, so the tests change it only while no request is in flight.

- [ ] **Step 3: Run them and confirm they fail**

Run: `go test ./internal/web/ -run 'TestReadyz|TestHealthAndReady' -count=1`
Expected: FAIL. `TestReadyzSharesPing` counts 50 pings, `TestReadyzCachesFailure` gets 200 right after the error clears, and `TestReadyzClockBackwards` passes only by accident, since every request pings today.

These tests pass today, and stay as regression guards:
- `TestReadyzNoSnapshotNoPing`.
- `TestHealthAndReady`.
- `TestReadyzIgnoresCancelledCaller`, which catches a cache that forgets `context.WithoutCancel`. With the fake returning `ctx.Err()`, such a cache would store `context.Canceled` and answer 503.

- [ ] **Step 4: Implement**

Create `internal/web/readiness.go`:

```go
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
```

In `internal/web/server.go`, add `ready pingCache // #13: one shared DB ping for /readyz` to `Server`. Replace `readyz` with:

```go
// readyz answers ready when a catalog snapshot is loaded and the database
// answered a ping in the last second (sharedPing). The snapshot check is
// free and never cached.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.d.Snapshot() == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("catalog not loaded"))
		return
	}
	if err := s.sharedPing(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("database unavailable"))
		return
	}
	_, _ = w.Write([]byte("ready"))
}
```

If `CheckReady` exists (the observability plan), use `if err := CheckReady(r.Context(), s.d.Snapshot, s.sharedPing); err != nil { ...write err.Error()... }` instead. The bodies are the same.

- [ ] **Step 5: Run the web tests with the race detector**

Run: `go test -race ./internal/web/ -count=1`
Expected: PASS, with no race reports.

- [ ] **Step 6: Commit**

```bash
git add internal/web/
git commit -m "web: share one database ping across /readyz requests for 1 s (#13)"
```

### Task 2: Docs, smoke test and e2e

**Files:**
- Modify: `docs/operations.md` ("Health and logs")
- Modify: `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` §11.1, §11.6, §16

**Interfaces:** documentation only.

- [ ] **Step 1: Runbook**

In `docs/operations.md` "Health and logs", after the sentence about `curl -s https://thymeand.place/readyz`, add: `The database part of /readyz is shared: one ping answers every request for 1 s, so after the database recovers, /readyz can say "database unavailable" for up to a second longer.`

- [ ] **Step 2: Core spec**

Apply the spec's §4.3 table exactly:
- §11.1's `/readyz` row: "…catalog loaded and a database ping succeeded within the last second (one shared ping, reused for 1 s)".
- §11.6's "No catalog snapshot loaded" row: "The server doesn't listen until a snapshot loads, so Caddy serves the maintenance page".
- §16: the new row "Readiness ping reuse: 1 s".

- [ ] **Step 3: Run the smoke test and e2e**

Run: `./deploy/smoke.sh && ./e2e/run.sh`
Expected: both pass. `smoke.sh` and `waitReady` poll `/readyz` until `ready`.

- [ ] **Step 4: Commit**

```bash
git add docs/
git commit -m "docs: /readyz reuses one database ping for a second (#13)"
```
