package web

import (
	"net/url"
	"strings"
	"testing"

	"thymeandplace/internal/web/views"
)

func TestPlacesMatches(t *testing.T) {
	hs := newHarness(t)
	code, body, hdr := hs.get("/places?q=lis", true)
	if code != 200 || strings.Contains(body, "<html") || !strings.Contains(body, "Lisbon, PT") ||
		!strings.Contains(body, `/season?lat=38.7200&lng=-9.1400&place=1`) || !strings.Contains(hdr.Get("Vary"), "HX-Request") {
		t.Fatalf("%d %v\n%s", code, hdr, body)
	}
}

func TestPlacesNoMatch(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/places?q=zzz", true)
	if !strings.Contains(body, "No places match 'zzz'. "+views.CopyNoMatchSuffix) {
		t.Fatalf("body = %s", body)
	}
}

// Review Focus 5: markup in the query is escaped.
func TestPlacesEscapesQuery(t *testing.T) {
	hs := newHarness(t)
	r := `/places?q=%3Cscript%3Ealert(1)%3C%2Fscript%3E`
	code, _, _ := hs.get(r, true)
	if code != 200 {
		t.Fatal(code)
	}
	raw := rawBody(t, hs, r)
	if strings.Contains(raw, "<script>alert(1)</script>") || !strings.Contains(raw, "&lt;script&gt;") {
		t.Fatalf("unescaped markup: %s", raw)
	}
}

func TestPlacesBlankQuery(t *testing.T) {
	hs := newHarness(t)
	code, body, _ := hs.get("/places?q=%20%20", true)
	if code != 200 || strings.Contains(body, "No places match") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestPlacesDown(t *testing.T) {
	hs := newHarness(t)
	hs.places.down = true
	code, body, _ := hs.get("/places?q=lis", true)
	if code != 503 || !strings.Contains(body, views.CopySearchUnavailable) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestPlacesRateLimited(t *testing.T) {
	hs := newHarness(t)
	var code int
	var body string
	var hdr = map[string][]string{}
	for i := 0; i < 21; i++ {
		code, body, hdr = hs.get("/places?q=lis", true)
	}
	if code != 429 || !strings.Contains(body, views.CopyPaused) || len(hdr["Retry-After"]) == 0 {
		t.Fatalf("%d %s", code, body)
	}
}

func TestPlacesFullPageWithoutHX(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/places?q=lis", false)
	if !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, `value="lis"`) || !strings.Contains(body, "Lisbon, PT") {
		t.Fatalf("body = %s", body)
	}
}

// Final review 11: the echoed no-match query is cut to 100 runes.
func TestPlacesNoMatchTruncatesQuery(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/places?q="+url.QueryEscape(strings.Repeat("é", 150)), true)
	if !strings.Contains(body, "No places match '"+strings.Repeat("é", 100)+"'") {
		t.Fatalf("body = %s", body)
	}
}

// #14: a query under 2 letters gets a hint, not a search.
func TestPlacesShortQueryHint(t *testing.T) {
	hs := newHarness(t)
	for _, q := range []string{"l", "%25", "a.", "%C3%A9"} { // l, %, a., é
		code, body, _ := hs.get("/places?q="+q, true)
		if code != 200 || !strings.Contains(body, views.CopyKeepTyping) || strings.Contains(body, "No places match") {
			t.Fatalf("q=%s: %d\n%s", q, code, body)
		}
	}
	if n := hs.places.searches.Load(); n != 0 {
		t.Fatalf("Search called %d times for short queries", n)
	}
	code, body, _ := hs.get("/places?q=l", false)
	if code != 200 || !strings.Contains(body, views.CopyKeepTyping) || !strings.Contains(body, `value="l"`) {
		t.Fatalf("full page: %d\n%s", code, body)
	}
	if _, body, _ := hs.get("/places?q=li", true); strings.Contains(body, views.CopyKeepTyping) {
		t.Fatalf("two letters must search:\n%s", body)
	}
}
