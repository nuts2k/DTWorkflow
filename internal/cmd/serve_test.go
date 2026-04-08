package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
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
		AppCfg: &config.Config{
			Gitea: config.GiteaConfig{
				URL:   "https://gitea.example.com",
				Token: "test-token",
			},
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
		},
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

// TestGetEnvDefault_EnvVarSet 覆盖环境变量已设置时返回 env 值的分支
func TestGetEnvDefault_EnvVarSet(t *testing.T) {
	t.Setenv("DTWORKFLOW_TEST_KEY_XYZ", "from-env")
	got := getEnvDefault("DTWORKFLOW_TEST_KEY_XYZ", "default-value")
	if got != "from-env" {
		t.Fatalf("getEnvDefault = %q, want %q", got, "from-env")
	}
}

// TestGetEnvDefault_EnvVarNotSet 覆盖环境变量未设置时返回默认值的分支
func TestGetEnvDefault_EnvVarNotSet(t *testing.T) {
	// 确保该变量在环境中不存在
	t.Setenv("DTWORKFLOW_TEST_KEY_ABSENT_XYZ", "")
	got := getEnvDefault("DTWORKFLOW_TEST_KEY_ABSENT_XYZ", "my-default")
	if got != "my-default" {
		t.Fatalf("getEnvDefault = %q, want %q", got, "my-default")
	}
}

// buildTestConfigManager 构造一个最小可通过校验并已 Load 的 config.Manager
func buildTestConfigManager(t *testing.T) *config.Manager {
	t.Helper()
	cfgPath := writeTestConfigFile(t, "") // 使用 writeTestConfigFile 的内置最小合法内容
	mgr, err := config.NewManager(config.WithDefaults(), config.WithConfigFile(cfgPath))
	if err != nil {
		t.Fatalf("config.NewManager 失败: %v", err)
	}
	if err := mgr.Load(); err != nil {
		t.Fatalf("mgr.Load 失败: %v", err)
	}
	return mgr
}

// TestConfigAdapter_ResolveReviewConfig_ReturnsOverride 覆盖 cfg 非 nil 时委托给 cfg.ResolveReviewConfig
func TestConfigAdapter_ResolveReviewConfig_ReturnsOverride(t *testing.T) {
	mgr := buildTestConfigManager(t)
	a := &configAdapter{mgr: mgr}

	// 不存在的 repo 应返回全局默认 ReviewOverride（零值）
	got := a.ResolveReviewConfig("unknown/repo")
	// 只验证调用不 panic 并返回有效结构；Enabled 默认为 nil
	if got.Enabled != nil {
		t.Fatalf("Enabled = %v, want nil（默认全局无 review 配置）", got.Enabled)
	}
}

// TestConfigAdapter_IsReviewEnabled_DefaultsToTrue 覆盖 Enabled == nil 时返回 true
func TestConfigAdapter_IsReviewEnabled_DefaultsToTrue(t *testing.T) {
	mgr := buildTestConfigManager(t)
	a := &configAdapter{mgr: mgr}

	// 未配置任何 review 覆盖，Enabled 为 nil，应默认启用
	got := a.IsReviewEnabled("unknown/repo")
	if !got {
		t.Fatal("IsReviewEnabled = false, want true（Enabled == nil 应默认启用）")
	}
}

// TestConfigAdapter_IsReviewEnabled_FalseWhenDisabled 覆盖 Enabled 明确为 false 时返回 false
func TestConfigAdapter_IsReviewEnabled_FalseWhenDisabled(t *testing.T) {
	// 构造一个带 repo review.enabled=false 的配置文件
	disabled := false
	cfgPath := writeTestConfigFile(t,
		"gitea:\n  url: \"http://gitea:3000\"\n  token: \"test-token\"\n"+
			"claude:\n  api_key: \"test-api-key\"\n"+
			"webhook:\n  secret: \"test-secret\"\n"+
			"notify:\n  default_channel: \"gitea\"\n  channels:\n    gitea:\n      enabled: true\n"+
			"repos:\n  - name: \"acme/repo\"\n    review:\n      enabled: false\n")
	_ = disabled // 通过 YAML 设置，disabled 变量仅用于文档说明

	mgr, err := config.NewManager(config.WithDefaults(), config.WithConfigFile(cfgPath))
	if err != nil {
		t.Fatalf("config.NewManager 失败: %v", err)
	}
	if err := mgr.Load(); err != nil {
		t.Fatalf("mgr.Load 失败: %v", err)
	}

	a := &configAdapter{mgr: mgr}

	got := a.IsReviewEnabled("acme/repo")
	if got {
		t.Fatal("IsReviewEnabled = true, want false（repo 的 review.enabled 明确设为 false）")
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
