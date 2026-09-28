package web

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/web/views"
)

// seafoodWords are the terms that must not reach a visitor while the seafood
// feature is off. ("sea" alone would match "season".)
var seafoodWords = regexp.MustCompile(`(?i)seafood|fish|marine|ocean|coast|ecoregion|mackerel|atlantic cod|landed`)

// withSeafoodPairing gives tomato a pairing to mackerel, like the launch
// catalog's dill → atlantic-cod.
func withSeafoodPairing(t *testing.T, hs *harness) {
	t.Helper()
	items := catalogtest.SmallCatalog()
	for i := range items {
		if items[i].Slug == "tomato" {
			items[i].Pairings = append(items[i].Pairings, "mackerel")
		}
	}
	snap, err := catalog.NewSnapshot(1, items)
	if err != nil {
		t.Fatal(err)
	}
	hs.snap = snap
}

// With seafood off (the launch setup), no public page mentions seafood, no
// seafood item or pairing is reachable, and no ocean lookup runs.
func TestSeafoodOffIsLandOnly(t *testing.T) {
	hs := newHarness(t, seafoodOff, discovery)
	withSeafoodPairing(t, hs)
	southern := climate.CellID(-60, -20) // no land normals, but ecoregions in reach (west of 0°)
	pages := []string{
		"/", "/about/data",
		lisbonQ + "&date=2026-08-15", // coastal: seafood would show here
		"/season?lat=-60&lng=-20",    // water only: supported with seafood, not without
		fmt.Sprintf("/ingredient/tomato?cell=%d", lisbonCell),
		fmt.Sprintf("/ingredient/tomato?cell=%d", southern),
		"/places?q=lis",
		"/llms.txt", "/sitemap.xml", "/robots.txt",
	}
	for _, p := range pages {
		for _, hx := range []bool{false, true} {
			code, body, _ := hs.get(p, hx)
			if code >= 500 {
				t.Fatalf("%s: %d", p, code)
			}
			if m := seafoodWords.FindString(body); m != "" {
				t.Errorf("%s (htmx=%v) mentions %q with seafood off:\n%s", p, hx, m, body)
			}
		}
	}
	if code, body, _ := hs.get("/season?lat=-60&lng=-20", true); !strings.Contains(body, views.CopyUnsupportedLand) {
		t.Errorf("water-only point with seafood off: %d, want the land-only panel\n%s", code, body)
	}
	if n := hs.ocean.calls.Load(); n != 0 {
		t.Errorf("seafood off made %d ocean lookups, want 0", n)
	}
}

// With seafood off, seafood ingredient pages are not found, even by direct link.
func TestSeafoodOffHidesSeafoodPages(t *testing.T) {
	hs := newHarness(t, seafoodOff)
	for _, slug := range []string{"mackerel", "atlantic-cod"} {
		for _, hx := range []bool{false, true} {
			code, body, _ := hs.get(fmt.Sprintf("/ingredient/%s?cell=%d", slug, lisbonCell), hx)
			if code != 404 {
				t.Errorf("/ingredient/%s (htmx=%v) with seafood off: %d, want 404", slug, hx, code)
			}
			if m := seafoodWords.FindString(body); m != "" {
				t.Errorf("/ingredient/%s 404 page mentions %q", slug, m)
			}
		}
	}
	if n := hs.ocean.calls.Load(); n != 0 {
		t.Errorf("seafood off made %d ocean lookups, want 0", n)
	}
}

// The same pages with seafood on show it, so the flag is what hides it.
func TestSeafoodOnShowsSeafood(t *testing.T) {
	hs := newHarness(t)
	withSeafoodPairing(t, hs)
	checks := []struct{ path, want string }{
		{lisbonQ + "&date=2026-08-15", "Seafood"},
		{fmt.Sprintf("/ingredient/tomato?cell=%d", lisbonCell), "Mackerel"},
		{"/about/data", "Marine Ecoregions of the World"},
		{"/season?lat=-60&lng=-20", "Seafood"},
	}
	for _, c := range checks {
		if code, body, _ := hs.get(c.path, true); code != 200 || !strings.Contains(body, c.want) {
			t.Errorf("%s with seafood on: %d, want %q\n%s", c.path, code, c.want, body)
		}
	}
	if code, _, _ := hs.get(fmt.Sprintf("/ingredient/mackerel?cell=%d", lisbonCell), true); code != 200 {
		t.Errorf("/ingredient/mackerel with seafood on: %d", code)
	}
}
