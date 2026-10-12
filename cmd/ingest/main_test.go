package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"thymeandplace/internal/dbtest"
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

func TestApproveLicenseNeedsVersionWithoutDB(t *testing.T) {
	ds := map[string]func() ingest.Dataset{"places": func() ingest.Dataset { return ingest.NewPlaces() }}
	var out, errb bytes.Buffer
	if code := run([]string{"places", "approve-license", "--yes"}, func(string) string { return "" }, &out, &errb, ds); code != 2 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
}

// approve-license --version N without --yes shows the summary and changes
// nothing; with --yes it approves.
func TestApproveLicenseDryRun(t *testing.T) {
	pool := dbtest.New(t)
	// The server returns each request's path as the file's bytes. The
	// sources are pinned to those bytes: CheckTransport refuses unpinned
	// plain-HTTP sources, and run() can't trust an httptest TLS cert.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.URL.Path)) }))
	defer srv.Close()
	pin := func(b string) string { h := sha256.Sum256([]byte(b)); return hex.EncodeToString(h[:]) }
	ds := map[string]func() ingest.Dataset{"places": func() ingest.Dataset {
		return &ingest.Places{SourceList: []ingest.Source{
			{URL: srv.URL + "/cities1000.zip", File: "cities1000.zip", SHA256: pin("/cities1000.zip")},
			{URL: srv.URL + "/admin1CodesASCII.txt", File: "admin1CodesASCII.txt", SHA256: pin("/admin1CodesASCII.txt")},
		}}
	}}
	env := map[string]string{"DATABASE_URL": dbtest.URLAs(pool, dbtest.Owner), "BLOB_DIR": t.TempDir(), "INGEST_WORKDIR": t.TempDir()}
	getenv := func(k string) string { return env[k] }
	var out, errb bytes.Buffer
	if code := run([]string{"places", "fetch"}, getenv, &out, &errb, ds); code != 0 {
		t.Fatalf("fetch: %d %s", code, errb.String())
	}
	for _, want := range []string{"files:", "sha256 " + pin("/admin1CodesASCII.txt"), "  pinned  ", "no live version"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("fetch must print the review summary (missing %q):\n%s", want, out.String())
		}
	}
	m := regexp.MustCompile(`fetched places version (\d+)`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no version in fetch output:\n%s", out.String())
	}
	v := m[1]
	id, _ := strconv.ParseInt(v, 10, 64)
	out.Reset()
	errb.Reset()
	if code := run([]string{"places", "approve-license", "--version", v}, getenv, &out, &errb, ds); code != 2 ||
		!strings.Contains(out.String(), "sha256") || !strings.Contains(errb.String(), "re-run with --yes") {
		t.Fatalf("dry run: code %d\n%s\n%s", code, out.String(), errb.String())
	}
	var reviewed bool
	if err := pool.QueryRow(context.Background(), `SELECT license_reviewed FROM dataset_versions WHERE id = $1`, id).Scan(&reviewed); err != nil || reviewed {
		t.Fatalf("a dry run must not approve (reviewed=%v, %v)", reviewed, err)
	}
	if code := run([]string{"places", "approve-license", "--version", v, "--yes"}, getenv, &out, &errb, ds); code != 0 {
		t.Fatalf("approve: %d %s", code, errb.String())
	}
	if err := pool.QueryRow(context.Background(), `SELECT license_reviewed FROM dataset_versions WHERE id = $1`, id).Scan(&reviewed); err != nil || !reviewed {
		t.Fatalf("--yes must approve (reviewed=%v, %v)", reviewed, err)
	}
}
