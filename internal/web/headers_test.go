package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var wantSecurityHeaders = map[string]string{
	"Content-Security-Policy":      PublicCSP,
	"X-Content-Type-Options":       "nosniff",
	"X-Frame-Options":              "DENY",
	"Referrer-Policy":              "same-origin",
	"Permissions-Policy":           "geolocation=(self), camera=(), microphone=(), payment=(), usb=()",
	"Cross-Origin-Opener-Policy":   "same-origin",
	"Cross-Origin-Resource-Policy": "same-origin",
}

// siteHandler wraps the public handler the way cmd/server does: web.Common
// around a mux with the public site at "/". It adds a route that panics
// outside the public handler, to reach Common's last-resort recovery.
func siteHandler(hs *harness) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", hs.h)
	mux.HandleFunc("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	return Common(slog.New(slog.NewTextHandler(io.Discard, nil)))(mux)
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	hs := newHarness(t)
	site := siteHandler(hs)
	serve := func(path, ip string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = ip + ":1"
		rec := httptest.NewRecorder()
		site.ServeHTTP(rec, r)
		return rec
	}
	// Exhaust the /season burst (10) for one address, so its next request is a 429.
	for i := 0; i < 10; i++ {
		serve("/season?lat=38.72&lng=-9.14", "192.0.2.50")
	}
	cases := []struct {
		name, path, ip string
		code           int
		before         func()
	}{
		{name: "home", path: "/", code: 200},
		{name: "season", path: lisbonQ + "&date=2026-08-15", code: 200},
		{name: "season bad coords", path: "/season?lat=abc&lng=1", code: 400},
		{name: "unknown ingredient", path: "/ingredient/nope?cell=1", code: 404},
		{name: "static", path: "/static/app.js", code: 200},
		{name: "unknown path", path: "/nope", code: 404},
		{name: "rate limited", path: "/season?lat=38.72&lng=-9.14", ip: "192.0.2.50", code: 429},
		{name: "public panic", path: "/season?lat=10&lng=10", code: 500, before: func() { hs.climate.panic = true }},
		{name: "common panic", path: "/boom", code: 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.before != nil {
				c.before()
			}
			ip := c.ip
			if ip == "" {
				ip = "192.0.2.1"
			}
			rec := serve(c.path, ip)
			if rec.Code != c.code {
				t.Fatalf("%s: status %d, want %d", c.path, rec.Code, c.code)
			}
			for k, want := range wantSecurityHeaders {
				if got := rec.Header().Get(k); got != want {
					t.Errorf("%s: %s = %q, want %q", c.path, k, got, want)
				}
			}
		})
	}
	if rec := serve("/static/app.js", "192.0.2.2"); !strings.Contains(rec.Header().Get("Cache-Control"), "max-age=86400") {
		t.Fatalf("static files must keep their Cache-Control: %v", rec.Header())
	}
}

func TestHTMXEvalAndScriptTagsDisabled(t *testing.T) {
	hs := newHarness(t)
	_, body, _ := hs.get("/", false)
	for _, want := range []string{`"allowEval":false`, `"allowScriptTags":false`, `"selfRequestsOnly":true`} {
		if !strings.Contains(body, want) {
			t.Fatalf("htmx config missing %s:\n%s", want, body)
		}
	}
}
