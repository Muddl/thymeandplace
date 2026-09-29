# Thyme & Place - Minimum place search query - Design

- **Status:** Proposed
- **Date:** 2026-09-28
- **Source:** Architectural audit, 2026-09-27 (finding F14)
- **Related issues:** [#14](https://github.com/Muddl/thymeandplace/issues/14) (F14)
- **Amends:** core spec §11.1 (the `/places` row), §11.6 (new row: query under 2 letters), §16 (new value: minimum place query)

---

## 1. Problem

Place search caps query length but has no minimum. `places.Service.Search` (`internal/places/places.go`) cleans the query, and it skips the database only when the query is blank. It does the following:
- drops invalid UTF-8 and control characters
- trims the query
- truncates it to `MaxQueryRunes` (100)

The `/places` handler (`internal/web/places.go`) also short-circuits only a blank query. So a single letter, or punctuation alone, reaches `store.SearchPlaces`. That runs `ascii_name % $1 OR name % $1`, ordered by `greatest(similarity(...))`, over the ~150k-row GeoNames table through GIN trigram indexes.

The search box fires on `keyup changed delay:250ms` (`internal/web/views/layout.templ`), so a slow typist's first keystroke sends a one-letter query. pg_trgm ignores non-alphanumeric characters, and pads each word with two leading spaces and one trailing space (pg_trgm docs, checked via Context7). So `a` yields two trigrams and `%` yields none. These are the least selective and least useful searches.

pg_trgm's source (not its docs) makes a row a GIN candidate when shared trigrams ÷ query trigrams ≥ `similarity_threshold`, which defaults to 0.3 and isn't changed in this repo. For `a` or `ab`, one shared trigram is enough. So every place with a word starting with that letter is fetched and rechecked, to return 8 fuzzy matches that don't help.

The per-IP limit (10/s, burst 20) and the 2 s timeout bound the damage. But a few clients typing slowly on a small VPS cost more CPU than they should.

## 2. Goals and non-goals

Goals:

1. A query with fewer than 2 letters or digits never reaches the database, and never takes a public DB gate slot.
2. The cook sees why nothing is listed, and what to do: a "keep typing" hint, not "No places match".
3. Towns with two-letter names stay findable, such as Bo (Sierra Leone) and Ho (Ghana).
4. The cost of the queries that remain is measured on real data.

Non-goals:

- A cheaper two-letter query path, such as an exact name match. It is built only if the measurement (§6) says it's needed, as a follow-up issue.
- Client-side filtering. An htmx `keyup[...]` trigger condition is evaluated like `eval` (htmx docs, checked via Context7), and the site runs with `allowEval: false` under a strict CSP.
- Changing `similarity_threshold`, the result limit or the ranking.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| What counts toward the minimum | Letters and digits only (`unicode.IsLetter`, `unicode.IsNumber`), because pg_trgm ignores everything else | Runes (`a.` would pass while giving the same two trigrams as `a`). Bytes (`é` is 2 bytes) |
| Minimum | 2 | 3 (Bo, Ho and other two-letter towns become unsearchable) |
| Where it is enforced | `places.Service.Search` returns `nil, nil`, **and** the handler renders the hint without calling `Search` | Handler only (another caller of `Search` would still hit the database). Service only (the handler couldn't tell "too short" from "no match") |
| Status | 200 with a hint in the places slot | 400 (the input is valid, just incomplete; htmx would treat it as an error) |
| Normalizing | Extract the existing cleanup into exported `places.Normalize` and use it in both places | Duplicating the cleanup in the handler (the two copies would drift) |

## 4. Design

### 4.1 `internal/places/places.go`

```go
// MinQueryRunes is the fewest letters or digits a search needs. pg_trgm
// ignores every other character, and shorter queries match almost anything.
const MinQueryRunes = 2

// Normalize drops invalid UTF-8 and control characters, trims, and cuts the
// query to MaxQueryRunes: the cleanup Search applies before querying.
func Normalize(q string) string

// TooShort reports whether q has fewer than MinQueryRunes letters or digits.
func TooShort(q string) bool
```

`Search` becomes: `q = Normalize(q)`. If `q == ""` or `TooShort(q)`, it returns `nil, nil`. Otherwise it runs the query as today.

### 4.2 `internal/web`

- `views.CopyKeepTyping = "Keep typing: enter at least 2 letters of your town's name."` in `internal/web/views/models.go`.
- `templ PlacesKeepTyping()` in `internal/web/views/places.templ`: `<p class="hint">{ CopyKeepTyping }</p>`, the same shape as `PlacesPaused`.
- In the `places` handler, after the blank check: if `places.TooShort(places.Normalize(q))`, render `views.PlacesKeepTyping()` in the places slot with status 200, echoing `q` on full-page loads (as `PlacesNoMatch` does). This happens before `withDB`, so no gate slot and no `Search` call.

### 4.3 Core spec text

| § | Change |
|---|---|
| §11.1 | `/places` row: "…queries with fewer than 2 letters or digits show a keep-typing hint and don't search" |
| §11.6 | New row: "Query under 2 letters or digits / 200 / 'Keep typing: enter at least 2 letters of your town's name.'" |
| §16 | New row: "Minimum place query: 2 letters or digits" |

## 5. Implementation outline

1. `internal/places/places.go`: `MinQueryRunes`, `Normalize`, `TooShort`, and the change to `Search`. Add tests.
2. `internal/web/views`: `CopyKeepTyping` and `PlacesKeepTyping`. Regenerate templ.
3. `internal/web/places.go`: the short-query branch. `internal/web/fakes_test.go`: `fakePlaces.Search` counts its calls.
4. `e2e/tests/public.spec.ts`: the keep-typing flow.
5. Measure (§6) and record in the pull request.
6. Core spec (§4.3).

## 6. Testing

- **`TestTooShort`** (table-driven, `internal/places`):
  - too short: `a`, `é`, `1`, `%`, `--`, `a.`, ` a `, `á`
  - long enough: `bo`, `Ii`, `Aš`, `12`, `東京`, `lis`
- **`TestSearchTooShortSkipsDB`**: `Search` over a `store.DBTX` fake that fails the test on any call returns `nil, nil` for every too-short input. Follow the `noQueryDB` pattern in `internal/ingest/places_test.go`.
- **Existing search tests.** `TestSearchRanking` and `TestSearchAccentsAndTypos` pass unchanged. `TestSearchHostileInput` keeps its cases. Its one-character `%`, `_` and `\` now stop before SQL, so add `%%lis`, `li_s` and `lis\` as cases, to keep SQL-side escaping covered.
- **`TestPlacesShortQueryHint`** (`internal/web/places_test.go`):
  - `GET /places?q=l` with `HX-Request` returns 200 with `CopyKeepTyping`, and without "No places match".
  - The fake's `Search` call count stays 0.
  - The full-page load keeps `value="l"`.
- **Playwright** (`e2e/tests/public.spec.ts`): typing `l` shows the hint, and typing on to `lisb` shows `Lisbon, PT`.
- **Measurement** (in the pull request, not CI): run `EXPLAIN (ANALYZE, BUFFERS)` of the `SearchPlaces` SQL against a full cities1000 load for `a`, `bo`, `lis` and `lisbon`. If `bo` costs about as much as `a`, open a follow-up issue for a cheaper two-letter query rather than raising the minimum.

## 7. Rollout, operations and rollback

- Code only: no migration or config change. `docs/operations.md` is unaffected.
- Rollback: redeploy the previous image.

## 8. Open questions for the owner

None. The copy wording above was proposed in the issue and accepted.
