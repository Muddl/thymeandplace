// Package web serves the public site.
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type ctxKey int

const infoKey ctxKey = 0

type reqInfo struct {
	id     string
	cellID *int
}

var inboundIDRE = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !inboundIDRE.MatchString(id) {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), infoKey, &reqInfo{id: id})))
	})
}

func info(ctx context.Context) *reqInfo {
	i, _ := ctx.Value(infoKey).(*reqInfo)
	return i
}

func RequestIDFrom(ctx context.Context) string {
	if i := info(ctx); i != nil {
		return i.id
	}
	return ""
}

// SetCellID records the grid cell for the access log. Coordinates are never logged.
func SetCellID(ctx context.Context, cellID int) {
	if i := info(ctx); i != nil {
		i.cellID = &cellID
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			attrs := []any{
				"method", r.Method, "path", r.URL.Path, "status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(), "request_id", RequestIDFrom(r.Context()),
			}
			if i := info(r.Context()); i != nil && i.cellID != nil {
				attrs = append(attrs, "cell_id", *i.cellID)
			}
			log.Info("request", attrs...)
		})
	}
}

func Recover(log *slog.Logger, render func(http.ResponseWriter, *http.Request)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler {
						panic(v)
					}
					log.Error("panic", "err", v, "request_id", RequestIDFrom(r.Context()), "stack", string(debug.Stack()))
					render(w, r)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP returns the caller's IP. With trustProxy, the first
// X-Forwarded-For entry (set by Caddy) wins.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type Limit struct {
	Rate  rate.Limit
	Burst int
}

// PublicLimits classifies public paths (§11.4).
func PublicLimits(path string) (string, Limit, bool) {
	switch {
	case path == "/places":
		return "places", Limit{Rate: 10, Burst: 20}, true
	case path == "/season" || strings.HasPrefix(path, "/ingredient/"):
		return "season", Limit{Rate: 2, Burst: 10}, true
	}
	return "", Limit{}, false
}

type limiterEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

type RateLimiter struct {
	mu        sync.Mutex
	m         map[string]*limiterEntry
	now       func() time.Time
	lastSweep time.Time
}

func NewRateLimiter(now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	return &RateLimiter{m: map[string]*limiterEntry{}, now: now}
}

func (rl *RateLimiter) allow(key string, l Limit) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	if now.Sub(rl.lastSweep) > time.Minute {
		for k, e := range rl.m {
			if now.Sub(e.seen) > 10*time.Minute {
				delete(rl.m, k)
			}
		}
		rl.lastSweep = now
	}
	e, ok := rl.m[key]
	if !ok {
		e = &limiterEntry{lim: rate.NewLimiter(l.Rate, l.Burst)}
		rl.m[key] = e
	}
	e.seen = now
	return e.lim.AllowN(now, 1)
}

func (rl *RateLimiter) Middleware(trustProxy bool, classify func(string) (string, Limit, bool), onLimit func(http.ResponseWriter, *http.Request)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if class, l, ok := classify(r.URL.Path); ok && !rl.allow(class+"|"+ClientIP(r, trustProxy), l) {
				onLimit(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
