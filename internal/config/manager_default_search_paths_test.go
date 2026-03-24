package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewManager_DefaultSearchPaths(t *testing.T) {
	// 该测试必须是稳定、可重复的：不依赖开发机真实 HOME、/etc 或仓库根目录。
	// 因此使用隔离的临时目录作为工作目录，并在其中写入 dtworkflow.yaml。

	workDir := t.TempDir()
	cfgPath := filepath.Join(workDir, "dtworkflow.yaml")
	if err := os.WriteFile(cfgPath, []byte(""+
		"server:\n"+
		"  port: 18080\n"+
		"webhook:\n"+
		"  secret: \"test-secret\"\n"+
		"notify:\n"+
		"  default_channel: \"gitea\"\n"+
		"  channels:\n"+
		"    gitea:\n"+
		"      enabled: true\n"), 0o600); err != nil {
		t.Fatalf("write temp config file: %v", err)
	}

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Chdir(workDir)

	m, err := NewManager(
		WithDefaults(),
		WithDefaultSearchPaths(),
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
	if cfg.Server.Port != 18080 {
		t.Fatalf("server.port got %d, want %d", cfg.Server.Port, 18080)
	}
}

func TestManager_Load_DefaultSearchPaths_ConfigMissing_IsNotError(t *testing.T) {
	// 证明点：启用默认搜索路径后，如果未找到配置文件，不应报错，仍应使用 defaults/env。
	workDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Chdir(workDir)

	// env-only 补齐 Validate 必需字段
	t.Setenv("DTWORKFLOW_WEBHOOK_SECRET", "test-secret")
	t.Setenv("DTWORKFLOW_NOTIFY_CHANNELS_GITEA_ENABLED", "true")

	m, err := NewManager(
		WithDefaults(),
		WithDefaultSearchPaths(),
		WithEnvPrefix("DTWORKFLOW"),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load should not fail when config file is missing in default search paths: %v", err)
	}
	cfg := m.Get()
	if cfg == nil {
		t.Fatalf("Get returned nil config")
	}
	if cfg.Server.Port != 8080 {
		t.Fatalf("server.port got %d, want %d", cfg.Server.Port, 8080)
	}
	if cfg.Webhook.Secret != "test-secret" {
		t.Fatalf("webhook.secret got %q, want %q", cfg.Webhook.Secret, "test-secret")
	}
}

func TestManager_Load_ExplicitConfigFileMissing_ReturnsError(t *testing.T) {
	// 证明点：显式指定配置文件时（WithConfigFile/SetConfigFile），文件不存在应报错。
	missingPath := filepath.Join(t.TempDir(), "missing.yaml")

	// env-only 补齐 Validate 必需字段（即便补齐，也应因配置文件缺失而失败）
	t.Setenv("DTWORKFLOW_WEBHOOK_SECRET", "test-secret")
	t.Setenv("DTWORKFLOW_NOTIFY_CHANNELS_GITEA_ENABLED", "true")

	m, err := NewManager(
		WithDefaults(),
		WithEnvPrefix("DTWORKFLOW"),
		WithConfigFile(missingPath),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.Load(); err == nil {
		t.Fatalf("Load should fail when explicit config file is missing")
	}
}
