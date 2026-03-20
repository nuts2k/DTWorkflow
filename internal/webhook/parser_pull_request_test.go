package webhook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParser_ParsePullRequestOpened(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "pull_request_opened.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("pull_request", "delivery-1", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	prEvent, ok := event.(PullRequestEvent)
	if !ok {
		t.Fatalf("event type = %T, want PullRequestEvent", event)
	}
	if prEvent.PullRequest.Number != 42 || prEvent.Action != "opened" {
		t.Fatalf("unexpected event: %+v", prEvent)
	}
}

func TestParser_ParsePullRequestClosedIgnored(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "pull_request_closed.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	_, err = parser.Parse("pull_request", "delivery-2", body)
	if err == nil || err != ErrUnsupportedAction {
		t.Fatalf("Parse() error = %v, want %v", err, ErrUnsupportedAction)
	}
}
