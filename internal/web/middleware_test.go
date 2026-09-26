package web

import (
	"bytes"
	"context"
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

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:5555"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if ClientIP(r, false) != "10.0.0.5" || ClientIP(r, true) != "203.0.113.9" {
		t.Fatalf("got %q / %q", ClientIP(r, false), ClientIP(r, true))
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
