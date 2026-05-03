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

func TestParsePullRequest_Merged(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "pull_request_merged.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("pull_request", "delivery-merged", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	prEvent, ok := event.(PullRequestEvent)
	if !ok {
		t.Fatalf("event type = %T, want PullRequestEvent", event)
	}
	if prEvent.Action != "merged" {
		t.Errorf("Action = %q, want %q", prEvent.Action, "merged")
	}
	if !prEvent.PullRequest.Merged {
		t.Errorf("PullRequest.Merged = false, want true")
	}
}

func TestParsePullRequest_ClosedNotMerged(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "pull_request_closed.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	_, err = parser.Parse("pull_request", "delivery-closed", body)
	if err != ErrUnsupportedAction {
		t.Fatalf("Parse() error = %v, want %v", err, ErrUnsupportedAction)
	}
}

func TestParsePullRequest_MergedFields(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "pull_request_merged.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("pull_request", "delivery-merged-fields", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	prEvent := event.(PullRequestEvent)
	if prEvent.PullRequest.Number != 99 {
		t.Errorf("Number = %d, want 99", prEvent.PullRequest.Number)
	}
	if prEvent.PullRequest.BaseRef != "main" {
		t.Errorf("BaseRef = %q, want %q", prEvent.PullRequest.BaseRef, "main")
	}
	if prEvent.PullRequest.HeadRef != "feature-x" {
		t.Errorf("HeadRef = %q, want %q", prEvent.PullRequest.HeadRef, "feature-x")
	}
	if prEvent.PullRequest.BaseSHA != "def456" {
		t.Errorf("BaseSHA = %q, want %q", prEvent.PullRequest.BaseSHA, "def456")
	}
	if prEvent.PullRequest.HeadSHA != "abc123" {
		t.Errorf("HeadSHA = %q, want %q", prEvent.PullRequest.HeadSHA, "abc123")
	}
	if prEvent.PullRequest.Title != "Implement feature X" {
		t.Errorf("Title = %q, want %q", prEvent.PullRequest.Title, "Implement feature X")
	}
	if !prEvent.PullRequest.Merged {
		t.Errorf("Merged = false, want true")
	}
	if prEvent.Repository.FullName != "owner/repo" {
		t.Errorf("Repository.FullName = %q, want %q", prEvent.Repository.FullName, "owner/repo")
	}
}
