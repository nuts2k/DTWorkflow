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
