package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempConfigFile(t *testing.T, dir string, content string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "dtworkflow.yaml")
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config file: %v", err)
	}
	return cfgPath
}

func minimalValidConfigYAML(port int) string {
	return fmt.Sprintf(""+
		"server:\n"+
		"  port: %d\n"+
		"webhook:\n"+
		"  secret: \"test-secret\"\n"+
		"notify:\n"+
		"  default_channel: \"gitea\"\n"+
		"  channels:\n"+
		"    gitea:\n"+
		"      enabled: true\n", port)
}

func minimalInvalidConfigYAML(port int) string {
	// webhook.secret 为空，必然触发 Validate 失败。
	return fmt.Sprintf(""+
		"server:\n"+
		"  port: %d\n"+
		"webhook:\n"+
		"  secret: \"\"\n"+
		"notify:\n"+
		"  default_channel: \"gitea\"\n"+
		"  channels:\n"+
		"    gitea:\n"+
		"      enabled: true\n", port)
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestManager_WatchConfig_UpdateCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := writeTempConfigFile(t, tmpDir, minimalValidConfigYAML(9000))

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

	if got := m.Get().Server.Port; got != 9000 {
		t.Fatalf("server.port got %d, want %d", got, 9000)
	}

	if err := m.WatchConfig(); err != nil {
		t.Fatalf("WatchConfig: %v", err)
	}

	// 写入新配置，等待 watcher 生效。
	if err := os.WriteFile(cfgPath, []byte(minimalValidConfigYAML(9001)), 0o600); err != nil {
		t.Fatalf("rewrite config file: %v", err)
	}

	waitUntil(t, 2*time.Second, func() bool {
		cfg := m.Get()
		return cfg != nil && cfg.Server.Port == 9001
	})
}

func TestManager_WatchConfig_InvalidConfigDoesNotPolluteCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := writeTempConfigFile(t, tmpDir, minimalValidConfigYAML(9100))

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
	if err := m.WatchConfig(); err != nil {
		t.Fatalf("WatchConfig: %v", err)
	}

	ch := make(chan struct{}, 1)
	m.OnChange(func(oldCfg, newCfg *Config) {
		ch <- struct{}{}
	})

	// 写入无效配置。
	if err := os.WriteFile(cfgPath, []byte(minimalInvalidConfigYAML(9101)), 0o600); err != nil {
		t.Fatalf("rewrite config file: %v", err)
	}

	// 等待一小段时间，确保 watcher 有机会触发；但 current 不应改变。
	time.Sleep(300 * time.Millisecond)
	if got := m.Get().Server.Port; got != 9100 {
		t.Fatalf("server.port got %d, want %d", got, 9100)
	}

	select {
	case <-ch:
		t.Fatalf("OnChange should not be called on invalid config")
	default:
	}
}

func TestManager_WatchConfig_OnChangeCalledAfterUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := writeTempConfigFile(t, tmpDir, minimalValidConfigYAML(9200))

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

	called := make(chan struct{}, 10)
	m.OnChange(func(oldCfg, newCfg *Config) {
		// 回调触发时，current 应已经更新为 newCfg。
		cur := m.Get()
		if cur == nil || newCfg == nil {
			return
		}
		if cur.Server.Port != newCfg.Server.Port {
			return
		}
		if oldCfg != nil && oldCfg.Server.Port == 9200 && newCfg.Server.Port == 9201 {
			called <- struct{}{}
		}
	})

	if err := m.WatchConfig(); err != nil {
		t.Fatalf("WatchConfig: %v", err)
	}

	if err := os.WriteFile(cfgPath, []byte(minimalValidConfigYAML(9201)), 0o600); err != nil {
		t.Fatalf("rewrite config file: %v", err)
	}

	select {
	case <-called:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatalf("OnChange not called within timeout")
	}
}
