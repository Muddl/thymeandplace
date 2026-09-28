package views

import (
	"bytes"
	"context"
	"html"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

var (
	scriptTagRE   = regexp.MustCompile(`(?i)<script\b[^>]*>`)
	scriptSrcRE   = regexp.MustCompile(`(?i)\ssrc=`)
	styleAttrRE   = regexp.MustCompile(`(?i)\sstyle=`)
	handlerAttrRE = regexp.MustCompile(`(?i)\son[a-z]+=`)
	hxOnRE        = regexp.MustCompile(`(?i)hx-on`)
)

// cspViolations lists what in html the admin CSP (no inline script or style)
// or the htmx config (no eval) would block. It matches the public views'
// check in internal/web/views.
func cspViolations(html string) []string {
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

func TestTemplatesFitCSP(t *testing.T) {
	signedIn := LayoutVM{Title: "t", CSRF: "tok", SignedIn: true}
	signedOut := LayoutVM{Title: "t", CSRF: "tok"}
	opts := []Option{{Value: "a", Label: "A", Selected: true}, {Value: "b", Label: "B"}}
	form := FormVM{
		Layout: signedIn, Action: "/admin/ingredients/tomato/preview", Kind: "annual",
		Values:     map[string]string{"slug": "tomato", "name": "Tomato", "frost": "tender"},
		Errors:     map[string][]string{"": {"bad"}, "name": {"required"}},
		Fields:     []string{"frost", "grow_temp_min_c", "chill_months_min", "fruiting_temp_min_c", "storage_months", "regions"},
		Categories: opts, Kinds: opts, Message: "m",
	}
	newForm := form
	newForm.IsNew, newForm.Fields = true, nil
	catalog := CatalogVM{Layout: signedIn, Q: "tom", Categories: opts, Kinds: opts, Rows: []CatalogRow{{Slug: "tomato", Name: "Tomato"}}, Total: 1, Message: "saved"}
	barRows := []BarRow{{Fixture: "lisbon", Before: []BarCell{{Class: "s-in", Title: "January"}}, After: []BarCell{{Class: "s-out", Title: "January"}}, HadBefore: true, HasAfter: true}, {Fixture: "oslo"}}
	preview := PreviewVM{
		Layout: signedIn, ID: 1, Source: "edit", DeleteSlug: "tomato", Added: []ItemRow{{Slug: "a", Name: "A"}},
		Deleted: []ItemRow{{Slug: "d", Name: "D"}}, Changed: []ChangedRow{{Slug: "c", Name: "C", Fields: []FieldChange{{Field: "name", Before: "x", After: "y"}}}},
		ScoreBefore: "0.9", ScoreAfter: "0.8", Delta: "-0.1", Compared: 1, NeedsOverride: true,
		Items: []ScoreRow{{Slug: "c", Name: "C", Before: "1", After: "0", Bars: barRows}}, Message: "m", ExpiresAt: "12:00",
	}
	components := map[string]templ.Component{
		"Layout":           Layout(signedIn),
		"LoginPage":        LoginPage(LoginVM{Layout: signedOut, BackupEnabled: true, Message: "m"}),
		"BackupPage":       BackupPage(BackupVM{Layout: signedOut, Message: "m"}),
		"EnrollPage":       EnrollPage(EnrollVM{Layout: signedOut, Token: "t", Valid: true}),
		"EnrollPage bad":   EnrollPage(EnrollVM{Layout: signedOut}),
		"SecurityPage":     SecurityPage(SecurityVM{Layout: signedIn, Passkeys: []PasskeyRow{{ID: "AAAA", Name: "Phone"}}, RecentAuth: true, BackupEnabled: true, Message: "m"}),
		"SecurityPage old": SecurityPage(SecurityVM{Layout: signedIn}),
		"CatalogPage":      CatalogPage(catalog),
		"CatalogRows":      CatalogRows(catalog),
		"MessagePage":      MessagePage(signedIn, "m", []Link{{Href: "/admin/import", Label: "Import"}}),
		"FormPage":         FormPage(form),
		"FormPage new":     FormPage(newForm),
		"KindFields":       KindFields(form),
		"PreviewPage":      PreviewPage(preview),
		"PreviewPage none": PreviewPage(PreviewVM{Layout: signedIn, Empty: true}),
		"ImportPage":       ImportPage(ImportVM{Layout: signedIn, Message: "m", Problems: []string{"p"}}),
		"HistoryPage":      HistoryPage(HistoryVM{Layout: signedIn, Slug: "s", Rows: []HistoryRow{{ID: 1, At: "now"}}, OlderURL: "/admin/history?before=1"}),
	}
	for name, c := range components {
		var buf bytes.Buffer
		if err := c.Render(context.Background(), &buf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v := cspViolations(buf.String()); len(v) > 0 {
			t.Errorf("%s: %v", name, v)
		}
	}
}

func TestCSPViolationsCatchesInlineContent(t *testing.T) {
	for _, bad := range []string{`<script>x()</script>`, `<p style="x">`, `<a onclick="x()">`, `<div hx-on::after-request="x()">`} {
		if len(cspViolations(bad)) == 0 {
			t.Errorf("not caught: %s", bad)
		}
	}
}

func TestHTMXConfigDisablesEval(t *testing.T) {
	var buf bytes.Buffer
	if err := Layout(LayoutVM{Title: "t"}).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	body := html.UnescapeString(buf.String())
	for _, want := range []string{`"allowEval":false`, `"allowScriptTags":false`, `"selfRequestsOnly":true`} {
		if !strings.Contains(body, want) {
			t.Fatalf("htmx config missing %s:\n%s", want, body)
		}
	}
}
