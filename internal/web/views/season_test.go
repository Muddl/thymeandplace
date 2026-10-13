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
	if !strings.Contains(items[0], "Tomato") || !strings.Contains(items[0], wantBadge) || !strings.Contains(items[0], wantMark) {
		t.Fatalf("Tomato must show Estimate and ≈:\n%s", items[0])
	}
	if !strings.Contains(items[1], "Mango") {
		t.Fatalf("second row should be Mango:\n%s", items[1])
	}
	if strings.Contains(items[1], wantBadge) || strings.Contains(items[1], CopyEstimate) || strings.Contains(items[1], CopyEstimateMark) {
		t.Fatalf("Mango (Estimate:false) must not show Estimate or ≈:\n%s", items[1])
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
