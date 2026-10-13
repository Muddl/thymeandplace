package views

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
)

var itemBlockRE = regexp.MustCompile(`(?s)<li class="item[^"]*">.*?</li>`)

func TestSeasonRowsLabelEstimate(t *testing.T) {
	vm := SeasonVM{
		Label: "Lisbon, PT", MonthName: "August",
		Groups: []Group{
			{Title: "Peak", Items: []Item{
				{Name: "Tomato", StatusClass: "s-peak", Basis: "Grown near you", URL: "/ingredient/tomato", Estimate: true},
				{Name: "Mango", StatusClass: "s-in", Basis: "Imported", URL: "/ingredient/mango", Estimate: false},
			}},
		},
	}
	var buf bytes.Buffer
	if err := Season(vm).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if strings.Contains(html, "ESTIMATE") {
		t.Fatal("source must keep Estimate in normal case")
	}
	items := itemBlockRE.FindAllString(html, -1)
	if len(items) != 2 {
		t.Fatalf("items %d:\n%s", len(items), html)
	}
	wantBadge := `<span class="badge">` + CopyEstimate + `</span>`
	wantMark := `<span class="approx" aria-hidden="true">` + CopyEstimateMark + `</span>`
	for _, item := range items {
		if !strings.Contains(item, wantBadge) {
			t.Fatalf("missing %s in\n%s", wantBadge, item)
		}
		if !strings.Contains(item, wantMark) {
			t.Fatalf("missing %s in\n%s", wantMark, item)
		}
	}
	if i := strings.Index(html, `href="/about/data#estimates"`); i < 0 {
		t.Fatal("missing how-we-estimate link")
	} else if strings.Contains(html[i:i+120], CopyEstimateMark) {
		t.Fatal("how-we-estimate link must not include ≈")
	}
	if v := CSPViolations(html); len(v) > 0 {
		t.Fatalf("csp: %v", v)
	}
}
