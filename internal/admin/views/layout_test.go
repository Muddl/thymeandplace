package views

import (
	"bytes"
	"context"
	"regexp"
	"testing"
)

// Admin pages version their assets without the public handler ever being
// built: no construction order.
func TestAdminLayoutVersionsAssetsWithoutWebNew(t *testing.T) {
	var buf bytes.Buffer
	if err := Layout(LayoutVM{Title: "t"}).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`/static/admin\.css\?v=[0-9a-f]{10}`).Match(buf.Bytes()) {
		t.Fatalf("unversioned admin asset:\n%s", buf.String())
	}
}
