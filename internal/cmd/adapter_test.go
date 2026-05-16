package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/code"
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

func TestGiteaCommentAdapter_ListIssueCommentsFetchesMultiplePages(t *testing.T) {
	var requestedPages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/acme/repo/issues/42/comments" {
			http.NotFound(w, r)
			return
		}
		requestedPages = append(requestedPages, r.URL.Query().Get("page"))
		if got := r.URL.Query().Get("limit"); got != "50" {
			t.Errorf("limit = %q, want 50", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("Link", `<http://gitea.test/api/v1/repos/acme/repo/issues/42/comments?page=2&limit=50>; rel="next"`)
			_, _ = w.Write([]byte(`[{"id":1,"body":"第一页"}]`))
		case "2":
			_, _ = w.Write([]byte(`[null,{"id":2,"body":"第二页"}]`))
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	client, err := gitea.NewClient(srv.URL, gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("创建 Gitea client 失败: %v", err)
	}
	adapter := &giteaCommentAdapter{client: client}

	comments, err := adapter.ListIssueComments(context.Background(), "acme", "repo", 42)
	if err != nil {
		t.Fatalf("ListIssueComments 返回错误: %v", err)
	}
	if got, want := requestedPages, []string{"1", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("请求页序列 = %v, want %v", got, want)
	}
	if len(comments) != 2 {
		t.Fatalf("comments len = %d, want 2: %+v", len(comments), comments)
	}
	if comments[0].ID != 1 || comments[0].Body != "第一页" {
		t.Errorf("comments[0] = %+v", comments[0])
	}
	if comments[1].ID != 2 || comments[1].Body != "第二页" {
		t.Errorf("comments[1] = %+v", comments[1])
	}
}

func TestGiteaCodePRAdapter_ListRepoPullRequestsFiltersSameRepoAndPages(t *testing.T) {
	var requestedPages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/acme/repo/pulls" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Errorf("state = %q, want open", got)
		}
		if got := r.URL.Query().Get("limit"); got != "50" {
			t.Errorf("limit = %q, want 50", got)
		}
		requestedPages = append(requestedPages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Query().Get("page") {
		case "1":
			prs := make([]map[string]any, 0, 50)
			prs = append(prs, map[string]any{
				"number":   1,
				"html_url": "https://gitea.local/acme/repo/pulls/1",
				"head": map[string]any{
					"ref":  "feature/spec",
					"repo": map[string]any{"full_name": "fork/repo"},
				},
			})
			for i := 2; i <= 50; i++ {
				prs = append(prs, map[string]any{
					"number":   i,
					"html_url": fmt.Sprintf("https://gitea.local/acme/repo/pulls/%d", i),
					"head": map[string]any{
						"ref":  fmt.Sprintf("other-%d", i),
						"repo": map[string]any{"full_name": "acme/repo"},
					},
				})
			}
			_ = json.NewEncoder(w).Encode(prs)
		case "2":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"number":   99,
				"html_url": "https://gitea.local/acme/repo/pulls/99",
				"head": map[string]any{
					"ref":  "feature/spec",
					"repo": map[string]any{"full_name": "acme/repo"},
				},
			}})
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	client, err := gitea.NewClient(srv.URL, gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("创建 Gitea client 失败: %v", err)
	}
	adapter := &giteaCodePRAdapter{client: client}

	prs, err := adapter.ListRepoPullRequests(context.Background(), "acme", "repo", code.ListPullRequestsOptions{
		State: "open",
		Head:  "feature/spec",
	})
	if err != nil {
		t.Fatalf("ListRepoPullRequests 返回错误: %v", err)
	}
	if got, want := requestedPages, []string{"1", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("请求页序列 = %v, want %v", got, want)
	}
	if len(prs) != 1 {
		t.Fatalf("prs len = %d, want 1: %+v", len(prs), prs)
	}
	if prs[0].Number != 99 {
		t.Fatalf("复用 PR number = %d, want 99", prs[0].Number)
	}
}

func TestGiteaRepoFileChecker_HasFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/acme/repo/contents/services/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"api","path":"services/api","type":"dir"}`))
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

	ok, err = checker.HasFile(context.Background(), "acme", "repo", "main", "services/api", "")
	if err != nil {
		t.Fatalf("HasFile(dir) 返回错误: %v", err)
	}
	if !ok {
		t.Fatal("存在的目录应返回 true")
	}

	ok, err = checker.HasFile(context.Background(), "acme", "repo", "main", "services/api", "package.json")
	if err != nil {
		t.Fatalf("HasFile(not found) 返回错误: %v", err)
	}
	if ok {
		t.Fatal("不存在的文件应返回 false")
	}
}

func TestGiteaRepoFileChecker_ListDir(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := []gitea.ContentsResponse{
			{Name: "backend", Type: "dir"},
			{Name: "frontend", Type: "dir"},
			{Name: "README.md", Type: "file"},
			{Name: ".gitignore", Type: "file"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client, _ := gitea.NewClient(ts.URL, gitea.WithToken("test"))
	checker := &giteaRepoFileChecker{client: client}
	dirs, err := checker.ListDir(context.Background(), "owner", "repo", "main", "")
	if err != nil {
		t.Fatalf("ListDir 返回错误: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("期望 2 个子目录，实际 %d: %v", len(dirs), dirs)
	}
	if dirs[0] != "backend" || dirs[1] != "frontend" {
		t.Errorf("子目录不符合预期: %v", dirs)
	}
}
