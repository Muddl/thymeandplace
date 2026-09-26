package admin

import (
	"html"
	"net/http"
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
	// A history-restore navigation (browser back/forward into a previous
	// /admin?... URL) still carries HX-Request from the boosted link that
	// got there, but htmx needs the whole page to restore into, not the
	// #rows fragment it would otherwise get swapped into nothing.
	rec = c.do("GET", "/admin?q=kale", nil, map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"})
	if !strings.Contains(rec.Body.String(), "<html") || !strings.Contains(rec.Body.String(), "Kale") {
		t.Fatalf("history-restore request should return the full page:\n%s", rec.Body.String())
	}
}

func TestEditFormPrefilledAndKindSwap(t *testing.T) {
	_, c := catalogEnv(t)
	b := c.get("/admin/ingredients/tomato").Body.String()
	for _, want := range []string{`name="grow_temp_min_c" value="16"`, `name="grow_temp_max_c" value="30"`, `value="basil"`, `hx-get="/admin/kind-fields"`, `hx-params="not csrf"`} {
		if !strings.Contains(b, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(b, `name="chill_months_min"`) {
		t.Fatal("annual form must not show perennial fields")
	}
	part := c.do("GET", "/admin/kind-fields?kind=perennial_fruit&frost=hardy", nil, map[string]string{"HX-Request": "true"}).Body.String()
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

// Review Focus 5: a stale edit form. The ingredient is deleted in another
// tab after the edit form was rendered; submitting that now-stale form must
// not silently re-add it via FormChangeset's upsert.
func TestStaleEditFormAfterDelete(t *testing.T) {
	e, c := catalogEnv(t)
	// Open the edit form (as the owner's other tab would have, before the
	// delete below), then delete basil via the normal preview+apply path.
	c.get("/admin/ingredients/basil")
	id := pendingID(t, c.postForm("/admin/ingredients/basil/delete/preview", url.Values{}).Body.String())
	if rec := c.postForm("/admin/changesets/"+id+"/apply", url.Values{"confirm_slug": {"basil"}}); rec.Code != 303 {
		t.Fatalf("delete apply: %d", rec.Code)
	}
	if _, ok := e.snap(t).Get("basil"); ok {
		t.Fatal("basil should be deleted")
	}

	var before int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM pending_changeset`).Scan(&before)

	// Now submit the stale edit form, as if it had just been posted from
	// that other tab.
	f := url.Values{
		"slug": {"basil"}, "name": {"Basil"}, "category": {"herb"}, "kind": {"cultivated_year_round"},
		"tip_choose": {"c"}, "tip_store": {"s"}, "pairings": {""},
	}
	rec := c.postForm("/admin/ingredients/basil/preview", f)
	b := html.UnescapeString(rec.Body.String())
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale edit: %d\n%s", rec.Code, b)
	}
	for _, want := range []string{"was deleted after this form was opened", "Add ingredient", "/admin/ingredients/new"} {
		if !strings.Contains(b, want) {
			t.Fatalf("missing %q:\n%s", want, b)
		}
	}
	if _, ok := e.snap(t).Get("basil"); ok {
		t.Fatal("stale edit form must not resurrect the deleted ingredient")
	}

	var after int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM pending_changeset`).Scan(&after)
	if after != before {
		t.Fatalf("stale edit created a pending changeset: before=%d after=%d", before, after)
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
