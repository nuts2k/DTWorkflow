package config

import "testing"

// boolPtr 便于构造指针常量，提升 test 表达力。
func boolPtr(v bool) *bool {
	return &v
}

func TestResolveNotifyRoutes_NilConfig(t *testing.T) {
	var c *Config
	routes := c.ResolveNotifyRoutes("any/repo")
	if routes != nil {
		t.Errorf("nil config 应返回 nil，得到: %v", routes)
	}
}

func TestResolveNotifyRoutes_NoMatchingRepo(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Routes = []RouteConfig{{Repo: "*", Channels: []string{"gitea"}}}
	cfg.Repos = []RepoConfig{{
		Name:   "other/repo",
		Notify: &NotifyOverride{Routes: []RouteConfig{{Repo: "*", Channels: []string{"other"}}}},
	}}

	// 不匹配的 repo 应回退到全局
	routes := cfg.ResolveNotifyRoutes("acme/repo")
	if len(routes) != 1 || routes[0].Channels[0] != "gitea" {
		t.Errorf("不匹配 repo 应回退全局路由，得到: %v", routes)
	}
}

func TestResolveNotifyRoutes_RepoNoNotify(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Routes = []RouteConfig{{Repo: "*", Channels: []string{"gitea"}}}
	cfg.Repos = []RepoConfig{{Name: "acme/repo", Notify: nil}}

	// Notify 为 nil 应回退全局
	routes := cfg.ResolveNotifyRoutes("acme/repo")
	if len(routes) != 1 || routes[0].Channels[0] != "gitea" {
		t.Errorf("nil Notify 应回退全局路由，得到: %v", routes)
	}
}

func TestResolveNotifyRoutes_RepoOverridePreferred(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Routes = []RouteConfig{{Repo: "*", Channels: []string{"gitea"}}}
	cfg.Repos = []RepoConfig{{
		Name:   "acme/repo",
		Notify: &NotifyOverride{Routes: []RouteConfig{{Repo: "*", Channels: []string{"repo"}}}},
	}}

	routes := cfg.ResolveNotifyRoutes("acme/repo")
	if len(routes) != 1 {
		t.Fatalf("routes length = %d, want %d", len(routes), 1)
	}
	if len(routes[0].Channels) != 1 || routes[0].Channels[0] != "repo" {
		t.Fatalf("routes[0].Channels = %#v, want %#v", routes[0].Channels, []string{"repo"})
	}
}

func TestResolveNotifyRoutes_FallbackToGlobal(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Routes = []RouteConfig{{Repo: "*", Channels: []string{"gitea"}}}
	cfg.Repos = []RepoConfig{{
		Name:   "acme/repo",
		Notify: &NotifyOverride{Routes: nil},
	}}

	routes := cfg.ResolveNotifyRoutes("acme/repo")
	if len(routes) != 1 {
		t.Fatalf("routes length = %d, want %d", len(routes), 1)
	}
	if len(routes[0].Channels) != 1 || routes[0].Channels[0] != "gitea" {
		t.Fatalf("routes[0].Channels = %#v, want %#v", routes[0].Channels, []string{"gitea"})
	}
}

func TestResolveNotifyRoutes_OverrideEmptyClearsGlobal(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Routes = []RouteConfig{{Repo: "*", Channels: []string{"gitea"}}}
	cfg.Repos = []RepoConfig{{
		Name:   "acme/repo",
		Notify: &NotifyOverride{Routes: []RouteConfig{}},
	}}

	routes := cfg.ResolveNotifyRoutes("acme/repo")
	if routes == nil {
		t.Fatalf("routes is nil, want empty slice")
	}
	if len(routes) != 0 {
		t.Fatalf("routes length = %d, want %d", len(routes), 0)
	}
}

func TestResolveReviewConfig_M22Fields(t *testing.T) {
	t.Run("全局TechStack无仓库覆盖时返回全局值", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.TechStack = []string{"go", "vue"}
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if len(resolved.TechStack) != 2 || resolved.TechStack[0] != "go" {
			t.Errorf("TechStack = %v, want [go vue]", resolved.TechStack)
		}
	})

	t.Run("仓库级TechStack覆盖全局值", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.TechStack = []string{"go", "vue"}
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Review: &ReviewOverride{TechStack: []string{"java"}},
		}}
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if len(resolved.TechStack) != 1 || resolved.TechStack[0] != "java" {
			t.Errorf("TechStack = %v, want [java]", resolved.TechStack)
		}
	})

	t.Run("全局CodeStandardsPaths无仓库覆盖时返回全局值", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.CodeStandardsPaths = []string{"docs/standards.md"}
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if len(resolved.CodeStandardsPaths) != 1 || resolved.CodeStandardsPaths[0] != "docs/standards.md" {
			t.Errorf("CodeStandardsPaths = %v, want [docs/standards.md]", resolved.CodeStandardsPaths)
		}
	})

	t.Run("仓库级CodeStandardsPaths覆盖全局值", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.CodeStandardsPaths = []string{"docs/standards.md"}
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Review: &ReviewOverride{CodeStandardsPaths: []string{"repo/rules.md"}},
		}}
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if len(resolved.CodeStandardsPaths) != 1 || resolved.CodeStandardsPaths[0] != "repo/rules.md" {
			t.Errorf("CodeStandardsPaths = %v, want [repo/rules.md]", resolved.CodeStandardsPaths)
		}
	})

	t.Run("未设置时TechStack和CodeStandardsPaths均为nil", func(t *testing.T) {
		cfg := validBaseConfig()
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if resolved.TechStack != nil {
			t.Errorf("TechStack 未设置时应为 nil，得到: %v", resolved.TechStack)
		}
		if resolved.CodeStandardsPaths != nil {
			t.Errorf("CodeStandardsPaths 未设置时应为 nil，得到: %v", resolved.CodeStandardsPaths)
		}
	})
}

func TestResolveReviewConfig_ModelEffort(t *testing.T) {
	t.Run("claude.model 作为全局默认填充", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Claude.Model = "claude-sonnet-4-6"
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if resolved.Model != "claude-sonnet-4-6" {
			t.Errorf("Model = %q, want %q", resolved.Model, "claude-sonnet-4-6")
		}
	})

	t.Run("review.model 覆盖全局 claude.model", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Claude.Model = "claude-sonnet-4-6"
		cfg.Review.Model = "claude-opus-4-6"
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if resolved.Model != "claude-opus-4-6" {
			t.Errorf("Model = %q, want %q", resolved.Model, "claude-opus-4-6")
		}
	})

	t.Run("仓库级 review.model 覆盖全局和顶级 review.model", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Claude.Model = "claude-sonnet-4-6"
		cfg.Review.Model = "claude-opus-4-6"
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Review: &ReviewOverride{Model: "claude-haiku-4-5"},
		}}
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if resolved.Model != "claude-haiku-4-5" {
			t.Errorf("Model = %q, want %q", resolved.Model, "claude-haiku-4-5")
		}
	})

	t.Run("claude.effort 作为全局默认填充", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Claude.Effort = "high"
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if resolved.Effort != "high" {
			t.Errorf("Effort = %q, want %q", resolved.Effort, "high")
		}
	})

	t.Run("review.effort 覆盖全局 claude.effort", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Claude.Effort = "high"
		cfg.Review.Effort = "medium"
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if resolved.Effort != "medium" {
			t.Errorf("Effort = %q, want %q", resolved.Effort, "medium")
		}
	})

	t.Run("仓库级 review.effort 覆盖全局和顶级 review.effort", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Claude.Effort = "high"
		cfg.Review.Effort = "medium"
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Review: &ReviewOverride{Effort: "low"},
		}}
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if resolved.Effort != "low" {
			t.Errorf("Effort = %q, want %q", resolved.Effort, "low")
		}
	})

	t.Run("仓库级空 effort 不覆盖全局", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Claude.Effort = "high"
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Review: &ReviewOverride{Effort: ""},
		}}
		resolved := cfg.ResolveReviewConfig("acme/repo")
		if resolved.Effort != "high" {
			t.Errorf("Effort = %q, want %q（空仓库级 effort 不应覆盖全局）", resolved.Effort, "high")
		}
	})
}

func TestResolveFeishuOverride(t *testing.T) {
	t.Run("nil config 返回 nil", func(t *testing.T) {
		var c *Config
		if got := c.ResolveFeishuOverride("any/repo"); got != nil {
			t.Errorf("nil config 应返回 nil，得到: %+v", got)
		}
	})

	t.Run("无匹配仓库返回 nil", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "other/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: "https://example.com/hook"}},
		}}
		if got := cfg.ResolveFeishuOverride("acme/repo"); got != nil {
			t.Errorf("不匹配 repo 应返回 nil，得到: %+v", got)
		}
	})

	t.Run("匹配仓库但 Notify 为 nil 返回 nil", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{Name: "acme/repo", Notify: nil}}
		if got := cfg.ResolveFeishuOverride("acme/repo"); got != nil {
			t.Errorf("nil Notify 应返回 nil，得到: %+v", got)
		}
	})

	t.Run("匹配仓库但 Feishu 为 nil 返回 nil", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Routes: []RouteConfig{{Repo: "*"}}},
		}}
		if got := cfg.ResolveFeishuOverride("acme/repo"); got != nil {
			t.Errorf("nil Feishu 应返回 nil，得到: %+v", got)
		}
	})

	t.Run("匹配仓库有 Feishu 覆盖时返回覆盖", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{
				WebhookURL: "https://example.com/hook/repo",
				Secret:     "repo-secret",
			}},
		}}
		got := cfg.ResolveFeishuOverride("acme/repo")
		if got == nil {
			t.Fatal("有覆盖时不应返回 nil")
		}
		if got.WebhookURL != "https://example.com/hook/repo" {
			t.Errorf("WebhookURL = %q, want %q", got.WebhookURL, "https://example.com/hook/repo")
		}
		if got.Secret != "repo-secret" {
			t.Errorf("Secret = %q, want %q", got.Secret, "repo-secret")
		}
	})

	t.Run("多仓库匹配返回第一个", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{
			{Name: "acme/repo", Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: "https://first"}}},
			{Name: "acme/repo", Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: "https://second"}}},
		}
		got := cfg.ResolveFeishuOverride("acme/repo")
		if got == nil || got.WebhookURL != "https://first" {
			t.Errorf("重复 repo 应返回第一个匹配项，得到: %+v", got)
		}
	})
}

func TestResolveTestGenConfig(t *testing.T) {
	t.Run("nil config 返回零值", func(t *testing.T) {
		var c *Config
		got := c.ResolveTestGenConfig("any/repo")
		want := TestGenOverride{}
		if got != want {
			t.Errorf("nil config 应返回零值 TestGenOverride，得到: %+v", got)
		}
	})

	t.Run("无仓库匹配时返回全局 TestGen", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{
			Enabled:        boolPtr(true),
			ModuleScope:    "backend",
			MaxRetryRounds: 5,
			TestFramework:  "junit5",
		}
		cfg.Repos = []RepoConfig{{
			Name:    "other/repo",
			TestGen: &TestGenOverride{ModuleScope: "frontend"},
		}}
		got := cfg.ResolveTestGenConfig("acme/repo")
		if got.ModuleScope != "backend" {
			t.Errorf("ModuleScope = %q, want %q", got.ModuleScope, "backend")
		}
		if got.MaxRetryRounds != 5 {
			t.Errorf("MaxRetryRounds = %d, want %d", got.MaxRetryRounds, 5)
		}
		if got.TestFramework != "junit5" {
			t.Errorf("TestFramework = %q, want %q", got.TestFramework, "junit5")
		}
		if got.Enabled == nil || !*got.Enabled {
			t.Errorf("Enabled = %v, want *true", got.Enabled)
		}
	})

	t.Run("仓库 TestGen 为 nil 时返回全局", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{
			ModuleScope:    "backend",
			MaxRetryRounds: 5,
		}
		cfg.Repos = []RepoConfig{{Name: "acme/repo", TestGen: nil}}
		got := cfg.ResolveTestGenConfig("acme/repo")
		if got.ModuleScope != "backend" {
			t.Errorf("ModuleScope = %q, want %q", got.ModuleScope, "backend")
		}
		if got.MaxRetryRounds != 5 {
			t.Errorf("MaxRetryRounds = %d, want %d", got.MaxRetryRounds, 5)
		}
	})

	t.Run("仓库 TestGen 非零字段覆盖", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{
			ModuleScope:    "backend",
			MaxRetryRounds: 3,
			TestFramework:  "junit5",
		}
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			TestGen: &TestGenOverride{
				ModuleScope:    "services/api",
				MaxRetryRounds: 7,
				TestFramework:  "vitest",
			},
		}}
		got := cfg.ResolveTestGenConfig("acme/repo")
		if got.ModuleScope != "services/api" {
			t.Errorf("ModuleScope = %q, want %q", got.ModuleScope, "services/api")
		}
		if got.MaxRetryRounds != 7 {
			t.Errorf("MaxRetryRounds = %d, want %d", got.MaxRetryRounds, 7)
		}
		if got.TestFramework != "vitest" {
			t.Errorf("TestFramework = %q, want %q", got.TestFramework, "vitest")
		}
	})

	t.Run("Enabled 指针语义：nil 保留全局 *true", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{Enabled: boolPtr(true)}
		cfg.Repos = []RepoConfig{{
			Name:    "acme/repo",
			TestGen: &TestGenOverride{Enabled: nil},
		}}
		got := cfg.ResolveTestGenConfig("acme/repo")
		if got.Enabled == nil || !*got.Enabled {
			t.Errorf("Enabled = %v，nil 应保留全局 *true", got.Enabled)
		}
	})

	t.Run("Enabled 指针语义：*false 覆盖全局 *true", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{Enabled: boolPtr(true)}
		cfg.Repos = []RepoConfig{{
			Name:    "acme/repo",
			TestGen: &TestGenOverride{Enabled: boolPtr(false)},
		}}
		got := cfg.ResolveTestGenConfig("acme/repo")
		if got.Enabled == nil || *got.Enabled {
			t.Errorf("Enabled = %v，*false 应覆盖全局 *true", got.Enabled)
		}
	})

	t.Run("Enabled 指针语义：*true 覆盖全局 *false", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{Enabled: boolPtr(false)}
		cfg.Repos = []RepoConfig{{
			Name:    "acme/repo",
			TestGen: &TestGenOverride{Enabled: boolPtr(true)},
		}}
		got := cfg.ResolveTestGenConfig("acme/repo")
		if got.Enabled == nil || !*got.Enabled {
			t.Errorf("Enabled = %v，*true 应覆盖全局 *false", got.Enabled)
		}
	})

	t.Run("仓库空字符串不覆盖 ModuleScope", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{ModuleScope: "backend"}
		cfg.Repos = []RepoConfig{{
			Name:    "acme/repo",
			TestGen: &TestGenOverride{ModuleScope: ""},
		}}
		got := cfg.ResolveTestGenConfig("acme/repo")
		if got.ModuleScope != "backend" {
			t.Errorf("ModuleScope = %q，空字符串不应覆盖全局", got.ModuleScope)
		}
	})

	t.Run("仓库空字符串不覆盖 TestFramework", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{TestFramework: "junit5"}
		cfg.Repos = []RepoConfig{{
			Name:    "acme/repo",
			TestGen: &TestGenOverride{TestFramework: ""},
		}}
		got := cfg.ResolveTestGenConfig("acme/repo")
		if got.TestFramework != "junit5" {
			t.Errorf("TestFramework = %q，空字符串不应覆盖全局", got.TestFramework)
		}
	})

	t.Run("MaxRetryRounds=0 不覆盖全局", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{MaxRetryRounds: 5}
		cfg.Repos = []RepoConfig{{
			Name:    "acme/repo",
			TestGen: &TestGenOverride{MaxRetryRounds: 0},
		}}
		got := cfg.ResolveTestGenConfig("acme/repo")
		if got.MaxRetryRounds != 5 {
			t.Errorf("MaxRetryRounds = %d，0 应视为未设置不覆盖全局", got.MaxRetryRounds)
		}
	})

	t.Run("MaxRetryRounds > 0 覆盖全局", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{MaxRetryRounds: 3}
		cfg.Repos = []RepoConfig{{
			Name:    "acme/repo",
			TestGen: &TestGenOverride{MaxRetryRounds: 8},
		}}
		got := cfg.ResolveTestGenConfig("acme/repo")
		if got.MaxRetryRounds != 8 {
			t.Errorf("MaxRetryRounds = %d, want 8", got.MaxRetryRounds)
		}
	})

	t.Run("多仓库匹配返回第一个", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{ModuleScope: "global"}
		cfg.Repos = []RepoConfig{
			{Name: "acme/repo", TestGen: &TestGenOverride{ModuleScope: "first"}},
			{Name: "acme/repo", TestGen: &TestGenOverride{ModuleScope: "second"}},
		}
		got := cfg.ResolveTestGenConfig("acme/repo")
		if got.ModuleScope != "first" {
			t.Errorf("ModuleScope = %q，重复仓库应返回第一个匹配项", got.ModuleScope)
		}
	})
}
