package webhook

import (
	"encoding/json"
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

func TestIsFixToPRLabel(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"fix-to-pr", true},
		{"Fix-To-PR", true},
		{"FIX-TO-PR", true},
		{"auto-fix", false},
		{"fix-to-pr2", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFixToPRLabel(tc.name); got != tc.want {
				t.Errorf("isFixToPRLabel(%q) = %v, 期望 %v", tc.name, got, tc.want)
			}
		})
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

func TestParseIssue_FixToPRLabel(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issue_labeled_fix_to_pr.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-fix-to-pr", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent, ok := event.(IssueLabelEvent)
	if !ok {
		t.Fatalf("event type = %T, want IssueLabelEvent", event)
	}
	if !issueEvent.FixToPRAdded {
		t.Errorf("FixToPRAdded = false, 期望 true")
	}
	if !issueEvent.FixToPRChanged {
		t.Errorf("FixToPRChanged = false, 期望 true")
	}
	if issueEvent.FixToPRRemoved {
		t.Errorf("FixToPRRemoved = true, 期望 false")
	}
	if issueEvent.AutoFixAdded {
		t.Errorf("AutoFixAdded = true, 期望 false")
	}
	if issueEvent.AutoFixChanged {
		t.Errorf("AutoFixChanged = true, 期望 false")
	}
}

func TestParser_ParseIssueLabelUpdated_MultipleLabelsDoesNotFalseTrigger(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issue_label_updated_auto_fix.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	issue := payload["issue"].(map[string]any)
	issue["labels"] = []map[string]any{
		{"name": "auto-fix", "color": "70c24a"},
		{"name": "bug", "color": "ee0701"},
	}
	payload["issue"] = issue
	body, err = json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-multi-label", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent := event.(IssueLabelEvent)
	if issueEvent.AutoFixChanged || issueEvent.AutoFixAdded || issueEvent.FixToPRChanged || issueEvent.FixToPRAdded {
		t.Fatalf("多标签 label_updated 不应误判为目标标签变更: %+v", issueEvent)
	}
}
