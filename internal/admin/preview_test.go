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
