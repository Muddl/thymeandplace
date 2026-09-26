package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRefusesWithoutDevseedAllow(t *testing.T) {
	getenv := func(string) string { return "" }
	var stdout, stderr bytes.Buffer

	code := run(getenv, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be untouched, got %q", stdout.String())
	}
	const want = "devseed seeds synthetic reference data and resets the sample catalog; set DEVSEED_ALLOW=1 only for local development or end-to-end tests"
	if got := strings.TrimSpace(stderr.String()); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRunRefusesWhenDevseedAllowIsNotExactlyOne(t *testing.T) {
	for _, v := range []string{"0", "true", "yes", " 1", "1 "} {
		getenv := func(k string) string {
			if k == "DEVSEED_ALLOW" {
				return v
			}
			return ""
		}
		var stdout, stderr bytes.Buffer

		code := run(getenv, &stdout, &stderr)

		if code != 2 {
			t.Fatalf("DEVSEED_ALLOW=%q: exit code = %d, want 2", v, code)
		}
		if !strings.Contains(stderr.String(), "DEVSEED_ALLOW=1") {
			t.Fatalf("DEVSEED_ALLOW=%q: stderr missing guidance: %q", v, stderr.String())
		}
	}
}

// TestRunRefusalIsCheckedBeforeDatabaseURL confirms the guard fires even when
// DATABASE_URL is unset/invalid, i.e. the refusal happens before any attempt
// to connect or migrate.
func TestRunRefusalIsCheckedBeforeDatabaseURL(t *testing.T) {
	getenv := func(k string) string {
		if k == "DATABASE_URL" {
			return "postgres://not-a-real-host-xyz/thymeandplace"
		}
		return ""
	}
	var stdout, stderr bytes.Buffer

	code := run(getenv, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (should refuse before touching DATABASE_URL)", code)
	}
}
