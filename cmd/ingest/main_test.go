package main

import (
	"bytes"
	"strings"
	"testing"

	"thymeandplace/internal/ingest"
)

func TestUsageErrors(t *testing.T) {
	ds := map[string]func() ingest.Dataset{"places": func() ingest.Dataset { return ingest.NewPlaces() }}
	env := func(string) string { return "" }
	for _, args := range [][]string{{}, {"places"}, {"nope", "fetch"}, {"places", "bogus-step"}} {
		var out, errb bytes.Buffer
		if code := run(args, env, &out, &errb, ds); code != 2 {
			t.Fatalf("%v: code %d", args, code)
		}
	}
}

// Review Focus M7: an unknown step name is rejected before any DB connection
// is attempted (no DATABASE_URL is set, so a DB-connect attempt would hang or
// error differently than exit 2).
func TestUnknownStepExitsWithoutDB(t *testing.T) {
	ds := map[string]func() ingest.Dataset{"places": func() ingest.Dataset { return ingest.NewPlaces() }}
	env := func(string) string { return "" }
	var out, errb bytes.Buffer
	if code := run([]string{"places", "bogus-step"}, env, &out, &errb, ds); code != 2 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	if !strings.Contains(errb.String(), `unknown step "bogus-step"`) {
		t.Fatalf("stderr = %q", errb.String())
	}
}

// Review Focus 5: failures name the dataset and step and exit non-zero.
func TestFailureNamesDatasetAndStep(t *testing.T) {
	ds := map[string]func() ingest.Dataset{"places": func() ingest.Dataset { return ingest.NewPlaces() }}
	env := func(k string) string {
		if k == "DATABASE_URL" {
			return "postgres://nobody:nothing@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"
		}
		return ""
	}
	var out, errb bytes.Buffer
	code := run([]string{"places", "swap", "--version", "1"}, env, &out, &errb, ds)
	if code != 1 || !strings.Contains(errb.String(), "ingest places swap:") {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
}
