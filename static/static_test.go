package static

import (
	"regexp"
	"testing"
	"testing/fstest"
)

func TestVersionIsStableAndContentAddressed(t *testing.T) {
	first, second := Version(), Version()
	if !regexp.MustCompile(`^[0-9a-f]{10}$`).MatchString(first) || first != second {
		t.Fatalf("Version() = %q, then %q", first, second)
	}
	a := hashFS(fstest.MapFS{"app.css": {Data: []byte("a")}})
	b := hashFS(fstest.MapFS{"app.css": {Data: []byte("b")}})
	if a == b || a != hashFS(fstest.MapFS{"app.css": {Data: []byte("a")}}) {
		t.Fatal("the hash must change with content and only with content")
	}
	if Asset("admin.css") != "/static/admin.css?v="+Version() {
		t.Fatalf("Asset = %q", Asset("admin.css"))
	}
}
