// Package admin serves /admin for the single owner.
package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/web"
)

const (
	PreCookie    = "__Host-tp_pre"
	maxBody      = 6 << 20
	adminTimeout = 30 * time.Second
)

type Deps struct {
	Auth       *auth.Service
	Log        *slog.Logger
	TrustProxy bool
	Changes    *changeset.Service
	Load       func(context.Context) (*catalog.Snapshot, error)
	Pool       *pgxpool.Pool

	now    func() time.Time                       // request limiter clock; nil → time.Now (tests pin it)
	limits func(string) (string, web.Limit, bool) // request limiter classes; nil → adminLimits (tests switch it off)
}

// adminLimiterKeys caps how many client keys the /admin request limiter
// tracks; the owner is one client, so this only bounds a flood's memory.
const adminLimiterKeys = 10_000

// adminLimits classifies /admin paths for the per-client request limiter.
// The sizes come from the owner's real traffic: a sign-in is 3 requests,
// enrolling two devices 6, and catalog search fires at most every 250 ms.
func adminLimits(path string) (string, web.Limit, bool) {
	switch {
	case path == "/admin/login" || strings.HasPrefix(path, "/admin/login/") ||
		path == "/admin/enroll" || strings.HasPrefix(path, "/admin/enroll/"):
		return "admin-signin", web.Limit{Rate: 1, Burst: 20}, true
	case path == "/admin" || strings.HasPrefix(path, "/admin/"):
		return "admin", web.Limit{Rate: 10, Burst: 100}, true
	}
	return "", web.Limit{}, false
}

// tooMany answers a request over the /admin request limit. It runs before
// any database work and writes no audit row.
func tooMany(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Retry-After", "1")
	http.Error(w, "Too many requests. Wait a few seconds and try again.", http.StatusTooManyRequests)
}

type Admin struct {
	d   Deps
	mux *http.ServeMux
}

type route struct{ Method, Path string }

// writeRoutes lists every non-GET route; the CSRF test walks it.
var writeRoutes = []route{
	{"POST", "/admin/login/passkey/begin"}, {"POST", "/admin/login/passkey/finish"},
	{"POST", "/admin/login/backup"}, {"POST", "/admin/logout"},
	{"POST", "/admin/enroll/begin?token=x"}, {"POST", "/admin/enroll/finish?token=x"},
	{"POST", "/admin/security/reauth/begin"}, {"POST", "/admin/security/reauth/finish"},
	{"POST", "/admin/security/passkeys/begin"}, {"POST", "/admin/security/passkeys/finish"},
	{"POST", "/admin/security/passkeys/AAAA/rename"}, {"POST", "/admin/security/passkeys/AAAA/remove"},
	{"POST", "/admin/ingredients/tomato/preview"}, {"POST", "/admin/ingredients/tomato/delete/preview"},
	{"POST", "/admin/import"}, {"POST", "/admin/changesets/1/apply"},
}

type ctxKey int

const (
	sessionKey ctxKey = iota
	csrfKey
)

func SessionFrom(ctx context.Context) (auth.Session, bool) {
	s, ok := ctx.Value(sessionKey).(auth.Session)
	return s, ok
}

func CSRFToken(ctx context.Context) string {
	s, _ := ctx.Value(csrfKey).(string)
	return s
}

func New(d Deps) http.Handler {
	a := &Admin{d: d, mux: http.NewServeMux()}
	m := a.mux
	m.HandleFunc("GET /admin/login", a.loginPage)
	m.HandleFunc("POST /admin/login/passkey/begin", a.passkeyBegin)
	m.HandleFunc("POST /admin/login/passkey/finish", a.passkeyFinish)
	m.HandleFunc("GET /admin/login/backup", a.backupPage)
	m.HandleFunc("POST /admin/login/backup", a.backupSubmit)
	m.HandleFunc("POST /admin/logout", a.logout)
	m.HandleFunc("GET /admin/enroll", a.enrollPage)
	m.HandleFunc("POST /admin/enroll/begin", a.enrollBegin)
	m.HandleFunc("POST /admin/enroll/finish", a.enrollFinish)

	m.Handle("/admin/", a.requireAuth(http.NotFoundHandler()))
	a.registerSecurity() // Task 7
	a.registerCatalog()

	limits := d.limits
	if limits == nil {
		limits = adminLimits
	}
	var h http.Handler = m
	h = a.csrf(h)
	h = a.loadSession(h)
	h = web.Timeout(adminTimeout)(h)
	h = http.MaxBytesHandler(h, maxBody)
	// The request limiter answers before any body is read or any database
	// call is made, and inside securityHeaders so a 429 carries them too.
	h = web.NewRateLimiterSize(adminLimiterKeys, d.now).Middleware(d.TrustProxy, limits, tooMany)(h)
	h = securityHeaders(h)
	h = web.Recover(d.Log, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Something went wrong. The error was logged.", http.StatusInternalServerError)
	})(h)
	return h
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Robots-Tag", "noindex")
		h.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// loadSession puts a valid session and the right CSRF token in the context.
// Without a session it ensures the pre-session cookie exists.
func (a *Admin) loadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if c, err := r.Cookie(auth.CookieName); err == nil {
			sess, ok, err := a.d.Auth.LookupSession(ctx, c.Value)
			if err != nil {
				a.logStoreError(ctx, "session lookup failed", "err", err, "request_id", web.RequestIDFrom(ctx))
				http.Error(w, "Something went wrong. Try again.", http.StatusServiceUnavailable)
				return
			}
			if ok {
				ctx = context.WithValue(ctx, sessionKey, sess)
				ctx = context.WithValue(ctx, csrfKey, sess.CSRF)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			// Lookup succeeded and found nothing (missing, expired, or
			// already deleted): only then is it safe to tell the browser to
			// stop presenting this cookie.
			auth.ClearSessionCookie(w)
		}
		pre := ""
		if c, err := r.Cookie(PreCookie); err == nil && len(c.Value) >= 32 {
			pre = c.Value
		} else if r.Method == http.MethodGet {
			pre, _ = auth.NewToken()
			http.SetCookie(w, &http.Cookie{Name: PreCookie, Value: pre, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, csrfKey, pre)))
	})
}

func (a *Admin) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		// A known Content-Length above the limit is rejected before any form
		// parsing touches the body. This is the common case (every regular
		// upload sends Content-Length) and it must come first: parsing a
		// huge multipart body just to discover it is too large would waste
		// the read.
		if r.ContentLength > maxBody {
			http.Error(w, "The upload is too large. The limit is 5 MB.", http.StatusRequestEntityTooLarge)
			return
		}
		// Logging out with no live session (it expired, or there never was
		// one) has nothing to protect: a forced logout of nobody is
		// harmless, and answering "Your form expired" to a Sign out click is
		// wrong. Let the logout handler redirect to the login page. This is
		// the only exemption; with a session, logout needs the CSRF token.
		if _, ok := SessionFrom(r.Context()); !ok && r.Method == http.MethodPost && r.URL.Path == "/admin/logout" {
			next.ServeHTTP(w, r)
			return
		}
		// A chunked request has no Content-Length (it reads as -1 above), so
		// the guard above cannot see it coming; the outer
		// http.MaxBytesHandler still caps the actual read at maxBody. Parse
		// the form here, rather than leaving it to PostFormValue below, so a
		// body that trips that cap surfaces as the same 413 instead of a
		// misleading CSRF 403 (X-CSRF-Token is still absent because the
		// handler hasn't run) or a different error from deeper in the
		// handler.
		//
		// maxBody is passed as the in-memory limit too, not just the read
		// cap: the body can never exceed maxBody (the guard above, or
		// http.MaxBytesHandler's cap for a chunked one), so nothing here
		// ever spills to a temp file. RemoveAll below is still called
		// defensively, so a future change to these constants can't leak
		// temp files silently.
		if err := r.ParseMultipartForm(maxBody); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				http.Error(w, "The upload is too large. The limit is 5 MB.", http.StatusRequestEntityTooLarge)
				return
			}
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		want := CSRFToken(r.Context())
		got := r.Header.Get("X-CSRF-Token")
		if got == "" {
			got = r.PostFormValue("csrf")
		}
		if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
			// A signed-out HTMX request whose CSRF token no longer matches
			// (e.g. its session was deleted or expired server-side) must not
			// have this plain-text 403 swapped into a page fragment; redirect
			// it to the login page like requireAuth does for anonymous GETs.
			if _, ok := SessionFrom(r.Context()); !ok && r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/admin/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Error(w, "Your form expired. Reload the page and try again.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth redirects anonymous requests to the login page. HTMX requests
// get HX-Redirect so a login page is never swapped into a fragment.
func (a *Admin) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := SessionFrom(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/admin/login")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Sign in again.", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
	})
}

// render writes a templ component. Rendering uses context.WithoutCancel: templ
// components return ctx.Err() at entry, so a request that has already hit the
// admin timeout would otherwise get a blank body instead of the rendered page.
func (a *Admin) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(context.WithoutCancel(r.Context()), w); err != nil {
		a.d.Log.Warn("admin render failed", "err", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// caller identifies the client for the sign-in budget: its full IP for the
// audit row, and the same key the request limiter uses.
func (a *Admin) caller(r *http.Request) auth.Caller {
	ip := web.ClientIP(r, a.d.TrustProxy)
	return auth.Caller{IP: ip, Key: web.LimiterKey(ip)}
}
