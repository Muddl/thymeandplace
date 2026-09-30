package monitor

import (
	"net/http"
	"sync"
)

// Window counts responses and 5xx between reports. Probe paths are ignored:
// the watchdog samples readiness directly. It stores counts only.
type Window struct {
	mu            sync.Mutex
	requests, e5x int
}

// Record counts one response to path with status, unless path is a probe.
func (w *Window) Record(path string, status int) {
	if path == "/healthz" || path == "/readyz" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.requests++
	if status >= 500 {
		w.e5x++
	}
}

// Take returns the counts since the last Take and resets them.
func (w *Window) Take() (requests, errors5xx int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	requests, errors5xx = w.requests, w.e5x
	w.requests, w.e5x = 0, 0
	return
}

// CountResponses records every response's status in w. Wrap it outside
// web.Common, whose recovery turns a panic into a 500 that this then sees.
func CountResponses(w *Window) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rec := &statusWriter{ResponseWriter: rw}
			next.ServeHTTP(rec, r)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			w.Record(r.URL.Path, rec.status)
		})
	}
}

// statusWriter mirrors web.statusRecorder so monitor does not depend on web internals.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
