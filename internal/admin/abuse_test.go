package admin

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/dbtest"
)

// rowsFor counts admin_login_log rows for a client key, split by budget.
func rowsFor(t *testing.T, e *env, key string) (login, enroll int) {
	t.Helper()
	err := e.pool.QueryRow(t.Context(), `SELECT
		count(*) FILTER (WHERE method IN ('passkey', 'backup')),
		count(*) FILTER (WHERE method = 'enroll')
		FROM admin_login_log WHERE ip_key = $1`, key).Scan(&login, &enroll)
	if err != nil {
		t.Fatal(err)
	}
	return login, enroll
}

// Unauthenticated attempts write at most AttemptLimit failures plus one
// refusal per budget per window, whatever the route.
func TestAuditRowsBoundedPerClient(t *testing.T) {
	e := newEnv(t, true)
	if _, err := e.auth.SetBackup(t.Context(), "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	c := e.client(t)
	c.get("/admin/login")
	for i := 0; i < 30; i++ {
		c.postForm("/admin/login/backup", url.Values{"password": {"x"}, "code": {"000000"}})
		c.postJSON("/admin/login/passkey/finish", "{}")
		c.postJSON("/admin/enroll/finish?token=x", "{}")
	}
	bound := auth.AttemptLimit + 1 // failures plus one refusal
	if login, enroll := rowsFor(t, e, c.ip); login > bound || enroll > bound {
		t.Fatalf("90 attempts wrote %d login and %d enroll rows; want at most %d each", login, enroll, bound)
	}
	if rec := c.postJSON("/admin/enroll/finish?token=x", "{}"); rec.Code != 429 {
		t.Fatalf("enroll finish over budget: %d", rec.Code)
	}
}

// With backup login off, the route stays a plain 404, and its audit rows are
// bounded too.
func TestDisabledBackupBoundedAndHidden(t *testing.T) {
	e := newEnv(t, false)
	c := e.client(t)
	c.get("/admin/login")
	for i := 0; i < 30; i++ {
		if rec := c.postForm("/admin/login/backup", url.Values{"password": {"x"}, "code": {"000000"}}); rec.Code != 404 {
			t.Fatalf("disabled backup attempt %d: %d", i, rec.Code)
		}
	}
	if login, _ := rowsFor(t, e, c.ip); login > auth.AttemptLimit+1 {
		t.Fatalf("30 disabled-backup attempts wrote %d rows; want at most %d", login, auth.AttemptLimit+1)
	}
}

// Addresses in one IPv6 /64 share a budget.
func TestLoginBudgetPerIPv6Prefix(t *testing.T) {
	e := newEnv(t, true)
	c := e.client(t)
	c.get("/admin/login")
	for i := 1; i <= auth.AttemptLimit; i++ {
		c.ip = fmt.Sprintf("[2001:db8:1:2::%d]", i)
		if rec := c.postJSON("/admin/login/passkey/finish", "{}"); rec.Code != 401 {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	c.ip = "[2001:db8:1:2::ffff]"
	if rec := c.postJSON("/admin/login/passkey/finish", "{}"); rec.Code != 429 {
		t.Fatalf("6th attempt from the same /64: %d", rec.Code)
	}
	c.ip = "[2001:db8:1:3::1]"
	if rec := c.postJSON("/admin/login/passkey/finish", "{}"); rec.Code != 401 {
		t.Fatalf("another /64: %d", rec.Code)
	}
}

// If the budget can't be checked, the answer is 503 and nothing is written.
func TestBudgetCheckFailureIs503(t *testing.T) {
	e := newBrokenEnv(t)
	c := e.client(t)
	c.get("/admin/login")
	for _, rec := range []struct {
		name string
		code int
	}{
		{"passkey finish", c.postJSON("/admin/login/passkey/finish", "{}").Code},
		{"backup", c.postForm("/admin/login/backup", url.Values{"password": {"x"}, "code": {"000000"}}).Code},
		{"enroll finish", c.postJSON("/admin/enroll/finish?token=x", "{}").Code},
	} {
		if rec.code != 503 {
			t.Errorf("%s with a broken database: %d, want 503", rec.name, rec.code)
		}
	}
}

func TestAdminRequestLimiter(t *testing.T) {
	e := newLimitedEnv(t, dbtest.New(t))
	c := e.client(t)
	for i := 0; i < 20; i++ {
		if rec := c.get("/admin/login"); rec.Code != 200 {
			t.Fatalf("request %d: %d", i+1, rec.Code)
		}
	}
	rec := c.get("/admin/login")
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("21st rapid sign-in request: %d %v", rec.Code, rec.Header())
	}
	other := e.client(t)
	other.ip = "198.51.100.8"
	if rec := other.get("/admin/login"); rec.Code != 200 {
		t.Fatalf("another IPv4 address: %d", rec.Code)
	}
	v6 := e.client(t)
	for i := 0; i < 20; i++ {
		v6.ip = fmt.Sprintf("[2001:db8:5:6::%d]", i+1)
		v6.get("/admin/login")
	}
	v6.ip = "[2001:db8:5:6::abc]"
	if rec := v6.get("/admin/login"); rec.Code != 429 {
		t.Fatalf("the same /64 shares a bucket: %d", rec.Code)
	}
	v6.ip = "[2001:db8:5:7::1]"
	if rec := v6.get("/admin/login"); rec.Code != 200 {
		t.Fatalf("another /64: %d", rec.Code)
	}
	e.clock.add(time.Second)
	if rec := c.get("/admin/login"); rec.Code != 200 {
		t.Fatalf("after a second: %d", rec.Code)
	}
}

// The limiter answers before any database work: on a broken pool, requests
// with a session cookie fail in loadSession until the limiter takes over.
func TestAdminRequestLimiterRunsBeforeDB(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/nothing")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	e := newLimitedEnv(t, pool)
	c := e.client(t)
	c.jar[auth.CookieName] = "some-session"
	for i := 0; i < 100; i++ {
		if rec := c.get("/admin"); rec.Code != 503 {
			t.Fatalf("request %d: %d, want loadSession's 503", i+1, rec.Code)
		}
	}
	if rec := c.get("/admin"); rec.Code != 429 {
		t.Fatalf("101st request: %d, want 429 from the limiter", rec.Code)
	}
}

// The owner's own use never hits the limiter.
func TestAdminRequestLimiterSparesTheOwner(t *testing.T) {
	e := newLimitedEnv(t, dbtest.New(t))
	c, _ := e.enrolled(t)
	for i := 0; i < 40; i++ {
		e.clock.add(250 * time.Millisecond)
		rec := c.do("GET", "/admin?q=tom", nil, map[string]string{"HX-Request": "true"})
		if rec.Code == 429 {
			t.Fatalf("catalog search %d hit the limiter", i+1)
		}
		if rec.Code != 200 || strings.Contains(rec.Body.String(), "Too many requests") {
			t.Fatalf("catalog search %d: %d", i+1, rec.Code)
		}
	}
}
