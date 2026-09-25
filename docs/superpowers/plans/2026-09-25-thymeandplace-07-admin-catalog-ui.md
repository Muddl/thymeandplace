# Thyme & Place — Plan 7: Admin Catalog UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the owner the catalog screens: list with search and filters; add/edit form whose field set follows `kind` (via HTMX); delete; JSON import; a preview page with field diff, calibration before/after and sample-city season bars; apply (with override and typed-slug confirmation); export; and history.

**Architecture:** Every screen lives in `internal/admin` behind `requireAuth`. Handlers never write the catalog directly: form saves, deletes and imports all build a `changeset.Changeset` and call `changeset.Service.Preview`/`Apply` (Plan 4). Form parsing is a pure function from `url.Values` to `catalog.Ingredient` plus parse problems, so it is unit-tested without HTTP. Views are `templ`; per-field errors reuse the `catalog.Problem.Field` names.

**Tech Stack:** Go ≥ 1.25, templ, HTMX 2.0.4, sqlc.

**Spec:** `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (§9.1, §9.2, §9.4, §11.6 admin errors)

**Depends on:** Plans 1–6.

## Global Constraints

- Routes (§9.1): `GET /admin` (list: search by name/slug, filter by category and kind), `GET /admin/ingredients/new`, `GET /admin/ingredients/{slug}`, `GET /admin/ingredients/fields?kind=` (HTMX field-set swap), `POST /admin/ingredients/{slug}/preview` (`{slug}` is `new` for creation), `POST /admin/ingredients/{slug}/delete/preview`, `GET|POST /admin/import`, `POST /admin/changesets/{id}/apply`, `GET /admin/export`, `GET /admin/history?slug=&before=`.
- Every edit is a changeset: build → validate → preview → apply. Validation errors show inline next to their fields, and nothing is stored.
- Delete confirmation requires typing the slug. Apply requires ticking **"Apply despite lower calibration score"** when the score drops by more than 0.02.
- A stale preview shows exactly: `The catalog changed since this preview. Preview again.`
- Import: multipart field `file`, maximum **5 MB**; upsert only; unknown `format` rejected with a clear message.
- Export: `Content-Disposition: attachment; filename="thymeandplace-catalog-YYYYMMDD.json"`, same format as import.
- History: newest first, 100 per page, filter by slug.
- Actor recorded on apply: `owner (<session method>)`, e.g. `owner (passkey)`.
- The slug `new` is reserved (it collides with the "new ingredient" route), and creating an ingredient whose slug already exists is refused with "already exists; edit it instead".
- Regions textarea format: one region per line, `<ecoregion_id>: <months> [| peak <months>]`, months as comma-separated numbers, e.g. `25: 1,2,3,4 | peak 2,3`. Pairings: comma-separated slugs.
- Commit after every task. Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Changing an ingredient's kind** (annual → perennial fruit) submits only the new kind's fields. The preview shows the old annual-only values being cleared, not kept silently. Pinned in Task 2.
2. **Blank vs zero vs junk in numeric fields**: blank means not set, `0` stays 0, and `abc` or `1e400` gives an inline "must be a number" error, never a 500. Pinned in Task 1.
3. **HTML and Unicode in tips** (`<b>ripe</b> & fragrant`, `Crème fraîche`) are stored verbatim and rendered escaped everywhere, including the diff. Pinned in Task 3.
4. **Creating a "new" ingredient whose slug already exists** is refused instead of silently overwriting the existing one. Pinned in Task 2.
5. **Apply re-submitted from the browser's back button** gets the friendly "expired or already applied" page, and the catalog changes once. Pinned in Task 4.

---

## File Structure

```
internal/store/queries/history.sql
internal/admin/form.go, form_test.go        # url.Values ↔ Ingredient, regions/pairings syntax
internal/admin/catalog.go                   # list, form, fields partial, previews, apply, import, export, history
internal/admin/catalog_test.go
internal/admin/catalogenv_test.go           # env with changeset service + seeded catalog
internal/admin/views/catalog.templ          # list, form, kind fields
internal/admin/views/preview.templ          # preview + apply
internal/admin/views/importexport.templ     # import page, history
internal/admin/views/models.go              # + catalog view models
internal/admin/admin.go                     # Deps additions, registerCatalog, writeRoutes, 413 check
cmd/server/app.go                           # pass changeset service to admin
```

---

### Task 1: Form parsing and formatting

**Files:**
- Create: `internal/admin/form.go`, `internal/admin/form_test.go`

**Interfaces:**
- Consumes: `catalog.Ingredient`, `catalog.Problem`, `ocean.EcoregionID`.
- Produces:
  - `func parseIngredientForm(v url.Values) (catalog.Ingredient, []catalog.Problem)`: parse problems use the same field names as `Ingredient.Validate`
  - `func formValues(ing catalog.Ingredient) map[string]string`: the inverse, for prefilling
  - `func parseRegions(text string) ([]catalog.SeafoodRegion, []catalog.Problem)`; `func formatRegions([]catalog.SeafoodRegion) string`
  - `func parsePairings(text string) []string`
  - `var kindFields map[catalog.Kind][]string`: which numeric/choice fields each kind shows

- [ ] **Step 1: Write the failing tests**

Create `internal/admin/form_test.go`:

```go
package admin

import (
	"net/url"
	"reflect"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
)

func hasField(ps []catalog.Problem, field string) bool {
	for _, p := range ps {
		if p.Field == field {
			return true
		}
	}
	return false
}

func TestParseAnnual(t *testing.T) {
	v := url.Values{
		"slug": {"tomato"}, "name": {"Tomato"}, "category": {"vegetable"}, "kind": {"annual"},
		"frost": {"tender"}, "grow_temp_min_c": {"16"}, "grow_temp_max_c": {"30.5"}, "months_to_harvest": {"3"},
		"storage_months": {"0"}, "tip_choose": {"c"}, "tip_store": {"s"}, "note": {""},
		"pairings": {" basil, garlic ,, olive-oil "},
	}
	ing, ps := parseIngredientForm(v)
	if len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
	if *ing.GrowTempMaxC != 30.5 || *ing.MonthsToHarvest != 3 || ing.StorageMonths != 0 || ing.ChillMonthsMin != nil ||
		!reflect.DeepEqual(ing.Pairings, []string{"basil", "garlic", "olive-oil"}) {
		t.Fatalf("ing = %+v", ing)
	}
}

// Review Focus 2.
func TestParseNumbers(t *testing.T) {
	base := url.Values{"slug": {"x"}, "name": {"X"}, "category": {"vegetable"}, "kind": {"annual"}, "storage_months": {""}}
	for _, bad := range []string{"abc", "1e400", "NaN", "Inf"} {
		v := cloneValues(base)
		v.Set("grow_temp_min_c", bad)
		if _, ps := parseIngredientForm(v); !hasField(ps, "grow_temp_min_c") {
			t.Fatalf("%q accepted", bad)
		}
	}
	v := cloneValues(base)
	v.Set("months_to_harvest", "2.5")
	if _, ps := parseIngredientForm(v); !hasField(ps, "months_to_harvest") {
		t.Fatal("fractional month accepted")
	}
	v = cloneValues(base)
	v.Set("grow_temp_min_c", "0")
	ing, ps := parseIngredientForm(v)
	if len(ps) != 0 || ing.GrowTempMinC == nil || *ing.GrowTempMinC != 0 || ing.GrowTempMaxC != nil || ing.StorageMonths != 0 {
		t.Fatalf("zero/blank handling: %+v %v", ing, ps)
	}
}

func TestRegionsRoundTrip(t *testing.T) {
	rs, ps := parseRegions("25: 1,2,3,4 | peak 2,3\n\n 30 : 10, 11 \n")
	if len(ps) != 0 || len(rs) != 2 || rs[0].EcoregionID != 25 || !reflect.DeepEqual(rs[0].PeakMonths, []int{2, 3}) || rs[1].PeakMonths != nil {
		t.Fatalf("rs=%+v ps=%v", rs, ps)
	}
	if got := formatRegions(rs); got != "25: 1,2,3,4 | peak 2,3\n30: 10,11" {
		t.Fatalf("format = %q", got)
	}
	for _, bad := range []string{"25 1,2", "x: 1", "25: a", "25: 1 | top 2"} {
		if _, ps := parseRegions(bad); !hasField(ps, "regions[0].months") {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestFormValuesRoundTrip(t *testing.T) {
	for _, ing := range catalogtest.SmallCatalog() {
		v := url.Values{}
		for k, val := range formValues(ing) {
			v.Set(k, val)
		}
		got, ps := parseIngredientForm(v)
		if len(ps) != 0 {
			t.Fatalf("%s: %v", ing.Slug, ps)
		}
		if !reflect.DeepEqual(catalog.Normalize(got), catalog.Normalize(ing)) {
			t.Fatalf("%s:\n got %+v\nwant %+v", ing.Slug, catalog.Normalize(got), catalog.Normalize(ing))
		}
	}
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run 'Parse|Regions|FormValues'`
Expected: FAIL to compile: `undefined: parseIngredientForm`.

- [ ] **Step 3: Implement**

Create `internal/admin/form.go`:

```go
package admin

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/ocean"
)

// kindFields lists the kind-specific inputs each kind shows.
var kindFields = map[catalog.Kind][]string{
	catalog.KindAnnual:         {"frost", "grow_temp_min_c", "grow_temp_max_c", "months_to_harvest", "storage_months"},
	catalog.KindPerennialFruit: {"frost", "chill_months_min", "warmup_temp_c", "months_bloom_to_harvest", "harvest_span_months", "storage_months"},
	catalog.KindYearRound:      {},
	catalog.KindWildMushroom:   {"fruiting_temp_min_c", "fruiting_temp_max_c", "min_precip_mm", "storage_months"},
	catalog.KindSeafood:        {"regions"},
}

func parseFloat(v url.Values, field string, ps *[]catalog.Problem, slug string) *float64 {
	s := strings.TrimSpace(v.Get(field))
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		*ps = append(*ps, catalog.Problem{Slug: slug, Field: field, Message: "must be a number"})
		return nil
	}
	return &f
}

func parseInt(v url.Values, field string, ps *[]catalog.Problem, slug string) *int {
	s := strings.TrimSpace(v.Get(field))
	if s == "" {
		return nil
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		*ps = append(*ps, catalog.Problem{Slug: slug, Field: field, Message: "must be a whole number"})
		return nil
	}
	return &i
}

func parsePairings(text string) []string {
	var out []string
	for _, p := range strings.Split(text, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var regionLine = regexp.MustCompile(`^\s*(\d+)\s*:\s*([0-9,\s]+?)\s*(?:\|\s*peak\s*([0-9,\s]*))?\s*$`)

func parseMonths(s string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		m, err := strconv.Atoi(p)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func parseRegions(text string) ([]catalog.SeafoodRegion, []catalog.Problem) {
	var rs []catalog.SeafoodRegion
	var ps []catalog.Problem
	i := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		field := fmt.Sprintf("regions[%d].months", i)
		i++
		m := regionLine.FindStringSubmatch(line)
		if m == nil {
			ps = append(ps, catalog.Problem{Field: field, Message: fmt.Sprintf("%q should look like \"25: 1,2,3 | peak 2\"", strings.TrimSpace(line))})
			continue
		}
		id, _ := strconv.Atoi(m[1])
		months, err1 := parseMonths(m[2])
		peak, err2 := parseMonths(m[3])
		if err1 != nil || err2 != nil {
			ps = append(ps, catalog.Problem{Field: field, Message: "months must be numbers 1–12"})
			continue
		}
		rs = append(rs, catalog.SeafoodRegion{EcoregionID: ocean.EcoregionID(id), Months: months, PeakMonths: peak})
	}
	return rs, ps
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}

func formatRegions(rs []catalog.SeafoodRegion) string {
	lines := make([]string, len(rs))
	for i, r := range rs {
		lines[i] = fmt.Sprintf("%d: %s", r.EcoregionID, joinInts(r.Months))
		if len(r.PeakMonths) > 0 {
			lines[i] += " | peak " + joinInts(r.PeakMonths)
		}
	}
	return strings.Join(lines, "\n")
}

// parseIngredientForm reads every field that was submitted. Inputs for other
// kinds are not in the form (HTMX swaps them out), so they stay nil.
func parseIngredientForm(v url.Values) (catalog.Ingredient, []catalog.Problem) {
	slug := strings.TrimSpace(v.Get("slug"))
	var ps []catalog.Problem
	ing := catalog.Ingredient{
		Slug: slug, Name: strings.TrimSpace(v.Get("name")),
		Category: catalog.Category(v.Get("category")), Kind: catalog.Kind(v.Get("kind")),
		Frost:     catalog.Frost(v.Get("frost")),
		TipChoose: strings.TrimSpace(v.Get("tip_choose")), TipStore: strings.TrimSpace(v.Get("tip_store")),
		Note:      strings.TrimSpace(v.Get("note")), Pairings: parsePairings(v.Get("pairings")),
	}
	ing.GrowTempMinC = parseFloat(v, "grow_temp_min_c", &ps, slug)
	ing.GrowTempMaxC = parseFloat(v, "grow_temp_max_c", &ps, slug)
	ing.MonthsToHarvest = parseInt(v, "months_to_harvest", &ps, slug)
	if sm := parseInt(v, "storage_months", &ps, slug); sm != nil {
		ing.StorageMonths = *sm
	}
	ing.ChillMonthsMin = parseInt(v, "chill_months_min", &ps, slug)
	ing.WarmupTempC = parseFloat(v, "warmup_temp_c", &ps, slug)
	ing.MonthsBloomToHarvest = parseInt(v, "months_bloom_to_harvest", &ps, slug)
	ing.HarvestSpanMonths = parseInt(v, "harvest_span_months", &ps, slug)
	ing.FruitingTempMinC = parseFloat(v, "fruiting_temp_min_c", &ps, slug)
	ing.FruitingTempMaxC = parseFloat(v, "fruiting_temp_max_c", &ps, slug)
	ing.MinPrecipMM = parseFloat(v, "min_precip_mm", &ps, slug)
	rs, rps := parseRegions(v.Get("regions"))
	for i := range rps {
		rps[i].Slug = slug
	}
	ing.Regions, ps = rs, append(ps, rps...)
	return ing, ps
}

func fstr(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

func istr(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

// formValues is the inverse of parseIngredientForm, for prefilling the form.
func formValues(ing catalog.Ingredient) map[string]string {
	return map[string]string{
		"slug": ing.Slug, "name": ing.Name, "category": string(ing.Category), "kind": string(ing.Kind),
		"frost": string(ing.Frost), "grow_temp_min_c": fstr(ing.GrowTempMinC), "grow_temp_max_c": fstr(ing.GrowTempMaxC),
		"months_to_harvest": istr(ing.MonthsToHarvest), "storage_months": strconv.Itoa(ing.StorageMonths),
		"chill_months_min": istr(ing.ChillMonthsMin), "warmup_temp_c": fstr(ing.WarmupTempC),
		"months_bloom_to_harvest": istr(ing.MonthsBloomToHarvest), "harvest_span_months": istr(ing.HarvestSpanMonths),
		"fruiting_temp_min_c": fstr(ing.FruitingTempMinC), "fruiting_temp_max_c": fstr(ing.FruitingTempMaxC),
		"min_precip_mm": fstr(ing.MinPrecipMM), "tip_choose": ing.TipChoose, "tip_store": ing.TipStore,
		"note": ing.Note, "pairings": strings.Join(ing.Pairings, ", "), "regions": formatRegions(ing.Regions),
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/admin/ -run 'Parse|Regions|FormValues' -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/form.go internal/admin/form_test.go
git commit -m "feat(admin): ingredient form parsing with regions/pairings syntax

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Catalog list, ingredient form and kind field swap

**Files:**
- Create: `internal/admin/catalog.go`, `internal/admin/views/catalog.templ`, `internal/admin/catalogenv_test.go`, `internal/admin/catalog_test.go`
- Modify: `internal/admin/admin.go` (Deps additions; remove the `registerCatalog` no-op and the placeholder `home` route), `internal/admin/login.go` (delete `home`), `internal/admin/views/models.go` (catalog view models; delete `Home` from `layout.templ`)

**Interfaces:**
- Consumes: `changeset.Service`, `catalog.Load`, form helpers (Task 1).
- Produces:
  - `admin.Deps` gains `Changes *changeset.Service`, `Load func(context.Context) (*catalog.Snapshot, error)` and `Pool *pgxpool.Pool` (history queries)
  - `(*Admin).registerCatalog()` registering the routes of this plan
  - `views.CatalogPage(CatalogVM)`, `views.CatalogRows(CatalogVM)` (HTMX partial), `views.FormPage(FormVM)`, `views.KindFields(FormVM)`
  - `func (a *Admin) problemsToErrors(ps []catalog.Problem) map[string][]string` (regions problems collapse onto the `regions` key)

- [ ] **Step 1: Write the test environment and failing tests**

Create `internal/admin/catalogenv_test.go`:

```go
package admin

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/season"
	"thymeandplace/internal/store"
)

// catalogEnv is an admin env with the reference fixture, SmallCatalog and a
// changeset service scoring against the test fixture.
func catalogEnv(t *testing.T) (*env, *client) {
	t.Helper()
	e := newEnv(t, true)
	ctx := context.Background()
	dbtest.LoadReference(t, e.pool)
	tx, _ := e.pool.Begin(ctx)
	if err := catalog.Write(ctx, store.New(tx), catalogtest.SmallCatalog(), nil); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)

	fx, err := calibration.LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	n := climatetest.Temperate()
	cases := []calibration.Case{{Fixture: fx[0], Input: season.Input{Normals: &n}}}
	svc := changeset.NewService(e.pool, ocean.NewService(e.pool), func(context.Context) ([]calibration.Case, error) { return cases, nil })
	e.h = New(Deps{
		Auth: e.auth, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Changes: svc, Pool: e.pool,
		Load: func(ctx context.Context) (*catalog.Snapshot, error) { return catalog.Load(ctx, e.pool) },
	})
	c, _ := e.enrolled(t)
	return e, c
}

func (e *env) snap(t *testing.T) *catalog.Snapshot {
	t.Helper()
	s, err := catalog.Load(context.Background(), e.pool)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
```

Create `internal/admin/catalog_test.go`:

```go
package admin

import (
	"html"
	"net/url"
	"strings"
	"testing"
)

func TestCatalogListAndFilters(t *testing.T) {
	_, c := catalogEnv(t)
	rec := c.get("/admin")
	b := html.UnescapeString(rec.Body.String())
	if rec.Code != 200 || !strings.Contains(b, "Tomato") || !strings.Contains(b, "Atlantic cod") || !strings.Contains(b, "11 ingredients") {
		t.Fatalf("%d\n%s", rec.Code, b)
	}
	b = html.UnescapeString(c.get("/admin?q=tom").Body.String())
	if !strings.Contains(b, "Tomato") || strings.Contains(b, "Banana") {
		t.Fatal("search by name failed")
	}
	b = html.UnescapeString(c.get("/admin?category=seafood").Body.String())
	if !strings.Contains(b, "Mackerel") || strings.Contains(b, "Tomato") {
		t.Fatal("category filter failed")
	}
	b = html.UnescapeString(c.get("/admin?kind=perennial_fruit").Body.String())
	if !strings.Contains(b, "Apple") || strings.Contains(b, "Kale") {
		t.Fatal("kind filter failed")
	}
	rec = c.do("GET", "/admin?q=kale", nil, map[string]string{"HX-Request": "true"})
	if strings.Contains(rec.Body.String(), "<html") || !strings.Contains(rec.Body.String(), "Kale") {
		t.Fatal("HTMX list request should return rows only")
	}
}

func TestEditFormPrefilledAndKindSwap(t *testing.T) {
	_, c := catalogEnv(t)
	b := c.get("/admin/ingredients/tomato").Body.String()
	for _, want := range []string{`name="grow_temp_min_c" value="16"`, `name="grow_temp_max_c" value="30"`, `value="basil"`, `hx-get="/admin/ingredients/fields"`} {
		if !strings.Contains(b, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(b, `name="chill_months_min"`) {
		t.Fatal("annual form must not show perennial fields")
	}
	part := c.do("GET", "/admin/ingredients/fields?kind=perennial_fruit&frost=hardy", nil, map[string]string{"HX-Request": "true"}).Body.String()
	if !strings.Contains(part, `name="chill_months_min"`) || strings.Contains(part, `name="grow_temp_min_c"`) || strings.Contains(part, "<html") {
		t.Fatalf("kind partial:\n%s", part)
	}
	if !strings.Contains(c.get("/admin/ingredients/new").Body.String(), `name="slug"`) {
		t.Fatal("new form needs an editable slug")
	}
	if rec := c.get("/admin/ingredients/unicorn"); rec.Code != 404 {
		t.Fatalf("unknown slug: %d", rec.Code)
	}
}

func tomatoForm() url.Values {
	return url.Values{
		"slug": {"tomato"}, "name": {"Tomato"}, "category": {"vegetable"}, "kind": {"annual"}, "frost": {"tender"},
		"grow_temp_min_c": {"16"}, "grow_temp_max_c": {"30"}, "months_to_harvest": {"3"}, "storage_months": {"0"},
		"tip_choose": {"Heavy for its size, glossy, fragrant at the stem."}, "tip_store": {"Stem-side down at room temperature."},
		"pairings": {"basil"},
	}
}

func TestInvalidFormShowsInlineErrorsAndStoresNothing(t *testing.T) {
	e, c := catalogEnv(t)
	f := tomatoForm()
	f.Set("grow_temp_max_c", "10")
	f.Set("months_to_harvest", "abc")
	rec := c.postForm("/admin/ingredients/tomato/preview", f)
	b := html.UnescapeString(rec.Body.String())
	if rec.Code != 422 || !strings.Contains(b, "must be greater than grow_temp_min_c") || !strings.Contains(b, "must be a whole number") ||
		!strings.Contains(b, `value="10"`) {
		t.Fatalf("%d\n%s", rec.Code, b)
	}
	var n int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM pending_changeset`).Scan(&n)
	if n != 0 {
		t.Fatal("invalid form stored a pending changeset")
	}
}

// Review Focus 4.
func TestNewWithExistingOrReservedSlug(t *testing.T) {
	_, c := catalogEnv(t)
	f := tomatoForm()
	rec := c.postForm("/admin/ingredients/new/preview", f)
	if rec.Code != 422 || !strings.Contains(html.UnescapeString(rec.Body.String()), "already exists; edit it instead") {
		t.Fatalf("existing slug: %d", rec.Code)
	}
	f.Set("slug", "new")
	if rec := c.postForm("/admin/ingredients/new/preview", f); rec.Code != 422 || !strings.Contains(rec.Body.String(), "reserved") {
		t.Fatalf("reserved slug: %d", rec.Code)
	}
}

// Review Focus 1.
func TestKindChangeClearsOldFields(t *testing.T) {
	_, c := catalogEnv(t)
	f := url.Values{
		"slug": {"tomato"}, "name": {"Tomato"}, "category": {"fruit"}, "kind": {"perennial_fruit"}, "frost": {"tender"},
		"chill_months_min": {"0"}, "warmup_temp_c": {"18"}, "months_bloom_to_harvest": {"3"}, "harvest_span_months": {"3"},
		"storage_months": {"0"}, "tip_choose": {"c"}, "tip_store": {"s"}, "pairings": {"basil"},
	}
	rec := c.postForm("/admin/ingredients/tomato/preview", f)
	b := html.UnescapeString(rec.Body.String())
	if rec.Code != 200 || !strings.Contains(b, "grow_temp_min_c") || !strings.Contains(b, "months_to_harvest") {
		t.Fatalf("preview should list cleared annual fields: %d\n%s", rec.Code, b)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run 'Catalog|EditForm|InvalidForm|NewWith|KindChange'`
Expected: FAIL to compile: `unknown field Changes in struct literal of type Deps`.

- [ ] **Step 3: Add view models and templates**

Append to `internal/admin/views/models.go`:

```go
type Option struct {
	Value, Label string
	Selected     bool
}

type CatalogRow struct{ Slug, Name, Category, Kind string }

type CatalogVM struct {
	Layout               LayoutVM
	Q, Category, Kind    string
	Categories, Kinds    []Option
	Rows                 []CatalogRow
	Total                int
	Message              string
}

type FormVM struct {
	Layout     LayoutVM
	IsNew      bool
	Action     string
	Values     map[string]string
	Errors     map[string][]string
	Kind       string
	Fields     []string // kind-specific fields to show
	Categories []Option
	Kinds      []Option
	Message    string
}

func (vm FormVM) Has(field string) bool {
	for _, f := range vm.Fields {
		if f == field {
			return true
		}
	}
	return false
}
```

Create `internal/admin/views/catalog.templ`:

```templ
package views

templ CatalogPage(vm CatalogVM) {
	@Layout(vm.Layout) {
		<h1>Catalog</h1>
		if vm.Message != "" {
			<p class="ok">{ vm.Message }</p>
		}
		<form method="get" action="/admin" hx-get="/admin" hx-target="#rows" hx-trigger="input changed delay:250ms from:#q, change" hx-push-url="true">
			<input id="q" type="search" name="q" value={ vm.Q } placeholder="Search name or slug"/>
			<select name="category">
				<option value="">All categories</option>
				for _, o := range vm.Categories {
					<option value={ o.Value } selected?={ o.Selected }>{ o.Label }</option>
				}
			</select>
			<select name="kind">
				<option value="">All kinds</option>
				for _, o := range vm.Kinds {
					<option value={ o.Value } selected?={ o.Selected }>{ o.Label }</option>
				}
			</select>
			<button type="submit">Filter</button>
			<a href="/admin/ingredients/new">Add ingredient</a>
		</form>
		<div id="rows">
			@CatalogRows(vm)
		</div>
	}
}

templ CatalogRows(vm CatalogVM) {
	<p>{ plural(vm.Total, "ingredient") }</p>
	<table>
		<thead><tr><th>Name</th><th>Slug</th><th>Category</th><th>Kind</th></tr></thead>
		<tbody>
			for _, r := range vm.Rows {
				<tr>
					<td><a href={ templ.SafeURL("/admin/ingredients/" + r.Slug) }>{ r.Name }</a></td>
					<td>{ r.Slug }</td>
					<td>{ r.Category }</td>
					<td>{ r.Kind }</td>
				</tr>
			}
		</tbody>
	</table>
}

templ fieldErrors(vm FormVM, field string) {
	for _, msg := range vm.Errors[field] {
		<span class="field-error">{ msg }</span>
	}
}

templ textInput(vm FormVM, field, label string) {
	<label>
		{ label }
		<input type="text" name={ field } value={ vm.Values[field] }/>
		@fieldErrors(vm, field)
	</label>
}

templ FormPage(vm FormVM) {
	@Layout(vm.Layout) {
		if vm.IsNew {
			<h1>Add ingredient</h1>
		} else {
			<h1>Edit { vm.Values["name"] }</h1>
		}
		if vm.Message != "" {
			<p class="error">{ vm.Message }</p>
		}
		@fieldErrors(vm, "")
		<form method="post" action={ templ.SafeURL(vm.Action) }>
			<input type="hidden" name="csrf" value={ vm.Layout.CSRF }/>
			if vm.IsNew {
				@textInput(vm, "slug", "Slug (lowercase, hyphens)")
			} else {
				<input type="hidden" name="slug" value={ vm.Values["slug"] }/>
				<p>Slug: <code>{ vm.Values["slug"] }</code></p>
			}
			@textInput(vm, "name", "Name")
			<label>
				Category
				<select name="category">
					for _, o := range vm.Categories {
						<option value={ o.Value } selected?={ o.Selected }>{ o.Label }</option>
					}
				</select>
				@fieldErrors(vm, "category")
			</label>
			<label>
				Kind
				<select name="kind" hx-get="/admin/ingredients/fields" hx-target="#kind-fields" hx-include="closest form" hx-swap="outerHTML">
					for _, o := range vm.Kinds {
						<option value={ o.Value } selected?={ o.Selected }>{ o.Label }</option>
					}
				</select>
				@fieldErrors(vm, "kind")
			</label>
			@KindFields(vm)
			<label>
				How to choose
				<textarea name="tip_choose" rows="2">{ vm.Values["tip_choose"] }</textarea>
				@fieldErrors(vm, "tip_choose")
			</label>
			<label>
				How to store
				<textarea name="tip_store" rows="2">{ vm.Values["tip_store"] }</textarea>
				@fieldErrors(vm, "tip_store")
			</label>
			@textInput(vm, "pairings", "Pairs with (slugs, comma-separated)")
			<label>
				Note
				<textarea name="note" rows="2">{ vm.Values["note"] }</textarea>
			</label>
			<button type="submit">Preview changes</button>
		</form>
		if !vm.IsNew {
			<h2>Delete</h2>
			<form method="post" action={ templ.SafeURL("/admin/ingredients/" + vm.Values["slug"] + "/delete/preview") }>
				<input type="hidden" name="csrf" value={ vm.Layout.CSRF }/>
				<button type="submit">Preview delete</button>
			</form>
		}
	}
}

templ KindFields(vm FormVM) {
	<fieldset id="kind-fields">
		<legend>Season rules for { vm.Kind }</legend>
		if vm.Has("frost") {
			<label>
				Frost tolerance
				<select name="frost">
					<option value="tender" selected?={ vm.Values["frost"] == "tender" }>tender</option>
					<option value="hardy" selected?={ vm.Values["frost"] == "hardy" }>hardy</option>
				</select>
				@fieldErrors(vm, "frost")
			</label>
		}
		if vm.Has("grow_temp_min_c") {
			@textInput(vm, "grow_temp_min_c", "Minimum growing mean temperature (°C)")
			@textInput(vm, "grow_temp_max_c", "Maximum growing mean temperature (°C)")
			@textInput(vm, "months_to_harvest", "Months from planting to harvest")
		}
		if vm.Has("chill_months_min") {
			@textInput(vm, "chill_months_min", "Minimum chill months (mean below 7 °C)")
			@textInput(vm, "warmup_temp_c", "Spring warm-up temperature (°C)")
			@textInput(vm, "months_bloom_to_harvest", "Months from warm-up to harvest")
			@textInput(vm, "harvest_span_months", "Harvest length (months)")
		}
		if vm.Has("fruiting_temp_min_c") {
			@textInput(vm, "fruiting_temp_min_c", "Minimum fruiting temperature (°C)")
			@textInput(vm, "fruiting_temp_max_c", "Maximum fruiting temperature (°C)")
			@textInput(vm, "min_precip_mm", "Minimum monthly rain (mm)")
		}
		if vm.Has("storage_months") {
			@textInput(vm, "storage_months", "Months it keeps in storage (0–9)")
		}
		if vm.Has("regions") {
			<label>
				Regions (one per line: <code>25: 1,2,3,4 | peak 2,3</code>)
				<textarea name="regions" rows="4">{ vm.Values["regions"] }</textarea>
				@fieldErrors(vm, "regions")
			</label>
		}
		if len(vm.Fields) == 0 {
			<p>Year-round produce has no season rules.</p>
		}
	</fieldset>
}
```

Add to `internal/admin/views/models.go`:

```go
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
```

(import `strconv`). Delete `Home` from `layout.templ`.

- [ ] **Step 4: Implement the handlers**

In `internal/admin/admin.go`: add to `Deps`

```go
	Changes *changeset.Service
	Load    func(context.Context) (*catalog.Snapshot, error)
	Pool    *pgxpool.Pool
```

remove the `m.Handle("GET /admin", …home…)` line and the `registerCatalog` no-op, and extend `writeRoutes` with:

```go
	{"POST", "/admin/ingredients/tomato/preview"}, {"POST", "/admin/ingredients/tomato/delete/preview"},
	{"POST", "/admin/import"}, {"POST", "/admin/changesets/1/apply"},
```

Delete `func (a *Admin) home` from `login.go`.

Create `internal/admin/catalog.go`:

```go
package admin

import (
	"net/http"
	"sort"
	"strings"

	"thymeandplace/internal/admin/views"
	"thymeandplace/internal/catalog"
)

func (a *Admin) registerCatalog() {
	m := a.mux
	auth := func(f http.HandlerFunc) http.Handler { return a.requireAuth(f) }
	m.Handle("GET /admin", auth(a.catalogList))
	m.Handle("GET /admin/ingredients/new", auth(a.newForm))
	m.Handle("GET /admin/ingredients/fields", auth(a.kindFields))
	m.Handle("GET /admin/ingredients/{slug}", auth(a.editForm))
	m.Handle("POST /admin/ingredients/{slug}/preview", auth(a.formPreview))
	m.Handle("POST /admin/ingredients/{slug}/delete/preview", auth(a.deletePreview))
	m.Handle("GET /admin/import", auth(a.importPage))
	m.Handle("POST /admin/import", auth(a.importSubmit))
	m.Handle("POST /admin/changesets/{id}/apply", auth(a.apply))
	m.Handle("GET /admin/export", auth(a.export))
	m.Handle("GET /admin/history", auth(a.history))
}

func options[T ~string](all []T, selected string) []views.Option {
	out := make([]views.Option, len(all))
	for i, v := range all {
		out[i] = views.Option{Value: string(v), Label: string(v), Selected: string(v) == selected}
	}
	return out
}

func (a *Admin) loadSnap(w http.ResponseWriter, r *http.Request) (*catalog.Snapshot, bool) {
	s, err := a.d.Load(r.Context())
	if err != nil {
		a.d.Log.Error("catalog load failed", "err", err)
		http.Error(w, "Couldn't load the catalog. Try again in a moment.", http.StatusServiceUnavailable)
		return nil, false
	}
	return s, true
}

func (a *Admin) catalogList(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.loadSnap(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	needle := strings.ToLower(strings.TrimSpace(q.Get("q")))
	vm := views.CatalogVM{
		Layout: a.layout(r, "Catalog"), Q: q.Get("q"), Category: q.Get("category"), Kind: q.Get("kind"),
		Categories: options(catalog.Categories, q.Get("category")), Kinds: options(catalog.Kinds, q.Get("kind")),
	}
	if q.Get("applied") != "" {
		vm.Message = "Changes applied."
	}
	for _, ing := range snap.All() {
		if needle != "" && !strings.Contains(strings.ToLower(ing.Name), needle) && !strings.Contains(ing.Slug, needle) {
			continue
		}
		if vm.Category != "" && string(ing.Category) != vm.Category {
			continue
		}
		if vm.Kind != "" && string(ing.Kind) != vm.Kind {
			continue
		}
		vm.Rows = append(vm.Rows, views.CatalogRow{Slug: ing.Slug, Name: ing.Name, Category: string(ing.Category), Kind: string(ing.Kind)})
	}
	sort.Slice(vm.Rows, func(i, j int) bool { return vm.Rows[i].Name < vm.Rows[j].Name })
	vm.Total = len(vm.Rows)
	if r.Header.Get("HX-Request") == "true" {
		a.render(w, r, http.StatusOK, views.CatalogRows(vm))
		return
	}
	a.render(w, r, http.StatusOK, views.CatalogPage(vm))
}

func (a *Admin) formVM(r *http.Request, isNew bool, values map[string]string, errs map[string][]string) views.FormVM {
	kind := values["kind"]
	if kind == "" {
		kind = string(catalog.KindAnnual)
		values["kind"] = kind
	}
	action := "/admin/ingredients/new/preview"
	if !isNew {
		action = "/admin/ingredients/" + values["slug"] + "/preview"
	}
	title := "Add ingredient"
	if !isNew {
		title = "Edit " + values["name"]
	}
	return views.FormVM{
		Layout: a.layout(r, title), IsNew: isNew, Action: action, Values: values, Errors: errs,
		Kind: kind, Fields: kindFields[catalog.Kind(kind)],
		Categories: options(catalog.Categories, values["category"]), Kinds: options(catalog.Kinds, kind),
	}
}

func (a *Admin) newForm(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusOK, views.FormPage(a.formVM(r, true, map[string]string{"storage_months": "0", "frost": "tender"}, nil)))
}

func (a *Admin) editForm(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.loadSnap(w, r)
	if !ok {
		return
	}
	ing, found := snap.Get(r.PathValue("slug"))
	if !found {
		http.NotFound(w, r)
		return
	}
	a.render(w, r, http.StatusOK, views.FormPage(a.formVM(r, false, formValues(ing), nil)))
}

// kindFields re-renders the kind-specific fieldset, keeping typed values.
func (a *Admin) kindFields(w http.ResponseWriter, r *http.Request) {
	values := map[string]string{}
	for k := range r.URL.Query() {
		values[k] = r.URL.Query().Get(k)
	}
	if values["frost"] == "" {
		values["frost"] = "tender"
	}
	a.render(w, r, http.StatusOK, views.KindFields(a.formVM(r, false, values, nil)))
}

// problemsToErrors maps problems to form fields; regions[i].* collapse onto "regions".
func problemsToErrors(ps []catalog.Problem) map[string][]string {
	out := map[string][]string{}
	for _, p := range ps {
		field := p.Field
		if strings.HasPrefix(field, "regions") {
			msg := p.Message
			if field != "regions" {
				msg = field + " " + msg
			}
			out["regions"] = append(out["regions"], msg)
			continue
		}
		out[field] = append(out[field], p.Message)
	}
	return out
}

func formValuesFromRequest(r *http.Request) map[string]string {
	values := map[string]string{}
	for k := range r.PostForm {
		values[k] = r.PostForm.Get(k)
	}
	return values
}
```

`formPreview`, `deletePreview`, `importPage`, `importSubmit`, `apply`, `export` and `history` come in Tasks 3–5. So this task compiles and tests on its own, add these stubs to `catalog.go` and replace each one in its task:

```go
func (a *Admin) deletePreview(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
func (a *Admin) importPage(w http.ResponseWriter, r *http.Request)    { http.NotFound(w, r) }
func (a *Admin) importSubmit(w http.ResponseWriter, r *http.Request)  { http.NotFound(w, r) }
func (a *Admin) apply(w http.ResponseWriter, r *http.Request)         { http.NotFound(w, r) }
func (a *Admin) export(w http.ResponseWriter, r *http.Request)        { http.NotFound(w, r) }
func (a *Admin) history(w http.ResponseWriter, r *http.Request)       { http.NotFound(w, r) }
```

`formPreview` is implemented in this task because the form tests need it. Add it to `catalog.go`. It renders the preview page, which Task 3 builds; until then render a minimal success response:

```go
func (a *Admin) formPreview(w http.ResponseWriter, r *http.Request) {
	isNew := r.PathValue("slug") == "new"
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Couldn't read the form.", http.StatusBadRequest)
		return
	}
	ing, problems := parseIngredientForm(r.PostForm)
	if !isNew {
		ing.Slug = r.PathValue("slug") // slugs are stable keys; the edit form can't rename
	}
	if isNew {
		if ing.Slug == "new" {
			problems = append(problems, catalog.Problem{Slug: ing.Slug, Field: "slug", Message: "“new” is reserved; choose another slug"})
		} else if snap, ok := a.loadSnap(w, r); !ok {
			return
		} else if _, exists := snap.Get(ing.Slug); exists {
			problems = append(problems, catalog.Problem{Slug: ing.Slug, Field: "slug", Message: "already exists; edit it instead"})
		}
	}
	// Parse problems win for their field; rule problems fill in the rest.
	parsed := map[string]bool{}
	for _, p := range problems {
		parsed[p.Field] = true
	}
	for _, p := range ing.Validate() {
		if !parsed[p.Field] {
			problems = append(problems, p)
		}
	}
	if len(problems) > 0 {
		a.render(w, r, http.StatusUnprocessableEntity, views.FormPage(a.formVM(r, isNew, formValuesFromRequest(r), problemsToErrors(problems))))
		return
	}
	a.previewChangeset(w, r, changeset.FormChangeset(ing), func(errs map[string][]string) {
		a.render(w, r, http.StatusUnprocessableEntity, views.FormPage(a.formVM(r, isNew, formValuesFromRequest(r), errs)))
	})
}
```

and, in this task only, this temporary `previewChangeset` (Task 3 replaces it):

```go
func (a *Admin) previewChangeset(w http.ResponseWriter, r *http.Request, cs changeset.Changeset, onInvalid func(map[string][]string)) {
	_, pv, err := a.d.Changes.Preview(r.Context(), cs)
	var inv *changeset.InvalidError
	if errors.As(err, &inv) {
		onInvalid(problemsToErrors(inv.Problems))
		return
	}
	if err != nil {
		http.Error(w, "Couldn't build the preview.", http.StatusInternalServerError)
		return
	}
	for _, c := range pv.Diff.Changed {
		for _, f := range c.Fields {
			fmt.Fprintln(w, f.Field)
		}
	}
}
```

(imports: `errors`, `fmt`, `thymeandplace/internal/changeset`.)

- [ ] **Step 5: Run the tests**

Run: `go tool templ generate && go test ./internal/admin/ -count=1`
Expected: `ok`. `TestKindChangeClearsOldFields` passes against the temporary preview, which prints changed field names.

- [ ] **Step 6: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): catalog list with filters, kind-aware ingredient form

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Preview page (diff, calibration, bars) and delete preview

**Files:**
- Create: `internal/admin/views/preview.templ`, `internal/admin/preview.go`, `internal/admin/preview_test.go`
- Modify: `internal/admin/catalog.go` (remove the temporary `previewChangeset` and the `deletePreview` stub), `internal/admin/views/models.go` (preview VMs)

**Interfaces:**
- Consumes: `changeset.Preview`, `changeset.Pending`, `season.Status`.
- Produces:
  - `views.PreviewVM`, `views.PreviewPage(PreviewVM)`
  - `func (a *Admin) previewChangeset(w, r, cs changeset.Changeset, onInvalid func(map[string][]string))`
  - `func (a *Admin) renderPreview(w, r, status int, p changeset.Pending, pv changeset.Preview, msg string)`
  - `func previewVM(layout views.LayoutVM, p changeset.Pending, pv changeset.Preview, msg string) views.PreviewVM`

- [ ] **Step 1: Write the failing tests**

Create `internal/admin/preview_test.go`:

```go
package admin

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var applyRE = regexp.MustCompile(`action="/admin/changesets/(\d+)/apply"`)

func pendingID(t *testing.T, body string) string {
	t.Helper()
	m := applyRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no apply form in:\n%s", body)
	}
	return m[1]
}

func TestFormPreviewShowsDiffScoreAndBars(t *testing.T) {
	_, c := catalogEnv(t)
	f := tomatoForm()
	f.Set("months_to_harvest", "1")
	rec := c.postForm("/admin/ingredients/tomato/preview", f)
	b := html.UnescapeString(rec.Body.String())
	for _, want := range []string{
		"months_to_harvest", ">3<", ">1<", // field diff before/after
		"10 unchanged", "Calibration score", "0.556", "0.422", // (1+.667)/3 → (.6+.667)/3
		"Temperate test town", "Apply despite lower calibration score",
	} {
		if !strings.Contains(b, want) {
			t.Fatalf("preview missing %q\n%s", want, b)
		}
	}
	if !strings.Contains(b, `class="s-peak"`) {
		t.Fatal("season bars missing")
	}
	pendingID(t, b)
}

// Review Focus 3.
func TestPreviewEscapesUserContent(t *testing.T) {
	_, c := catalogEnv(t)
	f := tomatoForm()
	f.Set("tip_choose", "<b>ripe</b> & fragrant — Crème fraîche")
	rec := c.postForm("/admin/ingredients/tomato/preview", f)
	raw := rec.Body.String()
	if strings.Contains(raw, "<b>ripe</b>") || !strings.Contains(raw, "&lt;b&gt;ripe&lt;/b&gt;") {
		t.Fatalf("unescaped content in preview:\n%s", raw)
	}
	if !strings.Contains(html.UnescapeString(raw), "Crème fraîche") {
		t.Fatal("unicode mangled")
	}
}

func TestDeletePreviewNeedsTypedSlug(t *testing.T) {
	_, c := catalogEnv(t)
	rec := c.postForm("/admin/ingredients/basil/delete/preview", url.Values{})
	b := html.UnescapeString(rec.Body.String())
	if rec.Code != 200 || !strings.Contains(b, "Deleted") || !strings.Contains(b, `name="confirm_slug"`) || !strings.Contains(b, "Tomato") {
		t.Fatalf("delete preview:\n%s", b)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run 'FormPreview|Escapes|DeletePreview'`
Expected: FAIL (the temporary preview prints field names only; the delete stub returns 404).

- [ ] **Step 3: Add view models and the preview template**

Append to `internal/admin/views/models.go`:

```go
type FieldChange struct{ Field, Before, After string }

type ChangedRow struct {
	Slug, Name string
	Fields     []FieldChange
}

type ItemRow struct{ Slug, Name string }

type BarCell struct{ Class, Title string }

type BarRow struct {
	Fixture             string
	Before, After       []BarCell
	HadBefore, HasAfter bool
}

type ScoreRow struct {
	Slug, Name, Before, After string
	Bars                      []BarRow
}

type PreviewVM struct {
	Layout                               LayoutVM
	ID                                   int64
	Source, DeleteSlug                   string
	Added, Deleted                       []ItemRow
	Changed                              []ChangedRow
	Unchanged                            int
	ScoreBefore, ScoreAfter, Delta       string
	Compared                             int
	NeedsOverride                        bool
	Items                                []ScoreRow
	Message                              string
	ExpiresAt                            string
}
```

Create `internal/admin/views/preview.templ`:

```templ
package views

templ bars(cells []BarCell) {
	for _, c := range cells {
		<span class={ c.Class } title={ c.Title }>{ c.Title[:1] }</span>
	}
}

templ PreviewPage(vm PreviewVM) {
	@Layout(vm.Layout) {
		<h1>Preview</h1>
		if vm.Message != "" {
			<p class="error warn">{ vm.Message }</p>
		}
		<p>This preview expires at { vm.ExpiresAt } UTC.</p>
		<h2>Changes</h2>
		<p>{ plural(len(vm.Added), "added item") }, { plural(len(vm.Changed), "changed item") }, { plural(len(vm.Deleted), "deleted item") }, { vm.Unchanged } unchanged.</p>
		if len(vm.Added) > 0 {
			<h3>Added</h3>
			<ul>
				for _, it := range vm.Added {
					<li>{ it.Name } (<code>{ it.Slug }</code>)</li>
				}
			</ul>
		}
		if len(vm.Changed) > 0 {
			<h3>Changed</h3>
			for _, ch := range vm.Changed {
				<h4>{ ch.Name } (<code>{ ch.Slug }</code>)</h4>
				<table class="diff">
					<thead><tr><th>Field</th><th>Before</th><th>After</th></tr></thead>
					<tbody>
						for _, f := range ch.Fields {
							<tr><td>{ f.Field }</td><td class="before">{ f.Before }</td><td class="after">{ f.After }</td></tr>
						}
					</tbody>
				</table>
			}
		}
		if len(vm.Deleted) > 0 {
			<h3>Deleted</h3>
			<ul>
				for _, it := range vm.Deleted {
					<li>{ it.Name } (<code>{ it.Slug }</code>)</li>
				}
			</ul>
		}
		<h2>Calibration score</h2>
		if vm.Compared == 0 {
			<p>No calibration fixture lists these ingredients yet, so there is nothing to compare.</p>
		} else {
			<p>Before { vm.ScoreBefore } → after { vm.ScoreAfter } ({ vm.Delta }).</p>
		}
		for _, it := range vm.Items {
			<h3>{ it.Name }: { it.Before } → { it.After }</h3>
			for _, b := range it.Bars {
				<div class="bars">
					<strong>{ b.Fixture } before</strong>
					if b.HadBefore {
						@bars(b.Before)
					} else {
						<span>(new)</span>
					}
				</div>
				<div class="bars">
					<strong>{ b.Fixture } after</strong>
					if b.HasAfter {
						@bars(b.After)
					} else {
						<span>(deleted)</span>
					}
				</div>
			}
		}
		<h2>Apply</h2>
		<form method="post" action={ templ.SafeURL("/admin/changesets/" + itoa(vm.ID) + "/apply") }>
			<input type="hidden" name="csrf" value={ vm.Layout.CSRF }/>
			if vm.DeleteSlug != "" {
				<label>Type <code>{ vm.DeleteSlug }</code> to confirm the delete <input type="text" name="confirm_slug" autocomplete="off" required/></label>
			}
			if vm.NeedsOverride {
				<p class="warn">The calibration score drops by more than 0.02.</p>
				<label><input type="checkbox" name="override" value="1"/> Apply despite lower calibration score</label>
			}
			<button type="submit">Apply</button>
		</form>
	}
}
```

Add to `internal/admin/views/models.go`: `func itoa(i int64) string { return strconv.FormatInt(i, 10) }`.

`bars` uses `c.Title[:1]` for the month letter; titles look like `"January: peak"`.

- [ ] **Step 4: Implement the preview plumbing**

Create `internal/admin/preview.go`:

```go
package admin

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"thymeandplace/internal/admin/views"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/season"
)

func score(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%.3f", *p)
}

func barCells(months [12]season.Status) []views.BarCell {
	out := make([]views.BarCell, 12)
	for m := 0; m < 12; m++ {
		name := time.Month(m + 1).String()
		cls := map[season.Status]string{season.Peak: "s-peak", season.InSeason: "s-in", season.FromStorage: "s-store", season.YearRound: "s-year"}[months[m]]
		out[m] = views.BarCell{Class: cls, Title: name + ": " + months[m].String()}
	}
	return out
}

func previewVM(layout views.LayoutVM, p changeset.Pending, pv changeset.Preview, msg string) views.PreviewVM {
	vm := views.PreviewVM{
		Layout: layout, ID: p.ID, Source: p.Changeset.Source, Unchanged: pv.Diff.Unchanged,
		ScoreBefore: fmt.Sprintf("%.3f", pv.Before.Score), ScoreAfter: fmt.Sprintf("%.3f", pv.After.Score),
		Delta: fmt.Sprintf("%+.3f", pv.Delta), Compared: max(pv.Before.Compared, pv.After.Compared),
		NeedsOverride: pv.NeedsOverride, Message: msg, ExpiresAt: p.ExpiresAt.UTC().Format("15:04"),
	}
	if p.Changeset.Source == changeset.SourceDelete && len(p.Changeset.Deletes) == 1 {
		vm.DeleteSlug = p.Changeset.Deletes[0]
	}
	for _, a := range pv.Diff.Added {
		vm.Added = append(vm.Added, views.ItemRow{Slug: a.Slug, Name: a.Name})
	}
	for _, d := range pv.Diff.Deleted {
		vm.Deleted = append(vm.Deleted, views.ItemRow{Slug: d.Slug, Name: d.Name})
	}
	for _, c := range pv.Diff.Changed {
		row := views.ChangedRow{Slug: c.Slug, Name: c.Name}
		for _, f := range c.Fields {
			row.Fields = append(row.Fields, views.FieldChange{Field: f.Field, Before: f.Before, After: f.After})
		}
		vm.Changed = append(vm.Changed, row)
	}
	for _, it := range pv.Items {
		sr := views.ScoreRow{Slug: it.Slug, Name: it.Name, Before: score(it.ScoreBefore), After: score(it.ScoreAfter)}
		for _, b := range it.Bars {
			sr.Bars = append(sr.Bars, views.BarRow{Fixture: b.Fixture, Before: barCells(b.Before), After: barCells(b.After), HadBefore: b.HadBefore, HasAfter: b.HasAfter})
		}
		vm.Items = append(vm.Items, sr)
	}
	return vm
}

func (a *Admin) renderPreview(w http.ResponseWriter, r *http.Request, status int, p changeset.Pending, pv changeset.Preview, msg string) {
	a.render(w, r, status, views.PreviewPage(previewVM(a.layout(r, "Preview"), p, pv, msg)))
}

// previewChangeset validates, stores and renders a preview. Validation
// problems go to onInvalid so each caller can show them in context.
func (a *Admin) previewChangeset(w http.ResponseWriter, r *http.Request, cs changeset.Changeset, onInvalid func(map[string][]string)) {
	p, pv, err := a.d.Changes.Preview(r.Context(), cs)
	var inv *changeset.InvalidError
	switch {
	case errors.As(err, &inv):
		onInvalid(problemsToErrors(inv.Problems))
	case err != nil:
		a.d.Log.Error("preview failed", "err", err)
		http.Error(w, "Couldn't build the preview. Try again.", http.StatusInternalServerError)
	default:
		a.renderPreview(w, r, http.StatusOK, p, pv, "")
	}
}

func (a *Admin) deletePreview(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	a.previewChangeset(w, r, changeset.DeleteChangeset(slug), func(errs map[string][]string) {
		http.Error(w, "Can't delete "+slug+": it does not exist.", http.StatusNotFound)
	})
}
```

Delete the temporary `previewChangeset` and the `deletePreview` stub from `catalog.go`, and drop the now-unused `fmt` import there.

- [ ] **Step 5: Run the tests**

Run: `go tool templ generate && go test ./internal/admin/ -count=1`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): preview page with field diff, calibration change and season bars

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Apply

**Files:**
- Create: `internal/admin/apply_test.go`
- Modify: `internal/admin/preview.go` (add `apply`; remove the `apply` stub from `catalog.go`)

**Interfaces:**
- Consumes: `changeset.Service.Apply`, `Reopen`, `UserMessage`, `ApplyOptions`.
- Produces: `func (a *Admin) apply(w, r)`: on success, 303 to `/admin?applied=1`. Override and confirm errors re-render the preview with the message (422). Stale and not-found errors render a message page with links back (409 / 410).

- [ ] **Step 1: Write the failing tests**

Create `internal/admin/apply_test.go`:

```go
package admin

import (
	"html"
	"net/url"
	"strings"
	"testing"
)

func TestApplyFormChange(t *testing.T) {
	e, c := catalogEnv(t)
	f := tomatoForm()
	f.Set("name", "Big tomato")
	id := pendingID(t, c.postForm("/admin/ingredients/tomato/preview", f).Body.String())
	rec := c.postForm("/admin/changesets/"+id+"/apply", url.Values{})
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin?applied=1" {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := e.snap(t).Get("tomato"); got.Name != "Big tomato" {
		t.Fatalf("name = %q", got.Name)
	}
	var actor string
	_ = e.pool.QueryRow(t.Context(), `SELECT actor FROM catalog_history ORDER BY id DESC LIMIT 1`).Scan(&actor)
	if actor != "owner (enroll)" {
		t.Fatalf("actor = %q", actor)
	}
	if !strings.Contains(c.get("/admin?applied=1").Body.String(), "Changes applied.") {
		t.Fatal("flash message missing")
	}
}

func TestApplyOverride(t *testing.T) {
	e, c := catalogEnv(t)
	f := tomatoForm()
	f.Set("months_to_harvest", "1")
	id := pendingID(t, c.postForm("/admin/ingredients/tomato/preview", f).Body.String())
	rec := c.postForm("/admin/changesets/"+id+"/apply", url.Values{})
	if rec.Code != 422 || !strings.Contains(html.UnescapeString(rec.Body.String()), "Apply despite lower calibration score") {
		t.Fatalf("no override: %d", rec.Code)
	}
	if rec := c.postForm("/admin/changesets/"+id+"/apply", url.Values{"override": {"1"}}); rec.Code != 303 {
		t.Fatalf("override: %d", rec.Code)
	}
	if got, _ := e.snap(t).Get("tomato"); *got.MonthsToHarvest != 1 {
		t.Fatal("not applied")
	}
}

func TestApplyDeleteWithConfirmation(t *testing.T) {
	e, c := catalogEnv(t)
	id := pendingID(t, c.postForm("/admin/ingredients/basil/delete/preview", url.Values{}).Body.String())
	rec := c.postForm("/admin/changesets/"+id+"/apply", url.Values{"confirm_slug": {"basi"}})
	if rec.Code != 422 || !strings.Contains(html.UnescapeString(rec.Body.String()), "Type the ingredient's slug exactly") {
		t.Fatalf("mismatch: %d", rec.Code)
	}
	if rec := c.postForm("/admin/changesets/"+id+"/apply", url.Values{"confirm_slug": {"basil"}}); rec.Code != 303 {
		t.Fatalf("confirmed: %d", rec.Code)
	}
	if _, ok := e.snap(t).Get("basil"); ok {
		t.Fatal("basil not deleted")
	}
}

func TestApplyStale(t *testing.T) {
	e, c := catalogEnv(t)
	id := pendingID(t, c.postForm("/admin/ingredients/tomato/preview", tomatoForm()).Body.String())
	_, _ = e.pool.Exec(t.Context(), `UPDATE ingredient SET note = 'elsewhere' WHERE slug = 'banana'`)
	rec := c.postForm("/admin/changesets/"+id+"/apply", url.Values{})
	if rec.Code != 409 || !strings.Contains(html.UnescapeString(rec.Body.String()), "The catalog changed since this preview. Preview again.") {
		t.Fatalf("stale: %d", rec.Code)
	}
}

// Review Focus 5.
func TestApplyResubmitted(t *testing.T) {
	e, c := catalogEnv(t)
	f := tomatoForm()
	f.Set("note", "once")
	id := pendingID(t, c.postForm("/admin/ingredients/tomato/preview", f).Body.String())
	c.postForm("/admin/changesets/"+id+"/apply", url.Values{})
	var before int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM catalog_history`).Scan(&before)
	rec := c.postForm("/admin/changesets/"+id+"/apply", url.Values{})
	if rec.Code != 410 || !strings.Contains(rec.Body.String(), "expired or was already applied") {
		t.Fatalf("resubmit: %d", rec.Code)
	}
	var after int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM catalog_history`).Scan(&after)
	if after != before {
		t.Fatal("resubmitted apply wrote history")
	}
}
```

The enrolled client's session method is `enroll`, so the expected actor is `owner (enroll)`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run Apply`
Expected: FAIL (the apply stub returns 404).

- [ ] **Step 3: Implement apply**

Append to `internal/admin/preview.go` (add `strconv` to the imports):

```go
func (a *Admin) apply(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sess, _ := SessionFrom(r.Context())
	opts := changeset.ApplyOptions{Override: r.PostFormValue("override") == "1", ConfirmSlug: strings.TrimSpace(r.PostFormValue("confirm_slug"))}
	err = a.d.Changes.Apply(r.Context(), id, "owner ("+sess.Method+")", opts)
	var inv *changeset.InvalidError
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin?applied=1", http.StatusSeeOther)
	case errors.Is(err, changeset.ErrOverrideRequired), errors.Is(err, changeset.ErrConfirmMismatch):
		p, pv, rerr := a.d.Changes.Reopen(r.Context(), id)
		if rerr != nil {
			a.messagePage(w, r, http.StatusConflict, changeset.UserMessage(rerr))
			return
		}
		a.renderPreview(w, r, http.StatusUnprocessableEntity, p, pv, changeset.UserMessage(err))
	case errors.Is(err, changeset.ErrStale):
		a.messagePage(w, r, http.StatusConflict, changeset.UserMessage(err))
	case errors.Is(err, changeset.ErrNotFound):
		a.messagePage(w, r, http.StatusGone, changeset.UserMessage(err))
	case errors.As(err, &inv):
		msgs := []string{}
		for _, p := range inv.Problems {
			msgs = append(msgs, p.String())
		}
		a.messagePage(w, r, http.StatusConflict, "The change is no longer valid: "+strings.Join(msgs, "; ")+". Preview again.")
	default:
		a.d.Log.Error("apply failed", "err", err, "changeset", id)
		a.messagePage(w, r, http.StatusInternalServerError, "Couldn't apply the change. Nothing was written. Try again.")
	}
}

func (a *Admin) messagePage(w http.ResponseWriter, r *http.Request, status int, msg string) {
	a.render(w, r, status, views.MessagePage(a.layout(r, "Catalog"), msg))
}
```

Add to `internal/admin/views/catalog.templ`:

```templ
templ MessagePage(layout LayoutVM, msg string) {
	@Layout(layout) {
		<p class="error warn">{ msg }</p>
		<p><a href="/admin">Back to the catalog</a> · <a href="/admin/import">Import</a></p>
	}
}
```

Remove the `apply` stub from `catalog.go`.

- [ ] **Step 4: Run the tests**

Run: `go tool templ generate && go test ./internal/admin/ -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/admin
git commit -m "feat(admin): apply with override, typed-slug delete, stale and resubmit handling

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Import, export and history

**Files:**
- Create: `internal/store/queries/history.sql`, `internal/admin/views/importexport.templ`, `internal/admin/importexport.go`, `internal/admin/importexport_test.go`
- Modify: `internal/admin/catalog.go` (remove the import/export/history stubs), `internal/admin/admin.go` (413 check before CSRF), `internal/admin/views/models.go` (`ImportVM`, `HistoryVM`, `HistoryRow`), `cmd/server/app.go` (pass `Changes` and `Load` to admin)

**Interfaces:**
- Consumes: `changeset.ImportChangeset`, `changeset.Export`, `catalogjson.ErrTooLarge`/`ErrUnknownFormat`.
- Produces:
  - sqlc `ListHistory(ctx, ListHistoryParams{Slug pgtype.Text, Before pgtype.Int8}) ([]CatalogHistory, error)`
  - Routes' handlers `importPage`, `importSubmit`, `export`, `history`
  - A request with `Content-Length` above 6 MB gets **413** `The upload is too large. The limit is 5 MB.` before CSRF parsing

- [ ] **Step 1: Add the history query**

Create `internal/store/queries/history.sql`:

```sql
-- name: ListHistory :many
SELECT * FROM catalog_history
WHERE (sqlc.narg(slug)::text IS NULL OR row_key->>'slug' = sqlc.narg(slug)::text)
  AND (sqlc.narg(before)::bigint IS NULL OR id < sqlc.narg(before)::bigint)
ORDER BY id DESC
LIMIT 100;
```

Run: `./scripts/sqlc.sh generate && go build ./internal/store/`
Expected: compiles; `ListHistoryParams{Slug pgtype.Text; Before pgtype.Int8}`.

- [ ] **Step 2: Write the failing tests**

Create `internal/admin/importexport_test.go`:

```go
package admin

import (
	"bytes"
	"html"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogjson"
)

func (c *client) upload(path, filename string, content []byte) *httptest.ResponseRecorder {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf", c.csrf)
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(content)
	_ = mw.Close()
	return c.do("POST", path, &buf, map[string]string{"Content-Type": mw.FormDataContentType()})
}

const chardFile = `{"format":"thymeandplace-catalog/v1","ingredients":[
 {"slug":"chard","name":"Chard","category":"vegetable","kind":"cultivated_year_round",
  "tips":{"choose":"Glossy leaves.","store":"Fridge, wrapped."},"pairings":["kale"],"note":""}]}`

func TestImportPreviewApplyExportRoundTrip(t *testing.T) {
	e, c := catalogEnv(t)
	c.get("/admin/import")
	rec := c.upload("/admin/import", "chard.json", []byte(chardFile))
	b := html.UnescapeString(rec.Body.String())
	if rec.Code != 200 || !strings.Contains(b, "Chard") || !strings.Contains(b, "1 added item") || !strings.Contains(b, "11 unchanged") {
		t.Fatalf("import preview: %d\n%s", rec.Code, b)
	}
	if rec := c.postForm("/admin/changesets/"+pendingID(t, b)+"/apply", url.Values{}); rec.Code != 303 {
		t.Fatalf("apply: %d", rec.Code)
	}
	exp := c.get("/admin/export")
	if cd := exp.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="thymeandplace-catalog-`) {
		t.Fatalf("disposition = %q", cd)
	}
	items, err := catalogjson.Decode(exp.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := e.snap(t).All()
	if len(items) != 12 {
		t.Fatalf("export has %d items", len(items))
	}
	for i := range want {
		if !reflect.DeepEqual(catalog.Normalize(items[i]), want[i]) {
			t.Fatalf("export mismatch for %s", want[i].Slug)
		}
	}
}

func TestImportErrors(t *testing.T) {
	_, c := catalogEnv(t)
	c.get("/admin/import")
	cases := map[string]struct {
		content []byte
		status  int
		want    string
	}{
		"unknown format": {[]byte(`{"format":"other/v2","ingredients":[]}`), 400, `unknown catalog format "other/v2"`},
		"not json":       {[]byte(`hello`), 400, "not a valid JSON document"},
		"too large":      {append([]byte(`{"format":"thymeandplace-catalog/v1","ingredients":[],"pad":"`), bytes.Repeat([]byte("x"), catalogjson.MaxBytes)...), 400, "larger than 5 MB"},
		"invalid item": {[]byte(`{"format":"thymeandplace-catalog/v1","ingredients":[
			{"slug":"chard","name":"","category":"vegetable","kind":"cultivated_year_round","tips":{"choose":"c","store":"s"},"pairings":[]}]}`), 422, "chard: name must be 1 to 80 characters"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := c.upload("/admin/import", "x.json", tc.content)
			if rec.Code != tc.status || !strings.Contains(html.UnescapeString(rec.Body.String()), tc.want) {
				t.Fatalf("%d\n%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestOversizedRequestIs413(t *testing.T) {
	_, c := catalogEnv(t)
	c.get("/admin/import")
	rec := c.upload("/admin/import", "big.json", bytes.Repeat([]byte("x"), 7<<20))
	if rec.Code != 413 || !strings.Contains(rec.Body.String(), "The limit is 5 MB") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestHistoryNewestFirstAndFilter(t *testing.T) {
	_, c := catalogEnv(t)
	f := tomatoForm()
	f.Set("note", "first")
	c.postForm("/admin/changesets/"+pendingID(t, c.postForm("/admin/ingredients/tomato/preview", f).Body.String())+"/apply", url.Values{})
	f.Set("note", "second")
	c.postForm("/admin/changesets/"+pendingID(t, c.postForm("/admin/ingredients/tomato/preview", f).Body.String())+"/apply", url.Values{})

	b := html.UnescapeString(c.get("/admin/history?slug=tomato").Body.String())
	rows := strings.Split(b, "<tr>") // [0] before the table, [1] header, [2] newest row
	if len(rows) < 4 || !strings.Contains(rows[2], `"note": "second"`) || !strings.Contains(rows[3], `"note": "first"`) {
		t.Fatalf("history not newest first:\n%s", b)
	}
	if strings.Contains(c.get("/admin/history?slug=kale").Body.String(), "second") {
		t.Fatal("slug filter leaked other rows")
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/admin/ -run 'Import|Oversized|History'`
Expected: FAIL (stubs return 404).

- [ ] **Step 4: Implement import, export, history and the 413 guard**

Append to `internal/admin/views/models.go`:

```go
type ImportVM struct {
	Layout   LayoutVM
	Message  string
	Problems []string
}

type HistoryRow struct {
	ID                                   int64
	At, Actor, Action, Table, Key         string
	Before, After                        string
	ChangeID                             string
}

type HistoryVM struct {
	Layout     LayoutVM
	Slug       string
	Rows       []HistoryRow
	OlderURL   string
}
```

Create `internal/admin/views/importexport.templ`:

```templ
package views

templ ImportPage(vm ImportVM) {
	@Layout(vm.Layout) {
		<h1>Import</h1>
		<p>Upload a <code>thymeandplace-catalog/v1</code> JSON file (5 MB max). Import only adds or updates ingredients by slug; it never deletes. For each ingredient in the file, its pairings and regions replace the stored ones.</p>
		if vm.Message != "" {
			<p class="error">{ vm.Message }</p>
		}
		if len(vm.Problems) > 0 {
			<ul class="error">
				for _, p := range vm.Problems {
					<li>{ p }</li>
				}
			</ul>
		}
		<form method="post" action="/admin/import" enctype="multipart/form-data">
			<input type="hidden" name="csrf" value={ vm.Layout.CSRF }/>
			<input type="file" name="file" accept="application/json,.json" required/>
			<button type="submit">Preview import</button>
		</form>
	}
}

templ HistoryPage(vm HistoryVM) {
	@Layout(vm.Layout) {
		<h1>History</h1>
		<form method="get" action="/admin/history">
			<input type="search" name="slug" value={ vm.Slug } placeholder="Filter by slug"/>
			<button type="submit">Filter</button>
		</form>
		<table>
			<thead><tr><th>When (UTC)</th><th>Who</th><th>Change</th><th>What</th><th>Before</th><th>After</th></tr></thead>
			<tbody>
				for _, h := range vm.Rows {
					<tr>
						<td>{ h.At }</td>
						<td>{ h.Actor }</td>
						<td>{ h.ChangeID }</td>
						<td>{ h.Action } { h.Table } { h.Key }</td>
						<td><code>{ h.Before }</code></td>
						<td><code>{ h.After }</code></td>
					</tr>
				}
			</tbody>
		</table>
		if vm.OlderURL != "" {
			<p><a href={ templ.SafeURL(vm.OlderURL) }>Older</a></p>
		}
	}
}
```

Create `internal/admin/importexport.go`:

```go
package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"thymeandplace/internal/admin/views"
	"thymeandplace/internal/catalog/catalogjson"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/store"
)

func (a *Admin) importPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusOK, views.ImportPage(views.ImportVM{Layout: a.layout(r, "Import")}))
}

func (a *Admin) importError(w http.ResponseWriter, r *http.Request, status int, msg string, problems []string) {
	a.render(w, r, status, views.ImportPage(views.ImportVM{Layout: a.layout(r, "Import"), Message: msg, Problems: problems}))
}

func (a *Admin) importSubmit(w http.ResponseWriter, r *http.Request) {
	f, fh, err := r.FormFile("file")
	if err != nil {
		a.importError(w, r, http.StatusBadRequest, "Choose a JSON file to import.", nil)
		return
	}
	defer f.Close()
	if fh.Size > catalogjson.MaxBytes {
		a.importError(w, r, http.StatusBadRequest, catalogjson.ErrTooLarge.Error()+".", nil)
		return
	}
	cs, err := changeset.ImportChangeset(f)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, catalogjson.ErrTooLarge) {
			msg = catalogjson.ErrTooLarge.Error()
		}
		a.importError(w, r, http.StatusBadRequest, "The file can't be imported: "+msg+".", nil)
		return
	}
	a.previewImport(w, r, cs)
}

func (a *Admin) previewImport(w http.ResponseWriter, r *http.Request, cs changeset.Changeset) {
	p, pv, err := a.d.Changes.Preview(r.Context(), cs)
	var inv *changeset.InvalidError
	switch {
	case errors.As(err, &inv):
		var lines []string
		for _, pr := range inv.Problems {
			lines = append(lines, pr.String())
		}
		a.importError(w, r, http.StatusUnprocessableEntity, "Nothing was imported. Fix these problems and upload again:", lines)
	case err != nil:
		a.d.Log.Error("import preview failed", "err", err)
		a.importError(w, r, http.StatusInternalServerError, "Couldn't build the preview. Try again.", nil)
	default:
		a.renderPreview(w, r, http.StatusOK, p, pv, "")
	}
}

func (a *Admin) export(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.loadSnap(w, r)
	if !ok {
		return
	}
	now := a.d.Auth.Now().UTC()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="thymeandplace-catalog-%s.json"`, now.Format("20060102")))
	if err := changeset.Export(w, snap, now); err != nil {
		a.d.Log.Error("export failed", "err", err)
	}
}

func (a *Admin) history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := store.ListHistoryParams{}
	if s := q.Get("slug"); s != "" {
		params.Slug = pgtype.Text{String: s, Valid: true}
	}
	if b, err := strconv.ParseInt(q.Get("before"), 10, 64); err == nil {
		params.Before = pgtype.Int8{Int64: b, Valid: true}
	}
	rows, err := store.New(a.d.Pool).ListHistory(r.Context(), params)
	if err != nil {
		a.d.Log.Error("history failed", "err", err)
		http.Error(w, "Couldn't load history.", http.StatusInternalServerError)
		return
	}
	vm := views.HistoryVM{Layout: a.layout(r, "History"), Slug: q.Get("slug")}
	for _, h := range rows {
		row := views.HistoryRow{
			ID: h.ID, At: h.ChangedAt.Time.UTC().Format("2006-01-02 15:04:05"), Actor: h.Actor, Action: h.Action,
			Table: h.TableName, Key: string(h.RowKey), Before: string(h.Before), After: string(h.After),
		}
		if h.ChangeID.Valid {
			row.ChangeID = "#" + strconv.FormatInt(h.ChangeID.Int64, 10)
		}
		vm.Rows = append(vm.Rows, row)
	}
	if len(rows) == 100 {
		v := url.Values{"before": {strconv.FormatInt(rows[len(rows)-1].ID, 10)}}
		if vm.Slug != "" {
			v.Set("slug", vm.Slug)
		}
		vm.OlderURL = "/admin/history?" + v.Encode()
	}
	a.render(w, r, http.StatusOK, views.HistoryPage(vm))
}
```

In `internal/admin/admin.go`, add the 413 guard at the top of the `csrf` middleware:

```go
		if r.ContentLength > maxBody {
			http.Error(w, "The upload is too large. The limit is 5 MB.", http.StatusRequestEntityTooLarge)
			return
		}
```

Remove the import/export/history stubs from `catalog.go`.

In `cmd/server/app.go`, construct the changeset service and pass the new deps where the admin handler is built:

```go
		changes := changeset.NewService(pool, a.ocean, a.cases)
		adminH := admin.New(admin.Deps{
			Auth: authSvc, Log: log, TrustProxy: cfg.TrustProxy, Changes: changes, Pool: pool,
			Load: func(ctx context.Context) (*catalog.Snapshot, error) { return catalog.Load(ctx, pool) },
		})
```

- [ ] **Step 5: Run the tests**

Run: `go tool templ generate && go vet ./... && go test ./internal/admin/ ./cmd/server/ -count=1`
Expected: `ok`. The CSRF test now also covers the catalog write routes added to `writeRoutes` in Task 2.

- [ ] **Step 6: Commit**

```bash
git add internal/store internal/admin cmd/server
git commit -m "feat(admin): JSON import with preview, export download, catalog history

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Plan completion check

- [ ] `go tool templ generate && ./scripts/sqlc.sh generate && git diff --exit-code`
- [ ] `go vet ./... && go test ./... -count=1` passes.
- [ ] Manual check in a browser (dev server from Plan 6, Task 8): add an ingredient, change its kind (the fields swap without losing typed name/tips), preview, apply, and confirm the public `/season` page shows the change without a restart. Delete it with the typed slug. Export, then re-import the export: the preview shows no changes.
