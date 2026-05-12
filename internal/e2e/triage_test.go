package e2e

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseTriageResult_Normal(t *testing.T) {
	raw := `{"modules":[{"name":"order","reason":"changes affect order API"}],"skipped_modules":[{"name":"auth","reason":"no auth changes"}],"analysis":"Order module affected"}`
	out, err := ParseTriageResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Modules) != 1 || out.Modules[0].Name != "order" {
		t.Errorf("modules = %v, want [{order ...}]", out.Modules)
	}
	if len(out.SkippedModules) != 1 {
		t.Errorf("skipped_modules = %v, want 1 entry", out.SkippedModules)
	}
	if out.Analysis != "Order module affected" {
		t.Errorf("analysis = %q", out.Analysis)
	}
}

func TestParseTriageResult_EmptyModules(t *testing.T) {
	raw := `{"modules":[],"analysis":"no changes need regression"}`
	out, err := ParseTriageResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Modules) != 0 {
		t.Errorf("modules should be empty, got %v", out.Modules)
	}
}

func TestParseTriageResult_MissingModulesField(t *testing.T) {
	raw := `{"analysis":"some analysis"}`
	_, err := ParseTriageResult(raw)
	if err == nil {
		t.Fatal("expected error for missing modules field")
	}
	if !errors.Is(err, ErrE2ETriageParseFailure) {
		t.Errorf("error should wrap ErrE2ETriageParseFailure, got: %v", err)
	}
}

func TestParseTriageResult_EmptyModuleName(t *testing.T) {
	raw := `{"modules":[{"name":"","reason":"bad"}]}`
	_, err := ParseTriageResult(raw)
	if err == nil {
		t.Fatal("expected error for empty module name")
	}
	if !errors.Is(err, ErrE2ETriageParseFailure) {
		t.Errorf("error should wrap ErrE2ETriageParseFailure, got: %v", err)
	}
}

func TestParseTriageResult_CLIEnvelope(t *testing.T) {
	inner := `{"modules":[{"name":"payment","reason":"payment API changed"}],"analysis":"done"}`
	raw := `{"result":"` + strings.ReplaceAll(inner, `"`, `\"`) + `","cost_usd":0.01}`
	out, err := ParseTriageResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Modules) != 1 || out.Modules[0].Name != "payment" {
		t.Errorf("modules = %v, want [{payment ...}]", out.Modules)
	}
}

func TestSanitizeTriageOutput_Truncation(t *testing.T) {
	longText := strings.Repeat("a", 3000)
	raw := `{"modules":[],"analysis":"` + longText + `"}`
	out, err := ParseTriageResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Analysis) > maxTriageAnalysisLen+20 {
		t.Errorf("analysis should be truncated, got len=%d", len(out.Analysis))
	}
}

func TestBuildTriagePrompt_Basic(t *testing.T) {
	prompt := BuildTriagePrompt("org/repo", "main", []string{"src/api/order.go", "src/service/order.go"})
	if !strings.Contains(prompt, "org/repo") {
		t.Error("prompt should contain repo name")
	}
	if !strings.Contains(prompt, "src/api/order.go") {
		t.Error("prompt should contain changed files")
	}
	if !strings.Contains(prompt, "READ-ONLY") {
		t.Error("prompt should contain READ-ONLY constraint")
	}
}

func TestBuildTriagePromptWithContext_ExactDiffCommands(t *testing.T) {
	prompt := BuildTriagePromptWithContext(TriagePromptContext{
		Repo:           "org/repo",
		BaseRef:        "main",
		BaseSHA:        "base123",
		HeadSHA:        "head456",
		MergeCommitSHA: "merge789",
		ChangedFiles:   []string{"src/api/order.go"},
	})
	for _, want := range []string{
		"Base SHA before merge: base123",
		"PR head SHA: head456",
		"Merged commit SHA: merge789",
		"git diff base123...head456",
		"git diff merge789^1 merge789",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt should contain %q", want)
		}
	}
}

func TestBuildTriagePrompt_Truncation(t *testing.T) {
	files := make([]string, 60)
	for i := range files {
		files[i] = fmt.Sprintf("file_%d.go", i)
	}
	prompt := BuildTriagePrompt("org/repo", "main", files)
	if !strings.Contains(prompt, "truncated") {
		t.Error("prompt should mention truncation for >50 files")
	}
}
