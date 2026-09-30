package web

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/web/views"
)

func twoSlots(d *Deps) { d.DBSlots = 2 }

// getFrom is hs.get from another client address, safe to call from a goroutine.
func getFrom(hs *harness, path, ip string) int {
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = ip + ":1"
	r.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, r)
	return rec.Code
}

// §11.4: with every public DB slot taken, a cache miss is shed at once with
// the retry panel, 503 and Retry-After, while cache hits keep being served.
func TestDBGateShedsWhenFull(t *testing.T) {
	hs := newHarness(t, twoSlots)
	if code, _, _ := hs.get(lisbonQ+"&date=2026-08-15", true); code != 200 {
		t.Fatal("warm-up failed")
	}

	hs.climate.hold, hs.climate.entered = make(chan struct{}), make(chan struct{}, 2)
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, q := range []string{"/season?lat=10&lng=10", "/season?lat=20&lng=20"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = getFrom(hs, q, fmt.Sprintf("192.0.2.%d", 10+i))
		}()
	}
	for range 2 { // both misses now hold a slot
		select {
		case <-hs.climate.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("blocked misses never reached the database")
		}
	}

	start := time.Now()
	code, body, hdr := hs.get("/season?lat=30&lng=30&date=2026-08-15&x=junk", true)
	if took := time.Since(start); took > 400*time.Millisecond {
		t.Fatalf("shedding took %v; it must answer within the gate wait", took)
	}
	if code != 503 || hdr.Get("Retry-After") != "5" || !strings.Contains(body, views.CopyLoadError) || !strings.Contains(body, "30.00°N, 30.00°E") ||
		!strings.Contains(body, `href="/season?lat=30&lng=30&date=2026-08-15"`) || strings.Contains(body, "junk") {
		t.Fatalf("shed miss: %d Retry-After=%q\n%s", code, hdr.Get("Retry-After"), body)
	}

	// A warmed cell is still served. Its label needs the gate too, so it
	// falls back to coordinates instead of failing the request.
	code, body, _ = hs.get(lisbonQ+"&date=2026-08-15", true)
	if code != 200 || !strings.Contains(body, "Tomato") || !strings.Contains(body, "38.72°N, 9.14°W") {
		t.Fatalf("cache hit while the gate is full: %d\n%s", code, body)
	}

	if code, body, _ := hs.get("/places?q=lis", true); code != 503 || !strings.Contains(body, views.CopySearchUnavailable) {
		t.Fatalf("/places while the gate is full: %d\n%s", code, body)
	}
	cell30 := climate.CellID(30, 30)
	if code, body, _ := hs.get(fmt.Sprintf("/ingredient/tomato?cell=%d&x=junk", cell30), true); code != 503 || !strings.Contains(body, views.CopyLoadError) ||
		!strings.Contains(body, fmt.Sprintf(`href="/ingredient/tomato?cell=%d"`, cell30)) || strings.Contains(body, "junk") {
		t.Fatalf("/ingredient miss while the gate is full: %d\n%s", code, body)
	}

	close(hs.climate.hold)
	wg.Wait()
	if codes[0] != 200 || codes[1] != 200 {
		t.Fatalf("the requests holding slots must finish normally: %v", codes)
	}
	hs.climate.hold, hs.climate.entered = nil, nil
	if code, _, _ := hs.get("/season?lat=30&lng=30&date=2026-08-15", true); code != 200 {
		t.Fatalf("after the burst: %d", code)
	}
}

// /ingredient looks a cell's inputs up once per data version.
func TestIngredientInputsCachedPerCell(t *testing.T) {
	hs := newHarness(t)
	calls := func() (int32, int32) { return hs.climate.calls.Load(), hs.ocean.calls.Load() }
	tomato := fmt.Sprintf("/ingredient/tomato?cell=%d", lisbonCell)
	for _, p := range []string{tomato, tomato, fmt.Sprintf("/ingredient/basil?cell=%d", lisbonCell)} {
		if code, _, _ := hs.get(p, true); code != 200 {
			t.Fatalf("%s: %d", p, code)
		}
	}
	if c, o := calls(); c != 1 || o != 1 {
		t.Fatalf("one cell and version: Normals %d, Nearby %d calls; want 1 each", c, o)
	}

	hs.version = "v2"
	if code, _, _ := hs.get(tomato, true); code != 200 {
		t.Fatal(code)
	}
	if c, o := calls(); c != 2 || o != 2 {
		t.Fatalf("new version: Normals %d, Nearby %d calls; want 2 each", c, o)
	}

	hs.version = "v3"
	hs.ocean.down = true
	if code, _, _ := hs.get(tomato, true); code != 503 {
		t.Fatalf("outage: %d", code)
	}
	hs.ocean.down = false
	if code, _, _ := hs.get(tomato, true); code != 200 {
		t.Fatalf("after outage: %d", code)
	}
	if c, o := calls(); c != 4 || o != 4 {
		t.Fatalf("a failed lookup must not be cached: Normals %d, Nearby %d calls; want 4 each", c, o)
	}

	south := fmt.Sprintf("/ingredient/tomato?cell=%d", climate.CellID(-60, 20)) // no normals there
	for range 2 {
		if code, body, _ := hs.get(south, true); code != 200 || !strings.Contains(body, "Not grown or landed near here") {
			t.Fatalf("cell without normals: %d\n%s", code, body)
		}
	}
	if c, _ := calls(); c != 5 {
		t.Fatalf("a cell with no normals must be cached too: Normals %d calls; want 5", c)
	}
}
