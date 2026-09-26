package main

import (
	"context"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, html.UnescapeString(string(b))
}

func TestAppServesAndReloadsWithoutRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	tx, _ := pool.Begin(ctx)
	if err := catalog.Write(ctx, store.New(tx), catalogtest.SmallCatalog(), nil); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)

	a, err := newApp(ctx, config{DatabaseURL: pool.Config().ConnString(), Addr: ":0"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	a.start(ctx)
	srv := httptest.NewServer(a.handler)
	defer srv.Close()

	if code, _ := get(t, srv.URL+"/readyz"); code != 200 {
		t.Fatalf("readyz = %d", code)
	}
	season := srv.URL + "/season?lat=38.7167&lng=-9.1333&date=2026-10-15"
	code, body := get(t, season)
	for _, want := range []string{"near Lisbon, PT", "Mackerel", "Banana"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("missing %q (%d)\n%s", want, code, body)
		}
	}

	if _, err := pool.Exec(ctx, `UPDATE ingredient SET name = 'Plantain' WHERE slug = 'banana'`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, body := get(t, season); strings.Contains(body, "Plantain") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("catalog change not reflected without restart")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestLoadConfig(t *testing.T) {
	if _, err := loadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("DATABASE_URL is required")
	}
	c, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://x", "TRUST_PROXY_HEADERS": "true"}[k]
	})
	if err != nil || c.Addr != ":8080" || !c.TrustProxy {
		t.Fatalf("%+v %v", c, err)
	}
}
