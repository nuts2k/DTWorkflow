package iterate

import (
	"strings"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
)

func TestBuildFixPrompt_Basic(t *testing.T) {
	issues := []review.ReviewIssue{
		{File: "main.go", Line: 10, Severity: "ERROR", Message: "nil pointer"},
	}
	prompt := BuildFixPrompt(FixPromptContext{
		Repo:        "owner/repo",
		PRNumber:    42,
		HeadRef:     "feature",
		BaseRef:     "main",
		Issues:      issues,
		ReportPath:  "docs/review_history/42-2026-05-13-round1.md",
		RoundNumber: 1,
		MaxRounds:   3,
	})
	if !strings.Contains(prompt, "owner/repo") {
		t.Error("prompt should contain repo name")
	}
	if !strings.Contains(prompt, "main.go") {
		t.Error("prompt should contain issue file")
	}
	if !strings.Contains(prompt, "nil pointer") {
		t.Error("prompt should contain issue message")
	}
	if !strings.Contains(prompt, "round1.md") {
		t.Error("prompt should contain report path")
	}
}

func TestBuildFixPrompt_WithPreviousFixes(t *testing.T) {
	prompt := BuildFixPrompt(FixPromptContext{
		Repo:    "owner/repo",
		PRNumber: 42,
		HeadRef: "feature",
		BaseRef: "main",
		Issues:  []review.ReviewIssue{{File: "a.go", Severity: "ERROR", Message: "bug"}},
		ReportPath:  "docs/review_history/42-2026-05-13-round2.md",
		RoundNumber: 2,
		MaxRounds:   3,
		PreviousFixes: []FixSummary{
			{Round: 1, IssuesFixed: 2, Summary: "Fixed nil checks"},
		},
	})
	if !strings.Contains(prompt, "Fixed nil checks") {
		t.Error("prompt should contain previous fix summary")
	}
	if !strings.Contains(prompt, "Round 1") {
		t.Error("prompt should reference round number")
	}
}
