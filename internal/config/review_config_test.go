package config

import "testing"

func TestResolveReviewConfig_OverrideEmptyIgnorePatternsClearsGlobal(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Review.IgnorePatterns = []string{"*.gen.go"}

	cfg.Repos = []RepoConfig{{
		Name: "acme/repo",
		Review: &ReviewOverride{
			IgnorePatterns: []string{},
		},
	}}

	resolved := cfg.ResolveReviewConfig("acme/repo")
	if resolved.IgnorePatterns == nil {
		t.Fatalf("IgnorePatterns is nil, want empty slice")
	}
	if len(resolved.IgnorePatterns) != 0 {
		t.Fatalf("IgnorePatterns length = %d, want %d", len(resolved.IgnorePatterns), 0)
	}
}
