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
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dataversion"
	"thymeandplace/internal/db"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/jobs"
	"thymeandplace/internal/monitor"
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
	// its clock to the date the season URL below asks for. It connects as the
	// least-privilege runtime role, as in production. Seafood is on so the
	// ocean lookups run against the real reference data too.
	cfg := config{DatabaseURL: dbtest.URLAs(pool, dbtest.App), Addr: ":0", Seafood: true,
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
	// The sitemap links a season page for each calibration city (get
	// unescapes the body, so the XML's &amp; reads as &).
	if code, body := get(t, srv.URL+"/sitemap.xml"); code != 200 || !strings.Contains(body, "<loc>https://thymeand.place/season?lat=51.5074&lng=-0.1278</loc>") {
		t.Fatalf("sitemap.xml (%d) has no London season page:\n%s", code, body)
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
		return map[string]string{"DATABASE_URL": dbtest.URLAs(pool, dbtest.App), "ADMIN_SECRET_KEY": key}[k]
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

// With admin enabled, start() prunes expired admin rows right away, and
// cancelling the context stops the loop.
func TestStartPrunesAdminTables(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)
	if _, err := pool.Exec(ctx, `INSERT INTO admin_session (token_hash, csrf_token, method, created_at, last_seen_at, auth_at)
		VALUES ('\x0102', 'c', 'passkey', now() - interval '13 hours', now() - interval '13 hours', now() - interval '13 hours')`); err != nil {
		t.Fatal(err)
	}
	var logBuf syncBuffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	cfg, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": dbtest.URLAs(pool, dbtest.App), "ADMIN_SECRET_KEY": key}[k]
	}, log)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newApp(ctx, cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	a.start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM admin_session`).Scan(&n)
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expired session not pruned at start (rows=%d):\n%s", n, logBuf.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(logBuf.String(), "admin prune failed") {
		t.Fatalf("prune logged a failure:\n%s", logBuf.String())
	}

	done := make(chan struct{})
	stop, cancelLoop := context.WithCancel(ctx)
	hourly := jobs.Schedule{Every: time.Hour, RetryMin: time.Hour, RetryMax: time.Hour}
	go func() { a.pruneLoop(stop, hourly); close(done) }()
	cancelLoop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pruneLoop did not stop when its context was cancelled")
	}
}

// A failed prune is logged and retried with backoff, not left for a day.
func TestPruneLoopRetriesFailures(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://tp@127.0.0.1:1/tp")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close() // every call now fails at once
	var logBuf syncBuffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	svc, err := auth.NewService(pool, auth.Config{
		RPID: "localhost", RPOrigins: []string{"http://localhost"}, SecretKey: bytes.Repeat([]byte{7}, 32), Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := &app{auth: svc, log: log}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	fast := jobs.Schedule{Every: time.Hour, RetryMin: time.Millisecond, RetryMax: 5 * time.Millisecond}
	go func() { a.pruneLoop(ctx, fast); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(logBuf.String(), "admin prune failed") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("prune failure not retried:\n%s", logBuf.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pruneLoop did not stop when its context was cancelled")
	}
}

// The app role can't migrate, so the app refuses a database that is behind.
func TestNewAppRefusesPendingMigrations(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = (SELECT max(version_id) FROM goose_db_version)`); err != nil {
		t.Fatal(err)
	}
	_, err := newApp(ctx, config{DatabaseURL: dbtest.URLAs(pool, dbtest.App)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, db.ErrPendingMigrations) {
		t.Fatalf("newApp with a pending migration: %v, want ErrPendingMigrations", err)
	}
}

func TestMigrateOnly(t *testing.T) {
	ctx := context.Background()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := dbtest.New(t)
	owner := dbtest.URLAs(pool, dbtest.Owner)
	if code := runMigrate(ctx, func(k string) string { return map[string]string{"DATABASE_URL": owner}[k] }, discard); code != 0 {
		t.Fatalf("-migrate-only on a migrated database: exit %d", code)
	}
	if code := runMigrate(ctx, func(string) string { return "" }, discard); code != 1 {
		t.Fatalf("-migrate-only without DATABASE_URL: exit %d", code)
	}
}

// syncBuffer is a bytes.Buffer safe for the app's concurrent log writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
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
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
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
	// Each failed retry is logged once, with its reason.
	if n := strings.Count(logBuf.String(), "catalog snapshot not loaded yet"); n != 2 || !strings.Contains(logBuf.String(), "db not ready") {
		t.Fatalf("want 2 retry log lines carrying the load error, got %d:\n%s", n, logBuf.String())
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
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := loadConfig(func(string) string { return "" }, discard); err == nil {
		t.Fatal("DATABASE_URL is required")
	}
	c, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://x", "TRUST_PROXY_HEADERS": "true"}[k]
	}, discard)
	if err != nil || c.Addr != ":8080" || !c.TrustProxy {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://x", "ADMIN_SECRET_KEY": "c2hvcnQ="}[k]
	}, discard); err == nil {
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
		}, discard)
		if err != nil || c.AdminOrigin != tc.want {
			t.Fatalf("rp=%q origin=%q: AdminOrigin=%q want %q (%v)", tc.rpID, tc.origin, c.AdminOrigin, tc.want, err)
		}
	}
}

func TestLoadConfigDBMaxConns(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		v    string
		want int32
		ok   bool
	}{{"", 0, true}, {"10", 10, true}, {"1", 0, false}, {"lots", 0, false}, {"-4", 0, false}} {
		c, err := loadConfig(func(k string) string {
			return map[string]string{"DATABASE_URL": "postgres://x", "DB_MAX_CONNS": tc.v}[k]
		}, discard)
		if (err == nil) != tc.ok || c.DBMaxConns != tc.want {
			t.Fatalf("DB_MAX_CONNS=%q: got %d, err %v", tc.v, c.DBMaxConns, err)
		}
	}
	if publicDBSlots(16) != 12 || publicDBSlots(10) != 7 || publicDBSlots(1) != 1 {
		t.Fatalf("public DB slots: 16→%d 10→%d 1→%d", publicDBSlots(16), publicDBSlots(10), publicDBSlots(1))
	}
}

// FEATURE_SEAFOOD is off unless set to "on"; any other value is a startup
// error, so a typo can't quietly turn seafood on or off.
func TestLoadConfigSeafood(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		v      string
		want   bool
		wantOK bool
	}{{"", false, true}, {"off", false, true}, {"on", true, true}, {"true", false, false}, {"ON", false, false}, {"1", false, false}} {
		c, err := loadConfig(func(k string) string {
			return map[string]string{"DATABASE_URL": "postgres://x", "FEATURE_SEAFOOD": tc.v}[k]
		}, discard)
		if (err == nil) != tc.wantOK || c.Seafood != tc.want {
			t.Fatalf("FEATURE_SEAFOOD=%q: got %v, err %v", tc.v, c.Seafood, err)
		}
	}
}

// PUBLIC_BASE_URL defaults to the admin origin (as the admin CLI's enroll
// links do) and must be a bare origin.
func TestLoadConfigBaseURL(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		env  map[string]string
		want string
		ok   bool
	}{
		{map[string]string{}, "https://thymeand.place", true},
		{map[string]string{"ADMIN_RP_ID": "localhost"}, "https://localhost", true},
		{map[string]string{"PUBLIC_BASE_URL": "https://example.test/"}, "https://example.test", true},
		{map[string]string{"PUBLIC_BASE_URL": "http://localhost:8080"}, "http://localhost:8080", true},
		{map[string]string{"PUBLIC_BASE_URL": "thymeand.place"}, "", false},
		{map[string]string{"PUBLIC_BASE_URL": "https://example.test/site"}, "", false},
		{map[string]string{"PUBLIC_BASE_URL": "ftp://example.test"}, "", false},
	} {
		c, err := loadConfig(func(k string) string {
			if k == "DATABASE_URL" {
				return "postgres://x"
			}
			return tc.env[k]
		}, discard)
		if (err == nil) != tc.ok || (tc.ok && c.BaseURL != tc.want) {
			t.Fatalf("%v: got %q, err %v", tc.env, c.BaseURL, err)
		}
	}
}

// TestLoadConfigPublicToday covers Ruling F: TP_PUBLIC_TODAY pins config.now
// (which feeds web.Deps.Now, the public site's clock) to a fixed calendar
// date while keeping the real time of day, an invalid value is a startup
// error, and leaving it unset leaves config.now nil (time.Now, the
// production default).
func TestLoadConfigPublicToday(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	c, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://x"}[k]
	}, discard)
	if err != nil || c.now != nil {
		t.Fatalf("no TP_PUBLIC_TODAY: now should stay nil (err=%v, now set=%v)", err, c.now != nil)
	}

	c, err = loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://x", "ADMIN_RP_ID": "localhost", "TP_PUBLIC_TODAY": "2026-05-15"}[k]
	}, discard)
	if err != nil {
		t.Fatalf("valid TP_PUBLIC_TODAY: %v", err)
	}
	if c.now == nil {
		t.Fatal("TP_PUBLIC_TODAY set but config.now is still nil")
	}
	got := c.now()
	if y, m, d := got.Date(); y != 2026 || m != time.May || d != 15 {
		t.Fatalf("now() = %v, want 2026-05-15 (with the real time of day)", got)
	}
	if got.Location() != time.UTC {
		t.Fatalf("now() must be UTC, got %v", got.Location())
	}
	// The time of day tracks the real clock (not frozen at midnight): it
	// must be within a minute of time.Now().UTC()'s time of day.
	nowUTC := time.Now().UTC()
	real := time.Duration(nowUTC.Hour())*time.Hour + time.Duration(nowUTC.Minute())*time.Minute
	pinned := time.Duration(got.Hour())*time.Hour + time.Duration(got.Minute())*time.Minute
	if diff := real - pinned; diff < -time.Minute || diff > time.Minute {
		t.Fatalf("now() time-of-day = %v, want close to real %v", pinned, real)
	}

	if _, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://x", "ADMIN_RP_ID": "localhost", "TP_PUBLIC_TODAY": "not-a-date"}[k]
	}, discard); err == nil {
		t.Fatal("an invalid TP_PUBLIC_TODAY must be a startup error")
	}
}

// TestLoadConfigPublicTodayRefusedOutsideLocalhost: TP_PUBLIC_TODAY is a
// test-only setting, so the server refuses to start with it unless the admin
// relying party is "localhost" (the e2e stack's value). A production env file
// (ADMIN_RP_ID=thymeand.place, or unset, which defaults to it) that leaked the
// setting must fail loudly rather than serve a pinned month.
func TestLoadConfigPublicTodayRefusedOutsideLocalhost(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, rp := range []string{"", "thymeand.place", "example.org", "localhost.example.org"} {
		_, err := loadConfig(func(k string) string {
			return map[string]string{"DATABASE_URL": "postgres://x", "ADMIN_RP_ID": rp, "TP_PUBLIC_TODAY": "2026-05-15"}[k]
		}, discard)
		if err == nil || !strings.Contains(err.Error(), "TP_PUBLIC_TODAY") {
			t.Fatalf("ADMIN_RP_ID=%q with TP_PUBLIC_TODAY: want a startup error naming TP_PUBLIC_TODAY, got %v", rp, err)
		}
	}
}

func TestLoadConfigHeartbeats(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "postgres://x/y", "HEARTBEAT_APP_URL": "https://hc-ping.com/a",
		"HEARTBEAT_ADMIN_AUTH_URL": "https://hc-ping.com/b", "HEARTBEAT_DRIFT_URL": "https://hc-ping.com/c"}
	c, err := loadConfig(func(k string) string { return env[k] }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || c.HeartbeatApp != env["HEARTBEAT_APP_URL"] || c.HeartbeatAdminAuth != env["HEARTBEAT_ADMIN_AUTH_URL"] || c.HeartbeatDrift != env["HEARTBEAT_DRIFT_URL"] {
		t.Fatalf("%+v %v", c, err)
	}
}

// pingReceiver records each heartbeat ping as "<path> <body>".
func pingReceiver(t *testing.T) (*httptest.Server, <-chan string) {
	t.Helper()
	got := make(chan string, 64)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		select {
		case got <- r.URL.Path + " " + string(b):
		default: // never block a handler, or recv.Close would wait on it
		}
	}))
	t.Cleanup(recv.Close)
	return recv, got
}

// TestAppSendsHeartbeat: with HEARTBEAT_APP_URL set, the running app pings
// it with a success report that counts the requests served through
// a.handler (so the CountResponses wrap is wired); the checks whose
// variables are unset stay off (spec §6).
func TestAppSendsHeartbeat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)
	recv, got := pingReceiver(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": dbtest.URLAs(pool, dbtest.App), "HEARTBEAT_APP_URL": recv.URL + "/app-check"}[k]
	}, log)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HeartbeatAdminAuth != "" || cfg.HeartbeatDrift != "" {
		t.Fatalf("unset heartbeat variables must stay empty: %+v", cfg)
	}
	cfg.watchdogSample = 20 * time.Millisecond
	a, err := newApp(ctx, cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	if p, ok := a.watchdog.AdminAuth.(*monitor.Pinger); !ok || p.Enabled() {
		t.Fatalf("admin-auth check must be off with HEARTBEAT_ADMIN_AUTH_URL unset: %#v", a.watchdog.AdminAuth)
	}
	// Ready before the first sample, so the first report is a success.
	if err := a.waitForSnapshot(ctx, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, httptest.NewRequest("GET", "/robots.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /robots.txt: %d", rec.Code)
	}
	a.start(ctx)
	select {
	case p := <-got:
		if want := "/app-check ok requests=1 5xx=0"; p != want {
			t.Fatalf("first ping %q, want %q", p, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no app heartbeat")
	}
}

// TestAppWiresMonitoringHooks: the one-line wires in newApp reach their
// checks. A backup login (auth.Config.OnEvent → Watchdog.Event) fails
// admin-auth with its count, and the drift check (Drift.Notify) pings drift.
func TestAppWiresMonitoringHooks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)
	recv, got := pingReceiver(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	cfg, err := loadConfig(func(k string) string {
		return map[string]string{
			"DATABASE_URL": dbtest.URLAs(pool, dbtest.App), "ADMIN_SECRET_KEY": key,
			"HEARTBEAT_APP_URL": recv.URL + "/app-check", "HEARTBEAT_ADMIN_AUTH_URL": recv.URL + "/auth-check",
			"HEARTBEAT_DRIFT_URL": recv.URL + "/drift-check",
		}[k]
	}, log)
	if err != nil {
		t.Fatal(err)
	}
	cfg.watchdogSample = 20 * time.Millisecond
	a, err := newApp(ctx, cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	if a.auth == nil {
		t.Fatal("admin auth service not wired with ADMIN_SECRET_KEY set")
	}
	if err := a.waitForSnapshot(ctx, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	a.auth.LogAttempt(ctx, auth.Caller{IP: "192.0.2.10", Key: "192.0.2.10"}, "backup", "success")
	a.start(ctx)
	const wantAuth = "/auth-check/fail backup login used: 1; backup lockout: 0; login log growth: 0"
	var sawAuth, sawDrift bool
	deadline := time.After(20 * time.Second)
	for !sawAuth || !sawDrift {
		select {
		case p := <-got:
			switch {
			case strings.HasPrefix(p, "/auth-check"):
				if !sawAuth && p != wantAuth {
					t.Fatalf("first admin-auth ping %q, want %q", p, wantAuth)
				}
				sawAuth = true
			case strings.HasPrefix(p, "/drift-check"):
				sawDrift = true
			}
		case <-deadline:
			t.Fatalf("missing pings: admin-auth fail %v, drift %v", sawAuth, sawDrift)
		}
	}
}
