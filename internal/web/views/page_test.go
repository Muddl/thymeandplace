package views

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestPageKickoffLandmarks(t *testing.T) {
	var buf bytes.Buffer
	err := Page(PageVM{Title: "Lisbon, PT", Results: Season(SeasonVM{
		Label: "Lisbon, PT", MonthName: "August",
		Groups: []Group{{Title: "Peak", Items: []Item{{Name: "Tomato", StatusClass: "s-peak", Basis: "Grown near you", URL: "/ingredient/tomato", Estimate: true}}}},
	})}).Render(context.Background(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if strings.Count(html, `id="announce"`) != 1 {
		t.Fatalf("want one #announce:\n%s", html)
	}
	if !strings.Contains(html, `id="announce" role="status"`) && !strings.Contains(html, `role="status" id="announce"`) {
		t.Fatalf("#announce must have role=status:\n%s", html)
	}
	if strings.Count(html, `aria-live="polite"`) != 1 {
		t.Fatalf("want one aria-live region:\n%s", html)
	}
	if !strings.Contains(html, "<h1>Lisbon, PT</h1>") {
		t.Fatalf("missing main h1:\n%s", html)
	}
	if i, j := strings.Index(html, "<h1>"), strings.Index(html, `id="results"`); i < 0 || j < 0 || i > j {
		t.Fatal("h1 must sit in main above #results")
	}
	if v := CSPViolations(html); len(v) > 0 {
		t.Fatalf("csp: %v", v)
	}
}
