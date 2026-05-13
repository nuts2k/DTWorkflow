package iterate

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateFixReport(t *testing.T) {
	fixedTime := time.Date(2026, 5, 13, 10, 30, 0, 0, time.UTC)
	output := FixReviewOutput{
		Fixes: []FixItem{
			{
				File:     "main.go",
				Line:     10,
				IssueRef: "Issue 1",
				Severity: "ERROR",
				Action:   "modified",
				What:     "Added nil check",
				Why:      "Prevent nil pointer dereference",
			},
			{
				File:     "util.go",
				Line:     20,
				IssueRef: "Issue 2",
				Severity: "WARNING",
				Action:   "skipped",
				What:     "Style preference",
				Why:      "Existing pattern is consistent with codebase",
			},
		},
		Summary: "Fixed critical nil pointer issue",
	}
	report := GenerateFixReport(FixReportContext{
		PRNumber:    42,
		RoundNumber: 1,
		Output:      output,
		Repo:        "owner/repo",
		Timestamp:   fixedTime,
	})
	if !strings.Contains(report, "# PR #42") {
		t.Error("report should contain PR number header")
	}
	if !strings.Contains(report, "main.go") {
		t.Error("report should contain file name")
	}
	if !strings.Contains(report, "modified") {
		t.Error("report should contain action")
	}
	if !strings.Contains(report, "skipped") {
		t.Error("report should contain skipped action")
	}
	if !strings.Contains(report, "2026-05-13 10:30:00") {
		t.Error("report should contain the provided timestamp")
	}
}
