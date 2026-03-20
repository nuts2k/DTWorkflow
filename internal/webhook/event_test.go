package webhook

import "testing"

func TestPullRequestEvent_Name(t *testing.T) {
	e := PullRequestEvent{EventType: "pull_request", Action: "opened"}
	if got := e.Name(); got != "pull_request.opened" {
		t.Fatalf("Name() = %q, want %q", got, "pull_request.opened")
	}
}

func TestIssueLabelEvent_Name(t *testing.T) {
	e := IssueLabelEvent{EventType: "issues", Action: "labeled"}
	if got := e.Name(); got != "issues.labeled" {
		t.Fatalf("Name() = %q, want %q", got, "issues.labeled")
	}
}
