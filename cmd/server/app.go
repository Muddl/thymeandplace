package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	calibfixtures "thymeandplace/calibration"
	"thymeandplace/internal/admin"
	"thymeandplace/internal/auth"
	"thymeandplace/internal/calibration"
	"thymeandplace/internal/calibrun"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/dataversion"
	"thymeandplace/internal/db"
	"thymeandplace/internal/jobs"
	"thymeandplace/internal/monitor"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/places"
	"thymeandplace/internal/season"
	"thymeandplace/internal/web"
)

type config struct {
	DatabaseURL string
	Addr        string
	TrustProxy  bool
	DBMaxConns  int32  // DB_MAX_CONNS; 0 → db.ServerPool's default
	Seafood     bool   // FEATURE_SEAFOOD=on; off (the default) serves a land-only site
	BaseURL     string // PUBLIC_BASE_URL, for robots.txt, sitemap.xml and llms.txt; defaults to AdminOrigin

	AdminSecretKey []byte
	AdminRPID      string
	AdminOrigin    string
	AdminBackup    bool

	// Healthchecks.io ping URLs (secrets; empty → that ping is off).
	HeartbeatApp, HeartbeatAdminAuth, HeartbeatDrift string

	watchdogSample time.Duration // 0 → 1 min (tests shorten it)

	now func() time.Time // server clock for the public site; nil → time.Now (tests pin it)
}

func loadConfig(getenv func(string) string, log *slog.Logger) (config, error) {
	c := config{DatabaseURL: getenv("DATABASE_URL"), Addr: getenv("ADDR"), TrustProxy: getenv("TRUST_PROXY_HEADERS") == "true"}
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if v := getenv("DB_MAX_CONNS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 2 {
			return c, fmt.Errorf("DB_MAX_CONNS must be a whole number of at least 2, got %q", v)
		}
		c.DBMaxConns = int32(n)
	}
	// Seafood stays off until the ocean dataset's license is confirmed
	// (docs/reference-data-licenses.md); a typo must not silently change it.
	switch v := getenv("FEATURE_SEAFOOD"); v {
	case "", "off":
	case "on":
		c.Seafood = true
	default:
		return c, fmt.Errorf(`FEATURE_SEAFOOD must be "on" or "off", got %q`, v)
	}
	if k := getenv("ADMIN_SECRET_KEY"); k != "" {
		key, err := auth.ParseSecretKey(k)
		if err != nil {
			return c, err
		}
		c.AdminSecretKey = key
	}
	rawRPID := getenv("ADMIN_RP_ID")
	c.AdminRPID, c.AdminOrigin = rawRPID, getenv("ADMIN_ORIGIN")
	if c.AdminRPID == "" {
		c.AdminRPID = "thymeand.place"
	}
	if c.AdminOrigin == "" {
		c.AdminOrigin = "https://" + c.AdminRPID
	}
	// A browser's Origin never ends in "/", so neither may the WebAuthn
	// origin (nor the enrollment links built from it).
	c.AdminOrigin = strings.TrimRight(c.AdminOrigin, "/")
	// Same default as the admin CLI's enroll links: the public site and the
	// admin share an origin unless PUBLIC_BASE_URL says otherwise.
	c.BaseURL = strings.TrimRight(getenv("PUBLIC_BASE_URL"), "/")
	if c.BaseURL == "" {
		c.BaseURL = c.AdminOrigin
	}
	if u, err := url.Parse(c.BaseURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		return c, fmt.Errorf("PUBLIC_BASE_URL must be an origin like https://thymeand.place, got %q", c.BaseURL)
	}
	c.AdminBackup = getenv("ADMIN_BACKUP_LOGIN") != "off"
	c.HeartbeatApp = getenv("HEARTBEAT_APP_URL")
	c.HeartbeatAdminAuth = getenv("HEARTBEAT_ADMIN_AUTH_URL")
	c.HeartbeatDrift = getenv("HEARTBEAT_DRIFT_URL")
	// TP_PUBLIC_TODAY pins the public site's "today" to a fixed calendar
	// date, keeping the current time of day. Test use only (e2e needs a
	// specific month, e.g. one where an edit crosses a season boundary),
	// never set in production. It is refused unless ADMIN_RP_ID is
	// "localhost" (the e2e stack's value), so a production env file that
	// leaks it fails to start instead of serving a pinned month.
	if v := getenv("TP_PUBLIC_TODAY"); v != "" {
		if rawRPID != "localhost" {
			return c, errors.New("TP_PUBLIC_TODAY is test-only and requires ADMIN_RP_ID=localhost; remove it from this env file")
		}
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			return c, fmt.Errorf("TP_PUBLIC_TODAY: %w", err)
		}
		log.Warn("TP_PUBLIC_TODAY is set; the public site's month is pinned — test use only")
		c.now = func() time.Time {
			now := time.Now().UTC()
			return time.Date(d.Year(), d.Month(), d.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)
		}
	}
	return c, nil
}

type app struct {
	handler  http.Handler
	mux      *http.ServeMux // Plans 6-7 mount /admin/ here
	pool     *pgxpool.Pool
	holder   *catalog.Holder
	watcher  *dataversion.Watcher
	drift    *calibrun.Drift
	watchdog *monitor.Watchdog
	cases    changeset.CasesFunc
	climate  *climate.Service
	ocean    *ocean.Service
	auth     *auth.Service
	log      *slog.Logger
	cancel   context.CancelFunc // stops start()'s background work; set by start
}

// newApp refuses to start on a database with pending migrations: the app
// role can't run DDL, so migrations run first as a separate step (the
// migrate service, `server -migrate-only`, as tp_owner).
func newApp(ctx context.Context, cfg config, log *slog.Logger) (*app, error) {
	// Count ERROR records from everything built here, for the watchdog.
	errs := monitor.NewErrorCounter(log.Handler())
	log = slog.New(errs)
	if err := db.RequireMigrated(ctx, cfg.DatabaseURL); err != nil {
		return nil, err
	}
	poolOpts := db.ServerPool
	if cfg.DBMaxConns > 0 {
		poolOpts.MaxConns = cfg.DBMaxConns
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, poolOpts)
	if err != nil {
		return nil, err
	}
	a := &app{pool: pool, log: log, climate: climate.NewService(pool), ocean: ocean.NewService(pool)}
	a.holder = catalog.NewHolder(func(ctx context.Context) (*catalog.Snapshot, error) { return catalog.Load(ctx, pool) }, log)
	a.watcher = dataversion.NewWatcher(pool, 5*time.Minute, log, func(ctx context.Context, _ string) error {
		return a.holder.Reload(ctx)
	})
	// The first Refresh runs onChange (the initial catalog load) before it
	// publishes the starting version, so the snapshot is never older than the
	// published version. A failure is logged, not fatal: nothing is published,
	// and waitForSnapshot's retry loop (plus the watcher's own poll/LISTEN,
	// once start() runs it) retries it.
	if err := a.watcher.Refresh(ctx); err != nil {
		log.Error("initial catalog load failed; will retry", "err", err)
	}

	fixtures, err := calibration.LoadFixtures(calibfixtures.FS())
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("calibration fixtures: %w", err)
	}
	a.cases = func(ctx context.Context) ([]calibration.Case, error) {
		return calibrun.Cases(ctx, fixtures, a.climate, a.ocean)
	}
	a.drift = &calibrun.Drift{Pool: pool, Snapshot: a.holder.Current, Cases: a.cases, DataVersion: a.watcher.Current, Log: log}
	drift := monitor.NewPinger("drift", cfg.HeartbeatDrift, log)
	a.drift.Notify = func(ctx context.Context, failed bool, summary string) {
		if failed {
			drift.Fail(ctx, summary)
		} else {
			drift.Success(ctx, summary)
		}
	}
	window := &monitor.Window{}
	schema := func(ctx context.Context) error {
		if err := db.SchemaCurrent(ctx, pool); err != nil {
			if errors.Is(err, db.ErrPendingMigrations) {
				return web.ErrSchemaNotCurrent
			}
			return err
		}
		return nil
	}
	a.watchdog = &monitor.Watchdog{
		Ready: func(ctx context.Context) error {
			return web.CheckReady(ctx, web.Ready{Snapshot: a.holder.Current, Ping: pool.Ping, Schema: schema})
		},
		Window:    window,
		Errors:    errs,
		App:       monitor.NewPinger("app", cfg.HeartbeatApp, log),
		AdminAuth: monitor.NewPinger("admin-auth", cfg.HeartbeatAdminAuth, log),
		Sample:    cfg.watchdogSample,
	}

	// The calibration cities double as the sitemap's season pages: they're
	// the places whose estimates are checked against published guides.
	featured := make([]web.Featured, len(fixtures))
	for i, f := range fixtures {
		featured[i] = web.Featured{Name: f.Name, Lat: f.Lat, Lng: f.Lng}
	}
	public := web.New(web.Deps{
		Climate: a.climate, Ocean: a.ocean, Places: places.NewService(pool),
		Snapshot: a.holder.Current, DataVersion: a.watcher.Current, Adjuster: season.NoopAdjuster{},
		Ping: pool.Ping, Schema: schema, Now: cfg.now, Log: log, TrustProxy: cfg.TrustProxy,
		DBSlots: publicDBSlots(pool.Config().MaxConns), Seafood: cfg.Seafood,
		BaseURL: cfg.BaseURL, Featured: featured,
	})
	log.Info("seafood feature", "enabled", cfg.Seafood)
	a.mux = http.NewServeMux()
	a.mux.Handle("/", public)

	if len(cfg.AdminSecretKey) == 32 {
		authSvc, err := auth.NewService(pool, auth.Config{
			RPID: cfg.AdminRPID, RPOrigins: []string{cfg.AdminOrigin}, SecretKey: cfg.AdminSecretKey,
			BackupEnabled: cfg.AdminBackup, Log: log, OnEvent: a.watchdog.Event,
		})
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("admin auth: %w", err)
		}
		a.auth = authSvc
		changes := changeset.NewService(pool, a.cases)
		adminH := admin.New(admin.Deps{
			Auth: authSvc, Log: log, TrustProxy: cfg.TrustProxy, Changes: changes, Pool: pool,
			Load: func(ctx context.Context) (*catalog.Snapshot, error) { return catalog.Load(ctx, pool) },
		})
		a.mux.Handle("/admin", adminH)
		a.mux.Handle("/admin/", adminH)
	} else {
		log.Warn("ADMIN_SECRET_KEY not set; /admin is disabled")
	}

	a.handler = monitor.CountResponses(window)(web.Common(log)(a.mux))
	return a, nil
}

// publicDBSlots is how many of the pool's connections public requests may
// hold at once: three quarters (12 of 16, 7 of 10), leaving the rest for
// admin, the drift check, catalog reloads and Apply.
func publicDBSlots(poolSize int32) int {
	return max(1, int(poolSize)*3/4)
}

// jobSchedule runs the drift check and the admin prune: daily, retrying
// failures after 1 min, doubling to 1 h.
var jobSchedule = jobs.Schedule{Every: 24 * time.Hour, RetryMin: time.Minute, RetryMax: time.Hour}

// start launches background work: the data-version listener, the daily
// drift check (on jobSchedule, anchored to the last recorded run; see
// calibrun.Drift.Loop), the monitoring watchdog and the admin prune. It derives its own cancelable
// context so close() can stop that work before closing the pool. The
// watcher's LISTEN session uses its own connection outside the pool, so
// pool.Close() no longer waits on it, but cancelling first still stops the
// drift check and any Refresh cleanly.
func (a *app) start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	go a.watcher.Run(ctx)
	go a.drift.Loop(ctx, jobSchedule)
	go a.watchdog.Run(ctx)
	if a.auth != nil {
		go a.pruneLoop(ctx, jobSchedule)
	}
}

// pruneLoop prunes the admin audit, session and enrollment-token tables at
// once, then every s.Every, retrying failures with backoff, until ctx ends.
func (a *app) pruneLoop(ctx context.Context, s jobs.Schedule) {
	jobs.Run(ctx, s, 0, func(ctx context.Context) error {
		r, err := a.auth.Prune(ctx)
		if err != nil {
			return err
		}
		a.log.Info("admin prune", "login_log", r.LoginLog, "sessions", r.Sessions, "enrollment_tokens", r.EnrollmentTokens)
		return nil
	}, func(err error) { a.log.Error("admin prune failed", "err", err) })
}

// waitForSnapshot blocks until a catalog snapshot is loaded, retrying every
// `every` via the data-version watcher (whose onChange reloads the catalog
// before any version publish, including the first, so a successful Refresh
// always leaves a snapshot live). The server does not listen before this
// returns (spec §11.6: the site is not served without a snapshot).
//
// Each failed attempt is logged once (so at most one line per `every`),
// giving operators the reason the site is still not listening.
func (a *app) waitForSnapshot(ctx context.Context, every time.Duration) error {
	for attempt := 1; a.holder.Current() == nil; attempt++ {
		err := a.watcher.Refresh(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() == nil {
			a.log.Warn("catalog snapshot not loaded yet; retrying", "attempt", attempt, "retry_in", every.String(), "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
	return nil
}

func (a *app) close() {
	if a.cancel != nil {
		a.cancel()
	}
	a.pool.Close()
}
