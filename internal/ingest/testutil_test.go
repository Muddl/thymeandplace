package ingest

import (
	"archive/zip"
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/dbtest"
)

// serveFiles serves name → content over HTTP and returns the base URL.
func serveFiles(t *testing.T, files map[string][]byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[filepath.Base(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// newPipeline runs the pipeline as tp_owner, as ingest does in production:
// the live views belong to tp_owner, so a versioned table the superuser
// created would be unreadable through them.
func newPipeline(t *testing.T, pool *pgxpool.Pool, ds Dataset) *Pipeline {
	t.Helper()
	root := t.TempDir()
	return &Pipeline{
		DS: ds, Pool: dbtest.As(t, pool, dbtest.Owner), Blob: FSBlob{Root: filepath.Join(root, "bucket")},
		WorkDir: filepath.Join(root, "work"), HTTP: http.DefaultClient,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
