package web

import (
	"context"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/places"
)

var errDown = errors.New("database unavailable")

// fakeClimate: temperate normals north of -50°, polar north of 65°, none south of -50°.
type fakeClimate struct {
	down  bool
	panic bool
	block bool // hang until the request context ends
	calls int
}

func (f *fakeClimate) Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error) {
	f.calls++
	if f.panic {
		panic("climate exploded")
	}
	if f.block {
		<-ctx.Done()
		return climate.Lookup{}, ctx.Err()
	}
	if f.down {
		return climate.Lookup{}, errDown
	}
	cell := climate.CellID(lat, lng)
	switch {
	case lat < -50:
		return climate.Lookup{QueryCell: cell, Cell: cell}, nil
	case lat > 65:
		n := climatetest.Polar()
		return climate.Lookup{QueryCell: cell, Cell: cell, Normals: &n}, nil
	}
	n := climatetest.Temperate()
	return climate.Lookup{QueryCell: cell, Cell: cell, Normals: &n}, nil
}

func (f *fakeClimate) ForCell(ctx context.Context, id int) (*climate.MonthlyNormals, error) {
	lat, lng := climate.CellCenter(id)
	l, err := f.Normals(ctx, lat, lng)
	return l.Normals, err
}

// fakeOcean: ecoregions 25 and 30 west of the prime meridian.
type fakeOcean struct{ down bool }

func (f *fakeOcean) Nearby(_ context.Context, _, lng float64) ([]ocean.EcoregionID, error) {
	if f.down {
		return nil, errDown
	}
	if lng < 0 {
		return []ocean.EcoregionID{25, 30}, nil
	}
	return nil, nil
}

type fakePlaces struct {
	down      bool
	blockByID bool // ByID hangs until its context ends
}

var lisbon = places.Place{ID: 1, Name: "Lisbon", Admin1: "Lisbon", Country: "PT", Lat: 38.72, Lng: -9.14}

func (f *fakePlaces) Search(_ context.Context, q string) ([]places.Place, error) {
	if f.down {
		return nil, errDown
	}
	if q == "lis" {
		return []places.Place{lisbon}, nil
	}
	return nil, nil
}

func (f *fakePlaces) Nearest(_ context.Context, lat, lng float64) (places.Place, bool, error) {
	if f.down {
		return places.Place{}, false, errDown
	}
	p := lisbon
	p.DistanceKm = climate.DistanceKm(lat, lng, p.Lat, p.Lng)
	return p, true, nil
}

func (f *fakePlaces) ByID(ctx context.Context, id int32) (places.Place, bool, error) {
	if f.blockByID {
		<-ctx.Done()
		return places.Place{}, false, ctx.Err()
	}
	if f.down {
		return places.Place{}, false, errDown
	}
	return lisbon, id == 1, nil
}

type harness struct {
	h       http.Handler
	climate *fakeClimate
	ocean   *fakeOcean
	places  *fakePlaces
	snap    *catalog.Snapshot
	pingErr error
	version string
	now     time.Time // the server clock
}

// newHarness builds the public handler over fakes. The server clock defaults
// to 2026-08-15 12:00 UTC; opts adjust the Deps (e.g. a short request timeout).
func newHarness(t *testing.T, opts ...func(*Deps)) *harness {
	t.Helper()
	snap, err := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	if err != nil {
		t.Fatal(err)
	}
	hs := &harness{climate: &fakeClimate{}, ocean: &fakeOcean{}, places: &fakePlaces{}, snap: snap, version: "v1",
		now: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)}
	d := Deps{
		Climate: hs.climate, Ocean: hs.ocean, Places: hs.places,
		Snapshot:    func() *catalog.Snapshot { return hs.snap },
		DataVersion: func() string { return hs.version },
		Ping:        func(context.Context) error { return hs.pingErr },
		Now:         func() time.Time { return hs.now },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(&d)
	}
	hs.h = New(d)
	return hs
}

// shortTimeout shrinks the per-request deadline so timeout tests stay fast.
func shortTimeout(d *Deps) { d.timeout = 100 * time.Millisecond }

// get performs a request; hx adds HX-Request. It returns status and the
// HTML-unescaped body.
func (hs *harness) get(path string, hx bool, headers ...string) (int, string, http.Header) {
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = "192.0.2.1:1234"
	if hx {
		r.Header.Set("HX-Request", "true")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, r)
	return rec.Code, html.UnescapeString(rec.Body.String()), rec.Header()
}

func rawBody(t *testing.T, hs *harness, path string) string {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = "192.0.2.99:1"
	r.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, r)
	return rec.Body.String()
}
