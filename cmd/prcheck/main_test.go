package main

import "testing"

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
