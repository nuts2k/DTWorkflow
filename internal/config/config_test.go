package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManager_Load_ConfigFileAndDefaults(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "dtworkflow.yaml")

	// 仅提供部分配置，其他字段由默认值补齐。
	// 注意：由于 Manager.Load 会执行 Validate，本测试需提供校验要求的最小字段。
	content := []byte("" +
		"server:\n" +
		"  port: 9090\n" +
		"gitea:\n" +
		"  url: \"http://gitea:3000\"\n" +
		"  token: \"test-token\"\n" +
		"claude:\n" +
		"  api_key: \"test-api-key\"\n" +
		"redis:\n" +
		"  addr: \"redis:6379\"\n" +
		"worker:\n" +
		"  timeout: \"45m\"\n" +
		"webhook:\n" +
		"  secret: \"test-secret\"\n" +
		"notify:\n" +
		"  default_channel: \"gitea\"\n" +
		"  channels:\n" +
		"    gitea:\n" +
		"      enabled: true\n")
	if err := os.WriteFile(cfgPath, content, 0o600); err != nil {
		t.Fatalf("write temp config file: %v", err)
	}

	m, err := NewManager(
		WithConfigFile(cfgPath),
		WithDefaults(),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := m.Get()
	if cfg == nil {
		t.Fatalf("Get returned nil config")
	}

	// 配置文件值
	if cfg.Server.Port != 9090 {
		t.Fatalf("server.port got %d, want %d", cfg.Server.Port, 9090)
	}
	if cfg.Redis.Addr != "redis:6379" {
		t.Fatalf("redis.addr got %q, want %q", cfg.Redis.Addr, "redis:6379")
	}
	if cfg.Worker.Timeout != 45*time.Minute {
		t.Fatalf("worker.timeout got %s, want %s", cfg.Worker.Timeout, 45*time.Minute)
	}

	// 默认值补齐
	if cfg.Server.Host != "0.0.0.0" {
		t.Fatalf("server.host got %q, want %q", cfg.Server.Host, "0.0.0.0")
	}
	if cfg.Log.Level != "info" {
		t.Fatalf("log.level got %q, want %q", cfg.Log.Level, "info")
	}
	if cfg.Log.Format != "text" {
		t.Fatalf("log.format got %q, want %q", cfg.Log.Format, "text")
	}
	if cfg.Worker.Concurrency != 3 {
		t.Fatalf("worker.concurrency got %d, want %d", cfg.Worker.Concurrency, 3)
	}
	if cfg.Database.Path != "data/dtworkflow.db" {
		t.Fatalf("database.path got %q, want %q", cfg.Database.Path, "data/dtworkflow.db")
	}
	if cfg.Redis.DB != 0 {
		t.Fatalf("redis.db got %d, want %d", cfg.Redis.DB, 0)
	}
}

func TestManager_Load_EnvOverridesConfig(t *testing.T) {

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "dtworkflow.yaml")

	content := []byte("" +
		"gitea:\n" +
		"  url: \"http://gitea:3000\"\n" +
		"  token: \"test-token\"\n" +
		"claude:\n" +
		"  api_key: \"test-api-key\"\n" +
		"redis:\n" +
		"  addr: \"file:6379\"\n" +
		"webhook:\n" +
		"  secret: \"test-secret\"\n" +
		"notify:\n" +
		"  default_channel: \"gitea\"\n" +
		"  channels:\n" +
		"    gitea:\n" +
		"      enabled: true\n")
	if err := os.WriteFile(cfgPath, content, 0o600); err != nil {
		t.Fatalf("write temp config file: %v", err)
	}

	t.Setenv("DTWORKFLOW_REDIS_ADDR", "env:6379")

	m, err := NewManager(
		WithConfigFile(cfgPath),
		WithDefaults(),
		WithEnvPrefix("DTWORKFLOW"),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := m.Get()
	if cfg == nil {
		t.Fatalf("Get returned nil config")
	}

	if cfg.Redis.Addr != "env:6379" {
		t.Fatalf("redis.addr got %q, want %q", cfg.Redis.Addr, "env:6379")
	}
}

func TestManager_Load_DatabasePathSupportsLegacyEnvName(t *testing.T) {
	t.Setenv("DTWORKFLOW_WEBHOOK_SECRET", "test-secret")
	t.Setenv("DTWORKFLOW_NOTIFY_CHANNELS_GITEA_ENABLED", "true")
	t.Setenv("DTWORKFLOW_GITEA_URL", "http://gitea:3000")
	t.Setenv("DTWORKFLOW_GITEA_TOKEN", "test-token")
	t.Setenv("DTWORKFLOW_CLAUDE_API_KEY", "test-api-key")
	t.Setenv("DTWORKFLOW_DB_PATH", "/tmp/legacy.db")

	m, err := NewManager(
		WithDefaults(),
		WithEnvPrefix("DTWORKFLOW"),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := m.Get()
	if cfg == nil {
		t.Fatalf("Get returned nil config")
	}
	if cfg.Database.Path != "/tmp/legacy.db" {
		t.Fatalf("database.path got %q, want %q", cfg.Database.Path, "/tmp/legacy.db")
	}

	t.Setenv("DTWORKFLOW_DATABASE_PATH", "/tmp/new.db")
	m2, err := NewManager(
		WithDefaults(),
		WithEnvPrefix("DTWORKFLOW"),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg2 := m2.Get()
	if cfg2 == nil {
		t.Fatalf("Get returned nil config")
	}
	if cfg2.Database.Path != "/tmp/new.db" {
		t.Fatalf("database.path got %q, want %q", cfg2.Database.Path, "/tmp/new.db")
	}
}

func TestManager_Load_DefaultsAndEnvWithoutConfigFile(t *testing.T) {
	// 不指定配置文件时，Load() 也应能成功：默认值 + 环境变量覆盖。
	// 注意：由于 Manager.Load 会执行 Validate，本测试需通过环境变量补齐必填项。
	t.Setenv("DTWORKFLOW_REDIS_ADDR", "envonly:6379")
	t.Setenv("DTWORKFLOW_WEBHOOK_SECRET", "test-secret")
	t.Setenv("DTWORKFLOW_NOTIFY_CHANNELS_GITEA_ENABLED", "true")
	t.Setenv("DTWORKFLOW_GITEA_URL", "http://gitea:3000")
	t.Setenv("DTWORKFLOW_GITEA_TOKEN", "test-token")
	t.Setenv("DTWORKFLOW_CLAUDE_API_KEY", "test-api-key")

	m, err := NewManager(
		WithDefaults(),
		WithEnvPrefix("DTWORKFLOW"),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := m.Get()
	if cfg == nil {
		t.Fatalf("Get returned nil config")
	}

	// 默认值
	if cfg.Server.Host != "0.0.0.0" {
		t.Fatalf("server.host got %q, want %q", cfg.Server.Host, "0.0.0.0")
	}
	if cfg.Server.Port != 8080 {
		t.Fatalf("server.port got %d, want %d", cfg.Server.Port, 8080)
	}

	// env 覆盖
	if cfg.Redis.Addr != "envonly:6379" {
		t.Fatalf("redis.addr got %q, want %q", cfg.Redis.Addr, "envonly:6379")
	}
}

func TestManager_Load_DailyReportEnvOnly(t *testing.T) {
	t.Setenv("DTWORKFLOW_REDIS_ADDR", "envonly:6379")
	t.Setenv("DTWORKFLOW_WEBHOOK_SECRET", "test-secret")
	t.Setenv("DTWORKFLOW_NOTIFY_CHANNELS_GITEA_ENABLED", "true")
	t.Setenv("DTWORKFLOW_GITEA_URL", "http://gitea:3000")
	t.Setenv("DTWORKFLOW_GITEA_TOKEN", "test-token")
	t.Setenv("DTWORKFLOW_CLAUDE_API_KEY", "test-api-key")
	t.Setenv("DTWORKFLOW_DAILY_REPORT_ENABLED", "true")
	t.Setenv("DTWORKFLOW_DAILY_REPORT_CRON", "0 9 * * *")
	t.Setenv("DTWORKFLOW_DAILY_REPORT_TIMEZONE", "Asia/Shanghai")
	t.Setenv("DTWORKFLOW_DAILY_REPORT_FEISHU_WEBHOOK", "https://open.feishu.cn/open-apis/bot/v2/hook/xxx")
	t.Setenv("DTWORKFLOW_DAILY_REPORT_FEISHU_SECRET", "sec-env")

	m, err := NewManager(
		WithDefaults(),
		WithEnvPrefix("DTWORKFLOW"),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := m.Get()
	if cfg == nil {
		t.Fatalf("Get returned nil config")
	}
	if !cfg.DailyReport.Enabled {
		t.Fatal("daily_report.enabled should be true")
	}
	if cfg.DailyReport.FeishuWebhook != "https://open.feishu.cn/open-apis/bot/v2/hook/xxx" {
		t.Fatalf("daily_report.feishu_webhook got %q", cfg.DailyReport.FeishuWebhook)
	}
	if cfg.DailyReport.FeishuSecret != "sec-env" {
		t.Fatalf("daily_report.feishu_secret got %q", cfg.DailyReport.FeishuSecret)
	}
}

func TestWorkerTimeoutsAndStreamMonitor_Parse(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "dtworkflow.yaml")

	// 构造包含新字段的 YAML，同时提供校验所需的最小必填项
	content := []byte("" +
		"gitea:\n" +
		"  url: \"http://gitea:3000\"\n" +
		"  token: \"test-token\"\n" +
		"claude:\n" +
		"  api_key: \"test-api-key\"\n" +
		"redis:\n" +
		"  addr: \"redis:6379\"\n" +
		"webhook:\n" +
		"  secret: \"test-secret\"\n" +
		"notify:\n" +
		"  default_channel: \"gitea\"\n" +
		"  channels:\n" +
		"    gitea:\n" +
		"      enabled: true\n" +
		"worker:\n" +
		"  timeouts:\n" +
		"    review_pr: \"15m\"\n" +
		"    fix_issue: \"45m\"\n" +
		"    gen_tests: \"30m\"\n" +
		"  stream_monitor:\n" +
		"    enabled: true\n" +
		"    activity_timeout: \"2m\"\n")
	if err := os.WriteFile(cfgPath, content, 0o600); err != nil {
		t.Fatalf("write temp config file: %v", err)
	}

	m, err := NewManager(
		WithConfigFile(cfgPath),
		WithDefaults(),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := m.Get()
	if cfg == nil {
		t.Fatalf("Get returned nil config")
	}

	// 验证 Timeouts
	if cfg.Worker.Timeouts.ReviewPR != 15*time.Minute {
		t.Fatalf("worker.timeouts.review_pr got %s, want %s", cfg.Worker.Timeouts.ReviewPR, 15*time.Minute)
	}
	if cfg.Worker.Timeouts.FixIssue != 45*time.Minute {
		t.Fatalf("worker.timeouts.fix_issue got %s, want %s", cfg.Worker.Timeouts.FixIssue, 45*time.Minute)
	}
	if cfg.Worker.Timeouts.GenTests != 30*time.Minute {
		t.Fatalf("worker.timeouts.gen_tests got %s, want %s", cfg.Worker.Timeouts.GenTests, 30*time.Minute)
	}

	// 验证 StreamMonitor
	if !cfg.Worker.StreamMonitor.Enabled {
		t.Fatalf("worker.stream_monitor.enabled got false, want true")
	}
	if cfg.Worker.StreamMonitor.ActivityTimeout != 2*time.Minute {
		t.Fatalf("worker.stream_monitor.activity_timeout got %s, want %s", cfg.Worker.StreamMonitor.ActivityTimeout, 2*time.Minute)
	}
}

func TestExampleConfig_Loadable(t *testing.T) {
	t.Parallel()

	examplePath := filepath.Join("..", "..", "configs", "dtworkflow.example.yaml")
	if _, err := os.Stat(examplePath); err != nil {
		t.Fatalf("stat example config file %q: %v", examplePath, err)
	}

	m, err := NewManager(
		WithConfigFile(examplePath),
		WithDefaults(),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load example config: %v", err)
	}

	cfg := m.Get()
	if cfg == nil {
		t.Fatalf("Get returned nil config")
	}

	// 关键字段断言：确保示例配置包含 serve 运行所需的关键非空占位值。
	if cfg.Gitea.URL == "" {
		t.Fatalf("gitea.url should not be empty")
	}
	if cfg.Gitea.Token == "" {
		t.Fatalf("gitea.token should not be empty")
	}
	if cfg.Webhook.Secret == "" {
		t.Fatalf("webhook.secret should not be empty")
	}
	if cfg.Claude.APIKey == "" {
		t.Fatalf("claude.api_key should not be empty")
	}
	if cfg.Notify.DefaultChannel == "" {
		t.Fatalf("notify.default_channel should not be empty")
	}
}

func TestClone_FeishuOverrideDeepCopy(t *testing.T) {
	t.Parallel()

	original := &Config{
		Server:  ServerConfig{Port: 8080},
		Gitea:   GiteaConfig{URL: "http://gitea:3000", Token: "test-token"},
		Claude:  ClaudeConfig{APIKey: "test-api-key"},
		Redis:   RedisConfig{Addr: "localhost:6379"},
		Webhook: WebhookConfig{Secret: "test-secret"},
		Notify: NotifyConfig{
			DefaultChannel: "gitea",
			Channels:       map[string]ChannelConfig{"gitea": {Enabled: true}},
		},
		Repos: []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{
				Feishu: &FeishuOverride{
					WebhookURL: "https://original.com/hook",
					Secret:     "original-secret",
				},
			},
		}},
	}

	cloned := original.Clone()

	// 修改 clone 不应影响原始对象
	cloned.Repos[0].Notify.Feishu.WebhookURL = "https://modified.com/hook"
	cloned.Repos[0].Notify.Feishu.Secret = "modified-secret"

	if original.Repos[0].Notify.Feishu.WebhookURL != "https://original.com/hook" {
		t.Errorf("修改 clone 后原始 WebhookURL 被改变: %q", original.Repos[0].Notify.Feishu.WebhookURL)
	}
	if original.Repos[0].Notify.Feishu.Secret != "original-secret" {
		t.Errorf("修改 clone 后原始 Secret 被改变: %q", original.Repos[0].Notify.Feishu.Secret)
	}
}

func TestClone_FeishuOverrideNil(t *testing.T) {
	t.Parallel()

	original := &Config{
		Server:  ServerConfig{Port: 8080},
		Gitea:   GiteaConfig{URL: "http://gitea:3000", Token: "test-token"},
		Claude:  ClaudeConfig{APIKey: "test-api-key"},
		Redis:   RedisConfig{Addr: "localhost:6379"},
		Webhook: WebhookConfig{Secret: "test-secret"},
		Notify: NotifyConfig{
			DefaultChannel: "gitea",
			Channels:       map[string]ChannelConfig{"gitea": {Enabled: true}},
		},
		Repos: []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Routes: []RouteConfig{{Repo: "*"}}},
		}},
	}

	cloned := original.Clone()
	if cloned.Repos[0].Notify.Feishu != nil {
		t.Error("Feishu 为 nil 时 clone 也应为 nil")
	}
}

func TestClone_TestGenGlobalEnabledDeepCopy(t *testing.T) {
	t.Parallel()

	enabled := true
	original := &Config{
		TestGen: TestGenOverride{
			Enabled:        &enabled,
			ModuleScope:    "backend",
			MaxRetryRounds: 5,
			TestFramework:  "junit5",
		},
	}

	cloned := original.Clone()

	// 修改 clone 的 Enabled 指针指向的值不应影响原始值
	if cloned.TestGen.Enabled == original.TestGen.Enabled {
		t.Error("Enabled 指针应为不同的实例（深拷贝）")
	}
	*cloned.TestGen.Enabled = false
	if original.TestGen.Enabled == nil || !*original.TestGen.Enabled {
		t.Errorf("修改 clone 后原始 Enabled 被改变: %v", original.TestGen.Enabled)
	}
}

func TestClone_TestGenRepoDeepCopy(t *testing.T) {
	t.Parallel()

	repoEnabled := true
	original := &Config{
		Repos: []RepoConfig{{
			Name: "acme/repo",
			TestGen: &TestGenOverride{
				Enabled:        &repoEnabled,
				ModuleScope:    "services/api",
				MaxRetryRounds: 7,
				TestFramework:  "vitest",
			},
		}},
	}

	cloned := original.Clone()

	// 修改 clone 不应影响原始对象
	if cloned.Repos[0].TestGen == original.Repos[0].TestGen {
		t.Error("repo.TestGen 指针应为不同实例（深拷贝）")
	}
	if cloned.Repos[0].TestGen.Enabled == original.Repos[0].TestGen.Enabled {
		t.Error("repo.TestGen.Enabled 指针应为不同实例（深拷贝）")
	}
	cloned.Repos[0].TestGen.ModuleScope = "modified"
	*cloned.Repos[0].TestGen.Enabled = false

	if original.Repos[0].TestGen.ModuleScope != "services/api" {
		t.Errorf("修改 clone 后原始 ModuleScope 被改变: %q", original.Repos[0].TestGen.ModuleScope)
	}
	if original.Repos[0].TestGen.Enabled == nil || !*original.Repos[0].TestGen.Enabled {
		t.Errorf("修改 clone 后原始 Enabled 被改变: %v", original.Repos[0].TestGen.Enabled)
	}
}

func TestClone_TestGenNilPreserved(t *testing.T) {
	t.Parallel()

	// Repos[0].TestGen = nil 应保持 nil；全局 TestGen.Enabled = nil 应保持 nil
	original := &Config{
		TestGen: TestGenOverride{
			Enabled:        nil,
			ModuleScope:    "backend",
			MaxRetryRounds: 3,
		},
		Repos: []RepoConfig{{Name: "acme/repo", TestGen: nil}},
	}

	cloned := original.Clone()
	if cloned.TestGen.Enabled != nil {
		t.Error("全局 TestGen.Enabled 为 nil 时 clone 也应为 nil")
	}
	if cloned.Repos[0].TestGen != nil {
		t.Error("repo.TestGen 为 nil 时 clone 也应为 nil")
	}
	if cloned.TestGen.ModuleScope != "backend" {
		t.Errorf("TestGen.ModuleScope = %q, want backend", cloned.TestGen.ModuleScope)
	}
}
