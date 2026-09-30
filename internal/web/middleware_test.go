package web

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func ok() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
}

func TestRequestID(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = RequestIDFrom(r.Context()) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if len(seen) != 16 || rec.Header().Get("X-Request-ID") != seen {
		t.Fatalf("generated id %q / header %q", seen, rec.Header().Get("X-Request-ID"))
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "abc-12345")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "abc-12345" {
		t.Fatalf("inbound id not reused: %q", seen)
	}
	req.Header.Set("X-Request-ID", "<bad id>")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen == "<bad id>" {
		t.Fatal("invalid inbound id must be replaced")
	}
}

func TestAccessLogOmitsQueryAndRecordsCell(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := RequestID(AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetCellID(r.Context(), 12345)
		w.WriteHeader(http.StatusTeapot)
	})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/season?lat=51.5&lng=-0.12", nil))
	line := buf.String()
	if strings.Contains(line, "51.5") || strings.Contains(line, "lat=") {
		t.Fatalf("coordinates leaked into log: %s", line)
	}
	for _, want := range []string{`"path":"/season"`, `"status":418`, `"cell_id":12345`, `"request_id"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("log missing %s: %s", want, line)
		}
	}
}

func TestAccessLogDefaultsEmptyResponseTo200(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := RequestID(AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handler writes nothing, neither WriteHeader nor Write called
	})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/test", nil))
	line := buf.String()
	if !strings.Contains(line, `"status":200`) {
		t.Fatalf("empty response should default to status 200, got: %s", line)
	}
}

func TestRecover(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := RequestID(Recover(log, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("panel"))
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || rec.Body.String() != "panel" || !strings.Contains(buf.String(), "boom") || !strings.Contains(buf.String(), "request_id") {
		t.Fatalf("code=%d body=%q log=%s", rec.Code, rec.Body.String(), buf.String())
	}
}

// ClientIP trusts exactly one proxy hop (Caddy): the rightmost
// X-Forwarded-For entry is the peer Caddy saw. Anything a client sent sits to
// its left and is ignored. A garbled rightmost entry falls back to RemoteAddr.
func TestClientIP(t *testing.T) {
	for _, tc := range []struct {
		name       string
		remoteAddr string
		xff        []string // one element per header line
		trust      bool
		want       string
	}{
		{"untrusted ignores the header", "10.0.0.5:5555", []string{"203.0.113.9"}, false, "10.0.0.5"},
		{"rightmost entry wins", "10.0.0.5:5555", []string{"198.51.100.1, 203.0.113.9"}, true, "203.0.113.9"},
		{"last entry of the last line", "10.0.0.5:5555", []string{"198.51.100.1", "192.0.2.7, 203.0.113.9"}, true, "203.0.113.9"},
		{"garbled rightmost falls back", "10.0.0.5:5555", []string{"198.51.100.1, garbage"}, true, "10.0.0.5"},
		{"empty rightmost falls back", "10.0.0.5:5555", []string{"203.0.113.9, "}, true, "10.0.0.5"},
		{"no header falls back", "10.0.0.5:5555", nil, true, "10.0.0.5"},
		{"IPv6 canonical form", "10.0.0.5:5555", []string{"2001:DB8::1"}, true, "2001:db8::1"},
		{"IPv6 zone dropped", "10.0.0.5:5555", []string{"fe80::1%eth0"}, true, "fe80::1"},
		{"RemoteAddr without port", "10.0.0.5", nil, true, "10.0.0.5"},
		{"RemoteAddr IPv6 zone dropped", "[fe80::1%eth0]:5555", nil, false, "fe80::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remoteAddr
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, tc.trust); got != tc.want {
				t.Fatalf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRateLimit(t *testing.T) {
	now := time.Unix(0, 0)
	rl := NewRateLimiter(func() time.Time { return now })
	limited := 0
	h := rl.Middleware(false, PublicLimits, func(w http.ResponseWriter, r *http.Request) {
		limited++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})(ok())

	hit := func(path, ip string) int {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = ip + ":1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 0; i < 20; i++ {
		if c := hit("/places", "1.1.1.1"); c != 200 {
			t.Fatalf("request %d limited too early", i)
		}
	}
	if hit("/places", "1.1.1.1") != 429 {
		t.Fatal("21st request in the burst should be limited")
	}
	if hit("/places", "2.2.2.2") != 200 {
		t.Fatal("other IPs are independent")
	}
	for i := 0; i < 10; i++ {
		_ = hit("/season", "3.3.3.3")
	}
	if hit("/ingredient/tomato", "3.3.3.3") != 429 {
		t.Fatal("season and ingredient share the 2/s burst-10 bucket")
	}
	if hit("/about/data", "1.1.1.1") != 200 {
		t.Fatal("unclassified paths are not limited")
	}
	now = now.Add(time.Second) // 10 tokens refill for /places
	if hit("/places", "1.1.1.1") != 200 {
		t.Fatal("tokens should refill")
	}
}

func TestRateLimiterLRUEviction(t *testing.T) {
	now := time.Unix(0, 0)
	rl := NewRateLimiterSize(2, func() time.Time { return now })
	limited := 0
	h := rl.Middleware(false, PublicLimits, func(w http.ResponseWriter, r *http.Request) {
		limited++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})(ok())

	hit := func(path, ip string) int {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = ip + ":1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}

	// Fill burst for IP1: /places has burst 20
	for i := 0; i < 20; i++ {
		if c := hit("/places", "1.1.1.1"); c != 200 {
			t.Fatalf("IP1 request %d limited too early", i)
		}
	}
	if hit("/places", "1.1.1.1") != 429 {
		t.Fatal("IP1 21st request should be limited")
	}

	// Fill burst for IP2: /season has burst 10
	for i := 0; i < 10; i++ {
		if c := hit("/season", "2.2.2.2"); c != 200 {
			t.Fatalf("IP2 request %d limited too early", i)
		}
	}
	if hit("/season", "2.2.2.2") != 429 {
		t.Fatal("IP2 11th request should be limited")
	}

	// Now add IP3 with /places (should evict least-recently-used, which is IP1)
	if c := hit("/places", "3.3.3.3"); c != 200 {
		t.Fatal("IP3 first request should succeed")
	}

	// IP1's limiter should have been evicted; it gets a fresh burst
	if c := hit("/places", "1.1.1.1"); c != 200 {
		t.Fatal("evicted IP1 should have a fresh burst and succeed")
	}

	// Verify cache never exceeds capacity
	if rl.cache.Len() > 2 {
		t.Fatalf("cache size %d exceeds capacity 2", rl.cache.Len())
	}
}

func TestTimeout(t *testing.T) {
	var dl time.Time
	h := Timeout(2 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dl, _ = r.Context().Deadline()
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(context.Background()))
	if d := time.Until(dl); d <= 0 || d > 2*time.Second {
		t.Fatalf("deadline in %v", d)
	}
}

func TestLimiterKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.9":        "203.0.113.9",
		"::ffff:9.9.9.9":     "9.9.9.9",
		"2001:db8:1:2::1":    "2001:db8:1:2::/64",
		"2001:db8:1:2:ff::9": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":    "2001:db8:1:3::/64",
		"garbage":            "garbage",
	} {
		if got := LimiterKey(in); got != want {
			t.Errorf("LimiterKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// Final review 5: IPv6 clients are limited per /64 (one subscriber), and
// IPv4-mapped IPv6 addresses count as the IPv4 address.
func TestRateLimitIPv6Prefix(t *testing.T) {
	now := time.Unix(0, 0)
	rl := NewRateLimiter(func() time.Time { return now })
	h := rl.Middleware(false, PublicLimits, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})(ok())
	hit := func(remoteAddr string) int {
		r := httptest.NewRequest("GET", "/places", nil)
		r.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 0; i < 20; i++ {
		hit(fmt.Sprintf("[2001:db8:1:2::%x]:1", i+1)) // 20 addresses, one /64
	}
	if hit("[2001:db8:1:2:ffff::1]:1") != 429 {
		t.Fatal("addresses in the same /64 must share a bucket")
	}
	if hit("[2001:db8:1:3::1]:1") != 200 {
		t.Fatal("a different /64 is independent")
	}
	for i := 0; i < 20; i++ {
		hit("9.9.9.9:1")
	}
	if hit("[::ffff:9.9.9.9]:1") != 429 {
		t.Fatal("IPv4-mapped IPv6 must share the IPv4 bucket")
	}
	if hit("9.9.9.10:1") != 200 {
		t.Fatal("IPv4 addresses are keyed individually")
	}
}

// Issue #10: behind Caddy the limiter keys on the rightmost X-Forwarded-For
// entry, so rotating the entries a client controls (to its left) can't buy
// fresh buckets.
func TestRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	now := time.Unix(0, 0)
	rl := NewRateLimiter(func() time.Time { return now })
	h := rl.Middleware(true, PublicLimits, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})(ok())
	hit := func(xff string) int {
		r := httptest.NewRequest("GET", "/places", nil)
		r.RemoteAddr = "10.0.0.5:5555" // Caddy
		r.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 0; i < 20; i++ {
		if code := hit(fmt.Sprintf("198.51.100.%d, 203.0.113.9", i+1)); code != 200 {
			t.Fatalf("request %d = %d, want 200 within burst", i+1, code)
		}
	}
	if hit("198.51.100.99, 203.0.113.9") != 429 {
		t.Fatal("rotating the spoofed leftmost entry must not escape the limit")
	}
	if hit("203.0.113.10") != 200 {
		t.Fatal("a different real client is independent")
	}
}

// Healthy probes don't reach the log; failing ones do.
func TestAccessLogSkipsHealthyProbes(t *testing.T) {
	var out bytes.Buffer
	status := http.StatusOK
	h := AccessLog(slog.New(slog.NewTextHandler(&out, nil)))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	for _, p := range []string{"/healthz", "/readyz"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
	}
	if out.Len() != 0 {
		t.Fatalf("healthy probes logged:\n%s", out.String())
	}
	status = http.StatusServiceUnavailable
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/readyz", nil))
	status = http.StatusOK
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/season", nil))
	if s := out.String(); !strings.Contains(s, "path=/readyz") || !strings.Contains(s, "status=503") || !strings.Contains(s, "path=/season") {
		t.Fatalf("failing probe or page request not logged:\n%s", s)
	}
}
