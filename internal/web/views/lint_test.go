package views

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	"thymeandplace/internal/season"
)

var (
	scriptTagRE   = regexp.MustCompile(`(?i)<script\b[^>]*>`)
	scriptSrcRE   = regexp.MustCompile(`(?i)\ssrc=`)
	styleAttrRE   = regexp.MustCompile(`(?i)\sstyle=`)
	handlerAttrRE = regexp.MustCompile(`(?i)\son[a-z]+=`)
	hxOnRE        = regexp.MustCompile(`(?i)hx-on`)
)

// CSPViolations lists what in html the site's CSP (no inline script or
// style) or the htmx config (no eval) would block.
func CSPViolations(html string) []string {
	var out []string
	for _, tag := range scriptTagRE.FindAllString(html, -1) {
		if !scriptSrcRE.MatchString(tag) {
			out = append(out, "inline <script>: "+tag)
		}
	}
	for _, re := range []*regexp.Regexp{styleAttrRE, handlerAttrRE, hxOnRE} {
		for _, m := range re.FindAllString(html, -1) {
			out = append(out, "attribute "+m)
		}
	}
	return out
}

// Every public component must render without anything the CSP would block,
// so the policy can stay free of 'unsafe-inline'.
func TestTemplatesFitCSP(t *testing.T) {
	var months [12]season.Status
	for i := range months {
		months[i] = season.Status(i % 5)
	}
	seasonVM := SeasonVM{Label: "Lisbon, PT", MonthName: "August", Harsh: true, Groups: []Group{
		{Title: "Peak", Items: []Item{{Name: "Tomato", StatusClass: "s-peak", Basis: "Grown near you", URL: "/ingredient/tomato?cell=1", Estimate: true}}},
	}}
	ingredient := IngredientVM{
		Name: "Tomato", Category: "vegetable", Basis: "Grown near you", EstimateNote: CopyLandEstimate, Local: true, WildMushroom: true,
		Bars: MonthBars(months, time.August), TipChoose: "Heavy for size.", TipStore: "Counter.", Note: "A note.",
		Pairings: []Link{{Name: "Basil", URL: "/ingredient/basil?cell=1"}}, BackURL: "/season?lat=1&lng=2",
	}
	places := PlaceList([]PlaceItem{{Label: "Lisbon, PT", URL: "/season?lat=1&lng=2&place=1"}})
	components := map[string]templ.Component{
		"Page":              Page(PageVM{Title: "t", Query: "lis", Places: places, Results: Season(seasonVM)}),
		"Page empty":        Page(PageVM{Title: "t"}),
		"Location":          Location("Lisbon"),
		"Unsupported":       Unsupported(true),
		"Unsupported land":  Unsupported(false),
		"LoadError":         LoadError("Lisbon", "/season?lat=1&lng=2"),
		"NotFound":          NotFound(),
		"TooMany":           TooMany(),
		"Season":            Season(seasonVM),
		"Ingredient":        Ingredient(ingredient),
		"Ingredient remote": Ingredient(IngredientVM{Name: "Mango", BackURL: "/"}),
		"PlaceList":         places,
		"PlacesNoMatch":     PlacesNoMatch("zzz"),
		"PlacesPaused":      PlacesPaused(),
		"PlacesKeepTyping":  PlacesKeepTyping(),
		"PlacesUnavailable": PlacesUnavailable(),
		"About":             About(true),
		"About land":        About(false),
	}
	for name, c := range components {
		var buf bytes.Buffer
		if err := c.Render(context.Background(), &buf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v := CSPViolations(buf.String()); len(v) > 0 {
			t.Errorf("%s: %v", name, v)
		}
	}
}

func TestCSPViolationsCatchesInlineContent(t *testing.T) {
	for _, bad := range []string{
		`<script>alert(1)</script>`, `<p style="color:red">`, `<a onclick="x()">`, `<div hx-on:click="x()">`, `<img src=x onerror=y>`,
	} {
		if len(CSPViolations(bad)) == 0 {
			t.Errorf("not caught: %s", bad)
		}
	}
	if v := CSPViolations(`<script src="/static/app.js" defer></script><section aria-live="polite">Location</section>`); len(v) > 0 {
		t.Errorf("false positive: %v", v)
	}
}

// #15: public views never bypass templ's URL sanitizer. Every href goes
// through templ.URL, even when the URL is built from server values. The
// hand-written Go (view models) is checked too, since it could build a
// SafeURL and pass it in; generated and test files are not.
func TestNoSafeURLInPublicViews(t *testing.T) {
	files, err := filepath.Glob("*.templ")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templ files: %v", err)
	}
	goFiles, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range goFiles {
		if !strings.HasSuffix(f, "_templ.go") && !strings.HasSuffix(f, "_test.go") {
			files = append(files, f)
		}
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
	html := buf.String()
	if !strings.Contains(html, `href="about:invalid#TemplFailedSanitizationURL"`) {
		t.Fatalf("retry href not sanitized:\n%s", html)
	}
	if !strings.Contains(html, `role="alert"`) {
		t.Fatal("LoadError must be role=alert")
	}
	if strings.Contains(html, "data-announce") {
		t.Fatal("LoadError must not also data-announce")
	}
}
