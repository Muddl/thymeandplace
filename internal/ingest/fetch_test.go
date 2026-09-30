package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"thymeandplace/internal/dbtest"
)

func sumOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// pinnedPlaces serves files and pins each source to pins[file] ("" = unpinned).
func pinnedPlaces(t *testing.T, files map[string][]byte, pins map[string]string) *Places {
	t.Helper()
	base := serveFiles(t, files)
	var src []Source
	for _, f := range []string{"cities1000.zip", "admin1CodesASCII.txt"} {
		s := Source{URL: base + "/" + f, File: f, SHA256: pins[f]}
		if s.SHA256 == "" {
			s.Unpinned = "test"
		}
		src = append(src, s)
	}
	return &Places{SourceList: src}
}

func TestFetchRejectsPinMismatch(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	files := map[string][]byte{"cities1000.zip": []byte("zip bytes"), "admin1CodesASCII.txt": []byte("changed upstream")}
	p := newPipeline(t, pool, pinnedPlaces(t, files, map[string]string{"admin1CodesASCII.txt": sumOf([]byte("reviewed bytes"))}))

	_, err := p.Fetch(ctx)
	if err == nil || !strings.Contains(err.Error(), "admin1CodesASCII.txt") || !strings.Contains(err.Error(), sumOf([]byte("changed upstream"))) ||
		!strings.Contains(err.Error(), "live data unchanged") {
		t.Fatalf("want a pin mismatch naming the file and both hashes, got %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dataset_versions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("dataset_versions rows = %d (%v); want 0", n, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(p.Blob.(FSBlob).Root, "raw")); len(entries) != 0 {
		t.Fatal("a rejected fetch uploaded raw files")
	}
}

func TestFetchAcceptsPin(t *testing.T) {
	pool := dbtest.New(t)
	files := map[string][]byte{"cities1000.zip": []byte("zip bytes"), "admin1CodesASCII.txt": []byte("reviewed bytes")}
	p := newPipeline(t, pool, pinnedPlaces(t, files, map[string]string{"admin1CodesASCII.txt": sumOf([]byte("reviewed bytes"))}))
	if _, err := p.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFetchFromBucketSource(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, nil)
	body := []byte("a reviewed copy")
	local := filepath.Join(t.TempDir(), "meow.geojson")
	if err := os.WriteFile(local, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Blob.PutFile(ctx, "pinned/ocean/meow.geojson", local); err != nil {
		t.Fatal(err)
	}
	base := serveFiles(t, map[string][]byte{"admin1CodesASCII.txt": []byte("x")})
	p.DS = &Places{SourceList: []Source{
		{URL: "bucket:pinned/ocean/meow.geojson", File: "cities1000.zip", SHA256: sumOf(body)},
		{URL: base + "/admin1CodesASCII.txt", File: "admin1CodesASCII.txt", Unpinned: "test"},
	}}
	if _, err := p.Fetch(ctx); err != nil {
		t.Fatalf("bucket: source: %v", err)
	}
	p.DS.(*Places).SourceList[0].SHA256 = sumOf([]byte("other"))
	if _, err := p.Fetch(ctx); err == nil {
		t.Fatal("a bucket: source must still be checked against its pin")
	}
}

func TestParseChecksums(t *testing.T) {
	m, err := parseChecksums("sha256:a.zip=aa;b.txt=bb")
	if err != nil || m["a.zip"] != "aa" || m["b.txt"] != "bb" || len(m) != 2 {
		t.Fatalf("%v %v", m, err)
	}
	for _, bad := range []string{"", "md5:a=b", "sha256:a", "sha256:=x"} {
		if _, err := parseChecksums(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestCheckTransport(t *testing.T) {
	pin := strings.Repeat("a", 64)
	for _, tc := range []struct {
		src Source
		ok  bool
	}{
		{Source{URL: "https://x/f", File: "f", Unpinned: "r"}, true},
		{Source{URL: "http://x/f", File: "f", SHA256: pin}, true},
		{Source{URL: "bucket:pinned/f", File: "f", SHA256: pin}, true},
		{Source{URL: "http://x/f", File: "f", Unpinned: "r"}, false},
		{Source{URL: "bucket:pinned/f", File: "f", Unpinned: "r"}, false},
		{Source{URL: "ftp://x/f", File: "f", SHA256: pin}, false},
	} {
		err := CheckTransport(&Places{SourceList: []Source{tc.src}})
		if (err == nil) != tc.ok {
			t.Errorf("%s pinned=%v: err=%v, want ok=%v", tc.src.URL, tc.src.SHA256 != "", err, tc.ok)
		}
	}
	for _, ds := range []Dataset{NewPlaces(), NewOcean(""), NewClimate("")} {
		if err := CheckTransport(ds); err != nil {
			t.Errorf("default %s: %v", ds.Name(), err)
		}
	}
}

func TestRedirectPolicy(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer plain.Close()
	var tlsSrv *httptest.Server
	tlsSrv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-http":
			http.Redirect(w, r, plain.URL+"/f", http.StatusFound)
		case "/to-https":
			http.Redirect(w, r, tlsSrv.URL+"/final", http.StatusFound)
		case "/hop":
			http.Redirect(w, r, tlsSrv.URL+"/to-http", http.StatusFound) // https → https → http
		case "/loop":
			n, _ := strconv.Atoi(r.URL.Query().Get("n"))
			http.Redirect(w, r, tlsSrv.URL+"/loop?n="+strconv.Itoa(n+1), http.StatusFound)
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer tlsSrv.Close()
	c := NewHTTPClient(5 * time.Second)
	c.Transport = tlsSrv.Client().Transport
	get := func(path string) error {
		resp, err := c.Get(tlsSrv.URL + path)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if err := get("/to-http"); err == nil || !strings.Contains(err.Error(), "HTTPS to http") {
		t.Fatalf("https → http must be refused: %v", err)
	}
	if err := get("/hop"); err == nil {
		t.Fatal("https → https → http must be refused")
	}
	if err := get("/to-https"); err != nil {
		t.Fatalf("https → https must be followed: %v", err)
	}
	if err := get("/loop"); err == nil || !strings.Contains(err.Error(), "10 redirects") {
		t.Fatalf("11 redirects must be refused: %v", err)
	}
}

func TestTransformRejectsTamperedRaw(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, placesSource(t, 20))
	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Change the bucket copy and drop the local one, so Transform pulls the
	// changed bytes back.
	root := p.Blob.(FSBlob).Root
	if err := os.WriteFile(filepath.Join(root, "raw", "places", "v"+strconv.FormatInt(v, 10), "admin1CodesASCII.txt"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(p.WorkDir, "places", "v"+strconv.FormatInt(v, 10), "raw")); err != nil {
		t.Fatal(err)
	}
	if err := p.Transform(ctx, v); err == nil || !strings.Contains(err.Error(), "differs from what was fetched and reviewed") {
		t.Fatalf("want tamper refusal, got %v", err)
	}
}

func TestTransformRejectsUnrecordedFile(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, placesSource(t, 20))
	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Keep cities1000.zip's real checksum and drop admin1CodesASCII.txt's
	// entry, so the first file verifies and the second has nothing recorded.
	if _, err := pool.Exec(ctx, `UPDATE dataset_versions SET source_checksum = split_part(source_checksum, ';', 1) WHERE id = $1`, v); err != nil {
		t.Fatal(err)
	}
	err = p.Transform(ctx, v)
	if err == nil || !strings.Contains(err.Error(), "admin1CodesASCII.txt") || !strings.Contains(err.Error(), "differs from what was fetched and reviewed") {
		t.Fatalf("a file with no recorded checksum must not be treated as verified; got %v", err)
	}
}
