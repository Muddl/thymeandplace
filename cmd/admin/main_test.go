package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"thymeandplace/internal/dbtest"
)

func TestCLI(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	env := map[string]string{
		"DATABASE_URL":     pool.Config().ConnString(),
		"ADMIN_SECRET_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)),
		"PUBLIC_BASE_URL":  "https://thymeand.place",
	}
	getenv := func(k string) string { return env[k] }
	pw := func(string) (string, error) { return "correct horse battery staple", nil }

	var out, errb bytes.Buffer
	if code := run(ctx, []string{"enroll"}, getenv, nil, &out, &errb, pw); code != 0 {
		t.Fatalf("enroll: %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "https://thymeand.place/admin/enroll?token=") || !strings.Contains(out.String(), "15 minutes") {
		t.Fatalf("enroll output: %s", out.String())
	}

	out.Reset()
	if code := run(ctx, []string{"set-backup"}, getenv, nil, &out, &errb, pw); code != 0 {
		t.Fatalf("set-backup: %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "otpauth://totp/") {
		t.Fatalf("set-backup output: %s", out.String())
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM admin_backup`).Scan(&n)
	if n != 1 {
		t.Fatal("backup row not stored")
	}

	mismatch := 0
	pwMismatch := func(string) (string, error) {
		mismatch++
		if mismatch == 1 {
			return "correct horse battery staple", nil
		}
		return "different horse battery staple", nil
	}
	if code := run(ctx, []string{"set-backup"}, getenv, nil, &out, &errb, pwMismatch); code == 0 {
		t.Fatal("mismatched confirmation accepted")
	}

	_, _ = pool.Exec(ctx, `INSERT INTO admin_session (token_hash, csrf_token, method, created_at, last_seen_at, auth_at) VALUES ('\x01','c','passkey',now(),now(),now())`)
	out.Reset()
	if code := run(ctx, []string{"revoke-sessions"}, getenv, nil, &out, &errb, pw); code != 0 || !strings.Contains(out.String(), "1 session") {
		t.Fatalf("revoke: %d %s", code, out.String())
	}

	if code := run(ctx, []string{"nope"}, getenv, nil, &out, &errb, pw); code != 2 {
		t.Fatal("unknown command must exit 2")
	}
	failPW := func(string) (string, error) { return "", errors.New("no tty") }
	if code := run(ctx, []string{"set-backup"}, getenv, nil, &out, &errb, failPW); code == 0 {
		t.Fatal("password read failure must fail")
	}
}
