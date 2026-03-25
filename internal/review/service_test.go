package review

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// --- mock 实现 ---

type mockPRClient struct {
	getPR   func(ctx context.Context, owner, repo string, index int64) (*gitea.PullRequest, *gitea.Response, error)
	listFiles func(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error)
}

func (m *mockPRClient) GetPullRequest(ctx context.Context, owner, repo string, index int64) (*gitea.PullRequest, *gitea.Response, error) {
	return m.getPR(ctx, owner, repo, index)
}

func (m *mockPRClient) ListPullRequestFiles(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error) {
	return m.listFiles(ctx, owner, repo, index, opts)
}

type mockReviewPool struct {
	runWithCommand func(ctx context.Context, payload model.TaskPayload, cmd []string) (*worker.ExecutionResult, error)
}

func (m *mockReviewPool) RunWithCommand(ctx context.Context, payload model.TaskPayload, cmd []string) (*worker.ExecutionResult, error) {
	return m.runWithCommand(ctx, payload, cmd)
}

type mockConfigProvider struct {
	override config.ReviewOverride
}

func (m *mockConfigProvider) ResolveReviewConfig(_ string) config.ReviewOverride {
	return m.override
}

// --- 辅助函数 ---

func openPR(prNum int64) *gitea.PullRequest {
	return &gitea.PullRequest{
		Number: prNum,
		Title:  "test PR",
		State:  "open",
		Body:   "test body",
		User:   &gitea.User{Login: "author"},
		Base: &gitea.PRBranch{
			Ref: "main",
			Repo: &gitea.Repository{
				FullName: "owner/repo",
			},
		},
	}
}

func noFiles() []*gitea.ChangedFile {
	return []*gitea.ChangedFile{}
}

func newService(prClient PRClient, pool ReviewPoolRunner, override config.ReviewOverride) *Service {
	return NewService(prClient, pool, &mockConfigProvider{override: override})
}

func testPayload() model.TaskPayload {
	return model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		PRNumber:     1,
		DeliveryID:   "test-delivery",
	}
}

// validCLIOutput 返回合法双层 JSON（外层 CLI 信封 + 内层 ReviewOutput）
func validCLIOutput() string {
	inner := `{"summary":"looks good","verdict":"approve","issues":[]}`
	return fmt.Sprintf(`{"type":"result","subtype":"success","cost_usd":0.01,"duration_ms":1000,"duration_api_ms":900,"is_error":false,"num_turns":1,"result":%q,"session_id":"sess-1"}`, inner)
}

// --- 测试用例 ---

func TestExecute_Success(t *testing.T) {
	svc := newService(
		&mockPRClient{
			getPR: func(_ context.Context, _, _ string, _ int64) (*gitea.PullRequest, *gitea.Response, error) {
				return openPR(1), nil, nil
			},
			listFiles: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error) {
				return noFiles(), nil, nil
			},
		},
		&mockReviewPool{
			runWithCommand: func(_ context.Context, _ model.TaskPayload, _ []string) (*worker.ExecutionResult, error) {
				return &worker.ExecutionResult{Output: validCLIOutput(), ExitCode: 0}, nil
			},
		},
		config.ReviewOverride{},
	)

	result, err := svc.Execute(context.Background(), testPayload())
	if err != nil {
		t.Fatalf("预期无错误，实际: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
	if result.Review == nil {
		t.Fatalf("Review 不应为 nil，ParseError: %v", result.ParseError)
	}
	if result.Review.Verdict != VerdictApprove {
		t.Errorf("预期 verdict=approve，实际: %s", result.Review.Verdict)
	}
}

func TestExecute_PRNotOpen(t *testing.T) {
	closedPR := openPR(1)
	closedPR.State = "closed"

	svc := newService(
		&mockPRClient{
			getPR: func(_ context.Context, _, _ string, _ int64) (*gitea.PullRequest, *gitea.Response, error) {
				return closedPR, nil, nil
			},
			listFiles: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error) {
				return noFiles(), nil, nil
			},
		},
		&mockReviewPool{},
		config.ReviewOverride{},
	)

	_, err := svc.Execute(context.Background(), testPayload())
	if err == nil {
		t.Fatal("预期返回错误")
	}
	if !errors.Is(err, ErrPRNotOpen) {
		t.Errorf("预期 ErrPRNotOpen，实际: %v", err)
	}
}

func TestExecute_InvalidPRNumber(t *testing.T) {
	svc := newService(&mockPRClient{}, &mockReviewPool{}, config.ReviewOverride{})

	payload := testPayload()
	payload.PRNumber = 0

	_, err := svc.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("预期返回错误")
	}
}

func TestExecute_GiteaAPIError(t *testing.T) {
	svc := newService(
		&mockPRClient{
			getPR: func(_ context.Context, _, _ string, _ int64) (*gitea.PullRequest, *gitea.Response, error) {
				return nil, nil, errors.New("connection refused")
			},
			listFiles: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error) {
				return noFiles(), nil, nil
			},
		},
		&mockReviewPool{},
		config.ReviewOverride{},
	)

	_, err := svc.Execute(context.Background(), testPayload())
	if err == nil {
		t.Fatal("预期返回错误")
	}
}

func TestExecute_ContainerError(t *testing.T) {
	svc := newService(
		&mockPRClient{
			getPR: func(_ context.Context, _, _ string, _ int64) (*gitea.PullRequest, *gitea.Response, error) {
				return openPR(1), nil, nil
			},
			listFiles: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error) {
				return noFiles(), nil, nil
			},
		},
		&mockReviewPool{
			runWithCommand: func(_ context.Context, _ model.TaskPayload, _ []string) (*worker.ExecutionResult, error) {
				return &worker.ExecutionResult{Output: "partial output", ExitCode: 1}, errors.New("container failed")
			},
		},
		config.ReviewOverride{},
	)

	result, err := svc.Execute(context.Background(), testPayload())
	if err == nil {
		t.Fatal("预期返回错误")
	}
	if result == nil {
		t.Fatal("容器失败时 result 不应为 nil")
	}
}

func TestParseResult_ValidJSON(t *testing.T) {
	svc := &Service{}
	result := svc.parseResult(validCLIOutput())

	if result.ParseError != nil {
		t.Fatalf("预期无解析错误，实际: %v", result.ParseError)
	}
	if result.CLIMeta == nil {
		t.Fatal("CLIMeta 不应为 nil")
	}
	if result.Review == nil {
		t.Fatal("Review 不应为 nil")
	}
	if result.Review.Verdict != VerdictApprove {
		t.Errorf("预期 verdict=approve，实际: %s", result.Review.Verdict)
	}
}

func TestParseResult_WithCodeFence(t *testing.T) {
	inner := `{"summary":"ok","verdict":"comment","issues":[]}`
	fenced := "```json\n" + inner + "\n```"
	outer := fmt.Sprintf(`{"type":"result","subtype":"success","is_error":false,"result":%q}`, fenced)

	svc := &Service{}
	result := svc.parseResult(outer)

	if result.ParseError != nil {
		t.Fatalf("预期无解析错误，实际: %v", result.ParseError)
	}
	if result.Review == nil {
		t.Fatal("Review 不应为 nil")
	}
}

func TestParseResult_InvalidOuterJSON(t *testing.T) {
	svc := &Service{}
	result := svc.parseResult("not valid json")

	if result.ParseError == nil {
		t.Fatal("预期外层 JSON 解析错误")
	}
}

func TestParseResult_CLIError(t *testing.T) {
	output := `{"type":"result","subtype":"error","is_error":true,"result":""}`

	svc := &Service{}
	result := svc.parseResult(output)

	if result.ParseError == nil {
		t.Fatal("预期 CLI 错误时返回 ParseError")
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "纯 JSON",
			input: `{"key":"value"}`,
			want:  `{"key":"value"}`,
		},
		{
			name:  "带 json code fence",
			input: "```json\n{\"key\":\"value\"}\n```",
			want:  `{"key":"value"}`,
		},
		{
			name:  "带无语言 code fence",
			input: "```\n{\"key\":\"value\"}\n```",
			want:  `{"key":"value"}`,
		},
		{
			name:  "前后有空白",
			input: "  \n{\"key\":\"value\"}\n  ",
			want:  `{"key":"value"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractJSON(tc.input)
			if got != tc.want {
				t.Errorf("extractJSON(%q) = %q, 预期 %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"短于限制", "hello", 10, "hello"},
		{"等于限制", "hello", 5, "hello"},
		{"超过限制", "hello world", 5, "hello..."},
		{"空字符串", "", 5, ""},
		{"多字节中文", "你好世界测试", 4, "你好世界..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.input, tc.maxLen)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, 预期 %q", tc.input, tc.maxLen, got, tc.want)
			}
		})
	}
}

func TestBuildPrompt(t *testing.T) {
	svc := &Service{}
	pr := openPR(42)
	pr.Body = "这是描述"
	files := []*gitea.ChangedFile{
		{Filename: "main.go", Additions: 10, Deletions: 2, Status: "modified"},
	}
	cfg := ReviewConfig{
		Instructions:     "自定义指令",
		Dimensions:       []string{"security", "logic"},
		LargePRThreshold: 5000,
	}

	prompt := svc.buildPrompt(pr, files, cfg)

	// 检查四段内容均存在
	checks := []string{
		"PR #42",         // 1. 任务上下文
		"自定义指令",          // 2. 评审指令
		"Output Format",  // 3. 输出格式约束
		"main.go",        // 文件列表
	}
	for _, check := range checks {
		if !containsString(prompt, check) {
			t.Errorf("prompt 缺少预期内容: %q", check)
		}
	}
}

func TestBuildPrompt_RepoInstructions(t *testing.T) {
	svc := &Service{}
	pr := openPR(1)
	cfg := ReviewConfig{
		Instructions:     "全局指令",
		RepoInstructions: "仓库级追加指令",
		LargePRThreshold: 5000,
	}

	prompt := svc.buildPrompt(pr, noFiles(), cfg)

	if !containsString(prompt, "仓库级追加指令") {
		t.Error("prompt 应包含 RepoInstructions")
	}
}

func TestBuildPrompt_LargePRGuidance(t *testing.T) {
	svc := &Service{}
	pr := openPR(1)

	// 构造超过阈值的文件列表
	files := make([]*gitea.ChangedFile, 1)
	files[0] = &gitea.ChangedFile{Filename: "big.go", Additions: 3000, Deletions: 2001, Status: "modified"}

	cfg := ReviewConfig{
		Instructions:     "指令",
		LargePRThreshold: 5000,
	}

	prompt := svc.buildPrompt(pr, files, cfg)

	if !containsString(prompt, "Large PR Notice") {
		t.Error("超大 PR 应包含 Large PR Notice")
	}
}

func TestResolveConfig_Defaults(t *testing.T) {
	svc := &Service{
		cfgProv: &mockConfigProvider{override: config.ReviewOverride{}},
	}

	cfg := svc.resolveConfig("owner/repo")

	if cfg.Instructions == "" {
		t.Error("默认 Instructions 不应为空")
	}
	if len(cfg.Dimensions) == 0 {
		t.Error("默认 Dimensions 不应为空")
	}
	if cfg.LargePRThreshold <= 0 {
		t.Error("默认 LargePRThreshold 应大于 0")
	}
}

func TestResolveConfig_Override(t *testing.T) {
	svc := &Service{
		cfgProv: &mockConfigProvider{override: config.ReviewOverride{
			Instructions:     "自定义指令",
			Dimensions:       []string{"security"},
			LargePRThreshold: 1000,
		}},
	}

	cfg := svc.resolveConfig("owner/repo")

	if cfg.Instructions != "自定义指令" {
		t.Errorf("Instructions 应为 '自定义指令'，实际: %s", cfg.Instructions)
	}
	if len(cfg.Dimensions) != 1 || cfg.Dimensions[0] != "security" {
		t.Errorf("Dimensions 应为 [security]，实际: %v", cfg.Dimensions)
	}
	if cfg.LargePRThreshold != 1000 {
		t.Errorf("LargePRThreshold 应为 1000，实际: %d", cfg.LargePRThreshold)
	}
}

// containsString 检查 s 是否包含 sub
func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
