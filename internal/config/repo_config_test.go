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
