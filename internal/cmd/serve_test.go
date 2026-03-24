package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// getFreePort 获取一个可用的随机端口
func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("获取空闲端口失败: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// skipIfNoRedis 如果本地 Redis 不可用则跳过测试
func skipIfNoRedis(t *testing.T, addr string) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer func() { _ = rdb.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("跳过测试：本地 Redis (%s) 不可用: %v", addr, err)
	}
}

func skipIfNoDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := worker.NewDockerClient()
	if err != nil {
		t.Skipf("跳过测试：Docker 不可用: %v", err)
		return
	}
	defer func() { _ = c.Close() }()

	// 通过一次轻量的 ImageExists 调用验证 daemon 可达。
	// 该调用在镜像不存在时返回 (false, nil)，属于正常情况。
	if _, err := c.ImageExists(ctx, "dtworkflow-worker:1.0"); err != nil {
		t.Skipf("跳过测试：Docker 不可用: %v", err)
	}
}

// newTestConfig 构造测试用的 serveConfig，使用独立的随机端口
func newTestConfig(t *testing.T) serveConfig {
	t.Helper()
	return serveConfig{
		Host:          "127.0.0.1",
		Port:          getFreePort(t),
		RedisAddr:     "localhost:6379",
		RedisDB:       0,
		DBPath:        t.TempDir() + "/test.db",
		WebhookSecret: "secret",
		ClaudeAPIKey:  "test-api-key",
		GiteaURL:      "https://gitea.example.com",
		GiteaToken:    "test-token",
		MaxWorkers:    1,
		WorkerImage:   "dtworkflow-worker:1.0",
		NetworkName:   "dtworkflow-net",
	}
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

func TestBuildNotifier_UnsupportedDefaultChannelReturnsError(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "fallback-channel",
			Channels: map[string]config.ChannelConfig{
				"gitea":            {Enabled: true},
				"fallback-channel": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"gitea"}}},
		},
	}

	client, err := gitea.NewClient("https://gitea.example.com", gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	_, err = buildNotifier(cfg, client)
	if err == nil {
		t.Fatal("buildNotifier should return error when default_channel is unsupported")
	}
	if !strings.Contains(err.Error(), "notify.default_channel") {
		t.Fatalf("error = %v, want message containing notify.default_channel", err)
	}
}

func TestBuildNotifier_RoutesReferenceNonGiteaChannelReturnsError(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{
				"gitea": {Enabled: true},
				"repo":  {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"repo"}}},
		},
	}

	client, err := gitea.NewClient("https://gitea.example.com", gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	_, err = buildNotifier(cfg, client)
	if err == nil {
		t.Fatal("buildNotifier should return error when routes reference non-gitea channel")
	}
	if !strings.Contains(err.Error(), "notify.routes") {
		t.Fatalf("error = %v, want message containing notify.routes", err)
	}
}

func TestComputeReadyStatus_DegradedWhenWorkerImageMissing(t *testing.T) {
	payload, httpStatus := computeReadyStatus(readinessSnapshot{
		RedisOK:            true,
		SQLiteOK:           true,
		GiteaConfigured:    true,
		NotifierEnabled:    true,
		WorkerImagePresent: false,
		ActiveWorkers:      0,
	})
	if httpStatus != http.StatusServiceUnavailable {
		t.Fatalf("http status = %d, want %d", httpStatus, http.StatusServiceUnavailable)
	}
	if payload["worker_image_present"] != false {
		t.Fatalf("worker_image_present = %v, want false", payload["worker_image_present"])
	}
}

func TestBuildServiceDeps_WithoutGiteaConfig_ReturnsError(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.GiteaURL = ""
	cfg.GiteaToken = ""
	// 当前版本 Worker 执行依赖 Gitea 配置，因此缺失时必须硬失败。
	deps, cleanup, err := BuildServiceDeps(cfg)
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("BuildServiceDeps should return error when Gitea config is absent")
	}
	if deps != nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("deps should be nil on error")
	}
	if cleanup != nil {
		cleanup()
	}
	if !strings.Contains(err.Error(), "gitea-url") {
		t.Fatalf("error=%v, want contains %q", err, "gitea-url")
	}
}

func TestBuildServiceDeps_WithGiteaConfig_BuildsNotifier(t *testing.T) {
	cfg := newTestConfig(t)
	skipIfNoRedis(t, cfg.RedisAddr)
	skipIfNoDocker(t)
	cfg.GiteaURL = "https://gitea.example.com"
	cfg.GiteaToken = "test-token"
	cfg.AppCfg = &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{
				"gitea": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"gitea"}}},
		},
	}
	deps, cleanup, err := BuildServiceDeps(cfg)
	if err != nil {
		t.Fatalf("BuildServiceDeps error: %v", err)
	}
	defer cleanup()
	if deps.Notifier == nil {
		t.Fatal("deps.Notifier should be non-nil when Gitea config is present")
	}
	if !deps.GiteaConfigured {
		t.Fatal("deps.GiteaConfigured should be true when Gitea config is present")
	}
	if !deps.NotifierEnabled {
		t.Fatal("deps.NotifierEnabled should be true when notifier is present")
	}
}

func TestBuildServeConfigFromManager_ReadsAllRequiredFields(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "dtworkflow.yaml")
	content := "server:\n  host: \"127.0.0.1\"\n  port: 18080\n" +
		"redis:\n  addr: \"redis:6379\"\n  db: 2\n" +
		"database:\n  path: \"data/test.db\"\n" +
		"webhook:\n  secret: \"wh\"\n" +
		"claude:\n  api_key: \"ck\"\n" +
		"gitea:\n  url: \"https://gitea.example.com\"\n  token: \"gt\"\n" +
		"worker:\n  concurrency: 9\n  image: \"dtworkflow-worker:9.9\"\n  cpu_limit: \"3.5\"\n  memory_limit: \"8g\"\n  network_name: \"custom-net\"\n" +
		"notify:\n  default_channel: \"gitea\"\n  channels:\n    gitea:\n      enabled: true\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}

	mgr, err := config.NewManager(config.WithConfigFile(cfgPath), config.WithDefaults())
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load error: %v", err)
	}

	sc, err := buildServeConfigFromManager(mgr)
	if err != nil {
		t.Fatalf("buildServeConfigFromManager error: %v", err)
	}
	if sc.Host != "127.0.0.1" {
		t.Fatalf("Host=%q, want %q", sc.Host, "127.0.0.1")
	}
	if sc.Port != 18080 {
		t.Fatalf("Port=%d, want %d", sc.Port, 18080)
	}
	if sc.RedisAddr != "redis:6379" {
		t.Fatalf("RedisAddr=%q, want %q", sc.RedisAddr, "redis:6379")
	}
	if sc.RedisDB != 2 {
		t.Fatalf("RedisDB=%d, want %d", sc.RedisDB, 2)
	}
	if sc.DBPath != "data/test.db" {
		t.Fatalf("DBPath=%q, want %q", sc.DBPath, "data/test.db")
	}
	if sc.WebhookSecret != "wh" {
		t.Fatalf("WebhookSecret=%q, want %q", sc.WebhookSecret, "wh")
	}
	if sc.ClaudeAPIKey != "ck" {
		t.Fatalf("ClaudeAPIKey=%q, want %q", sc.ClaudeAPIKey, "ck")
	}
	if sc.GiteaURL != "https://gitea.example.com" {
		t.Fatalf("GiteaURL=%q, want %q", sc.GiteaURL, "https://gitea.example.com")
	}
	if sc.GiteaToken != "gt" {
		t.Fatalf("GiteaToken=%q, want %q", sc.GiteaToken, "gt")
	}
	if sc.MaxWorkers != 9 {
		t.Fatalf("MaxWorkers=%d, want %d", sc.MaxWorkers, 9)
	}
	if sc.WorkerImage != "dtworkflow-worker:9.9" {
		t.Fatalf("WorkerImage=%q, want %q", sc.WorkerImage, "dtworkflow-worker:9.9")
	}
	if sc.CPULimit != "3.5" {
		t.Fatalf("CPULimit=%q, want %q", sc.CPULimit, "3.5")
	}
	if sc.MemoryLimit != "8g" {
		t.Fatalf("MemoryLimit=%q, want %q", sc.MemoryLimit, "8g")
	}
	if sc.NetworkName != "custom-net" {
		t.Fatalf("NetworkName=%q, want %q", sc.NetworkName, "custom-net")
	}
	if sc.AppCfg == nil {
		t.Fatalf("AppCfg should not be nil")
	}
	if sc.AppCfg.Server.Port != 18080 {
		t.Fatalf("AppCfg.Server.Port=%d, want %d", sc.AppCfg.Server.Port, 18080)
	}
}

func TestRunServe_UsesConfigManagerSnapshot(t *testing.T) {
	// 证明点：runServe 应从 cfgManager.Get() 构造 serveConfig，而不是依赖包级变量。
	// 这里将 cfgManager 中的 server.port 设置为非法值（70000），并将全局 servePort
	// 设为合法值（8080）。若 runServe 仍使用全局变量，将不会触发端口范围校验。

	cfgPath := filepath.Join(t.TempDir(), "dtworkflow.yaml")
	content := "server:\n  port: 70000\n" +
		"worker:\n  concurrency: 1\n" +
		"webhook:\n  secret: \"test-secret\"\n" +
		"notify:\n  default_channel: \"gitea\"\n  channels:\n    gitea:\n      enabled: true\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}

	mgr, err := config.NewManager(config.WithConfigFile(cfgPath), config.WithDefaults())
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load error: %v", err)
	}

	oldMgr := cfgManager
	oldServePort := servePort
	cfgManager = mgr
	servePort = 8080
	defer func() {
		cfgManager = oldMgr
		servePort = oldServePort
	}()

	err = runServe(serveCmd, nil)
	if err == nil {
		t.Fatalf("预期 runServe 因端口非法失败，但返回 nil")
	}
	if !strings.Contains(err.Error(), "--port") {
		t.Fatalf("error=%v, want contains %q", err, "--port")
	}
}

func TestBuildServiceDeps_UsesServeConfigResourceLimitsAndNetwork(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.GiteaURL = "https://gitea.example.com"
	cfg.GiteaToken = "test-token"
	cfg.CPULimit = "9.9"
	cfg.MemoryLimit = "99g"
	cfg.NetworkName = "net-from-config"

	// 最小通知配置（避免 buildNotifier 直接返回 nil）
	cfg.AppCfg = &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{"gitea": {Enabled: true}},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"gitea"}}},
		},
	}

	// 该测试只验证装配层是否正确把 serveConfig 的资源限制/网络名映射到 worker.PoolConfig。
	// 不依赖 Redis/Docker。
	poolCfg := buildWorkerPoolConfigFromServeConfig(cfg)
	if poolCfg.NetworkName != "net-from-config" {
		t.Fatalf("NetworkName=%q, want %q", poolCfg.NetworkName, "net-from-config")
	}
	if poolCfg.CPULimit != "9.9" {
		t.Fatalf("CPULimit=%q, want %q", poolCfg.CPULimit, "9.9")
	}
	if poolCfg.MemoryLimit != "99g" {
		t.Fatalf("MemoryLimit=%q, want %q", poolCfg.MemoryLimit, "99g")
	}
	if poolCfg.Image != cfg.WorkerImage {
		t.Fatalf("Image=%q, want %q", poolCfg.Image, cfg.WorkerImage)
	}
	if string(poolCfg.GiteaToken) != cfg.GiteaToken {
		t.Fatalf("GiteaToken should come from serveConfig")
	}
	if string(poolCfg.ClaudeAPIKey) != cfg.ClaudeAPIKey {
		t.Fatalf("ClaudeAPIKey should come from serveConfig")
	}
}

func TestComputeReadyStatus_DegradedWhenGiteaMissing(t *testing.T) {
	payload, httpStatus := computeReadyStatus(readinessSnapshot{
		RedisOK:            true,
		SQLiteOK:           true,
		GiteaConfigured:    false,
		NotifierEnabled:    false,
		WorkerImagePresent: true,
		ActiveWorkers:      0,
	})
	if httpStatus != http.StatusServiceUnavailable {
		t.Fatalf("http status = %d, want %d", httpStatus, http.StatusServiceUnavailable)
	}
	if payload["status"] != "degraded" {
		t.Fatalf("status = %v, want degraded", payload["status"])
	}
	if payload["gitea_configured"] != false {
		t.Fatalf("gitea_configured = %v, want false", payload["gitea_configured"])
	}
	if payload["notifier_enabled"] != false {
		t.Fatalf("notifier_enabled = %v, want false", payload["notifier_enabled"])
	}
}

func TestComputeReadyStatus_OkWhenAllCriticalDepsPresent(t *testing.T) {
	payload, httpStatus := computeReadyStatus(readinessSnapshot{
		RedisOK:            true,
		SQLiteOK:           true,
		GiteaConfigured:    true,
		NotifierEnabled:    true,
		WorkerImagePresent: true,
		ActiveWorkers:      1,
	})
	if httpStatus != http.StatusOK {
		t.Fatalf("http status = %d, want %d", httpStatus, http.StatusOK)
	}
	if payload["status"] != "ok" {
		t.Fatalf("status = %v, want ok", payload["status"])
	}
	if payload["worker_image_present"] != true {
		t.Fatalf("worker_image_present = %v, want true", payload["worker_image_present"])
	}
	if payload["active_workers"] != 1 {
		t.Fatalf("active_workers = %v, want 1", payload["active_workers"])
	}
}

func TestServe_Healthz(t *testing.T) {
	cfg := newTestConfig(t)
	skipIfNoRedis(t, cfg.RedisAddr)
	skipIfNoDocker(t)

	stopCh := make(chan struct{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- runServeWithConfig(cfg, stopCh)
	}()

	// 等待服务就绪
	addr := fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Port)
	var resp *http.Response
	var err error
	for i := 0; i < 20; i++ {
		resp, err = http.Get(addr) //nolint:gosec // 测试用固定地址
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("无法连接到 healthz 端点: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// /healthz 是 liveness 探针，始终返回 200
	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200, got %d", resp.StatusCode)
	}

	// 验证 JSON 响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("JSON 解析失败: %v, body: %s", err, body)
	}

	// liveness 探针返回 "alive" 状态
	if result["status"] != "alive" {
		t.Errorf("status 应为 alive, got %v", result["status"])
	}
	if _, exists := result["version"]; !exists {
		t.Error("响应应包含 version 字段")
	}

	// 通过关闭 stopCh 触发优雅关闭，无需 syscall.Kill
	close(stopCh)

	// 等待 serve 退出
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runServeWithConfig 应返回 nil, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServeWithConfig 未在 10 秒内退出")
	}
}

// TestServe_Readyz 测试 /readyz（readiness）端点返回子系统详细状态
func TestServe_Readyz(t *testing.T) {
	cfg := newTestConfig(t)
	skipIfNoRedis(t, cfg.RedisAddr)
	skipIfNoDocker(t)

	stopCh := make(chan struct{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- runServeWithConfig(cfg, stopCh)
	}()

	// 等待服务就绪
	addr := fmt.Sprintf("http://127.0.0.1:%d/readyz", cfg.Port)
	var resp *http.Response
	var err error
	for i := 0; i < 20; i++ {
		resp, err = http.Get(addr) //nolint:gosec // 测试用固定地址
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("无法连接到 readyz 端点: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 注：当前版本缺少 Gitea 配置会在启动阶段硬失败，因此本用例使用 newTestConfig 提供完整 Gitea 配置。
	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("JSON 解析失败: %v, body: %s", err, body)
	}

	if result["status"] != "ok" {
		t.Errorf("status 应为 ok, got %v", result["status"])
	}
	if _, exists := result["version"]; !exists {
		t.Error("响应应包含 version 字段")
	}
	if result["redis"] != true {
		t.Errorf("redis 应为 true, got %v", result["redis"])
	}
	if result["sqlite"] != true {
		t.Errorf("sqlite 应为 true, got %v", result["sqlite"])
	}
	if result["gitea_configured"] != true {
		t.Errorf("gitea_configured 应为 true, got %v", result["gitea_configured"])
	}

	if _, exists := result["worker_image_present"]; !exists {
		t.Error("响应应包含 worker_image_present 字段")
	}
	if _, exists := result["active_workers"]; !exists {
		t.Error("响应应包含 active_workers 字段")
	}

	close(stopCh)
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runServeWithConfig 应返回 nil, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServeWithConfig 未在 10 秒内退出")
	}
}

// TestServe_PortConflict 测试端口被占用时返回错误
func TestServe_PortConflict(t *testing.T) {
	cfg := newTestConfig(t)
	skipIfNoRedis(t, cfg.RedisAddr)
	skipIfNoDocker(t)

	// 先占用一个端口
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	defer func() { _ = l.Close() }()

	cfg.Port = port

	stopCh := make(chan struct{})
	defer close(stopCh)

	// runServeWithConfig 应立即返回端口冲突错误
	err = runServeWithConfig(cfg, stopCh)
	if err == nil {
		t.Error("端口被占用时应返回错误")
	}
}

// TestServe_RedisUnavailable 测试 Redis 不可用时 BuildServiceDeps 快速失败
func TestServe_RedisUnavailable(t *testing.T) {
	cfg := newTestConfig(t)
	// 使用一个不存在的 Redis 地址
	cfg.RedisAddr = "localhost:16379"

	stopCh := make(chan struct{})
	defer close(stopCh)

	err := runServeWithConfig(cfg, stopCh)
	if err == nil {
		t.Fatal("Redis 不可用时应返回错误")
	}
	if !strings.Contains(err.Error(), "Redis 连接失败") {
		t.Errorf("错误信息应包含 'Redis 连接失败', got: %v", err)
	}
}

func TestServe_RequiresWebhookSecret(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.WebhookSecret = ""

	stopCh := make(chan struct{})
	defer close(stopCh)

	err := runServeWithConfig(cfg, stopCh)
	if err == nil {
		t.Fatal("runServeWithConfig should fail when webhook secret is empty")
	}
	if !strings.Contains(err.Error(), "webhook-secret") {
		t.Fatalf("error = %v, want message containing webhook-secret", err)
	}
}

func TestServe_WebhookRouteReturnsUnauthorizedWithoutSignature(t *testing.T) {
	cfg := newTestConfig(t)
	skipIfNoRedis(t, cfg.RedisAddr)
	skipIfNoDocker(t)

	stopCh := make(chan struct{})

	errCh := make(chan error, 1)
	go func() { errCh <- runServeWithConfig(cfg, stopCh) }()

	addr := fmt.Sprintf("http://127.0.0.1:%d/webhooks/gitea", cfg.Port)
	var resp *http.Response
	var err error
	for i := 0; i < 20; i++ {
		resp, err = http.Post(addr, "application/json", strings.NewReader(`{}`)) //nolint:gosec // 测试用固定地址
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("POST webhook failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	// 通过关闭 stopCh 触发优雅关闭，无需 syscall.Kill
	close(stopCh)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runServeWithConfig 应返回 nil, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServeWithConfig 未在 10 秒内退出")
	}
}
