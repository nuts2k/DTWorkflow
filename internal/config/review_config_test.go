package config

import "testing"

func TestResolveReviewConfig_NilConfig(t *testing.T) {
	var c *Config
	resolved := c.ResolveReviewConfig("any/repo")
	if resolved.Enabled != nil || resolved.Severity != "" || resolved.IgnorePatterns != nil {
		t.Errorf("nil config 应返回零值 ReviewOverride，得到: %+v", resolved)
	}
}

func TestResolveReviewConfig_FallbackToGlobal(t *testing.T) {
	enabled := true
	cfg := validBaseConfig()
	cfg.Review = ReviewOverride{Enabled: &enabled, Severity: "high", IgnorePatterns: []string{"*.gen.go"}}
	// 无匹配 repo
	resolved := cfg.ResolveReviewConfig("other/repo")
	if resolved.Severity != "high" {
		t.Errorf("Severity = %q, want %q", resolved.Severity, "high")
	}
	if resolved.Enabled == nil || *resolved.Enabled != true {
		t.Errorf("Enabled 应继承全局值 true")
	}
}

func TestResolveReviewConfig_OverrideEnabled(t *testing.T) {
	globalEnabled := true
	repoEnabled := false
	cfg := validBaseConfig()
	cfg.Review = ReviewOverride{Enabled: &globalEnabled, Severity: "high"}
	cfg.Repos = []RepoConfig{{
		Name:   "acme/repo",
		Review: &ReviewOverride{Enabled: &repoEnabled},
	}}

	resolved := cfg.ResolveReviewConfig("acme/repo")
	if resolved.Enabled == nil || *resolved.Enabled != false {
		t.Errorf("Enabled 应被仓库级覆盖为 false")
	}
	// Severity 未覆盖，应保留全局值
	if resolved.Severity != "high" {
		t.Errorf("Severity = %q, want %q（继承全局）", resolved.Severity, "high")
	}
}

func TestResolveReviewConfig_OverrideSeverity(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Review = ReviewOverride{Severity: "high"}
	cfg.Repos = []RepoConfig{{
		Name:   "acme/repo",
		Review: &ReviewOverride{Severity: "low"},
	}}

	resolved := cfg.ResolveReviewConfig("acme/repo")
	if resolved.Severity != "low" {
		t.Errorf("Severity = %q, want %q", resolved.Severity, "low")
	}
}

func TestResolveReviewConfig_RepoNilReview(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Review = ReviewOverride{Severity: "high"}
	cfg.Repos = []RepoConfig{{Name: "acme/repo", Review: nil}}

	resolved := cfg.ResolveReviewConfig("acme/repo")
	// nil Review 不覆盖，应使用全局
	if resolved.Severity != "high" {
		t.Errorf("nil Review 应回退到全局，Severity = %q, want %q", resolved.Severity, "high")
	}
}

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
