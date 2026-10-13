package prcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPRGovernanceWorkflow(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "pr-governance.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "issue_comment") {
		t.Fatal("pr-governance.yml must not run on issue_comment")
	}
	if strings.Contains(s, "gh-readonly-queue") {
		t.Fatal("pr-governance.yml must not key a skip on gh-readonly-queue")
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
	if !strings.Contains(s, "merge_group:") {
		t.Fatal("pr-governance.yml must trigger on merge_group")
	}
	if !strings.Contains(s, "github.event_name == 'merge_group'") {
		t.Fatal("pr-governance.yml skip must be github.event_name == 'merge_group'")
	}
	if !strings.Contains(s, "cancel-in-progress: ${{ github.event_name == 'pull_request' }}") {
		t.Fatal("pr-governance.yml must cancel in-progress runs only for pull_request")
	}
	if !strings.Contains(s, "group: pr-governance-${{ github.event_name }}-${{ github.event.pull_request.number || github.sha }}") {
		t.Fatal("pr-governance.yml concurrency group must include event_name")
	}
}

func TestCIWorkflowMergeGroup(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "merge_group:") {
		t.Fatal("ci.yml must trigger on merge_group")
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "cancel-in-progress: true" {
			t.Fatal("ci.yml cancel-in-progress must not be a bare true")
		}
	}
	if !strings.Contains(s, "cancel-in-progress: ${{ github.event_name != 'merge_group' }}") {
		t.Fatal("ci.yml must keep merge_group runs from being cancelled")
	}
	if !strings.Contains(s, "group: ci-${{ github.workflow }}-${{ github.event_name }}-${{ github.ref }}") {
		t.Fatal("ci.yml concurrency group must include event_name")
	}
	for _, job := range []string{"e2e-select", "e2e", "e2e-report", "e2e-result"} {
		got := jobIf(t, s, job)
		if !strings.Contains(got, "merge_group") {
			t.Fatalf("%s if = %q, want merge_group", job, got)
		}
	}
	if !strings.Contains(s, "github.event.merge_group.base_sha") {
		t.Fatal("ci.yml must diff e2e browsers against merge_group.base_sha")
	}
	backupIf := jobIf(t, s, "backup")
	if strings.Contains(backupIf, "merge_group") {
		t.Fatalf("backup if = %q, must not include merge_group", backupIf)
	}
	checkouts := strings.Count(s, "uses: actions/checkout@")
	creds := strings.Count(s, "persist-credentials: false")
	if checkouts == 0 || creds < checkouts {
		t.Fatalf("ci.yml has %d checkouts and %d persist-credentials: false", checkouts, creds)
	}
}

func jobIf(t *testing.T, yaml, job string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(job) + `:\n    if: (.+)$`)
	m := re.FindStringSubmatch(yaml)
	if m == nil {
		t.Fatalf("job %s has no if", job)
	}
	return m[1]
}
