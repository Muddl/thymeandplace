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
	f := tomatoForm()
	f.Set("note", "changed")
	id := pendingID(t, c.postForm("/admin/ingredients/tomato/preview", f).Body.String())
	_, _ = e.pool.Exec(t.Context(), `UPDATE ingredient SET note = 'elsewhere' WHERE slug = 'banana'`)
	rec := c.postForm("/admin/changesets/"+id+"/apply", url.Values{})
	if rec.Code != 409 || !strings.Contains(html.UnescapeString(rec.Body.String()), "The catalog changed since this preview. Preview again.") {
		t.Fatalf("stale: %d", rec.Code)
	}
}

// Review Focus 5, updated by Review Focus 4 (2026-09-25 final review): a
// resubmit of an already-applied changeset (e.g. a double-clicked Apply
// button, or an old tab's form resent) must land on the same success page a
// first apply would, not a confusing "expired" error — the pending row is
// gone in both cases (ErrNotFound), but catalog_history already has rows
// for this change_id, which tells the two apart.
func TestApplyResubmitted(t *testing.T) {
	e, c := catalogEnv(t)
	f := tomatoForm()
	f.Set("note", "once")
	id := pendingID(t, c.postForm("/admin/ingredients/tomato/preview", f).Body.String())
	c.postForm("/admin/changesets/"+id+"/apply", url.Values{})
	var before int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM catalog_history`).Scan(&before)
	rec := c.postForm("/admin/changesets/"+id+"/apply", url.Values{})
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin?applied=1" {
		t.Fatalf("resubmit: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	var after int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM catalog_history`).Scan(&after)
	if after != before {
		t.Fatal("resubmitted apply wrote history a second time")
	}
}

// A truly expired preview (never applied: no pending row, no history for
// this change_id) still gets the 410 page.
func TestApplyExpiredNeverAppliedIs410(t *testing.T) {
	e, c := catalogEnv(t)
	f := tomatoForm()
	f.Set("note", "changed")
	id := pendingID(t, c.postForm("/admin/ingredients/tomato/preview", f).Body.String())
	_, _ = e.pool.Exec(t.Context(), `UPDATE pending_changeset SET expires_at = now() - interval '1 second'`)
	rec := c.postForm("/admin/changesets/"+id+"/apply", url.Values{})
	if rec.Code != 410 || !strings.Contains(rec.Body.String(), "expired or was already applied") {
		t.Fatalf("expired: %d %s", rec.Code, rec.Body.String())
	}
}
