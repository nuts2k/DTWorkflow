package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/dtw"
)

// --- flag 校验器单元测试 ---

func TestValidateGenTestsFrameworkFlag_AllowedValues(t *testing.T) {
	cases := []string{"", "junit5", "vitest"}
	for _, v := range cases {
		if err := validateGenTestsFrameworkFlag(v); err != nil {
			t.Errorf("validateGenTestsFrameworkFlag(%q) = %v，期望 nil", v, err)
		}
	}
}

func TestValidateGenTestsFrameworkFlag_RejectsOthers(t *testing.T) {
	cases := []string{"jest", "mocha", "junit", "JUNIT5", "Vitest", "pytest", " "}
	for _, v := range cases {
		if err := validateGenTestsFrameworkFlag(v); err == nil {
			t.Errorf("validateGenTestsFrameworkFlag(%q) = nil，期望错误", v)
		}
	}
}

func TestValidateGenTestsModuleFlag_EmptyIsAllowed(t *testing.T) {
	if err := validateGenTestsModuleFlag(""); err != nil {
		t.Errorf("空 module 不应报错，实际: %v", err)
	}
}

func TestValidateGenTestsModuleFlag_RejectsEscapePaths(t *testing.T) {
	bad := []string{
		"/etc/passwd",       // 绝对路径
		"..",                // 纯 ..
		"../sibling",        // 以 ../ 开头
		"a/../b",            // 中间 /../
		"a/..",              // 以 /.. 结尾
	}
	for _, m := range bad {
		if err := validateGenTestsModuleFlag(m); err == nil {
			t.Errorf("validateGenTestsModuleFlag(%q) = nil，期望错误", m)
		}
	}
}

func TestValidateGenTestsModuleFlag_AllowsLegitPaths(t *testing.T) {
	ok := []string{
		"service",
		"service/api",
		"modules/frontend",
		"a/b/c",
	}
	for _, m := range ok {
		if err := validateGenTestsModuleFlag(m); err != nil {
			t.Errorf("validateGenTestsModuleFlag(%q) = %v，期望 nil", m, err)
		}
	}
}

// --- Cobra 命令注册测试 ---

func TestGenTestsCmd_RegisteredOnRoot(t *testing.T) {
	// rootCmd.Find 需要传入子命令名数组；命中则返回非 nil
	c, _, err := rootCmd.Find([]string{"gen-tests"})
	if err != nil {
		t.Fatalf("rootCmd.Find(gen-tests) 返回错误: %v", err)
	}
	if c == nil || c.Name() != "gen-tests" {
		t.Fatalf("未找到 gen-tests 命令；Find 返回 %v", c)
	}
}

func TestGenTestsCmd_RequiredFlags(t *testing.T) {
	// --repo 必需，MarkFlagRequired 会让 Flag 的 Annotations 包含 cobra.BashCompOneRequiredFlag
	flag := genTestsCmd.Flag("repo")
	if flag == nil {
		t.Fatal("未定义 --repo 标志")
	}
	if _, ok := flag.Annotations["cobra_annotation_bash_completion_one_required_flag"]; !ok {
		t.Errorf("--repo 应标记为必需")
	}
}

// --- HTTP 交互测试 ---

// setupTestClient 生成一个指向 srv 的全局 client/printer，并返回 restore 函数。
func setupTestClient(t *testing.T, serverURL string) func() {
	t.Helper()

	prevClient := client
	prevPrinter := printer
	prevJSON := flagJSON

	client = dtw.NewClient(serverURL, "test-token")
	// 使用 Discard 避免测试输出污染
	printer = &dtw.Printer{Mode: dtw.OutputHuman, Writer: io.Discard}
	flagJSON = false

	return func() {
		client = prevClient
		printer = prevPrinter
		flagJSON = prevJSON
	}
}

// resetGenTestsFlags 在用例之间重置 gen-tests 命令的全局 flag 变量。
func resetGenTestsFlags(t *testing.T) {
	t.Helper()
	prev := struct {
		repo, module, ref, framework string
		noWait                       bool
		timeout                      time.Duration
	}{
		repo: genTestsRepo, module: genTestsModule, ref: genTestsRef, framework: genTestsFramework,
		noWait: genTestsNoWait, timeout: genTestsTimeout,
	}
	genTestsRepo = ""
	genTestsModule = ""
	genTestsRef = ""
	genTestsFramework = ""
	genTestsNoWait = false
	genTestsTimeout = 0
	t.Cleanup(func() {
		genTestsRepo = prev.repo
		genTestsModule = prev.module
		genTestsRef = prev.ref
		genTestsFramework = prev.framework
		genTestsNoWait = prev.noWait
		genTestsTimeout = prev.timeout
	})
}

// writeDataEnvelope 仿照 client.Do 期望的 `{"data": ...}` 响应 envelope
func writeDataEnvelope(t *testing.T, w http.ResponseWriter, code int, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
		t.Fatalf("响应编码失败: %v", err)
	}
}

func TestGenTestsCmd_InvalidRepoFormat(t *testing.T) {
	resetGenTestsFlags(t)

	genTestsRepo = "owner" // 缺少 /
	genTestsNoWait = true

	err := genTestsCmd.RunE(genTestsCmd, nil)
	if err == nil {
		t.Fatal("期望 --repo 格式错误，实际 nil")
	}
	if !strings.Contains(err.Error(), "owner/repo") {
		t.Errorf("错误信息应提及 owner/repo，实际: %v", err)
	}
}

func TestGenTestsCmd_InvalidFrameworkFlag(t *testing.T) {
	resetGenTestsFlags(t)

	genTestsRepo = "owner/repo"
	genTestsFramework = "jest"
	genTestsNoWait = true

	err := genTestsCmd.RunE(genTestsCmd, nil)
	if err == nil {
		t.Fatal("期望 framework 校验失败，实际 nil")
	}
	if !strings.Contains(err.Error(), "framework") {
		t.Errorf("错误应提及 framework，实际: %v", err)
	}
}

func TestGenTestsCmd_InvalidModuleFlag(t *testing.T) {
	resetGenTestsFlags(t)

	genTestsRepo = "owner/repo"
	genTestsModule = "../escape"
	genTestsNoWait = true

	err := genTestsCmd.RunE(genTestsCmd, nil)
	if err == nil {
		t.Fatal("期望 module 校验失败，实际 nil")
	}
	if !strings.Contains(err.Error(), "module") {
		t.Errorf("错误应提及 module，实际: %v", err)
	}
}

// TestGenTestsCmd_PostsExpectedPathAndBody 核心测试：
//  1. 请求方法与路径符合 Task #8 冻结签名
//  2. body 里只携带用户显式提供的字段
//  3. 提交成功后返回的 task_id 被正确消费（--no-wait）
func TestGenTestsCmd_PostsExpectedPathAndBody(t *testing.T) {
	resetGenTestsFlags(t)

	var capturedPath string
	var capturedMethod string
	var capturedAuth string
	var capturedBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		capturedAuth = r.Header.Get("Authorization")

		capturedBody = map[string]string{}
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("解析请求体失败: %v", err)
		}

		writeDataEnvelope(t, w, http.StatusAccepted, map[string]string{"task_id": "abc123"})
	}))
	defer srv.Close()

	restore := setupTestClient(t, srv.URL)
	defer restore()

	genTestsRepo = "alice/widgets"
	genTestsModule = "service"
	genTestsRef = "develop"
	genTestsFramework = "junit5"
	genTestsNoWait = true

	genTestsCmd.SetContext(context.Background())
	if err := genTestsCmd.RunE(genTestsCmd, nil); err != nil {
		t.Fatalf("RunE 失败: %v", err)
	}

	if capturedMethod != http.MethodPost {
		t.Errorf("HTTP 方法 = %q，期望 POST", capturedMethod)
	}
	if capturedPath != "/api/v1/repos/alice/widgets/gen-tests" {
		t.Errorf("HTTP 路径 = %q，期望 /api/v1/repos/alice/widgets/gen-tests", capturedPath)
	}
	if capturedAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q，期望 Bearer test-token", capturedAuth)
	}

	if got := capturedBody["module"]; got != "service" {
		t.Errorf("body.module = %q，期望 service", got)
	}
	if got := capturedBody["ref"]; got != "develop" {
		t.Errorf("body.ref = %q，期望 develop", got)
	}
	if got := capturedBody["framework"]; got != "junit5" {
		t.Errorf("body.framework = %q，期望 junit5", got)
	}
}

// TestGenTestsCmd_OmitsEmptyFields 校验空 flag 不出现在 body 中（由 API 层决定默认值）
func TestGenTestsCmd_OmitsEmptyFields(t *testing.T) {
	resetGenTestsFlags(t)

	var capturedBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody = map[string]string{}
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("解析请求体失败: %v", err)
		}
		writeDataEnvelope(t, w, http.StatusAccepted, map[string]string{"task_id": "t-xyz"})
	}))
	defer srv.Close()

	restore := setupTestClient(t, srv.URL)
	defer restore()

	genTestsRepo = "alice/widgets"
	genTestsNoWait = true

	genTestsCmd.SetContext(context.Background())
	if err := genTestsCmd.RunE(genTestsCmd, nil); err != nil {
		t.Fatalf("RunE 失败: %v", err)
	}

	if _, has := capturedBody["module"]; has {
		t.Errorf("空 module 不应出现在 body: %+v", capturedBody)
	}
	if _, has := capturedBody["ref"]; has {
		t.Errorf("空 ref 不应出现在 body: %+v", capturedBody)
	}
	if _, has := capturedBody["framework"]; has {
		t.Errorf("空 framework 不应出现在 body: %+v", capturedBody)
	}
}

// TestGenTestsCmd_ServerErrorPropagates 验证后端 400 错误能被返回给调用方
func TestGenTestsCmd_ServerErrorPropagates(t *testing.T) {
	resetGenTestsFlags(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "invalid_argument", "message": "module 非法"},
		})
	}))
	defer srv.Close()

	restore := setupTestClient(t, srv.URL)
	defer restore()

	genTestsRepo = "alice/widgets"
	genTestsNoWait = true

	genTestsCmd.SetContext(context.Background())
	err := genTestsCmd.RunE(genTestsCmd, nil)
	if err == nil {
		t.Fatal("期望 400 错误被返回，实际 nil")
	}
	if !strings.Contains(err.Error(), "提交 gen_tests 失败") {
		t.Errorf("错误应包含包装前缀 '提交 gen_tests 失败'，实际: %v", err)
	}
}
