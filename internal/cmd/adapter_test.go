package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
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

func TestConfigAdapter_ResolveTestGenConfig_ReturnsMergedOverride(t *testing.T) {
	cfgPath := writeTestConfigFile(t,
		"gitea:\n  url: \"http://gitea:3000\"\n  token: \"test-token\"\n"+
			"claude:\n  api_key: \"test-api-key\"\n"+
			"webhook:\n  secret: \"test-secret\"\n"+
			"notify:\n  default_channel: \"gitea\"\n  channels:\n    gitea:\n      enabled: true\n"+
			"test_gen:\n  test_framework: \"junit5\"\n  max_retry_rounds: 3\n"+
			"repos:\n  - name: \"acme/repo\"\n    test_gen:\n      module_scope: \"services/api\"\n      test_framework: \"vitest\"\n")

	mgr, err := config.NewManager(config.WithDefaults(), config.WithConfigFile(cfgPath))
	if err != nil {
		t.Fatalf("config.NewManager 失败: %v", err)
	}
	if err := mgr.Load(); err != nil {
		t.Fatalf("mgr.Load 失败: %v", err)
	}

	a := &configAdapter{mgr: mgr}
	got := a.ResolveTestGenConfig("acme/repo")
	if got.ModuleScope != "services/api" {
		t.Fatalf("ModuleScope = %q, want %q", got.ModuleScope, "services/api")
	}
	if got.TestFramework != "vitest" {
		t.Fatalf("TestFramework = %q, want %q", got.TestFramework, "vitest")
	}
	if got.MaxRetryRounds != 3 {
		t.Fatalf("MaxRetryRounds = %d, want 3", got.MaxRetryRounds)
	}
}

func TestGiteaRepoFileChecker_HasFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/acme/repo/contents/services/api/pom.xml":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"pom.xml","path":"services/api/pom.xml","type":"file"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := gitea.NewClient(srv.URL, gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("创建 Gitea client 失败: %v", err)
	}

	checker := &giteaRepoFileChecker{client: client}
	ok, err := checker.HasFile(context.Background(), "acme", "repo", "main", "services/api", "pom.xml")
	if err != nil {
		t.Fatalf("HasFile 返回错误: %v", err)
	}
	if !ok {
		t.Fatal("HasFile 应返回 true")
	}

	ok, err = checker.HasFile(context.Background(), "acme", "repo", "main", "services/api", "package.json")
	if err != nil {
		t.Fatalf("HasFile(not found) 返回错误: %v", err)
	}
	if ok {
		t.Fatal("不存在的文件应返回 false")
	}
}
