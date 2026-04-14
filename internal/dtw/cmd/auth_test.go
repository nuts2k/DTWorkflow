package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrInitHostsConfig_NotExistCreatesEmptyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yml")

	cfg, err := loadOrInitHostsConfig(path)
	if err != nil {
		t.Fatalf("loadOrInitHostsConfig 失败: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg 不应为 nil")
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("期望空 servers，实际 %d", len(cfg.Servers))
	}
}

func TestLoadOrInitHostsConfig_InvalidYAMLReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yml")
	if err := os.WriteFile(path, []byte("servers: [invalid"), 0o600); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}

	_, err := loadOrInitHostsConfig(path)
	if err == nil {
		t.Fatal("期望返回错误，但得到 nil")
	}
}
