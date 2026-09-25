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
	for _, args := range [][]string{{}, {"places"}, {"nope", "fetch"}} {
		var out, errb bytes.Buffer
		if code := run(args, env, &out, &errb, ds); code != 2 {
			t.Fatalf("%v: code %d", args, code)
		}
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
