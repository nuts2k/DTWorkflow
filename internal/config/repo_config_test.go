package config

import "testing"

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
