package views

import (
	"bytes"
	"context"
	"html"
	"strings"
	"testing"
)

func renderPage(t *testing.T, p PageVM) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Page(p).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestSearchShell(t *testing.T) {
	home := renderPage(t, PageVM{Title: "What's in season"})
	if v := CSPViolations(home); len(v) > 0 {
		t.Fatalf("home CSP: %v", v)
	}
	if strings.Count(home, `id="announce"`) != 1 {
		t.Fatal("home must have exactly one #announce")
	}
	if strings.Count(home, "aria-live") != 1 {
		t.Fatalf("home must have exactly one aria-live, got %d\n%s", strings.Count(home, "aria-live"), home)
	}
	if !strings.Contains(home, `id="announce" role="status"`) {
		t.Fatal("home #announce must have role=status")
	}
	wantH1 := "<h1>" + html.EscapeString(CopySiteHeading) + "</h1>"
	if !strings.Contains(home, wantH1) {
		t.Fatalf("home must have a fixed site h1 %q", CopySiteHeading)
	}
	if !strings.Contains(home, `id="q"`) || !strings.Contains(home, "autofocus") {
		t.Fatal("home search must be #q with autofocus")
	}
	if !strings.Contains(home, `<header class="site">`) {
		t.Fatal("home must keep header.site")
	}

	season := renderPage(t, PageVM{Title: "Lisbon, PT", Results: Season(SeasonVM{
		Label: "Lisbon, PT", MonthName: "August",
		Groups: []Group{{Title: "Peak", Items: []Item{{Name: "Tomato", URL: "/ingredient/tomato"}}}},
	})})
	if v := CSPViolations(season); len(v) > 0 {
		t.Fatalf("season CSP: %v", v)
	}
	if strings.Count(season, "aria-live") != 1 || strings.Count(season, `id="announce"`) != 1 {
		t.Fatal("season page must keep exactly one #announce live region")
	}
	if strings.Contains(season, "autofocus") {
		t.Fatal("search must not autofocus when results are showing")
	}
	if !strings.Contains(season, `<header class="site">`) || !strings.Contains(season, `id="q"`) {
		t.Fatal("season page must keep the site header and search box")
	}
	if !strings.Contains(season, wantH1) {
		t.Fatalf("season page must keep the fixed site h1 %q", CopySiteHeading)
	}
	if i, j := strings.Index(season, "<h1>"), strings.Index(season, `id="results"`); i < 0 || j < 0 || i > j {
		t.Fatal("h1 must sit in main above #results")
	}
	if !strings.Contains(season, "<h2>Lisbon, PT</h2>") {
		t.Fatal("place label must stay the results h2")
	}
	if strings.Contains(season, "<h1>Lisbon, PT</h1>") {
		t.Fatal("site h1 must not copy the results h2")
	}
	want := `data-announce="1 ingredient in season near Lisbon, PT"`
	if !strings.Contains(season, want) {
		t.Fatalf("season fragment must carry short data-announce, want %q in\n%s", want, season)
	}
}

func TestAnnounceCopy(t *testing.T) {
	if got := AnnounceSeason(12, "Lisbon"); got != "12 ingredients in season near Lisbon" {
		t.Fatalf("AnnounceSeason(12) = %q", got)
	}
	if got := AnnounceSeason(12, "near Lisbon"); got != "12 ingredients in season near Lisbon" {
		t.Fatalf("AnnounceSeason must not double near: %q", got)
	}
	if got := AnnouncePlaces(3); got != "3 places" {
		t.Fatalf("AnnouncePlaces(3) = %q", got)
	}
	if got := AnnounceNoMatch("zzz"); got != "No places match 'zzz'." {
		t.Fatalf("AnnounceNoMatch = %q", got)
	}
}

func TestSearchAnotherPlaceHash(t *testing.T) {
	var buf bytes.Buffer
	if err := Unsupported(false).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `href="/#q"`) {
		t.Fatalf("Search another place must point at /#q:\n%s", buf.String())
	}
}
