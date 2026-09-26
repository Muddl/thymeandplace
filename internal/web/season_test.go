package web

import (
	"strings"
	"testing"
	"time"

	"thymeandplace/internal/web/views"
)

const lisbonQ = "/season?lat=38.7200&lng=-9.1400&place=1"

func TestSeasonGroups(t *testing.T) {
	hs := newHarness(t)
	code, body, _ := hs.get(lisbonQ+"&date=2026-08-15", true)
	if code != 200 || strings.Contains(body, "<html") {
		t.Fatalf("%d\n%s", code, body)
	}
	for _, want := range []string{"Lisbon, PT", "August", "Peak", "Year-round", "Seafood", "Tomato", "Banana", "Mackerel", "Estimate", "general guidance; check local fishing rules"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in\n%s", want, body)
		}
	}
	if strings.Contains(body, "Kale") {
		t.Fatal("kale is out of season in August")
	}
	if i, j := strings.Index(body, ">Peak<"), strings.Index(body, ">Seafood<"); i < 0 || j < i {
		t.Fatal("seafood group must come after the land groups")
	}
}

func TestSeasonInlandOmitsSeafood(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/season?lat=40.4&lng=3.7&date=2026-08-15", true)
	if strings.Contains(body, "Seafood") || strings.Contains(body, "Mackerel") {
		t.Fatal("inland must omit the seafood group")
	}
	if strings.Contains(body, views.CopyUnsupported) {
		t.Fatal("inland is supported")
	}
}

func TestSeasonLabelNearestOrCoordinates(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/season?lat=38.80&lng=-9.10&date=2026-08-15", true)
	if !strings.Contains(body, "near Lisbon, PT") {
		t.Fatalf("near label missing:\n%s", body)
	}
	_, body, _ = hs.get("/season?lat=45.00&lng=-30.00&date=2026-08-15", true)
	if !strings.Contains(body, "45.00°N, 30.00°W") {
		t.Fatalf("coordinate label missing:\n%s", body)
	}
}

func TestSeasonUnsupported(t *testing.T) {
	hs := newHarness(t)
	code, body, _ := hs.get("/season?lat=-60&lng=20", true)
	if code != 200 || !strings.Contains(body, views.CopyUnsupported) || !strings.Contains(body, views.CopySearchAnother) {
		t.Fatalf("%d\n%s", code, body)
	}
}

func TestSeasonHarsh(t *testing.T) {
	hs := newHarness(t)
	hs.now = time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	_, body, _ := hs.get("/season?lat=70&lng=20&date=2026-01-10", true)
	if !strings.Contains(body, views.CopyHarsh) || !strings.Contains(body, "Banana") {
		t.Fatalf("harsh:\n%s", body)
	}
}

// Review Focus 1.
func TestSeasonBadCoordinates(t *testing.T) {
	hs := newHarness(t)
	for _, q := range []string{"lat=abc&lng=1", "lat=NaN&lng=1", "lat=1&lng=Inf", "lat=91&lng=0", "lat=0&lng=181", "lat=1", ""} {
		code, body, _ := hs.get("/season?"+q, true)
		if code != 400 || !strings.Contains(body, views.CopyUnsupported) {
			t.Fatalf("%q: %d", q, code)
		}
	}
}

// Review Focus 2: the cook's local date wins across a year boundary, in
// either direction, as long as it is within a calendar day of server UTC.
func TestSeasonMonthFromLocalDate(t *testing.T) {
	hs := newHarness(t)
	hs.now = time.Date(2026, 12, 31, 20, 0, 0, 0, time.UTC) // local date already 2027-01-01 east of UTC+4
	_, body, _ := hs.get(lisbonQ+"&date=2027-01-01", true)
	if !strings.Contains(body, "January") {
		t.Fatal("client's local January must win over server December")
	}
	code, body, _ := hs.get(lisbonQ+"&date=31/12/2026", true)
	if code != 200 || !strings.Contains(body, "December") {
		t.Fatalf("malformed date must fall back to the server month: %d", code)
	}
	hs.now = time.Date(2027, 1, 1, 3, 0, 0, 0, time.UTC) // local date still 2026-12-31 west of UTC-3
	if _, body, _ = hs.get(lisbonQ+"&date=2026-12-31", true); !strings.Contains(body, "December") {
		t.Fatal("client's local December must win over server January")
	}
}

// Final review 3: a date frozen into a bookmarked URL is ignored once it is
// more than a calendar day from the server's UTC date (§12.5).
func TestSeasonIgnoresStaleDate(t *testing.T) {
	hs := newHarness(t) // server clock: 2026-08-15 UTC
	code, body, _ := hs.get(lisbonQ+"&date=2026-09-14", true)
	if code != 200 || !strings.Contains(body, "August") || strings.Contains(body, "September") {
		t.Fatalf("stale date must fall back to the server month: %d\n%s", code, body)
	}
	if strings.Contains(body, "date=2026-09-14") {
		t.Fatal("a stale date must not be carried into item links")
	}
}

func TestSeasonDatabaseDown(t *testing.T) {
	hs := newHarness(t)
	hs.climate.down = true
	code, body, _ := hs.get(lisbonQ+"&date=2026-08-15", true)
	if code != 503 || !strings.Contains(body, views.CopyLoadError) || !strings.Contains(body, views.CopyTryAgain) ||
		!strings.Contains(body, `hx-get="/season?lat=38.7200&lng=-9.1400&place=1&date=2026-08-15"`) ||
		!strings.Contains(body, "38.72°N, 9.14°W") {
		t.Fatalf("%d\n%s", code, body)
	}
}

// Review Focus 3.
func TestSeasonWarmCacheSurvivesOutage(t *testing.T) {
	hs := newHarness(t)
	if code, _, _ := hs.get(lisbonQ+"&date=2026-08-15", true); code != 200 {
		t.Fatal("warm-up failed")
	}
	calls := hs.climate.calls
	hs.climate.down, hs.ocean.down, hs.places.down = true, true, true
	code, body, _ := hs.get(lisbonQ+"&date=2026-08-16", true)
	if code != 200 || !strings.Contains(body, "Tomato") || hs.climate.calls != calls {
		t.Fatalf("cached result not served during outage: %d", code)
	}
	hs.version = "v2" // a data-version change makes the entry unreachable
	if code, _, _ := hs.get(lisbonQ+"&date=2026-08-16", true); code != 503 {
		t.Fatal("new data version must miss the cache")
	}
}

func TestSeasonPanicRendersPanel(t *testing.T) {
	hs := newHarness(t)
	hs.climate.panic = true
	code, body, _ := hs.get(lisbonQ, true)
	if code != 500 || !strings.Contains(body, views.CopyLoadError) {
		t.Fatalf("%d\n%s", code, body)
	}
}

func TestSeasonFullPage(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(lisbonQ+"&date=2026-08-15", false)
	if !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, `id="results"`) || !strings.Contains(body, "Tomato") {
		t.Fatal("full page expected")
	}
}

func TestSeasonItemLinksCarryCellAndDate(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(lisbonQ+"&date=2026-08-15", true)
	if !strings.Contains(body, "/ingredient/tomato?cell=") || !strings.Contains(body, "&date=2026-08-15") {
		t.Fatalf("item link:\n%s", body)
	}
}

// Final review 1: once the request deadline has passed, the error panel must
// still render (templ components bail out on a done context).
func TestSeasonTimeoutRendersLoadError(t *testing.T) {
	hs := newHarness(t, shortTimeout)
	hs.climate.block = true
	code, body, _ := hs.get(lisbonQ+"&date=2026-08-15", true)
	if code != 503 || !strings.Contains(body, views.CopyLoadError) {
		t.Fatalf("%d\n%q", code, body)
	}
}

// Final review 1: a hung place lookup must not eat the whole budget; the
// label falls back and the cached results still render.
func TestSeasonHungPlaceLookupFallsBack(t *testing.T) {
	hs := newHarness(t, shortTimeout)
	if code, _, _ := hs.get(lisbonQ+"&date=2026-08-15", true); code != 200 {
		t.Fatal("warm-up failed")
	}
	hs.places.blockByID = true
	code, body, _ := hs.get(lisbonQ+"&date=2026-08-15", true)
	if code != 200 || !strings.Contains(body, "Tomato") || !strings.Contains(body, "near Lisbon, PT") {
		t.Fatalf("%d\n%q", code, body)
	}
}
