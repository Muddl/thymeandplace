package monitor

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"thymeandplace/internal/web"
)

func TestWindowCountsAndResets(t *testing.T) {
	var w Window
	for _, rec := range []struct {
		path   string
		status int
	}{{"/season", 200}, {"/season", 503}, {"/healthz", 500}, {"/readyz", 503}, {"/admin", 500}, {"/static/app.js", 304}} {
		w.Record(rec.path, rec.status)
	}
	if n, e := w.Take(); n != 4 || e != 2 {
		t.Fatalf("got %d requests, %d 5xx; want 4, 2 (probes ignored)", n, e)
	}
	if n, e := w.Take(); n != 0 || e != 0 {
		t.Fatal("Take must reset")
	}
}

func TestCountResponsesSeesRecoveredPanicAs500(t *testing.T) {
	var w Window
	panicky := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := CountResponses(&w)(web.Common(slog.New(slog.NewTextHandler(io.Discard, nil)))(panicky))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/season", nil))
	quiet := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	CountResponses(&w)(quiet).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if n, e := w.Take(); n != 2 || e != 1 {
		t.Fatalf("got %d requests, %d 5xx; want 2, 1", n, e)
	}
}

// A client that disconnects mid-request must not be able to fail the app
// check: the 503 rendered after its cancelled lookup reached nobody.
func TestCountResponsesSkipsCancelledRequests(t *testing.T) {
	var w Window
	unavailable := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	h := CountResponses(&w)(unavailable)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 5 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/season", nil).WithContext(ctx))
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/season", nil))
	if n, e := w.Take(); n != 1 || e != 0 {
		t.Fatalf("got %d requests, %d 5xx; want 1, 0 (cancelled requests not counted)", n, e)
	}
	// A server-side deadline (web.Timeout derives its own context) is not a
	// disconnect: its 503 still counts.
	dctx, dcancel := context.WithTimeout(context.Background(), 0)
	defer dcancel()
	<-dctx.Done()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/season", nil).WithContext(dctx))
	if n, e := w.Take(); n != 1 || e != 1 {
		t.Fatalf("got %d requests, %d 5xx; want 1, 1 (deadline still counted)", n, e)
	}
}
