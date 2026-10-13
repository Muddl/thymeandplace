package dbtest_test

import (
	"os"
	"regexp"
	"strings"
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
		if !strings.Contains(string(src), "./db/roles.sql:/roles.sql:ro") {
			t.Errorf("%s does not mount deploy/db/roles.sql at /roles.sql", path)
		}
	}
}

func TestInitScriptRunsProductionRolesSQL(t *testing.T) {
	src, err := os.ReadFile("../../deploy/db/initdb/20-roles.sh")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "-f \"$roles_sql\"") {
		t.Fatal("20-roles.sh does not run roles.sql with psql -f")
	}
	if !strings.Contains(body, "roles_sql=/roles.sql") {
		t.Fatal("20-roles.sh does not read /roles.sql")
	}
}
