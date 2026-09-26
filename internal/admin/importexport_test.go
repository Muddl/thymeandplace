package admin

import (
	"bytes"
	"context"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
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
		// Review Focus 2: "new" is reserved so it can't collide with the
		// GET /admin/ingredients/new route; catalog.Validate rejects it for
		// every changeset source, not just the form, so import can't create
		// it either.
		"reserved slug": {[]byte(`{"format":"thymeandplace-catalog/v1","ingredients":[
			{"slug":"new","name":"New","category":"vegetable","kind":"cultivated_year_round","tips":{"choose":"c","store":"s"},"pairings":[]}]}`), 422, "reserved"},
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

// TestHistoryMalformedBeforeIs400 covers the "before" cursor coming back
// from a URL an owner could hand-edit or bookmark stale: a non-integer value
// must be reported, not silently ignored (which would return page one) and
// not a 500 (ParseInt failing is an ordinary client input error, not an
// infrastructure failure).
func TestHistoryMalformedBeforeIs400(t *testing.T) {
	_, c := catalogEnv(t)
	rec := c.get("/admin/history?before=not-a-number")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("history?before=not-a-number = %d, want 400\n%s", rec.Code, rec.Body.String())
	}
}

var (
	olderHrefRE = regexp.MustCompile(`href="([^"]+)">Older`)
	seqRE       = regexp.MustCompile(`"seq": (\d+)`)
)

// TestHistoryPaging seeds 200 catalog_history rows directly (bypassing real
// ingredient writes, which would need 200 slow preview/apply round trips)
// under a slug no real ingredient uses, so the count is exact and isolated
// from the rows catalogEnv's own setup writes. It checks the first page is
// exactly 100 rows, newest first, with an "Older" link, and that following
// that link returns the remaining 100 rows with no further "Older" link.
// 200 is chosen deliberately, not 105: it makes the second page exactly 100
// rows, the case a LIMIT-100 query cannot tell apart from "there might be
// more" (see history in ListHistory's query, now LIMIT 101).
func TestHistoryPaging(t *testing.T) {
	e, c := catalogEnv(t)
	const slug = "paging-test"
	const total = 200
	_, err := e.pool.Exec(context.Background(), `
		INSERT INTO catalog_history (actor, action, table_name, row_key, after)
		SELECT 'seed', 'update', 'ingredient', jsonb_build_object('slug', $1::text), jsonb_build_object('seq', seq)
		FROM generate_series(0, $2::int) AS seq`, slug, total-1)
	if err != nil {
		t.Fatal(err)
	}

	seqAt := func(row string) int {
		m := seqRE.FindStringSubmatch(row)
		if m == nil {
			t.Fatalf("no seq in row:\n%s", row)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}

	page1 := c.get("/admin/history?slug=" + slug)
	body1 := html.UnescapeString(page1.Body.String())
	rows1 := strings.Split(body1, "<tr>")[2:] // strip the pre-table text and the header row
	if len(rows1) != 100 {
		t.Fatalf("page 1 has %d rows, want 100\n%s", len(rows1), body1)
	}
	if got := seqAt(rows1[0]); got != total-1 {
		t.Fatalf("page 1 first row seq = %d, want %d (newest first)", got, total-1)
	}
	if got := seqAt(rows1[len(rows1)-1]); got != total-100 {
		t.Fatalf("page 1 last row seq = %d, want %d", got, total-100)
	}

	m := olderHrefRE.FindStringSubmatch(body1)
	if m == nil {
		t.Fatalf("no Older link on page 1:\n%s", body1)
	}
	older := html.UnescapeString(m[1])

	page2 := c.get(older)
	body2 := html.UnescapeString(page2.Body.String())
	rows2 := strings.Split(body2, "<tr>")[2:]
	if len(rows2) != total-100 {
		t.Fatalf("page 2 has %d rows, want %d\n%s", len(rows2), total-100, body2)
	}
	if got := seqAt(rows2[0]); got != total-101 {
		t.Fatalf("page 2 first row seq = %d, want %d", got, total-101)
	}
	if got := seqAt(rows2[len(rows2)-1]); got != 0 {
		t.Fatalf("page 2 last row seq = %d, want 0", got)
	}
	if olderHrefRE.MatchString(body2) {
		t.Fatal("page 2 (the last page) should have no Older link")
	}
}
