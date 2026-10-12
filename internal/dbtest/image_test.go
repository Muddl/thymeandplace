package dbtest_test

import (
	"os"
	"regexp"
	"testing"

	"thymeandplace/internal/dbtest"
)

var postgisImage = regexp.MustCompile(`(?m)^\s*image:\s*(postgis/postgis\S*)\s*$`)

// Dependabot bumps the compose digests but not the Go constant, so this
// keeps the test database on the image production runs.
func TestImageMatchesCompose(t *testing.T) {
	for _, path := range []string{"../../deploy/compose.yaml", "../../deploy/compose.dev.yaml"} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		refs := postgisImage.FindAllSubmatch(src, -1)
		if len(refs) == 0 {
			t.Errorf("%s: no postgis/postgis image", path)
		}
		for _, ref := range refs {
			if got := string(ref[1]); got != dbtest.Image {
				t.Errorf("%s uses %s, dbtest.Image is %s", path, got, dbtest.Image)
			}
		}
	}
}
