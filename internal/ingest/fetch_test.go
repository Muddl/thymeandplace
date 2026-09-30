package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
