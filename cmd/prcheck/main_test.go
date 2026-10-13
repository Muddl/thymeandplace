package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestPRNumberFromPullRequestEvent(t *testing.T) {
	n, err := prNumber([]byte(`{"pull_request":{"number":42}}`))
	if err != nil || n != 42 {
		t.Fatalf("prNumber() = %d, %v, want 42, nil", n, err)
	}
}

func TestPRNumberFromIssueComment(t *testing.T) {
	n, err := prNumber([]byte(`{"issue":{"number":9,"pull_request":{"url":"https://example.invalid"}}}`))
	if err != nil || n != 9 {
		t.Fatalf("prNumber() = %d, %v, want 9, nil", n, err)
	}
}

func TestPRNumberRejectsPlainIssue(t *testing.T) {
	n, err := prNumber([]byte(`{"issue":{"number":3}}`))
	if err == nil || n != 0 {
		t.Fatalf("prNumber() = %d, %v, want 0 and an error", n, err)
	}
}

func TestRunSkipsMergeGroup(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, func(k string) string {
		if k == "GITHUB_EVENT_NAME" {
			return "merge_group"
		}
		return ""
	}, &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("run() = %d, stderr %q, want 0", code, stderr.String())
	}
	if stdout.String() != "prcheck: ok (merge_group)\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "prcheck: ok (merge_group)\n")
	}
}

func TestRunDoesNotSkipQueueSpoof(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := map[string]string{
		"GITHUB_EVENT_NAME": "pull_request",
		"GITHUB_HEAD_REF":   "gh-readonly-queue/main/pr-1-deadbeef",
		"GITHUB_REF":        "refs/heads/gh-readonly-queue/main/pr-1-deadbeef",
		"SKIP_GOVERNANCE":   "1",
		"MERGE_QUEUE":       "1",
	}
	code := run([]string{"-map", filepath.Join("..", "..", ".github", "pr-signoffs.json")}, func(k string) string {
		return env[k]
	}, &stdout, &stderr, nil)
	if code != 2 {
		t.Fatalf("run() = %d, stderr %q, want 2", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "GITHUB_EVENT_PATH is required") {
		t.Fatalf("stderr = %q, want GITHUB_EVENT_PATH is required", stderr.String())
	}
	if strings.Contains(stdout.String(), "prcheck: ok (merge_group)") {
		t.Fatalf("stdout = %q, governance skipped", stdout.String())
	}
}
