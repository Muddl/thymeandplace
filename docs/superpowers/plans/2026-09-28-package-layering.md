# Package Layering and Shared Infrastructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Repair five drifted package seams with no behaviour change, and add a test that stops them drifting again. The five seams:
- The override threshold moves to `calibration`.
- The asset version moves to `static`.
- The shared configuration loaders move to `internal/config`.
- The shared HTTP middleware moves to `internal/httpx`.
- `admin` depends on service interfaces instead of concrete services and a raw pool.

**Architecture:** Each task is one pull request that ships on its own, keeps every test green, and changes no behaviour. The dependency rules (spec §4.1) are then enforced by `internal/archtest`, which runs `go list -deps`.

**Tech Stack:** Go 1.26, templ, sqlc (untouched), testcontainers via `internal/dbtest` (only for tests that already use it).

**Spec:** `docs/superpowers/specs/2026-09-27-package-layering-design.md`. Issues [#16](https://github.com/Muddl/thymeandplace/issues/16) (F16), [#17](https://github.com/Muddl/thymeandplace/issues/17) (F17), [#18](https://github.com/Muddl/thymeandplace/issues/18) (F18), [#19](https://github.com/Muddl/thymeandplace/issues/19) (F19) and [#20](https://github.com/Muddl/thymeandplace/issues/20) (F20). All are **Todo** on the project board.

**Order relative to the other 2026-09-28 plans:**
- Tasks 1-3 touch no file the other plans change heavily, and can land any time.
- Tasks 4 and 5 move and rewire `internal/web/middleware.go` and `internal/admin`. Land them **after** these plans:
  - `client-ip-rightmost-xff` (#10), which changes `ClientIP`
  - `retry-url-sanitizing` (#15)
  - `observability-and-alerting` (#7), which changes `AccessLog`, `Common`'s caller and `enrollFinish`
- If Task 4 lands first, those plans' references to `web.ClientIP`, `web.AccessLog`, `web.Common` and `internal/web/middleware.go` become `httpx.*` and `internal/httpx/middleware.go`. Their logic doesn't change.
- Task 1 conflicts in `internal/calibrun/drift.go` with the observability plan's Task 6. Both are small; whichever lands second rebases.

**Spec drift at HEAD:** The spec lists `SecurityHeaders` (from S3) and `LimiterKey`/`NewRateLimiterSize` (from S4) as future additions to `middleware.go`. All three are now in `internal/web/middleware.go`, and Task 4 moves them. `admin` also uses `web.Limit`, `web.RequestID` and `web.NewRateLimiterSize`, which move too. The spec's `Admin.RPIDSet` isn't needed: the `TP_PUBLIC_TODAY` guard can test `RPID == "localhost"`, which is equivalent, because an unset `ADMIN_RP_ID` defaults to `thymeand.place`.

## Global Constraints

- No behaviour changes. The same env vars, defaults, error messages, HTTP responses, log lines and SQL.
- Dependency rules (spec §4.1), enforced by `internal/archtest` in Task 5:
  1. `season` and `calibration` import none of `thymeandplace/internal/store`, `thymeandplace/internal/db` or `github.com/jackc/pgx/v5`.
  2. `web` and `admin` import none of `store`, `db` or `pgx`.
  3. `admin` doesn't import `web`, and `web` doesn't import `admin`.
  4. `calibrun` doesn't import `changeset`.
- `auth.Service` and `changeset.Service` keep their pools and their integration tests. Only their consumer, `admin`, sees interfaces.
- Moved tests keep their names and assertions. Only the package and imports change.
- The asset version is computed once, lazily, with `sync.OnceValue`, and no package variable is set at run time.
- `static.Asset(name)` returns exactly what `views.Asset` returns today: `/static/<name>?v=<10 hex>`.

## Review Focus

1. **An admin page rendered without `web.New` having run** (for example, a test that builds only the admin handler). Its asset URLs must still carry `?v=<hash>`. Task 2 adds `TestAdminLayoutVersionsAssetsWithoutWebNew`.
2. **The static handler's `?v=` check after the move.** A URL carrying the current version must still get the one-year immutable `Cache-Control`, and any other URL a day. Task 2 keeps the existing `TestAssetURLsAreVersioned`, and the new test pins the header on both paths.
3. **The server and the admin CLI deriving different origins** from the same environment (a trailing slash, or unset `ADMIN_ORIGIN`). Task 3 adds a test that feeds both loaders one environment and compares the results.
4. **`DB_MAX_CONNS` validation**, which today lives only in `cmd/server`. It must keep its exact error for `1` and for non-numbers, now that `LoadDatabase` reads it. Task 3's table covers both.
5. **The admin "double-clicked Apply" path** (`ErrNotFound`, then `Applied`), once it goes through `changeset.Service.Applied` instead of `store`. It must still redirect to `/admin?applied=1` or answer 410. Task 5 adds a fast fake test for both, and the existing integration test keeps the real path.

---

### Task 1: The override threshold belongs to calibration (#18)

**Files:**
- Modify: `internal/calibration/calibration.go` (add `OverrideThreshold`, `ScoreDropped`)
- Modify: `internal/changeset/preview.go:9-16` (delete the constant and `NeedsOverride`), `:45`; `internal/changeset/service.go:102,189`
- Modify: `internal/calibrun/drift.go:14,50`
- Modify: `internal/changeset/preview_test.go:93-99` (move to `calibration`), `internal/changeset/service_test.go:80`
- Test: `internal/calibration/calibration_test.go`

**Interfaces:**
- Produces: `calibration.OverrideThreshold = 0.02` and `func calibration.ScoreDropped(before, after float64) bool`, with the same 1e-9 epsilon. The field `changeset.Preview.NeedsOverride` keeps its name, since `admin` and its views use it.

- [ ] **Step 1: Write the failing test**

Append to `internal/calibration/calibration_test.go`:

```go
// A drop of exactly OverrideThreshold is allowed; anything more needs an
// override. The epsilon absorbs float noise in a decimal 0.02.
func TestScoreDropped(t *testing.T) {
	for _, tc := range []struct {
		before, after float64
		want          bool
	}{
		{0.50, 0.48, false},
		{0.50, 0.4799, true},
		{0.671, 0.651, false}, // 0.02 in decimal, 0.020000000000000018 in float64
		{0.5, 0.5, false},
		{0.5, 0.6, false},
	} {
		if got := ScoreDropped(tc.before, tc.after); got != tc.want {
			t.Errorf("ScoreDropped(%v, %v) = %v, want %v", tc.before, tc.after, got, tc.want)
		}
	}
}
```

Delete `TestNeedsOverrideBoundary` from `internal/changeset/preview_test.go`; it moved here.

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/calibration/ -run TestScoreDropped -count=1`
Expected: FAIL (`undefined: ScoreDropped`).

- [ ] **Step 3: Move the policy**

Append to `internal/calibration/calibration.go`:

```go
// OverrideThreshold is the largest calibration score drop allowed without an
// explicit override, both for catalog edits (changeset) and in the daily
// drift check (calibrun).
const OverrideThreshold = 0.02

// ScoreDropped reports a drop of more than OverrideThreshold. The epsilon
// keeps a drop of exactly 0.02 (in decimal) from tripping on float noise.
func ScoreDropped(before, after float64) bool {
	return before-after > OverrideThreshold+1e-9
}
```

Then:
- Delete `OverrideThreshold` and `NeedsOverride` from `internal/changeset/preview.go`.
- Replace every `NeedsOverride(` call in `preview.go:45` and `service.go:102,189` with `calibration.ScoreDropped(`. `changeset` already imports `calibration`.
- In `service_test.go:80`, use `calibration.OverrideThreshold`, adding the import.
- In `internal/calibrun/drift.go`, replace `changeset.NeedsOverride(` with `calibration.ScoreDropped(`, and drop the `changeset` import.

- [ ] **Step 4: Run the affected packages**

Run: `go build ./... && go test ./internal/calibration/ ./internal/changeset/ ./internal/calibrun/ ./internal/admin/ -count=1`
Expected: PASS.

- [ ] **Step 5: Confirm calibrun no longer depends on changeset**

Run: `go list -deps ./internal/calibrun | grep -c 'internal/changeset$'`
Expected: `0`.

- [ ] **Step 6: Commit**

```bash
git add internal/calibration/ internal/changeset/ internal/calibrun/
git commit -m "calibration: own the override threshold so calibrun stops importing changeset (#18)"
```

### Task 2: The asset version lives in `static` (#20)

**Files:**
- Modify: `static/*.go` (the embed file; add `Version`, `Asset`, `hashFS`)
- Create: `static/static_test.go`
- Modify: `internal/web/server.go:113-133` (delete `hashFS` and `assetVersionOnce`), `:127` (delete the `Do`), `:203` (compare with `static.Version()`)
- Modify: `internal/web/views/models.go:67-77` (`Asset` delegates; delete `AssetVersion`)
- Modify: `internal/admin/views/layout.templ:15-17` (`static.Asset`), then regenerate templ
- Test: `internal/admin/views/lint_test.go` or a new `internal/admin/views/layout_test.go`

**Interfaces:**
- Produces: `func static.Version() string` (10 hex characters) and `func static.Asset(name string) string` (`/static/<name>?v=<Version()>`). `views.Asset(name)` stays as a thin wrapper, so no public template changes.

- [ ] **Step 1: Write the failing tests**

Create `static/static_test.go`:

```go
package static

import (
	"regexp"
	"testing"
	"testing/fstest"
)

func TestVersionIsStableAndContentAddressed(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{10}$`).MatchString(Version()) || Version() != Version() {
		t.Fatalf("Version() = %q", Version())
	}
	a := hashFS(fstest.MapFS{"app.css": {Data: []byte("a")}})
	b := hashFS(fstest.MapFS{"app.css": {Data: []byte("b")}})
	if a == b || a != hashFS(fstest.MapFS{"app.css": {Data: []byte("a")}}) {
		t.Fatal("the hash must change with content and only with content")
	}
	if Asset("admin.css") != "/static/admin.css?v="+Version() {
		t.Fatalf("Asset = %q", Asset("admin.css"))
	}
}
```

Create `internal/admin/views/layout_test.go`. Before writing it, check `layout.templ` for the component's exact name and argument type:

```go
package views

import (
	"bytes"
	"context"
	"regexp"
	"testing"
)

// Admin pages version their assets without the public handler ever being
// built: no construction order.
func TestAdminLayoutVersionsAssetsWithoutWebNew(t *testing.T) {
	var buf bytes.Buffer
	if err := Layout(LayoutVM{Title: "t"}, nil).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`/static/admin\.css\?v=[0-9a-f]{10}`).Match(buf.Bytes()) {
		t.Fatalf("unversioned admin asset:\n%s", buf.String())
	}
}
```

If `Layout`'s signature differs, call it the way `lint_test.go` in the same package does. The assertion stays the same.

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./static/ ./internal/admin/views/ -run 'TestVersion|TestAdminLayoutVersions' -count=1`
Expected: FAIL (`undefined: Version`; the admin layout renders `/static/admin.css` with no `?v=`, because `web.New` never ran).

- [ ] **Step 3: Implement**

Add to the `static` package (next to the `//go:embed` file):

```go
// Version is a short hash of every embedded file's path and content. Asset
// URLs carry it, so a URL with the current version names these exact bytes
// and can be cached for a year. It is computed once, on first use.
var Version = sync.OnceValue(func() string { return hashFS(FS) })

// Asset is the versioned URL of an embedded file.
func Asset(name string) string { return "/static/" + name + "?v=" + Version() }

// hashFS returns a short hash of every file's path and content in fsys.
func hashFS(fsys fs.FS) string {
	// body moved verbatim from internal/web/server.go
}
```

Move `hashFS`'s body verbatim from `internal/web/server.go`, and add the imports it needs (`crypto/sha256`, `encoding/hex`, `io/fs`, `sync`).

In `internal/web/server.go`:
- Delete `assetVersionOnce`, `hashFS` and the `assetVersionOnce.Do(...)` line in `New`.
- In `staticFiles`, change the version check to `v == static.Version()`.

In `internal/web/views/models.go`, replace `AssetVersion` and `Asset` with:

```go
// Asset is the versioned URL of an embedded static file (static.Asset).
func Asset(name string) string { return static.Asset(name) }
```

and import `thymeandplace/static`. Check that `static` doesn't import `views`. It doesn't; it only embeds files.

In `internal/admin/views/layout.templ`, replace the three `webviews.Asset(` calls with `static.Asset(`, import `"thymeandplace/static"`, and remove the `webviews` import if it is now unused.

Run: `go tool templ generate`

- [ ] **Step 4: Run the tests**

Run: `go test ./static/ ./internal/web/... ./internal/admin/... -count=1`
Expected: PASS, including the unchanged `TestAssetURLsAreVersioned` in `internal/web/server_test.go`.

- [ ] **Step 5: Commit**

```bash
git add static/ internal/web/ internal/admin/views/
git commit -m "static: compute the asset version once, on first use, for both layouts (#20)"
```

### Task 3: One config loader for every binary (#19)

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `cmd/server/app.go:32-126` (`config`, `loadConfig`)
- Modify: `cmd/admin/main.go:51-70`
- Modify: `cmd/ingest/main.go` (the `DATABASE_URL` read), `cmd/devseed/main.go:31`

**Interfaces:**
- Produces:
  ```go
  type Database struct {
      URL      string
      MaxConns int32 // DB_MAX_CONNS; 0 → the binary's default pool size
  }
  func LoadDatabase(getenv func(string) string) (Database, error)

  type Admin struct {
      SecretKey     []byte // nil when ADMIN_SECRET_KEY is unset
      RPID, Origin  string // defaults thymeand.place and https://<RPID>; Origin has no trailing "/"
      PublicBaseURL string // PUBLIC_BASE_URL, no trailing "/"; default Origin; validated as an origin
      BackupEnabled bool   // ADMIN_BACKUP_LOGIN != "off"
  }
  func LoadAdmin(getenv func(string) string) (Admin, error)
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/config/config_test.go`. Take each error string from the existing `cmd/server` tests (`TestLoadConfigDBMaxConns`, `TestLoadConfigBaseURL`) so messages stay identical:

```go
package config

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDatabase(t *testing.T) {
	if _, err := LoadDatabase(env(nil)); err == nil || err.Error() != "DATABASE_URL is required" {
		t.Fatalf("missing URL: %v", err)
	}
	d, err := LoadDatabase(env(map[string]string{"DATABASE_URL": "postgres://x/y", "DB_MAX_CONNS": "10"}))
	if err != nil || d.URL != "postgres://x/y" || d.MaxConns != 10 {
		t.Fatalf("%+v %v", d, err)
	}
	for _, bad := range []string{"1", "abc", "-3"} {
		if _, err := LoadDatabase(env(map[string]string{"DATABASE_URL": "u", "DB_MAX_CONNS": bad})); err == nil || !strings.Contains(err.Error(), "DB_MAX_CONNS must be a whole number of at least 2") {
			t.Errorf("DB_MAX_CONNS=%q: %v", bad, err)
		}
	}
}

func TestLoadAdmin(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	a, err := LoadAdmin(env(nil))
	if err != nil || a.SecretKey != nil || a.RPID != "thymeand.place" || a.Origin != "https://thymeand.place" || a.PublicBaseURL != a.Origin || !a.BackupEnabled {
		t.Fatalf("defaults: %+v %v", a, err)
	}
	a, err = LoadAdmin(env(map[string]string{"ADMIN_SECRET_KEY": key, "ADMIN_RP_ID": "localhost", "ADMIN_ORIGIN": "https://localhost:8443/", "ADMIN_BACKUP_LOGIN": "off"}))
	if err != nil || len(a.SecretKey) != 32 || a.Origin != "https://localhost:8443" || a.PublicBaseURL != "https://localhost:8443" || a.BackupEnabled {
		t.Fatalf("set: %+v %v", a, err)
	}
	if _, err := LoadAdmin(env(map[string]string{"ADMIN_SECRET_KEY": "not base64!"})); err == nil {
		t.Fatal("bad key must fail")
	}
	if _, err := LoadAdmin(env(map[string]string{"PUBLIC_BASE_URL": "https://thymeand.place/path"})); err == nil {
		t.Fatal("a base URL with a path must fail")
	}
}
```

Add to `cmd/server/app_test.go`:

```go
// The server and the admin CLI read one environment the same way, so
// enrollment links always match the relying party the server checks.
func TestServerAndAdminCLIAgreeOnOrigin(t *testing.T) {
	e := map[string]string{"DATABASE_URL": "postgres://x/y", "ADMIN_RP_ID": "example.test", "ADMIN_ORIGIN": "https://example.test/"}
	c, err := loadConfig(func(k string) string { return e[k] }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	a, err := config.LoadAdmin(func(k string) string { return e[k] })
	if err != nil || c.AdminOrigin != a.Origin || c.AdminRPID != a.RPID || a.Origin != "https://example.test" {
		t.Fatalf("server %q/%q, loader %q/%q (%v)", c.AdminRPID, c.AdminOrigin, a.RPID, a.Origin, err)
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/config/ ./cmd/server/ -run 'TestLoad|TestServerAndAdminCLIAgreeOnOrigin' -count=1`
Expected: FAIL to compile (no `internal/config`).

- [ ] **Step 3: Implement `internal/config`**

Create `internal/config/config.go`. Move the bodies from `cmd/server/app.go`'s `loadConfig` verbatim: the `DB_MAX_CONNS` parse, `ParseSecretKey`, the RP ID and origin defaults, the `TrimRight`, the `PUBLIC_BASE_URL` default and validation, and `ADMIN_BACKUP_LOGIN`:

```go
// Package config reads the environment settings more than one binary
// shares: the database and the admin/WebAuthn settings. Settings only one
// binary uses stay in that binary.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"thymeandplace/internal/auth"
)

type Database struct {
	URL      string
	MaxConns int32
}

func LoadDatabase(getenv func(string) string) (Database, error) {
	d := Database{URL: getenv("DATABASE_URL")}
	if d.URL == "" {
		return d, errors.New("DATABASE_URL is required")
	}
	if v := getenv("DB_MAX_CONNS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 2 {
			return d, fmt.Errorf("DB_MAX_CONNS must be a whole number of at least 2, got %q", v)
		}
		d.MaxConns = int32(n)
	}
	return d, nil
}

type Admin struct {
	SecretKey     []byte
	RPID, Origin  string
	PublicBaseURL string
	BackupEnabled bool
}

func LoadAdmin(getenv func(string) string) (Admin, error) {
	var a Admin
	if k := getenv("ADMIN_SECRET_KEY"); k != "" {
		key, err := auth.ParseSecretKey(k)
		if err != nil {
			return a, err
		}
		a.SecretKey = key
	}
	a.RPID, a.Origin = getenv("ADMIN_RP_ID"), getenv("ADMIN_ORIGIN")
	if a.RPID == "" {
		a.RPID = "thymeand.place"
	}
	if a.Origin == "" {
		a.Origin = "https://" + a.RPID
	}
	// A browser's Origin never ends in "/", so neither may the WebAuthn
	// origin (nor the enrollment links built from it).
	a.Origin = strings.TrimRight(a.Origin, "/")
	a.PublicBaseURL = strings.TrimRight(getenv("PUBLIC_BASE_URL"), "/")
	if a.PublicBaseURL == "" {
		a.PublicBaseURL = a.Origin
	}
	if u, err := url.Parse(a.PublicBaseURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		return a, fmt.Errorf("PUBLIC_BASE_URL must be an origin like https://thymeand.place, got %q", a.PublicBaseURL)
	}
	a.BackupEnabled = getenv("ADMIN_BACKUP_LOGIN") != "off"
	return a, nil
}
```

`internal/config` imports `auth` only for `ParseSecretKey`. That is acceptable: `auth` doesn't import `config`.

- [ ] **Step 4: Use the loaders**

- **`cmd/server/app.go` `loadConfig`:**
  - Replace the duplicated blocks with `db, err := config.LoadDatabase(getenv)` and `adm, err := config.LoadAdmin(getenv)`.
  - Copy their fields into the existing `config` struct (`DatabaseURL`, `DBMaxConns`, `AdminSecretKey`, `AdminRPID`, `AdminOrigin`, `BaseURL`, `AdminBackup`), so `newApp` and the tests stay unchanged.
  - Keep `ADDR`, `TRUST_PROXY_HEADERS`, `FEATURE_SEAFOOD` (and the `HEARTBEAT_*` reads if the observability plan has landed) in `cmd/server`.
  - Change the `TP_PUBLIC_TODAY` guard to `if adm.RPID != "localhost"`. This is equivalent, because an unset `ADMIN_RP_ID` defaults to `thymeand.place`.
  - Keep the order of checks so the first error for a bad environment stays the same. `DATABASE_URL` is checked first, as today.
- **`cmd/admin/main.go`:**
  - Replace lines 51-70 with `LoadAdmin` and `LoadDatabase`.
  - If `adm.SecretKey == nil`, return `fail(err)` with the same error `auth.ParseSecretKey("")` gives today (call it to get the message), so `cmd/admin` tests and operator output don't change.
  - Pass `db.URL` to `db.Open`, and `adm.RPID`/`adm.Origin` to `auth.Config`.
- **`cmd/ingest/main.go` and `cmd/devseed/main.go`:** read the URL through `config.LoadDatabase(getenv)`. On error, keep each binary's current failure path and exit code.

- [ ] **Step 5: Run the binaries' tests**

Run: `go test ./internal/config/ ./cmd/... -count=1`
Expected: PASS, including every existing `TestLoadConfig*` test, unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/config/ cmd/
git commit -m "config: one loader for the database and admin settings shared by every binary (#19)"
```

### Task 4: Shared HTTP plumbing in `internal/httpx` (#16)

**Files:**
- Create: `internal/httpx/middleware.go` (moved from `internal/web/middleware.go`), `internal/httpx/common.go` (`Common`, moved from `internal/web/server.go`)
- Create: `internal/httpx/middleware_test.go` (moved tests)
- Modify: `internal/web/middleware.go` (keeps only `PublicLimits` and anything public-only), `internal/web/*.go` (`httpx.` prefixes), `internal/web/middleware_test.go` (keeps only tests of `PublicLimits`)
- Modify: `internal/admin/*.go` (`web.` → `httpx.` for the plumbing)
- Modify: `cmd/server/app.go` (`web.Common` → `httpx.Common`)

**Interfaces:**
- Produces: package `httpx`, with these exported names unchanged:
  - `RequestID`, `RequestIDFrom`, `SetCellID`
  - `AccessLog`, `Recover`, `SecurityHeaders`, `PublicCSP`, `Common`
  - `ClientIP`, `LimiterKey`, `Limit`
  - `RateLimiter`, `NewRateLimiter`, `NewRateLimiterSize`, `(*RateLimiter).Middleware`
  - `Timeout`
- `web` keeps `PublicLimits` and all handlers.

- [ ] **Step 1: Move the file with history**

```bash
mkdir -p internal/httpx
git mv internal/web/middleware.go internal/httpx/middleware.go
git mv internal/web/middleware_test.go internal/httpx/middleware_test.go
```

In both files, change `package web` to `package httpx`. Add a package comment to `internal/httpx/middleware.go`:

```go
// Package httpx is the HTTP plumbing shared by the public site and /admin:
// request IDs, the access log, panic recovery, security headers, client IP,
// rate limiting and timeouts. It depends on nothing internal.
```

- [ ] **Step 2: Put the public-only parts back in `web`**

- Cut `PublicLimits` (and any helper only it uses) from `internal/httpx/middleware.go` into a new `internal/web/limits.go` in `package web`.
- Move `Common` from `internal/web/server.go` into `internal/httpx/common.go` unchanged.
- Cut the tests that call `PublicLimits` (`TestRateLimit`, `TestRateLimitIPv6Prefix` and any other that references it) from `internal/httpx/middleware_test.go` into a new `internal/web/limits_test.go`, prefixing `NewRateLimiter` and friends with `httpx.`.
- If a moved test relies on a helper defined in the other file (such as `ok()`), copy that small helper into both test files.

- [ ] **Step 3: Fix references**

Run: `go build ./... 2>&1 | head -50`
Expected: `undefined:` errors in `web`, `admin` and `cmd/server`. Fix each by importing `thymeandplace/internal/httpx` and prefixing the name. In `web`, the calls are `SetCellID`, `RequestIDFrom`, `Timeout`, `NewRateLimiter`, `Recover` and `ClientIP`. In `admin`, change every `web.X` for the names listed above to `httpx.X`. `admin` must end with no import of `internal/web`: check with `grep -n '"thymeandplace/internal/web"' internal/admin/*.go`, which must print nothing. `cmd/server` uses `httpx.Common`.

- [ ] **Step 4: Confirm the dependency is gone, and run everything**

Run: `go list -deps ./internal/admin | grep -c 'thymeandplace/internal/web$'; go vet ./... && go test ./... -count=1`
Expected: `0`, then PASS. The moved tests pass unchanged in `httpx`.

- [ ] **Step 5: Commit**

```bash
git add internal/httpx/ internal/web/ internal/admin/ cmd/server/
git commit -m "httpx: move shared middleware out of the public package; admin no longer imports web (#16)"
```

### Task 5: Admin behind service interfaces, fast admin tests, and `archtest` (#17)

**Files:**
- Create: `internal/changeset/history.go` (`HistoryQuery`, `HistoryEntry`, `HistoryPage`, `(*Service).History`, `(*Service).Applied`)
- Test: `internal/changeset/history_test.go` (integration, `dbtest`)
- Create: `internal/admin/deps.go` (`AuthService`, `Changes`)
- Modify: `internal/admin/admin.go:28-35` (`Deps`), `internal/admin/importexport.go:80-125` (`history`), `internal/admin/preview.go:132` (`Applied`)
- Modify: `internal/admin/testclient_test.go:98`, `internal/admin/catalogenv_test.go:42`, `cmd/server/app.go` (drop `Pool:`)
- Create: `internal/admin/fast_test.go` (fakes and no-database tests)
- Create: `internal/archtest/archtest_test.go`
- Modify: `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` §4.2, §4.3

**Interfaces:**
- Produces in `changeset`:
  ```go
  const HistoryPageSize = 100
  type HistoryQuery struct { Slug string; Before int64 } // Before 0 → newest
  type HistoryEntry struct {
      ID        int64
      ChangedAt time.Time
      Actor, Action, Table string
      RowKey, Before, After []byte
      ChangeID  int64 // 0 → none
  }
  type HistoryPage struct { Entries []HistoryEntry; More bool }
  func (s *Service) History(ctx context.Context, q HistoryQuery) (HistoryPage, error)
  func (s *Service) Applied(ctx context.Context, id int64) (bool, error)
  ```
- Produces in `admin`:
  ```go
  type AuthService interface { /* the 21 methods admin calls, signatures below */ }
  type Changes interface {
      Preview(ctx context.Context, cs changeset.Changeset) (changeset.Pending, changeset.Preview, error)
      Reopen(ctx context.Context, id int64) (changeset.Pending, changeset.Preview, error)
      Apply(ctx context.Context, id int64, actor string, opts changeset.ApplyOptions) error
      Applied(ctx context.Context, id int64) (bool, error)
      History(ctx context.Context, q changeset.HistoryQuery) (changeset.HistoryPage, error)
  }
  type Deps struct {
      Auth       AuthService
      Log        *slog.Logger
      TrustProxy bool
      Changes    Changes
      Load       func(context.Context) (*catalog.Snapshot, error)
      now    func() time.Time
      limits func(string) (string, httpx.Limit, bool)
  }
  ```

- [ ] **Step 1: Service methods, integration-tested**

Create `internal/changeset/history.go`:

```go
package changeset

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"thymeandplace/internal/store"
)

// HistoryPageSize is how many history rows one page shows.
const HistoryPageSize = 100

type HistoryQuery struct {
	Slug   string // "" → every ingredient
	Before int64  // 0 → newest
}

type HistoryEntry struct {
	ID                    int64
	ChangedAt             time.Time
	Actor, Action, Table  string
	RowKey, Before, After []byte
	ChangeID              int64 // 0 → none
}

type HistoryPage struct {
	Entries []HistoryEntry
	More    bool // an older page exists
}

// History returns one page of catalog history, newest first. ListHistory
// fetches one row past the page size, so a page of exactly
// HistoryPageSize remaining rows is told apart from one with more to come.
func (s *Service) History(ctx context.Context, q HistoryQuery) (HistoryPage, error) {
	params := store.ListHistoryParams{}
	if q.Slug != "" {
		params.Slug = pgtype.Text{String: q.Slug, Valid: true}
	}
	if q.Before != 0 {
		params.Before = pgtype.Int8{Int64: q.Before, Valid: true}
	}
	rows, err := store.New(s.pool).ListHistory(ctx, params)
	if err != nil {
		return HistoryPage{}, err
	}
	var p HistoryPage
	if len(rows) > HistoryPageSize {
		rows, p.More = rows[:HistoryPageSize], true
	}
	for _, h := range rows {
		e := HistoryEntry{ID: h.ID, ChangedAt: h.ChangedAt.Time, Actor: h.Actor, Action: h.Action,
			Table: h.TableName, RowKey: h.RowKey, Before: h.Before, After: h.After}
		if h.ChangeID.Valid {
			e.ChangeID = h.ChangeID.Int64
		}
		p.Entries = append(p.Entries, e)
	}
	return p, nil
}

// Applied reports whether change id already wrote history (a double-clicked
// Apply's second request finds its pending row gone).
func (s *Service) Applied(ctx context.Context, id int64) (bool, error) {
	return store.New(s.pool).ChangeApplied(ctx, id)
}
```

Add `"time"` to the imports. Use the service's actual pool field name (check `changeset.Service`'s struct; it may be `db` or `pool`), and `ChangeApplied`'s exact parameter type from `internal/store`.

Create `internal/changeset/history_test.go`. It uses `dbtest`, applies one changeset the way `service_test.go` does, then asserts:
- `History(ctx, HistoryQuery{})` returns that change's rows newest first, with `ChangeID` set and `More == false`.
- `Applied(ctx, thatID)` is `true`, and `Applied(ctx, 999999)` is `false`.

Reuse `service_test.go`'s setup helper; don't duplicate it.

Run: `go test ./internal/changeset/ -count=1`. Expected: PASS.

- [ ] **Step 2: Admin interfaces and `Deps`**

Create `internal/admin/deps.go`:

```go
package admin

import (
	"context"
	"net/http"
	"time"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/changeset"
)

// AuthService is what admin needs from auth.Service. Handlers depend on it,
// not on the concrete service, so catalog handlers can be tested without a
// database. Sign-in flows keep their integration tests against the real one.
type AuthService interface {
	Now() time.Time
	BackupEnabled() bool
	LookupSession(ctx context.Context, raw string) (auth.Session, bool, error)
	CreateSession(ctx context.Context, method string) (string, auth.Session, error)
	DeleteSession(ctx context.Context, raw string) error
	MarkReauthed(ctx context.Context, sess auth.Session) error
	RevokeOtherSessions(ctx context.Context, keep auth.Session) (int64, error)
	AllowAttempt(ctx context.Context, c auth.Caller, method string) (bool, error)
	LogAttempt(ctx context.Context, c auth.Caller, method, outcome string)
	LogRefusal(ctx context.Context, c auth.Caller, method string)
	BackupLogin(ctx context.Context, password, code string) error
	BeginLogin(ctx context.Context) ([]byte, string, error)
	FinishLogin(ctx context.Context, state string, r *http.Request) error
	EnrollmentValid(ctx context.Context, raw string) (bool, error)
	BeginEnrollment(ctx context.Context, token string) ([]byte, string, error)
	FinishEnrollment(ctx context.Context, state string, r *http.Request, name, token string) error
	BeginRegistration(ctx context.Context) ([]byte, string, error)
	AddPasskey(ctx context.Context, state string, r *http.Request, name string) error
	Passkeys(ctx context.Context) ([]auth.Passkey, error)
	RenamePasskey(ctx context.Context, id []byte, name string) error
	RemovePasskey(ctx context.Context, id []byte) error
}

// Changes is what admin needs from changeset.Service.
type Changes interface {
	Preview(ctx context.Context, cs changeset.Changeset) (changeset.Pending, changeset.Preview, error)
	Reopen(ctx context.Context, id int64) (changeset.Pending, changeset.Preview, error)
	Apply(ctx context.Context, id int64, actor string, opts changeset.ApplyOptions) error
	Applied(ctx context.Context, id int64) (bool, error)
	History(ctx context.Context, q changeset.HistoryQuery) (changeset.HistoryPage, error)
}

var (
	_ AuthService = (*auth.Service)(nil)
	_ Changes     = (*changeset.Service)(nil)
)
```

Re-derive the method list with `grep -ohE 'a\.d\.Auth\.[A-Za-z]+' internal/admin/*.go | sort -u` and match each signature to `internal/auth`. The compile-time assertions catch a mismatch.

In `internal/admin/admin.go`, change `Deps.Auth` to `AuthService`, `Deps.Changes` to `Changes`, and delete `Pool` and the `pgxpool` import.

- [ ] **Step 3: Handlers through the services**

In `internal/admin/importexport.go` `history`:
- Build `changeset.HistoryQuery{Slug: slug}`, and set `Before` from the parsed `before` cursor.
- Call `a.d.Changes.History(r.Context(), hq)`.
- Loop over `page.Entries`:
  - `At: e.ChangedAt.UTC().Format("2006-01-02 15:04:05")`, `Table: e.Table`, `Key: string(e.RowKey)`.
  - `ChangeID: "#" + strconv.FormatInt(e.ChangeID, 10)` when `e.ChangeID != 0`.
- `OlderURL` when `page.More`, using the last entry's ID.
- Delete the `store`, `pgtype` and page-size-plus-one code there. Keep the log message and error response unchanged.

In `internal/admin/preview.go`, replace `store.New(a.d.Pool).ChangeApplied(r.Context(), id)` with `a.d.Changes.Applied(r.Context(), id)`, and drop the `store` import.

Remove `Pool: ...` from `internal/admin/testclient_test.go:98`, `internal/admin/catalogenv_test.go:42` and `cmd/server/app.go`.

Run: `go build ./... && go test ./internal/admin/ ./cmd/server/ -count=1`
Expected: PASS. The integration tests are unchanged.

- [ ] **Step 4: Fast admin tests with fakes**

Create `internal/admin/fast_test.go`:

```go
package admin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/changeset"
)

// fakeAuth accepts one session cookie. Methods it doesn't override panic
// (nil embedded interface), so a test that reaches one is a test that
// needs the real service.
type fakeAuth struct {
	AuthService
	now time.Time
}

const fakeCookie, fakeCSRF = "fake-session", "fake-csrf-token-0123456789abcdef"

func (f fakeAuth) Now() time.Time      { return f.now }
func (f fakeAuth) BackupEnabled() bool { return false }
func (f fakeAuth) LookupSession(_ context.Context, raw string) (auth.Session, bool, error) {
	if raw != fakeCookie {
		return auth.Session{}, false, nil
	}
	return auth.Session{CSRF: fakeCSRF, Method: "passkey", CreatedAt: f.now, LastSeenAt: f.now, AuthAt: f.now}, true, nil
}

type fakeChanges struct {
	Changes
	applyErr error
	applied  bool
	history  changeset.HistoryPage
	gotQuery changeset.HistoryQuery
}

func (f *fakeChanges) Apply(context.Context, int64, string, changeset.ApplyOptions) error { return f.applyErr }
func (f *fakeChanges) Applied(context.Context, int64) (bool, error)                      { return f.applied, nil }
func (f *fakeChanges) History(_ context.Context, q changeset.HistoryQuery) (changeset.HistoryPage, error) {
	f.gotQuery = q
	return f.history, nil
}

func fastAdmin(t *testing.T, ch *fakeChanges) http.Handler {
	t.Helper()
	snap, err := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	if err != nil {
		t.Fatal(err)
	}
	return New(Deps{
		Auth: fakeAuth{now: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}, Changes: ch,
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Load: func(context.Context) (*catalog.Snapshot, error) { return snap, nil },
		limits: noLimits,
	})
}

func fastDo(h http.Handler, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: fakeCookie})
	if method != http.MethodGet {
		r.Header.Set("X-CSRF-Token", fakeCSRF)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestFastCatalogList(t *testing.T) {
	rec := fastDo(fastAdmin(t, &fakeChanges{}), "GET", "/admin?q=tom")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Tomato") || strings.Contains(rec.Body.String(), "Basil") {
		t.Fatalf("%d\n%s", rec.Code, rec.Body.String())
	}
}

func TestFastExport(t *testing.T) {
	rec := fastDo(fastAdmin(t, &fakeChanges{}), "GET", "/admin/export")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "thymeandplace-catalog-20260925.json") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}

func TestFastHistoryPaging(t *testing.T) {
	ch := &fakeChanges{history: changeset.HistoryPage{More: true, Entries: []changeset.HistoryEntry{
		{ID: 42, ChangedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Actor: "owner (passkey)", Action: "update", Table: "ingredient", RowKey: []byte("tomato"), ChangeID: 7},
	}}}
	rec := fastDo(fastAdmin(t, ch), "GET", "/admin/history?slug=tomato&before=100")
	body := rec.Body.String()
	if rec.Code != 200 || ch.gotQuery != (changeset.HistoryQuery{Slug: "tomato", Before: 100}) ||
		!strings.Contains(body, "#7") || !strings.Contains(body, "before=42") {
		t.Fatalf("%d query=%+v\n%s", rec.Code, ch.gotQuery, body)
	}
	if rec := fastDo(fastAdmin(t, ch), "GET", "/admin/history?before=x"); rec.Code != 400 {
		t.Fatalf("bad cursor: %d", rec.Code)
	}
}

// A double-clicked Apply: the second request finds the pending row gone.
func TestFastApplyNotFound(t *testing.T) {
	ch := &fakeChanges{applyErr: changeset.ErrNotFound, applied: true}
	if rec := fastDo(fastAdmin(t, ch), "POST", "/admin/changesets/5/apply"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin?applied=1" {
		t.Fatalf("already applied: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	ch.applied = false
	if rec := fastDo(fastAdmin(t, ch), "POST", "/admin/changesets/5/apply"); rec.Code != http.StatusGone {
		t.Fatalf("expired: %d", rec.Code)
	}
}
```

Adjust assertions only to match real markup, such as how the catalog list filters or how history renders the `ChangeID`. Get those by rendering once and reading the output, never by weakening what the test checks. `noLimits` is in `testclient_test.go` (same package).

Run: `TP_SKIP_DB=1 go test ./internal/admin/ -run Fast -count=1 -v`
Expected: PASS without Docker. Record in the pull request description how long `go test ./internal/admin -run 'Fast' -count=1` takes, next to the full package's time.

- [ ] **Step 5: `archtest`**

Create `internal/archtest/archtest_test.go`:

```go
// Package archtest enforces the package dependency rules (core spec §4.3).
package archtest

import (
	"os/exec"
	"strings"
	"testing"
)

func deps(t *testing.T, pkg string) map[string]bool {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", pkg).Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pkg, err)
	}
	m := map[string]bool{}
	for _, l := range strings.Fields(string(out)) {
		m[l] = true
	}
	return m
}

const mod = "thymeandplace/internal/"

var ioPkgs = []string{mod + "store", mod + "db", "github.com/jackc/pgx/v5"}

func TestDependencyRules(t *testing.T) {
	for _, r := range []struct {
		pkg       string
		forbidden []string
		why       string
	}{
		{mod + "season", ioPkgs, "the engine is pure"},
		{mod + "calibration", ioPkgs, "scoring is pure"},
		{mod + "web", append([]string{mod + "admin"}, ioPkgs...), "handlers use services, not SQL; public and admin stay apart"},
		{mod + "admin", append([]string{mod + "web"}, ioPkgs...), "handlers use services, not SQL; public and admin stay apart"},
		{mod + "calibrun", []string{mod + "changeset"}, "the drift check doesn't depend on admin edits"},
	} {
		d := deps(t, r.pkg)
		for _, f := range r.forbidden {
			if d[f] {
				t.Errorf("%s imports %s (directly or not): %s", r.pkg, f, r.why)
			}
		}
	}
}
```

Run: `go test ./internal/archtest/ -count=1`
Expected: PASS. If `web` or `admin` still reaches `pgx` through a service package (for example `web` → `climate` → `store`), the rule is too strict as written. In that case, change rules 2 and 3 to check **direct** imports only: `go list -f '{{join .Imports "\n"}}'` for those two subjects. Keep the transitive check for `season`, `calibration` and `calibrun`. Note the choice in the pull request. Then prove the test bites: temporarily add `import _ "thymeandplace/internal/store"` to an `admin` file, confirm the test fails naming `admin`, and remove it.

- [ ] **Step 6: Core spec**

- §4.2 layout: add `internal/httpx/`, `internal/config/` and `internal/archtest/`.
- §4.3 table: add the rows from spec §7:
  - `httpx`: "request ID, access log, recovery, client IP, rate limiting, timeouts, security headers; depends on nothing internal"
  - `config`: "typed environment loaders shared by binaries"
  - `calibrun`: "resolves fixtures against live data; daily drift check"
- §4.3 text: replace the `store` sentence with "`store` is sqlc-generated query code used only by services. Handlers depend on consumer-side interfaces of services." Then list the four rules from the Global Constraints.

- [ ] **Step 7: Run everything and commit**

Run: `go vet ./... && go test ./... -count=1`
Expected: PASS.

```bash
git add internal/changeset/ internal/admin/ internal/archtest/ cmd/server/ docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md
git commit -m "admin: depend on service interfaces, test catalog handlers with fakes, enforce layering (#17)"
```

### Task 6: Whole-repo check

- [ ] **Step 1:** Run `go vet ./... && go test ./... -count=1`. Expected: PASS.
- [ ] **Step 2:** Run `go tool templ generate && ./scripts/sqlc.sh generate && git diff --exit-code`. Expected: no diff.
- [ ] **Step 3:** Run `./e2e/run.sh`. Expected: PASS. No behaviour changed.
