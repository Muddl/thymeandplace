# Thyme & Place — Plan 5: Public Web Layer and Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve the public site: home page with city search and "Use my location", the seasonal results partial with its error states, the ingredient detail with a 12-month bar, the About the data page, health endpoints, and the `server` binary. The server keeps its catalog snapshot and data version fresh through `LISTEN` plus a 5-minute poll.

**Architecture:** `internal/web` holds handlers, middleware and view models; `internal/web/views` holds `templ` components. Handlers depend on small interfaces (`Climate`, `Ocean`, `Places`), so every §11.6 state is tested with fakes and `httptest`. `internal/cache` is a generic LRU keyed by `(cellID, month, dataVersion)`. `dataversion.Watcher` recomputes the data version on `NOTIFY` or poll, reloading the snapshot **before** publishing the new version so cache entries never pair a new version with an old snapshot. `cmd/server` wires the real services and mounts the public handler; Plans 6–7 mount `/admin`.

**Tech Stack:** Go ≥ 1.25, `github.com/a-h/templ` (runtime + `go tool templ`), HTMX 2.0.4 (vendored), `github.com/hashicorp/golang-lru/v2`, `golang.org/x/time/rate`, `log/slog`.

**Spec:** `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (§11, §12.5, §13.7, §13.8, §14 logs, §15)

**Depends on:** Plans 1–4.

## Global Constraints

- Routes: `GET /`, `GET /places?q=`, `GET /season?lat=&lng=&date=[&place=]`, `GET /ingredient/{slug}?cell=&date=`, `GET /about/data`, `GET /healthz`, `GET /readyz`, `GET /static/…` (§11.1).
- Requests without `HX-Request: true`, or with `HX-History-Restore-Request: true`, get full pages for `/season`, `/ingredient` and `/places`. Every such response sets `Vary: HX-Request`.
- `/places`: up to 8 matches; the input uses `hx-trigger="keyup changed delay:250ms"`.
- `/season` groups: **Peak / In season / From storage / Year-round**, with seafood as its own **Seafood** group.
- Cache: in-process LRU, **50,000** entries, key `(cell_id, month, dataVersion)`. Data version refreshes on `LISTEN data_version_changed`, with a **5-minute** poll fallback.
- Middleware order: request ID → structured `slog` access log → panic recovery → per-IP rate limit → per-request timeout **2 s**.
- Public rate limits (token bucket, per IP): `/places` **10 req/s, burst 20**; `/season` and `/ingredient` **2 req/s, burst 10**. Over the limit: **429** with `Retry-After`.
- HTMX 2.x vendored, configured to swap 4xx/5xx responses. Geolocation is a few lines of vanilla JS calling `htmx.ajax` with coordinates. The browser's local date (`YYYY-MM-DD`) is added to every `/season` and `/ingredient` request.
- Month: the `date` parameter's month. If `date` is missing or malformed, use the server's current UTC date (§12.5). **Spec conflict:** §11.6 lists "invalid date → 400" while §12.5 says to fall back. This plan follows §12.5 (never dead-end); invalid **lat/lng** still get 400.
- Access logs never include the query string. `/season` and `/ingredient` log `cell_id` only (§14, §15). No public cookies.
- `/readyz`: 200 only if the DB ping succeeds within **500 ms** and a snapshot is loaded.
- "Harsh climate" threshold: fewer than **3** land items at Peak or In season, with normals present. (The spec gives no number; this plan sets one.)
- "near <place>" labels are used only when the nearest place is within **50 km**; otherwise the label is coordinates.
- Exact user-facing copy (§11.6), which tests assert after HTML-unescaping:
  - `No places match '<q>'. Try a nearby larger town or city.`
  - `Location access is off, so search for your town instead.`
  - `We can't estimate seasons here yet. Estimates cover land and coastal waters.` + `Search another place`
  - `Not much grows locally this time of year. Here's what's still around.`
  - `Couldn't load seasonal picks right now. Try again in a moment.` + `Try again`
  - `Search is pausing for a few seconds. Keep typing and results will catch up.`
- Wild-mushroom disclaimer (fixed copy, every `wild_mushroom` item): `Look for wild mushrooms at markets and from sellers who can identify them. We don't give foraging advice: many edible mushrooms have deadly look-alikes.`
- Commit after every task. Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Bad coordinates** (`lat=abc`, `lat=NaN`, `lng=Inf`, `lat=91`, a missing `lng`) return the 400 panel, not a 500 or a panic in grid math. Pinned in Task 5.
2. **Month from the cook's local date across a year boundary** (`date=2026-12-31` while the server is already in January UTC) shows December. A malformed date falls back to the server month without failing. Pinned in Task 5.
3. **Database outage with a warm cache**: a cached `/season` still returns 200. Only cache misses show the retry panel. Pinned in Task 5.
4. **Back button / HTMX history restore** (`HX-History-Restore-Request: true`) returns a full page, not a fragment that replaces the whole document. Pinned in Task 4.
5. **A search query containing markup** (`<script>alert(1)</script>`) is escaped in the "No places match" message. Pinned in Task 4.

---

## File Structure

```
internal/cache/cache.go, cache_test.go
internal/dataversion/watcher.go, watcher_test.go
internal/web/middleware.go, middleware_test.go   # request ID, access log, recover, rate limit, timeout, client IP
internal/web/server.go                           # Deps, New, Common, render helpers, health
internal/web/places.go                           # /places
internal/web/season.go                           # /season, seasonCore, view-model building
internal/web/ingredient.go                       # /ingredient/{slug}
internal/web/fakes_test.go                       # fake Climate/Ocean/Places
internal/web/server_test.go, places_test.go, season_test.go, ingredient_test.go
internal/web/views/models.go                     # view models, copy constants, helpers
internal/web/views/layout.templ, places.templ, season.templ, ingredient.templ, about.templ
internal/web/views/*_templ.go                    # generated, committed
static/static.go                                 # package static: embedded FS
static/htmx.min.js, static/app.css, static/app.js
cmd/server/main.go, app.go, app_test.go
```

---

### Task 1: Versioned cache and data-version watcher

**Files:**
- Create: `internal/cache/cache.go`, `internal/cache/cache_test.go`, `internal/dataversion/watcher.go`, `internal/dataversion/watcher_test.go`

**Interfaces:**
- Consumes: `dataversion.Current`, `store.New` (Plan 2).
- Produces:
  - `const cache.DefaultSize = 50_000`; `type cache.Key struct { CellID int; Month time.Month; DataVersion string }`
  - `func cache.New[V any](size int) *Cache[V]`; `(*Cache[V]).Get(Key) (V, bool)`; `Add(Key, V)`; `Len() int`
  - `func dataversion.NewWatcher(pool *pgxpool.Pool, poll time.Duration, log *slog.Logger, onChange func(ctx context.Context, version string)) *Watcher`
  - `(*Watcher).Current() string`; `Refresh(ctx) error`: on a change, calls `onChange` first and then publishes (the first call only publishes); `Run(ctx)`: `LISTEN` loop with poll timeout and reconnect

- [ ] **Step 1: Add dependencies and write the failing tests**

```bash
go get github.com/hashicorp/golang-lru/v2@latest golang.org/x/time@latest
mkdir -p internal/cache
```

Create `internal/cache/cache_test.go`:

```go
package cache

import (
	"testing"
	"time"
)

func TestVersionChangeMakesOldKeysUnreachable(t *testing.T) {
	c := New[string](10)
	k := Key{CellID: 1, Month: time.May, DataVersion: "v1"}
	c.Add(k, "old")
	if v, ok := c.Get(k); !ok || v != "old" {
		t.Fatal("hit expected")
	}
	k2 := k
	k2.DataVersion = "v2"
	if _, ok := c.Get(k2); ok {
		t.Fatal("new version must miss")
	}
}

func TestEviction(t *testing.T) {
	c := New[int](2)
	for i := 0; i < 3; i++ {
		c.Add(Key{CellID: i, Month: time.May, DataVersion: "v"}, i)
	}
	if c.Len() != 2 {
		t.Fatalf("len = %d", c.Len())
	}
	if _, ok := c.Get(Key{CellID: 0, Month: time.May, DataVersion: "v"}); ok {
		t.Fatal("oldest key should be evicted")
	}
}
```

Create `internal/dataversion/watcher_test.go`:

```go
package dataversion_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"thymeandplace/internal/dataversion"
	"thymeandplace/internal/dbtest"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestWatcherNotifyAndPoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)

	changes := make(chan string, 10)
	w := dataversion.NewWatcher(pool, 300*time.Millisecond, quiet(), func(_ context.Context, v string) { changes <- v })
	if err := w.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	first := w.Current()
	if first != dataversion.Compute(0, nil) {
		t.Fatalf("initial = %q", first)
	}
	go w.Run(ctx)

	// A catalog write sends NOTIFY.
	if _, err := pool.Exec(ctx, `INSERT INTO ingredient (slug, name, category, kind, tip_choose, tip_store)
		VALUES ('banana','Banana','fruit','cultivated_year_round','c','s')`); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-changes:
		if v == first {
			t.Fatal("version unchanged")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no change after NOTIFY")
	}

	// A bump with no NOTIFY (a missed notification) is caught by the poll.
	before := w.Current()
	if _, err := pool.Exec(ctx, `UPDATE catalog_meta SET version = version + 100`); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-changes:
		if v == before {
			t.Fatal("poll produced the same version")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("poll did not catch the missed notification")
	}
}

func TestRefreshCallsOnChangeBeforePublishing(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	var w *dataversion.Watcher
	var seenDuringCallback string
	w = dataversion.NewWatcher(pool, time.Minute, quiet(), func(_ context.Context, v string) {
		seenDuringCallback = w.Current()
	})
	_ = w.Refresh(ctx)
	old := w.Current()
	_, _ = pool.Exec(ctx, `UPDATE catalog_meta SET version = version + 1`)
	_ = w.Refresh(ctx)
	if seenDuringCallback != old {
		t.Fatalf("callback saw %q; the old version must still be published during reload", seenDuringCallback)
	}
	if w.Current() == old {
		t.Fatal("new version not published")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cache/ ./internal/dataversion/`
Expected: FAIL to compile: `undefined: New`, `undefined: dataversion.NewWatcher`.

- [ ] **Step 3: Implement**

Create `internal/cache/cache.go`:

```go
// Package cache is the versioned in-process LRU for season results.
package cache

import (
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

const DefaultSize = 50_000

// Key includes the data version, so a version change makes older entries
// unreachable; LRU eviction reclaims them.
type Key struct {
	CellID      int
	Month       time.Month
	DataVersion string
}

type Cache[V any] struct{ c *lru.Cache[Key, V] }

func New[V any](size int) *Cache[V] {
	c, err := lru.New[Key, V](size)
	if err != nil {
		panic(err) // only for size <= 0
	}
	return &Cache[V]{c: c}
}

func (c *Cache[V]) Get(k Key) (V, bool) { return c.c.Get(k) }
func (c *Cache[V]) Add(k Key, v V)      { c.c.Add(k, v) }
func (c *Cache[V]) Len() int            { return c.c.Len() }
```

Create `internal/dataversion/watcher.go`:

```go
package dataversion

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/store"
)

// Watcher keeps the current data version fresh via LISTEN, with a poll
// fallback for missed notifications.
type Watcher struct {
	pool     *pgxpool.Pool
	poll     time.Duration
	log      *slog.Logger
	onChange func(ctx context.Context, version string)

	mu      sync.Mutex // serialises Refresh
	current sync.Map   // "v" → string
}

func NewWatcher(pool *pgxpool.Pool, poll time.Duration, log *slog.Logger, onChange func(ctx context.Context, version string)) *Watcher {
	return &Watcher{pool: pool, poll: poll, log: log, onChange: onChange}
}

func (w *Watcher) Current() string {
	v, _ := w.current.Load("v")
	s, _ := v.(string)
	return s
}

// Refresh recomputes the version. On a change it runs onChange (e.g. reload
// the snapshot) BEFORE publishing, so readers never see a new version
// paired with the old snapshot.
func (w *Watcher) Refresh(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	v, err := Current(ctx, store.New(w.pool))
	if err != nil {
		return err
	}
	old := w.Current()
	if v == old {
		return nil
	}
	if old != "" && w.onChange != nil {
		w.onChange(ctx, v)
	}
	w.current.Store("v", v)
	w.log.Info("data version", "version", v, "previous", old)
	return nil
}

// Run listens until ctx ends, reconnecting after errors.
func (w *Watcher) Run(ctx context.Context) {
	for ctx.Err() == nil {
		err := w.session(ctx)
		if ctx.Err() != nil {
			return
		}
		w.log.Warn("data version listener stopped; retrying", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
		if err := w.Refresh(ctx); err != nil {
			w.log.Warn("data version refresh failed", "err", err)
		}
	}
}

func (w *Watcher) session(ctx context.Context) error {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN data_version_changed"); err != nil {
		return err
	}
	if err := w.Refresh(ctx); err != nil { // catch anything missed while reconnecting
		w.log.Warn("data version refresh failed", "err", err)
	}
	for {
		wctx, cancel := context.WithTimeout(ctx, w.poll)
		_, err := conn.Conn().WaitForNotification(wctx)
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A notification arrived or the poll interval elapsed.
		if err := w.Refresh(ctx); err != nil {
			w.log.Warn("data version refresh failed", "err", err)
		}
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cache/ ./internal/dataversion/ -count=1`
Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/cache internal/dataversion
git commit -m "feat: versioned LRU cache and LISTEN/poll data-version watcher

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Middleware

**Files:**
- Create: `internal/web/middleware.go`, `internal/web/middleware_test.go`

**Interfaces:**
- Consumes: `golang.org/x/time/rate`.
- Produces:
  - `func web.RequestID(next http.Handler) http.Handler` (reuses a valid inbound `X-Request-ID` of 8–64 of `[A-Za-z0-9-]`, else generates 16 hex; echoes it in the response header)
  - `func web.RequestIDFrom(ctx context.Context) string`
  - `func web.SetCellID(ctx context.Context, cellID int)`
  - `func web.AccessLog(log *slog.Logger) func(http.Handler) http.Handler` (logs `method`, `path` without query, `status`, `duration_ms`, `request_id`, and `cell_id` when set)
  - `func web.Recover(log *slog.Logger, render func(http.ResponseWriter, *http.Request)) func(http.Handler) http.Handler`
  - `func web.ClientIP(r *http.Request, trustProxy bool) string`
  - `type web.Limit struct { Rate rate.Limit; Burst int }`; `func web.NewRateLimiter(now func() time.Time) *RateLimiter`; `(*RateLimiter).Middleware(trustProxy bool, classify func(path string) (class string, lim Limit, ok bool), onLimit func(http.ResponseWriter, *http.Request)) func(http.Handler) http.Handler`
  - `func web.Timeout(d time.Duration) func(http.Handler) http.Handler`
  - `func web.PublicLimits(path string) (string, Limit, bool)`: `/places` → 10/s burst 20; `/season` and `/ingredient/…` → 2/s burst 10

- [ ] **Step 1: Write the failing tests**

Create `internal/web/middleware_test.go`:

```go
package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func ok() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
}

func TestRequestID(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = RequestIDFrom(r.Context()) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if len(seen) != 16 || rec.Header().Get("X-Request-ID") != seen {
		t.Fatalf("generated id %q / header %q", seen, rec.Header().Get("X-Request-ID"))
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "abc-12345")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "abc-12345" {
		t.Fatalf("inbound id not reused: %q", seen)
	}
	req.Header.Set("X-Request-ID", "<bad id>")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen == "<bad id>" {
		t.Fatal("invalid inbound id must be replaced")
	}
}

func TestAccessLogOmitsQueryAndRecordsCell(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := RequestID(AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetCellID(r.Context(), 12345)
		w.WriteHeader(http.StatusTeapot)
	})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/season?lat=51.5&lng=-0.12", nil))
	line := buf.String()
	if strings.Contains(line, "51.5") || strings.Contains(line, "lat=") {
		t.Fatalf("coordinates leaked into log: %s", line)
	}
	for _, want := range []string{`"path":"/season"`, `"status":418`, `"cell_id":12345`, `"request_id"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("log missing %s: %s", want, line)
		}
	}
}

func TestRecover(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := RequestID(Recover(log, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("panel"))
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || rec.Body.String() != "panel" || !strings.Contains(buf.String(), "boom") || !strings.Contains(buf.String(), "request_id") {
		t.Fatalf("code=%d body=%q log=%s", rec.Code, rec.Body.String(), buf.String())
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:5555"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if ClientIP(r, false) != "10.0.0.5" || ClientIP(r, true) != "203.0.113.9" {
		t.Fatalf("got %q / %q", ClientIP(r, false), ClientIP(r, true))
	}
}

func TestRateLimit(t *testing.T) {
	now := time.Unix(0, 0)
	rl := NewRateLimiter(func() time.Time { return now })
	limited := 0
	h := rl.Middleware(false, PublicLimits, func(w http.ResponseWriter, r *http.Request) {
		limited++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})(ok())

	hit := func(path, ip string) int {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = ip + ":1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 0; i < 20; i++ {
		if c := hit("/places", "1.1.1.1"); c != 200 {
			t.Fatalf("request %d limited too early", i)
		}
	}
	if hit("/places", "1.1.1.1") != 429 {
		t.Fatal("21st request in the burst should be limited")
	}
	if hit("/places", "2.2.2.2") != 200 {
		t.Fatal("other IPs are independent")
	}
	for i := 0; i < 10; i++ {
		_ = hit("/season", "3.3.3.3")
	}
	if hit("/ingredient/tomato", "3.3.3.3") != 429 {
		t.Fatal("season and ingredient share the 2/s burst-10 bucket")
	}
	if hit("/about/data", "1.1.1.1") != 200 {
		t.Fatal("unclassified paths are not limited")
	}
	now = now.Add(time.Second) // 10 tokens refill for /places
	if hit("/places", "1.1.1.1") != 200 {
		t.Fatal("tokens should refill")
	}
}

func TestTimeout(t *testing.T) {
	var dl time.Time
	h := Timeout(2 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dl, _ = r.Context().Deadline()
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(context.Background()))
	if d := time.Until(dl); d <= 0 || d > 2*time.Second {
		t.Fatalf("deadline in %v", d)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/web/`
Expected: FAIL to compile: `undefined: RequestID`.

- [ ] **Step 3: Implement the middleware**

Create `internal/web/middleware.go`:

```go
// Package web serves the public site.
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type ctxKey int

const infoKey ctxKey = 0

type reqInfo struct {
	id     string
	cellID *int
}

var inboundIDRE = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !inboundIDRE.MatchString(id) {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), infoKey, &reqInfo{id: id})))
	})
}

func info(ctx context.Context) *reqInfo {
	i, _ := ctx.Value(infoKey).(*reqInfo)
	return i
}

func RequestIDFrom(ctx context.Context) string {
	if i := info(ctx); i != nil {
		return i.id
	}
	return ""
}

// SetCellID records the grid cell for the access log. Coordinates are never logged.
func SetCellID(ctx context.Context, cellID int) {
	if i := info(ctx); i != nil {
		i.cellID = &cellID
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			attrs := []any{
				"method", r.Method, "path", r.URL.Path, "status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(), "request_id", RequestIDFrom(r.Context()),
			}
			if i := info(r.Context()); i != nil && i.cellID != nil {
				attrs = append(attrs, "cell_id", *i.cellID)
			}
			log.Info("request", attrs...)
		})
	}
}

func Recover(log *slog.Logger, render func(http.ResponseWriter, *http.Request)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler {
						panic(v)
					}
					log.Error("panic", "err", v, "request_id", RequestIDFrom(r.Context()), "stack", string(debug.Stack()))
					render(w, r)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP returns the caller's IP. With trustProxy, the first
// X-Forwarded-For entry (set by Caddy) wins.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type Limit struct {
	Rate  rate.Limit
	Burst int
}

// PublicLimits classifies public paths (§11.4).
func PublicLimits(path string) (string, Limit, bool) {
	switch {
	case path == "/places":
		return "places", Limit{Rate: 10, Burst: 20}, true
	case path == "/season" || strings.HasPrefix(path, "/ingredient/"):
		return "season", Limit{Rate: 2, Burst: 10}, true
	}
	return "", Limit{}, false
}

type limiterEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

type RateLimiter struct {
	mu        sync.Mutex
	m         map[string]*limiterEntry
	now       func() time.Time
	lastSweep time.Time
}

func NewRateLimiter(now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	return &RateLimiter{m: map[string]*limiterEntry{}, now: now}
}

func (rl *RateLimiter) allow(key string, l Limit) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	if now.Sub(rl.lastSweep) > time.Minute {
		for k, e := range rl.m {
			if now.Sub(e.seen) > 10*time.Minute {
				delete(rl.m, k)
			}
		}
		rl.lastSweep = now
	}
	e, ok := rl.m[key]
	if !ok {
		e = &limiterEntry{lim: rate.NewLimiter(l.Rate, l.Burst)}
		rl.m[key] = e
	}
	e.seen = now
	return e.lim.AllowN(now, 1)
}

func (rl *RateLimiter) Middleware(trustProxy bool, classify func(string) (string, Limit, bool), onLimit func(http.ResponseWriter, *http.Request)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if class, l, ok := classify(r.URL.Path); ok && !rl.allow(class+"|"+ClientIP(r, trustProxy), l) {
				onLimit(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/web/ -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/web
git commit -m "feat(web): request ID, cell-only access log, recovery, per-IP rate limits, timeout

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: templ setup, layout, static assets, About page and health endpoints

**Files:**
- Create: `static/static.go`, `static/htmx.min.js` (downloaded), `static/app.css`, `static/app.js`, `internal/web/views/models.go`, `internal/web/views/layout.templ`, `internal/web/views/about.templ`, `internal/web/views/season.templ` (panels only in this task), `internal/web/server.go`, `internal/web/fakes_test.go`, `internal/web/server_test.go`

**Interfaces:**
- Consumes: middleware (Task 2), `cache` (Task 1), `catalog.Snapshot`, `season.Adjuster`, `climate.Lookup`, `places.Place`.
- Produces:
  - `static.FS embed.FS`
  - `views.PageVM{ Title, Query string; Places, Results templ.Component }`; `views.Page(PageVM)`; `views.About()`; panels `views.Unsupported()`, `views.LoadError(label, retryURL string)`, `views.NotFound()`, `views.TooMany()`, `views.Location(label string)`
  - Copy constants in `views`: `CopyNoMatch`, `CopyGeoOff`, `CopyUnsupported`, `CopySearchAnother`, `CopyHarsh`, `CopyLoadError`, `CopyTryAgain`, `CopyPaused`, `WildMushroomDisclaimer`
  - `web.Climate`, `web.Ocean`, `web.Places` interfaces; `type web.Deps struct { Climate Climate; Ocean Ocean; Places Places; Snapshot func() *catalog.Snapshot; DataVersion func() string; Adjuster season.Adjuster; Ping func(context.Context) error; Now func() time.Time; Log *slog.Logger; TrustProxy bool }`
  - `func web.New(d Deps) http.Handler` (public routes + public middleware: Recover(panel) → RateLimit → Timeout)
  - `func web.Common(log *slog.Logger) func(http.Handler) http.Handler` (RequestID → AccessLog → Recover(plain 500)); used by `cmd/server` around the whole mux

- [ ] **Step 1: Install templ and vendor HTMX**

```bash
go get -tool github.com/a-h/templ/cmd/templ@latest
mkdir -p static internal/web/views
curl -fsSL -o static/htmx.min.js https://cdn.jsdelivr.net/npm/htmx.org@2.0.4/dist/htmx.min.js
head -c 120 static/htmx.min.js
```

Expected: the file starts with minified JS (`var htmx=function(){…` or similar). Record the version in `static/README.md`: `htmx.min.js — htmx 2.0.4 from jsDelivr (BSD 2-Clause).`

- [ ] **Step 2: Write static assets**

Create `static/static.go`:

```go
// Package static embeds the site's vendored and hand-written assets.
package static

import "embed"

//go:embed htmx.min.js app.css app.js
var FS embed.FS
```

Create `static/app.js`:

```js
// Adds the cook's local date to season/ingredient requests and handles
// "Use my location" with a search fallback.
(function () {
  function localDate() {
    var d = new Date();
    return d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0");
  }
  document.addEventListener("htmx:configRequest", function (e) {
    var p = e.detail.path || "";
    if ((p.indexOf("/season") === 0 || p.indexOf("/ingredient/") === 0) && !e.detail.parameters.date) {
      e.detail.parameters.date = localDate();
    }
  });
  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-geolocate]");
    if (!btn) return;
    var hint = document.getElementById("geo-hint");
    var search = document.getElementById("q");
    function fallback() { hint.hidden = false; search.focus(); }
    if (!navigator.geolocation) { fallback(); return; }
    navigator.geolocation.getCurrentPosition(function (pos) {
      hint.hidden = true;
      // Two decimals (~1 km) is plenty for a 28 km grid and keeps shared URLs coarse.
      var url = "/season?lat=" + pos.coords.latitude.toFixed(2) + "&lng=" + pos.coords.longitude.toFixed(2);
      htmx.ajax("GET", url, { target: "#results", swap: "innerHTML" }).then(function () {
        history.pushState({}, "", url);
      });
    }, fallback, { timeout: 10000, maximumAge: 600000 });
  });
})();
```

Create `static/app.css`:

```css
:root { --ink:#1f2a1f; --muted:#5b6b5b; --bg:#fbfaf6; --line:#e3e0d6; --peak:#2f7d32; --in:#7cb342; --store:#c9a227; --year:#6d8fb3; --warn:#b3261e; }
@media (prefers-color-scheme: dark) { :root { --ink:#e9efe6; --muted:#a9b5a6; --bg:#141814; --line:#2c332b; } }
* { box-sizing: border-box; }
body { margin:0; font:16px/1.5 system-ui, sans-serif; color:var(--ink); background:var(--bg); }
header.site, main, footer.site { max-width: 46rem; margin: 0 auto; padding: 1rem; }
.brand { font-weight:700; font-size:1.4rem; color:inherit; text-decoration:none; }
.tag, .hint, .basis, .when { color: var(--muted); }
.finder input { width:100%; padding:.6rem; font-size:1rem; border:1px solid var(--line); border-radius:.4rem; }
.finder button { margin-top:.5rem; padding:.5rem .8rem; }
.places { list-style:none; padding:0; } .places a { display:block; padding:.3rem 0; }
.panel { border:1px solid var(--line); border-radius:.5rem; padding:1rem; margin:1rem 0; }
.panel.warn { border-color: var(--warn); }
.notice { background: color-mix(in srgb, var(--store) 15%, transparent); padding:.6rem; border-radius:.4rem; }
.group h3 { margin-bottom:.2rem; } .items { list-style:none; padding:0; }
.item { padding:.3rem 0; border-bottom:1px solid var(--line); }
.item .basis { display:block; font-size:.9rem; }
.badge { font-size:.75rem; color: var(--muted); }
.bar { display:grid; grid-template-columns: repeat(12, 1fr); gap:2px; list-style:none; padding:0; }
.bar .m { text-align:center; padding:.3rem 0; background: var(--line); border-radius:.2rem; font-size:.8rem; }
.bar .s-peak { background: var(--peak); color:#fff; } .bar .s-in { background: var(--in); }
.bar .s-store { background: var(--store); } .bar .s-year { background: var(--year); color:#fff; }
.bar .now { outline: 2px solid var(--ink); }
footer.site { color: var(--muted); font-size: .9rem; display:flex; gap:1rem; flex-wrap:wrap; }
.htmx-request { opacity: .6; }
```

- [ ] **Step 3: Write view models and copy constants**

Create `internal/web/views/models.go`:

```go
// Package views holds the templ components for the public site.
package views

import (
	"time"

	"github.com/a-h/templ"

	"thymeandplace/internal/season"
)

// User-facing copy (spec §11.6). Tests assert these exact strings.
const (
	CopyNoMatchSuffix      = "Try a nearby larger town or city."
	CopyGeoOff             = "Location access is off, so search for your town instead."
	CopyUnsupported        = "We can't estimate seasons here yet. Estimates cover land and coastal waters."
	CopySearchAnother      = "Search another place"
	CopyHarsh              = "Not much grows locally this time of year. Here's what's still around."
	CopyLoadError          = "Couldn't load seasonal picks right now. Try again in a moment."
	CopyTryAgain           = "Try again"
	CopyPaused             = "Search is pausing for a few seconds. Keep typing and results will catch up."
	CopySearchUnavailable  = "Search isn't available right now. Try again in a moment."
	CopyNotFound           = "We couldn't find that page."
	WildMushroomDisclaimer = "Look for wild mushrooms at markets and from sellers who can identify them. We don't give foraging advice: many edible mushrooms have deadly look-alikes."
	CopyLandEstimate       = "Estimated from local climate normals."
)

// HTMX config: swap 4xx/5xx so error partials render (§11.5).
const htmxConfig = `{"responseHandling":[{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"[45]..","swap":true,"error":true}],"includeIndicatorStyles":false}`

type PageVM struct {
	Title   string
	Query   string
	Places  templ.Component
	Results templ.Component
}

type PlaceItem struct{ Label, URL string }

type SeasonVM struct {
	Label     string
	MonthName string
	Harsh     bool
	Groups    []Group
}

type Group struct {
	Title string
	Items []Item
}

type Item struct {
	Name, StatusClass, Basis, URL string
	Estimate                      bool
}

type Link struct{ Name, URL string }

type IngredientVM struct {
	Name, Category, Basis, EstimateNote string
	Local, WildMushroom                 bool
	Bars                                []Bar
	TipChoose, TipStore, Note           string
	Pairings                            []Link
	BackURL                             string
}

type Bar struct {
	Letter, Title, Class string
	Current              bool
}

// StatusClass maps a status to its CSS class.
func StatusClass(s season.Status) string {
	switch s {
	case season.Peak:
		return "s-peak"
	case season.InSeason:
		return "s-in"
	case season.FromStorage:
		return "s-store"
	case season.YearRound:
		return "s-year"
	}
	return "s-out"
}

// MonthBars builds the 12-month bar, marking the current month.
func MonthBars(months [12]season.Status, current time.Month) []Bar {
	out := make([]Bar, 12)
	for m := 0; m < 12; m++ {
		name := time.Month(m + 1).String()
		out[m] = Bar{Letter: name[:1], Title: name + ": " + months[m].String(), Class: StatusClass(months[m]), Current: time.Month(m+1) == current}
	}
	return out
}
```

- [ ] **Step 4: Write the layout, About page and panels**

Create `internal/web/views/layout.templ`:

```templ
package views

templ Page(p PageVM) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="utf-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1"/>
			<meta name="htmx-config" content={ htmxConfig }/>
			<title>{ p.Title } · Thyme &amp; Place</title>
			<link rel="stylesheet" href="/static/app.css"/>
			<script src="/static/htmx.min.js" defer></script>
			<script src="/static/app.js" defer></script>
		</head>
		<body>
			<header class="site">
				<a class="brand" href="/">Thyme &amp; Place</a>
				<p class="tag">What's in season where you live, how to buy it, and what it goes with.</p>
			</header>
			<main>
				<section class="finder">
					<label for="q">Find your town</label>
					<input
						id="q"
						name="q"
						type="search"
						autocomplete="off"
						value={ p.Query }
						placeholder="Start typing a town or city"
						hx-get="/places"
						hx-trigger="keyup changed delay:250ms, search"
						hx-target="#places"
					/>
					<button type="button" data-geolocate>Use my location</button>
					<p id="geo-hint" class="hint" hidden>{ CopyGeoOff }</p>
					<div id="places" aria-live="polite">
						if p.Places != nil {
							@p.Places
						}
					</div>
				</section>
				<section id="results" aria-live="polite">
					if p.Results != nil {
						@p.Results
					}
				</section>
			</main>
			<footer class="site">
				<a href="/about/data">About the data</a>
				<span>Place names © GeoNames, CC BY 4.0</span>
			</footer>
		</body>
	</html>
}
```

Create `internal/web/views/about.templ`:

```templ
package views

templ About() {
	<article class="about">
		<h2>About the data</h2>
		<h3 id="estimates">How estimates work</h3>
		<p>Every land result is an estimate. We look up 1991–2020 monthly climate normals for the 0.25° grid cell (about 28 km across) around your location, then apply simple rules per ingredient:</p>
		<ul>
			<li>Annual crops are in season after enough warm, frost-free months to grow them.</li>
			<li>Fruit trees need enough cold months in winter, then ripen a set time after spring warms up.</li>
			<li>Some produce is grown year-round under cover; some keeps for months in storage after harvest.</li>
			<li>Wild mushrooms appear in months that are both mild and wet enough.</li>
		</ul>
		<p>Seafood is different: it depends on spawning seasons and fishing rules, so we use hand-curated months for marine regions within 100 km of the coast. Treat it as general guidance and check local fishing rules.</p>
		<p>Estimates are checked against published seasonal guides for about fifteen places around the world. They won't match every market; local growers, greenhouses and imports all make a difference.</p>
		<h3>Wild mushrooms</h3>
		<p>{ WildMushroomDisclaimer }</p>
		<h3>Sources and licenses</h3>
		<ul>
			<li>Climate: TerraClimate (Abatzoglou, J.T., Dobrowski, S.Z., Parks, S.A., Hegewisch, K.C., 2018, Scientific Data), CC0. 1991–2020 normals.</li>
			<li>Marine regions: Marine Ecoregions of the World (Spalding, M.D. et al., 2007, BioScience), distributed by UNEP-WCMC; used under its data licence.</li>
			<li>Places: <a href="https://www.geonames.org/">GeoNames</a>, licensed under <a href="https://creativecommons.org/licenses/by/4.0/">CC BY 4.0</a>.</li>
		</ul>
		<h3>Privacy</h3>
		<p>There are no accounts and no cookies on the public site. We do not store the locations you look up. Our logs record only the roughly 28 km grid cell of a request, never your coordinates or IP address. Our single administrator's sign-in log keeps IP addresses for security.</p>
	</article>
}
```

Create `internal/web/views/season.templ` (panels now; the `Season` list is added in Task 5):

```templ
package views

templ Location(label string) {
	<header class="where"><h2>{ label }</h2></header>
}

templ Unsupported() {
	<div class="panel">
		<p>{ CopyUnsupported }</p>
		<p><a href="/#q">{ CopySearchAnother }</a></p>
	</div>
}

templ LoadError(label, retryURL string) {
	if label != "" {
		@Location(label)
	}
	<div class="panel warn" role="alert">
		<p>{ CopyLoadError }</p>
		<p><a href={ templ.SafeURL(retryURL) } hx-get={ retryURL } hx-target="#results">{ CopyTryAgain }</a></p>
	</div>
}

templ NotFound() {
	<div class="panel">
		<p>{ CopyNotFound }</p>
		<p><a href="/">Back to the start</a></p>
	</div>
}

templ TooMany() {
	<div class="panel"><p>{ CopyPaused }</p></div>
}
```

Run: `go tool templ generate && go build ./internal/web/views/`
Expected: `*_templ.go` files generated; compiles.

- [ ] **Step 5: Write the server skeleton**

Create `internal/web/server.go`:

```go
package web

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/a-h/templ"

	"thymeandplace/internal/cache"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/places"
	"thymeandplace/internal/season"
	"thymeandplace/internal/web/views"
	"thymeandplace/static"
)

type Climate interface {
	Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error)
	ForCell(ctx context.Context, id int) (*climate.MonthlyNormals, error)
}

type Ocean interface {
	Nearby(ctx context.Context, lat, lng float64) ([]ocean.EcoregionID, error)
}

type Places interface {
	Search(ctx context.Context, q string) ([]places.Place, error)
	Nearest(ctx context.Context, lat, lng float64) (places.Place, bool, error)
	ByID(ctx context.Context, id int32) (places.Place, bool, error)
}

type Deps struct {
	Climate     Climate
	Ocean       Ocean
	Places      Places
	Snapshot    func() *catalog.Snapshot
	DataVersion func() string
	Adjuster    season.Adjuster
	Ping        func(context.Context) error
	Now         func() time.Time
	Log         *slog.Logger
	TrustProxy  bool
}

type Server struct {
	d     Deps
	cache *cache.Cache[seasonCore]
}

// New returns the public handler with its own middleware (§11.4):
// panic recovery → per-IP rate limit → 2 s timeout.
func New(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Adjuster == nil {
		d.Adjuster = season.NoopAdjuster{}
	}
	s := &Server{d: d, cache: cache.New[seasonCore](cache.DefaultSize)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /places", s.places)
	mux.HandleFunc("GET /season", s.season)
	mux.HandleFunc("GET /ingredient/{slug}", s.ingredient)
	mux.HandleFunc("GET /about/data", s.about)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(24*time.Hour, http.FileServerFS(static.FS))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.render(w, r, http.StatusNotFound, "Not found", "", nil, views.NotFound())
	})

	var h http.Handler = mux
	h = Timeout(2 * time.Second)(h)
	h = NewRateLimiter(nil).Middleware(d.TrustProxy, PublicLimits, s.tooMany)(h)
	h = Recover(d.Log, func(w http.ResponseWriter, r *http.Request) {
		s.render(w, r, http.StatusInternalServerError, "Something went wrong", "", nil, views.LoadError("", r.URL.RequestURI()))
	})(h)
	return h
}

// Common wraps the whole site (public and admin): request ID → access log →
// last-resort recovery.
func Common(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		plain := Recover(log, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		})(next)
		return RequestID(AccessLog(log)(plain))
	}
}

func cacheFor(d time.Duration, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(int(d.Seconds())))
		h.ServeHTTP(w, r)
	})
}

// partial reports whether to send a fragment rather than a full page.
func partial(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true"
}

// render writes either the fragment (for HTMX) or the full page with the
// fragment in the right slot. placesSlot is used only by /places.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, title, query string, placesSlot, results templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Add("Vary", "HX-Request")
	w.WriteHeader(status)
	var c templ.Component
	switch {
	case partial(r) && placesSlot != nil:
		c = placesSlot
	case partial(r):
		c = results
	default:
		c = views.Page(views.PageVM{Title: title, Query: query, Places: placesSlot, Results: results})
	}
	if c == nil { // an HTMX request for a page with no fragment (e.g. GET / with HX-Request)
		c = templ.NopComponent
	}
	if err := c.Render(r.Context(), w); err != nil {
		s.d.Log.Warn("render failed", "err", err, "request_id", RequestIDFrom(r.Context()))
	}
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "What's in season", "", nil, nil)
}

func (s *Server) about(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "About the data", "", nil, views.About())
}

func (s *Server) tooMany(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "1")
	if r.URL.Path == "/places" {
		s.render(w, r, http.StatusTooManyRequests, "Search", "", views.PlacesPaused(), nil)
		return
	}
	s.render(w, r, http.StatusTooManyRequests, "Please wait", "", nil, views.TooMany())
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.d.Snapshot() == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("catalog not loaded"))
		return
	}
	if err := s.d.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("database unavailable"))
		return
	}
	_, _ = w.Write([]byte("ready"))
}
```

`s.places`, `s.season`, `s.ingredient`, `seasonCore` and `views.PlacesPaused` are added in Tasks 4–6. To compile this task on its own, add these temporary stubs to `internal/web/server.go` and delete them in the task that implements each one:

```go
type seasonCore struct{}

func (s *Server) places(w http.ResponseWriter, r *http.Request)     { http.NotFound(w, r) }
func (s *Server) season(w http.ResponseWriter, r *http.Request)     { http.NotFound(w, r) }
func (s *Server) ingredient(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
```

and add to `internal/web/views/season.templ` temporarily:

```templ
templ PlacesPaused() {
	<p class="hint">{ CopyPaused }</p>
}
```

(`PlacesPaused` moves to `places.templ` in Task 4.)

- [ ] **Step 6: Write fakes and the server tests**

Create `internal/web/fakes_test.go`:

```go
package web

import (
	"context"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/places"
)

var errDown = errors.New("database unavailable")

// fakeClimate: temperate normals north of -50°, polar north of 65°, none south of -50°.
type fakeClimate struct {
	down  bool
	panic bool
	calls int
}

func (f *fakeClimate) Normals(_ context.Context, lat, lng float64) (climate.Lookup, error) {
	f.calls++
	if f.panic {
		panic("climate exploded")
	}
	if f.down {
		return climate.Lookup{}, errDown
	}
	cell := climate.CellID(lat, lng)
	switch {
	case lat < -50:
		return climate.Lookup{QueryCell: cell, Cell: cell}, nil
	case lat > 65:
		n := climatetest.Polar()
		return climate.Lookup{QueryCell: cell, Cell: cell, Normals: &n}, nil
	}
	n := climatetest.Temperate()
	return climate.Lookup{QueryCell: cell, Cell: cell, Normals: &n}, nil
}

func (f *fakeClimate) ForCell(ctx context.Context, id int) (*climate.MonthlyNormals, error) {
	lat, lng := climate.CellCenter(id)
	l, err := f.Normals(ctx, lat, lng)
	return l.Normals, err
}

// fakeOcean: ecoregions 25 and 30 west of the prime meridian.
type fakeOcean struct{ down bool }

func (f *fakeOcean) Nearby(_ context.Context, _, lng float64) ([]ocean.EcoregionID, error) {
	if f.down {
		return nil, errDown
	}
	if lng < 0 {
		return []ocean.EcoregionID{25, 30}, nil
	}
	return nil, nil
}

type fakePlaces struct{ down bool }

var lisbon = places.Place{ID: 1, Name: "Lisbon", Admin1: "Lisbon", Country: "PT", Lat: 38.72, Lng: -9.14}

func (f *fakePlaces) Search(_ context.Context, q string) ([]places.Place, error) {
	if f.down {
		return nil, errDown
	}
	if q == "lis" {
		return []places.Place{lisbon}, nil
	}
	return nil, nil
}

func (f *fakePlaces) Nearest(_ context.Context, lat, lng float64) (places.Place, bool, error) {
	if f.down {
		return places.Place{}, false, errDown
	}
	p := lisbon
	p.DistanceKm = climate.DistanceKm(lat, lng, p.Lat, p.Lng)
	return p, true, nil
}

func (f *fakePlaces) ByID(_ context.Context, id int32) (places.Place, bool, error) {
	if f.down {
		return places.Place{}, false, errDown
	}
	return lisbon, id == 1, nil
}

type harness struct {
	h       http.Handler
	climate *fakeClimate
	ocean   *fakeOcean
	places  *fakePlaces
	snap    *catalog.Snapshot
	pingErr error
	version string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	snap, err := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	if err != nil {
		t.Fatal(err)
	}
	hs := &harness{climate: &fakeClimate{}, ocean: &fakeOcean{}, places: &fakePlaces{}, snap: snap, version: "v1"}
	hs.h = New(Deps{
		Climate: hs.climate, Ocean: hs.ocean, Places: hs.places,
		Snapshot:    func() *catalog.Snapshot { return hs.snap },
		DataVersion: func() string { return hs.version },
		Ping:        func(context.Context) error { return hs.pingErr },
		Now:         func() time.Time { return time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC) },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return hs
}

// get performs a request; hx adds HX-Request. It returns status and the
// HTML-unescaped body.
func (hs *harness) get(path string, hx bool, headers ...string) (int, string, http.Header) {
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = "192.0.2.1:1234"
	if hx {
		r.Header.Set("HX-Request", "true")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, r)
	return rec.Code, html.UnescapeString(rec.Body.String()), rec.Header()
}
```

Create `internal/web/server_test.go`:

```go
package web

import (
	"errors"
	"strings"
	"testing"

	"thymeandplace/internal/web/views"
)

func TestHomeAndAbout(t *testing.T) {
	hs := newHarness(t)
	code, body, _ := hs.get("/", false)
	if code != 200 || !strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, views.CopyGeoOff) ||
		!strings.Contains(body, `hx-trigger="keyup changed delay:250ms, search"`) || !strings.Contains(body, "Use my location") {
		t.Fatalf("home: %d\n%s", code, body)
	}
	code, body, _ = hs.get("/about/data", false)
	for _, want := range []string{"TerraClimate", "CC0", "GeoNames", "CC BY 4.0", "Marine Ecoregions of the World", "do not store the locations", "How estimates work"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("about missing %q", want)
		}
	}
}

func TestHealthAndReady(t *testing.T) {
	hs := newHarness(t)
	if code, _, _ := hs.get("/healthz", false); code != 200 {
		t.Fatal("healthz")
	}
	if code, _, _ := hs.get("/readyz", false); code != 200 {
		t.Fatal("readyz should be ready")
	}
	hs.pingErr = errors.New("down")
	if code, _, _ := hs.get("/readyz", false); code != 503 {
		t.Fatal("readyz must fail when the DB ping fails")
	}
	hs.pingErr = nil
	hs.snap = nil
	if code, _, _ := hs.get("/readyz", false); code != 503 {
		t.Fatal("readyz must fail without a snapshot")
	}
}

func TestStaticAndNotFound(t *testing.T) {
	hs := newHarness(t)
	code, _, hdr := hs.get("/static/app.js", false)
	if code != 200 || !strings.Contains(hdr.Get("Cache-Control"), "max-age=86400") {
		t.Fatalf("static: %d %v", code, hdr)
	}
	if code, body, _ := hs.get("/nope", false); code != 404 || !strings.Contains(body, views.CopyNotFound) {
		t.Fatalf("404: %d", code)
	}
}
```

- [ ] **Step 7: Run the tests**

Run: `go tool templ generate && go test ./internal/web/... -count=1`
Expected: `ok`.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum static internal/web
git commit -m "feat(web): templ layout, vendored htmx 2.0.4, about page, health endpoints

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: `/places`

**Files:**
- Create: `internal/web/views/places.templ`, `internal/web/places.go`, `internal/web/places_test.go`
- Modify: `internal/web/server.go` (remove the `places` stub), `internal/web/views/season.templ` (remove the temporary `PlacesPaused`)

**Interfaces:**
- Consumes: `Places.Search`, `render`.
- Produces: `views.PlaceList([]PlaceItem)`, `views.PlacesNoMatch(q string)`, `views.PlacesPaused()`, `views.PlacesUnavailable()`; handler `(*Server).places`. Each result links to `/season?lat=<4dp>&lng=<4dp>&place=<id>` with `hx-get`, `hx-target="#results"`, `hx-push-url="true"`.

- [ ] **Step 1: Write the failing tests**

Create `internal/web/places_test.go`:

```go
package web

import (
	"strings"
	"testing"

	"thymeandplace/internal/web/views"
)

func TestPlacesMatches(t *testing.T) {
	hs := newHarness(t)
	code, body, hdr := hs.get("/places?q=lis", true)
	if code != 200 || strings.Contains(body, "<html") || !strings.Contains(body, "Lisbon, PT") ||
		!strings.Contains(body, `/season?lat=38.7200&lng=-9.1400&place=1`) || !strings.Contains(hdr.Get("Vary"), "HX-Request") {
		t.Fatalf("%d %v\n%s", code, hdr, body)
	}
}

func TestPlacesNoMatch(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/places?q=zzz", true)
	if !strings.Contains(body, "No places match 'zzz'. "+views.CopyNoMatchSuffix) {
		t.Fatalf("body = %s", body)
	}
}

// Review Focus 5: markup in the query is escaped.
func TestPlacesEscapesQuery(t *testing.T) {
	hs := newHarness(t)
	r := `/places?q=%3Cscript%3Ealert(1)%3C%2Fscript%3E`
	code, _, _ := hs.get(r, true)
	if code != 200 {
		t.Fatal(code)
	}
	raw := rawBody(t, hs, r)
	if strings.Contains(raw, "<script>alert(1)</script>") || !strings.Contains(raw, "&lt;script&gt;") {
		t.Fatalf("unescaped markup: %s", raw)
	}
}

func TestPlacesBlankQuery(t *testing.T) {
	hs := newHarness(t)
	code, body, _ := hs.get("/places?q=%20%20", true)
	if code != 200 || strings.Contains(body, "No places match") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestPlacesDown(t *testing.T) {
	hs := newHarness(t)
	hs.places.down = true
	code, body, _ := hs.get("/places?q=lis", true)
	if code != 503 || !strings.Contains(body, views.CopySearchUnavailable) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestPlacesRateLimited(t *testing.T) {
	hs := newHarness(t)
	var code int
	var body string
	var hdr = map[string][]string{}
	for i := 0; i < 21; i++ {
		code, body, hdr = hs.get("/places?q=lis", true)
	}
	if code != 429 || !strings.Contains(body, views.CopyPaused) || len(hdr["Retry-After"]) == 0 {
		t.Fatalf("%d %s", code, body)
	}
}

func TestPlacesFullPageWithoutHX(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/places?q=lis", false)
	if !strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, `value="lis"`) || !strings.Contains(body, "Lisbon, PT") {
		t.Fatalf("body = %s", body)
	}
}
```

Add to `internal/web/fakes_test.go`:

```go
func rawBody(t *testing.T, hs *harness, path string) string {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = "192.0.2.99:1"
	r.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, r)
	return rec.Body.String()
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/web/ -run Places`
Expected: FAIL (`/places` returns 404 from the stub, or `views.PlaceList` is undefined).

- [ ] **Step 3: Implement**

Create `internal/web/views/places.templ`:

```templ
package views

templ PlaceList(items []PlaceItem) {
	<ul class="places">
		for _, it := range items {
			<li>
				<a href={ templ.SafeURL(it.URL) } hx-get={ it.URL } hx-target="#results" hx-push-url="true">{ it.Label }</a>
			</li>
		}
	</ul>
}

templ PlacesNoMatch(q string) {
	<p class="hint">No places match '{ q }'. { CopyNoMatchSuffix }</p>
}

templ PlacesPaused() {
	<p class="hint">{ CopyPaused }</p>
}

templ PlacesUnavailable() {
	<p class="hint">{ CopySearchUnavailable }</p>
}
```

Remove the temporary `PlacesPaused` from `season.templ`, and the `places` stub from `server.go`.

Create `internal/web/places.go`:

```go
package web

import (
	"fmt"
	"net/http"
	"strings"

	"thymeandplace/internal/web/views"
)

func seasonURL(lat, lng float64, placeID int32) string {
	return fmt.Sprintf("/season?lat=%.4f&lng=%.4f&place=%d", lat, lng, placeID)
}

func (s *Server) places(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		s.render(w, r, http.StatusOK, "Search", "", views.PlaceList(nil), nil)
		return
	}
	found, err := s.d.Places.Search(r.Context(), q)
	if err != nil {
		s.d.Log.Warn("place search failed", "err", err, "request_id", RequestIDFrom(r.Context()))
		s.render(w, r, http.StatusServiceUnavailable, "Search", q, views.PlacesUnavailable(), nil)
		return
	}
	if len(found) == 0 {
		s.render(w, r, http.StatusOK, "Search", q, views.PlacesNoMatch(q), nil)
		return
	}
	items := make([]views.PlaceItem, len(found))
	for i, p := range found {
		items[i] = views.PlaceItem{Label: p.Label(), URL: seasonURL(p.Lat, p.Lng, p.ID)}
	}
	s.render(w, r, http.StatusOK, "Search", q, views.PlaceList(items), nil)
}
```

- [ ] **Step 4: Add the history-restore test (Review Focus 4)**

Append to `internal/web/server_test.go`:

```go
func TestHistoryRestoreGetsFullPage(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/places?q=lis", true, "HX-History-Restore-Request", "true")
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Fatal("history restore must return a full page")
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `go tool templ generate && go test ./internal/web/... -count=1`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/web
git commit -m "feat(web): /places search partial with no-match, paused and outage states

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: `/season`

**Files:**
- Create: `internal/web/season.go`, `internal/web/season_test.go`
- Modify: `internal/web/views/season.templ` (add `Season`), `internal/web/server.go` (remove the `season` stub and `seasonCore` stub)

**Interfaces:**
- Consumes: `Climate`, `Ocean`, `Places`, `season.Estimate`, `Adjuster`, `cache`, `SetCellID`.
- Produces:
  - `type seasonCore struct { Cell int; HasNormals, Supported bool; Results []season.Result; NearLabel string }` (cached value)
  - `const harshThreshold = 3`, `const nearKm = 50`
  - `func parseLatLng(latS, lngS string) (lat, lng float64, ok bool)`
  - `func (s *Server) monthFor(date string) time.Month`
  - `func ingredientURL(slug string, cell int, date string) string` (`/ingredient/<slug>?cell=<n>[&date=…]`)
  - `views.Season(SeasonVM)`

- [ ] **Step 1: Write the failing tests**

Create `internal/web/season_test.go`:

```go
package web

import (
	"strings"
	"testing"

	"thymeandplace/internal/web/views"
)

const lisbonQ = "/season?lat=38.7200&lng=-9.1400&place=1"

func TestSeasonGroups(t *testing.T) {
	hs := newHarness(t)
	code, body, _ := hs.get(lisbonQ+"&date=2026-08-15", true)
	if code != 200 || strings.Contains(body, "<html") {
		t.Fatalf("%d\n%s", code, body)
	}
	for _, want := range []string{"Lisbon, PT", "August", "Peak", "Year-round", "Seafood", "Tomato", "Banana", "Mackerel", "Estimate", "general guidance; check local fishing rules"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in\n%s", want, body)
		}
	}
	if strings.Contains(body, "Kale") {
		t.Fatal("kale is out of season in August")
	}
	if i, j := strings.Index(body, ">Peak<"), strings.Index(body, ">Seafood<"); i < 0 || j < i {
		t.Fatal("seafood group must come after the land groups")
	}
}

func TestSeasonInlandOmitsSeafood(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/season?lat=40.4&lng=3.7&date=2026-08-15", true)
	if strings.Contains(body, "Seafood") || strings.Contains(body, "Mackerel") {
		t.Fatal("inland must omit the seafood group")
	}
	if strings.Contains(body, views.CopyUnsupported) {
		t.Fatal("inland is supported")
	}
}

func TestSeasonLabelNearestOrCoordinates(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/season?lat=38.80&lng=-9.10&date=2026-08-15", true)
	if !strings.Contains(body, "near Lisbon, PT") {
		t.Fatalf("near label missing:\n%s", body)
	}
	_, body, _ = hs.get("/season?lat=45.00&lng=-30.00&date=2026-08-15", true)
	if !strings.Contains(body, "45.00°N, 30.00°W") {
		t.Fatalf("coordinate label missing:\n%s", body)
	}
}

func TestSeasonUnsupported(t *testing.T) {
	hs := newHarness(t)
	code, body, _ := hs.get("/season?lat=-60&lng=20", true)
	if code != 200 || !strings.Contains(body, views.CopyUnsupported) || !strings.Contains(body, views.CopySearchAnother) {
		t.Fatalf("%d\n%s", code, body)
	}
}

func TestSeasonHarsh(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/season?lat=70&lng=20&date=2026-01-10", true)
	if !strings.Contains(body, views.CopyHarsh) || !strings.Contains(body, "Banana") {
		t.Fatalf("harsh:\n%s", body)
	}
}

// Review Focus 1.
func TestSeasonBadCoordinates(t *testing.T) {
	hs := newHarness(t)
	for _, q := range []string{"lat=abc&lng=1", "lat=NaN&lng=1", "lat=1&lng=Inf", "lat=91&lng=0", "lat=0&lng=181", "lat=1", ""} {
		code, body, _ := hs.get("/season?"+q, true)
		if code != 400 || !strings.Contains(body, views.CopyUnsupported) {
			t.Fatalf("%q: %d", q, code)
		}
	}
}

// Review Focus 2.
func TestSeasonMonthFromLocalDate(t *testing.T) {
	hs := newHarness(t) // server clock: 2026-01-15 UTC
	_, body, _ := hs.get(lisbonQ+"&date=2026-12-31", true)
	if !strings.Contains(body, "December") {
		t.Fatal("client's local December must win over server January")
	}
	code, body, _ := hs.get(lisbonQ+"&date=31/12/2026", true)
	if code != 200 || !strings.Contains(body, "January") {
		t.Fatalf("malformed date must fall back to the server month: %d", code)
	}
}

func TestSeasonDatabaseDown(t *testing.T) {
	hs := newHarness(t)
	hs.climate.down = true
	code, body, _ := hs.get(lisbonQ+"&date=2026-08-15", true)
	if code != 503 || !strings.Contains(body, views.CopyLoadError) || !strings.Contains(body, views.CopyTryAgain) ||
		!strings.Contains(body, `hx-get="/season?lat=38.7200&lng=-9.1400&place=1&date=2026-08-15"`) ||
		!strings.Contains(body, "38.72°N, 9.14°W") {
		t.Fatalf("%d\n%s", code, body)
	}
}

// Review Focus 3.
func TestSeasonWarmCacheSurvivesOutage(t *testing.T) {
	hs := newHarness(t)
	if code, _, _ := hs.get(lisbonQ+"&date=2026-08-15", true); code != 200 {
		t.Fatal("warm-up failed")
	}
	calls := hs.climate.calls
	hs.climate.down, hs.ocean.down, hs.places.down = true, true, true
	code, body, _ := hs.get(lisbonQ+"&date=2026-08-20", true)
	if code != 200 || !strings.Contains(body, "Tomato") || hs.climate.calls != calls {
		t.Fatalf("cached result not served during outage: %d", code)
	}
	hs.version = "v2" // a data-version change makes the entry unreachable
	if code, _, _ := hs.get(lisbonQ+"&date=2026-08-20", true); code != 503 {
		t.Fatal("new data version must miss the cache")
	}
}

func TestSeasonPanicRendersPanel(t *testing.T) {
	hs := newHarness(t)
	hs.climate.panic = true
	code, body, _ := hs.get(lisbonQ, true)
	if code != 500 || !strings.Contains(body, views.CopyLoadError) {
		t.Fatalf("%d\n%s", code, body)
	}
}

func TestSeasonFullPage(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(lisbonQ+"&date=2026-08-15", false)
	if !strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, `id="results"`) || !strings.Contains(body, "Tomato") {
		t.Fatal("full page expected")
	}
}

func TestSeasonItemLinksCarryCellAndDate(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(lisbonQ+"&date=2026-08-15", true)
	if !strings.Contains(body, "/ingredient/tomato?cell=") || !strings.Contains(body, "&date=2026-08-15") {
		t.Fatalf("item link:\n%s", body)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/web/ -run Season`
Expected: FAIL (the stub returns 404).

- [ ] **Step 3: Add the `Season` component**

Append to `internal/web/views/season.templ`:

```templ
templ Season(vm SeasonVM) {
	<article class="season">
		<header class="where">
			<h2>{ vm.Label }</h2>
			<p class="when">{ vm.MonthName } · <a href="/about/data#estimates">Estimates from local climate: how we estimate</a></p>
		</header>
		if vm.Harsh {
			<p class="notice">{ CopyHarsh }</p>
		}
		for _, g := range vm.Groups {
			<section class="group">
				<h3>{ g.Title }</h3>
				<ul class="items">
					for _, it := range g.Items {
						<li class={ "item", it.StatusClass }>
							<a href={ templ.SafeURL(it.URL) } hx-get={ it.URL } hx-target="#results" hx-push-url="true">{ it.Name }</a>
							<span class="basis">{ it.Basis }</span>
							if it.Estimate {
								<span class="badge">Estimate</span>
							}
						</li>
					}
				</ul>
			</section>
		}
	</article>
}
```

- [ ] **Step 4: Implement the handler**

Create `internal/web/season.go` (and delete the `season`/`seasonCore` stubs from `server.go`):

```go
package web

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"thymeandplace/internal/cache"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/season"
	"thymeandplace/internal/web/views"
)

const (
	harshThreshold = 3  // fewer land items at peak/in season than this → "not much grows"
	nearKm         = 50 // "near X" only when X is this close
)

// seasonCore is the cached, location-and-month specific part of /season.
type seasonCore struct {
	Cell       int
	HasNormals bool
	Supported  bool
	Results    []season.Result
	NearLabel  string
}

func parseLatLng(latS, lngS string) (float64, float64, bool) {
	lat, err1 := strconv.ParseFloat(latS, 64)
	lng, err2 := strconv.ParseFloat(lngS, 64)
	if err1 != nil || err2 != nil || math.IsNaN(lat) || math.IsNaN(lng) || math.IsInf(lat, 0) || math.IsInf(lng, 0) ||
		lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, false
	}
	return lat, lng, true
}

// monthFor uses the cook's local date; missing or malformed → server UTC (§12.5).
func (s *Server) monthFor(date string) time.Month {
	if d, err := time.Parse("2006-01-02", date); err == nil {
		return d.Month()
	}
	return s.d.Now().UTC().Month()
}

func validDate(date string) string {
	if _, err := time.Parse("2006-01-02", date); err == nil {
		return date
	}
	return ""
}

func coords(lat, lng float64) string {
	ns, ew := "N", "E"
	if lat < 0 {
		ns = "S"
	}
	if lng < 0 {
		ew = "W"
	}
	return fmt.Sprintf("%.2f°%s, %.2f°%s", math.Abs(lat), ns, math.Abs(lng), ew)
}

func ingredientURL(slug string, cell int, date string) string {
	v := url.Values{"cell": {strconv.Itoa(cell)}}
	if date != "" {
		v.Set("date", date)
	}
	return "/ingredient/" + url.PathEscape(slug) + "?" + v.Encode()
}

func (s *Server) season(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lat, lng, ok := parseLatLng(q.Get("lat"), q.Get("lng"))
	if !ok {
		s.render(w, r, http.StatusBadRequest, "Seasons", "", nil, views.Unsupported())
		return
	}
	ctx := r.Context()
	month := s.monthFor(q.Get("date"))
	queryCell := climate.CellID(lat, lng)
	SetCellID(ctx, queryCell)

	snap := s.d.Snapshot()
	if snap == nil {
		s.render(w, r, http.StatusServiceUnavailable, "Seasons", "", nil, views.LoadError(coords(lat, lng), r.URL.RequestURI()))
		return
	}
	key := cache.Key{CellID: queryCell, Month: month, DataVersion: s.d.DataVersion()}
	core, hit := s.cache.Get(key)
	if !hit {
		var err error
		core, err = s.compute(ctx, lat, lng, month, snap)
		if err != nil {
			s.d.Log.Warn("season lookup failed", "err", err, "request_id", RequestIDFrom(ctx), "cell_id", queryCell)
			s.render(w, r, http.StatusServiceUnavailable, "Seasons", "", nil, views.LoadError(coords(lat, lng), r.URL.RequestURI()))
			return
		}
		s.cache.Add(key, core)
	}
	if !core.Supported {
		s.render(w, r, http.StatusOK, "Seasons", "", nil, views.Unsupported())
		return
	}
	vm := s.seasonVM(ctx, core, snap, month, q.Get("place"), lat, lng, validDate(q.Get("date")))
	s.render(w, r, http.StatusOK, vm.Label, "", nil, views.Season(vm))
}

func (s *Server) compute(ctx context.Context, lat, lng float64, month time.Month, snap *catalog.Snapshot) (seasonCore, error) {
	l, err := s.d.Climate.Normals(ctx, lat, lng)
	if err != nil {
		return seasonCore{}, err
	}
	eco, err := s.d.Ocean.Nearby(ctx, lat, lng)
	if err != nil {
		return seasonCore{}, err
	}
	in := season.Input{Normals: l.Normals, Ecoregions: eco, Month: month}
	results := s.d.Adjuster.Adjust(ctx, l.Cell, season.Estimate(in, snap))
	core := seasonCore{
		Cell: l.Cell, HasNormals: l.Normals != nil, Supported: l.Normals != nil || len(eco) > 0, Results: results,
	}
	if p, ok, err := s.d.Places.Nearest(ctx, lat, lng); err == nil && ok && p.DistanceKm <= nearKm {
		core.NearLabel = "near " + p.Label()
	}
	return core, nil
}

func (s *Server) label(ctx context.Context, placeParam string, core seasonCore, lat, lng float64) string {
	if id, err := strconv.ParseInt(placeParam, 10, 32); err == nil {
		if p, ok, err := s.d.Places.ByID(ctx, int32(id)); err == nil && ok {
			return p.Label()
		}
	}
	if core.NearLabel != "" {
		return core.NearLabel
	}
	return coords(lat, lng)
}

var groupOrder = []struct {
	status season.Status
	title  string
}{
	{season.Peak, "Peak"}, {season.InSeason, "In season"}, {season.FromStorage, "From storage"}, {season.YearRound, "Year-round"},
}

func (s *Server) seasonVM(ctx context.Context, core seasonCore, snap *catalog.Snapshot, month time.Month, placeParam string, lat, lng float64, date string) views.SeasonVM {
	vm := views.SeasonVM{Label: s.label(ctx, placeParam, core, lat, lng), MonthName: month.String()}
	byStatus := map[season.Status][]views.Item{}
	var seafood []views.Item
	growing := 0
	for _, res := range core.Results {
		ing, ok := snap.Get(res.Slug)
		if !ok {
			continue
		}
		it := views.Item{
			Name: ing.Name, StatusClass: views.StatusClass(res.Status), Basis: res.Basis,
			URL: ingredientURL(res.Slug, core.Cell, date), Estimate: res.Estimate,
		}
		if ing.Kind == catalog.KindSeafood {
			seafood = append(seafood, it)
			continue
		}
		if res.Status == season.Peak || res.Status == season.InSeason {
			growing++
		}
		byStatus[res.Status] = append(byStatus[res.Status], it)
	}
	vm.Harsh = core.HasNormals && growing < harshThreshold
	for _, g := range groupOrder {
		if items := byStatus[g.status]; len(items) > 0 {
			sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
			vm.Groups = append(vm.Groups, views.Group{Title: g.title, Items: items})
		}
	}
	if len(seafood) > 0 {
		sort.Slice(seafood, func(i, j int) bool { return seafood[i].Name < seafood[j].Name })
		vm.Groups = append(vm.Groups, views.Group{Title: "Seafood", Items: seafood})
	}
	return vm
}
```

`LoadError`'s retry URL is `r.URL.RequestURI()`, which already holds the original query (including `date`). The test's expected `hx-get` includes `&date=2026-08-15` for that reason.

- [ ] **Step 5: Run the tests**

Run: `go tool templ generate && go test ./internal/web/... -count=1`
Expected: `ok`. The panic test's 500 panel comes from the public `Recover`, which renders `LoadError` (§11.6: "Same panel as 503").

- [ ] **Step 6: Commit**

```bash
git add internal/web
git commit -m "feat(web): /season results with grouping, harsh/unsupported/outage states and cache

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: `/ingredient/{slug}`

**Files:**
- Create: `internal/web/views/ingredient.templ`, `internal/web/ingredient.go`, `internal/web/ingredient_test.go`
- Modify: `internal/web/server.go` (remove the `ingredient` stub)

**Interfaces:**
- Consumes: `Climate.ForCell`, `Ocean.Nearby`, `season.Year`, `views.MonthBars`.
- Produces: `views.Ingredient(IngredientVM)`; handler `(*Server).ingredient`.

- [ ] **Step 1: Write the failing tests**

Create `internal/web/ingredient_test.go`:

```go
package web

import (
	"fmt"
	"strings"
	"testing"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/web/views"
)

var lisbonCell = climate.CellID(38.72, -9.14)

func TestIngredientDetail(t *testing.T) {
	hs := newHarness(t)
	code, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d&date=2026-08-15", lisbonCell), true)
	if code != 200 || strings.Contains(body, "<html") {
		t.Fatalf("%d\n%s", code, body)
	}
	for _, want := range []string{"Tomato", "Heavy for its size", "Stem-side down", "Basil", `title="August: peak"`, "now", views.CopyLandEstimate, "growing conditions Apr–Sep"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, views.WildMushroomDisclaimer) {
		t.Fatal("disclaimer is for wild mushrooms only")
	}
}

func TestIngredientWildMushroomDisclaimer(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(fmt.Sprintf("/ingredient/chanterelle?cell=%d", lisbonCell), true)
	if !strings.Contains(body, views.WildMushroomDisclaimer) {
		t.Fatal("wild mushroom disclaimer missing")
	}
}

func TestIngredientSeafood(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(fmt.Sprintf("/ingredient/atlantic-cod?cell=%d", lisbonCell), true)
	if !strings.Contains(body, "general guidance; check local fishing rules") || strings.Contains(body, views.CopyLandEstimate) {
		t.Fatalf("seafood detail:\n%s", body)
	}
}

func TestIngredientNotLocal(t *testing.T) {
	hs := newHarness(t)
	inland := climate.CellID(40.4, 3.7)
	_, body, _ := hs.get(fmt.Sprintf("/ingredient/atlantic-cod?cell=%d", inland), true)
	if !strings.Contains(body, "Not grown or landed near here") || !strings.Contains(body, "Firm, translucent flesh") {
		t.Fatalf("not-local detail:\n%s", body)
	}
}

func TestIngredientErrors(t *testing.T) {
	hs := newHarness(t)
	if code, body, _ := hs.get("/ingredient/unicorn?cell=1", true); code != 404 || !strings.Contains(body, views.CopyNotFound) {
		t.Fatalf("unknown slug: %d", code)
	}
	for _, c := range []string{"", "cell=abc", "cell=-1", "cell=99999999"} {
		if code, _, _ := hs.get("/ingredient/tomato?"+c, true); code != 400 {
			t.Fatalf("%q: %d", c, code)
		}
	}
	hs.climate.down = true
	if code, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d", lisbonCell), true); code != 503 || !strings.Contains(body, views.CopyLoadError) {
		t.Fatalf("outage: %d", code)
	}
}

func TestIngredientFullPage(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d", lisbonCell), false)
	if !strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, "Tomato") {
		t.Fatal("full page expected")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/web/ -run Ingredient`
Expected: FAIL (the stub returns 404 for everything).

- [ ] **Step 3: Implement**

Create `internal/web/views/ingredient.templ`:

```templ
package views

templ Ingredient(vm IngredientVM) {
	<article class="ingredient">
		<p><a href={ templ.SafeURL(vm.BackURL) } hx-get={ vm.BackURL } hx-target="#results" hx-push-url="true">Back to the list</a></p>
		<h2>{ vm.Name }</h2>
		<p class="category">{ vm.Category }</p>
		<ol class="bar" aria-label="Season by month">
			for _, b := range vm.Bars {
				<li class={ "m", b.Class, templ.KV("now", b.Current) } title={ b.Title }>{ b.Letter }</li>
			}
		</ol>
		if vm.Local {
			<p class="basis">{ vm.Basis }</p>
		} else {
			<p class="basis">Not grown or landed near here, as far as our estimates can tell.</p>
		}
		if vm.EstimateNote != "" {
			<p class="badge">{ vm.EstimateNote }</p>
		}
		if vm.WildMushroom {
			<p class="notice">{ WildMushroomDisclaimer }</p>
		}
		<h3>Choosing</h3>
		<p>{ vm.TipChoose }</p>
		<h3>Storing</h3>
		<p>{ vm.TipStore }</p>
		if vm.Note != "" {
			<p class="note">{ vm.Note }</p>
		}
		if len(vm.Pairings) > 0 {
			<h3>Goes well with</h3>
			<ul class="pairings">
				for _, p := range vm.Pairings {
					<li><a href={ templ.SafeURL(p.URL) } hx-get={ p.URL } hx-target="#results" hx-push-url="true">{ p.Name }</a></li>
				}
			</ul>
		}
	</article>
}
```

Create `internal/web/ingredient.go`:

```go
package web

import (
	"fmt"
	"net/http"
	"strconv"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/season"
	"thymeandplace/internal/web/views"
)

func (s *Server) ingredient(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap := s.d.Snapshot()
	if snap == nil {
		s.render(w, r, http.StatusServiceUnavailable, "Ingredient", "", nil, views.LoadError("", r.URL.RequestURI()))
		return
	}
	ing, ok := snap.Get(r.PathValue("slug"))
	if !ok {
		s.render(w, r, http.StatusNotFound, "Not found", "", nil, views.NotFound())
		return
	}
	q := r.URL.Query()
	cell, err := strconv.Atoi(q.Get("cell"))
	if err != nil || !climate.ValidCellID(cell) {
		s.render(w, r, http.StatusBadRequest, ing.Name, "", nil, views.Unsupported())
		return
	}
	SetCellID(ctx, cell)
	month := s.monthFor(q.Get("date"))
	date := validDate(q.Get("date"))

	normals, err := s.d.Climate.ForCell(ctx, cell)
	if err != nil {
		s.d.Log.Warn("ingredient lookup failed", "err", err, "request_id", RequestIDFrom(ctx), "cell_id", cell)
		s.render(w, r, http.StatusServiceUnavailable, ing.Name, "", nil, views.LoadError("", r.URL.RequestURI()))
		return
	}
	lat, lng := climate.CellCenter(cell)
	eco, err := s.d.Ocean.Nearby(ctx, lat, lng)
	if err != nil {
		s.d.Log.Warn("ingredient lookup failed", "err", err, "request_id", RequestIDFrom(ctx), "cell_id", cell)
		s.render(w, r, http.StatusServiceUnavailable, ing.Name, "", nil, views.LoadError("", r.URL.RequestURI()))
		return
	}
	months, basis, local := season.Year(season.Input{Normals: normals, Ecoregions: eco, Month: month}, ing)

	vm := views.IngredientVM{
		Name: ing.Name, Category: string(ing.Category), Basis: basis, Local: local,
		WildMushroom: ing.Kind == catalog.KindWildMushroom,
		Bars:         views.MonthBars(months, month),
		TipChoose:    ing.TipChoose, TipStore: ing.TipStore, Note: ing.Note,
		BackURL: fmt.Sprintf("/season?lat=%.4f&lng=%.4f", lat, lng),
	}
	if date != "" {
		vm.BackURL += "&date=" + date
	}
	if ing.Kind != catalog.KindSeafood {
		vm.EstimateNote = views.CopyLandEstimate
	}
	for _, p := range ing.Pairings {
		if other, ok := snap.Get(p); ok {
			vm.Pairings = append(vm.Pairings, views.Link{Name: other.Name, URL: ingredientURL(p, cell, date)})
		}
	}
	s.render(w, r, http.StatusOK, ing.Name, "", nil, views.Ingredient(vm))
}
```

- [ ] **Step 4: Run the tests**

Run: `go tool templ generate && go vet ./internal/web/... && go test ./internal/web/... -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/web
git commit -m "feat(web): ingredient detail with 12-month bar, tips, pairings and disclaimers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: `server` binary with live reload of catalog changes

**Files:**
- Create: `cmd/server/main.go`, `cmd/server/app.go`, `cmd/server/app_test.go`

**Interfaces:**
- Consumes: everything above; `db.Migrate`, `db.Open`, `catalog.Holder`, `dataversion.Watcher`, `calibrun.Drift`, `calibrun.Cases`, `calibfixtures.FS`, `climate`/`ocean`/`places` services.
- Produces:
  - `type config struct { DatabaseURL, Addr string; TrustProxy bool }`; `func loadConfig(getenv func(string) string) (config, error)`
  - `type app struct { handler http.Handler; mux *http.ServeMux; pool *pgxpool.Pool; holder *catalog.Holder; watcher *dataversion.Watcher; drift *calibrun.Drift; cases changeset.CasesFunc; climate *climate.Service; ocean *ocean.Service; log *slog.Logger }`
  - `func newApp(ctx context.Context, cfg config, log *slog.Logger) (*app, error)`; `(*app).start(ctx)`; `(*app).close()`
  - `server -healthcheck <url>`: exit 0 on HTTP 200 (for container health checks; the image has no curl)
  - Env: `DATABASE_URL` (required), `ADDR` (default `:8080`), `TRUST_PROXY_HEADERS` (`true` behind Caddy)

- [ ] **Step 1: Write the failing integration test**

Create `cmd/server/app_test.go`:

```go
package main

import (
	"context"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, html.UnescapeString(string(b))
}

func TestAppServesAndReloadsWithoutRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	tx, _ := pool.Begin(ctx)
	if err := catalog.Write(ctx, store.New(tx), catalogtest.SmallCatalog(), nil); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)

	a, err := newApp(ctx, config{DatabaseURL: pool.Config().ConnString(), Addr: ":0"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	a.start(ctx)
	srv := httptest.NewServer(a.handler)
	defer srv.Close()

	if code, _ := get(t, srv.URL+"/readyz"); code != 200 {
		t.Fatalf("readyz = %d", code)
	}
	season := srv.URL + "/season?lat=38.7167&lng=-9.1333&date=2026-10-15"
	code, body := get(t, season)
	for _, want := range []string{"near Lisbon, PT", "Mackerel", "Banana"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("missing %q (%d)\n%s", want, code, body)
		}
	}

	if _, err := pool.Exec(ctx, `UPDATE ingredient SET name = 'Plantain' WHERE slug = 'banana'`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, body := get(t, season); strings.Contains(body, "Plantain") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("catalog change not reflected without restart")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestLoadConfig(t *testing.T) {
	if _, err := loadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("DATABASE_URL is required")
	}
	c, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://x", "TRUST_PROXY_HEADERS": "true"}[k]
	})
	if err != nil || c.Addr != ":8080" || !c.TrustProxy {
		t.Fatalf("%+v %v", c, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/server/`
Expected: FAIL to compile: `undefined: newApp`.

- [ ] **Step 3: Implement the app wiring**

Create `cmd/server/app.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	calibfixtures "thymeandplace/calibration"
	"thymeandplace/internal/calibration"
	"thymeandplace/internal/calibrun"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/dataversion"
	"thymeandplace/internal/db"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/places"
	"thymeandplace/internal/season"
	"thymeandplace/internal/web"
)

type config struct {
	DatabaseURL string
	Addr        string
	TrustProxy  bool
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{DatabaseURL: getenv("DATABASE_URL"), Addr: getenv("ADDR"), TrustProxy: getenv("TRUST_PROXY_HEADERS") == "true"}
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	return c, nil
}

type app struct {
	handler http.Handler
	mux     *http.ServeMux // Plans 6–7 mount /admin/ here
	pool    *pgxpool.Pool
	holder  *catalog.Holder
	watcher *dataversion.Watcher
	drift   *calibrun.Drift
	cases   changeset.CasesFunc
	climate *climate.Service
	ocean   *ocean.Service
	log     *slog.Logger
}

func newApp(ctx context.Context, cfg config, log *slog.Logger) (*app, error) {
	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return nil, err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	a := &app{pool: pool, log: log, climate: climate.NewService(pool), ocean: ocean.NewService(pool)}
	a.holder = catalog.NewHolder(func(ctx context.Context) (*catalog.Snapshot, error) { return catalog.Load(ctx, pool) }, log)
	a.watcher = dataversion.NewWatcher(pool, 5*time.Minute, log, func(ctx context.Context, _ string) {
		_ = a.holder.Reload(ctx) // failures keep the previous snapshot and are logged
	})
	if err := a.watcher.Refresh(ctx); err != nil {
		log.Error("initial data version failed", "err", err)
	}
	_ = a.holder.Reload(ctx)

	fixtures, err := calibration.LoadFixtures(calibfixtures.FS())
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("calibration fixtures: %w", err)
	}
	a.cases = func(ctx context.Context) ([]calibration.Case, error) {
		return calibrun.Cases(ctx, fixtures, a.climate, a.ocean)
	}
	a.drift = &calibrun.Drift{Pool: pool, Snapshot: a.holder.Current, Cases: a.cases, DataVersion: a.watcher.Current, Log: log}

	public := web.New(web.Deps{
		Climate: a.climate, Ocean: a.ocean, Places: places.NewService(pool),
		Snapshot: a.holder.Current, DataVersion: a.watcher.Current, Adjuster: season.NoopAdjuster{},
		Ping: pool.Ping, Log: log, TrustProxy: cfg.TrustProxy,
	})
	a.mux = http.NewServeMux()
	a.mux.Handle("/", public)
	a.handler = web.Common(log)(a.mux)
	return a, nil
}

// start launches background work: the data-version listener, retries of a
// failed initial catalog load, and the daily drift check.
func (a *app) start(ctx context.Context) {
	go a.watcher.Run(ctx)
	go func() {
		for a.holder.Current() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				_ = a.holder.Reload(ctx)
			}
		}
	}()
	go a.drift.Loop(ctx, 24*time.Hour)
}

func (a *app) close() { a.pool.Close() }
```

Create `cmd/server/main.go`:

```go
// Command server serves the public site and /admin.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	healthcheck := flag.String("healthcheck", "", "GET this URL and exit 0 on HTTP 200 (container health checks)")
	flag.Parse()
	if *healthcheck != "" {
		os.Exit(runHealthcheck(*healthcheck))
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	a, err := newApp(ctx, cfg, log)
	if err != nil {
		log.Error("startup", "err", err)
		os.Exit(1)
	}
	defer a.close()
	a.start(ctx)

	srv := &http.Server{
		Addr: cfg.Addr, Handler: a.handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("listening", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}

func runHealthcheck(url string) int {
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Run the tests and a manual smoke run**

Run: `go test ./cmd/server/ -count=1`
Expected: `ok`.

Manual: `docker compose -f deploy/compose.dev.yaml up -d`, then:

```bash
DATABASE_URL='postgres://tp:tp@localhost:55432/thymeandplace?sslmode=disable' go run ./cmd/server
```

Open `http://localhost:8080/`. The home page renders; `/readyz` returns `ready` (an empty catalog still counts as loaded). Stop with Ctrl-C.

- [ ] **Step 5: Commit**

```bash
git add cmd/server
git commit -m "feat(server): wire services, live snapshot reload, drift loop and healthcheck flag

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Plan completion check

- [ ] `go tool templ generate && git diff --exit-code` (generated views are committed and current).
- [ ] `go vet ./... && go test ./... -count=1` passes.
- [ ] Every row of §11.6 has a web test: no match, geolocation hint present, unsupported, inland, harsh, invalid lat/lng (400), DB unavailable (503 + retry), panic (500 panel), rate limited (429 + Retry-After), no snapshot (`/readyz` 503). The ingestion-failure row is covered in Plan 3.
