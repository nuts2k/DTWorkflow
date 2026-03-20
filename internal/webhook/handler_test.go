package webhook

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLogHandler_DefaultLogger(t *testing.T) {
	h := NewLogHandler(nil)
	if h == nil {
		t.Fatal("NewLogHandler(nil) returned nil")
	}
	if h.logger == nil {
		t.Fatal("NewLogHandler(nil) should set default logger")
	}
}

func TestLogHandler_HandlePullRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := NewLogHandler(logger)

	err := h.HandlePullRequest(context.Background(), PullRequestEvent{
		DeliveryID: "delivery-pr-1",
		EventType:  "pull_request",
		Action:     "opened",
		Repository: RepositoryRef{FullName: "owner/repo"},
		PullRequest: PullRequestRef{Number: 42},
	})
	if err != nil {
		t.Fatalf("HandlePullRequest() error = %v", err)
	}
	output := buf.String()
	for _, want := range []string{"delivery-pr-1", "owner/repo", "42", "opened"} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output missing %q: %s", want, output)
		}
	}
}

func TestLogHandler_HandleIssueLabel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := NewLogHandler(logger)

	err := h.HandleIssueLabel(context.Background(), IssueLabelEvent{
		DeliveryID:     "delivery-1",
		EventType:      "issues",
		Action:         "labeled",
		Repository:     RepositoryRef{FullName: "owner/repo"},
		Issue:          IssueRef{Number: 12},
		Label:          LabelRef{Name: "auto-fix"},
		AutoFixChanged: true,
		AutoFixAdded:   true,
	})
	if err != nil {
		t.Fatalf("HandleIssueLabel() error = %v", err)
	}
	output := buf.String()
	for _, want := range []string{"delivery-1", "owner/repo", "auto-fix", "true"} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output missing %q: %s", want, output)
		}
	}
}
