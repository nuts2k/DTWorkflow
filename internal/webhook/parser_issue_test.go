package webhook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParser_ParseIssueLabeledAutoFix(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issue_labeled_auto_fix.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-3", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent, ok := event.(IssueLabelEvent)
	if !ok {
		t.Fatalf("event type = %T, want IssueLabelEvent", event)
	}
	if !issueEvent.AutoFixChanged || !issueEvent.AutoFixAdded || issueEvent.AutoFixRemoved {
		t.Fatalf("unexpected auto-fix flags: %+v", issueEvent)
	}
}

func TestParser_ParseIssueLabeledOther(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issue_labeled_other.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-4", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent := event.(IssueLabelEvent)
	if issueEvent.AutoFixChanged {
		t.Fatalf("AutoFixChanged = true, want false")
	}
}

func TestParser_ParseIssueUnlabeledAutoFixCaseInsensitive(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issue_unlabeled_auto_fix.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-5", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent := event.(IssueLabelEvent)
	if !issueEvent.AutoFixChanged || issueEvent.AutoFixAdded || !issueEvent.AutoFixRemoved {
		t.Fatalf("unexpected auto-fix flags: %+v", issueEvent)
	}
}

// Gitea 1.21+ 兼容性测试

func TestParser_ParseIssueLabelUpdatedAutoFix(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issue_label_updated_auto_fix.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-6", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent, ok := event.(IssueLabelEvent)
	if !ok {
		t.Fatalf("event type = %T, want IssueLabelEvent", event)
	}
	if issueEvent.Action != "labeled" {
		t.Fatalf("Action = %q, want %q", issueEvent.Action, "labeled")
	}
	if !issueEvent.AutoFixChanged || !issueEvent.AutoFixAdded || issueEvent.AutoFixRemoved {
		t.Fatalf("unexpected auto-fix flags: %+v", issueEvent)
	}
	if issueEvent.Label.Name != "auto-fix" {
		t.Fatalf("Label.Name = %q, want %q", issueEvent.Label.Name, "auto-fix")
	}
}

func TestParser_ParseIssueLabelUpdatedOther(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issue_label_updated_other.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-7", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent := event.(IssueLabelEvent)
	if issueEvent.AutoFixChanged {
		t.Fatalf("AutoFixChanged = true, want false")
	}
	if issueEvent.Action != "labeled" {
		t.Fatalf("Action = %q, want %q", issueEvent.Action, "labeled")
	}
}

func TestParser_ParseIssueLabelCleared(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issue_label_cleared.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-8", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent := event.(IssueLabelEvent)
	if issueEvent.Action != "unlabeled" {
		t.Fatalf("Action = %q, want %q", issueEvent.Action, "unlabeled")
	}
	// label_cleared 清除所有标签，不携带具体标签 → AutoFixChanged = false
	if issueEvent.AutoFixChanged {
		t.Fatalf("AutoFixChanged = true, want false (no label info in label_cleared)")
	}
}

func TestParser_ParseIssueRef(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issue_labeled_auto_fix.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-ref", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent := event.(IssueLabelEvent)
	if issueEvent.Issue.Ref != "feature/user-auth" {
		t.Errorf("Issue.Ref = %q, want %q", issueEvent.Issue.Ref, "feature/user-auth")
	}
}

func TestParser_ParseIssueRefEmpty(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issue_labeled_other.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-ref-empty", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent := event.(IssueLabelEvent)
	if issueEvent.Issue.Ref != "" {
		t.Errorf("Issue.Ref = %q, want empty", issueEvent.Issue.Ref)
	}
}
