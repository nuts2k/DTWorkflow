package cmd

import (
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
)

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
