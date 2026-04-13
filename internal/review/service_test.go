package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// --- mock 实现 ---

type mockPRClient struct {
	getPR     func(ctx context.Context, owner, repo string, index int64) (*gitea.PullRequest, *gitea.Response, error)
	listFiles func(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error)
}

func (m *mockPRClient) GetPullRequest(ctx context.Context, owner, repo string, index int64) (*gitea.PullRequest, *gitea.Response, error) {
	return m.getPR(ctx, owner, repo, index)
}

func (m *mockPRClient) ListPullRequestFiles(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error) {
	return m.listFiles(ctx, owner, repo, index, opts)
}

type mockReviewPool struct {
	runWithCommandAndStdin func(ctx context.Context, payload model.TaskPayload, cmd []string, stdinData []byte) (*worker.ExecutionResult, error)
}

func (m *mockReviewPool) RunWithCommandAndStdin(ctx context.Context, payload model.TaskPayload, cmd []string, stdinData []byte) (*worker.ExecutionResult, error) {
	if m.runWithCommandAndStdin != nil {
		return m.runWithCommandAndStdin(ctx, payload, cmd, stdinData)
	}
	return nil, errors.New("RunWithCommandAndStdin not implemented")
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

type stubWritebackClient struct {
	reviewID int64
	err      error
}

func (s *stubWritebackClient) CreatePullReview(_ context.Context, _, _ string, _ int64, _ gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
	return &gitea.PullReview{ID: s.reviewID}, nil, nil
}

func (s *stubWritebackClient) GetPullRequestDiff(_ context.Context, _, _ string, _ int64) (string, *gitea.Response, error) {
	if s.err != nil {
		return "", nil, s.err
	}
	return simpleDiff, nil, nil
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
			runWithCommandAndStdin: func(_ context.Context, _ model.TaskPayload, _ []string, _ []byte) (*worker.ExecutionResult, error) {
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

func TestExecute_StdinPassedCorrectly(t *testing.T) {
	var capturedStdin []byte
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
			runWithCommandAndStdin: func(_ context.Context, _ model.TaskPayload, _ []string, stdinData []byte) (*worker.ExecutionResult, error) {
				capturedStdin = stdinData
				return &worker.ExecutionResult{Output: validCLIOutput(), ExitCode: 0}, nil
			},
		},
		config.ReviewOverride{},
	)

	_, err := svc.Execute(context.Background(), testPayload())
	if err != nil {
		t.Fatalf("预期无错误，实际: %v", err)
	}
	if len(capturedStdin) == 0 {
		t.Error("stdin 数据不应为空")
	}
	// prompt 应包含 PR 上下文
	if !strings.Contains(string(capturedStdin), "PR #1") {
		t.Error("stdin 应包含 PR 上下文")
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
			runWithCommandAndStdin: func(_ context.Context, _ model.TaskPayload, _ []string, _ []byte) (*worker.ExecutionResult, error) {
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

func TestExecute_WritebackDegradedPreservesReviewIDAndError(t *testing.T) {
	svc := NewService(
		&mockPRClient{
			getPR: func(_ context.Context, _, _ string, _ int64) (*gitea.PullRequest, *gitea.Response, error) {
				pr := openPR(1)
				pr.Head = &gitea.PRBranch{SHA: "head-sha"}
				return pr, nil, nil
			},
			listFiles: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error) {
				return noFiles(), nil, nil
			},
		},
		&mockReviewPool{
			runWithCommandAndStdin: func(_ context.Context, _ model.TaskPayload, _ []string, _ []byte) (*worker.ExecutionResult, error) {
				return &worker.ExecutionResult{Output: validCLIOutput(), ExitCode: 0}, nil
			},
		},
		&mockConfigProvider{override: config.ReviewOverride{}},
		WithWriter(NewWriter(&stubWritebackClient{
			reviewID: 42,
			err:      errors.New("diff unavailable"),
		}, nil, nil, nil)),
	)

	result, err := svc.Execute(context.Background(), testPayload())
	if err != nil {
		t.Fatalf("回写降级不应让 Execute 返回错误，实际: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
	if result.GiteaReviewID != 42 {
		t.Fatalf("应保留已创建的 Gitea review ID，实际: %d", result.GiteaReviewID)
	}
	if result.WritebackError == nil {
		t.Fatal("降级回写应保留 WritebackError")
	}
	if !strings.Contains(result.WritebackError.Error(), "获取 PR diff 失败") {
		t.Fatalf("WritebackError 应包含 diff 获取失败信息，实际: %v", result.WritebackError)
	}
}

func TestExecute_StaleWriteback_ReturnsErrStaleReview(t *testing.T) {
	svc := NewService(
		&mockPRClient{
			getPR: func(_ context.Context, _, _ string, _ int64) (*gitea.PullRequest, *gitea.Response, error) {
				pr := openPR(1)
				pr.Head = &gitea.PRBranch{SHA: "head-sha"}
				return pr, nil, nil
			},
			listFiles: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error) {
				return noFiles(), nil, nil
			},
		},
		&mockReviewPool{
			runWithCommandAndStdin: func(_ context.Context, _ model.TaskPayload, _ []string, _ []byte) (*worker.ExecutionResult, error) {
				return &worker.ExecutionResult{Output: validCLIOutput(), ExitCode: 0}, nil
			},
		},
		&mockConfigProvider{override: config.ReviewOverride{}},
		WithWriter(NewWriter(&stubWritebackClient{reviewID: 42}, nil, &mockStaleChecker{hasNewer: true}, nil)),
	)

	payload := testPayload()
	payload.CreatedAt = time.Now().Add(-time.Minute)

	result, err := svc.Execute(context.Background(), payload)
	if !errors.Is(err, ErrStaleReview) {
		t.Fatalf("stale writeback 应返回 ErrStaleReview，实际: %v", err)
	}
	if result == nil {
		t.Fatal("stale writeback 时 result 不应为 nil")
	}
	if result.GiteaReviewID != 0 {
		t.Fatalf("stale writeback 不应生成 GiteaReviewID，实际: %d", result.GiteaReviewID)
	}
	if result.WritebackError != nil {
		t.Fatalf("stale writeback 不应被记录为普通 WritebackError，实际: %v", result.WritebackError)
	}
}

func TestExecute_ListFilesError(t *testing.T) {
	listErr := errors.New("gitea: connection refused")
	svc := newService(
		&mockPRClient{
			getPR: func(_ context.Context, _, _ string, _ int64) (*gitea.PullRequest, *gitea.Response, error) {
				return openPR(1), nil, nil
			},
			listFiles: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error) {
				return nil, nil, listErr
			},
		},
		&mockReviewPool{},
		config.ReviewOverride{},
	)

	_, err := svc.Execute(context.Background(), testPayload())
	if err == nil {
		t.Fatal("预期 ListPullRequestFiles 错误应被返回")
	}
	if !strings.Contains(err.Error(), "文件列表") {
		t.Errorf("错误信息应包含\"文件列表\"，实际: %v", err)
	}
	if !errors.Is(err, listErr) {
		t.Errorf("原始错误应被包装在返回错误中，实际: %v", err)
	}
}

func TestParseResult_InvalidInnerJSON(t *testing.T) {
	// 外层 JSON 合法，但 result 字段不是合法 JSON 对象
	outer := `{"type":"result","subtype":"success","is_error":false,"result":"not-a-valid-json-object"}`

	svc := &Service{}
	result := svc.parseResult(outer)

	if result.ParseError == nil {
		t.Fatal("畸形内层 JSON 应返回非 nil ParseError")
	}
	if !strings.Contains(result.ParseError.Error(), "评审 JSON 解析失败") {
		t.Errorf("ParseError 应包含\"评审 JSON 解析失败\"，实际: %v", result.ParseError)
	}
	if result.Review != nil {
		t.Error("内层 JSON 解析失败时 Review 应为 nil")
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

func TestParseResult_ErrorDuringExecution(t *testing.T) {
	// 复现 PR #69 故障：type=error_during_execution 但 is_error=false
	output := `{
		"type": "error_during_execution",
		"is_error": false,
		"total_cost_usd": 0,
		"duration_ms": 35351,
		"duration_api_ms": 34993,
		"num_turns": 1,
		"session_id": "ea9734bb-78bc-4596-a3a2-420a5ea756f7"
	}`

	svc := &Service{}
	result := svc.parseResult(output)

	if result.ParseError == nil {
		t.Fatal("error_during_execution 响应应返回 ParseError")
	}
	if result.CLIMeta == nil {
		t.Fatal("CLIMeta 应被填充")
	}
	if !result.CLIMeta.IsError {
		t.Error("CLIMeta.IsError 应为 true（type 不是 result）")
	}
	if result.Review != nil {
		t.Error("Review 应为 nil")
	}
}

func TestParseResult_TotalCostUSD(t *testing.T) {
	// 验证新版 CLI 的 total_cost_usd 字段能正确映射到 CostUSD
	inner := `{"summary":"ok","verdict":"comment","issues":[]}`
	output := fmt.Sprintf(`{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.05,"result":%q}`, inner)

	svc := &Service{}
	result := svc.parseResult(output)

	if result.ParseError != nil {
		t.Fatalf("预期无解析错误，实际: %v", result.ParseError)
	}
	if result.CLIMeta.CostUSD != 0.05 {
		t.Errorf("CostUSD = %f, want 0.05", result.CLIMeta.CostUSD)
	}
}

func TestParseResult_StreamJsonSuccess(t *testing.T) {
	// 流式监控路径下 tryExtractResultCLIJSON 将 type="result"+subtype="success"
	// 转换为 type="success"，parseResult 不应将其视为错误
	inner := `{"summary":"looks good","verdict":"approve","issues":[]}`
	output := fmt.Sprintf(`{"type":"success","is_error":false,"total_cost_usd":0.03,"duration_ms":12000,"num_turns":1,"result":%q,"session_id":"stream-sess"}`, inner)

	svc := &Service{}
	result := svc.parseResult(output)

	if result.ParseError != nil {
		t.Fatalf("流式 success 不应产生解析错误，实际: %v", result.ParseError)
	}
	if result.CLIMeta == nil || result.CLIMeta.IsError {
		t.Fatal("CLIMeta.IsError 应为 false")
	}
	if result.Review == nil {
		t.Fatal("Review 应被成功解析")
	}
	if result.Review.Verdict != VerdictApprove {
		t.Errorf("Verdict = %q, want %q", result.Review.Verdict, VerdictApprove)
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
		{
			name:  "code fence 前有前导文本",
			input: "Here is my review:\n```json\n{\"key\":\"value\"}\n```",
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

	prompt := svc.buildPrompt(pr, files, cfg, 0)

	// 检查四段内容均存在
	checks := []string{
		"PR #42",        // 1. 任务上下文
		"自定义指令",         // 2. 评审指令
		"输出格式", // 3. 输出格式约束
		"main.go",       // 文件列表
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
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

	prompt := svc.buildPrompt(pr, noFiles(), cfg, 0)

	if !strings.Contains(prompt, "仓库级追加指令") {
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

	prompt := svc.buildPrompt(pr, files, cfg, 0)

	if !strings.Contains(prompt, "大 PR 提示") {
		t.Error("超大 PR 应包含大 PR 提示")
	}
}

func TestBuildPrompt_TechStack(t *testing.T) {
	svc := &Service{}
	pr := openPR(1)
	cfg := ReviewConfig{
		Instructions:     "指令",
		LargePRThreshold: 5000,
	}

	t.Run("Java 专项段", func(t *testing.T) {
		prompt := svc.buildPrompt(pr, noFiles(), cfg, TechJava)
		if !strings.Contains(prompt, "Java 专项评审") {
			t.Error("Java 技术栈应包含 Java 专项评审段")
		}
		if strings.Contains(prompt, "Vue 专项评审") {
			t.Error("Java 技术栈不应包含 Vue 专项评审段")
		}
	})

	t.Run("Vue 专项段", func(t *testing.T) {
		prompt := svc.buildPrompt(pr, noFiles(), cfg, TechVue)
		if !strings.Contains(prompt, "Vue 专项评审") {
			t.Error("Vue 技术栈应包含 Vue 专项评审段")
		}
		if strings.Contains(prompt, "Java 专项评审") {
			t.Error("Vue 技术栈不应包含 Java 专项评审段")
		}
	})

	t.Run("Java+Vue 混合", func(t *testing.T) {
		prompt := svc.buildPrompt(pr, noFiles(), cfg, TechJava|TechVue)
		if !strings.Contains(prompt, "Java 专项评审") {
			t.Error("混合技术栈应包含 Java 专项评审段")
		}
		if !strings.Contains(prompt, "Vue 专项评审") {
			t.Error("混合技术栈应包含 Vue 专项评审段")
		}
	})

	t.Run("无技术栈", func(t *testing.T) {
		prompt := svc.buildPrompt(pr, noFiles(), cfg, 0)
		if strings.Contains(prompt, "Java 专项评审") {
			t.Error("无技术栈不应包含 Java 专项评审段")
		}
		if strings.Contains(prompt, "Vue 专项评审") {
			t.Error("无技术栈不应包含 Vue 专项评审段")
		}
	})
}

func TestBuildCodeStandardsSection(t *testing.T) {
	t.Run("无自定义路径使用默认", func(t *testing.T) {
		result := buildCodeStandardsSection(nil)
		if !strings.Contains(result, "CLAUDE.md") {
			t.Error("默认规范段应包含 CLAUDE.md 引导")
		}
	})

	t.Run("自定义路径列表", func(t *testing.T) {
		paths := []string{"docs/java-standards.md", "docs/api-guide.md"}
		result := buildCodeStandardsSection(paths)
		if !strings.Contains(result, "docs/java-standards.md") {
			t.Error("自定义规范段应包含指定路径")
		}
		if !strings.Contains(result, "docs/api-guide.md") {
			t.Error("自定义规范段应包含指定路径")
		}
		// 自定义路径时不应出现默认扫描列表
		if strings.Contains(result, "CONTRIBUTING.md") {
			t.Error("自定义路径时不应出现默认扫描列表")
		}
	})

	t.Run("空切片使用默认", func(t *testing.T) {
		result := buildCodeStandardsSection([]string{})
		if !strings.Contains(result, "CLAUDE.md") {
			t.Error("空切片应使用默认规范段")
		}
	})
}

func TestDetectTechStack(t *testing.T) {
	tests := []struct {
		name  string
		files []*gitea.ChangedFile
		want  TechStack
	}{
		{
			name:  ".java 文件 -> TechJava",
			files: []*gitea.ChangedFile{{Filename: "src/main/java/Foo.java"}},
			want:  TechJava,
		},
		{
			name:  ".vue 文件 -> TechVue",
			files: []*gitea.ChangedFile{{Filename: "src/views/Home.vue"}},
			want:  TechVue,
		},
		{
			name: ".ts + .vue 信号 -> TechVue",
			files: []*gitea.ChangedFile{
				{Filename: "src/views/Home.vue"},
				{Filename: "src/composables/useUser.ts"},
			},
			want: TechVue,
		},
		{
			name: "混合 .java + .vue -> TechJava|TechVue",
			files: []*gitea.ChangedFile{
				{Filename: "src/main/java/Foo.java"},
				{Filename: "frontend/src/views/Home.vue"},
			},
			want: TechJava | TechVue,
		},
		{
			name:  "纯 Go 文件 -> 无技术栈",
			files: []*gitea.ChangedFile{{Filename: "main.go"}},
			want:  0,
		},
		{
			name:  "空列表 -> 无技术栈",
			files: []*gitea.ChangedFile{},
			want:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectTechStack(tc.files)
			if got != tc.want {
				t.Errorf("detectTechStack() = %d, 预期 %d", got, tc.want)
			}
		})
	}
}

func TestHasVueSignal(t *testing.T) {
	t.Run(".vue 文件", func(t *testing.T) {
		files := []*gitea.ChangedFile{{Filename: "src/App.vue"}}
		if !hasVueSignal(files) {
			t.Error("有 .vue 文件应返回 true")
		}
	})

	t.Run("src/components 路径", func(t *testing.T) {
		files := []*gitea.ChangedFile{{Filename: "src/components/Button.ts"}}
		if !hasVueSignal(files) {
			t.Error("src/components 路径应返回 true")
		}
	})

	t.Run("src/stores 路径", func(t *testing.T) {
		files := []*gitea.ChangedFile{{Filename: "src/stores/user.ts"}}
		if !hasVueSignal(files) {
			t.Error("src/stores 路径应返回 true")
		}
	})

	t.Run("无信号", func(t *testing.T) {
		files := []*gitea.ChangedFile{
			{Filename: "main.go"},
			{Filename: "internal/service/user.go"},
		}
		if hasVueSignal(files) {
			t.Error("无 Vue 信号应返回 false")
		}
	})
}

func TestResolveTechStack(t *testing.T) {
	t.Run("配置覆盖自动检测", func(t *testing.T) {
		files := []*gitea.ChangedFile{{Filename: "Foo.java"}}
		cfg := ReviewConfig{TechStack: []string{"vue"}}
		got, unknown := resolveTechStack(files, cfg)
		if got != TechVue {
			t.Errorf("配置优先：预期 TechVue，实际 %d", got)
		}
		if len(unknown) != 0 {
			t.Errorf("不应有未识别的技术栈，实际: %v", unknown)
		}
	})

	t.Run("无配置回退自动检测", func(t *testing.T) {
		files := []*gitea.ChangedFile{{Filename: "Foo.java"}}
		cfg := ReviewConfig{}
		got, unknown := resolveTechStack(files, cfg)
		if got != TechJava {
			t.Errorf("自动检测：预期 TechJava，实际 %d", got)
		}
		if len(unknown) != 0 {
			t.Errorf("不应有未识别的技术栈，实际: %v", unknown)
		}
	})

	t.Run("配置多技术栈", func(t *testing.T) {
		files := []*gitea.ChangedFile{}
		cfg := ReviewConfig{TechStack: []string{"java", "vue"}}
		got, unknown := resolveTechStack(files, cfg)
		if got != TechJava|TechVue {
			t.Errorf("多技术栈配置：预期 TechJava|TechVue，实际 %d", got)
		}
		if len(unknown) != 0 {
			t.Errorf("不应有未识别的技术栈，实际: %v", unknown)
		}
	})

	t.Run("未识别技术栈返回 unknown", func(t *testing.T) {
		files := []*gitea.ChangedFile{}
		cfg := ReviewConfig{TechStack: []string{"java", "kotlin", "flutter"}}
		got, unknown := resolveTechStack(files, cfg)
		if got != TechJava {
			t.Errorf("预期 TechJava，实际 %d", got)
		}
		if len(unknown) != 2 || unknown[0] != "kotlin" || unknown[1] != "flutter" {
			t.Errorf("预期 unknown=[kotlin flutter]，实际: %v", unknown)
		}
	})
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

func TestResolveConfig_TechStackAndCodeStandards(t *testing.T) {
	svc := &Service{
		cfgProv: &mockConfigProvider{override: config.ReviewOverride{
			TechStack:          []string{"java", "vue"},
			CodeStandardsPaths: []string{"docs/standards.md"},
		}},
	}

	cfg := svc.resolveConfig("owner/repo")

	if len(cfg.TechStack) != 2 {
		t.Errorf("TechStack 应有 2 项，实际: %v", cfg.TechStack)
	}
	if len(cfg.CodeStandardsPaths) != 1 || cfg.CodeStandardsPaths[0] != "docs/standards.md" {
		t.Errorf("CodeStandardsPaths 不正确，实际: %v", cfg.CodeStandardsPaths)
	}
}

// TestResolveConfig_SeverityAndIgnorePatterns M2.5: resolveConfig 应正确传递 Severity 和 IgnorePatterns
func TestResolveConfig_SeverityAndIgnorePatterns(t *testing.T) {
	svc := &Service{
		cfgProv: &mockConfigProvider{override: config.ReviewOverride{
			Severity:       "error",
			IgnorePatterns: []string{"**/*.md", "docs/**"},
		}},
	}

	cfg := svc.resolveConfig("owner/repo")

	if cfg.Severity != "error" {
		t.Errorf("Severity 应为 'error'，实际: %s", cfg.Severity)
	}
	if len(cfg.IgnorePatterns) != 2 {
		t.Errorf("IgnorePatterns 应有 2 项，实际: %v", cfg.IgnorePatterns)
	}
	if cfg.IgnorePatterns[0] != "**/*.md" {
		t.Errorf("IgnorePatterns[0] 应为 '**/*.md'，实际: %s", cfg.IgnorePatterns[0])
	}
}

// TestResolveConfig_EmptySeverityAndPatterns M2.5: Severity 和 IgnorePatterns 为空时不影响默认值
func TestResolveConfig_EmptySeverityAndPatterns(t *testing.T) {
	svc := &Service{
		cfgProv: &mockConfigProvider{override: config.ReviewOverride{}},
	}

	cfg := svc.resolveConfig("owner/repo")

	if cfg.Severity != "" {
		t.Errorf("空配置时 Severity 应为空字符串，实际: %s", cfg.Severity)
	}
	if len(cfg.IgnorePatterns) != 0 {
		t.Errorf("空配置时 IgnorePatterns 应为空，实际: %v", cfg.IgnorePatterns)
	}
}

func TestParseLocation(t *testing.T) {
	tests := []struct {
		name     string
		loc      string
		wantFile string
		wantLine int
	}{
		{"file:line", "path/File.java:42", "path/File.java", 42},
		{"多位置取第一个", "path/A.java:10; path/B.java:20", "path/A.java", 10},
		{"仅文件名无行号", "path/File.java", "path/File.java", 0},
		{"空字符串", "", "", 0},
		{"行号为零", "path/File.java:0", "path/File.java", 0},
		{"行号非数字", "path/File.java:abc", "path/File.java:abc", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file, line := parseLocation(tc.loc)
			if file != tc.wantFile {
				t.Errorf("file = %q, want %q", file, tc.wantFile)
			}
			if line != tc.wantLine {
				t.Errorf("line = %d, want %d", line, tc.wantLine)
			}
		})
	}
}

func TestNormalizeIssues_LocationTitleDetail(t *testing.T) {
	// 模拟 Claude 使用 location/title/detail 替代 file/category/message 的情况
	rawJSON := `{
		"summary": "test",
		"verdict": "request_changes",
		"issues": [
			{
				"severity": "ERROR",
				"location": "src/Service.java:62; src/Other.java:100",
				"title": "权限校验缺失",
				"detail": "接口未校验用户权限",
				"suggestion": "添加权限校验"
			}
		]
	}`
	// json.Unmarshal 会填充已知字段（severity、suggestion），normalizeIssues 补充未知字段
	issues := []ReviewIssue{
		{Severity: "ERROR", Suggestion: "添加权限校验"},
	}

	normalizeIssues(rawJSON, issues)

	if issues[0].File != "src/Service.java" {
		t.Errorf("File = %q, want %q", issues[0].File, "src/Service.java")
	}
	if issues[0].Line != 62 {
		t.Errorf("Line = %d, want %d", issues[0].Line, 62)
	}
	if !strings.Contains(issues[0].Message, "权限校验缺失") {
		t.Errorf("Message 应包含 title，实际: %q", issues[0].Message)
	}
	if !strings.Contains(issues[0].Message, "接口未校验用户权限") {
		t.Errorf("Message 应包含 detail，实际: %q", issues[0].Message)
	}
	if strings.Contains(issues[0].Message, "\n\n") {
		t.Errorf("Message 不应包含空行，实际: %q", issues[0].Message)
	}
	if issues[0].Suggestion != "添加权限校验" {
		t.Errorf("Suggestion = %q, want %q", issues[0].Suggestion, "添加权限校验")
	}
}

func TestNormalizeIssues_LocationComplementsExistingFile(t *testing.T) {
	rawJSON := `{
		"issues": [
			{
				"file": "src/Service.java",
				"location": "src/Service.java:62",
				"severity": "ERROR"
			}
		]
	}`
	issues := []ReviewIssue{
		{File: "src/Service.java", Severity: "ERROR"},
	}

	normalizeIssues(rawJSON, issues)

	if issues[0].File != "src/Service.java" {
		t.Errorf("File = %q, want %q", issues[0].File, "src/Service.java")
	}
	if issues[0].Line != 62 {
		t.Errorf("Line = %d, want %d", issues[0].Line, 62)
	}
}

func TestNormalizeIssues_StandardFields_NoOverwrite(t *testing.T) {
	// 当标准字段已有值时，不应被覆盖
	rawJSON := `{
		"issues": [
			{
				"file": "main.go",
				"line": 10,
				"severity": "WARNING",
				"category": "logic",
				"message": "原始消息"
			}
		]
	}`
	issues := []ReviewIssue{
		{File: "main.go", Line: 10, Severity: "WARNING", Category: "logic", Message: "原始消息"},
	}

	normalizeIssues(rawJSON, issues)

	if issues[0].File != "main.go" {
		t.Errorf("File 不应被覆盖, got %q", issues[0].File)
	}
	if issues[0].Message != "原始消息" {
		t.Errorf("Message 不应被覆盖, got %q", issues[0].Message)
	}
}

func TestNormalizeIssues_FallbackToSuggestion(t *testing.T) {
	// message 和 detail 均为空时，用 suggestion 兜底
	rawJSON := `{
		"issues": [
			{
				"severity": "ERROR",
				"suggestion": "兜底建议内容"
			}
		]
	}`
	issues := []ReviewIssue{
		{Severity: "ERROR", Suggestion: "兜底建议内容"},
	}

	normalizeIssues(rawJSON, issues)

	if issues[0].Message != "兜底建议内容" {
		t.Errorf("Message 应回退到 suggestion，实际: %q", issues[0].Message)
	}
	if issues[0].Suggestion != "" {
		t.Errorf("回退后 Suggestion 应清空，实际: %q", issues[0].Suggestion)
	}
}

func TestNormalizeIssues_Description(t *testing.T) {
	// Claude 使用 description 替代 message
	rawJSON := `{
		"issues": [
			{
				"severity": "WARNING",
				"description": "描述性文字"
			}
		]
	}`
	issues := []ReviewIssue{
		{Severity: "WARNING"},
	}

	normalizeIssues(rawJSON, issues)

	if issues[0].Message != "描述性文字" {
		t.Errorf("Message = %q, want %q", issues[0].Message, "描述性文字")
	}
}

func TestParseResult_NormalizeIssues_Integration(t *testing.T) {
	// 端到端：Claude 返回 location/title/detail，parseResult 应正确规范化
	inner := `{"summary":"found issues","verdict":"request_changes","issues":[{"severity":"ERROR","location":"src/App.java:42","title":"SQL注入","detail":"拼接SQL存在注入风险","suggestion":"使用参数化查询"}]}`
	outer := fmt.Sprintf(`{"type":"result","subtype":"success","is_error":false,"cost_usd":0.01,"duration_ms":1000,"num_turns":1,"result":%q,"session_id":"sess-1"}`, inner)

	svc := &Service{}
	result := svc.parseResult(outer)

	if result.ParseError != nil {
		t.Fatalf("预期无解析错误，实际: %v", result.ParseError)
	}
	if result.Review == nil || len(result.Review.Issues) != 1 {
		t.Fatal("应有 1 个 issue")
	}
	issue := result.Review.Issues[0]
	if issue.File != "src/App.java" {
		t.Errorf("File = %q, want %q", issue.File, "src/App.java")
	}
	if issue.Line != 42 {
		t.Errorf("Line = %d, want %d", issue.Line, 42)
	}
	if !strings.Contains(issue.Message, "SQL注入") {
		t.Errorf("Message 应包含 title，实际: %q", issue.Message)
	}
	if !strings.Contains(issue.Message, "拼接SQL存在注入风险") {
		t.Errorf("Message 应包含 detail，实际: %q", issue.Message)
	}
}
