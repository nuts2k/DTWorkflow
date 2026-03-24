package config

import "testing"

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
