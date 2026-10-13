package prcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIGitleaksJob(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "merge_group:") {
		t.Fatal("ci.yml must run on merge_group")
	}
	if !strings.Contains(s, "gitleaks:") {
		t.Fatal("ci.yml must define a gitleaks job")
	}
	if strings.Contains(s, "gitleaks/gitleaks-action") || strings.Contains(s, "gitleaks-action@") {
		t.Fatal("ci.yml must download a pinned gitleaks binary, not a third-party action")
	}
	for _, want := range []string{
		"permissions:\n      contents: read",
		"fetch-depth: 0",
		"persist-credentials: false",
		"GITLEAKS_VERSION: \"8.30.1\"",
		"GITLEAKS_SHA256: 551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb",
		"sha256sum -c",
		"gitleaks detect",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ci.yml gitleaks job missing %q", want)
		}
	}
}

func TestCIUploadArtifactOnlyOnFailure(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	uploads := 0
	for i, line := range lines {
		if !strings.Contains(line, "actions/upload-artifact@") {
			continue
		}
		uploads++
		if i == 0 || !strings.Contains(lines[i-1], "if:") {
			t.Fatalf("upload-artifact at line %d must set if: on the same step", i+1)
		}
		cond := lines[i-1]
		if strings.Contains(cond, "always()") && !strings.Contains(cond, "failure()") {
			t.Fatalf("upload-artifact at line %d still uses if: always()", i+1)
		}
		if !strings.Contains(cond, "failure()") && !strings.Contains(cond, "!cancelled()") {
			t.Fatalf("upload-artifact at line %d must run on failure, not success", i+1)
		}
		block := strings.Join(lines[i:min(len(lines), i+8)], "\n")
		if !strings.Contains(block, "retention-days: 3") {
			t.Fatalf("upload-artifact at line %d must set retention-days: 3", i+1)
		}
	}
	if uploads == 0 {
		t.Fatal("ci.yml has no upload-artifact steps")
	}
}

func TestGitleaksConfigAllowlistsE2EAgeKeys(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".gitleaks.toml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "useDefault = true") {
		t.Fatal(".gitleaks.toml must extend the default rules")
	}
	if !strings.Contains(s, "deploy/e2e/") || !strings.Contains(s, ".agekey") {
		t.Fatal(".gitleaks.toml must allowlist deploy/e2e/*.agekey")
	}
}
