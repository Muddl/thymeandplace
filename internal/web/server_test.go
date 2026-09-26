package web

import (
	"errors"
	"regexp"
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

func TestHistoryRestoreGetsFullPage(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/places?q=lis", true, "HX-History-Restore-Request", "true")
	if !strings.Contains(body, "<!doctype html>") {
		t.Fatal("history restore must return a full page")
	}
}

// Final review 8: the static handler serves files, never directory listings.
func TestStaticNoDirectoryListing(t *testing.T) {
	hs := newHarness(t)
	for _, p := range []string{"/static/", "/static/nope.js"} {
		if code, body, _ := hs.get(p, false); code != 404 || !strings.Contains(body, views.CopyNotFound) {
			t.Fatalf("%s: %d\n%s", p, code, body)
		}
	}
}

// Final review 8: asset URLs carry a content hash so a deploy busts caches.
func TestAssetURLsAreVersioned(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/", false)
	m := regexp.MustCompile(`/static/app\.js\?v=([0-9a-f]{10})"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("app.js not versioned:\n%s", body)
	}
	for _, f := range []string{"app.css", "htmx.min.js"} {
		if !strings.Contains(body, "/static/"+f+"?v="+m[1]+`"`) {
			t.Fatalf("%s not versioned with %s", f, m[1])
		}
	}
	if code, _, _ := hs.get("/static/app.js?v="+m[1], false); code != 200 {
		t.Fatalf("versioned asset: %d", code)
	}
}

// Final review 9: htmx must not keep pages (with place labels) in localStorage.
func TestHTMXHistoryCacheDisabled(t *testing.T) {
	hs := newHarness(t)
	if _, body, _ := hs.get("/", false); !strings.Contains(body, `"historyCacheSize":0`) {
		t.Fatalf("htmx config:\n%s", body)
	}
}
