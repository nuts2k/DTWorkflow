package fix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// --- mock 实现 ---

type mockIssueClient struct {
	getIssue      func(ctx context.Context, owner, repo string, index int64) (*gitea.Issue, *gitea.Response, error)
	listComments  func(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error)
	createComment func(ctx context.Context, owner, repo string, index int64, opts gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error)
}

func (m *mockIssueClient) GetIssue(ctx context.Context, owner, repo string, index int64) (*gitea.Issue, *gitea.Response, error) {
	return m.getIssue(ctx, owner, repo, index)
}

func (m *mockIssueClient) ListIssueComments(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
	return m.listComments(ctx, owner, repo, index, opts)
}

func (m *mockIssueClient) CreateIssueComment(ctx context.Context, owner, repo string, index int64, opts gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
	if m.createComment != nil {
		return m.createComment(ctx, owner, repo, index, opts)
	}
	return &gitea.Comment{ID: 999}, nil, nil
}

type mockFixPoolRunner struct {
	result    *worker.ExecutionResult
	err       error
	calls     int
	lastCmd   []string
	lastStdin []byte
}

func (m *mockFixPoolRunner) RunWithCommandAndStdin(_ context.Context, _ model.TaskPayload, cmd []string, stdinData []byte) (*worker.ExecutionResult, error) {
	m.calls++
	m.lastCmd = cmd
	m.lastStdin = stdinData
	return m.result, m.err
}

func defaultPool() *mockFixPoolRunner {
	return &mockFixPoolRunner{
		result: &worker.ExecutionResult{ExitCode: 0, Output: "{}"},
	}
}

// --- 辅助函数 ---

func openIssue(num int64) *gitea.Issue {
	return &gitea.Issue{
		Number:   num,
		Title:    "test bug",
		Body:     "something is broken",
		State:    "open",
		Comments: 0,
		Labels:   []*gitea.Label{{ID: 1, Name: "auto-fix"}},
	}
}

func fixPayload() model.TaskPayload {
	return model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
		IssueTitle:   "test bug",
		DeliveryID:   "test-delivery",
	}
}

// --- 测试用例 ---

func TestNewService_NilGitea(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("预期 panic")
		}
	}()
	NewService(nil, &mockFixPoolRunner{})
}

func TestNewService_NilPool(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("预期 panic")
		}
	}()
	NewService(&mockIssueClient{}, nil)
}

func TestWithServiceLogger_NonNil(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{}, WithServiceLogger(logger))
	if svc == nil {
		t.Fatal("service should not be nil")
	}
}

func TestWithServiceLogger_Nil(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{}, WithServiceLogger(nil))
	if svc == nil {
		t.Fatal("service should not be nil")
	}
}

func TestExecute_InvalidIssueNumber(t *testing.T) {
	svc := NewService(&mockIssueClient{}, defaultPool())

	payload := fixPayload()
	payload.IssueNumber = 0

	_, err := svc.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("预期返回错误")
	}
}

func TestExecute_IssueNotOpen(t *testing.T) {
	closedIssue := openIssue(10)
	closedIssue.State = "closed"

	svc := NewService(&mockIssueClient{
		getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
			return closedIssue, nil, nil
		},
	}, defaultPool())

	_, err := svc.Execute(context.Background(), fixPayload())
	if err == nil {
		t.Fatal("预期返回错误")
	}
	if !errors.Is(err, ErrIssueNotOpen) {
		t.Errorf("预期 ErrIssueNotOpen，实际: %v", err)
	}
}

func TestExecute_GetIssueFailed(t *testing.T) {
	apiErr := errors.New("connection refused")
	svc := NewService(&mockIssueClient{
		getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
			return nil, nil, apiErr
		},
	}, defaultPool())

	_, err := svc.Execute(context.Background(), fixPayload())
	if err == nil {
		t.Fatal("预期返回错误")
	}
	if !errors.Is(err, apiErr) {
		t.Errorf("原始错误应被包装在返回错误中，实际: %v", err)
	}
}

func TestExecute_ListCommentsFailed(t *testing.T) {
	commentErr := errors.New("api error")
	svc := NewService(&mockIssueClient{
		getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
			return openIssue(10), nil, nil
		},
		listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
			return nil, nil, commentErr
		},
	}, defaultPool())

	_, err := svc.Execute(context.Background(), fixPayload())
	if err == nil {
		t.Fatal("预期返回错误")
	}
	if !errors.Is(err, commentErr) {
		t.Errorf("原始错误应被包装在返回错误中，实际: %v", err)
	}
}

func TestExecute_Success(t *testing.T) {
	issue := openIssue(10)
	issue.Comments = 2
	comments := []*gitea.Comment{
		{ID: 1, Body: "I can reproduce this", User: &gitea.User{Login: "user1"}},
		{ID: 2, Body: "Stack trace: ...", User: &gitea.User{Login: "user2"}},
	}

	svc := NewService(&mockIssueClient{
		getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
			return issue, nil, nil
		},
		listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
			return comments, nil, nil
		},
	}, defaultPool())

	result, err := svc.Execute(context.Background(), fixPayload())
	if err != nil {
		t.Fatalf("预期无错误，实际: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
	if result.IssueContext == nil {
		t.Fatal("IssueContext 不应为 nil")
	}
	if result.IssueContext.Issue.Number != 10 {
		t.Errorf("Issue number = %d, want 10", result.IssueContext.Issue.Number)
	}
	if len(result.IssueContext.Comments) != 2 {
		t.Errorf("Comments count = %d, want 2", len(result.IssueContext.Comments))
	}
	if result.CLIMeta == nil {
		t.Error("CLIMeta 不应为 nil")
	}
	if result.RawOutput != "{}" {
		t.Errorf("RawOutput = %q, want %q", result.RawOutput, "{}")
	}
}

func TestExecute_CommentsTruncated(t *testing.T) {
	issue := openIssue(10)
	issue.Comments = 100 // 超过单页 50 条上限

	svc := NewService(&mockIssueClient{
		getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
			return issue, nil, nil
		},
		listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
			// 模拟只返回 50 条
			comments := make([]*gitea.Comment, 50)
			for i := range comments {
				comments[i] = &gitea.Comment{ID: int64(i + 1), User: &gitea.User{Login: "user"}}
			}
			return comments, nil, nil
		},
	}, defaultPool())

	result, err := svc.Execute(context.Background(), fixPayload())
	if err != nil {
		t.Fatalf("评论截断不应导致错误，实际: %v", err)
	}
	if len(result.IssueContext.Comments) != 50 {
		t.Errorf("Comments count = %d, want 50", len(result.IssueContext.Comments))
	}
}

func TestParseResult_Success(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	cliJSON := `{
		"type": "result",
		"subtype": "success",
		"cost_usd": 0.05,
		"duration_ms": 12345,
		"duration_api_ms": 10000,
		"is_error": false,
		"num_turns": 3,
		"result": "{\"info_sufficient\":true,\"root_cause\":{\"file\":\"main.go\",\"function\":\"handler\",\"start_line\":42,\"end_line\":55,\"description\":\"空指针引用\"},\"analysis\":\"详细分析\",\"fix_suggestion\":\"添加空值检查\",\"confidence\":\"high\",\"related_files\":[\"util.go\"]}",
		"session_id": "sess-123"
	}`

	result := svc.parseResult(cliJSON)
	if result.ParseError != nil {
		t.Fatalf("ParseError 应为 nil，实际: %v", result.ParseError)
	}
	if result.CLIMeta == nil {
		t.Fatal("CLIMeta 不应为 nil")
	}
	if result.CLIMeta.CostUSD != 0.05 {
		t.Errorf("CostUSD = %f, want 0.05", result.CLIMeta.CostUSD)
	}
	if result.CLIMeta.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want %q", result.CLIMeta.SessionID, "sess-123")
	}
	if result.Analysis == nil {
		t.Fatal("Analysis 不应为 nil")
	}
	if !result.Analysis.InfoSufficient {
		t.Error("InfoSufficient should be true")
	}
	if result.Analysis.RootCause == nil {
		t.Fatal("RootCause 不应为 nil")
	}
	if result.Analysis.RootCause.File != "main.go" {
		t.Errorf("RootCause.File = %q, want %q", result.Analysis.RootCause.File, "main.go")
	}
	if result.Analysis.Confidence != "high" {
		t.Errorf("Confidence = %q, want %q", result.Analysis.Confidence, "high")
	}
}

func TestParseResult_CLIError(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	cliJSON := `{
		"type": "result",
		"subtype": "error",
		"is_error": true,
		"cost_usd": 0.01,
		"duration_ms": 1000,
		"duration_api_ms": 800,
		"num_turns": 1,
		"result": "",
		"session_id": "sess-err"
	}`

	result := svc.parseResult(cliJSON)
	if result.ParseError == nil {
		t.Fatal("CLI 错误时 ParseError 应非 nil")
	}
	if result.Analysis != nil {
		t.Error("CLI 错误时 Analysis 应为 nil")
	}
	if result.CLIMeta == nil {
		t.Fatal("即使 CLI 错误，CLIMeta 也应被解析")
	}
	if !result.CLIMeta.IsError {
		t.Error("CLIMeta.IsError should be true")
	}
}

func TestParseResult_InnerJSONFail(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	cliJSON := `{
		"type": "result",
		"subtype": "success",
		"is_error": false,
		"cost_usd": 0.02,
		"duration_ms": 5000,
		"duration_api_ms": 4000,
		"num_turns": 2,
		"result": "not valid json at all",
		"session_id": "sess-bad"
	}`

	result := svc.parseResult(cliJSON)
	if result.ParseError == nil {
		t.Fatal("内层 JSON 解析失败时 ParseError 应非 nil")
	}
	if result.Analysis != nil {
		t.Error("解析失败时 Analysis 应为 nil")
	}
	if result.CLIMeta == nil {
		t.Fatal("外层解析成功时 CLIMeta 应非 nil")
	}
}

func TestParseResult_InfoInsufficient(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	cliJSON := `{
		"type": "result",
		"subtype": "success",
		"is_error": false,
		"cost_usd": 0.01,
		"duration_ms": 3000,
		"duration_api_ms": 2500,
		"num_turns": 1,
		"result": "{\"info_sufficient\":false,\"missing_info\":[\"缺少错误堆栈信息\",\"缺少复现步骤\"],\"analysis\":\"初步判断可能是配置问题\",\"confidence\":\"low\",\"related_files\":[]}",
		"session_id": "sess-info"
	}`

	result := svc.parseResult(cliJSON)
	if result.ParseError != nil {
		t.Fatalf("ParseError 应为 nil，实际: %v", result.ParseError)
	}
	if result.Analysis == nil {
		t.Fatal("Analysis 不应为 nil")
	}
	if result.Analysis.InfoSufficient {
		t.Error("InfoSufficient should be false")
	}
	if result.Analysis.RootCause != nil {
		t.Error("信息不足时 RootCause 应为 nil")
	}
	if len(result.Analysis.MissingInfo) != 2 {
		t.Errorf("MissingInfo 长度 = %d, want 2", len(result.Analysis.MissingInfo))
	}
}

func TestParseResult_OuterJSONFail(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	result := svc.parseResult("not json")
	if result.ParseError == nil {
		t.Fatal("外层 JSON 解析失败时 ParseError 应非 nil")
	}
	if result.CLIMeta != nil {
		t.Error("外层解析失败时 CLIMeta 应为 nil")
	}
}

func TestExecute_ContainerSuccess(t *testing.T) {
	issue := openIssue(10)
	cliJSON := `{
		"type":"result","subtype":"success","is_error":false,
		"cost_usd":0.03,"duration_ms":8000,"duration_api_ms":7000,
		"num_turns":2,"session_id":"sess-ok",
		"result":"{\"info_sufficient\":true,\"root_cause\":{\"file\":\"main.go\",\"description\":\"问题\"},\"analysis\":\"分析\",\"confidence\":\"medium\",\"related_files\":[]}"
	}`

	pool := &mockFixPoolRunner{
		result: &worker.ExecutionResult{ExitCode: 0, Output: cliJSON},
	}

	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return issue, nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
		},
		pool,
	)

	result, err := svc.Execute(context.Background(), fixPayload())
	if err != nil {
		t.Fatalf("预期无错误，实际: %v", err)
	}
	if pool.calls != 1 {
		t.Errorf("pool.RunWithCommandAndStdin 应被调用 1 次，实际 %d 次", pool.calls)
	}
	if result.RawOutput != cliJSON {
		t.Error("RawOutput 应等于 CLI 输出")
	}
	if result.Analysis == nil {
		t.Fatal("Analysis 不应为 nil")
	}
	if result.Analysis.RootCause == nil {
		t.Fatal("RootCause 不应为 nil")
	}
	if result.CLIMeta == nil {
		t.Fatal("CLIMeta 不应为 nil")
	}
	if len(pool.lastCmd) == 0 || pool.lastCmd[0] != "claude" {
		t.Errorf("expected command to start with 'claude', got %v", pool.lastCmd)
	}
	if len(pool.lastStdin) == 0 {
		t.Error("stdin (prompt) should not be empty")
	}
}

func TestExecute_ContainerError(t *testing.T) {
	issue := openIssue(10)
	containerErr := fmt.Errorf("container timeout")

	pool := &mockFixPoolRunner{
		result: &worker.ExecutionResult{ExitCode: 1, Output: "partial output"},
		err:    containerErr,
	}

	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return issue, nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
		},
		pool,
	)

	result, err := svc.Execute(context.Background(), fixPayload())
	if err == nil {
		t.Fatal("容器错误时应返回 error")
	}
	if !errors.Is(err, containerErr) {
		t.Errorf("应包装原始容器错误，实际: %v", err)
	}
	if result == nil {
		t.Fatal("即使容器失败，result 不应为 nil")
	}
	if result.RawOutput != "partial output" {
		t.Errorf("RawOutput = %q, want %q", result.RawOutput, "partial output")
	}
	if result.IssueContext == nil {
		t.Error("即使容器失败，IssueContext 应已采集")
	}
}

func TestExecute_ContainerNonZeroExitSkipsParseAndWriteback(t *testing.T) {
	issue := openIssue(10)
	createCalls := 0

	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return issue, nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
			createComment: func(_ context.Context, _, _ string, _ int64, _ gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
				createCalls++
				return &gitea.Comment{ID: 1}, nil, nil
			},
		},
		&mockFixPoolRunner{
			result: &worker.ExecutionResult{ExitCode: 17, Output: "invalid model"},
		},
	)

	result, err := svc.Execute(context.Background(), fixPayload())
	if err != nil {
		t.Fatalf("非零退出码应通过结果返回给上层处理，实际错误: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
	if result.ExitCode != 17 {
		t.Fatalf("ExitCode = %d, want 17", result.ExitCode)
	}
	if result.CLIMeta != nil {
		t.Fatal("非零退出时不应继续解析 CLI envelope")
	}
	if result.Analysis != nil {
		t.Fatal("非零退出时不应产生 Analysis")
	}
	if createCalls != 0 {
		t.Fatalf("非零退出时不应回写评论，实际调用 %d 次", createCalls)
	}
}

func TestExecute_WritebackSuccess(t *testing.T) {
	issue := openIssue(10)
	cliJSON := `{
		"type":"result","subtype":"success","is_error":false,
		"cost_usd":0.03,"duration_ms":8000,"duration_api_ms":7000,
		"num_turns":2,"session_id":"sess-ok",
		"result":"{\"info_sufficient\":true,\"root_cause\":{\"file\":\"main.go\",\"description\":\"问题\"},\"analysis\":\"分析\",\"confidence\":\"medium\",\"related_files\":[]}"
	}`

	var commentBody string
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return issue, nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
			createComment: func(_ context.Context, _, _ string, _ int64, opts gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
				commentBody = opts.Body
				return &gitea.Comment{ID: 100}, nil, nil
			},
		},
		&mockFixPoolRunner{
			result: &worker.ExecutionResult{ExitCode: 0, Output: cliJSON},
		},
	)

	result, err := svc.Execute(context.Background(), fixPayload())
	if err != nil {
		t.Fatalf("预期无错误，实际: %v", err)
	}
	if result.WritebackError != nil {
		t.Errorf("WritebackError 应为 nil，实际: %v", result.WritebackError)
	}
	if commentBody == "" {
		t.Fatal("CreateIssueComment 应被调用")
	}
	if !strings.Contains(commentBody, "## DTWorkflow Issue 分析报告") {
		t.Error("评论应包含分析报告标题")
	}
}

func TestExecute_WritebackFailure(t *testing.T) {
	issue := openIssue(10)
	cliJSON := `{
		"type":"result","subtype":"success","is_error":false,
		"cost_usd":0.03,"duration_ms":8000,"duration_api_ms":7000,
		"num_turns":2,"session_id":"sess-ok",
		"result":"{\"info_sufficient\":true,\"root_cause\":{\"file\":\"main.go\",\"description\":\"问题\"},\"analysis\":\"分析\",\"confidence\":\"medium\",\"related_files\":[]}"
	}`

	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return issue, nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
			createComment: func(_ context.Context, _, _ string, _ int64, _ gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
				return nil, nil, fmt.Errorf("Gitea API 500")
			},
		},
		&mockFixPoolRunner{
			result: &worker.ExecutionResult{ExitCode: 0, Output: cliJSON},
		},
	)

	result, err := svc.Execute(context.Background(), fixPayload())
	// 回写失败不影响 Execute 的返回 error
	if err != nil {
		t.Fatalf("回写失败不应导致 Execute 返回 error，实际: %v", err)
	}
	if result.WritebackError == nil {
		t.Fatal("WritebackError 应非 nil")
	}
}

func TestExecute_CLIErrorDoesNotWritebackFallbackComment(t *testing.T) {
	issue := openIssue(10)
	createCalls := 0
	cliJSON := `{
		"type":"result","subtype":"error","is_error":true,
		"cost_usd":0.01,"duration_ms":1000,"duration_api_ms":900,
		"num_turns":1,"session_id":"sess-rate-limit","result":""
	}`

	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return issue, nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
			createComment: func(_ context.Context, _, _ string, _ int64, _ gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
				createCalls++
				return &gitea.Comment{ID: 1}, nil, nil
			},
		},
		&mockFixPoolRunner{
			result: &worker.ExecutionResult{ExitCode: 0, Output: cliJSON},
		},
	)

	result, err := svc.Execute(context.Background(), fixPayload())
	if err != nil {
		t.Fatalf("预期无 transport error，实际: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
	if result.CLIMeta == nil || !result.CLIMeta.IsError {
		t.Fatal("应保留 Claude CLI 错误元数据")
	}
	if result.WritebackError != nil {
		t.Fatalf("CLI 错误未尝试回写时不应设置 WritebackError，实际: %v", result.WritebackError)
	}
	if createCalls != 0 {
		t.Fatalf("CLI 可重试错误不应发送兜底评论，实际调用 %d 次", createCalls)
	}
}

func TestFixResult_HasWritebackError(t *testing.T) {
	r := &FixResult{
		WritebackError: fmt.Errorf("test error"),
	}
	if r.WritebackError == nil {
		t.Fatal("WritebackError 应非 nil")
	}
}

func TestExecute_ContainerNilResult(t *testing.T) {
	issue := openIssue(10)
	containerErr := fmt.Errorf("docker daemon not running")

	pool := &mockFixPoolRunner{
		result: nil,
		err:    containerErr,
	}

	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return issue, nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
		},
		pool,
	)

	result, err := svc.Execute(context.Background(), fixPayload())
	if err == nil {
		t.Fatal("容器错误时应返回 error")
	}
	if result == nil {
		t.Fatal("即使容器失败，result 不应为 nil")
	}
	if result.RawOutput != "" {
		t.Errorf("nil result 时 RawOutput 应为空，实际: %q", result.RawOutput)
	}
}
