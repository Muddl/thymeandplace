package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dataversion"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, html.UnescapeString(string(b))
}

func TestAppServesAndReloadsWithoutRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	tx, _ := pool.Begin(ctx)
	if err := catalog.Write(ctx, store.New(tx), catalogtest.SmallCatalog(), nil); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)

	// The server ignores a date more than a day from its own UTC date, so pin
	// its clock to the date the season URL below asks for.
	cfg := config{DatabaseURL: pool.Config().ConnString(), Addr: ":0",
		now: func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) }}
	a, err := newApp(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	a.start(ctx)
	srv := httptest.NewServer(a.handler)
	defer srv.Close()

	if code, _ := get(t, srv.URL+"/readyz"); code != 200 {
		t.Fatalf("readyz = %d", code)
	}
	season := srv.URL + "/season?lat=38.7167&lng=-9.1333&date=2026-10-15"
	code, body := get(t, season)
	for _, want := range []string{"near Lisbon, PT", "Mackerel", "Banana"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("missing %q (%d)\n%s", want, code, body)
		}
	}

	if _, err := pool.Exec(ctx, `UPDATE ingredient SET name = 'Plantain' WHERE slug = 'banana'`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, body := get(t, season); strings.Contains(body, "Plantain") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("catalog change not reflected without restart")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestAdminMountedWithCatalogDeps builds a real app with ADMIN_SECRET_KEY
// set, the condition under which newApp wires admin.Deps.Changes, .Load and
// .Pool (previously left zero, GET /admin 500s on any request that reaches
// a.loadSnap). It mints a session directly through auth.Service.CreateSession
// rather than running a full WebAuthn ceremony, then confirms GET /admin
// renders the catalog instead of the 500 the missing deps would cause.
func TestAdminMountedWithCatalogDeps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	tx, _ := pool.Begin(ctx)
	if err := catalog.Write(ctx, store.New(tx), catalogtest.SmallCatalog(), nil); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)

	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	cfg, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": pool.Config().ConnString(), "ADMIN_SECRET_KEY": key}[k]
	})
	if err != nil {
		t.Fatal(err)
	}

	a, err := newApp(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	if a.auth == nil {
		t.Fatal("admin auth service was not wired even though ADMIN_SECRET_KEY was set")
	}

	raw, _, err := a.auth.CreateSession(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.handler)
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/admin", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: raw})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Catalog") {
		t.Fatalf("GET /admin = %d (want 200 with the catalog page; a nil Changes/Load/Pool dep would 500)\n%s", resp.StatusCode, body)
	}
}

// Review Focus 1: an empty database still gives a working site.
func TestEmptyDatabaseServes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)
	a, err := newApp(ctx, config{DatabaseURL: pool.Config().ConnString()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	if err := a.waitForSnapshot(ctx, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.handler)
	defer srv.Close()
	// A plain (non-HTMX) GET / is a full-page load: render() sends the layout,
	// which is where "Find your town" lives. get() always sets HX-Request,
	// under which GET / has no fragment of its own (see server.go's render:
	// "an HTMX request for a page with no fragment"), so it would see an
	// empty body here.
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	homeBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(homeBody), "Find your town") {
		t.Fatalf("home: %d\n%s", resp.StatusCode, homeBody)
	}
	code, body := get(t, srv.URL+"/season?lat=38.7&lng=-9.1")
	if code != 200 || !strings.Contains(body, "We can't estimate seasons here yet.") {
		t.Fatalf("season on empty DB: %d\n%s", code, body)
	}
}

// Review Focus 4: waitForSnapshot keeps retrying until a load succeeds.
//
// waitForSnapshot loops on the data-version watcher's Refresh (not
// holder.Reload directly): Refresh's onChange reloads the catalog before
// publishing any version, including the first, so a real *dataversion.Watcher
// is needed here rather than a bare holder. That in turn needs a real pool
// (Refresh reads the current data version from the database), so this uses
// dbtest like the other app-level tests; the loader injected into the holder
// is what actually simulates the failures.
func TestWaitForSnapshotRetries(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := dbtest.New(t)

	calls := 0
	a := &app{log: log, holder: catalog.NewHolder(func(context.Context) (*catalog.Snapshot, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("db not ready")
		}
		return catalog.NewSnapshot(1, nil)
	}, log)}
	a.watcher = dataversion.NewWatcher(pool, time.Hour, log, func(ctx context.Context, _ string) error {
		return a.holder.Reload(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.waitForSnapshot(ctx, 5*time.Millisecond); err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}

	never := &app{log: log, holder: catalog.NewHolder(func(context.Context) (*catalog.Snapshot, error) {
		return nil, errors.New("down")
	}, log)}
	never.watcher = dataversion.NewWatcher(pool, time.Hour, log, func(ctx context.Context, _ string) error {
		return never.holder.Reload(ctx)
	})
	short, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	if err := never.waitForSnapshot(short, 5*time.Millisecond); err == nil {
		t.Fatal("must give up when the context ends")
	}
}

func TestLoadConfig(t *testing.T) {
	if _, err := loadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("DATABASE_URL is required")
	}
	c, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://x", "TRUST_PROXY_HEADERS": "true"}[k]
	})
	if err != nil || c.Addr != ":8080" || !c.TrustProxy {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://x", "ADMIN_SECRET_KEY": "c2hvcnQ="}[k]
	}); err == nil {
		t.Fatal("a short ADMIN_SECRET_KEY must be rejected")
	}

	// A trailing "/" on ADMIN_ORIGIN (or the https://<rpid> fallback) would
	// never match the browser's Origin header, which has no path.
	for _, tc := range []struct{ rpID, origin, want string }{
		{"", "", "https://thymeand.place"},
		{"", "https://admin.example.org/", "https://admin.example.org"},
		{"", "https://admin.example.org//", "https://admin.example.org"},
		{"example.org/", "", "https://example.org"},
	} {
		c, err := loadConfig(func(k string) string {
			return map[string]string{"DATABASE_URL": "postgres://x", "ADMIN_RP_ID": tc.rpID, "ADMIN_ORIGIN": tc.origin}[k]
		})
		if err != nil || c.AdminOrigin != tc.want {
			t.Fatalf("rp=%q origin=%q: AdminOrigin=%q want %q (%v)", tc.rpID, tc.origin, c.AdminOrigin, tc.want, err)
		}
	}
}
