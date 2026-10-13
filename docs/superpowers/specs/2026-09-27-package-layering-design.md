# Thyme & Place - Package layering and shared infrastructure - Design

- **Status:** Step 1 implemented (F18). Steps 2–5 still proposed.
- **Date:** 2026-09-27
- **Source:** Architectural audit, 2026-09-27 (findings F16, F17, F18, F19, F20)
- **Related issues:** [#16](https://github.com/Muddl/thymeandplace/issues/16) (F16), [#17](https://github.com/Muddl/thymeandplace/issues/17) (F17), [#18](https://github.com/Muddl/thymeandplace/issues/18) (F18), [#19](https://github.com/Muddl/thymeandplace/issues/19) (F19), [#20](https://github.com/Muddl/thymeandplace/issues/20) (F20)
- **Amends:** core spec §4.2 (repository layout: `internal/httpx`, `internal/config`), §4.3 (package responsibilities and the dependency rule that replaces "store exposes narrow interfaces")

---

## 1. Problem

The package structure mostly matches core spec §4.3, but five seams have drifted. Each one alone is small. Together they make the admin side slow to test and easy to couple further.

- **F16: shared HTTP plumbing lives in the public package.**
  - `internal/web/middleware.go` holds the cross-cutting middleware (request ID, access log, panic recovery, client IP, rate limiter, timeout) next to public-only concerns such as `PublicLimits`.
  - `internal/admin` imports `internal/web` to reuse it: `admin.go:19` (import), `:93` (`web.Timeout`), `:96` (`web.Recover`), `:123` (`web.RequestIDFrom`), `:262` (`web.ClientIP`). `login.go` and `security.go` import it for `RequestIDFrom` as well.
  - So the admin area depends on the public handler package.
- **F17: two dependency styles, and admin skips the service layer.**
  - `internal/web` depends on small interfaces and function fields (`internal/web/server.go:26-54`). Its tests run without a database in under a second.
  - `internal/admin` takes concrete services and a raw pool (`admin.go:28-35`: `Auth *auth.Service`, `Changes *changeset.Service`, `Pool *pgxpool.Pool`).
  - It calls `store` directly for two queries: `store.New(a.d.Pool).ListHistory` (`importexport.go:96`) and `store.New(a.d.Pool).ChangeApplied` (`preview.go:132`).
  - Every admin handler test therefore needs Postgres through testcontainers. The package takes about 55 s locally, against about 0.6 s for `web`.
  - Core spec §4.3 says `store` "exposes narrow interfaces", which it doesn't: it is sqlc query code called by services, and in these two places by a handler.
- **F18: the drift check depends on the admin-edit package.** `internal/calibrun/drift.go:14` imports `internal/changeset` only for `NeedsOverride` (`:50`). The 0.02 threshold (`internal/changeset/preview.go:9-16`) is a calibration policy that both packages use.
- **F19: configuration is parsed twice.** `cmd/server/app.go:43-91` (`loadConfig`) and `cmd/admin/main.go:51-70` each parse `ADMIN_SECRET_KEY` and `DATABASE_URL`. Each defaults `ADMIN_RP_ID` to `thymeand.place` and derives `ADMIN_ORIGIN` with `TrimRight("/")`. If one copy changes and the other doesn't, enrollment links or relying-party settings disagree with the server.
- **F20: the asset version is a hidden global.**
  - `web.New` sets the package variable `views.AssetVersion` through a `sync.Once` (`internal/web/server.go:76`, `:111`; `internal/web/views/models.go:33-43`).
  - The admin layout reads it through `webviews.Asset` (`internal/admin/views/layout.templ:15-17`).
  - So admin pages depend on `web.New` having run first, which is construction order in `cmd/server/app.go:140-164`. A test that builds only the admin handler gets unversioned asset URLs.

## 2. Goals and non-goals

Goals:

1. `internal/admin` doesn't import `internal/web` or `internal/store`, and holds no pool.
2. Admin catalog, preview, import, export and history handlers can be tested with fakes and no database. The auth and changeset services keep their integration tests.
3. The drift check doesn't depend on `changeset`.
4. One loader parses the settings that more than one binary shares.
5. No package variable is set at run time. The asset version is available to both layouts without any construction order.
6. A test enforces the dependency rules, so they don't drift again.
7. Every step ships on its own, keeps all tests green, and changes no behaviour.

Non-goals:

- Changing `auth.Service` or `changeset.Service` to take interfaces instead of a pool. They own transactions, and their correctness depends on real Postgres behaviour (locks, triggers, constraints). Integration tests are the right tool for them.
- Splitting the module or renaming existing packages other than those listed.
- Behaviour changes of any kind.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| Home for shared middleware | New `internal/httpx`. `web` keeps `PublicLimits`, handlers and views | Leave it in `web` (admin keeps depending on the public package). Put it in `admin` (the same problem in reverse) |
| Admin's dependency on auth | A consumer-side `admin.AuthService` interface listing the methods admin calls. `*auth.Service` satisfies it. Test fakes embed the interface and override only what a test needs | Keep the concrete type (every catalog handler test needs a database just to fake a session). Many tiny interfaces (more noise than value for one consumer) |
| History and "already applied?" | New `changeset.Service.History` and `Applied` methods, and an `admin.Changes` interface | A new `history` package (a single query doesn't justify it) |
| Override threshold | `calibration.OverrideThreshold` and `calibration.ScoreDropped(before, after)`. `changeset` and `calibrun` both use them | Keep it in `changeset` (the coupling). Duplicate the constant (two places to change) |
| Config | `internal/config` with typed loaders for the shared groups (database, admin/WebAuthn). Server-only settings stay in `cmd/server` | One struct for every binary (each binary would carry settings it doesn't use, and the test-only `TP_PUBLIC_TODAY` guard would leak into CLIs) |
| Asset version | `static.Version()` built once with `sync.OnceValue` over the embedded files, and `static.Asset(name)`. Both layouts call it | Set it at `init` (fine too, but hashing all assets at every `import` affects every test binary). Inject it through view models (every page's view model gains a field) |
| Enforcing the rules | `internal/archtest`: a test that runs `go list -deps` for chosen packages and fails on forbidden imports | A linter rule (depguard needs golangci-lint, which the supply-chain design doesn't adopt). Code review alone (the problem being fixed) |

## 4. Design

### 4.1 Dependency rules

Recorded in core spec §4.3 and enforced by `internal/archtest`:

1. `season` and `calibration` import no I/O packages (`store`, `db`, `pgx`), as core spec §4.3 already requires.
2. Handlers (`web`, `admin`) import services and `httpx`. They don't import `store`, `db` or `pgx`.
3. `admin` doesn't import `web`, and `web` doesn't import `admin`.
4. `calibrun` doesn't import `changeset`.
5. Services (`auth`, `changeset`, `climate`, `ocean`, `places`, `catalog`) own their SQL and transactions through `store`.

`archtest` runs `go list -deps -f '{{.ImportPath}}' <pkg>` for each rule's subject and checks the output. It needs only the Go toolchain, which CI has.

### 4.2 Steps

Each step is one pull request. Later audit specs change some of the same files, so the order matters (§4.3).

**Step 1: override threshold (F18).**
- Move `OverrideThreshold` and `NeedsOverride` (`internal/changeset/preview.go:9-16`) to `internal/calibration` as `OverrideThreshold` and `ScoreDropped(before, after float64) bool`, with the same epsilon.
- `changeset` (preview and `Apply`) and `calibrun.Drift.RunOnce` call it.
- Existing tests move with it.

**Step 2: asset version (F20).**
- `static.Version()` wraps `sync.OnceValue(func() string { return hashFS(FS) })`. `hashFS` moves from `internal/web/server.go:113-133`.
- `static.Asset(name string) string`.
- `internal/web/views.Asset` delegates to it, so templates don't change. The admin layout switches from `webviews.Asset` to `static.Asset`.
- Delete `views.AssetVersion` and `assetVersionOnce`.

**Step 3: shared config (F19).** New `internal/config`:

```go
type Database struct{ URL string; MaxConns int32 } // MaxConns from DB_MAX_CONNS, 0 = caller default (S1)
type Admin struct {
	SecretKey     []byte // nil when ADMIN_SECRET_KEY is unset
	RPID, Origin  string // defaults: thymeand.place, https://<RPID>, no trailing "/"
	PublicBaseURL string // default: Origin
	BackupEnabled bool   // ADMIN_BACKUP_LOGIN != "off"
}
func LoadDatabase(getenv func(string) string) (Database, error) // DATABASE_URL required
func LoadAdmin(getenv func(string) string) (Admin, error)       // parses the key when set
```

- `cmd/server/loadConfig` composes these and keeps `ADDR`, `TRUST_PROXY_HEADERS` and the `TP_PUBLIC_TODAY` guard, which needs the raw `ADMIN_RP_ID` and is exposed as `Admin.RPIDSet`.
- `cmd/admin` uses the same loaders and requires `SecretKey`.
- `cmd/ingest` and `cmd/devseed` use `LoadDatabase`.
- Settings added by other specs are read here: S1's `DB_MAX_CONNS`, and S2's migrate URL if it lands before this step.

**Step 4: `internal/httpx` (F16).**
- Move from `internal/web/middleware.go`: `RequestID`, `RequestIDFrom`, `SetCellID` and the request-info context, `AccessLog`, `Recover`, `ClientIP`, `LimiterKey`, `Limit`, `RateLimiter` and its constructors, and `Timeout`.
- Also move `SecurityHeaders` (added by S3) and `Common` (`internal/web/server.go:102-109`).
- `web` keeps `PublicLimits`, the handlers and `tooMany`.
- `cmd/server` calls `httpx.Common`, and `admin` imports `httpx`.
- The tests in `internal/web/middleware_test.go` move to `internal/httpx`.

**Step 5: admin service boundaries (F17).**
- `changeset.Service` gains `History(ctx, HistoryQuery) (HistoryPage, error)`, wrapping `ListHistory` with its page-size-plus-one logic (`importexport.go:96-108`), and `Applied(ctx, id int64) (bool, error)`, wrapping `ChangeApplied`.
- `admin` defines:
  - `Changes` (`Preview`, `Reopen`, `Apply`, `Applied`, `History`)
  - `AuthService` (the methods `admin` calls on `*auth.Service` today)
- `Deps` becomes `{Auth AuthService; Changes Changes; Load func(ctx) (*catalog.Snapshot, error); Log; TrustProxy}`, with no `Pool`.
- New fast tests in `internal/admin` use `fakeAuth{AuthService; sess auth.Session}`, which overrides `LookupSession`, `Now` and `BackupEnabled`, plus a `fakeChanges` and a fixed snapshot. They cover the catalog list, form validation, preview rendering, apply error mapping (stale, not found, override, confirm), import errors, export and history paging.
- The existing integration tests stay for login, enrollment, security and the full apply flow.
- Add `internal/archtest` with the rules from §4.1.

### 4.3 Order relative to other audit specs

S3 (public-origin hardening) and S4 (admin abuse resistance) are launch or post-launch priorities, and they add to `internal/web/middleware.go`: `SecurityHeaders`, the exported `LimiterKey` and `NewRateLimiterSize`, and the rightmost `ClientIP`. This refactor is backlog, so it lands **after** them. Step 4 moves whatever is there at the time; nothing in S3 or S4 depends on the package name.

S1 adds the DB gate and input cache to `web`, which stay in `web`. S5 adds `internal/cellinput`, a service-level package that handlers use through `web.Deps`. Steps 1–3 don't touch the files those specs change, so they can land at any time.

## 5. Implementation outline

1. Step 1: `internal/calibration`, `internal/changeset/preview.go` and `service.go`, `internal/calibrun/drift.go`, the admin preview view model and tests.
2. Step 2: `static/static.go`, `internal/web/server.go`, `internal/web/views/models.go` and `internal/admin/views/layout.templ`. Regenerate templ.
3. Step 3: `internal/config` with table-driven tests, then `cmd/server/app.go`, `cmd/admin/main.go`, `cmd/ingest/main.go` and `cmd/devseed/main.go`.
4. Step 4: create `internal/httpx`, move code and tests, and update imports in `web`, `admin` and `cmd/server`.
5. Step 5: `internal/changeset/service.go` (`History`, `Applied`), `internal/admin` (interfaces, `Deps`, handlers, fakes, fast tests), `cmd/server/app.go` wiring and `internal/archtest`.
6. Core spec §4.2 and §4.3.

## 6. Testing

- **Every step:** the full existing suite passes unchanged, apart from moved files and updated imports. The e2e suite passes on `main`.
- **Step 1:** `calibration.ScoreDropped` table test covering 0.02 exactly (false), 0.0201 (true) and float noise. The existing `NeedsOverride` tests move over.
- **Step 2:**
  - Rendering the admin layout without calling `web.New` produces `/static/admin.css?v=<10 hex>`.
  - `static.Version()` is stable across calls and changes when an embedded file changes (test with an `fstest.MapFS` through the internal `hashFS`).
- **Step 3:** table tests for the defaults, trailing-slash trimming, a bad key and `ADMIN_BACKUP_LOGIN=off`. The existing `cmd/server` and `cmd/admin` config tests keep passing through the new loaders. One test checks that both binaries derive the same origin from the same environment.
- **Step 4:** the moved middleware tests pass in `httpx`.
- **Step 5:**
  - The new fast admin tests run with `TP_SKIP_DB=1` (`internal/dbtest/dbtest.go`) and pass without Docker.
  - `internal/archtest` passes. It fails if `admin` imports `web` or `store` again.
  - A timing check in the pull request description records how long `go test ./internal/admin -run 'Catalog|Preview|Import|Export|History' -count=1` takes before and after.

## 7. Rollout, operations and rollback

- Pure refactor. There is no migration, config or operational change, and `docs/operations.md` is unaffected.
- Each step is a separate pull request and revertable on its own.
- After step 5, core spec §4.3's table gains `httpx` ("request ID, access log, recovery, client IP, rate limiting, timeouts, security headers; depends on nothing internal"), `config` ("typed environment loaders shared by binaries"), and `calibrun`, which is missing today ("resolves fixtures against live data; daily drift check"). The sentence about `store` becomes: "`store` is sqlc-generated query code used only by services. Handlers depend on consumer-side interfaces of services."

## 8. Open questions for the owner

1. **Interface for auth in admin.** Recommended: yes, as in §4.2 step 5. It is the only way to test catalog handlers without a database. The login and security flows stay integration-tested against the real `auth.Service`.
2. **When to do this.** Recommended: after S3 and S4, as low-priority work between features. Steps 1–3 are small and can be picked up any time.
