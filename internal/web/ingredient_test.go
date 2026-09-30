package web

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/web/views"
)

var lisbonCell = climate.CellID(38.72, -9.14)

func TestIngredientDetail(t *testing.T) {
	hs := newHarness(t)
	code, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d&date=2026-08-15", lisbonCell), true)
	if code != 200 || strings.Contains(body, "<html") {
		t.Fatalf("%d\n%s", code, body)
	}
	for _, want := range []string{"Tomato", "Heavy for its size", "Stem-side down", "Basil", `title="August: peak"`, "now", views.CopyLandEstimate, "growing conditions Apr–Sep"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, views.WildMushroomDisclaimer) {
		t.Fatal("disclaimer is for wild mushrooms only")
	}
}

func TestIngredientWildMushroomDisclaimer(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(fmt.Sprintf("/ingredient/chanterelle?cell=%d", lisbonCell), true)
	if !strings.Contains(body, views.WildMushroomDisclaimer) {
		t.Fatal("wild mushroom disclaimer missing")
	}
}

func TestIngredientSeafood(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(fmt.Sprintf("/ingredient/atlantic-cod?cell=%d", lisbonCell), true)
	if !strings.Contains(body, "general guidance; check local fishing rules") || strings.Contains(body, views.CopyLandEstimate) {
		t.Fatalf("seafood detail:\n%s", body)
	}
}

func TestIngredientNotLocal(t *testing.T) {
	hs := newHarness(t)
	inland := climate.CellID(40.4, 3.7)
	_, body, _ := hs.get(fmt.Sprintf("/ingredient/atlantic-cod?cell=%d", inland), true)
	if !strings.Contains(body, "Not grown or landed near here") || !strings.Contains(body, "Firm, translucent flesh") {
		t.Fatalf("not-local detail:\n%s", body)
	}
}

func TestIngredientErrors(t *testing.T) {
	hs := newHarness(t)
	if code, body, _ := hs.get("/ingredient/unicorn?cell=1", true); code != 404 || !strings.Contains(body, views.CopyNotFound) {
		t.Fatalf("unknown slug: %d", code)
	}
	for _, c := range []string{"", "cell=abc", "cell=-1", "cell=99999999"} {
		if code, _, _ := hs.get("/ingredient/tomato?"+c, true); code != 400 {
			t.Fatalf("%q: %d", c, code)
		}
	}
	hs.climate.down = true
	if code, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d", lisbonCell), true); code != 503 || !strings.Contains(body, views.CopyLoadError) {
		t.Fatalf("outage: %d", code)
	}
}

func TestIngredientFullPage(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d", lisbonCell), false)
	if !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, "Tomato") {
		t.Fatal("full page expected")
	}
}

// A cell with no land row whose west and east neighbours both have normals.
// From its centre the two are tied, so the west (lower ID) wins.
var fallbackCell = climate.CellID(40.1, -8.2) // lat 40.0–40.25, lng −8.25 to −8.0

func withFallbackCells(hs *harness) {
	hs.climate.cells = map[int]climate.MonthlyNormals{fallbackCell - 1: climatetest.Temperate(), fallbackCell + 1: climatetest.Polar()}
}

var backURLRE = regexp.MustCompile(`href="/season\?lat=(-?[0-9.]+)&lng=(-?[0-9.]+)`)

// /ingredient?cell= is the query cell: it resolves with the fallback, and
// Back and the pairings keep the same cell.
func TestIngredientUsesQueryCell(t *testing.T) {
	hs := newHarness(t)
	withFallbackCells(hs)
	code, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d&date=2026-08-15", fallbackCell), true)
	if code != 200 || strings.Contains(body, views.CopyNotLocal) {
		t.Fatalf("fallback cell must use the west neighbour's normals: %d\n%s", code, body)
	}
	m := backURLRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no Back link:\n%s", body)
	}
	lat, _ := strconv.ParseFloat(m[1], 64)
	lng, _ := strconv.ParseFloat(m[2], 64)
	if climate.CellID(lat, lng) != fallbackCell {
		t.Fatalf("Back goes to cell %d, want %d", climate.CellID(lat, lng), fallbackCell)
	}
	if !strings.Contains(body, fmt.Sprintf("/ingredient/basil?cell=%d", fallbackCell)) {
		t.Fatalf("pairing must carry the query cell:\n%s", body)
	}
	lat0, lng0 := climate.CellCenter(fallbackCell)
	if len(hs.climate.points()) == 0 || len(hs.ocean.points()) == 0 {
		t.Fatalf("climate %v, ocean %v: want both looked up", hs.climate.points(), hs.ocean.points())
	}
	for _, p := range append(hs.climate.points(), hs.ocean.points()...) {
		if p != [2]float64{lat0, lng0} {
			t.Fatalf("lookup at %v, want the cell centre (%v, %v)", p, lat0, lng0)
		}
	}
}

// Review Focus 3: links issued before this change carry the normals cell, a
// land cell, which resolves to itself: they render exactly as before.
func TestIngredientOldLandCellLink(t *testing.T) {
	hs := newHarness(t)
	withFallbackCells(hs)
	west := fallbackCell - 1
	code, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d&date=2026-08-15", west), true)
	if code != 200 || strings.Contains(body, views.CopyNotLocal) || !strings.Contains(body, fmt.Sprintf("/ingredient/basil?cell=%d", west)) {
		t.Fatalf("%d\n%s", code, body)
	}
}

// Review Focus 4: with seafood off, neither page ever asks the ocean.
func TestSeafoodOffNeverQueriesOcean(t *testing.T) {
	hs := newHarness(t, seafoodOff)
	for _, u := range []string{lisbonQ + "&date=2026-08-15", fmt.Sprintf("/ingredient/tomato?cell=%d", climate.CellID(10, 10))} {
		if code, body, _ := hs.get(u, true); code != 200 {
			t.Fatalf("%s: %d\n%s", u, code, body)
		}
	}
	if n := hs.ocean.calls.Load(); n != 0 {
		t.Fatalf("ocean queried %d times with seafood off", n)
	}
}

var listItemRE = regexp.MustCompile(`<li class="item ([^"]+)">\s*<a href="([^"]+)"`)

// Spec §6: every item on /season opens a detail page that agrees with it:
// never "not local", and the current month has the list's status.
func TestIngredientAgreesWithSeasonList(t *testing.T) {
	rows := []struct {
		name  string
		setup func(*harness)
	}{
		{"fallback neighbour", withFallbackCells},
		{"reach edge between A and the centre", func(hs *harness) {
			hs.climate.cells = map[int]climate.MonthlyNormals{fallbackCell: climatetest.Temperate()}
			hs.ocean.reach = reachWestOf(-8.15) // A in reach, centre and B not
		}},
	}
	for _, row := range rows {
		for _, q := range []string{qA, qB} {
			t.Run(row.name+" "+q, func(t *testing.T) {
				hs := newHarness(t)
				february(hs)
				row.setup(hs)
				_, list, _ := hs.get(q, true)
				items := listItemRE.FindAllStringSubmatch(list, -1)
				if len(items) == 0 {
					t.Fatalf("no items:\n%s", list)
				}
				for _, it := range items {
					status, link := it[1], it[2]
					code, detail, _ := hs.get(link, true)
					if code != 200 || strings.Contains(detail, views.CopyNotLocal) || !strings.Contains(detail, `class="m `+status+` now"`) {
						t.Fatalf("%s (list status %s): %d\n%s", link, status, code, detail)
					}
				}
			})
		}
	}
}
