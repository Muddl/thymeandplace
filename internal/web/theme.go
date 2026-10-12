package web

import "net/http"

// Design exploration only (branch design/explore): ?theme=a|b|c|c2 picks one of
// the candidate stylesheets under static/design and remembers it in a cookie
// so htmx navigation and reloads keep it; ?theme=off clears it.
const themeCookie = "tp_theme"

func validTheme(v string) bool { return v == "a" || v == "b" || v == "c" || v == "c2" }

// themeFor returns the theme for a full-page response, and refreshes the
// cookie when ?theme= was given. It must run before the status is written.
func themeFor(w http.ResponseWriter, r *http.Request) string {
	w.Header().Add("Vary", "Cookie")
	if v := r.URL.Query().Get("theme"); v != "" {
		c := &http.Cookie{Name: themeCookie, Path: "/", SameSite: http.SameSiteLaxMode, HttpOnly: true}
		if validTheme(v) {
			c.Value, c.MaxAge = v, 86400
			http.SetCookie(w, c)
			return v
		}
		c.MaxAge = -1
		http.SetCookie(w, c)
		return ""
	}
	if c, err := r.Cookie(themeCookie); err == nil && validTheme(c.Value) {
		return c.Value
	}
	return ""
}
