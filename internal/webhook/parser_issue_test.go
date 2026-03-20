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
