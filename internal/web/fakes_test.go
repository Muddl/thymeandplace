package web

import (
	"context"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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
// Its fields are set before requests run; calls is safe for concurrent requests.
type fakeClimate struct {
	down    bool
	panic   bool
	block   bool          // hang until the request context ends
	hold    chan struct{} // when set, wait until it is closed (or the context ends)
	entered chan struct{} // when set, receives once per call that starts waiting on hold
	calls   atomic.Int32
}

func (f *fakeClimate) Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error) {
	f.calls.Add(1)
	if f.panic {
		panic("climate exploded")
	}
	if f.block {
		<-ctx.Done()
		return climate.Lookup{}, ctx.Err()
	}
	if f.hold != nil {
		if f.entered != nil {
			f.entered <- struct{}{}
		}
		select {
		case <-f.hold:
		case <-ctx.Done():
			return climate.Lookup{}, ctx.Err()
		}
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
type fakeOcean struct {
	down  bool
	calls atomic.Int32
}

func (f *fakeOcean) Nearby(_ context.Context, _, lng float64) ([]ocean.EcoregionID, error) {
	f.calls.Add(1)
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

	pings       atomic.Int32
	pingHold    chan struct{} // when set, Ping waits until it is closed, ignoring its deadline
	pingEntered chan struct{} // when set, receives once per Ping that starts waiting on pingHold
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
		Ping:        hs.ping,
		Now:         func() time.Time { return hs.now },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		// Most tests cover the full site; seafoodOff gives the launch setup.
		Seafood: true,
	}
	for _, o := range opts {
		o(&d)
	}
	hs.h = New(d)
	return hs
}

// ping is the fake Deps.Ping: it counts calls, fails at once on a done
// context (like pgx), and when pingHold is set waits for it to close. The
// wait ignores ctx, so a slow host can't turn sharedPing's real 500 ms
// timeout into a test failure; the test always closes pingHold.
func (hs *harness) ping(ctx context.Context) error {
	hs.pings.Add(1)
	if err := ctx.Err(); err != nil {
		return err
	}
	if hs.pingHold != nil {
		if hs.pingEntered != nil {
			select { // never block: without sharing, 50 pings would arrive here
			case hs.pingEntered <- struct{}{}:
			default:
			}
		}
		<-hs.pingHold
	}
	return hs.pingErr
}

// seafoodOff turns the seafood feature off, as at launch (FEATURE_SEAFOOD unset).
func seafoodOff(d *Deps) { d.Seafood = false }

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
