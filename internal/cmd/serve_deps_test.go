package cmd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
)

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

// TestBuildWorkerPoolConfig_SplitTokens 验证 serveConfig 中拆分的 review/fix token
// 被正确映射到 PoolConfig 的 GiteaToken / GiteaTokenFix 字段。
func TestBuildWorkerPoolConfig_SplitTokens(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.GiteaURL = "https://gitea.example.com"
	cfg.GiteaToken = "tok-fallback"
	cfg.GiteaTokenReview = "tok-review"
	cfg.GiteaTokenFix = "tok-fix"

	poolCfg := buildWorkerPoolConfigFromServeConfig(cfg)
	if string(poolCfg.GiteaToken) != "tok-review" {
		t.Fatalf("PoolConfig.GiteaToken = %q, want tok-review（review 账号）", poolCfg.GiteaToken)
	}
	if string(poolCfg.GiteaTokenFix) != "tok-fix" {
		t.Fatalf("PoolConfig.GiteaTokenFix = %q, want tok-fix（fix 账号）", poolCfg.GiteaTokenFix)
	}
}

// TestBuildWorkerPoolConfig_TokenFallback 验证只填兜底 gitea.token 时，
// 两个 PoolConfig token 字段都回退到它（保持向后兼容）。
func TestBuildWorkerPoolConfig_TokenFallback(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.GiteaURL = "https://gitea.example.com"
	cfg.GiteaToken = "tok-fallback"
	cfg.GiteaTokenReview = ""
	cfg.GiteaTokenFix = ""

	poolCfg := buildWorkerPoolConfigFromServeConfig(cfg)
	if string(poolCfg.GiteaToken) != "tok-fallback" {
		t.Fatalf("PoolConfig.GiteaToken = %q, want tok-fallback", poolCfg.GiteaToken)
	}
	if string(poolCfg.GiteaTokenFix) != "tok-fallback" {
		t.Fatalf("PoolConfig.GiteaTokenFix 未回退到兜底 token, 得到 %q", poolCfg.GiteaTokenFix)
	}
}
