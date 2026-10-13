package prcheck

import (
	"strings"
	"testing"
)

func TestSkipGovernance(t *testing.T) {
	if !SkipGovernance("merge_group") {
		t.Fatal(`SkipGovernance("merge_group") = false, want true`)
	}
	for _, name := range []string{"pull_request", "push", "issue_comment", "workflow_dispatch", ""} {
		if SkipGovernance(name) {
			t.Fatalf("SkipGovernance(%q) = true, want false", name)
		}
	}
}

func TestCheckIgnoresMergeQueueSpoof(t *testing.T) {
	cfg := testConfig(t)
	in := Input{
		Body:    "Queued on gh-readonly-queue/main/pr-1-deadbeef with merge-queue, merge_group, and gh-readonly-queue.",
		HeadSHA: head,
		Labels:  []string{"merge-queue", "merge_group", "gh-readonly-queue"},
	}
	got := Check(in, cfg)
	want := "PR must link an issue with Closes #N, Fixes #N, or Refs #N"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Check() = %#v, want [%q]", got, want)
	}

	in = linkedInput()
	in.Body = "Fixes #12 on gh-readonly-queue/main/pr-1-deadbeef"
	in.Labels = []string{"merge-queue", "merge_group", "gh-readonly-queue"}
	in.Files = []string{".github/workflows/ci.yml"}
	got = Check(in, cfg)
	if len(got) != 1 || !strings.Contains(got[0], "`devops-approved`") {
		t.Fatalf("Check() = %#v, want devops-approved for the workflow file", got)
	}
}
