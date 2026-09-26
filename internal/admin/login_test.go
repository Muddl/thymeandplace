package admin

import (
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t, true)
	rec := e.client(t).get("/admin/login")
	h := rec.Header()
	if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'self'") || h.Get("X-Frame-Options") != "DENY" ||
		h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cache-Control") != "no-store" || h.Get("X-Robots-Tag") != "noindex" {
		t.Fatalf("headers = %v", h)
	}
}

func TestEnrollThenPasskeyLogin(t *testing.T) {
	e := newEnv(t, true)
	c, d := e.enrolled(t)
	if rec := c.get("/admin"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Signed in") {
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
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := e.client(t)
			c.get("/admin/enroll?token=" + tok)
			codes[i] = newDevice().register(c, "/admin/enroll/begin?token="+tok, "/admin/enroll/finish?token="+tok).Code
		}(i)
	}
	wg.Wait()
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
				if rec.Code != 403 {
					t.Fatalf("%s %s with token %q: %d", rt.Method, rt.Path, token, rec.Code)
				}
			}
		}
	}
}
