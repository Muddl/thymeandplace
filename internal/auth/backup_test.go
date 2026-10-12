package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

const pw = "correct horse battery staple"

func setup(t *testing.T) (*Service, *clock, string) {
	t.Helper()
	s, c := newTestService(t, true)
	uri, err := s.SetBackup(context.Background(), pw)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(uri)
	if u.Scheme != "otpauth" || u.Host != "totp" {
		t.Fatalf("uri = %q", uri)
	}
	return s, c, u.Query().Get("secret")
}

func code(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestBackupLoginSuccessAndFailures(t *testing.T) {
	ctx := context.Background()
	s, c, secret := setup(t)
	if err := s.BackupLogin(ctx, "wrong password", code(t, secret, c.now())); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := s.BackupLogin(ctx, pw, "000000"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong code: %v", err)
	}
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); err != nil {
		t.Fatalf("right credentials: %v", err)
	}
}

// Review Focus 2: a code cannot be used twice, even within its window.
func TestBackupRejectsReplayedCode(t *testing.T) {
	ctx := context.Background()
	s, c, secret := setup(t)
	cd := code(t, secret, c.now())
	if err := s.BackupLogin(ctx, pw, cd); err != nil {
		t.Fatal(err)
	}
	c.add(10 * time.Second)
	if err := s.BackupLogin(ctx, pw, cd); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("replayed code accepted: %v", err)
	}
	// The previous-step code (skew) is also spent once a later step was used.
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now().Add(-30*time.Second))); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("older step accepted: %v", err)
	}
	c.add(30 * time.Second)
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); err != nil {
		t.Fatalf("next step rejected: %v", err)
	}
}

func TestBackupLockoutAndBackoff(t *testing.T) {
	ctx := context.Background()
	s, c, secret := setup(t)
	fail5 := func() {
		for i := 0; i < LockoutFailures; i++ {
			_ = s.BackupLogin(ctx, "nope", "000000")
		}
	}
	fail5()
	var le *LockedError
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); !errors.As(err, &le) || !le.Until.Equal(c.now().Add(15*time.Minute)) {
		t.Fatalf("first lock: %v", err)
	}
	c.add(15*time.Minute + time.Second)
	fail5() // second lockout doubles
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); !errors.As(err, &le) || !le.Until.Equal(c.now().Add(30*time.Minute)) {
		t.Fatalf("second lock: %v", err)
	}
	for i := 0; i < 10; i++ { // many more lockouts: capped at 24h
		c.add(25 * time.Hour)
		fail5()
	}
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); !errors.As(err, &le) || le.Until.Sub(c.now()) != 24*time.Hour {
		t.Fatalf("cap: %v", err)
	}
	c.add(24*time.Hour + time.Second)
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); err != nil {
		t.Fatalf("after lock expiry: %v", err)
	}
	// Success reset the level: the next lockout is 15 minutes again.
	c.add(time.Minute)
	fail5()
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); !errors.As(err, &le) || le.Until.Sub(c.now()) != 15*time.Minute {
		t.Fatalf("reset: %v", err)
	}
}

func TestBackupDisabledAndNotSet(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, false)
	if err := s.BackupLogin(ctx, pw, "123456"); !errors.Is(err, ErrBackupDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	s2, _ := newTestService(t, true)
	if err := s2.BackupLogin(ctx, pw, "123456"); !errors.Is(err, ErrBackupNotSet) {
		t.Fatalf("not set: %v", err)
	}
	if _, err := s2.SetBackup(ctx, "short"); err == nil {
		t.Fatal("short password accepted")
	}
}

// v4 is the Caller for an IPv4 address, keyed by the address itself.
func v4(ip string) Caller { return Caller{IP: ip, Key: ip} }

func TestAttemptRateLimit(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	for i := 0; i < AttemptLimit; i++ {
		if ok, _ := s.AllowAttempt(ctx, v4("203.0.113.5"), "backup"); !ok {
			t.Fatalf("attempt %d refused", i)
		}
		s.LogAttempt(ctx, v4("203.0.113.5"), "backup", "failure")
	}
	if ok, _ := s.AllowAttempt(ctx, v4("203.0.113.5"), "backup"); ok {
		t.Fatal("6th attempt within 15 minutes allowed")
	}
	s.LogRefusal(ctx, v4("203.0.113.5"), "backup")
	if ok, _ := s.AllowAttempt(ctx, v4("198.51.100.1"), "backup"); !ok {
		t.Fatal("other IP limited")
	}
	c.add(AttemptWindow + time.Second)
	if ok, _ := s.AllowAttempt(ctx, v4("203.0.113.5"), "backup"); !ok {
		t.Fatal("window did not slide (rate_limited rows must not count)")
	}
}

// The budget counts by client key, so an IPv6 client rotating addresses in
// its /64 shares one budget, while the audit row keeps the full address.
func TestAttemptBudgetByClientKey(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	const key = "2001:db8:1:2::/64"
	for i := 1; i <= AttemptLimit; i++ {
		s.LogAttempt(ctx, Caller{IP: fmt.Sprintf("2001:db8:1:2::%d", i), Key: key}, "passkey", "failure")
	}
	if ok, err := s.AllowAttempt(ctx, Caller{IP: "2001:db8:1:2::99", Key: key}, "passkey"); err != nil || ok {
		t.Fatalf("another address in the same /64 must share the budget: ok=%v err=%v", ok, err)
	}
	if ok, err := s.AllowAttempt(ctx, Caller{IP: "2001:db8:1:3::1", Key: "2001:db8:1:3::/64"}, "passkey"); err != nil || !ok {
		t.Fatalf("another /64 has its own budget: ok=%v err=%v", ok, err)
	}
	var n int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM admin_login_log WHERE ip_key = $1 AND ip LIKE '2001:db8:1:2::%'`, key).Scan(&n)
	if n != AttemptLimit {
		t.Fatalf("stored rows must keep their full ip: %d", n)
	}
	// Login failures leave the enroll budget alone, and the other way round.
	if ok, _ := s.AllowAttempt(ctx, Caller{IP: "2001:db8:1:2::99", Key: key}, "enroll"); !ok {
		t.Fatal("login failures must not use up the enroll budget")
	}
	for i := 0; i < AttemptLimit; i++ {
		s.LogAttempt(ctx, v4("192.0.2.30"), "enroll", "failure")
	}
	if ok, _ := s.AllowAttempt(ctx, v4("192.0.2.30"), "enroll"); ok {
		t.Fatal("enroll failures must use up the enroll budget")
	}
	if ok, _ := s.AllowAttempt(ctx, v4("192.0.2.30"), "passkey"); !ok {
		t.Fatal("enroll failures must not use up the login budget")
	}
}

// Refused attempts write one row per key and budget per window, and one WARN.
func TestLogRefusalOncePerWindow(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	var logs bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	count := func() int {
		var n int
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM admin_login_log WHERE ip_key = '203.0.113.9' AND outcome = 'rate_limited'`).Scan(&n)
		return n
	}
	for i := 0; i < 50; i++ {
		s.LogRefusal(ctx, v4("203.0.113.9"), "passkey")
	}
	if n, w := count(), strings.Count(logs.String(), "admin sign-in budget reached"); n != 1 || w != 1 {
		t.Fatalf("50 refusals in one window: %d rows, %d WARN lines; want 1 and 1", n, w)
	}
	s.LogRefusal(ctx, v4("203.0.113.9"), "enroll") // another budget
	c.add(AttemptWindow + time.Second)
	s.LogRefusal(ctx, v4("203.0.113.9"), "backup") // same login budget, next window
	if n := count(); n != 3 {
		t.Fatalf("rows after a second budget and a new window: %d, want 3", n)
	}
}

// A binary from before the ip_key column (after a rollback) still inserts.
func TestLoginLogInsertWithoutIPKey(t *testing.T) {
	s, _ := newTestService(t, true)
	if _, err := s.pool.Exec(context.Background(), `INSERT INTO admin_login_log (at, ip, method, outcome) VALUES (now(), '192.0.2.1', 'passkey', 'failure')`); err != nil {
		t.Fatalf("insert without ip_key: %v", err)
	}
}

// TestAttemptRateLimitCountsOnlyLoginFailures verifies controller ruling E:
// the limit tracks failed login attempts (passkey/backup) only. Successful
// logins and enrollment outcomes (any outcome, since enroll isn't a login
// method) must not count, while AttemptLimit login failures still block.
func TestAttemptRateLimitCountsOnlyLoginFailures(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	ip := "203.0.113.20"

	for i := 0; i < 10; i++ {
		s.LogAttempt(ctx, v4(ip), "passkey", "success")
		s.LogAttempt(ctx, v4(ip), "backup", "success")
		s.LogAttempt(ctx, v4(ip), "enroll", "success")
		s.LogAttempt(ctx, v4(ip), "enroll", "failure")
	}
	if ok, err := s.AllowAttempt(ctx, v4(ip), "passkey"); err != nil || !ok {
		t.Fatalf("successes and enroll outcomes must not count: ok=%v err=%v", ok, err)
	}

	for i := 0; i < AttemptLimit; i++ {
		method := "passkey"
		if i%2 == 1 {
			method = "backup"
		}
		s.LogAttempt(ctx, v4(ip), method, "failure")
	}
	if ok, err := s.AllowAttempt(ctx, v4(ip), "backup"); err != nil || ok {
		t.Fatalf("%d login failures should block the next attempt: ok=%v err=%v", AttemptLimit, ok, err)
	}
}

// Monitoring hook: backup login use and a new lockout fire events; plain
// failures, attempts while locked and passkey outcomes don't. A new lockout
// also logs exactly one WARN "backup login locked" (spec §4.3).
func TestSecurityEvents(t *testing.T) {
	ctx := context.Background()
	s, c, secret := setup(t)
	var events []string
	s.onEvent = func(k string) { events = append(events, k) }
	var logs bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	const lockedLine = `level=WARN msg="backup login locked"`

	s.LogAttempt(ctx, v4("192.0.2.1"), "passkey", "success")
	s.LogAttempt(ctx, v4("192.0.2.1"), "passkey", "failure")
	s.LogAttempt(ctx, v4("192.0.2.1"), "backup", "failure")
	if len(events) != 0 {
		t.Fatalf("no event expected yet: %v", events)
	}
	s.LogAttempt(ctx, v4("192.0.2.1"), "backup", "success")
	if len(events) != 1 || events[0] != "backup_login" {
		t.Fatalf("backup login: %v", events)
	}
	events = nil
	for i := 0; i < LockoutFailures-1; i++ {
		_ = s.BackupLogin(ctx, "nope", "000000")
	}
	if len(events) != 0 {
		t.Fatalf("failures before the lockout: %v", events)
	}
	if n := strings.Count(logs.String(), lockedLine); n != 0 {
		t.Fatalf("lock WARN logged %d times before the lockout:\n%s", n, logs.String())
	}
	_ = s.BackupLogin(ctx, "nope", "000000")
	if len(events) != 1 || events[0] != "backup_lockout" {
		t.Fatalf("lockout: %v", events)
	}
	if n := strings.Count(logs.String(), lockedLine); n != 1 {
		t.Fatalf("lock WARN logged %d times at the lockout, want 1:\n%s", n, logs.String())
	}
	_ = s.BackupLogin(ctx, pw, code(t, secret, c.now())) // refused while locked
	if len(events) != 1 {
		t.Fatalf("an attempt while locked must not fire: %v", events)
	}
	if n := strings.Count(logs.String(), lockedLine); n != 1 {
		t.Fatalf("lock WARN logged %d times after an attempt while locked, want 1:\n%s", n, logs.String())
	}
}

// Sign-in routes are open to anonymous callers, and ERROR pages the owner: an
// audit write that fails because the request was cancelled (the client went
// away) is a WARN. A real store failure stays ERROR.
func TestAuditWriteFailureOnCancelIsWarn(t *testing.T) {
	s, _ := newTestService(t, true)
	var logs bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.LogAttempt(cctx, v4("192.0.2.30"), "passkey", "failure")
	s.LogRefusal(cctx, v4("192.0.2.30"), "passkey")
	out := logs.String()
	if strings.Contains(out, "level=ERROR") {
		t.Fatalf("cancelled audit write logged at ERROR:\n%s", out)
	}
	if n := strings.Count(out, `level=WARN msg="login audit write failed"`); n != 2 {
		t.Fatalf("cancelled audit writes logged %d WARNs, want 2:\n%s", n, out)
	}
}
