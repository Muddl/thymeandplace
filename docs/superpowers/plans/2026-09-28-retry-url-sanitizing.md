# Retry URLs Through templ's Sanitizer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop passing request-derived retry URLs through `templ.SafeURL`. Error panels get a retry link rebuilt from validated values, and every public view renders URLs through `templ.URL`.

**Architecture:** Two new helpers in `internal/web` build the retry URL. `seasonRetryURL` rebuilds `/season` from the parsed floats, a valid numeric place ID and the validated date. `retryPath` gives a query-free path for the early failure and the panic handler. `/ingredient` reuses `ingredientURL`. In the views, `templ.SafeURL` becomes `templ.URL`, and a test fails if `SafeURL` comes back into `internal/web/views`.

**Tech Stack:** Go 1.26, templ v0.3.1020 (`go tool templ generate`), `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-09-27-public-origin-hardening-design.md` §4.3 and §6 ("Retry URLs"). Issue [#15](https://github.com/Muddl/thymeandplace/issues/15).

**Scope note:** The spec's F03 part (CSP, security headers, htmx hardening, the template lint test, the e2e CSP guard) shipped in `620cf2b`. This plan covers only F15.

## Global Constraints

- There is no `templ.SafeURL` anywhere in `internal/web/views`.
- `href` values use `templ.URL(...)`. The templ docs (checked through Context7) say it replaces an unsafe URL with `about:invalid#TemplFailedSanitizationURL`. `hx-get` keeps the plain string, because htmx is already limited to same-origin requests (`selfRequestsOnly`).
- A retry URL never echoes a raw query value. Every part comes from a parsed or validated value.
- Coordinates in a retry URL are formatted exactly (`strconv.FormatFloat(v, 'f', -1, 64)`), not rounded. Rounding could move a point across a 0.25° cell boundary, so the retry would ask for a different cell.
- Regenerate templ output with `go tool templ generate`. CI fails if the generated `*_templ.go` files are stale.

## Review Focus

1. **Retry must request the same cell.** A point such as `lat=38.49996` must not become `38.5000`. Task 1 adds a boundary row that pins exact formatting.
2. **A junk or oversized `place`** (`place=abc`, `place=99999999999`). It is dropped from the retry URL, not echoed. Task 1's table pins this.
3. **A stale or malformed `date`.** It is dropped, the same way item links drop it (`validDate`). Task 1's table pins this.
4. **The shed path (`overloaded`)** also builds a retry URL. It must use the same helper as the 503 path. Task 2 changes every call site, and `gate_test.go` covers the shed response.
5. **A panic under `/ingredient/<slug>` with a slug that needs escaping** (for example a space or `%2F`). `retryPath` must return an escaped path, never the raw text. Task 1 pins this.

---

### Task 1: Retry URL helpers

**Files:**
- Create: `internal/web/retry.go`
- Test: `internal/web/retry_test.go`

**Interfaces:**
- Consumes: `(*Server).validDate(date string) string` (`internal/web/season.go`), `ingredientURL(slug string, cell int, date string) string` (`internal/web/season.go`).
- Produces:
  - `func seasonRetryURL(lat, lng float64, placeParam, date string) string`. `placeParam` is the raw `place` query value. The helper keeps it only if it parses as an int32. `date` must already be validated (pass `s.validDate(...)`).
  - `func retryPath(path string) string` returns `/season` for `/season`, `/ingredient/<escaped rest>` for paths under `/ingredient/`, and `/` otherwise.

- [ ] **Step 1: Write the failing tests**

Create `internal/web/retry_test.go`:

```go
package web

import "testing"

func TestSeasonRetryURL(t *testing.T) {
	for _, tc := range []struct {
		name            string
		lat, lng        float64
		place, date     string
		want            string
	}{
		{"all parts", 38.72, -9.14, "1", "2026-08-15", "/season?lat=38.72&lng=-9.14&place=1&date=2026-08-15"},
		{"no place, no date", 38.72, -9.14, "", "", "/season?lat=38.72&lng=-9.14"},
		{"junk place dropped", 38.72, -9.14, "abc", "", "/season?lat=38.72&lng=-9.14"},
		{"out-of-range place dropped", 38.72, -9.14, "99999999999", "", "/season?lat=38.72&lng=-9.14"},
		{"script place dropped", 38.72, -9.14, "javascript:alert(1)", "", "/season?lat=38.72&lng=-9.14"},
		{"exact near a cell boundary", 38.49996, -9.0, "", "", "/season?lat=38.49996&lng=-9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := seasonRetryURL(tc.lat, tc.lng, tc.place, tc.date); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRetryPath(t *testing.T) {
	for in, want := range map[string]string{
		"/season":              "/season",
		"/ingredient/tomato":   "/ingredient/tomato",
		"/ingredient/a b":      "/ingredient/a%20b",
		"/ingredient/a/b":      "/ingredient/a%2Fb",
		"/places":              "/",
		"/":                    "/",
		"/seasonjunk":          "/",
		"javascript:alert(1)":  "/",
	} {
		if got := retryPath(in); got != want {
			t.Errorf("retryPath(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/web/ -run 'TestSeasonRetryURL|TestRetryPath' -count=1`
Expected: FAIL to compile: `undefined: seasonRetryURL`, `undefined: retryPath`.

- [ ] **Step 3: Implement the helpers**

Create `internal/web/retry.go`:

```go
package web

import (
	"net/url"
	"strconv"
	"strings"
)

// seasonRetryURL rebuilds a /season URL for an error panel's retry link from
// validated values only: the parsed coordinates (exact, so the retry asks for
// the same cell), the place ID if it is a valid int32, and a date that
// validDate already accepted. Nothing from the raw query is echoed.
func seasonRetryURL(lat, lng float64, placeParam, date string) string {
	u := "/season?lat=" + strconv.FormatFloat(lat, 'f', -1, 64) + "&lng=" + strconv.FormatFloat(lng, 'f', -1, 64)
	if id, err := strconv.ParseInt(placeParam, 10, 32); err == nil {
		u += "&place=" + strconv.FormatInt(id, 10)
	}
	if date != "" {
		u += "&date=" + url.QueryEscape(date)
	}
	return u
}

// retryPath is the retry link for failures that happen before the request's
// parameters are validated (no snapshot yet, or a panic): the page's own
// path with no query, or the home page.
func retryPath(path string) string {
	switch {
	case path == "/season":
		return "/season"
	case strings.HasPrefix(path, "/ingredient/"):
		return "/ingredient/" + url.PathEscape(strings.TrimPrefix(path, "/ingredient/"))
	}
	return "/"
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/web/ -run 'TestSeasonRetryURL|TestRetryPath' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/web/retry.go internal/web/retry_test.go
git commit -m "web: build retry URLs from validated values (#15)"
```

### Task 2: Use the helpers at every call site

**Files:**
- Modify: `internal/web/season.go:117,130,135` (the three `r.URL.RequestURI()` uses)
- Modify: `internal/web/ingredient.go:64,84,89`
- Modify: `internal/web/server.go:150` (the `Recover` render func in `New`)
- Test: `internal/web/season_test.go` (`TestSeasonDatabaseDown`, plus a new junk-parameter test), `internal/web/ingredient_test.go` (new panic test)

**Interfaces:**
- Consumes: `seasonRetryURL`, `retryPath` (Task 1), `ingredientURL`, `(*Server).validDate`.
- Produces: none.

- [ ] **Step 1: Write the failing tests**

In `internal/web/season_test.go`, change the expectation in `TestSeasonDatabaseDown` from
`` `hx-get="/season?lat=38.7200&lng=-9.1400&place=1&date=2026-08-15"` `` to
`` `hx-get="/season?lat=38.72&lng=-9.14&place=1&date=2026-08-15"` ``, and add after it:

```go
// #15: the retry link carries only validated values, never the raw query.
func TestSeasonRetryLinkDropsJunk(t *testing.T) {
	hs := newHarness(t)
	hs.climate.down = true
	code, body, _ := hs.get("/season?lat=38.72&lng=-9.14&place=1&date=2026-08-15&x=javascript:alert(1)&utm=%22%3E", true)
	want := `href="/season?lat=38.72&lng=-9.14&place=1&date=2026-08-15"`
	if code != 503 || !strings.Contains(body, want) || strings.Contains(body, "javascript") || strings.Contains(body, "utm") {
		t.Fatalf("%d\n%s", code, body)
	}
}
```

In `internal/web/ingredient_test.go`, add:

```go
// #15: a panic's retry link is the page path with no query.
func TestIngredientPanicRetryLinkIsPathOnly(t *testing.T) {
	hs := newHarness(t)
	hs.climate.panic = true
	code, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d&x=javascript:alert(1)", lisbonCell), true)
	if code != 500 || !strings.Contains(body, `href="/ingredient/tomato"`) || strings.Contains(body, "javascript") {
		t.Fatalf("%d\n%s", code, body)
	}
}

// #15: a lookup failure's retry link rebuilds the ingredient URL from the
// validated cell and date.
func TestIngredientRetryLinkRebuilt(t *testing.T) {
	hs := newHarness(t)
	hs.climate.down = true
	code, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d&date=2026-08-15&x=%%22junk", lisbonCell), true)
	want := fmt.Sprintf(`href="/ingredient/tomato?cell=%d&date=2026-08-15"`, lisbonCell)
	if code != 503 || !strings.Contains(body, want) || strings.Contains(body, "junk") {
		t.Fatalf("%d\n%s", code, body)
	}
}
```

Add `"fmt"` to the imports of `ingredient_test.go` if it isn't there already.

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/web/ -run 'TestSeasonDatabaseDown|TestSeasonRetryLinkDropsJunk|TestIngredientPanicRetryLinkIsPathOnly|TestIngredientRetryLinkRebuilt' -count=1`
Expected: FAIL. The bodies still contain the raw request URI.

- [ ] **Step 3: Change the call sites**

`internal/web/season.go`, in `season()`: after `month := s.monthFor(q.Get("date"))`, add

```go
	retry := seasonRetryURL(lat, lng, q.Get("place"), s.validDate(q.Get("date")))
```

Then replace each of the three `r.URL.RequestURI()` in `season()` with `retry`.

`internal/web/ingredient.go`:
- Line 64 (snapshot nil, before `cell` is parsed): `views.LoadError("", retryPath(r.URL.Path))`.
- After `date := s.validDate(q.Get("date"))`, add `retry := ingredientURL(ing.Slug, cell, date)`.
- Lines 84 and 89: replace `r.URL.RequestURI()` with `retry`.

`internal/web/server.go`, in `New`'s `Recover` render func:

```go
		s.render(w, r, http.StatusInternalServerError, "Something went wrong", "", nil, views.LoadError("", retryPath(r.URL.Path)))
```

- [ ] **Step 4: Run the web tests**

Run: `go test ./internal/web/ -count=1`
Expected: PASS. Fix any other test that asserted the old `%.4f` retry text, and only change its expected string.

- [ ] **Step 5: Commit**

```bash
git add internal/web/season.go internal/web/ingredient.go internal/web/server.go internal/web/season_test.go internal/web/ingredient_test.go
git commit -m "web: error panels link to rebuilt retry URLs, not the request URI (#15)"
```

### Task 3: `templ.URL` in every public view, with a guard

**Files:**
- Modify: `internal/web/views/season.templ:24,54`, `internal/web/views/ingredient.templ:5,35`, `internal/web/views/places.templ:7`
- Regenerate: `internal/web/views/*_templ.go`
- Test: `internal/web/views/lint_test.go` (add `TestNoSafeURLInPublicViews` and a sanitizer test)

**Interfaces:**
- Consumes: none.
- Produces: none.

- [ ] **Step 1: Write the failing tests**

Append to `internal/web/views/lint_test.go` (add `"os"`, `"path/filepath"` and `"strings"` to its imports):

```go
// #15: public views never bypass templ's URL sanitizer. Every href goes
// through templ.URL, even when the URL is built from server values.
func TestNoSafeURLInPublicViews(t *testing.T) {
	files, err := filepath.Glob("*.templ")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templ files: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "templ.SafeURL") {
			t.Errorf("%s uses templ.SafeURL; use templ.URL", f)
		}
	}
}

// The retry link is sanitized: a script URL renders as templ's placeholder.
func TestLoadErrorSanitizesRetryURL(t *testing.T) {
	var buf bytes.Buffer
	if err := LoadError("", "javascript:alert(1)").Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `href="about:invalid#TemplFailedSanitizationURL"`) {
		t.Fatalf("retry href not sanitized:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/web/views/ -run 'TestNoSafeURLInPublicViews|TestLoadErrorSanitizesRetryURL' -count=1`
Expected: FAIL. Three files use `templ.SafeURL`, and the href renders `javascript:alert(1)`.

- [ ] **Step 3: Replace `templ.SafeURL` with `templ.URL`**

In each of the five places, change `href={ templ.SafeURL(x) }` to `href={ templ.URL(x) }`, and leave `hx-get={ x }` as it is:
- `season.templ:24`: `href={ templ.URL(retryURL) }`
- `season.templ:54`: `href={ templ.URL(it.URL) }`
- `ingredient.templ:5`: `href={ templ.URL(vm.BackURL) }`
- `ingredient.templ:35`: `href={ templ.URL(p.URL) }`
- `places.templ:7`: `href={ templ.URL(it.URL) }`

Then run: `go tool templ generate`

- [ ] **Step 4: Run the views and web tests**

Run: `go test ./internal/web/... -count=1`
Expected: PASS. All server-built URLs are relative paths, which `templ.URL` keeps unchanged.

- [ ] **Step 5: Check the generated code is current**

Run: `go tool templ generate && git diff --exit-code -- internal/web/views/`
Expected: no diff after the files are staged with your changes. Only the edited `.templ` files and their `_templ.go` outputs change.

- [ ] **Step 6: Commit**

```bash
git add internal/web/views/
git commit -m "web: render public view URLs through templ.URL and forbid SafeURL (#15)"
```

### Task 4: Whole-repo check

- [ ] **Step 1: Run everything**

Run: `go vet ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 2: Run the e2e suite if Docker is available**

Run: `./e2e/run.sh`
Expected: PASS. Search, ingredient and back links are unchanged relative paths.
