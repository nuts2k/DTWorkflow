package worker

import (
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
		"GITEA_URL":       "http://gitea.example.com",
		"GITEA_TOKEN":     "token123",
		"ANTHROPIC_API_KEY": "sk-claude-key",
		"REPO_CLONE_URL":  "http://gitea.example.com/owner/repo.git",
		"REPO_OWNER":      "owner",
		"REPO_NAME":       "repo",
		"REPO_FULL_NAME":  "owner/repo",
		"PR_NUMBER":       "42",
		"HEAD_REF":        "feature/test",
		"BASE_REF":        "main",
		"HEAD_SHA":        "abc123",
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
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if envMap["MODULE"] != "internal/auth" {
		t.Errorf("MODULE = %q, 期望 internal/auth", envMap["MODULE"])
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
		{"-1", 0, true},
		{"abc", 0, true},
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
		{"-1g", 0, true},
		{"abc", 0, true},
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
