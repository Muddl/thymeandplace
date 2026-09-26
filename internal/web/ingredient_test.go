package web

import (
	"fmt"
	"strings"
	"testing"

	"thymeandplace/internal/climate"
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
