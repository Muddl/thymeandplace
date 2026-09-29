# Minimum Place Search Query Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Place search skips the database for queries with fewer than 2 letters or digits, and shows a "keep typing" hint instead of "No places match".

**Architecture:** `internal/places` gains `Normalize` (the existing cleanup, extracted), `MinQueryRunes = 2` and `TooShort`. `Search` returns `nil, nil` for a too-short query. The `/places` handler checks `TooShort(Normalize(q))` before the DB gate and renders a new `views.PlacesKeepTyping()` hint with status 200.

**Tech Stack:** Go 1.26 (`unicode`), templ, testcontainers via `internal/dbtest`, Playwright.

**Spec:** `docs/superpowers/specs/2026-09-28-place-search-minimum-design.md`. Issue [#14](https://github.com/Muddl/thymeandplace/issues/14).

## Global Constraints

- `MinQueryRunes = 2`. Only runes where `unicode.IsLetter(r) || unicode.IsNumber(r)` count.
- Copy, exactly: `CopyKeepTyping = "Keep typing: enter at least 2 letters of your town's name."`
- The hint answers with status 200, in the places slot, and echoes the query on full-page loads.
- The check is server-side only. Add no `hx-trigger` condition, because it would need `allowEval`, which is off.
- A too-short query takes no public DB gate slot and never calls `Search`.
- `Normalize` does exactly what `Search` does today: `strings.ToValidUTF8(q, "")`, drop `unicode.IsControl` runes, `strings.TrimSpace`, and truncate to `MaxQueryRunes`.

## Review Focus

1. **Letters with combining marks** (a decomposed `á` is `a` + U+0301). The combining mark isn't a letter, so a decomposed `á` alone stays too short, while `áb` passes. Task 1's table includes the decomposed form.
2. **Scripts without spaces or Latin letters** (`東京`, `Αθ`). They count by letters, so they pass. Task 1's table covers both.
3. **Whitespace and control characters around a single letter** (` a `, `a\x00`, `\ta\n`). These normalize to one letter, so they are too short. Task 1 covers them.
4. **Hostile input over 2 letters** (`%%lis`, `li_s`, `lis\`). It must still reach SQL safely, and those strings still exercise the escaping. Task 1 extends `TestSearchHostileInput`.
5. **A full-page load of `/places?q=l`** (no HTMX). The page must render with the hint and keep `value="l"` in the search box. Task 2 covers it.

---

### Task 1: `Normalize`, `TooShort`, and `Search` skips short queries

**Files:**
- Modify: `internal/places/places.go:14-68`
- Test: `internal/places/places_test.go` (`TestTooShort`, `TestNormalize`, `TestSearchTooShortSkipsDB`; extend `TestSearchHostileInput`)

**Interfaces:**
- Produces:
  - `const MinQueryRunes = 2`
  - `func Normalize(q string) string`
  - `func TooShort(q string) bool`: it counts letters and digits in `q` as given; callers pass a normalized query.
  - `Search` returns `nil, nil` when `TooShort(Normalize(q))`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/places/places_test.go` (add imports `"github.com/jackc/pgx/v5"` and `"github.com/jackc/pgx/v5/pgconn"`):

```go
func TestTooShort(t *testing.T) {
	short := []string{"", "a", "é", "á", "1", "%", "--", "a.", " a ", "á", "_a_"}
	long := []string{"bo", "Ii", "Aš", "12", "東京", "Αθ", "lis", "áb", "a1"}
	for _, q := range short {
		if !places.TooShort(places.Normalize(q)) {
			t.Errorf("TooShort(%q) = false, want true", q)
		}
	}
	for _, q := range long {
		if places.TooShort(places.Normalize(q)) {
			t.Errorf("TooShort(%q) = true, want false", q)
		}
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"  Lisbon ":  "Lisbon",
		"Lis\xffbon": "Lisbon",
		"a\x00b":     "ab",
		"\ta\n":      "a",
	} {
		if got := places.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if got := places.Normalize(strings.Repeat("é", 500)); len([]rune(got)) != places.MaxQueryRunes {
		t.Fatalf("not truncated: %d runes", len([]rune(got)))
	}
}

// noQueryDB fails the test on any SQL: short queries must never reach it.
type noQueryDB struct{ t *testing.T }

func (d noQueryDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	d.t.Fatal("unexpected Exec")
	return pgconn.CommandTag{}, nil
}
func (d noQueryDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	d.t.Fatal("unexpected Query")
	return nil, nil
}
func (d noQueryDB) QueryRow(context.Context, string, ...any) pgx.Row {
	d.t.Fatal("unexpected QueryRow")
	return nil
}

func TestSearchTooShortSkipsDB(t *testing.T) {
	s := places.NewService(noQueryDB{t})
	for _, q := range []string{"a", "é", "1", "%", "--", "a.", " a ", "_", `\`} {
		if got, err := s.Search(context.Background(), q); got != nil || err != nil {
			t.Fatalf("%q: %v %v", q, got, err)
		}
	}
}
```

Check that `noQueryDB`'s methods match `store.DBTX` (see `internal/ingest/places_test.go:15-32`). If `DBTX` has another method (such as `CopyFrom` or `SendBatch`), add it with the same `t.Fatal`.

In `TestSearchHostileInput`, add `"%%lis"`, `"li_s"` and `"lis\\"` to the query list. The one-character cases now stop before SQL, and these keep SQL escaping covered.

- [ ] **Step 2: Run them and confirm they fail**

Run: `go test ./internal/places/ -run 'TestTooShort|TestNormalize|TestSearchTooShortSkipsDB' -count=1`
Expected: FAIL to compile (`undefined: places.TooShort`).

- [ ] **Step 3: Implement**

In `internal/places/places.go`, add `MinQueryRunes = 2` to the `const` block, with the comment `// MinQueryRunes is the fewest letters or digits a search needs: pg_trgm ignores every other character, and shorter queries match almost anything.`

Add these two functions, and rewrite the top of `Search`:

```go
// Normalize drops invalid UTF-8 and control characters, trims, and cuts the
// query to MaxQueryRunes: the cleanup Search applies before querying.
func Normalize(q string) string {
	q = strings.ToValidUTF8(q, "")
	q = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, q)
	q = strings.TrimSpace(q)
	if utf8.RuneCountInString(q) > MaxQueryRunes {
		q = string([]rune(q)[:MaxQueryRunes])
	}
	return q
}

// TooShort reports whether q has fewer than MinQueryRunes letters or digits.
func TooShort(q string) bool {
	n := 0
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if n++; n >= MinQueryRunes {
				return false
			}
		}
	}
	return true
}

// Search returns up to SearchLimit places. Queries with fewer than
// MinQueryRunes letters or digits (including blank ones) return nil without
// touching the database; long queries are truncated to MaxQueryRunes.
func (s *Service) Search(ctx context.Context, q string) ([]Place, error) {
	q = Normalize(q)
	if TooShort(q) {
		return nil, nil
	}
	rows, err := store.SearchPlaces(ctx, s.db, liveView, q, SearchLimit)
	// … unchanged from here …
```

Order matters: truncation happens before `TooShort` counts, as in today's code. A 100-rune query is never too short, so this doesn't change any result.

- [ ] **Step 4: Run the places tests**

Run: `go test ./internal/places/ -count=1`
Expected: PASS, including `TestSearchRanking` and `TestSearchAccentsAndTypos`, unchanged, and the extended `TestSearchHostileInput`.

- [ ] **Step 5: Commit**

```bash
git add internal/places/
git commit -m "places: skip the search for queries under 2 letters or digits (#14)"
```

### Task 2: The keep-typing hint on `/places`

**Files:**
- Modify: `internal/web/views/models.go` (`CopyKeepTyping`), `internal/web/views/places.templ` (`PlacesKeepTyping`); regenerate templ
- Modify: `internal/web/places.go:28-33` (the short-query branch)
- Modify: `internal/web/fakes_test.go` (`fakePlaces.searches` counter)
- Modify: `internal/web/views/lint_test.go` (add the component to the CSP lint)
- Test: `internal/web/places_test.go` (`TestPlacesShortQueryHint`)
- Modify: `e2e/tests/public.spec.ts`

**Interfaces:**
- Consumes: `places.Normalize`, `places.TooShort` (Task 1).
- Produces: `views.CopyKeepTyping` and the component `views.PlacesKeepTyping()`.

- [ ] **Step 1: Write the failing test**

In `internal/web/fakes_test.go`, add `searches atomic.Int32` to `fakePlaces`, and make `Search` begin with `f.searches.Add(1)`.

Append to `internal/web/places_test.go`:

```go
// #14: a query under 2 letters gets a hint, not a search.
func TestPlacesShortQueryHint(t *testing.T) {
	hs := newHarness(t)
	for _, q := range []string{"l", "%25", "a.", "%C3%A9"} { // l, %, a., é
		code, body, _ := hs.get("/places?q="+q, true)
		if code != 200 || !strings.Contains(body, views.CopyKeepTyping) || strings.Contains(body, "No places match") {
			t.Fatalf("q=%s: %d\n%s", q, code, body)
		}
	}
	if n := hs.places.searches.Load(); n != 0 {
		t.Fatalf("Search called %d times for short queries", n)
	}
	code, body, _ := hs.get("/places?q=l", false)
	if code != 200 || !strings.Contains(body, views.CopyKeepTyping) || !strings.Contains(body, `value="l"`) {
		t.Fatalf("full page: %d\n%s", code, body)
	}
	if _, body, _ := hs.get("/places?q=li", true); strings.Contains(body, views.CopyKeepTyping) {
		t.Fatalf("two letters must search:\n%s", body)
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/web/ -run TestPlacesShortQueryHint -count=1`
Expected: FAIL to compile (`undefined: views.CopyKeepTyping`).

- [ ] **Step 3: Implement**

In `internal/web/views/models.go`, add to the copy constants:

```go
	CopyKeepTyping         = "Keep typing: enter at least 2 letters of your town's name."
```

In `internal/web/views/places.templ`, after `PlacesPaused`:

```templ
templ PlacesKeepTyping() {
	<p class="hint">{ CopyKeepTyping }</p>
}
```

In `internal/web/views/lint_test.go`, add `"PlacesKeepTyping": PlacesKeepTyping(),` to the `components` map.

In `internal/web/places.go` `places`, after the blank-query `if` block:

```go
	// Too short to search usefully (#14): answer with a hint and no DB work.
	if places.TooShort(places.Normalize(q)) {
		s.render(w, r, http.StatusOK, "Search", q, views.PlacesKeepTyping(), nil)
		return
	}
```

Run: `go tool templ generate`

- [ ] **Step 4: Run the web tests**

Run: `go test ./internal/web/... -count=1`
Expected: PASS. `TestPlacesMatches` (`lis`) and `TestPlacesNoMatch` (`zzz`) are 3 letters, so they are unaffected. `TestPlacesEscapesQuery` has more than 2 letters.

- [ ] **Step 5: Playwright**

In `e2e/tests/public.spec.ts`, add after test 1:

```ts
test("a one-letter search asks for more", async ({ page }) => {
  await page.goto("/");
  const box = page.getByLabel("Find your town");
  await box.pressSequentially("l", { delay: 50 });
  await expect(page.getByText("Keep typing: enter at least 2 letters of your town's name.")).toBeVisible();
  await box.pressSequentially("isb", { delay: 50 });
  await expect(page.getByRole("link", { name: "Lisbon, PT" })).toBeVisible();
});
```

Run: `./e2e/run.sh`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/web/ e2e/tests/public.spec.ts
git commit -m "web: ask for at least 2 letters instead of searching one (#14)"
```

### Task 3: Measure, and update the core spec

**Files:**
- Modify: `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` §11.1, §11.6, §16

**Interfaces:** documentation and a measurement only.

- [ ] **Step 1: Measure on a full cities1000 load**

This needs the dev stack with real GeoNames loaded (`docs/calibration-baseline.md`, "How this was measured"). For each of `a`, `bo`, `lis` and `lisbon`, run:

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT place_id, name, admin1, country, population, ST_Y(geom::geometry), ST_X(geom::geometry)
FROM place WHERE ascii_name % '<q>' OR name % '<q>'
ORDER BY greatest(similarity(ascii_name, '<q>'), similarity(name, '<q>')) DESC, population DESC, place_id
LIMIT 8;
```

Copy the exact SQL of `store.SearchPlaces` from `internal/store/handwritten.go`, not this outline, if they differ.
Expected: `a` is the most expensive. Record execution time and shared buffers for all four in the pull request description. If `bo` costs about as much as `a` (within 2×), open a follow-up issue, "places: cheaper two-letter search", and link it. Don't raise the minimum.

- [ ] **Step 2: Core spec**

- §11.1's `/places` row gains: "…queries with fewer than 2 letters or digits show a keep-typing hint and don't search".
- §11.6 gains the row: "Query under 2 letters or digits / 200 / 'Keep typing: enter at least 2 letters of your town's name.'"
- §16 gains the row: "Minimum place query: 2 letters or digits".

- [ ] **Step 3: Commit**

```bash
git add docs/
git commit -m "docs: minimum place query of 2 letters or digits (#14)"
```
