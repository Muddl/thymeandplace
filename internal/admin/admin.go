// Package admin serves /admin for the single owner.
package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"

	"thymeandplace/internal/auth"
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
	// Plan 7 adds the catalog write routes here.
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

	m.Handle("GET /admin", a.requireAuth(http.HandlerFunc(a.home)))
	m.Handle("/admin/", a.requireAuth(http.NotFoundHandler()))
	a.registerSecurity() // Task 7
	a.registerCatalog()  // Plan 7; a no-op until then

	var h http.Handler = m
	h = a.csrf(h)
	h = a.loadSession(h)
	h = web.Timeout(adminTimeout)(h)
	h = http.MaxBytesHandler(h, maxBody)
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
				a.d.Log.Error("session lookup failed", "err", err, "request_id", web.RequestIDFrom(ctx))
			}
			if ok {
				ctx = context.WithValue(ctx, sessionKey, sess)
				ctx = context.WithValue(ctx, csrfKey, sess.CSRF)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
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
		want := CSRFToken(r.Context())
		got := r.Header.Get("X-CSRF-Token")
		if got == "" {
			got = r.PostFormValue("csrf")
		}
		if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
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

func (a *Admin) ip(r *http.Request) string { return web.ClientIP(r, a.d.TrustProxy) }

// registerSecurity is implemented in Task 7.
func (a *Admin) registerSecurity() {}

// registerCatalog is replaced in Plan 7.
func (a *Admin) registerCatalog() {}
