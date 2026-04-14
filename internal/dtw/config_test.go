package dtw

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadHostsConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yml")

	cfg := &HostsConfig{
		Active: "prod",
		Servers: map[string]ServerConfig{
			"prod": {URL: "https://prod.example.com", Token: "token-prod"},
			"dev":  {URL: "https://dev.example.com", Token: "token-dev"},
		},
	}

	if err := SaveHostsConfig(path, cfg); err != nil {
		t.Fatalf("SaveHostsConfig 失败: %v", err)
	}

	// 验证文件权限为 0600
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("文件权限 = %04o，期望 0600", perm)
	}

	loaded, err := LoadHostsConfig(path)
	if err != nil {
		t.Fatalf("LoadHostsConfig 失败: %v", err)
	}

	if loaded.Active != "prod" {
		t.Errorf("Active = %q，期望 %q", loaded.Active, "prod")
	}
	if len(loaded.Servers) != 2 {
		t.Errorf("Servers 数量 = %d，期望 2", len(loaded.Servers))
	}
	if loaded.Servers["prod"].URL != "https://prod.example.com" {
		t.Errorf("prod URL = %q，期望 %q", loaded.Servers["prod"].URL, "https://prod.example.com")
	}
}

func TestLoadHostsConfig_NotExist(t *testing.T) {
	_, err := LoadHostsConfig("/nonexistent/path/hosts.yml")
	if err == nil {
		t.Fatal("期望返回错误，但得到 nil")
	}
}

func TestSaveHostsConfig_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "hosts.yml")

	cfg := &HostsConfig{Active: "test", Servers: map[string]ServerConfig{
		"test": {URL: "http://localhost:8080", Token: "t"},
	}}

	if err := SaveHostsConfig(path, cfg); err != nil {
		t.Fatalf("SaveHostsConfig 失败: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("文件不存在: %v", err)
	}
}

func TestResolveServer_FlagOverride(t *testing.T) {
	cfg := &HostsConfig{
		Active: "prod",
		Servers: map[string]ServerConfig{
			"prod": {URL: "https://prod.example.com", Token: "tp"},
			"dev":  {URL: "https://dev.example.com", Token: "td"},
		},
	}

	srv, err := cfg.ResolveServer("dev")
	if err != nil {
		t.Fatalf("ResolveServer 失败: %v", err)
	}
	if srv.URL != "https://dev.example.com" {
		t.Errorf("URL = %q，期望 dev URL", srv.URL)
	}
}

func TestResolveServer_ActiveFallback(t *testing.T) {
	cfg := &HostsConfig{
		Active: "prod",
		Servers: map[string]ServerConfig{
			"prod": {URL: "https://prod.example.com", Token: "tp"},
		},
	}

	srv, err := cfg.ResolveServer("")
	if err != nil {
		t.Fatalf("ResolveServer 失败: %v", err)
	}
	if srv.URL != "https://prod.example.com" {
		t.Errorf("URL = %q，期望 prod URL", srv.URL)
	}
}

func TestResolveServer_EnvOverride(t *testing.T) {
	t.Setenv("DTW_SERVER_URL", "https://env.example.com")
	t.Setenv("DTW_TOKEN", "env-token")

	cfg := &HostsConfig{
		Active: "prod",
		Servers: map[string]ServerConfig{
			"prod": {URL: "https://prod.example.com", Token: "tp"},
		},
	}

	srv, err := cfg.ResolveServer("prod")
	if err != nil {
		t.Fatalf("ResolveServer 失败: %v", err)
	}
	if srv.URL != "https://env.example.com" {
		t.Errorf("URL = %q，期望 env URL", srv.URL)
	}
	if srv.Token != "env-token" {
		t.Errorf("Token = %q，期望 env-token", srv.Token)
	}
}

func TestResolveServer_NoServer(t *testing.T) {
	cfg := &HostsConfig{Servers: map[string]ServerConfig{}}

	_, err := cfg.ResolveServer("")
	if err == nil {
		t.Fatal("期望返回错误，但得到 nil")
	}
}

func TestResolveServer_ServerNotFound(t *testing.T) {
	cfg := &HostsConfig{
		Active: "nonexistent",
		Servers: map[string]ServerConfig{
			"prod": {URL: "https://prod.example.com", Token: "tp"},
		},
	}

	_, err := cfg.ResolveServer("")
	if err == nil {
		t.Fatal("期望返回错误，但得到 nil")
	}
}
