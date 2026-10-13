package prcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const head = "abcdef1234567890aaaa"

func testConfig(t *testing.T) Config {
	t.Helper()
	path := filepath.Join("..", "..", ".github", "pr-signoffs.json")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := LoadConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func linkedInput() Input {
	return Input{
		Body:    "Fixes #12",
		HeadSHA: head,
		Issues:  map[int]string{12: "## Acceptance criteria\n\n- [ ] done\n"},
	}
}

func TestMissingIssueLink(t *testing.T) {
	cfg := testConfig(t)
	in := linkedInput()
	in.Body = "No ticket mentioned."
	got := Check(in, cfg)
	want := "PR must link an issue with Closes #N, Fixes #N, or Refs #N"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Check() = %#v, want [%q]", got, want)
	}
}

func TestExemptNoIssueLabel(t *testing.T) {
	cfg := testConfig(t)
	in := Input{
		Body:    "docs typo",
		HeadSHA: head,
		Labels:  []string{"no-issue"},
	}
	got := Check(in, cfg)
	if len(got) != 0 {
		t.Fatalf("Check() = %#v, want no failures", got)
	}
}

func TestStaleApprovalSHA(t *testing.T) {
	cfg := testConfig(t)
	in := linkedInput()
	in.Files = []string{"migrations/00099_x.sql"}
	in.Labels = []string{"arch-approved"}
	in.Comments = []string{"Architect Farmer: approved 0000000"}
	got := Check(in, cfg)
	if len(got) != 1 {
		t.Fatalf("Check() = %#v, want one failure", got)
	}
	if !strings.Contains(got[0], "stale") || !strings.Contains(got[0], "0000000") || !strings.Contains(got[0], "abcdef1") {
		t.Fatalf("Check() = %q, want stale SHA 0000000 vs head abcdef1", got[0])
	}
}

func TestValidApproval(t *testing.T) {
	cfg := testConfig(t)
	in := linkedInput()
	in.Files = []string{"migrations/00099_x.sql"}
	in.Labels = []string{"arch-approved"}
	in.Comments = []string{"Architect Farmer: approved abcdef1"}
	got := Check(in, cfg)
	if len(got) != 0 {
		t.Fatalf("Check() = %#v, want no failures", got)
	}
}

func TestPathMatch(t *testing.T) {
	cfg := testConfig(t)

	readme := linkedInput()
	readme.Files = []string{"README.md"}
	if got := Check(readme, cfg); len(got) != 0 {
		t.Fatalf("README-only Check() = %#v, want no sign-off", got)
	}

	migration := linkedInput()
	migration.Files = []string{"migrations/00099_x.sql"}
	got := Check(migration, cfg)
	if len(got) != 1 {
		t.Fatalf("migration Check() = %#v, want one failure", got)
	}
	if !strings.Contains(got[0], "`arch-approved`") || !strings.Contains(got[0], "migrations/00099_x.sql") {
		t.Fatalf("migration Check() = %q, want arch-approved for the migration file", got[0])
	}

	dockerfile := linkedInput()
	dockerfile.Files = []string{"deploy/Dockerfile.app"}
	got = Check(dockerfile, cfg)
	if len(got) != 1 || !strings.Contains(got[0], "`devops-approved`") {
		t.Fatalf("Dockerfile Check() = %#v, want devops-approved only", got)
	}

	compose := linkedInput()
	compose.Files = []string{"deploy/compose.yaml"}
	got = Check(compose, cfg)
	if len(got) != 1 || !strings.Contains(got[0], "`liveops-approved` or `arch-approved`") {
		t.Fatalf("compose Check() = %#v, want liveops or arch", got)
	}
}

func TestDependabotSkipsIssueLink(t *testing.T) {
	cfg := testConfig(t)
	in := Input{
		Body:    "Bump actions",
		HeadSHA: head,
		Author:  "dependabot[bot]",
	}
	got := Check(in, cfg)
	if len(got) != 0 {
		t.Fatalf("Check() = %#v, want no failures", got)
	}
}

func TestMissingAcceptanceHeading(t *testing.T) {
	cfg := testConfig(t)
	in := linkedInput()
	in.Issues = map[int]string{12: "Please do the thing.\n"}
	got := Check(in, cfg)
	want := `issue #12 is missing a "## Acceptance criteria" heading`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Check() = %#v, want [%q]", got, want)
	}
}

func TestMatchPath(t *testing.T) {
	cases := []struct {
		pattern, file string
		want          bool
	}{
		{"migrations/**", "migrations/00001.sql", true},
		{"migrations/**", "internal/store/db.go", false},
		{"cmd/*/app.go", "cmd/server/app.go", true},
		{"cmd/*/app.go", "cmd/server/main.go", false},
		{"**/views/lint_test.go", "internal/web/views/lint_test.go", true},
		{"**/Dockerfile.*", "deploy/Dockerfile.app", true},
		{"**/Dockerfile", "deploy/backup/Dockerfile", true},
		{"**/Dockerfile", "deploy/Dockerfile.app", false},
		{"deploy/**", "deploy/compose.yaml", true},
		{"static/**", "static/app.css", true},
	}
	for _, tc := range cases {
		if got := MatchPath(tc.pattern, tc.file); got != tc.want {
			t.Errorf("MatchPath(%q, %q) = %v, want %v", tc.pattern, tc.file, got, tc.want)
		}
	}
}
