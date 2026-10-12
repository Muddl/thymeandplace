package auth

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestPruneRemovesOnlyExpiredRows(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	now := c.now()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Login-log retention is applied by prune_admin_login_log against the
	// database clock, so these rows are placed relative to now() in SQL.
	exec(`INSERT INTO admin_login_log (at, ip, ip_key, method, outcome) VALUES
		(now() - interval '91 days', 'old', 'old', 'passkey', 'failure'),
		(now() - interval '89 days', 'kept', 'kept', 'passkey', 'failure')`)

	live, _, err := s.CreateSession(ctx, "passkey")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO admin_session (token_hash, csrf_token, method, created_at, last_seen_at, auth_at) VALUES
		('\x01', 'c', 'passkey', $1, $2, $1), ('\x02', 'c', 'passkey', $3, $4, $3)`,
		now.Add(-time.Hour), now.Add(-IdleTimeout-time.Minute), // idle
		now.Add(-AbsoluteTimeout-time.Minute), now.Add(-time.Minute)) // over age

	liveTok, _, err := s.CreateEnrollmentToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO admin_enrollment_token (token_hash, created_at, expires_at, used_at) VALUES
		('\x03', $1, $2, $1), ('\x04', $1, $3, NULL)`,
		now.Add(-time.Minute), now.Add(10*time.Minute), // used
		now.Add(-time.Second)) // expired

	r, err := s.Prune(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.LoginLog != 1 || r.Sessions != 2 || r.EnrollmentTokens != 2 {
		t.Fatalf("pruned %+v; want 1 log row, 2 sessions, 2 tokens", r)
	}
	var ips []string
	rows, _ := s.pool.Query(ctx, `SELECT ip FROM admin_login_log`)
	for rows.Next() {
		var ip string
		_ = rows.Scan(&ip)
		ips = append(ips, ip)
	}
	if len(ips) != 1 || ips[0] != "kept" {
		t.Fatalf("login log after prune: %v; only the 89-day-old row should remain", ips)
	}
	if _, ok, err := s.LookupSession(ctx, live); err != nil || !ok {
		t.Fatalf("the live session must survive the prune: ok=%v err=%v", ok, err)
	}
	if ok, err := s.EnrollmentValid(ctx, liveTok); err != nil || !ok {
		t.Fatalf("the live enrollment token must survive the prune: ok=%v err=%v", ok, err)
	}
}

func TestPruneWarnsWhenLoginLogGrowsFast(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	var logs bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	s.growthWarn = 3
	var fired []string
	s.onEvent = func(k string) { fired = append(fired, k) }
	for i := 0; i < 3; i++ {
		s.LogAttempt(ctx, v4("192.0.2.40"), "passkey", "failure")
	}
	if _, err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "admin login log growing fast") {
		t.Fatal("warned at the threshold; it must warn only above it")
	}
	if len(fired) != 0 {
		t.Fatalf("event fired at the threshold: %v", fired)
	}
	s.LogAttempt(ctx, v4("192.0.2.40"), "passkey", "failure")
	if _, err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "admin login log growing fast") {
		t.Fatalf("no growth warning above the threshold:\n%s", logs.String())
	}
	if len(fired) != 1 || fired[0] != "login_log_growth" {
		t.Fatalf("growth event: %v", fired)
	}
}
