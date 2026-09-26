// Package web serves the public site.
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2"
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
			// Default to 200 if handler never called WriteHeader or Write
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
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

// limiterKey buckets a client IP for rate limiting: IPv4 (including
// IPv4-mapped IPv6) per address, IPv6 per /64, since one subscriber
// typically holds a whole /64. Unparseable values are used as-is.
func limiterKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.Unmap().WithZone("")
	if a.Is4() {
		return a.String()
	}
	p, err := a.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
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

const maxLimiterKeys = 100_000

type RateLimiter struct {
	mu    sync.Mutex
	cache *lru.Cache[string, *rate.Limiter]
	now   func() time.Time
}

// newRateLimiterWithCapacity creates a RateLimiter with a specified cache capacity (for testing).
func newRateLimiterWithCapacity(cap int, now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	cache, _ := lru.New[string, *rate.Limiter](cap)
	return &RateLimiter{cache: cache, now: now}
}

func NewRateLimiter(now func() time.Time) *RateLimiter {
	return newRateLimiterWithCapacity(maxLimiterKeys, now)
}

func (rl *RateLimiter) allow(key string, l Limit) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	lim, ok := rl.cache.Get(key)
	if !ok {
		lim = rate.NewLimiter(l.Rate, l.Burst)
		rl.cache.Add(key, lim)
	}
	return lim.AllowN(now, 1)
}

func (rl *RateLimiter) Middleware(trustProxy bool, classify func(string) (string, Limit, bool), onLimit func(http.ResponseWriter, *http.Request)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if class, l, ok := classify(r.URL.Path); ok && !rl.allow(class+"|"+limiterKey(ClientIP(r, trustProxy)), l) {
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
