package cmd

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestBuildNotifier_GenTestsRepoScopedMessageSkipsGitea(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{
				"gitea": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"gitea"}}},
		},
	}
	client, err := gitea.NewClient(srv.URL, gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	notifier, err := buildNotifier(cfg, client)
	if err != nil {
		t.Fatalf("buildNotifier error: %v", err)
	}

	err = notifier.Send(context.Background(), notify.Message{
		EventType: notify.EventGenTestsDone,
		Target:    notify.Target{Owner: "acme", Repo: "repo"},
	})
	if err != nil {
		t.Fatalf("repo 级 gen_tests 消息不应因 gitea 渠道报错: %v", err)
	}
	if calls != 0 {
		t.Fatalf("repo 级 gen_tests 消息应跳过 Gitea 评论 API，实际请求 %d 次", calls)
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

func TestBuildNotifier_RepoFeishuOverrideWithoutGlobalWebhook(t *testing.T) {
	repoCalled := make(chan struct{}, 1)
	repoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repoCalled <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer repoServer.Close()

	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "",
			Channels: map[string]config.ChannelConfig{
				"feishu": {Enabled: true, Options: map[string]string{}},
			},
		},
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Routes: []config.RouteConfig{{Repo: "acme/repo", Events: []string{"*"}, Channels: []string{"feishu"}}},
				Feishu: &config.FeishuOverride{WebhookURL: repoServer.URL},
			},
		}},
	}

	notifier, err := buildNotifier(cfg, nil)
	if err != nil {
		t.Fatalf("buildNotifier(repo override only) error: %v", err)
	}
	if notifier == nil {
		t.Fatal("buildNotifier(repo override only) should return non-nil notifier")
	}

	err = notifier.Send(context.Background(), notify.Message{
		EventType: notify.EventPRReviewDone,
		Target:    notify.Target{Owner: "acme", Repo: "repo", Number: 1, IsPR: true},
	})
	if err != nil {
		t.Fatalf("notifier.Send() error: %v", err)
	}

	select {
	case <-repoCalled:
	default:
		t.Fatal("expected repo-specific feishu webhook to receive a request")
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

func TestConfigDrivenNotifier_RepoFeishuOverride_SendUsesRepoWebhook(t *testing.T) {
	globalCalled := make(chan struct{}, 1)
	globalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		globalCalled <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer globalServer.Close()

	repoCalled := make(chan struct{}, 1)
	repoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repoCalled <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer repoServer.Close()

	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "feishu",
			Channels: map[string]config.ChannelConfig{
				"feishu": {
					Enabled: true,
					Options: map[string]string{
						"webhook_url": globalServer.URL,
					},
				},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"feishu"}}},
		},
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Feishu: &config.FeishuOverride{
					WebhookURL: repoServer.URL,
				},
			},
		}},
	}

	notifier, err := buildNotifier(cfg, nil)
	if err != nil {
		t.Fatalf("buildNotifier(cfg, nil) error: %v", err)
	}
	if notifier == nil {
		t.Fatal("buildNotifier(cfg, nil) should return non-nil notifier")
	}

	err = notifier.Send(context.Background(), notify.Message{
		EventType: notify.EventPRReviewDone,
		Target:    notify.Target{Owner: "acme", Repo: "repo", Number: 1, IsPR: true},
	})
	if err != nil {
		t.Fatalf("notifier.Send() error: %v", err)
	}

	select {
	case <-repoCalled:
	default:
		t.Fatal("expected repo-specific feishu webhook to receive a request")
	}

	select {
	case <-globalCalled:
		t.Fatal("global feishu webhook should not be used when repo override exists")
	default:
	}
}

func TestHasAnyRepoFeishuOverride(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Feishu: &config.FeishuOverride{WebhookURL: "https://example.com/hook"},
			},
		}},
	}

	if !hasAnyRepoFeishuOverride(cfg) {
		t.Fatal("expected repo feishu override to be detected")
	}

	cfg = &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{
				"feishu": {Enabled: true, Options: map[string]string{}},
			},
		},
	}

	if hasAnyRepoFeishuOverride(cfg) {
		t.Fatal("expected no repo feishu override")
	}
}
