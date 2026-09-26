package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/places"
	"thymeandplace/internal/season"
	"thymeandplace/internal/web"
)

type config struct {
	DatabaseURL string
	Addr        string
	TrustProxy  bool

	AdminSecretKey []byte
	AdminRPID      string
	AdminOrigin    string
	AdminBackup    bool

	now func() time.Time // server clock for the public site; nil → time.Now (tests pin it)
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{DatabaseURL: getenv("DATABASE_URL"), Addr: getenv("ADDR"), TrustProxy: getenv("TRUST_PROXY_HEADERS") == "true"}
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if k := getenv("ADMIN_SECRET_KEY"); k != "" {
		key, err := auth.ParseSecretKey(k)
		if err != nil {
			return c, err
		}
		c.AdminSecretKey = key
	}
	c.AdminRPID, c.AdminOrigin = getenv("ADMIN_RP_ID"), getenv("ADMIN_ORIGIN")
	if c.AdminRPID == "" {
		c.AdminRPID = "thymeand.place"
	}
	if c.AdminOrigin == "" {
		c.AdminOrigin = "https://" + c.AdminRPID
	}
	// A browser's Origin never ends in "/", so neither may the WebAuthn
	// origin (nor the enrollment links built from it).
	c.AdminOrigin = strings.TrimRight(c.AdminOrigin, "/")
	c.AdminBackup = getenv("ADMIN_BACKUP_LOGIN") != "off"
	return c, nil
}

type app struct {
	handler http.Handler
	mux     *http.ServeMux // Plans 6-7 mount /admin/ here
	pool    *pgxpool.Pool
	holder  *catalog.Holder
	watcher *dataversion.Watcher
	drift   *calibrun.Drift
	cases   changeset.CasesFunc
	climate *climate.Service
	ocean   *ocean.Service
	auth    *auth.Service
	log     *slog.Logger
	cancel  context.CancelFunc // stops start()'s background work; set by start
}

func newApp(ctx context.Context, cfg config, log *slog.Logger) (*app, error) {
	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return nil, err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
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
	// and start()'s retry loop and the watcher's own poll/LISTEN retry it.
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

	public := web.New(web.Deps{
		Climate: a.climate, Ocean: a.ocean, Places: places.NewService(pool),
		Snapshot: a.holder.Current, DataVersion: a.watcher.Current, Adjuster: season.NoopAdjuster{},
		Ping: pool.Ping, Now: cfg.now, Log: log, TrustProxy: cfg.TrustProxy,
	})
	a.mux = http.NewServeMux()
	a.mux.Handle("/", public)

	if len(cfg.AdminSecretKey) == 32 {
		authSvc, err := auth.NewService(pool, auth.Config{
			RPID: cfg.AdminRPID, RPOrigins: []string{cfg.AdminOrigin}, SecretKey: cfg.AdminSecretKey,
			BackupEnabled: cfg.AdminBackup, Log: log,
		})
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("admin auth: %w", err)
		}
		a.auth = authSvc
		changes := changeset.NewService(pool, a.ocean, a.cases)
		adminH := admin.New(admin.Deps{
			Auth: authSvc, Log: log, TrustProxy: cfg.TrustProxy, Changes: changes, Pool: pool,
			Load: func(ctx context.Context) (*catalog.Snapshot, error) { return catalog.Load(ctx, pool) },
		})
		a.mux.Handle("/admin", adminH)
		a.mux.Handle("/admin/", adminH)
	} else {
		log.Warn("ADMIN_SECRET_KEY not set; /admin is disabled")
	}

	a.handler = web.Common(log)(a.mux)
	return a, nil
}

// start launches background work: the data-version listener, retries of a
// failed initial catalog load, and the daily drift check. It derives its own
// cancelable context so close() can stop that work before closing the pool:
// the watcher holds a dedicated LISTEN connection checked out of the pool for
// as long as it runs, and pool.Close() blocks until every checked-out
// connection is released, so shutdown must cancel the background work first.
func (a *app) start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	go a.watcher.Run(ctx)
	go func() {
		for a.holder.Current() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				// A failed load leaves the version unpublished, so Refresh
				// sees a difference and runs onChange (the reload) again.
				if err := a.watcher.Refresh(ctx); err != nil {
					a.log.Warn("catalog load retry failed", "err", err)
				}
			}
		}
	}()
	go a.drift.Loop(ctx, 24*time.Hour)
}

func (a *app) close() {
	if a.cancel != nil {
		a.cancel()
	}
	a.pool.Close()
}
