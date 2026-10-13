package dbtest_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"thymeandplace/internal/dbtest"
)

var postgisImage = regexp.MustCompile(`(?m)^\s*image:\s*(postgis/postgis:[^\s@]+@sha256:[0-9a-f]{64})\s*$`)

func TestPostgisImageRegexRequiresDigest(t *testing.T) {
	digest := strings.Repeat("a", 64)
	pinned := "    image: postgis/postgis:17-3.5@sha256:" + digest + "\n"
	if got := postgisImage.FindAllSubmatch([]byte(pinned), -1); len(got) != 1 || string(got[0][1]) != "postgis/postgis:17-3.5@sha256:"+digest {
		t.Fatalf("pinned image: %q", got)
	}
	for _, src := range []string{
		"    image: postgis/postgis:17-3.5\n",
		"    image: postgis/postgis:17-3.5@sha256:short\n",
		"    image: postgres:17-bookworm@sha256:" + digest + "\n",
	} {
		if postgisImage.MatchString(src) {
			t.Errorf("accepted %q", src)
		}
	}
}

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
