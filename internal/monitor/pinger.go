// Package monitor sends heartbeat pings to Healthchecks.io and watches the
// app's own health (readiness, 5xx, ERROR logs, security events). Nothing
// it sends carries visitor data (core spec §15; docs/operations.md,
// "Monitoring and alerts").
package monitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Notifier is one heartbeat check.
type Notifier interface {
	Success(ctx context.Context, body string)
	Fail(ctx context.Context, body string)
}

const maxPingBody = 10 << 10

// Pinger POSTs to one Healthchecks.io check. Its URL is a secret: it is
// never logged.
type Pinger struct {
	check   string
	url     string // "" → disabled
	client  *http.Client
	backoff []time.Duration // waits before attempts 2 and 3
	log     *slog.Logger
}

// NewPinger returns a pinger for check. An empty rawURL disables it quietly.
// An unusable one (not https, or http to a non-loopback host) disables it
// and logs an ERROR, so a monitoring typo never stops the site; the missed
// pings then raise the alert.
func NewPinger(check, rawURL string, log *slog.Logger) *Pinger {
	p := &Pinger{check: check, client: &http.Client{Timeout: 5 * time.Second},
		backoff: []time.Duration{time.Second, 2 * time.Second}, log: log}
	if rawURL == "" {
		return p
	}
	u, err := url.Parse(rawURL)
	if err != nil || !pingURLAllowed(u) {
		log.Error("heartbeat URL invalid; check disabled", "check", check)
		return p
	}
	p.url = strings.TrimRight(rawURL, "/")
	return p
}

func pingURLAllowed(u *url.URL) bool {
	if u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		h := u.Hostname()
		ip := net.ParseIP(h)
		return h == "localhost" || (ip != nil && ip.IsLoopback())
	}
	return false
}

func (p *Pinger) Enabled() bool                            { return p.url != "" }
func (p *Pinger) Success(ctx context.Context, body string) { p.send(ctx, p.url, body) }
func (p *Pinger) Fail(ctx context.Context, body string)    { p.send(ctx, p.url+"/fail", body) }

func (p *Pinger) send(ctx context.Context, target, body string) {
	if p.url == "" {
		return
	}
	if len(body) > maxPingBody {
		body = body[:maxPingBody]
	}
	var last error
	for attempt := 0; attempt <= len(p.backoff); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(p.backoff[attempt-1]):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
		if err != nil {
			last = errors.New("building request failed")
			break
		}
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		resp, err := p.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			last = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
		resp.Body.Close()
		if resp.StatusCode < 300 {
			return
		}
		last = fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// *url.Error prints the request URL, which holds the check's secret
	// UUID: log only its cause.
	var ue *url.Error
	if errors.As(last, &ue) {
		last = ue.Err
	}
	p.log.Warn("heartbeat ping failed", "check", p.check, "err", last)
}
