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
	// 证明点：buildServeConfigFromManager 应从 cfgManager.Get() 读取 server.port，
	// 而不是依赖包级变量 servePort。
	// 方法：cfgManager 中使用端口 9999，全局 servePort 使用 8080；
	// 若 buildServeConfigFromManager 正确使用 cfgManager，输出端口应为 9999。

	cfgPath := filepath.Join(t.TempDir(), "dtworkflow.yaml")
	content := "server:\n  port: 9999\n" +
		"gitea:\n  url: \"http://gitea:3000\"\n  token: \"test-token\"\n" +
		"claude:\n  api_key: \"test-api-key\"\n" +
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

	oldServePort := servePort
	servePort = 8080 // 与 cfgManager 中的 9999 不同
	defer func() { servePort = oldServePort }()

	sc, err := buildServeConfigFromManager(mgr)
	if err != nil {
		t.Fatalf("buildServeConfigFromManager error: %v", err)
	}
	if sc.Port != 9999 {
		t.Fatalf("Port=%d, want 9999（应使用 cfgManager 的值，而非全局 servePort=%d）", sc.Port, servePort)
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
		Worker: config.WorkerConfig{
			Timeouts: config.TaskTimeouts{
				ReviewPR: 11 * time.Minute,
				FixIssue: 22 * time.Minute,
				GenTests: 33 * time.Minute,
			},
			StreamMonitor: config.StreamMonitorConf{
				Enabled:         true,
				ActivityTimeout: 90 * time.Second,
			},
		},
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels:       map[string]config.ChannelConfig{"gitea": {Enabled: true}},
			Routes:         []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"gitea"}}},
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
	if poolCfg.Timeouts.ReviewPR != 11*time.Minute {
		t.Fatalf("ReviewPR timeout = %s, want %s", poolCfg.Timeouts.ReviewPR, 11*time.Minute)
	}
	if poolCfg.Timeouts.FixIssue != 22*time.Minute {
		t.Fatalf("FixIssue timeout = %s, want %s", poolCfg.Timeouts.FixIssue, 22*time.Minute)
	}
	if poolCfg.Timeouts.GenTests != 33*time.Minute {
		t.Fatalf("GenTests timeout = %s, want %s", poolCfg.Timeouts.GenTests, 33*time.Minute)
	}
	if !poolCfg.StreamMonitor.Enabled {
		t.Fatal("StreamMonitor.Enabled = false, want true")
	}
	if poolCfg.StreamMonitor.ActivityTimeout != 90*time.Second {
		t.Fatalf("StreamMonitor.ActivityTimeout = %s, want %s", poolCfg.StreamMonitor.ActivityTimeout, 90*time.Second)
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
