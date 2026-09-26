package auth

import (
	"context"
	"errors"
	"net/url"
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

func TestAttemptRateLimit(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	for i := 0; i < AttemptLimit; i++ {
		if ok, _ := s.AllowAttempt(ctx, "203.0.113.5"); !ok {
			t.Fatalf("attempt %d refused", i)
		}
		s.LogAttempt(ctx, "203.0.113.5", "backup", "failure")
	}
	if ok, _ := s.AllowAttempt(ctx, "203.0.113.5"); ok {
		t.Fatal("6th attempt within 15 minutes allowed")
	}
	s.LogAttempt(ctx, "203.0.113.5", "backup", "rate_limited")
	if ok, _ := s.AllowAttempt(ctx, "198.51.100.1"); !ok {
		t.Fatal("other IP limited")
	}
	c.add(AttemptWindow + time.Second)
	if ok, _ := s.AllowAttempt(ctx, "203.0.113.5"); !ok {
		t.Fatal("window did not slide (rate_limited rows must not count)")
	}
}
