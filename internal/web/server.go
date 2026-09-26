package web

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/a-h/templ"

	"thymeandplace/internal/cache"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/places"
	"thymeandplace/internal/season"
	"thymeandplace/internal/web/views"
	"thymeandplace/static"
)

type Climate interface {
	Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error)
	ForCell(ctx context.Context, id int) (*climate.MonthlyNormals, error)
}

type Ocean interface {
	Nearby(ctx context.Context, lat, lng float64) ([]ocean.EcoregionID, error)
}

type Places interface {
	Search(ctx context.Context, q string) ([]places.Place, error)
	Nearest(ctx context.Context, lat, lng float64) (places.Place, bool, error)
	ByID(ctx context.Context, id int32) (places.Place, bool, error)
}

type Deps struct {
	Climate     Climate
	Ocean       Ocean
	Places      Places
	Snapshot    func() *catalog.Snapshot
	DataVersion func() string
	Adjuster    season.Adjuster
	Ping        func(context.Context) error
	Now         func() time.Time
	Log         *slog.Logger
	TrustProxy  bool
}

type Server struct {
	d     Deps
	cache *cache.Cache[seasonCore]
}

// New returns the public handler with its own middleware (§11.4):
// panic recovery → per-IP rate limit → 2 s timeout.
func New(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Adjuster == nil {
		d.Adjuster = season.NoopAdjuster{}
	}
	s := &Server{d: d, cache: cache.New[seasonCore](cache.DefaultSize)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /places", s.places)
	mux.HandleFunc("GET /season", s.season)
	mux.HandleFunc("GET /ingredient/{slug}", s.ingredient)
	mux.HandleFunc("GET /about/data", s.about)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(24*time.Hour, http.FileServerFS(static.FS))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.render(w, r, http.StatusNotFound, "Not found", "", nil, views.NotFound())
	})

	var h http.Handler = mux
	h = Timeout(2 * time.Second)(h)
	h = NewRateLimiter(nil).Middleware(d.TrustProxy, PublicLimits, s.tooMany)(h)
	h = Recover(d.Log, func(w http.ResponseWriter, r *http.Request) {
		s.render(w, r, http.StatusInternalServerError, "Something went wrong", "", nil, views.LoadError("", r.URL.RequestURI()))
	})(h)
	return h
}

// Common wraps the whole site (public and admin): request ID → access log →
// last-resort recovery.
func Common(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		plain := Recover(log, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		})(next)
		return RequestID(AccessLog(log)(plain))
	}
}

func cacheFor(d time.Duration, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(int(d.Seconds())))
		h.ServeHTTP(w, r)
	})
}

// partial reports whether to send a fragment rather than a full page.
func partial(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true"
}

// render writes either the fragment (for HTMX) or the full page with the
// fragment in the right slot. placesSlot is used only by /places.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, title, query string, placesSlot, results templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Add("Vary", "HX-Request")
	w.WriteHeader(status)
	var c templ.Component
	switch {
	case partial(r) && placesSlot != nil:
		c = placesSlot
	case partial(r):
		c = results
	default:
		c = views.Page(views.PageVM{Title: title, Query: query, Places: placesSlot, Results: results})
	}
	if c == nil { // an HTMX request for a page with no fragment (e.g. GET / with HX-Request)
		c = templ.NopComponent
	}
	if err := c.Render(r.Context(), w); err != nil {
		s.d.Log.Warn("render failed", "err", err, "request_id", RequestIDFrom(r.Context()))
	}
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "What's in season", "", nil, nil)
}

func (s *Server) about(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "About the data", "", nil, views.About())
}

func (s *Server) tooMany(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "1")
	if r.URL.Path == "/places" {
		s.render(w, r, http.StatusTooManyRequests, "Search", "", views.PlacesPaused(), nil)
		return
	}
	s.render(w, r, http.StatusTooManyRequests, "Please wait", "", nil, views.TooMany())
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.d.Snapshot() == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("catalog not loaded"))
		return
	}
	if err := s.d.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("database unavailable"))
		return
	}
	_, _ = w.Write([]byte("ready"))
}

// TODO(task 6): the stub below is temporary so this task compiles on its
// own. It is replaced (and this block deleted) by the task that implements
// the real handler: ingredient (task 6).
func (s *Server) ingredient(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
