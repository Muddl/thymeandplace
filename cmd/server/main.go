// Command server serves the public site and /admin.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	healthcheck := flag.String("healthcheck", "", "GET this URL and exit 0 on HTTP 200 (container health checks)")
	flag.Parse()
	if *healthcheck != "" {
		os.Exit(runHealthcheck(*healthcheck))
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	a, err := newApp(ctx, cfg, log)
	if err != nil {
		log.Error("startup", "err", err)
		os.Exit(1)
	}
	a.start(ctx)

	if err := a.waitForSnapshot(ctx, 5*time.Second); err != nil {
		log.Error("no catalog snapshot; not serving", "err", err)
		a.close()
		os.Exit(1)
	}

	srv := &http.Server{
		Addr: cfg.Addr, Handler: a.handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	// done closes once Shutdown has drained in-flight requests. ListenAndServe
	// returns ErrServerClosed as soon as Shutdown begins, so main must wait
	// on done before closing the pool those requests are still using.
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			log.Warn("shutdown", "err", err)
		}
	}()
	log.Info("listening", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Error("server", "err", err)
		a.close()
		os.Exit(1)
	}
	<-done
	a.close()
}

func runHealthcheck(url string) int {
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
