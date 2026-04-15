package cmd

import (
	"context"
	"log/slog"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
)

type noopNotifier struct {
	name string
}

func (n noopNotifier) Name() string {
	return n.name
}

func (n noopNotifier) Send(ctx context.Context, msg notify.Message) error {
	return nil
}

func TestBuildNotifyRules_MapsGlobalRoutes(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "fallback-channel",
			Channels: map[string]config.ChannelConfig{
				"gitea":            {Enabled: true},
				"fallback-channel": {Enabled: true},
			},
			Routes: []config.RouteConfig{{
				Repo:     "*",
				Events:   []string{"*"},
				Channels: []string{"gitea"},
			}},
		},
	}

	rules, fallback := buildNotifyRules(cfg, "acme/repo")
	if fallback != "fallback-channel" {
		t.Fatalf("fallback = %q, want %q", fallback, "fallback-channel")
	}
	if len(rules) != 1 {
		t.Fatalf("rules length = %d, want %d", len(rules), 1)
	}
	if rules[0].RepoPattern != "*" {
		t.Fatalf("rules[0].RepoPattern = %q, want %q", rules[0].RepoPattern, "*")
	}
	if len(rules[0].Channels) != 1 || rules[0].Channels[0] != "gitea" {
		t.Fatalf("rules[0].Channels = %#v, want %#v", rules[0].Channels, []string{"gitea"})
	}
}

func TestBuildNotifyRules_EventsStarMapsToNotifyEventTypeStar(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{
				"gitea": {Enabled: true},
			},
			Routes: []config.RouteConfig{{
				Repo:     "*",
				Events:   []string{"*"},
				Channels: []string{"gitea"},
			}},
		},
	}

	rules, _ := buildNotifyRules(cfg, "acme/repo")
	if len(rules) != 1 {
		t.Fatalf("rules length = %d, want %d", len(rules), 1)
	}
	if len(rules[0].EventTypes) != 1 {
		t.Fatalf("event types length = %d, want %d", len(rules[0].EventTypes), 1)
	}
	if rules[0].EventTypes[0] != notify.EventType("*") {
		t.Fatalf("event type = %q, want %q", rules[0].EventTypes[0], notify.EventType("*"))
	}
}

func TestBuildNotifyRules_RepoOverridePreferred(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{
				"gitea": {Enabled: true},
				"repo":  {Enabled: true},
			},
			Routes: []config.RouteConfig{{
				Repo:     "*",
				Events:   []string{"*"},
				Channels: []string{"gitea"},
			}},
		},
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{Routes: []config.RouteConfig{{
				Repo:     "*",
				Events:   []string{"*"},
				Channels: []string{"repo"},
			}}},
		}},
	}

	rules, _ := buildNotifyRules(cfg, "acme/repo")
	if len(rules) != 1 {
		t.Fatalf("rules length = %d, want %d", len(rules), 1)
	}
	if len(rules[0].Channels) != 1 || rules[0].Channels[0] != "repo" {
		t.Fatalf("rules[0].Channels = %#v, want %#v", rules[0].Channels, []string{"repo"})
	}
}

func TestBuildNotifier_NilConfigOrNilClient(t *testing.T) {
	client, err := gitea.NewClient("https://gitea.example.com", gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	t.Run("nil config", func(t *testing.T) {
		router, err := buildNotifier(nil, client)
		if err != nil {
			t.Fatalf("buildNotifier(nil, client) error: %v", err)
		}
		if router != nil {
			t.Fatal("buildNotifier(nil, client) should return nil router")
		}
	})

	t.Run("nil client", func(t *testing.T) {
		cfg := &config.Config{Notify: config.NotifyConfig{DefaultChannel: "gitea"}}
		router, err := buildNotifier(cfg, nil)
		if err != nil {
			t.Fatalf("buildNotifier(cfg, nil) error: %v", err)
		}
		if router != nil {
			t.Fatal("buildNotifier(cfg, nil) should return nil router")
		}
	})
}

func TestBuildNotifier_WithClientAndConfig(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{
				"gitea": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"gitea"}}},
		},
	}

	client, err := gitea.NewClient("https://gitea.example.com", gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	router, err := buildNotifier(cfg, client)
	if err != nil {
		t.Fatalf("buildNotifier(cfg, client) error: %v", err)
	}
	if router == nil {
		t.Fatal("buildNotifier(cfg, client) should return non-nil router")
	}
}

func TestBuildNotifier_FeishuOnlyConfig(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "feishu",
			Channels: map[string]config.ChannelConfig{
				"feishu": {
					Enabled: true,
					Options: map[string]string{
						"webhook_url": "https://open.feishu.cn/open-apis/bot/v2/hook/test",
					},
				},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"feishu"}}},
		},
	}

	client, err := gitea.NewClient("https://gitea.example.com", gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	router, err := buildNotifier(cfg, client)
	if err != nil {
		t.Fatalf("buildNotifier(cfg, client) error: %v", err)
	}
	if router == nil {
		t.Fatal("buildNotifier(feishu-only) should return non-nil router")
	}
}

func TestConfigDrivenNotifier_ReusesGlobalRouterWithoutRepoOverride(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{
				"gitea": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"gitea"}}},
		},
	}

	n := &configDrivenNotifier{
		cfg:           cfg,
		giteaNotifier: noopNotifier{name: "gitea"},
		logger:        slog.Default(),
	}

	r1, err := n.getRouter("acme/repo1")
	if err != nil {
		t.Fatalf("getRouter(repo1) error: %v", err)
	}
	r2, err := n.getRouter("acme/repo2")
	if err != nil {
		t.Fatalf("getRouter(repo2) error: %v", err)
	}
	if r1 != r2 {
		t.Fatal("expected global router to be reused for repos without override")
	}
	if n.globalRouter == nil {
		t.Fatal("expected globalRouter to be initialized")
	}
	if len(n.routers) != 0 {
		t.Fatalf("override routers length = %d, want 0", len(n.routers))
	}
}

func TestConfigDrivenNotifier_FeishuOnlyRouter(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "feishu",
			Channels: map[string]config.ChannelConfig{
				"feishu": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"feishu"}}},
		},
	}

	n := &configDrivenNotifier{
		cfg:            cfg,
		feishuNotifier: noopNotifier{name: "feishu"},
		logger:         slog.Default(),
	}

	router, err := n.getRouter("acme/repo1")
	if err != nil {
		t.Fatalf("getRouter(repo1) error: %v", err)
	}
	if router == nil {
		t.Fatal("expected feishu-only router to be initialized")
	}
	if err := router.Send(context.Background(), notify.Message{
		EventType: notify.EventPRReviewStarted,
		Target:    notify.Target{Owner: "acme", Repo: "repo1", Number: 1, IsPR: true},
	}); err != nil {
		t.Fatalf("router.Send() error: %v", err)
	}
}

func TestConfigDrivenNotifier_CachesOnlyRepoOverrides(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{
				"gitea": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"gitea"}}},
		},
		Repos: []config.RepoConfig{{
			Name:   "acme/repo1",
			Notify: &config.NotifyOverride{Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"gitea"}}}},
		}},
	}

	n := &configDrivenNotifier{
		cfg:           cfg,
		giteaNotifier: noopNotifier{name: "gitea"},
		logger:        slog.Default(),
	}

	r1, err := n.getRouter("acme/repo1")
	if err != nil {
		t.Fatalf("getRouter(repo1) error: %v", err)
	}
	r2, err := n.getRouter("acme/repo1")
	if err != nil {
		t.Fatalf("getRouter(repo1 second call) error: %v", err)
	}
	if r1 != r2 {
		t.Fatal("expected override router to be cached per repo")
	}
	if len(n.routers) != 1 {
		t.Fatalf("override routers length = %d, want 1", len(n.routers))
	}
	if n.globalRouter != nil {
		t.Fatal("expected globalRouter to remain nil when only override repo was requested")
	}
}

func TestHasRepoNotifyOverride_FeishuOnly(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Feishu: &config.FeishuOverride{WebhookURL: "https://example.com/hook"},
			},
		}},
	}
	n := &configDrivenNotifier{cfg: cfg}

	if !n.hasRepoNotifyOverride("acme/repo") {
		t.Error("仅配置 Feishu 覆盖时应返回 true")
	}
}

func TestHasRepoNotifyOverride_BothRoutesAndFeishu(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Routes: []config.RouteConfig{{Repo: "*"}},
				Feishu: &config.FeishuOverride{WebhookURL: "https://example.com/hook"},
			},
		}},
	}
	n := &configDrivenNotifier{cfg: cfg}

	if !n.hasRepoNotifyOverride("acme/repo") {
		t.Error("Routes 和 Feishu 都配置时应返回 true")
	}
}

func TestHasRepoNotifyOverride_NeitherRoutesNorFeishu(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.RepoConfig{{
			Name:   "acme/repo",
			Notify: &config.NotifyOverride{},
		}},
	}
	n := &configDrivenNotifier{cfg: cfg}

	if n.hasRepoNotifyOverride("acme/repo") {
		t.Error("Routes 和 Feishu 都为 nil 时应返回 false")
	}
}

func TestConfigDrivenNotifier_RepoFeishuOverride_UsesRepoRouter(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "feishu",
			Channels: map[string]config.ChannelConfig{
				"feishu": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"feishu"}}},
		},
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Feishu: &config.FeishuOverride{
					WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo-specific",
				},
			},
		}},
	}

	n := &configDrivenNotifier{
		cfg:            cfg,
		feishuNotifier: noopNotifier{name: "feishu"},
		logger:         slog.Default(),
	}

	// 有仓库级飞书覆盖的仓库应该走 per-repo Router
	r1, err := n.getRouter("acme/repo")
	if err != nil {
		t.Fatalf("getRouter(acme/repo) error: %v", err)
	}

	// 无覆盖的仓库应该走全局 Router
	r2, err := n.getRouter("other/repo")
	if err != nil {
		t.Fatalf("getRouter(other/repo) error: %v", err)
	}

	// 两者应该是不同的 Router 实例
	if r1 == r2 {
		t.Error("有飞书覆盖的仓库应使用独立 Router，但与全局 Router 相同")
	}

	// per-repo Router 应被缓存
	if len(n.routers) != 1 {
		t.Errorf("override routers 数量 = %d, want 1", len(n.routers))
	}
}

func TestConfigDrivenNotifier_RepoFeishuOverride_NoGlobalFeishu(t *testing.T) {
	// 仅有仓库级飞书覆盖，全局无飞书 notifier
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{
				"gitea":  {Enabled: true},
				"feishu": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"gitea"}}},
		},
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Feishu: &config.FeishuOverride{
					WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
				},
			},
		}},
	}

	n := &configDrivenNotifier{
		cfg:           cfg,
		giteaNotifier: noopNotifier{name: "gitea"},
		logger:        slog.Default(),
	}

	router, err := n.getRouter("acme/repo")
	if err != nil {
		t.Fatalf("getRouter error: %v", err)
	}
	if router == nil {
		t.Fatal("router 不应为 nil")
	}
}

func TestConfigDrivenNotifier_RepoFeishuOverride_WithSecret(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "feishu",
			Channels: map[string]config.ChannelConfig{
				"feishu": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"feishu"}}},
		},
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Feishu: &config.FeishuOverride{
					WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
					Secret:     "repo-secret",
				},
			},
		}},
	}

	n := &configDrivenNotifier{
		cfg:            cfg,
		feishuNotifier: noopNotifier{name: "feishu"},
		logger:         slog.Default(),
	}

	// 有 secret 的仓库级覆盖也应正常构造
	router, err := n.getRouter("acme/repo")
	if err != nil {
		t.Fatalf("getRouter error: %v", err)
	}
	if router == nil {
		t.Fatal("router 不应为 nil")
	}
}
