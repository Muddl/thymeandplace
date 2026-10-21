package views

import (
	"bytes"
	"context"
	"html"
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
	got := buf.String()
	if strings.Count(got, `id="announce"`) != 1 {
		t.Fatalf("page must have exactly one #announce:\n%s", got)
	}
	if !strings.Contains(got, `id="announce" role="status"`) {
		t.Fatal("page #announce must have role=status")
	}
	if strings.Count(got, "aria-live") != 1 {
		t.Fatalf("page must have exactly one aria-live, got %d\n%s", strings.Count(got, "aria-live"), got)
	}
	wantH1 := "<h1>" + html.EscapeString(CopySiteHeading) + "</h1>"
	if !strings.Contains(got, wantH1) {
		t.Fatalf("missing fixed site h1 %q:\n%s", CopySiteHeading, got)
	}
	if strings.Count(got, "<h1>") != 1 {
		t.Fatalf("want one h1:\n%s", got)
	}
	if i, j := strings.Index(got, "<h1>"), strings.Index(got, `id="results"`); i < 0 || j < 0 || i > j {
		t.Fatal("h1 must sit in main above #results")
	}
	if !strings.Contains(got, "<h2>Lisbon, PT</h2>") {
		t.Fatalf("place label must stay the results h2:\n%s", got)
	}
	if strings.Contains(got, "<h1>Lisbon, PT</h1>") {
		t.Fatal("site h1 must not copy the results h2")
	}
	if v := CSPViolations(got); len(v) > 0 {
		t.Fatalf("csp: %v", v)
	}
}
