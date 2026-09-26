package web

import (
	"errors"
	"strings"
	"testing"

	"thymeandplace/internal/web/views"
)

func TestHomeAndAbout(t *testing.T) {
	hs := newHarness(t)
	code, body, _ := hs.get("/", false)
	if code != 200 || !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, views.CopyGeoOff) ||
		!strings.Contains(body, `hx-trigger="keyup changed delay:250ms, search"`) || !strings.Contains(body, "Use my location") {
		t.Fatalf("home: %d\n%s", code, body)
	}
	code, body, _ = hs.get("/about/data", false)
	for _, want := range []string{"TerraClimate", "CC0", "GeoNames", "CC BY 4.0", "Marine Ecoregions of the World", "do not store the locations", "How estimates work"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("about missing %q", want)
		}
	}
}

func TestHealthAndReady(t *testing.T) {
	hs := newHarness(t)
	if code, _, _ := hs.get("/healthz", false); code != 200 {
		t.Fatal("healthz")
	}
	if code, _, _ := hs.get("/readyz", false); code != 200 {
		t.Fatal("readyz should be ready")
	}
	hs.pingErr = errors.New("down")
	if code, _, _ := hs.get("/readyz", false); code != 503 {
		t.Fatal("readyz must fail when the DB ping fails")
	}
	hs.pingErr = nil
	hs.snap = nil
	if code, _, _ := hs.get("/readyz", false); code != 503 {
		t.Fatal("readyz must fail without a snapshot")
	}
}

func TestStaticAndNotFound(t *testing.T) {
	hs := newHarness(t)
	code, _, hdr := hs.get("/static/app.js", false)
	if code != 200 || !strings.Contains(hdr.Get("Cache-Control"), "max-age=86400") {
		t.Fatalf("static: %d %v", code, hdr)
	}
	if code, body, _ := hs.get("/nope", false); code != 404 || !strings.Contains(body, views.CopyNotFound) {
		t.Fatalf("404: %d", code)
	}
}
