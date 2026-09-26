package auth

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionIdleAndAbsoluteExpiry(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	raw, sess, err := s.CreateSession(ctx, "passkey")
	if err != nil || len(sess.CSRF) < 32 {
		t.Fatal(err)
	}
	c.add(29 * time.Minute)
	if _, ok, _ := s.LookupSession(ctx, raw); !ok {
		t.Fatal("29 min idle should be fine")
	}
	c.add(29 * time.Minute) // touched at 29m, now 58m: 29m idle
	if _, ok, _ := s.LookupSession(ctx, raw); !ok {
		t.Fatal("activity must extend the idle timer")
	}
	c.add(31 * time.Minute)
	if _, ok, _ := s.LookupSession(ctx, raw); ok {
		t.Fatal("31 min idle must expire")
	}

	raw2, _, _ := s.CreateSession(ctx, "passkey")
	for i := 0; i < 36; i++ { // active every 20 minutes for 12 hours
		c.add(20 * time.Minute)
		if _, ok, _ := s.LookupSession(ctx, raw2); !ok {
			t.Fatalf("expired early at step %d", i)
		}
	}
	c.add(20 * time.Minute)
	if _, ok, _ := s.LookupSession(ctx, raw2); ok {
		t.Fatal("12 h absolute limit exceeded")
	}
}

func TestReauthWindow(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	raw, sess, _ := s.CreateSession(ctx, "passkey")
	if !sess.RecentAuth(c.now().Add(4 * time.Minute)) {
		t.Fatal("4 min after sign-in is recent")
	}
	c.add(6 * time.Minute)
	sess, _, _ = s.LookupSession(ctx, raw)
	if sess.RecentAuth(c.now()) {
		t.Fatal("6 min is not recent")
	}
	if err := s.MarkReauthed(ctx, sess); err != nil {
		t.Fatal(err)
	}
	sess, _, _ = s.LookupSession(ctx, raw)
	if !sess.RecentAuth(c.now()) {
		t.Fatal("re-auth should reset the window")
	}
}

func TestRevokeAndDelete(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	a, _, _ := s.CreateSession(ctx, "passkey")
	b, _, _ := s.CreateSession(ctx, "backup")
	if err := s.DeleteSession(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.LookupSession(ctx, a); ok {
		t.Fatal("deleted session still valid")
	}
	if n, err := s.RevokeAllSessions(ctx); err != nil || n != 1 {
		t.Fatalf("revoked %d %v", n, err)
	}
	if _, ok, _ := s.LookupSession(ctx, b); ok {
		t.Fatal("revoked session still valid")
	}
	if _, ok, _ := s.LookupSession(ctx, "garbage"); ok {
		t.Fatal("unknown token valid")
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "tok")
	ck := rec.Result().Cookies()[0]
	if ck.Name != "__Host-tp_admin" || !ck.HttpOnly || !ck.Secure || ck.SameSite != 3 /* Strict */ || ck.Path != "/" || ck.Domain != "" {
		t.Fatalf("cookie = %+v", ck)
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	keepRaw, keep, _ := s.CreateSession(ctx, "passkey")
	a, _, _ := s.CreateSession(ctx, "passkey")
	b, _, _ := s.CreateSession(ctx, "backup")
	if n, err := s.RevokeOtherSessions(ctx, keep); err != nil || n != 2 {
		t.Fatalf("revoked %d %v", n, err)
	}
	if _, ok, _ := s.LookupSession(ctx, keepRaw); !ok {
		t.Fatal("current session was revoked")
	}
	for _, raw := range []string{a, b} {
		if _, ok, _ := s.LookupSession(ctx, raw); ok {
			t.Fatal("other session still valid")
		}
	}
}
