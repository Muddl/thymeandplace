package web

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/places"
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
		!strings.Contains(body, `hx-get="/season?lat=38.72&lng=-9.14&place=1&date=2026-08-15"`) ||
		!strings.Contains(body, "38.72°N, 9.14°W") {
		t.Fatalf("%d\n%s", code, body)
	}
}

// #15: the retry link carries only validated values, never the raw query.
func TestSeasonRetryLinkDropsJunk(t *testing.T) {
	hs := newHarness(t)
	hs.climate.down = true
	code, body, _ := hs.get("/season?lat=38.72&lng=-9.14&place=1&date=2026-08-15&x=javascript:alert(1)&utm=%22%3E", true)
	want := `href="/season?lat=38.72&lng=-9.14&place=1&date=2026-08-15"`
	if code != 503 || !strings.Contains(body, want) || strings.Contains(body, "javascript") || strings.Contains(body, "utm") {
		t.Fatalf("%d\n%s", code, body)
	}
}

// #15: a stale date is dropped from the retry link, as it is from item links.
func TestSeasonRetryLinkDropsStaleDate(t *testing.T) {
	hs := newHarness(t)
	hs.climate.down = true
	code, body, _ := hs.get("/season?lat=38.72&lng=-9.14&date=1999-01-01", true)
	if code != 503 || !strings.Contains(body, `href="/season?lat=38.72&lng=-9.14"`) || strings.Contains(body, "1999") {
		t.Fatalf("%d\n%s", code, body)
	}
}

// #15: before the catalog loads, the coordinates are already validated, so
// the retry link is rebuilt from them.
func TestSeasonNoSnapshotRetryLink(t *testing.T) {
	hs := newHarness(t)
	hs.snap = nil
	code, body, _ := hs.get("/season?lat=38.72&lng=-9.14&x=junk", true)
	if code != 503 || !strings.Contains(body, `href="/season?lat=38.72&lng=-9.14"`) || strings.Contains(body, "junk") {
		t.Fatalf("%d\n%s", code, body)
	}
}

// Review Focus 3.
func TestSeasonWarmCacheSurvivesOutage(t *testing.T) {
	hs := newHarness(t)
	if code, _, _ := hs.get(lisbonQ+"&date=2026-08-15", true); code != 200 {
		t.Fatal("warm-up failed")
	}
	calls, oceanCalls := hs.climate.calls.Load(), hs.ocean.calls.Load()
	hs.climate.down, hs.ocean.down, hs.places.down = true, true, true
	code, body, _ := hs.get(lisbonQ+"&date=2026-08-16", true)
	if code != 200 || !strings.Contains(body, "Tomato") || hs.climate.calls.Load() != calls || hs.ocean.calls.Load() != oceanCalls {
		t.Fatalf("cached result not served during outage: %d", code)
	}
	if !strings.Contains(body, "38.72°N, 9.14°W") { // core spec §11.6: the label falls back
		t.Fatalf("heading must fall back to coordinates during an outage:\n%s", body)
	}
	hs.version = "v2" // a data-version change makes the entry unreachable
	if code, _, _ := hs.get(lisbonQ+"&date=2026-08-16", true); code != 503 {
		t.Fatal("new data version must miss the cache")
	}
}

func TestSeasonPanicRendersPanel(t *testing.T) {
	hs := newHarness(t)
	hs.climate.panic = true
	code, body, _ := hs.get(lisbonQ+"&date=2026-08-15&x=junk", true)
	// #15: a panic's retry link is rebuilt from the validated query, so it
	// reloads the page rather than landing on a bare /season 400.
	if code != 500 || !strings.Contains(body, views.CopyLoadError) ||
		!strings.Contains(body, `href="/season?lat=38.72&lng=-9.14&place=1&date=2026-08-15"`) || strings.Contains(body, "junk") {
		t.Fatalf("%d\n%s", code, body)
	}
}

func TestSeasonFullPage(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(lisbonQ+"&date=2026-08-15", false)
	if !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, `id="results"`) || !strings.Contains(body, "Tomato") {
		t.Fatal("full page expected")
	}
	if strings.Contains(body, "autofocus") {
		t.Fatal("season results must not autofocus the search box")
	}
	if strings.Count(body, `id="announce"`) != 1 || strings.Count(body, "aria-live") != 1 {
		t.Fatal("season page must keep exactly one #announce live region")
	}
	if !strings.Contains(body, `data-announce="`) || !strings.Contains(body, "ingredients in season near Lisbon, PT") {
		t.Fatalf("season page must carry short data-announce:\n%s", body)
	}
}

func TestSeasonItemLinksCarryCellAndDate(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(lisbonQ+"&date=2026-08-15", true)
	if !strings.Contains(body, fmt.Sprintf("/ingredient/tomato?cell=%d&date=2026-08-15", lisbonCell)) {
		t.Fatalf("item link:\n%s", body)
	}
	// A cell that falls back still links its own (query) cell.
	hs = newHarness(t)
	withFallbackCells(hs)
	_, body, _ = hs.get("/season?lat=40.1&lng=-8.2&date=2026-08-15", true)
	if !strings.Contains(body, fmt.Sprintf("?cell=%d&", fallbackCell)) || strings.Contains(body, fmt.Sprintf("?cell=%d&", fallbackCell-1)) {
		t.Fatalf("fallback item link must carry the query cell:\n%s", body)
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
// label falls back and the cached results still render. The shared budget
// runs out inside ByID, so Nearest is never tried.
func TestSeasonHungPlaceLookupFallsBack(t *testing.T) {
	hs := newHarness(t, shortTimeout)
	if code, _, _ := hs.get(lisbonQ+"&date=2026-08-15", true); code != 200 {
		t.Fatal("warm-up failed")
	}
	hs.places.blockByID = true
	code, body, _ := hs.get(lisbonQ+"&date=2026-08-15", true)
	if code != 200 || !strings.Contains(body, "Tomato") || !strings.Contains(body, "38.72°N, 9.14°W") {
		t.Fatalf("%d\n%q", code, body)
	}
}

// Final review 6: a ?place= far from the requested point is ignored, so a
// crafted URL can't label mid-Atlantic results "Lisbon".
func TestSeasonIgnoresDistantPlaceLabel(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/season?lat=45.00&lng=-30.00&place=1&date=2026-08-15", true)
	if strings.Contains(body, "Lisbon") || !strings.Contains(body, "45.00°N, 30.00°W") {
		t.Fatalf("distant place label must fall back to coordinates:\n%s", body)
	}
	_, body, _ = hs.get("/season?lat=38.80&lng=-9.10&place=1&date=2026-08-15", true)
	if !strings.Contains(body, "<h2>Lisbon, PT</h2>") {
		t.Fatalf("a nearby place keeps its own label:\n%s", body)
	}
}

// Two points in fallbackCell (centre 40.125, −8.125): A in the west half,
// B in the east half. February, so the small catalog's cod (months 1–4) is
// in season wherever its ecoregion 25 is in reach.
const (
	qA = "/season?lat=40.1&lng=-8.2&date=2026-02-15"
	qB = "/season?lat=40.1&lng=-8.05&date=2026-02-15"
)

func february(hs *harness) { hs.now = time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC) }

// reachWestOf puts the edge of seafood reach at lng inside fallbackCell.
func reachWestOf(edge float64) func(float64, float64) []ocean.EcoregionID {
	return func(_, lng float64) []ocean.EcoregionID {
		if lng < edge {
			return []ocean.EcoregionID{25}
		}
		return nil
	}
}

// Each row puts a boundary between A and B, which a result computed from
// the first visitor's point would expose.
var sameCellRows = []struct {
	name  string
	setup func(*harness)
}{
	{"fallback neighbour", withFallbackCells},
	{"seafood reach", func(hs *harness) {
		hs.climate.cells = map[int]climate.MonthlyNormals{fallbackCell: climatetest.Temperate()}
		hs.ocean.reach = reachWestOf(-8.1) // centre and A in reach, B not
	}},
	{"supported", func(hs *harness) {
		hs.climate.cells = map[int]climate.MonthlyNormals{}
		hs.ocean.reach = reachWestOf(-8.1)
	}},
}

// headingRE strips per-request place copy (the h2 and data-announce) so
// same-cell responses can be compared for identical results.
var headingRE = regexp.MustCompile(`<h2>[^<]*</h2>|data-announce="[^"]*"`)

// Spec §6: two points in one cell get identical results in either order.
func TestSeasonSameCellSameResults(t *testing.T) {
	for _, row := range sameCellRows {
		t.Run(row.name, func(t *testing.T) {
			var bodies []string
			for _, order := range [][2]string{{qA, qB}, {qB, qA}} {
				hs := newHarness(t)
				february(hs)
				row.setup(hs)
				for _, q := range order {
					code, body, _ := hs.get(q, true)
					if code != 200 {
						t.Fatalf("%s: %d", q, code)
					}
					bodies = append(bodies, headingRE.ReplaceAllString(body, ""))
				}
			}
			for i, b := range bodies[1:] {
				if b != bodies[0] {
					t.Fatalf("response %d differs from response 0:\n%s\n----\n%s", i+1, bodies[0], b)
				}
			}
		})
	}
}

// Every reference-data lookup behind a result is at the query cell's centre.
func TestSeasonLookupsUseCellCentre(t *testing.T) {
	for _, p := range [][2]float64{{40.1, -8.2}, {40.1, -8.05}, {-33.87, 151.21}, {69.65, 18.96}, {0.01, -179.99}} {
		hs := newHarness(t)
		if code, _, _ := hs.get(fmt.Sprintf("/season?lat=%v&lng=%v&date=2026-08-15", p[0], p[1]), true); code != 200 {
			t.Fatalf("%v: %d", p, code)
		}
		lat, lng := climate.CellCenter(climate.CellID(p[0], p[1]))
		if len(hs.climate.points()) == 0 || len(hs.ocean.points()) == 0 {
			t.Fatalf("%v: climate %v, ocean %v: want both looked up", p, hs.climate.points(), hs.ocean.points())
		}
		for _, got := range append(hs.climate.points(), hs.ocean.points()...) {
			if got != [2]float64{lat, lng} {
				t.Fatalf("point %v: lookup at %v, want centre (%v, %v)", p, got, lat, lng)
			}
		}
	}
}

// The heading is this request's nearest place, never the first visitor's.
func TestSeasonLabelPerRequest(t *testing.T) {
	hs := newHarness(t)
	x := places.Place{ID: 7, Name: "Xville", Country: "PT", Lat: 40.1, Lng: -8.2}
	y := places.Place{ID: 8, Name: "Yton", Country: "PT", Lat: 40.1, Lng: -8.05}
	hs.places.nearest = func(lat, lng float64) (places.Place, bool) {
		p := y
		if lng < -8.125 {
			p = x
		}
		p.DistanceKm = climate.DistanceKm(lat, lng, p.Lat, p.Lng)
		return p, true
	}
	if _, body, _ := hs.get("/season?lat=40.1&lng=-8.2&date=2026-08-15", true); !strings.Contains(body, "near Xville, PT") {
		t.Fatalf("A:\n%s", body)
	}
	_, body, _ := hs.get("/season?lat=40.1&lng=-8.05&date=2026-08-15", true)
	if !strings.Contains(body, "near Yton, PT") || strings.Contains(body, "Xville") {
		t.Fatalf("B must get its own label:\n%s", body)
	}
	// Review Focus 1: places down on a cache hit.
	hs.places.down = true
	code, body, _ := hs.get("/season?lat=40.1&lng=-8.05&date=2026-08-15", true)
	if code != 200 || !strings.Contains(body, "40.10°N, 8.05°W") || !strings.Contains(body, "Tomato") {
		t.Fatalf("places down on a cache hit: %d\n%s", code, body)
	}
}
