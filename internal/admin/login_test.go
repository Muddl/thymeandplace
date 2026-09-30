package admin

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/web"
)

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t, true)
	// Serve through the site-wide headers, as cmd/server does via web.Common:
	// the admin values must replace the public ones.
	c := e.client(t)
	c.h = web.SecurityHeaders(e.h)
	rec := c.get("/admin/login")
	h := rec.Header()
	const wantCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
	switch {
	case !strings.HasPrefix(h.Get("Permissions-Policy"), "geolocation=(self)"):
		t.Fatalf("Permissions-Policy = %q (site-wide headers missing)", h.Get("Permissions-Policy"))
	case h.Get("Content-Security-Policy") != wantCSP:
		t.Fatalf("Content-Security-Policy = %q, want %q", h.Get("Content-Security-Policy"), wantCSP)
	case h.Get("X-Frame-Options") != "DENY":
		t.Fatalf("X-Frame-Options = %q", h.Get("X-Frame-Options"))
	case h.Get("Referrer-Policy") != "no-referrer":
		t.Fatalf("Referrer-Policy = %q", h.Get("Referrer-Policy"))
	case h.Get("Cache-Control") != "no-store":
		t.Fatalf("Cache-Control = %q", h.Get("Cache-Control"))
	case h.Get("X-Robots-Tag") != "noindex":
		t.Fatalf("X-Robots-Tag = %q", h.Get("X-Robots-Tag"))
	case h.Get("X-Content-Type-Options") != "nosniff":
		t.Fatalf("X-Content-Type-Options = %q", h.Get("X-Content-Type-Options"))
	}
}

func TestEnrollThenPasskeyLogin(t *testing.T) {
	e := newEnv(t, true)
	c, d := e.enrolled(t)
	if rec := c.get("/admin"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Catalog") {
		t.Fatalf("after enroll: %d", rec.Code)
	}
	if rec := c.postForm("/admin/logout", url.Values{}); rec.Code != 303 {
		t.Fatalf("logout %d", rec.Code)
	}
	if rec := c.get("/admin"); rec.Code != 303 {
		t.Fatal("session survived logout")
	}
	c.get("/admin/login")
	if rec := d.login(c, "/admin/login/passkey/begin", "/admin/login/passkey/finish"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"redirect":"/admin"`) {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.get("/admin"); rec.Code != 200 {
		t.Fatal("not signed in after passkey login")
	}
}

// Review Focus 4: racing enrollment finishes register one passkey at most.
func TestEnrollmentLinkSingleUseUnderRace(t *testing.T) {
	e := newEnv(t, true)
	tok, _, _ := e.auth.CreateEnrollmentToken(t.Context())
	var wg sync.WaitGroup
	codes := make([]int, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := e.client(t)
			c.get("/admin/enroll?token=" + tok)
			rec, err := newDevice().register(c, "/admin/enroll/begin?token="+tok, "/admin/enroll/finish?token="+tok)
			errs[i] = err
			if rec != nil {
				codes[i] = rec.Code
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("device %d: %v", i, err)
		}
	}
	pks, _ := e.auth.Passkeys(t.Context())
	if len(pks) != 1 || !((codes[0] == 200) != (codes[1] == 200)) {
		t.Fatalf("codes=%v passkeys=%d", codes, len(pks))
	}
	c := e.client(t)
	if rec := c.get("/admin/enroll?token=" + tok); rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid or has expired") {
		t.Fatalf("used link: %d", rec.Code)
	}
}

func TestEnrollmentLinkExpires(t *testing.T) {
	e := newEnv(t, true)
	tok, _, _ := e.auth.CreateEnrollmentToken(t.Context())
	e.clock.add(16 * time.Minute)
	if rec := e.client(t).get("/admin/enroll?token=" + tok); rec.Code != 400 {
		t.Fatalf("expired link: %d", rec.Code)
	}
}

func TestBackupLoginFlow(t *testing.T) {
	e := newEnv(t, true)
	uri, err := e.auth.SetBackup(t.Context(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(uri)
	secret := u.Query().Get("secret")
	c := e.client(t)
	c.get("/admin/login/backup")
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"nope-nope-nope"}, "code": {"000000"}}); rec.Code != 401 ||
		!strings.Contains(rec.Body.String(), "Password or code is wrong.") {
		t.Fatalf("bad creds: %d", rec.Code)
	}
	cd, _ := totp.GenerateCode(secret, e.clock.now())
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"correct horse battery staple"}, "code": {cd}}); rec.Code != 303 {
		t.Fatalf("good creds: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.get("/admin"); rec.Code != 200 {
		t.Fatal("not signed in via backup")
	}
}

func TestPasskeyWorksDuringBackupLockout(t *testing.T) {
	e := newEnv(t, true)
	_, d := e.enrolled(t)
	_, _ = e.auth.SetBackup(t.Context(), "correct horse battery staple")
	for i := 0; i < 5; i++ {
		_ = e.auth.BackupLogin(t.Context(), "wrong", "000000")
	}
	c := e.client(t)
	c.ip = "192.0.2.50" // separate IP: the lockout is global, the rate limit is per IP
	c.get("/admin/login/backup")
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"x"}, "code": {"000000"}}); !strings.Contains(rec.Body.String(), "Passkey sign-in still works") {
		t.Fatalf("lockout message missing: %s", rec.Body.String())
	}
	c.get("/admin/login")
	if rec := d.login(c, "/admin/login/passkey/begin", "/admin/login/passkey/finish"); rec.Code != 200 {
		t.Fatalf("passkey during lockout: %d", rec.Code)
	}
}

func TestBackupDisabled(t *testing.T) {
	e := newEnv(t, false)
	c := e.client(t)
	if rec := c.get("/admin/login"); strings.Contains(rec.Body.String(), "Use backup login") {
		t.Fatal("backup link shown while disabled")
	}
	if rec := c.get("/admin/login/backup"); rec.Code != 404 {
		t.Fatalf("backup page %d", rec.Code)
	}
}

func TestLoginRateLimitPerIP(t *testing.T) {
	e := newEnv(t, true)
	_, _ = e.auth.SetBackup(t.Context(), "correct horse battery staple")
	c := e.client(t)
	c.get("/admin/login/backup")
	for i := 0; i < 5; i++ {
		c.postForm("/admin/login/backup", url.Values{"password": {"x"}, "code": {"000000"}})
	}
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"x"}, "code": {"000000"}}); rec.Code != 429 {
		t.Fatalf("6th attempt: %d", rec.Code)
	}
	if rec := c.postJSON("/admin/login/passkey/finish", "{}"); rec.Code != 429 {
		t.Fatalf("limit must span all login routes: %d", rec.Code)
	}
}

// Review Focus 5: junk on a finish endpoint is a 4xx JSON error and counts.
func TestCeremonyGarbageIs4xx(t *testing.T) {
	e := newEnv(t, true)
	_, _ = e.enrolled(t)
	c := e.client(t)
	c.ip = "192.0.2.77"
	c.get("/admin/login")
	for _, body := range []string{"not json", strings.Repeat("x", 100<<10), `{"id":1}`} {
		rec := c.postJSON("/admin/login/passkey/finish", body)
		if rec.Code < 400 || rec.Code >= 500 || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Fatalf("body %.10q: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	var n int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_login_log WHERE ip = '192.0.2.77' AND method = 'passkey' AND outcome = 'failure'`).Scan(&n)
	if n != 3 {
		t.Fatalf("garbage attempts logged = %d, want 3", n)
	}
}

func TestUnauthenticatedRedirects(t *testing.T) {
	e := newEnv(t, true)
	c := e.client(t)
	for _, p := range []string{"/admin", "/admin/anything", "/admin/security"} {
		if rec := c.get(p); rec.Code != 303 || rec.Header().Get("Location") != "/admin/login" {
			t.Fatalf("%s: %d %s", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	// Review Focus 3: HTMX requests get HX-Redirect.
	rec := c.do("GET", "/admin", nil, map[string]string{"HX-Request": "true"})
	if rec.Header().Get("HX-Redirect") != "/admin/login" {
		t.Fatalf("HX-Redirect missing: %v", rec.Header())
	}
}

// Review Focus 3 (fix round 1): an HTMX POST whose session was deleted or
// expired server-side must not have the plain-text CSRF-mismatch 403 swapped
// into a page fragment; it gets HX-Redirect to the login page instead, same
// as an anonymous HTMX request against requireAuth.
func TestCSRFExpiredSessionHTMXRedirectsToLogin(t *testing.T) {
	e := newEnv(t, true)
	c, _ := e.enrolled(t)
	oldSession := c.jar[auth.CookieName]
	if oldSession == "" {
		t.Fatal("no session cookie after enrollment")
	}
	if err := e.auth.DeleteSession(t.Context(), oldSession); err != nil {
		t.Fatal(err)
	}
	// c still holds the old cookie and the old (now orphaned) CSRF token.
	rec := c.do("POST", "/admin/security/passkeys/AAAA/rename", strings.NewReader(""), map[string]string{"HX-Request": "true"})
	if rec.Code != 401 || rec.Header().Get("HX-Redirect") != "/admin/login" {
		t.Fatalf("expired session HTMX POST: %d hx-redirect=%q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
}

// Fix round 1, item 4: a login that arrives with an existing __Host-tp_admin
// cookie (still valid, e.g. backup login used while already signed in via
// passkey) must not leave that old session alive alongside the new one.
func TestStartSessionReplacesExistingSession(t *testing.T) {
	e := newEnv(t, true)
	uri, err := e.auth.SetBackup(t.Context(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(uri)
	secret := u.Query().Get("secret")

	c := e.client(t)
	c.get("/admin/login/backup")
	code1, _ := totp.GenerateCode(secret, e.clock.now())
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"correct horse battery staple"}, "code": {code1}}); rec.Code != 303 {
		t.Fatalf("first login: %d", rec.Code)
	}
	oldRaw := c.jar[auth.CookieName]
	if oldRaw == "" {
		t.Fatal("no session cookie set after first login")
	}

	// Second login while the still-valid session cookie from the first login
	// is attached to the request.
	c.get("/admin/login/backup")
	e.clock.add(31 * time.Second) // advance past the last accepted TOTP step
	code2, _ := totp.GenerateCode(secret, e.clock.now())
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"correct horse battery staple"}, "code": {code2}}); rec.Code != 303 {
		t.Fatalf("second login: %d %s", rec.Code, rec.Body.String())
	}
	newRaw := c.jar[auth.CookieName]
	if newRaw == "" || newRaw == oldRaw {
		t.Fatalf("expected a new session value, got old=%q new=%q", oldRaw, newRaw)
	}
	if _, ok, err := e.auth.LookupSession(t.Context(), oldRaw); err != nil || ok {
		t.Fatalf("old session should no longer be valid: ok=%v err=%v", ok, err)
	}
}

func TestCSRFOnEveryWriteRoute(t *testing.T) {
	e := newEnv(t, true)
	signedIn, _ := e.enrolled(t)
	anon := e.client(t)
	anon.get("/admin/login")
	for _, c := range []*client{signedIn, anon} {
		for _, rt := range writeRoutes {
			for _, token := range []string{"", "wrong-token-wrong-token-wrong-token"} {
				saved := c.csrf
				c.csrf = ""
				hdr := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
				if token != "" {
					hdr["X-CSRF-Token"] = token
				}
				rec := c.do(rt.Method, rt.Path, strings.NewReader(""), hdr)
				c.csrf = saved
				if c == anon && rt.Path == "/admin/logout" {
					// Logging out with no session has nothing to protect:
					// it redirects to the login page instead (see
					// TestLogoutWithoutSessionRedirects).
					if rec.Code != 303 || rec.Header().Get("Location") != "/admin/login" {
						t.Fatalf("anonymous logout with token %q: %d", token, rec.Code)
					}
					continue
				}
				if rec.Code != 403 {
					t.Fatalf("%s %s with token %q: %d", rt.Method, rt.Path, token, rec.Code)
				}

				// HTMX variant: a write route hit with HX-Request and no or
				// the wrong CSRF token is rejected the same way (403 with a
				// session), except an anonymous request gets HX-Redirect
				// instead of a plain-text body it can't swap into a
				// fragment (401, or 200 for logout: see (*Admin).logout).
				hdrHX := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "HX-Request": "true"}
				if token != "" {
					hdrHX["X-CSRF-Token"] = token
				}
				saved = c.csrf
				c.csrf = ""
				rec = c.do(rt.Method, rt.Path, strings.NewReader(""), hdrHX)
				c.csrf = saved
				switch {
				case c == anon && rt.Path == "/admin/logout":
					if rec.Code != 200 || rec.Header().Get("HX-Redirect") != "/admin/login" {
						t.Fatalf("anonymous HTMX logout with token %q: %d hx-redirect=%q", token, rec.Code, rec.Header().Get("HX-Redirect"))
					}
				case c == anon:
					if rec.Code != 401 || rec.Header().Get("HX-Redirect") != "/admin/login" {
						t.Fatalf("anonymous HTMX %s %s with token %q: %d hx-redirect=%q", rt.Method, rt.Path, token, rec.Code, rec.Header().Get("HX-Redirect"))
					}
				default:
					if rec.Code != 403 {
						t.Fatalf("signed-in HTMX %s %s with token %q: %d", rt.Method, rt.Path, token, rec.Code)
					}
				}
			}
		}
	}
}

// Final review, critical: an enrollment finish must present the same token
// its begin was issued for. Before the fix, POST /admin/enroll/finish with no
// ?token= took the "signed-in owner" path, registered a passkey from an
// enrollment state and started a session without consuming the token.
func TestEnrollFinishBindsToken(t *testing.T) {
	e := newEnv(t, true)
	tok, _, _ := e.auth.CreateEnrollmentToken(t.Context())
	other, _, _ := e.auth.CreateEnrollmentToken(t.Context())
	passkeys := func() int {
		pks, err := e.auth.Passkeys(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return len(pks)
	}
	stillValid := func(tokens ...string) {
		t.Helper()
		for _, tk := range tokens {
			if ok, err := e.auth.EnrollmentValid(t.Context(), tk); err != nil || !ok {
				t.Fatalf("token consumed by a rejected finish: ok=%v err=%v", ok, err)
			}
		}
	}

	for _, finish := range []string{"/admin/enroll/finish", "/admin/enroll/finish?token=", "/admin/enroll/finish?token=" + other} {
		c := e.client(t)
		c.get("/admin/enroll?token=" + tok)
		rec, err := newDevice().register(c, "/admin/enroll/begin?token="+tok, finish)
		if err != nil || rec.Code != 400 {
			t.Fatalf("%s: %d %s (err=%v)", finish, rec.Code, rec.Body.String(), err)
		}
		if c.jar[auth.CookieName] != "" {
			t.Fatalf("%s: a session was started", finish)
		}
	}
	if n := passkeys(); n != 0 {
		t.Fatalf("rejected finishes stored %d passkeys", n)
	}
	stillValid(tok, other)

	// The original token still works.
	owner := e.client(t)
	owner.get("/admin/enroll?token=" + tok)
	if rec, err := newDevice().register(owner, "/admin/enroll/begin?token="+tok, "/admin/enroll/finish?token="+tok+"&name=Phone"); err != nil || rec.Code != 200 {
		t.Fatalf("original token: %d (err=%v)", rec.Code, err)
	}
	owner.get("/admin")

	// A signed-in owner's "reg" state cannot finish an enrollment, and an
	// "enroll" state cannot finish the owner's add-passkey route.
	third, _, _ := e.auth.CreateEnrollmentToken(t.Context())
	if rec, err := newDevice().register(owner, "/admin/security/passkeys/begin", "/admin/enroll/finish?token="+third); err != nil || rec.Code != 400 {
		t.Fatalf("reg state at enroll finish: %d %s (err=%v)", rec.Code, rec.Body.String(), err)
	}
	if rec, err := newDevice().register(owner, "/admin/enroll/begin?token="+third, "/admin/security/passkeys/finish?name=Key"); err != nil || rec.Code != 400 {
		t.Fatalf("enroll state at add finish: %d %s (err=%v)", rec.Code, rec.Body.String(), err)
	}
	if n := passkeys(); n != 1 {
		t.Fatalf("want 1 passkey, got %d", n)
	}
	stillValid(third, other)
}

// Final review item 2: a database error while checking an enrollment link is
// a 503 (logged with the request ID), never "invalid or expired link".
func TestEnrollDBErrorIs503(t *testing.T) {
	e := newBrokenEnv(t)
	c := e.client(t)
	if rec := c.get("/admin/enroll?token=abc"); rec.Code != 503 || strings.Contains(rec.Body.String(), "invalid or has expired") {
		t.Fatalf("enroll page: %d %s", rec.Code, rec.Body.String())
	}
	c.csrf = c.jar[PreCookie]
	if rec := c.postJSON("/admin/enroll/begin?token=abc", "{}"); rec.Code != 503 || strings.Contains(rec.Body.String(), "invalid or has expired") {
		t.Fatalf("enroll begin: %d %s", rec.Code, rec.Body.String())
	}
	logs := e.logs.String()
	for _, want := range []string{"enrollment check failed", "enrollment begin failed"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("log missing %q:\n%s", want, logs)
		}
	}
	if n := strings.Count(logs, "request_id="+testRequestID); n < 2 {
		t.Fatalf("request_id logged %d times:\n%s", n, logs)
	}
}

// Final review item 4: an unexpected backup-login error is audited as
// "error" and logged with the request ID.
func TestBackupLoginErrorIsAudited(t *testing.T) {
	e := newEnv(t, true)
	if _, err := e.auth.SetBackup(t.Context(), "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `UPDATE admin_backup SET password_hash = 'not-a-phc-string'`); err != nil {
		t.Fatal(err)
	}
	c := e.client(t)
	c.ip = "192.0.2.91"
	c.get("/admin/login/backup")
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"correct horse battery staple"}, "code": {"000000"}}); rec.Code != 500 {
		t.Fatalf("backup error: %d", rec.Code)
	}
	var n int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_login_log WHERE ip = '192.0.2.91' AND method = 'backup' AND outcome = 'error'`).Scan(&n)
	if n != 1 {
		t.Fatalf("backup error audited %d times, want 1", n)
	}
	if logs := e.logs.String(); !strings.Contains(logs, "backup login error") || !strings.Contains(logs, "request_id="+testRequestID) {
		t.Fatalf("backup error not logged with request_id:\n%s", logs)
	}
}

// Final review item 2: a failure to create the session after a successful
// login is logged with the request ID.
func TestStartSessionFailureIsLogged(t *testing.T) {
	e := newEnv(t, true)
	uri, err := e.auth.SetBackup(t.Context(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(uri)
	if _, err := e.pool.Exec(t.Context(), `ALTER TABLE admin_session ADD CONSTRAINT test_no_sessions CHECK (method = 'none')`); err != nil {
		t.Fatal(err)
	}
	c := e.client(t)
	c.get("/admin/login/backup")
	cd, _ := totp.GenerateCode(u.Query().Get("secret"), e.clock.now())
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"correct horse battery staple"}, "code": {cd}}); rec.Code != 500 {
		t.Fatalf("session insert failure: %d", rec.Code)
	}
	if logs := e.logs.String(); !strings.Contains(logs, "session create failed") || !strings.Contains(logs, "request_id="+testRequestID) {
		t.Fatalf("session create failure not logged with request_id:\n%s", logs)
	}
}

// Final review item 5: POST /admin/logout whose session has expired (or that
// never had one) redirects to the login page, as a plain form post or HTMX,
// and never answers with the CSRF 403 "Your form expired".
func TestLogoutWithoutSessionRedirects(t *testing.T) {
	e := newEnv(t, true)
	check := func(name string, c *client, htmx bool) {
		t.Helper()
		hdr := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
		if htmx {
			hdr["HX-Request"] = "true"
		}
		rec := c.do("POST", "/admin/logout", strings.NewReader(url.Values{"csrf": {c.csrf}}.Encode()), hdr)
		switch {
		case rec.Code == 403:
			t.Fatalf("%s: CSRF 403: %s", name, rec.Body.String())
		case htmx && rec.Header().Get("HX-Redirect") != "/admin/login":
			t.Fatalf("%s: HX-Redirect = %q (%d)", name, rec.Header().Get("HX-Redirect"), rec.Code)
		case !htmx && (rec.Code != 303 || rec.Header().Get("Location") != "/admin/login"):
			t.Fatalf("%s: %d %q", name, rec.Code, rec.Header().Get("Location"))
		}
	}
	for _, htmx := range []bool{false, true} {
		expired, _ := e.enrolled(t)
		e.clock.add(31 * time.Minute) // idle timeout: the session is gone
		check("expired session", expired, htmx)

		anon := e.client(t)
		check("no cookies at all", anon, htmx)
	}

	// With a live session, CSRF still applies to logout.
	signedIn, _ := e.enrolled(t)
	signedIn.csrf = "wrong-token-wrong-token-wrong-token"
	if rec := signedIn.postForm("/admin/logout", url.Values{}); rec.Code != 403 {
		t.Fatalf("signed-in logout with a bad CSRF token: %d", rec.Code)
	}
	if rec := signedIn.get("/admin"); rec.Code != 200 {
		t.Fatalf("session ended by a CSRF-failed logout: %d", rec.Code)
	}
}

// Final review item 8: when the session lookup itself fails (database
// down), loadSession answers 503 and must not clear the session cookie: the
// session may well still be valid once the database is back.
func TestLoadSessionDBErrorIs503KeepsCookie(t *testing.T) {
	e := newBrokenEnv(t)
	c := e.client(t)
	c.jar[auth.CookieName] = "a-session-token-the-db-cannot-check"
	for _, path := range []string{"/admin", "/admin/login"} {
		rec := c.get(path)
		if rec.Code != 503 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		for _, ck := range rec.Result().Cookies() {
			if ck.Name == auth.CookieName {
				t.Fatalf("%s: session cookie touched on a lookup error: %+v", path, ck)
			}
		}
		if c.jar[auth.CookieName] == "" {
			t.Fatalf("%s: session cookie dropped", path)
		}
	}
	if logs := e.logs.String(); !strings.Contains(logs, "session lookup failed") || !strings.Contains(logs, "request_id="+testRequestID) {
		t.Fatalf("lookup failure not logged with request_id:\n%s", logs)
	}
}

// An anonymous caller must not be able to cause an ERROR (which pages the
// owner): an oversized enrollment body is a client error.
func TestEnrollFinishOversizeBodyIsNotError(t *testing.T) {
	e := newEnv(t, false)
	c := e.client(t)
	c.get("/admin/login") // sets the CSRF cookie the POST must carry
	rec := c.postJSON("/admin/enroll/finish?token=x", `{"x":"`+strings.Repeat("a", 70<<10)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(e.logs.String(), "level=ERROR") {
		t.Fatalf("client error logged at ERROR:\n%s", e.logs.String())
	}
}
