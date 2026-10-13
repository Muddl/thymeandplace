package prcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowRunsOnlyOnPullRequest(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "pr-governance.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "issue_comment") {
		t.Fatal("pr-governance.yml must not run on issue_comment")
	}
	if !strings.Contains(s, "persist-credentials: false") {
		t.Fatal("pr-governance.yml must set persist-credentials: false")
	}
	if !strings.Contains(s, "cache: false") {
		t.Fatal("pr-governance.yml must set setup-go cache: false")
	}
	if !strings.Contains(s, "github.event.pull_request.base.sha") {
		t.Fatal("pr-governance.yml must check out the pull request base SHA")
	}
	if !strings.Contains(s, "pr-governance-pr-${{ github.event.pull_request.number }}") {
		t.Fatal("pr-governance.yml concurrency must be per pull request only")
	}
}
