# Thyme & Place - Shared readiness ping - Design

- **Status:** Proposed
- **Date:** 2026-09-28
- **Source:** Architectural audit, 2026-09-27 (finding F13)
- **Related issues:** [#13](https://github.com/Muddl/thymeandplace/issues/13) (F13)
- **Amends:** core spec §11.1 (the `/readyz` row), §11.6 (the "No catalog snapshot loaded" row), §16 (new value: readiness ping reuse)

---

## 1. Problem

`GET /readyz` is public and has no rate limit. Caddy proxies every path to the app, and the public limiter only classifies `/places`, `/season` and `/ingredient/*` (`internal/web/middleware.go`, `PublicLimits`). Every request runs `s.d.Ping`, which is `pool.Ping` (`cmd/server/app.go`). `pool.Ping` acquires a pooled connection and sends an empty statement (pgx docs, checked via Context7). A client looping on `/readyz` therefore competes with real requests for connections, one acquisition and one round trip per request. During a database outage, every probe also tries to open a new connection to the recovering database.

Nothing in the deploy needs a fresh ping on every request. Docker and Caddy probe `/healthz` (`deploy/compose.yaml`, `deploy/Caddyfile`). The callers of `/readyz` are:

- the runbook (`docs/operations.md`, "Health and logs")
- `deploy/smoke.sh`
- the Playwright `waitReady` helper (`e2e/helpers.ts`)
- the external uptime monitor planned by the observability design (`2026-09-27-observability-and-alerting-design.md` §4.6)

All of them poll at human or monitor speed.

What the endpoint reveals is negligible. `catalog not loaded` is unreachable over HTTP in production: `main` doesn't listen until a snapshot loads, and the holder never drops one. `database unavailable` says no more than the public 503 retry panel already does.

The core spec says `/readyz` gates traffic through Compose (§11.1, §14). The deploy doesn't do that: only `/healthz` is probed.

## 2. Goals and non-goals

Goals:

1. However many clients call `/readyz`, the app pings the database at most once per second, and at most one ping is in flight at a time.
2. `/readyz` stays public, with the same status codes and bodies: `ready`, `database unavailable`, `catalog not loaded`.
3. A client that disconnects mid-request can't make later requests report the database as down.
4. Tests stay deterministic: expiry uses the server clock (`Deps.Now`).

Non-goals:

- Restricting `/readyz` to internal callers. That would break the smoke test, e2e and the runbook. The observability design §4.6 already names the fallback, probing `/` for `Find your town`, if the owner later wants the route hidden.
- Rate-limiting `/readyz` per IP. It doesn't bound the total number of pings across many IPs; the shared result does.
- Changing `/healthz`, or the in-process watchdog in the observability design. That watchdog samples once a minute and calls `pool.Ping` directly.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| How pings are shared | A mutex-guarded `{at, err}` on `Server`. The ping runs **while the lock is held**, so concurrent callers wait for it and reuse its result | `golang.org/x/sync/singleflight` (a new dependency for the same effect). A background ticker (pings even when nobody asks, and needs lifecycle wiring) |
| How long a result is reused | 1 s, for success and failure alike | Longer (a monitor polling every 5 min doesn't care, but the smoke test and e2e poll every second or so and would wait longer). Caching only successes (a failing database would get a ping per request, the load this design removes) |
| The ping's context | `context.WithTimeout(context.WithoutCancel(r.Context()), 500*time.Millisecond)` | The request context (a client that disconnects would cache `context.Canceled` as "database unavailable" for everyone) |
| Clock | `s.d.Now` | `time.Now` (tests couldn't step past the reuse window) |
| Bodies | Unchanged. No body ever contains `not ready`, because the uptime monitor matches the keyword `ready` | Richer bodies (nothing reads them) |

## 4. Design

### 4.1 `internal/web/readiness.go`

```go
// readyzPingTTL is how long one database ping answers /readyz for everyone.
const readyzPingTTL = time.Second

// pingCache shares one database ping across /readyz requests.
type pingCache struct {
	mu  sync.Mutex
	at  time.Time // when err was measured; zero → never
	err error
}

// sharedPing returns the last ping result if it is under readyzPingTTL old
// by the server clock. Otherwise it pings once, while holding the lock, so
// concurrent callers wait for that ping and reuse its result.
func (s *Server) sharedPing(ctx context.Context) error
```

- `Server` gains a field `ready pingCache`.
- Inside `sharedPing`, the ping runs on `context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)`.
- The cache is valid when `!at.IsZero() && age >= 0 && age < readyzPingTTL`, where `age = now.Sub(at)`. If the clock moves backwards, the age is negative, so the result counts as expired and the next request pings. Otherwise a backward jump could pin one stale result for as long as the jump.

### 4.2 `readyz`

The handler checks the snapshot first. That check is free and never cached. It then calls `s.sharedPing(r.Context())`. Status codes and bodies are unchanged.

If the observability plan has landed, `readyz` calls `CheckReady(r.Context(), s.d.Snapshot, s.sharedPing)`. `CheckReady` adds its own 500 ms timeout to the context it passes in. `sharedPing` detaches from that context's cancellation, so the only timeout that bounds the ping is `sharedPing`'s own 500 ms. The observability watchdog keeps calling `pool.Ping` through its own `CheckReady` call, uncached.

### 4.3 Core spec text

| § | Change |
|---|---|
| §11.1 | `/readyz` row: "…catalog loaded and a database ping succeeded within the last second (one shared ping, reused for 1 s)" |
| §11.6 | "No catalog snapshot loaded" row: "The server doesn't listen until a snapshot loads, so Caddy serves the maintenance page" |
| §16 | New row: "Readiness ping reuse: 1 s" |

## 5. Implementation outline

1. `internal/web/readiness.go`: `pingCache`, `readyzPingTTL`, `sharedPing`.
2. `internal/web/server.go`: the `ready` field, and `readyz` calls `sharedPing`.
3. `internal/web/fakes_test.go`: the harness counts `Ping` calls, and can block a ping until released.
4. Tests (§6).
5. Docs: `docs/operations.md` "Health and logs", and the core spec (§4.3).

## 6. Testing

In `internal/web/server_test.go`, with the existing harness:

- `TestReadyzSharesPing`: 50 concurrent `GET /readyz` at one clock instant all return 200 `ready`, with exactly one `Ping` call. To prove they overlap, the first ping blocks until all 50 requests have started.
- After the harness clock moves forward 1 s, the next request pings again.
- A failing ping returns 503 `database unavailable`. Clearing the error at the same instant still returns 503. After the clock moves forward 1 s, the response is 200.
- A request whose context is already cancelled doesn't make the next request return 503.
- With no snapshot, the response is 503 `catalog not loaded`, and `Ping` isn't called.
- `TestHealthAndReady` moves the clock forward between cases and passes.
- `deploy/smoke.sh` and the Playwright `waitReady` helper pass unchanged.

## 7. Rollout, operations and rollback

- Code only: no migration or config change.
- `docs/operations.md` "Health and logs" says the database part of `/readyz` can be up to 1 s old.
- Rollback: redeploy the previous image.

## 8. Open questions for the owner

None. The issue's proposed fix was accepted as written.
