package admin

import (
	"bytes"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/dbtest"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

type env struct {
	h     http.Handler
	auth  *auth.Service
	clock *clock
	pool  *pgxpool.Pool
}

func newEnv(t *testing.T, backup bool) *env {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
	pool := dbtest.New(t)
	svc, err := auth.NewService(pool, auth.Config{
		RPID: "localhost", RPOrigins: []string{"https://localhost"},
		SecretKey: bytes.Repeat([]byte{3}, 32), BackupEnabled: backup, Now: c.now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &env{h: New(Deps{Auth: svc, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}), auth: svc, clock: c, pool: pool}
}

var csrfRE = regexp.MustCompile(`name="csrf-token" content="([^"]*)"`)

// client keeps cookies and the latest CSRF token like a browser tab.
type client struct {
	t    *testing.T
	h    http.Handler
	jar  map[string]string
	csrf string
	ip   string
}

func (e *env) client(t *testing.T) *client {
	return &client{t: t, h: e.h, jar: map[string]string{}, ip: "198.51.100.7"}
}

func (c *client) do(method, path string, body io.Reader, hdr map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	r := httptest.NewRequest(method, path, body)
	r.RemoteAddr = c.ip + ":1"
	for k, v := range c.jar {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	if c.csrf != "" && method != "GET" {
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 || ck.Value == "" {
			delete(c.jar, ck.Name)
		} else {
			c.jar[ck.Name] = ck.Value
		}
	}
	if m := csrfRE.FindStringSubmatch(rec.Body.String()); m != nil {
		c.csrf = html.UnescapeString(m[1])
	}
	return rec
}

func (c *client) get(path string) *httptest.ResponseRecorder { return c.do("GET", path, nil, nil) }

func (c *client) postForm(path string, form url.Values) *httptest.ResponseRecorder {
	return c.do("POST", path, strings.NewReader(form.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
}

func (c *client) postJSON(path, body string) *httptest.ResponseRecorder {
	return c.do("POST", path, strings.NewReader(body), map[string]string{"Content-Type": "application/json"})
}

type device struct {
	rp   virtualwebauthn.RelyingParty
	auth virtualwebauthn.Authenticator
	cred virtualwebauthn.Credential
}

func newDevice() *device {
	return &device{
		rp:   virtualwebauthn.RelyingParty{Name: "Thyme & Place", ID: "localhost", Origin: "https://localhost"},
		auth: virtualwebauthn.NewAuthenticator(),
		cred: virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2),
	}
}

// register returns an error instead of calling t.Fatal so it is safe to call
// from a goroutine (e.g. the enrollment race test): t.FailNow/t.Fatal must
// only ever be called from the goroutine running the test itself.
func (d *device) register(c *client, begin, finish string) (*httptest.ResponseRecorder, error) {
	rec := c.postJSON(begin, "{}")
	if rec.Code != 200 {
		return rec, nil
	}
	opts, err := virtualwebauthn.ParseAttestationOptions(rec.Body.String())
	if err != nil {
		return nil, err
	}
	out := c.postJSON(finish, virtualwebauthn.CreateAttestationResponse(d.rp, d.auth, d.cred, *opts))
	if out.Code == 200 {
		d.auth.AddCredential(d.cred)
	}
	return out, nil
}

func (d *device) login(c *client, begin, finish string) *httptest.ResponseRecorder {
	c.t.Helper()
	rec := c.postJSON(begin, "{}")
	if rec.Code != 200 {
		return rec
	}
	opts, err := virtualwebauthn.ParseAssertionOptions(rec.Body.String())
	if err != nil {
		c.t.Fatal(err)
	}
	return c.postJSON(finish, virtualwebauthn.CreateAssertionResponse(d.rp, d.auth, d.cred, *opts))
}

// enrolled returns a signed-in client and the device it registered.
func (e *env) enrolled(t *testing.T) (*client, *device) {
	t.Helper()
	tok, _, err := e.auth.CreateEnrollmentToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c := e.client(t)
	if rec := c.get("/admin/enroll?token=" + tok); rec.Code != 200 {
		t.Fatalf("enroll page %d", rec.Code)
	}
	d := newDevice()
	rec, err := d.register(c, "/admin/enroll/begin?token="+tok, "/admin/enroll/finish?token="+tok+"&name=Phone")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 {
		t.Fatalf("enroll finish %d %s", rec.Code, rec.Body.String())
	}
	c.get("/admin") // pick up the session CSRF token
	return c, d
}
