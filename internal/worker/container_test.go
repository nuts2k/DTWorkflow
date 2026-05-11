package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

func TestBuildContainerEnv_ReviewPR(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token123",
		ClaudeAPIKey: "sk-claude-key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		PRNumber:     42,
		HeadRef:      "feature/test",
		BaseRef:      "main",
		HeadSHA:      "abc123",
	}

	env := buildContainerEnv(config, payload)

	// 检查必填字段
	mustContain := map[string]string{
		"GITEA_URL":         "http://gitea.example.com",
		"GITEA_TOKEN":       "token123",
		"ANTHROPIC_API_KEY": "sk-claude-key",
		"REPO_CLONE_URL":    "http://gitea.example.com/owner/repo.git",
		"REPO_OWNER":        "owner",
		"REPO_NAME":         "repo",
		"REPO_FULL_NAME":    "owner/repo",
		"PR_NUMBER":         "42",
		"HEAD_REF":          "feature/test",
		"BASE_REF":          "main",
		"HEAD_SHA":          "abc123",
	}

	envMap := envSliceToMap(env)
	for key, expectedVal := range mustContain {
		if got, ok := envMap[key]; !ok {
			t.Errorf("缺少环境变量 %s", key)
		} else if got != expectedVal {
			t.Errorf("环境变量 %s = %q, 期望 %q", key, got, expectedVal)
		}
	}

	// review_pr 不应包含 ISSUE_NUMBER
	if _, ok := envMap["ISSUE_NUMBER"]; ok {
		t.Error("review_pr 任务不应包含 ISSUE_NUMBER")
	}
}

func TestBuildContainerEnv_FixIssue(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		IssueNumber:  10,
		IssueTitle:   "Bug in login",
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if envMap["ISSUE_NUMBER"] != "10" {
		t.Errorf("ISSUE_NUMBER = %q, 期望 10", envMap["ISSUE_NUMBER"])
	}
	if envMap["ISSUE_TITLE"] != "Bug in login" {
		t.Errorf("ISSUE_TITLE = %q, 期望 Bug in login", envMap["ISSUE_TITLE"])
	}
	// fix_issue 不应包含 PR_NUMBER
	if _, ok := envMap["PR_NUMBER"]; ok {
		t.Error("fix_issue 任务不应包含 PR_NUMBER")
	}
}

// TestBuildContainerEnv_TokenSelectionByTaskType 验证按任务类型注入正确的 GITEA_TOKEN。
// fix_issue 走 GiteaTokenFix；gen_tests 走 GiteaTokenGenTests；只读任务走 GiteaToken。
// 设计目标：拆账号后 fix 创建的 PR 可以被 review 账号评审，规避 Gitea 自评审限制。
func TestBuildContainerEnv_TokenSelectionByTaskType(t *testing.T) {
	config := PoolConfig{
		GiteaURL:           "http://gitea.example.com",
		GiteaToken:         "tok-review",
		GiteaTokenFix:      "tok-fix",
		GiteaTokenGenTests: "tok-gen-tests",
		ClaudeAPIKey:       "key",
	}
	cases := []struct {
		name     string
		taskType model.TaskType
		wantTok  string
	}{
		{"review_pr 用 review token", model.TaskTypeReviewPR, "tok-review"},
		{"analyze_issue 用 review token", model.TaskTypeAnalyzeIssue, "tok-review"},
		{"gen_tests 用 gen_tests token", model.TaskTypeGenTests, "tok-gen-tests"},
		{"fix_issue 用 fix token", model.TaskTypeFixIssue, "tok-fix"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := model.TaskPayload{TaskType: tc.taskType, RepoFullName: "o/r"}
			env := buildContainerEnv(config, payload)
			envMap := envSliceToMap(env)
			if envMap["GITEA_TOKEN"] != tc.wantTok {
				t.Errorf("GITEA_TOKEN = %q, 期望 %q", envMap["GITEA_TOKEN"], tc.wantTok)
			}
		})
	}
}

// TestBuildContainerEnv_FixTokenFallback 验证 GiteaTokenFix 为空时回退到 GiteaToken，
// 保持向后兼容（单 token 部署）。
func TestBuildContainerEnv_FixTokenFallback(t *testing.T) {
	config := PoolConfig{
		GiteaURL:   "http://gitea.example.com",
		GiteaToken: "only-token",
		// GiteaTokenFix 留空
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{TaskType: model.TaskTypeFixIssue, RepoFullName: "o/r"}
	env := buildContainerEnv(config, payload)
	if envSliceToMap(env)["GITEA_TOKEN"] != "only-token" {
		t.Errorf("fix_issue 在 GiteaTokenFix 为空时应回退到 GiteaToken")
	}
}

// TestBuildContainerEnv_GenTestsTokenFallback 验证 GiteaTokenGenTests 为空时回退到 GiteaToken。
func TestBuildContainerEnv_GenTestsTokenFallback(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "only-token",
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{TaskType: model.TaskTypeGenTests, RepoFullName: "o/r"}
	env := buildContainerEnv(config, payload)
	if envSliceToMap(env)["GITEA_TOKEN"] != "only-token" {
		t.Errorf("gen_tests 在 GiteaTokenGenTests 为空时应回退到 GiteaToken")
	}
}

func TestBuildContainerEnv_GenTests(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		Module:       "internal/auth",
		BaseRef:      "develop",
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if envMap["MODULE"] != "internal/auth" {
		t.Errorf("MODULE = %q, 期望 internal/auth", envMap["MODULE"])
	}
	// M4.2：gen_tests 必须注入 BASE_REF
	if got, ok := envMap["BASE_REF"]; !ok {
		t.Error("gen_tests 任务应注入 BASE_REF")
	} else if got != "develop" {
		t.Errorf("BASE_REF = %q, 期望 develop", got)
	}
	// M4.2：gen_tests 必须注入 MODULE_SANITIZED（"internal/auth" → "internal-auth"）
	if got, ok := envMap["MODULE_SANITIZED"]; !ok {
		t.Error("gen_tests 任务应注入 MODULE_SANITIZED")
	} else if got != "internal-auth" {
		t.Errorf("MODULE_SANITIZED = %q, 期望 internal-auth", got)
	}
}

// M4.2：module 含 / 应清洗为 -
func TestBuildContainerEnv_GenTests_ModuleSanitizedSlash(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		Module:       "srv/api",
		BaseRef:      "main",
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if envMap["MODULE_SANITIZED"] != "srv-api" {
		t.Errorf("MODULE_SANITIZED = %q, 期望 srv-api", envMap["MODULE_SANITIZED"])
	}
	if envMap["BASE_REF"] != "main" {
		t.Errorf("BASE_REF = %q, 期望 main", envMap["BASE_REF"])
	}
}

// M4.2：module 为空应回落为 "all"
func TestBuildContainerEnv_GenTests_ModuleSanitizedEmpty(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		Module:       "",
		BaseRef:      "",
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	// 空 module → MODULE_SANITIZED=all
	if got, ok := envMap["MODULE_SANITIZED"]; !ok {
		t.Error("gen_tests 任务（空 module）应注入 MODULE_SANITIZED")
	} else if got != "all" {
		t.Errorf("MODULE_SANITIZED = %q, 期望 all", got)
	}
	// 空 BaseRef 也应注入（entrypoint 负责回落到默认分支）
	if got, ok := envMap["BASE_REF"]; !ok {
		t.Error("gen_tests 任务应注入 BASE_REF（即便为空）")
	} else if got != "" {
		t.Errorf("BASE_REF = %q, 期望空串", got)
	}
}

// M4.2：review_pr / analyze_issue / fix_issue case 不应注入 MODULE_SANITIZED（避免污染其他任务类型）
func TestBuildContainerEnv_NonGenTests_NoModuleSanitized(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
	}
	cases := []struct {
		name    string
		payload model.TaskPayload
	}{
		{
			name: "review_pr",
			payload: model.TaskPayload{
				TaskType:     model.TaskTypeReviewPR,
				RepoOwner:    "owner",
				RepoName:     "repo",
				RepoFullName: "owner/repo",
				CloneURL:     "http://gitea.example.com/owner/repo.git",
				PRNumber:     1,
				BaseRef:      "main",
			},
		},
		{
			name: "analyze_issue",
			payload: model.TaskPayload{
				TaskType:     model.TaskTypeAnalyzeIssue,
				RepoOwner:    "owner",
				RepoName:     "repo",
				RepoFullName: "owner/repo",
				CloneURL:     "http://gitea.example.com/owner/repo.git",
				IssueNumber:  1,
				IssueTitle:   "t",
			},
		},
		{
			name: "fix_issue",
			payload: model.TaskPayload{
				TaskType:     model.TaskTypeFixIssue,
				RepoOwner:    "owner",
				RepoName:     "repo",
				RepoFullName: "owner/repo",
				CloneURL:     "http://gitea.example.com/owner/repo.git",
				IssueNumber:  1,
				IssueTitle:   "t",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := buildContainerEnv(config, tc.payload)
			envMap := envSliceToMap(env)
			if _, ok := envMap["MODULE_SANITIZED"]; ok {
				t.Errorf("%s 任务不应注入 MODULE_SANITIZED，实际: %q", tc.name, envMap["MODULE_SANITIZED"])
			}
			// review_pr 本身就会注入 BASE_REF（既有行为），此处仅对非 review_pr
			// 验证 gen_tests 专属注入不会泄漏到 analyze_issue / fix_issue
			if tc.payload.TaskType != model.TaskTypeReviewPR {
				if _, ok := envMap["BASE_REF"]; ok {
					t.Errorf("%s 任务不应注入 BASE_REF，实际: %q", tc.name, envMap["BASE_REF"])
				}
			}
		})
	}
}

func TestBuildContainerCmd_ReviewPR(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		RepoFullName: "owner/repo",
		PRNumber:     42,
	}
	cmd := buildContainerCmd(payload)
	if len(cmd) < 3 {
		t.Fatalf("命令长度不足: %v", cmd)
	}
	if cmd[0] != "claude" {
		t.Errorf("命令第一个参数应为 claude, 得到 %s", cmd[0])
	}
	if cmd[1] != "-p" {
		t.Errorf("命令第二个参数应为 -p, 得到 %s", cmd[1])
	}
}

func TestBuildContainerCmd_FixIssue(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoFullName: "owner/repo",
		IssueNumber:  5,
		IssueTitle:   "crash on startup",
	}
	cmd := buildContainerCmd(payload)
	if cmd[0] != "claude" || cmd[1] != "-p" {
		t.Errorf("命令格式错误: %v", cmd)
	}
}

func TestBuildContainerCmd_GenTests(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		RepoFullName: "owner/repo",
	}
	cmd := buildContainerCmd(payload)
	if cmd[0] != "claude" || cmd[1] != "-p" {
		t.Errorf("命令格式错误: %v", cmd)
	}
}

func TestParseCPULimit(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"2.0", 2_000_000_000, false},
		{"1.5", 1_500_000_000, false},
		{"0.5", 500_000_000, false},
		{"", 0, false},
		{"0", 0, false},
		{"  2.0  ", 2_000_000_000, false}, // 前后空格
		{"-1", 0, true},
		{"abc", 0, true},
		{"64", 0, true}, // 超出上限 (maxCPUCores=32)
	}

	for _, tc := range tests {
		got, err := parseCPULimit(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseCPULimit(%q) 期望错误，但没有", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("parseCPULimit(%q) 非预期错误: %v", tc.input, err)
			}
			if got != tc.expected {
				t.Errorf("parseCPULimit(%q) = %d, 期望 %d", tc.input, got, tc.expected)
			}
		}
	}
}

func TestParseMemoryLimit(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"4g", 4 * 1024 * 1024 * 1024, false},
		{"512m", 512 * 1024 * 1024, false},
		{"1024k", 1024 * 1024, false},
		{"1024", 1024, false},
		{"", 0, false},
		{"0g", 0, false},                       // 零值
		{"4gb", 4 * 1024 * 1024 * 1024, false}, // gb 后缀
		{"512mb", 512 * 1024 * 1024, false},    // mb 后缀
		{"1024kb", 1024 * 1024, false},         // kb 后缀
		{"1024b", 1024, false},                 // b 后缀
		{"1.5g", 1610612736, false},            // 浮点数（走 float 分支）
		{"0.0g", 0, false},                     // 浮点零
		{"-1g", 0, true},
		{"-1", 0, true}, // 整数负数无后缀
		{"abc", 0, true},
		{"128g", 0, true},          // 超出 64GB 上限
		{"999999999999g", 0, true}, // 溢出检测
		{"-1.5g", 0, true},         // 浮点负数
		{"100g", 0, true},          // 超 64GB 上限（整数路径）
		{"100.0g", 0, true},        // 超 64GB 上限（浮点路径）
	}

	for _, tc := range tests {
		got, err := parseMemoryLimit(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseMemoryLimit(%q) 期望错误，但没有", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("parseMemoryLimit(%q) 非预期错误: %v", tc.input, err)
			}
			if got != tc.expected {
				t.Errorf("parseMemoryLimit(%q) = %d, 期望 %d", tc.input, got, tc.expected)
			}
		}
	}
}

func TestSanitizeInput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"正常输入", "hello world", "hello world"},
		{"换行符", "line1\nline2", "line1 line2"},
		{"回车符", "cr\rhere", "cr here"},
		{"空字节", "has\x00null", "hasnull"},
		{"混合恶意字符", "a\nb\rc\x00d", "a b cd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeInput(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeInput(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSanitizePromptInput(t *testing.T) {
	// 正常输入不截断
	result := sanitizePromptInput("hello", 100)
	if result != "hello" {
		t.Errorf("sanitizePromptInput 正常输入 = %q, 期望 hello", result)
	}

	// 超长输入截断
	long := strings.Repeat("中文", 200) // 200 个中文字符
	result = sanitizePromptInput(long, 50)
	runes := []rune(result)
	if len(runes) != 50 {
		t.Errorf("截断后 rune 数 = %d, 期望 50", len(runes))
	}

	// 包含换行的输入清理后再截断
	result = sanitizePromptInput("a\nb\nc", 3)
	if result != "a b" {
		t.Errorf("sanitizePromptInput 换行+截断 = %q, 期望 'a b'", result)
	}
}

func TestBuildContainerEnv_InsecureSkipVerify(t *testing.T) {
	config := PoolConfig{
		GiteaURL:                "https://gitea.example.com",
		GiteaToken:              "token",
		ClaudeAPIKey:            "key",
		GiteaInsecureSkipVerify: true,
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "https://gitea.example.com/owner/repo.git",
		PRNumber:     1,
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if envMap["GIT_SSL_NO_VERIFY"] != "true" {
		t.Error("开启 InsecureSkipVerify 时应设置 GIT_SSL_NO_VERIFY=true")
	}
}

func TestBuildContainerEnv_NoInsecureSkipVerify(t *testing.T) {
	config := PoolConfig{
		GiteaURL:                "https://gitea.example.com",
		GiteaToken:              "token",
		ClaudeAPIKey:            "key",
		GiteaInsecureSkipVerify: false,
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "https://gitea.example.com/owner/repo.git",
		PRNumber:     1,
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if _, ok := envMap["GIT_SSL_NO_VERIFY"]; ok {
		t.Error("未开启 InsecureSkipVerify 时不应包含 GIT_SSL_NO_VERIFY")
	}
}

func TestBuildContainerEnv_ClaudeBaseURL(t *testing.T) {
	config := PoolConfig{
		GiteaURL:      "http://gitea.example.com",
		GiteaToken:    "token",
		ClaudeAPIKey:  "key",
		ClaudeBaseURL: "https://proxy.example.com",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		PRNumber:     1,
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if envMap["ANTHROPIC_BASE_URL"] != "https://proxy.example.com" {
		t.Errorf("ANTHROPIC_BASE_URL = %q, 期望 https://proxy.example.com", envMap["ANTHROPIC_BASE_URL"])
	}
}

func TestBuildContainerCmd_ReviewPR_PromptContent(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		RepoFullName: "owner/repo",
		PRNumber:     42,
		BaseRef:      "develop",
	}
	cmd := buildContainerCmd(payload)

	prompt := cmd[2]
	// 验证 prompt 包含关键指引
	checks := []string{
		"PR #42",
		"owner/repo",
		"develop",
		"git diff",
		"cloned",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("prompt 应包含 %q，实际: %s", check, prompt)
		}
	}
}

func TestBuildContainerCmd_ReviewPR_DefaultBaseRef(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		RepoFullName: "owner/repo",
		PRNumber:     1,
		BaseRef:      "", // 空值应回退到 main
	}
	cmd := buildContainerCmd(payload)

	if !strings.Contains(cmd[2], "main") {
		t.Error("BaseRef 为空时 prompt 应默认使用 main")
	}
}

func TestBuildContainerCmd_GenTests_EmptyModule(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		RepoFullName: "owner/repo",
		Module:       "", // 空模块应回退到 "."
	}
	cmd := buildContainerCmd(payload)
	if !strings.Contains(cmd[2], ".") {
		t.Error("Module 为空时 prompt 应使用 '.'")
	}
}

func TestBuildContainerCmd_UnknownTaskType(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:     "unknown_task",
		RepoFullName: "owner/repo",
	}
	cmd := buildContainerCmd(payload)
	if cmd[0] != "claude" || cmd[1] != "-p" {
		t.Errorf("未知任务类型命令格式错误: %v", cmd)
	}
	if !strings.Contains(cmd[2], "unknown_task") {
		t.Error("未知任务类型 prompt 应包含任务类型名")
	}
}

func TestBuildContainerEnv_GenTests_EmptyModule(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		Module:       "", // 空模块时不应添加 MODULE 环境变量
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if _, ok := envMap["MODULE"]; ok {
		t.Error("Module 为空时不应包含 MODULE 环境变量")
	}
}

func TestBuildContainerCmd_FixIssue_PromptContent(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoFullName: "owner/repo",
		IssueNumber:  5,
		IssueTitle:   "crash on startup",
	}
	cmd := buildContainerCmd(payload)

	prompt := cmd[2]
	if !strings.Contains(prompt, "cloned") {
		t.Error("fix_issue prompt 应提示仓库已 clone")
	}
	if !strings.Contains(prompt, "crash on startup") {
		t.Error("fix_issue prompt 应包含 issue 标题")
	}
}

// envSliceToMap 将 KEY=VALUE 格式的环境变量切片转换为 map
func envSliceToMap(env []string) map[string]string {
	result := make(map[string]string, len(env))
	for _, e := range env {
		if key, val, ok := strings.Cut(e, "="); ok {
			result[key] = val
		}
	}
	return result
}

func TestBuildContainerEnv_FixIssueWithRef(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		IssueNumber:  10,
		IssueTitle:   "Bug in login",
		IssueRef:     "feature/user-auth",
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if envMap["ISSUE_REF"] != "feature/user-auth" {
		t.Errorf("ISSUE_REF = %q, 期望 %q", envMap["ISSUE_REF"], "feature/user-auth")
	}
}

func TestBuildContainerEnv_FixIssueWithoutRef(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		IssueNumber:  10,
		IssueTitle:   "Bug in login",
		IssueRef:     "",
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if _, ok := envMap["ISSUE_REF"]; ok {
		t.Error("ref 为空时不应包含 ISSUE_REF")
	}
}

func TestBuildContainerEnv_AnalyzeIssue(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeAnalyzeIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		IssueNumber:  15,
		IssueTitle:   "Login page crash",
		IssueRef:     "main",
	}
	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)
	if envMap["TASK_TYPE"] != "analyze_issue" {
		t.Errorf("TASK_TYPE = %q, 期望 analyze_issue", envMap["TASK_TYPE"])
	}
	if envMap["ISSUE_NUMBER"] != "15" {
		t.Errorf("ISSUE_NUMBER = %q, 期望 15", envMap["ISSUE_NUMBER"])
	}
	if envMap["ISSUE_TITLE"] != "Login page crash" {
		t.Errorf("ISSUE_TITLE = %q, 期望 Login page crash", envMap["ISSUE_TITLE"])
	}
	if envMap["ISSUE_REF"] != "main" {
		t.Errorf("ISSUE_REF = %q, 期望 main", envMap["ISSUE_REF"])
	}
	if _, ok := envMap["PR_NUMBER"]; ok {
		t.Error("analyze_issue 不应包含 PR_NUMBER")
	}
}

func TestBuildContainerCmd_AnalyzeIssue(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeAnalyzeIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  15,
		IssueTitle:   "Login page crash",
		IssueRef:     "release/v1.0",
	}
	cmd := buildContainerCmd(payload)
	if len(cmd) < 2 || cmd[0] != "claude" || cmd[1] != "-p" {
		t.Fatalf("命令前缀应为 [claude -p], 实际: %v", cmd[:min(len(cmd), 2)])
	}
	prompt := cmd[2]
	if !strings.Contains(prompt, "#15") {
		t.Error("prompt 应包含 Issue 编号 #15")
	}
	if !strings.Contains(strings.ToLower(prompt), "analyze") {
		t.Error("prompt 应包含分析指令关键词")
	}
	if strings.Contains(prompt, "Fix issue") || strings.Contains(prompt, "implement a fix") {
		t.Error("analyze_issue prompt 不应包含祈使句修复指令")
	}
}

func TestBuildContainerCmd_FixIssueWithRef(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
		IssueTitle:   "Bug",
		IssueRef:     "feature/auth",
	}

	cmd := buildContainerCmd(payload)
	prompt := strings.Join(cmd, " ")
	if !strings.Contains(prompt, "ref 'feature/auth' is checked out") {
		t.Errorf("prompt 应包含 ref checkout 信息，实际: %s", prompt)
	}
}

func TestModuleKeyForContainerWithFramework(t *testing.T) {
	tests := []struct {
		module    string
		framework string
		want      string
	}{
		{"backend", "", "backend"},
		{"", "", "all"},
		{"", "junit5", "all-junit5"},
		{"", "vitest", "all-vitest"},
		{"mono", "junit5", "mono-junit5"},
		{"mono", "vitest", "mono-vitest"},
		{"svc/api", "", "svc-api"},
		{"svc/api", "junit5", "svc-api-junit5"},
	}
	for _, tt := range tests {
		t.Run(tt.module+"_"+tt.framework, func(t *testing.T) {
			got := moduleKeyForContainerWithFramework(tt.module, tt.framework)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestModuleKeyConsistencyWithTestPackage 验证 moduleKeyForContainerWithFramework 与
// internal/test.BuildAutoTestBranchName 语义一致。
// 由于 internal/test → internal/worker 存在反向依赖（import cycle），
// 此处使用本地等价实现（moduleKeyForContainer + framework 后缀）重现 BuildAutoTestBranchName 逻辑，
// 与 worker 实现做交叉验证。期望值手动维护自 internal/test.BuildAutoTestBranchName 的定义：
//
//	auto-test/<moduleKey>[-framework]
func TestModuleKeyConsistencyWithTestPackage(t *testing.T) {
	cases := []struct {
		module    string
		framework string
		// wantBranchSuffix 是 auto-test/ 之后的部分，等价于 BuildAutoTestBranchName 去除前缀的结果
		wantBranchSuffix string
	}{
		{"", "", "all"},
		{"", "junit5", "all-junit5"},
		{"", "vitest", "all-vitest"},
		{"backend", "", "backend"},
		{"backend", "junit5", "backend-junit5"},
		{"frontend", "vitest", "frontend-vitest"},
		{"mono", "junit5", "mono-junit5"},
		{"mono", "vitest", "mono-vitest"},
		{"svc/api", "", "svc-api"},
		{"svc/api", "junit5", "svc-api-junit5"},
		{"a..b", "", "a.b"},
		{"--special--", "vitest", "special-vitest"},
		{"   ", "", "all"},
	}
	for _, tt := range cases {
		t.Run(tt.module+"_"+tt.framework, func(t *testing.T) {
			workerKey := moduleKeyForContainerWithFramework(tt.module, tt.framework)
			if workerKey != tt.wantBranchSuffix {
				t.Errorf("一致性失败: worker=%q, 期望与 BuildAutoTestBranchName 对齐=%q", workerKey, tt.wantBranchSuffix)
			}
		})
	}
}

func TestBuildContainerEnv_RunE2E(t *testing.T) {
	config := PoolConfig{
		Image:        "test-image",
		GiteaURL:     "https://gitea.test",
		GiteaToken:   "test-token",
		ClaudeAPIKey: "test-key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeRunE2E,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "https://gitea.test/owner/repo.git",
		BaseRef:      "main",
		ExtraEnvs: []string{
			"E2E_BASE_URL=https://staging.example.com",
			"E2E_DB_HOST=db.internal",
		},
	}
	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if envMap["BASE_REF"] != "main" {
		t.Errorf("BASE_REF = %q, 期望 main", envMap["BASE_REF"])
	}
	if envMap["E2E_BASE_URL"] != "https://staging.example.com" {
		t.Errorf("E2E_BASE_URL = %q, 期望 https://staging.example.com", envMap["E2E_BASE_URL"])
	}
	if envMap["E2E_DB_HOST"] != "db.internal" {
		t.Errorf("E2E_DB_HOST = %q, 期望 db.internal", envMap["E2E_DB_HOST"])
	}
}

func TestResolveImage_RunE2E(t *testing.T) {
	p := &Pool{config: PoolConfig{Image: "worker:latest", ImageE2E: "worker-e2e:latest"}}
	if got := p.resolveImage(model.TaskTypeRunE2E); got != "worker-e2e:latest" {
		t.Errorf("resolveImage(RunE2E) = %q, 期望 worker-e2e:latest", got)
	}

	p2 := &Pool{config: PoolConfig{Image: "worker:latest"}}
	if got := p2.resolveImage(model.TaskTypeRunE2E); got != "worker:latest" {
		t.Errorf("resolveImage(RunE2E, 无 ImageE2E) = %q, 期望 worker:latest（回退基础镜像）", got)
	}
}

func TestBuildBinds_RunE2EArtifactDirIsWorldWritable(t *testing.T) {
	dataDir := t.TempDir()
	p := &Pool{config: PoolConfig{DataDir: dataDir}}
	payload := model.TaskPayload{
		TaskID:   "task-1",
		TaskType: model.TaskTypeRunE2E,
	}

	binds := p.buildBinds(payload)
	if len(binds) != 1 {
		t.Fatalf("期望 1 个 bind，实际=%d", len(binds))
	}

	artifactDir := filepath.Join(dataDir, "e2e-artifacts", "task-1")
	wantBind := artifactDir + ":/workspace/artifacts"
	if binds[0] != wantBind {
		t.Fatalf("bind = %q，期望 %q", binds[0], wantBind)
	}

	info, err := os.Stat(artifactDir)
	if err != nil {
		t.Fatalf("artifact 目录不存在: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o777 {
		t.Fatalf("artifact 目录权限 = %o，期望 777", got)
	}
}

// TestModuleKeyForContainerParity 验证 moduleKeyForContainer 与 internal/test.ModuleKey 语义一致。
// 由于 test → worker 反向依赖无法在此 import internal/test，手动维护等价期望值。
// 若本测试失败，需同步检查 internal/test.ModuleKey 是否也有对应变更；
// 若 moduleKeyForContainer 需要修改，必须同步更新 internal/test.ModuleKey。
func TestModuleKeyForContainerParity(t *testing.T) {
	cases := []struct {
		module string
		want   string
	}{
		{"", "all"},
		{"services/api", "services-api"},
		{"packages/web/ui", "packages-web-ui"},
		{"   ", "all"},
		{"中文module", "module"},
		{"svc with space", "svc-with-space"},
		{"svc:foo", "svc-foo"},
		{"-foo-", "foo"},
		{".foo.", "foo"},
		{"a/../b", "a-b"},
		{"foo..bar", "foo.bar"},
		{"\x00\x00", "all"},
	}
	for _, c := range cases {
		if got := moduleKeyForContainer(c.module); got != c.want {
			t.Errorf("moduleKeyForContainer(%q)=%q, want %q (必须与 internal/test.ModuleKey 一致)", c.module, got, c.want)
		}
	}
}
