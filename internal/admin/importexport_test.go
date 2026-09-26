package admin

import (
	"bytes"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogjson"
)

func (c *client) upload(path, filename string, content []byte) *httptest.ResponseRecorder {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf", c.csrf)
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(content)
	_ = mw.Close()
	return c.do("POST", path, &buf, map[string]string{"Content-Type": mw.FormDataContentType()})
}

const chardFile = `{"format":"thymeandplace-catalog/v1","ingredients":[
 {"slug":"chard","name":"Chard","category":"vegetable","kind":"cultivated_year_round",
  "tips":{"choose":"Glossy leaves.","store":"Fridge, wrapped."},"pairings":["kale"],"note":""}]}`

func TestImportPreviewApplyExportRoundTrip(t *testing.T) {
	e, c := catalogEnv(t)
	c.get("/admin/import")
	rec := c.upload("/admin/import", "chard.json", []byte(chardFile))
	b := html.UnescapeString(rec.Body.String())
	if rec.Code != 200 || !strings.Contains(b, "Chard") || !strings.Contains(b, "1 added item") || !strings.Contains(b, "11 unchanged") {
		t.Fatalf("import preview: %d\n%s", rec.Code, b)
	}
	if rec := c.postForm("/admin/changesets/"+pendingID(t, b)+"/apply", url.Values{}); rec.Code != 303 {
		t.Fatalf("apply: %d", rec.Code)
	}
	exp := c.get("/admin/export")
	if cd := exp.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="thymeandplace-catalog-`) {
		t.Fatalf("disposition = %q", cd)
	}
	items, err := catalogjson.Decode(exp.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := e.snap(t).All()
	if len(items) != 12 {
		t.Fatalf("export has %d items", len(items))
	}
	for i := range want {
		if !reflect.DeepEqual(catalog.Normalize(items[i]), want[i]) {
			t.Fatalf("export mismatch for %s", want[i].Slug)
		}
	}
}

func TestImportErrors(t *testing.T) {
	_, c := catalogEnv(t)
	c.get("/admin/import")
	cases := map[string]struct {
		content []byte
		status  int
		want    string
	}{
		"unknown format": {[]byte(`{"format":"other/v2","ingredients":[]}`), 400, `unknown catalog format "other/v2"`},
		"not json":       {[]byte(`hello`), 400, "not a valid JSON document"},
		"too large":      {append([]byte(`{"format":"thymeandplace-catalog/v1","ingredients":[],"pad":"`), bytes.Repeat([]byte("x"), catalogjson.MaxBytes)...), 400, "larger than 5 MB"},
		"invalid item": {[]byte(`{"format":"thymeandplace-catalog/v1","ingredients":[
			{"slug":"chard","name":"","category":"vegetable","kind":"cultivated_year_round","tips":{"choose":"c","store":"s"},"pairings":[]}]}`), 422, "chard: name must be 1 to 80 characters"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := c.upload("/admin/import", "x.json", tc.content)
			if rec.Code != tc.status || !strings.Contains(html.UnescapeString(rec.Body.String()), tc.want) {
				t.Fatalf("%d\n%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestOversizedRequestIs413(t *testing.T) {
	_, c := catalogEnv(t)
	c.get("/admin/import")
	rec := c.upload("/admin/import", "big.json", bytes.Repeat([]byte("x"), 7<<20))
	if rec.Code != 413 || !strings.Contains(rec.Body.String(), "The limit is 5 MB") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

// chunkedBody wraps a Reader in a type httptest.NewRequest does not
// special-case (unlike *bytes.Buffer, *bytes.Reader or *strings.Reader), so
// the resulting request reports ContentLength -1: the same "length unknown"
// signal a real chunked-encoded request (no Content-Length header) would
// give the server.
type chunkedBody struct{ r io.Reader }

func (c *chunkedBody) Read(p []byte) (int, error) { return c.r.Read(p) }

// TestChunkedOversizedRequestIs413 covers a plain (non-HTMX) form post: no
// X-CSRF-Token header, only the "csrf" form field, and no Content-Length
// (as a real chunked-encoded upload would arrive). The Content-Length guard
// in csrf cannot see this one coming, so it must still resolve as the same
// 413 once the body read trips http.MaxBytesHandler's cap — never as a
// misleading CSRF 403 from a form read that silently failed partway through.
func TestChunkedOversizedRequestIs413(t *testing.T) {
	_, c := catalogEnv(t)
	c.get("/admin/import")
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf", c.csrf)
	fw, _ := mw.CreateFormFile("file", "big.json")
	_, _ = fw.Write(bytes.Repeat([]byte("x"), 7<<20))
	_ = mw.Close()

	r := httptest.NewRequest("POST", "/admin/import", &chunkedBody{&buf})
	r.Header.Set("Content-Type", mw.FormDataContentType())
	for k, v := range c.jar {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	if r.ContentLength != -1 {
		t.Fatalf("expected an unknown Content-Length, got %d", r.ContentLength)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)
	if rec.Code != 413 || !strings.Contains(rec.Body.String(), "The limit is 5 MB") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestHistoryNewestFirstAndFilter(t *testing.T) {
	_, c := catalogEnv(t)
	f := tomatoForm()
	f.Set("note", "first")
	c.postForm("/admin/changesets/"+pendingID(t, c.postForm("/admin/ingredients/tomato/preview", f).Body.String())+"/apply", url.Values{})
	f.Set("note", "second")
	c.postForm("/admin/changesets/"+pendingID(t, c.postForm("/admin/ingredients/tomato/preview", f).Body.String())+"/apply", url.Values{})

	b := html.UnescapeString(c.get("/admin/history?slug=tomato").Body.String())
	rows := strings.Split(b, "<tr>") // [0] before the table, [1] header, [2] newest row
	if len(rows) < 4 || !strings.Contains(rows[2], `"note": "second"`) || !strings.Contains(rows[3], `"note": "first"`) {
		t.Fatalf("history not newest first:\n%s", b)
	}
	if strings.Contains(c.get("/admin/history?slug=kale").Body.String(), "second") {
		t.Fatal("slug filter leaked other rows")
	}
}
