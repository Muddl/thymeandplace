# Thyme & Place - Admin abuse resistance: client IP, rate limits and audit retention - Design

- **Status:** Proposed
- **Date:** 2026-09-27
- **Source:** Architectural audit, 2026-09-27 (findings F04, F09, F10)
- **Related issues:** [#4](https://github.com/Muddl/thymeandplace/issues/4) (F04), [#9](https://github.com/Muddl/thymeandplace/issues/9) (F09), [#10](https://github.com/Muddl/thymeandplace/issues/10) (F10)
- **Amends:** core spec §10.4 (login rate limit, audit, new admin request limit), §13.4 (auth tests), §14 (client IP rule, automatic pruning), §15 (login log retention), §16 (login rate limit row; new rows for admin request limits and login log retention)

---

## 1. Problem

This spec covers three weaknesses in how `/admin` identifies clients, limits them and keeps their audit rows. Together they let an unauthenticated script grow `admin_login_log` without bound, and they make the per-IP login limit weaker than core spec §10.4 says.

**F04: unauthenticated requests insert unbounded audit rows, and nothing limits `/admin` requests.**

- Refused attempts still write a row. The passkey finish writes `rate_limited` each time `AllowAttempt` says no (`internal/admin/login.go:99-106`), and the backup form does the same (`internal/admin/login.go:165-172`). `LogAttempt` inserts unconditionally (`internal/auth/service.go:83-92`). `CountRecentLoginAttempts` ignores `rate_limited` rows (`internal/store/queries/admin.sql:75-77`), so these rows don't count toward the limit and nothing caps them.
- Two more paths write one row per request with no limit check at all. With `ADMIN_BACKUP_LOGIN=off`, every `POST /admin/login/backup` writes `disabled` before any check (`internal/admin/login.go:157-161`). Every failed `POST /admin/enroll/finish` writes `enroll`/`failure` (`internal/admin/login.go:255-270`). A request with no token or no state cookie fails without touching the database, but the row is still written. Enroll outcomes are left out of the budget on purpose (controller ruling E, `internal/auth/backup_test.go:148-177`).
- CSRF doesn't slow a script down. Before a session exists, `loadSession` accepts any `__Host-tp_pre` cookie of 32 or more characters (`internal/admin/admin.go:138-145`), and `csrf` only checks that the header matches it (`internal/admin/admin.go:198-203`). A script sets both values itself and never needs a GET. This is not a CSRF hole, because browsers enforce the `__Host-` prefix. It just means the token doesn't limit anything.
- Nothing limits request rate on `/admin/*`. The only limiter is built inside `web.New` (`internal/web/server.go:93`) and classifies only `/places`, `/season` and `/ingredient/` (`internal/web/middleware.go:162-171`). The admin handler is mounted next to the public one, not behind it (`cmd/server/app.go:163-164`), and its middleware chain has no limiter (`internal/admin/admin.go:90-99`). Unauthenticated routes hit the database on every request. `POST /admin/login/passkey/begin` loads every passkey (`internal/auth/passkey.go:141-147`, `:79-94`). `GET /admin/enroll` and `POST /admin/enroll/begin` check the token (`internal/admin/login.go:214-245`, `internal/auth/passkey.go:382-387`). Any request carrying a session cookie costs a lookup (`internal/admin/admin.go:120-126`).
- Nothing prunes `admin_login_log`, `admin_session` or `admin_enrollment_token`. `docs/operations.md:202-220` gives only a manual SQL recipe. Sessions are deleted lazily, and only when a client presents one (`internal/auth/session.go:57-59`). The only background work is the watcher and the drift loop (`cmd/server/app.go:179-184`).

**F09: the login limit is keyed by the exact IP, so IPv6 clients get around it.** `AllowAttempt` counts failures where `ip` equals the full address string (`internal/auth/service.go:74-81`, `internal/store/queries/admin.sql:75-77`). The column and index are defined at `migrations/00006_admin.sql:38-45`. The public limiter buckets IPv6 by /64 (`internal/web/middleware.go:138-155`, used at `:209`), since a /64 belongs to one subscriber. An IPv6 client that rotates addresses inside its /64 gets 5 fresh attempts per address every 15 minutes. The global backup lockout still caps password guessing (`internal/auth/backup.go:91-102`), and passkeys can't be brute-forced, so the direct impact is small. The real problem is that the per-IP limit in §10.4 doesn't exist for IPv6 clients.

**F10: the client IP comes from the leftmost `X-Forwarded-For` entry.** With `TRUST_PROXY_HEADERS=true` (`deploy/compose.yaml:34-36`), `ClientIP` returns the first entry of the first header line (`internal/web/middleware.go:123-136`), and a test pins that behaviour (`internal/web/middleware_test.go:86-93`). This is correct today only because Caddy is the first hop and has no `trusted_proxies`. Caddy's `reverse_proxy` docs (checked via Context7) say it ignores incoming `X-Forwarded-*` values by default and "sets or augments" them. When the peer is trusted, it keeps the incoming values. `deploy/Caddyfile:18-21` and `docs/operations.md:186` rely on this. If a CDN is ever put in front of Caddy and `trusted_proxies` is set, the leftmost entry comes from the client. The client can then spoof its IP past the public limiter and the admin login limit.

## 2. Goals and non-goals

Goals:

- An unauthenticated client adds at most a small, fixed number of `admin_login_log` rows per 15 minutes per client key. The client key is the IPv4 address, or the /64 for IPv6.
- Every `/admin` request passes an in-memory per-key request limiter before any database work. The limits are high enough that the owner never hits them.
- The DB-backed attempt budget (5 failures per 15 minutes) is counted per client key. The audit row still stores the full IP.
- The client IP comes from the entry the trusted proxy wrote, never from an entry the client sent. The rule is written down where an operator would change it.
- Admin audit, session and enrollment-token tables are pruned automatically every day.
- The core spec's guarantees stay. Passkey sign-in keeps working during a backup lockout. The budget survives restarts and is shared by replicas (§4.1). Every attempt that reaches a credential check is audited.

Non-goals:

- Volumetric DDoS protection. A flood from many addresses belongs upstream, in a firewall or CDN, not in the app.
- CDN support. This spec documents what must change when a CDN is added but doesn't build it (§8, question 3).
- Changing the backup lockout, session lifetimes or the CSRF model.
- Pruning `pending_changeset`. `changeset.Service` already deletes expired rows on every new preview (`internal/changeset/service.go:61`).

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| Refused attempts in the audit log | At most one `rate_limited` row per client key and budget per 15-minute window, written with a guarded `INSERT ... WHERE NOT EXISTS` | A row per refusal (unbounded). No row at all (the owner loses the record that someone hit the limit). An in-memory "already logged" set (not shared across replicas, lost on restart) |
| Paths that write rows but have no budget | The budget check runs first on every route that writes an audit row: passkey finish (both routes), backup submit (also when disabled) and enroll finish. Enroll gets its own budget of 5 per 15 minutes, so ruling E still holds | Stop auditing `disabled` and enroll failures (loses audit trail, §10.4). Count enroll toward the login budget (reverses ruling E, and failed logins could block the owner's enrollment) |
| Budget check fails (DB error) | Refuse with 503 "Couldn't check sign-in attempts" and write no row | Today's 429 plus a `rate_limited` row (misleading, and the insert fails anyway when the DB is down) |
| Request limiter for `/admin` | In-memory token bucket reusing `web.RateLimiter` and `Middleware`, placed in `admin.New` ahead of every database call, with two classes: sign-in/enroll routes and everything else | No limiter (DB work on every unauthenticated request). DB-backed limits only (a DB round trip per request is the load to avoid). Caddy `rate_limit` (a non-standard module that needs a custom `xcaddy` build, per Caddy docs via Context7, and it would split the limits between two places) |
| Key for the DB budget | New `ip_key text` column, written by the app with the same `LimiterKey` the public limiter uses. `ip` keeps the full address | Compute the /64 in SQL with `inet` casts. One unparseable `ip` value (`ClientIP` can return `RemoteAddr` as-is) would make the cast raise, so every budget check would fail closed. Postgres also keeps IPv4-mapped addresses as IPv6 (they sort with IPv6, per the network-type docs via Context7), so all of them would share `::/64` while Go unmaps them. And it would need an expression index |
| `ip_key` default and backfill | `NOT NULL DEFAULT ''`. Backfill only rows from the last hour | `NOT NULL` with no default (after a rollback, the previous binary's inserts would fail and audit rows would be lost). A full-table backfill (nothing reads `ip_key` outside the 15-minute window, and a table flooded before this ships could be large) |
| Which `X-Forwarded-For` entry | The rightmost entry across all header lines. If it doesn't parse as an IP, fall back to `RemoteAddr` | Leftmost (spoofable once a proxy is trusted). A configurable hop count now (speculative, §8 question 3). `r.Header.Get` (sees only the first header line) |
| Where pruning lives | `auth.Service.Prune` does the deletes. A daily loop in `cmd/server/app.go` `start()`, next to the drift loop, calls it | A host cron running `psql` (one more thing to install, and it can stop silently). pg_cron (not in the `postgis/postgis` image). The loop inside `auth` (a later job runner or heartbeat would then have to reach into `auth`, §4.7) |
| Login log retention | 90 days, the value already in `docs/operations.md` | Forever (grows without bound and keeps IPs, §15). 30 days (too short to look back over a slow attack) |

## 4. Design

### 4.1 Client IP (F10)

`ClientIP(r, trustProxy)` keeps its signature. When `trustProxy` is true:

1. Read every header line with `r.Header.Values("X-Forwarded-For")`, take the last line, and take the text after its last comma.
2. Trim it and parse it with `netip.ParseAddr`. If it parses, return `addr.WithZone("").String()`, the canonical form. If it doesn't parse, or there is no header, fall back to the host from `RemoteAddr` as today. Behind Caddy that is Caddy's own address, so a garbled header puts the client in a bucket shared with everyone else whose header is garbled. That fails closed, not open.

The rule to document: **the app trusts exactly one proxy hop, Caddy.** The rightmost entry is the address Caddy saw as its peer. Today Caddy discards the incoming header and writes that address. If a CDN is added and `trusted_proxies` is set, Caddy keeps the incoming values and adds its own peer (the CDN edge), so the rightmost entry becomes the edge address. The docs say "augment" without naming the position; appending the peer last is the `X-Forwarded-For` convention. Every visitor behind one edge would then share one bucket. That breaks visibly but can't be spoofed, and it forces the §8 question 3 decision before a CDN goes live. The rule goes in the `ClientIP` doc comment, the `deploy/Caddyfile` comment and `docs/operations.md`. The existing rule "never publish port 8080" (`docs/operations.md:186`) is unchanged and still required.

This changes the public limiter's key too, since it calls the same function. Today the leftmost and rightmost entries are the same, because Caddy writes a single entry.

### 4.2 Client key and the DB budget (F09)

- Rename `limiterKey` to the exported `LimiterKey` with no change in behaviour. IPv4 and IPv4-mapped addresses are keyed per address, IPv6 per /64, and unparseable input is returned unchanged. The public limiter keeps using it.
- `internal/auth` gets `type Caller struct{ IP, Key string }`. `admin` replaces `a.ip(r)` (`internal/admin/admin.go:262`) with `a.caller(r)`, which returns `auth.Caller{IP: ip, Key: web.LimiterKey(ip)}`. This keeps `auth` free of any HTTP package.
- Budgets, each `AttemptLimit` (5) failures per `AttemptWindow` (15 min) per key:

  | Budget | Methods counted | Charged by |
  |---|---|---|
  | login | `passkey`, `backup` | `/admin/login/passkey/finish`, `/admin/security/reauth/finish`, `/admin/login/backup` |
  | enroll | `enroll` | `/admin/enroll/finish` |

  A failure is any outcome except `success` and `rate_limited`, as today. That means `failure`, `locked`, `error` and `disabled` all count.
- Migration `migrations/00007_admin_login_log_ip_key.sql` (the next free number at HEAD; renumber if another spec lands first). Up: `ALTER TABLE admin_login_log ADD COLUMN ip_key text NOT NULL DEFAULT ''`, then `UPDATE admin_login_log SET ip_key = ip WHERE at > now() - interval '1 hour'`, then `CREATE INDEX admin_login_log_ip_key_at ON admin_login_log (ip_key, at DESC)` and `CREATE INDEX admin_login_log_at ON admin_login_log (at)`. The second index serves the prune and the growth count. Down drops both indexes and the column. Postgres adds a column with a non-volatile default without rewriting the table (ALTER TABLE notes, via Context7). The old `(ip, at DESC)` index stays for owner forensics and so a rolled-back binary still has an index for its query. Backfilled IPv6 rows keep a full-address key, which matters only in the 15 minutes around the deploy.
- Queries in `internal/store/queries/admin.sql`:
  - `InsertLoginAttempt` also writes `ip_key`.
  - `CountRecentLoginAttempts` becomes `WHERE ip_key = @ip_key AND at > @since AND method = ANY(@methods::text[]) AND outcome NOT IN ('success', 'rate_limited')`. sqlc maps an `ANY($1::type[])` parameter to a Go slice (sqlc docs via Context7).
  - New `InsertRefusalOnce :execrows`: `INSERT INTO admin_login_log (at, ip, ip_key, method, outcome) SELECT @at::timestamptz, @ip::text, @ip_key::text, @method::text, 'rate_limited' WHERE NOT EXISTS (SELECT 1 FROM admin_login_log WHERE ip_key = @ip_key AND method = ANY(@methods::text[]) AND outcome = 'rate_limited' AND at > @since)`. The casts set the parameter types, which sqlc documents for named parameters. That sqlc needs them in this exact `SELECT` list was not run here; `./scripts/sqlc.sh generate` will show it.
- `auth.Service` API: `AllowAttempt(ctx, c Caller, method string) (bool, error)`, `LogAttempt(ctx, c Caller, method, outcome string)` and a new `LogRefusal(ctx, c Caller, method string)`. The method picks the budget. `LogRefusal` logs `WARN "admin sign-in budget reached"` with `ip` and `method`, but only when its insert affected a row. That gives at most one line per key and budget per window.

### 4.3 Bounded audit rows (F04)

New helper in `internal/admin/login.go`:

```go
// admit applies method's attempt budget before any audited work. Over budget
// it records one refusal per key per window and calls refuse(429, ...). If
// the check itself fails it calls refuse(503, ...) and writes nothing. It
// reports whether the caller may go on.
func (a *Admin) admit(ctx context.Context, c auth.Caller, method string, refuse func(int, string)) bool
```

- `finishPasskeyLogin` calls `admit(ctx, c, "passkey", jsonErrorTo(w))` in place of lines 99-106. Both passkey finish routes share it, as today.
- `backupSubmit` calls `admit` **before** the disabled check. While backup is disabled, `refuse` is `http.NotFound`, so the route stays indistinguishable from a missing one. Otherwise it renders the backup page with the status. A disabled attempt still writes `disabled`, which counts as a failure, so at most 5 per window.
- `enrollFinish` calls `admit(ctx, c, "enroll", ...)` before `limitCeremonyBody`.
- Bound: for sequential requests, a key adds at most 5 failures and 1 refusal per budget per 15 minutes, so 12 rows across both budgets. Under Read Committed each statement sees only rows committed before it started (Postgres docs via Context7). So concurrent requests can all pass the count, or all pass the `NOT EXISTS`, before any of them commits. The in-memory burst (§4.4) caps how many can run together, so the worst case is about 20 rows per budget per window. That is still bounded, and a per-key advisory lock isn't worth it here.
- The 429 message is unchanged: "Too many sign-in attempts. Wait 15 minutes and try again."

### 4.4 Request limiter on `/admin` (F04)

`admin.New` builds its own limiter. It reuses `web.RateLimiter.Middleware(trustProxy, adminLimits, tooMany)`, whose key is already `class|LimiterKey(ClientIP)`. The limiter needs a smaller LRU than the public 100,000 keys, so the existing `newRateLimiterWithCapacity` is exported as `NewRateLimiterSize(cap, now)`, used with 10,000. The limiter wraps everything inside `securityHeaders` (so a 429 still carries the admin headers) and sits outside `MaxBytesHandler`, `Timeout`, `loadSession` and `csrf`. No body is read and no database call is made before it runs.

| Class | Paths | Rate | Burst |
|---|---|---|---|
| `admin-signin` | `/admin/login`, `/admin/login/*`, `/admin/enroll`, `/admin/enroll/*` | 1/s | 20 |
| `admin` | every other `/admin` path | 10/s | 100 |

The owner's real traffic sets these sizes. A sign-in is 3 requests, and enrolling two devices is 6. Static assets are served by the public handler under `/static/`. Catalog search fires at most once per 250 ms while typing (`internal/admin/views/catalog.templ:9`). The Playwright suite runs one worker, and all its requests arrive through Caddy from one address. Over the limit, the response is `429`, `Retry-After: 1`, and a plain-text body: "Too many requests. Wait a few seconds and try again." The ceremony script already shows a non-JSON error body (`static/admin.js:22-28`), and the admin HTMX config swaps 4xx responses (`internal/admin/views/models.go:9`). No audit row is written.

`Deps` gains two unexported fields: `now func() time.Time` for the limiter clock, and `limits func(string) (string, web.Limit, bool)`, where nil means `adminLimits`. This follows `web.Deps.timeout` (`internal/web/server.go:53`), so tests can pin the clock or switch the limiter off.

This limiter is defence in depth and works per process. The authoritative sign-in limit stays the DB budget, which survives restarts and is shared by replicas.

### 4.5 Daily prune (F04)

New `internal/auth/prune.go`:

- `const LoginLogRetention = 90 * 24 * time.Hour`.
- `(*Service).Prune(ctx) (PruneResult, error)` runs three new `:execrows` queries with the service clock:
  - `admin_login_log`: delete rows with `at < now - LoginLogRetention`.
  - `admin_session`: delete rows with `last_seen_at < now - IdleTimeout OR created_at < now - AbsoluteTimeout`. These are the same strict comparisons as `LookupSession` (`internal/auth/session.go:57`), so the prune never removes a session the app would still accept.
  - `admin_enrollment_token`: delete rows with `used_at IS NOT NULL OR expires_at <= now`. That is exactly the complement of `EnrollmentTokenValid`.
- After deleting, it counts `admin_login_log` rows with `at > now - 24h`. Above `loginLogGrowthWarn` (10,000), it logs `WARN "admin login log growing fast"` with the count. This is how the owner learns about a flood from many addresses, which §4.3 bounds per key but not in total.
- `cmd/server/app.go` gets `func (a *app) pruneLoop(ctx, every)`, which has the same shape as `calibrun.Drift.Loop`. It runs once right away, then on a ticker. It logs `INFO "admin prune"` with the three deleted counts, and `ERROR "admin prune failed"` unless the context is done. `start()` adds `if a.auth != nil { go a.pruneLoop(ctx, 24*time.Hour) }`. If the database is down at boot, the first run fails and the next one comes a day later. Nothing depends on prompt pruning. Two replicas pruning at once is harmless because the deletes are idempotent.

### 4.6 Core spec amendments

- §10.4 **Login rate limit**: "5 failed attempts per 15 minutes per client (an IPv4 address or an IPv6 /64), across the passkey, re-auth and backup sign-in routes. Enrollment has its own budget of 5 failed attempts. Refused attempts are recorded once per client per window." **Audit**: "(time, IP, client key, method, outcome), kept 90 days." New bullet, **Admin request limit**: "per client, in memory, before any database work: sign-in and enrollment routes 1 req/s burst 20; other `/admin` routes 10 req/s burst 100; 429 with `Retry-After`."
- §13.4: add "the per-client budget buckets IPv6 by /64; refused attempts write at most one row per window; the request limiter answers before any database call; pruning removes only expired rows."
- §14: add "The app trusts exactly one proxy hop (Caddy) and uses the rightmost `X-Forwarded-For` entry. Putting any proxy in front of Caddy requires changing that first." Also add "The app prunes admin audit, session and enrollment-token rows daily."
- §15: "Admin login logs store IP addresses for security auditing, for 90 days."
- §16: the "Login rate limit" row becomes "5 failures / 15 min / client (IPv4 or IPv6 /64); enrollment separate". New rows: "Admin request limits: sign-in/enroll 1/s burst 20; other 10/s burst 100" and "Admin login log retention: 90 days".

### 4.7 Placement relative to other audit specs

- S10 may move `ClientIP`, `LimiterKey` and `RateLimiter` to `internal/httpx`. If it lands first, these changes go there. Nothing here depends on the package name.
- S6/F23 may change how `start()` runs background jobs. `pruneLoop` needs only a context and a period, so it becomes one job in any runner.
- The observability spec (`2026-09-27-observability-and-alerting-design.md` §4.8) adds no separate check for the in-app prune. A failed prune logs `ERROR "admin prune failed"`, which fails its `app` check. The `WARN "admin login log growing fast"` also calls its `auth.Config.OnEvent("login_log_growth")` hook, which fails its `admin-auth` check. That spec also hooks `LogAttempt` for security events. The `Caller` parameter doesn't affect that hook.
- The least-privilege spec (`2026-09-27-least-privilege-deployment-design.md` §4.3) removes the app's `DELETE` privilege on `admin_login_log`. Once it lands, `PruneLoginLog` becomes `SELECT prune_admin_login_log(@retention::interval)`, a `SECURITY DEFINER` function that refuses any retention under 30 days. Until then it is a plain `DELETE`. The session and enrollment-token prunes keep plain `DELETE`s, which the app role is still granted.

## 5. Implementation outline

1. `internal/web/middleware.go`: rightmost-entry `ClientIP` with the one-hop doc comment. Export `LimiterKey` and `NewRateLimiterSize`. Update `internal/web/middleware_test.go`.
2. `migrations/00007_admin_login_log_ip_key.sql` as in §4.2.
3. `internal/store/queries/admin.sql`: change `InsertLoginAttempt` and `CountRecentLoginAttempts`. Add `InsertRefusalOnce`, `PruneLoginLog`, `PruneSessions`, `PruneEnrollmentTokens` and `CountLoginLogSince`. Regenerate with `./scripts/sqlc.sh generate`; CI runs `git diff --exit-code` on the output (`.github/workflows/ci.yml:25-27`).
4. `internal/auth/service.go`: `Caller`, the budget table, and the new `AllowAttempt`, `LogAttempt` and `LogRefusal`. Update `internal/auth/backup_test.go`.
5. `internal/auth/prune.go` and `internal/auth/prune_test.go`.
6. `internal/admin/login.go`: `admit` and the three call sites. `internal/admin/admin.go`: `caller`, `adminLimits`, the limiter in `New`, `tooMany`, and the `Deps.now` and `Deps.limits` fields. `internal/admin/testclient_test.go`: `newEnv` switches the limiter off, and a new `newLimitedEnv(t, pool)` turns it on with the test clock.
7. `cmd/server/app.go`: add `pruneLoop` and start it in `start()`.
8. Docs and config: the `deploy/Caddyfile` comment, `docs/operations.md` (§7), and `internal/web/views/about.templ:25`, which gains "for 90 days" (then regenerate templ). Apply the core spec amendments from §4.6.

## 6. Testing

- **`ClientIP`, table-driven, in `internal/web/middleware_test.go`:**

  | Input | Result |
  |---|---|
  | `trustProxy=false` | Ignores the header |
  | `"198.51.100.1, 203.0.113.9"` | `203.0.113.9` |
  | Two header lines | Last entry of the last line |
  | `"198.51.100.1, garbage"` | `RemoteAddr` host |
  | No header | `RemoteAddr` host |
  | `"2001:DB8::1"` | `2001:db8::1` |

  The leftmost expectation in `TestClientIP` flips.
- **`LimiterKey`, table-driven:** IPv4 maps to itself. `::ffff:9.9.9.9` maps to `9.9.9.9`. Two addresses in one /64 share a key, and a different /64 gets a different key. Garbage comes back unchanged. The existing `TestRateLimitIPv6Prefix` passes unchanged.
- **Budget by key (`internal/auth`, testcontainers via `dbtest`, fixed clock):** Five failures from `Caller{IP: "2001:db8:1:2::1", Key: "2001:db8:1:2::/64"}` refuse a caller with the same key and a different IP, but not a caller with key `2001:db8:1:3::/64`. Stored rows keep their full `ip`. Enroll failures leave the login budget alone and login failures leave the enroll budget alone, so ruling E still holds. `TestAttemptRateLimit` and `TestAttemptRateLimitCountsOnlyLoginFailures` move to the new signatures. `LogRefusal` called 50 times in one window inserts 1 row and one `WARN`, and inserts a second row after the clock moves past the window. An insert that omits `ip_key` still succeeds, which covers the rollback case.
- **Bounded rows over HTTP (`internal/admin`, `newEnv` with the limiter off):** Send 30 attempts each at backup submit with backup enabled, backup submit with backup disabled, passkey finish and enroll finish, all from one client. Assert at most 6 rows per budget for that `ip_key`, and a 404 on every disabled-backup response. Clients rotating through `[2001:db8:1:2::N]` get a 429 on the 6th attempt. With `newBrokenEnv`, the three routes answer 503, never 429. The existing `TestLoginRateLimitPerIP` and `TestReauthFinishIsRateLimitedAndAudited` pass unchanged. So does `TestEnrollFinishBindsToken`, whose 4 enroll failures stay under the enroll budget.
- **Request limiter (`internal/admin`, `newLimitedEnv`):**
  - 21 rapid `GET /admin/login` requests: the 21st gets 429 with `Retry-After` and `Content-Security-Policy`.
  - Another IPv4 address and another /64 are unaffected, while the same /64 shares the bucket. After the clock advances 1 s, a request goes through.
  - On a broken pool with a session cookie, the first 100 `GET /admin` requests get the 503 from `loadSession` and the 101st gets 429. This proves the limiter answers before any database call.
  - A signed-in client making 40 catalog searches with the clock advanced 250 ms each never sees a 429.
- **Prune (`internal/auth/prune_test.go`):**
  - Of two log rows at 91 and 89 days old, only the older one is deleted.
  - Of an idle session, an over-age session and a live one, only the live one remains, and `LookupSession` still accepts it.
  - Of a used token, an expired token and a live one, only the live one remains.
  - The growth `WARN` appears above a threshold lowered for the test, and not below it.
- **App wiring (`cmd/server/app_test.go`):** With `ADMIN_SECRET_KEY` set, an expired session row inserted before `start()` is gone shortly after it, and cancelling the context stops the loop.
- **E2E:** no new scenario. The existing Playwright admin flows must pass unchanged, which confirms the limits don't touch the owner.

## 7. Rollout, operations and rollback

- **Ship:** one release. Migration 00007 runs at startup before the app listens (§14). It is backward-compatible: the previous binary's inserts get `ip_key = ''`, and its `ip`-keyed query still has its index.
- **`docs/operations.md`:**
  - Replace "Periodic prune of admin audit/session tables" with "Admin table pruning". It covers the daily in-app prune, the 90-day retention, the three rules, and the `INFO "admin prune"`, `ERROR "admin prune failed"` and `WARN "admin login log growing fast"` lines. The existing SQL stays as a manual fallback for when the app is down.
  - Extend "Reverse proxy trust and logging" with the one-hop rule from §4.1: the app uses the rightmost `X-Forwarded-For` entry, and before any CDN or proxy goes in front of Caddy, `ClientIP` must change (§8 question 3).
  - Under "Admin access management", add the request limits and the per-client budget. If the owner locks themselves out, they wait 15 minutes or sign in from another network. `WARN "admin sign-in budget reached"` names the IP.
- **After deploy:** `docker compose logs app | grep "admin prune"` shows one line from startup. `SELECT ip, ip_key FROM admin_login_log ORDER BY id DESC LIMIT 5` shows `ip_key` filled in on new rows.
- **Rollback:** `git checkout <previous> && docker compose up -d --build app`. Leave the column and indexes in place: the old binary ignores them and the next release re-uses them. Rows the old binary writes have an empty `ip_key` and age out of the 15-minute window. Pruning stops until roll-forward, so run the manual SQL if the rollback lasts long.

## 8. Open questions for the owner

1. **Login log retention.** Recommended: 90 days, which matches today's manual recipe. Backups keep pruned rows for up to 4 more weeks (7 daily, 4 weekly copies).
2. **Admin request limits and growth warning.** Recommended: sign-in/enroll 1/s burst 20, other `/admin` 10/s burst 100, and a warning at 10,000 login-log rows in 24 hours. These go into §16 as confirm-or-change values.
3. **CDN readiness.** Two options. (a) The app gains a trusted-hop setting such as `TRUST_PROXY_HOPS`. (b) Caddy sends `header_up X-Client-IP {client_ip}`, the app reads only that header, and trust moves to Caddy's `trusted_proxies` with `trusted_proxies_strict`. Caddy docs (via Context7) say `header_up` overwrites existing values, `{client_ip}` expands to `{http.vars.client_ip}`, and a request not from a trusted proxy uses the direct remote IP. Recommended: neither for now. Document the one-hop rule and pick (b) when a CDN is actually chosen, since it keeps every trust decision in one Caddy config.
4. **A hard global cap on audit writes during a distributed flood.** Recommended: no. A global cap would either leave gaps in the audit or refuse the owner's passkey sign-in along with everyone else's. The growth warning plus an upstream firewall is the proportionate response.
