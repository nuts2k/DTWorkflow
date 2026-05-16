package code

import (
	"strings"
	"testing"
)

func TestBuildCodeFromDocPrompt(t *testing.T) {
	ctx := PromptContext{
		Owner:          "myorg",
		Repo:           "backend",
		Branch:         "auto-code/user-auth",
		BaseRef:        "main",
		DocPath:        "docs/plans/user-auth-design.md",
		MaxRetryRounds: 5,
	}
	prompt := BuildCodeFromDocPrompt(ctx)

	checks := []string{
		"myorg/backend",
		"auto-code/user-auth",
		"docs/plans/user-auth-design.md",
		"up to 5 rounds",
		"Do NOT run git push",
		"success",
		"failure_category",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("prompt missing %q", check)
		}
	}
}

func TestBuildCodeFromDocPrompt_DefaultRetryRounds(t *testing.T) {
	ctx := PromptContext{
		Owner:   "o",
		Repo:    "r",
		Branch:  "b",
		BaseRef: "main",
		DocPath: "doc.md",
	}
	prompt := BuildCodeFromDocPrompt(ctx)
	if !strings.Contains(prompt, "up to 3 rounds") {
		t.Error("expected default 3 rounds")
	}
}
