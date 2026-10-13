package web

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/web/views"
)

func TestHomeAndAbout(t *testing.T) {
	hs := newHarness(t)
	code, body, _ := hs.get("/", false)
	if code != 200 || !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, views.CopyGeoOff) ||
		!strings.Contains(body, `hx-trigger="keyup changed delay:250ms, search"`) || !strings.Contains(body, "Use my location") ||
		!strings.Contains(body, "autofocus") || strings.Count(body, `id="announce"`) != 1 || strings.Count(body, "aria-live") != 1 {
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
	hs.now = hs.now.Add(time.Second)
	hs.pingErr = errors.New("down")
	if code, _, _ := hs.get("/readyz", false); code != 503 {
		t.Fatal("readyz must fail when the DB ping fails")
	}
	hs.now = hs.now.Add(time.Second)
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
	// The current version names these exact bytes; an old one may not.
	if _, _, hdr := hs.get(views.Asset("app.js"), false); hdr.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("versioned static: %v", hdr)
	}
	if _, _, hdr := hs.get("/static/app.js?v=old", false); !strings.Contains(hdr.Get("Cache-Control"), "max-age=86400") {
		t.Fatalf("stale version: %v", hdr)
	}
	if code, _, hdr := hs.get("/favicon.ico", false); code != 302 || hdr.Get("Location") != views.Asset("favicon.svg") {
		t.Fatalf("favicon.ico: %d %v", code, hdr)
	}
	if code, _, hdr := hs.get(views.Asset("favicon.svg"), false); code != 200 || hdr.Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("favicon.svg: %d %v", code, hdr)
	}
	if code, body, _ := hs.get("/nope", false); code != 404 || !strings.Contains(body, views.CopyNotFound) {
		t.Fatalf("404: %d", code)
	}
}

// Every full page has a meta description (search results show it) and the icon.
func TestPagesHaveDescriptions(t *testing.T) {
	hs := newHarness(t)
	for path, want := range map[string]string{
		"/":                          views.SiteDescription(true),
		"/about/data":                views.AboutDescription,
		lisbonQ + "&date=2026-08-15": views.SeasonDescription("Lisbon, PT", "August"),
		fmt.Sprintf("/ingredient/tomato?cell=%d", lisbonCell): views.IngredientDescription("Tomato"),
		"/nope": views.SiteDescription(true),
	} {
		_, body, _ := hs.get(path, false)
		if !strings.Contains(body, `<meta name="description" content="`+want+`">`) {
			t.Errorf("%s: no meta description %q", path, want)
		}
		if !strings.Contains(body, `<link rel="icon" type="image/svg+xml" href="`+views.Asset("favicon.svg")+`">`) {
			t.Errorf("%s: no icon link", path)
		}
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

// #13: one ping answers every /readyz for a second, however many callers.
func TestReadyzSharesPing(t *testing.T) {
	hs := newHarness(t)
	hs.pingHold, hs.pingEntered = make(chan struct{}), make(chan struct{}, 1)
	const n = 50
	codes := make([]int, n)
	bodies := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], bodies[i], _ = hs.get("/readyz", false)
		}()
	}
	select {
	case <-hs.pingEntered: // the first request is pinging; the rest queue on the lock
	case <-time.After(5 * time.Second):
		t.Fatal("no ping started")
	}
	// Give the other 49 time to reach the lock. This is best-effort: one
	// that arrives after the release still passes (the clock is frozen, so
	// it reuses the result), it just doesn't exercise waiting on the lock.
	time.Sleep(50 * time.Millisecond)
	close(hs.pingHold)
	wg.Wait()
	for i := range n {
		if codes[i] != 200 || bodies[i] != "ready" {
			t.Fatalf("request %d: %d %q", i, codes[i], bodies[i])
		}
	}
	if p := hs.pings.Load(); p != 1 {
		t.Fatalf("pings = %d, want 1", p)
	}
	hs.pingHold, hs.pingEntered = nil, nil

	hs.now = hs.now.Add(999 * time.Millisecond)
	hs.get("/readyz", false)
	if p := hs.pings.Load(); p != 1 {
		t.Fatalf("within the window: pings = %d, want 1", p)
	}
	hs.now = hs.now.Add(time.Millisecond)
	hs.get("/readyz", false)
	if p := hs.pings.Load(); p != 2 {
		t.Fatalf("after the window: pings = %d, want 2", p)
	}
}

// A failure is shared for the window too, then the next ping recovers.
func TestReadyzCachesFailure(t *testing.T) {
	hs := newHarness(t)
	hs.pingErr = errors.New("down")
	if code, body, _ := hs.get("/readyz", false); code != 503 || body != "database unavailable" {
		t.Fatalf("%d %q", code, body)
	}
	hs.pingErr = nil
	if code, _, _ := hs.get("/readyz", false); code != 503 {
		t.Fatal("the failure must be reused within the window")
	}
	hs.now = hs.now.Add(time.Second)
	if code, body, _ := hs.get("/readyz", false); code != 200 || body != "ready" {
		t.Fatalf("after the window: %d %q", code, body)
	}
}

// A client that has gone away can't poison the shared result.
func TestReadyzIgnoresCancelledCaller(t *testing.T) {
	hs := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/readyz", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, r)
	if rec.Code != 200 || rec.Body.String() != "ready" || hs.pings.Load() != 1 {
		t.Fatalf("cancelled caller: %d %q pings=%d", rec.Code, rec.Body.String(), hs.pings.Load())
	}
	if code, body, _ := hs.get("/readyz", false); code != 200 || body != "ready" || hs.pings.Load() != 1 {
		t.Fatalf("after a cancelled caller: %d %q pings=%d", code, body, hs.pings.Load())
	}
}

// A clock that moves backwards expires the cached result.
func TestReadyzClockBackwards(t *testing.T) {
	hs := newHarness(t)
	hs.get("/readyz", false)
	hs.now = hs.now.Add(-time.Hour)
	hs.get("/readyz", false)
	if p := hs.pings.Load(); p != 2 {
		t.Fatalf("pings = %d, want 2 (a backwards clock must not pin a result)", p)
	}
}

// With no snapshot the answer is immediate and there is no ping.
func TestReadyzNoSnapshotNoPing(t *testing.T) {
	hs := newHarness(t)
	hs.snap = nil
	if code, body, _ := hs.get("/readyz", false); code != 503 || body != "catalog not loaded" || hs.pings.Load() != 0 {
		t.Fatalf("%d %q pings=%d", code, body, hs.pings.Load())
	}
}

func TestCheckReady(t *testing.T) {
	ctx := context.Background()
	snap, _ := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	ok := func(context.Context) error { return nil }
	down := func(context.Context) error { return errors.New("x") }
	hang := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	none := func() *catalog.Snapshot { return nil }
	have := func() *catalog.Snapshot { return snap }
	if err := CheckReady(ctx, none, ok); !errors.Is(err, ErrCatalogNotLoaded) {
		t.Fatalf("no snapshot: %v", err)
	}
	if err := CheckReady(ctx, have, down); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("db down: %v", err)
	}
	start := time.Now()
	if err := CheckReady(ctx, have, hang); !errors.Is(err, ErrDatabaseUnavailable) || time.Since(start) > time.Second {
		t.Fatalf("hung ping: %v after %v", err, time.Since(start))
	}
	if err := CheckReady(ctx, have, ok); err != nil {
		t.Fatalf("ready: %v", err)
	}
}
