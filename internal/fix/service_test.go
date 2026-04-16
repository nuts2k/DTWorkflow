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

// --- mockRefClient ---

type mockRefClient struct {
	getBranch func(ctx context.Context, owner, repo, branch string) (*gitea.Branch, *gitea.Response, error)
	getTag    func(ctx context.Context, owner, repo, tag string) (*gitea.Tag, *gitea.Response, error)
}

func (m *mockRefClient) GetBranch(ctx context.Context, owner, repo, branch string) (*gitea.Branch, *gitea.Response, error) {
	if m.getBranch != nil {
		return m.getBranch(ctx, owner, repo, branch)
	}
	return &gitea.Branch{Name: branch}, nil, nil
}

func (m *mockRefClient) GetTag(ctx context.Context, owner, repo, tag string) (*gitea.Tag, *gitea.Response, error) {
	if m.getTag != nil {
		return m.getTag(ctx, owner, repo, tag)
	}
	return &gitea.Tag{Name: tag}, nil, nil
}

func notFoundErr() error {
	return &gitea.ErrorResponse{StatusCode: 404, Message: "not found"}
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
		TaskType:     model.TaskTypeAnalyzeIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
		IssueTitle:   "test bug",
		IssueRef:     "main",
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

// stubPRClient mock PRClient for fix tests
type stubPRClient struct {
	createResult *gitea.PullRequest
	createErr    error
	createCalls  int
	getRepoResult *gitea.Repository
	getRepoErr    error
}

func (s *stubPRClient) CreatePullRequest(_ context.Context, _, _ string, _ gitea.CreatePullRequestOption) (*gitea.PullRequest, *gitea.Response, error) {
	s.createCalls++
	return s.createResult, nil, s.createErr
}

func (s *stubPRClient) GetRepo(_ context.Context, _, _ string) (*gitea.Repository, *gitea.Response, error) {
	if s.getRepoErr != nil {
		return nil, nil, s.getRepoErr
	}
	if s.getRepoResult != nil {
		return s.getRepoResult, nil, nil
	}
	return &gitea.Repository{DefaultBranch: "main"}, nil, nil
}

func TestExecuteFix_InfoInsufficientPreCheck(t *testing.T) {
	analysisRec := &model.TaskRecord{
		ID:     "a-1",
		Result: `{"type":"result","result":"{\"info_sufficient\":false,\"missing_info\":[\"堆栈\"]}"}`,
	}
	createCommentCount := 0
	issueClient := &mockIssueClient{
		getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
			return &gitea.Issue{Number: 15, State: "open", Ref: "main", Title: "t"}, nil, nil
		},
		listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
			return nil, nil, nil
		},
		createComment: func(_ context.Context, _, _ string, _ int64, _ gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
			createCommentCount++
			return &gitea.Comment{ID: 1}, nil, nil
		},
	}
	pool := defaultPool() // 不应被调用
	s := NewService(issueClient, pool,
		WithFixStaleChecker(&stubFixStaleChecker{record: analysisRec}),
	)

	payload := model.TaskPayload{
		TaskType: model.TaskTypeFixIssue, RepoOwner: "o", RepoName: "r",
		RepoFullName: "o/r", IssueNumber: 15,
	}
	_, err := s.Execute(context.Background(), payload)
	if !errors.Is(err, ErrInfoInsufficient) {
		t.Errorf("应返回 ErrInfoInsufficient, got %v", err)
	}
	if createCommentCount == 0 {
		t.Error("应发出 Issue 评论提醒用户补充信息")
	}
	if pool.calls != 0 {
		t.Errorf("容器池不应被调用，实际调用 %d 次", pool.calls)
	}
}

func TestExecuteFix_SuccessCreatesPR(t *testing.T) {
	createCommentCount := 0
	issueClient := &mockIssueClient{
		getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
			return &gitea.Issue{Number: 15, State: "open", Ref: "main", Title: "t"}, nil, nil
		},
		listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
			return nil, nil, nil
		},
		createComment: func(_ context.Context, _, _ string, _ int64, _ gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
			createCommentCount++
			return &gitea.Comment{ID: 1}, nil, nil
		},
	}
	fixEnvelope := `{"type":"result","duration_ms":1000,"total_cost_usd":0.01,"result":"{\"success\":true,\"info_sufficient\":true,\"branch_name\":\"auto-fix/issue-15\",\"commit_sha\":\"abc\",\"modified_files\":[\"a.go\"],\"test_results\":{\"passed\":5,\"failed\":0,\"skipped\":0,\"all_passed\":true},\"analysis\":\"x\",\"fix_approach\":\"y\"}"}`
	pool := &mockFixPoolRunner{
		result: &worker.ExecutionResult{ExitCode: 0, Output: fixEnvelope},
	}
	prClient := &stubPRClient{
		createResult: &gitea.PullRequest{Number: 42, HTMLURL: "https://g/o/r/pulls/42"},
	}
	svc := NewService(issueClient, pool,
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, branch string) (*gitea.Branch, *gitea.Response, error) {
				if branch == "main" {
					return &gitea.Branch{Name: branch}, nil, nil
				}
				return nil, nil, notFoundErr()
			},
		}),
		WithPRClient(prClient),
	)
	result, err := svc.Execute(context.Background(),
		model.TaskPayload{TaskType: model.TaskTypeFixIssue, RepoOwner: "o", RepoName: "r",
			RepoFullName: "o/r", IssueNumber: 15, IssueRef: "main"})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if result == nil || result.Fix == nil || !result.Fix.Success {
		t.Fatalf("Fix 结果不正确: %+v", result)
	}
	if result.PRNumber != 42 || result.PRURL == "" {
		t.Errorf("PRNumber=%d PRURL=%q, 期望 42 + 非空", result.PRNumber, result.PRURL)
	}
	if prClient.createCalls != 1 {
		t.Errorf("CreatePullRequest 应调用 1 次, 实际 %d", prClient.createCalls)
	}
	if createCommentCount != 1 {
		t.Errorf("应发出 1 条 Issue 评论, 实际 %d", createCommentCount)
	}
}

func TestExecuteFix_PushSuccessButPRCreateFailed(t *testing.T) {
	createCommentCount := 0
	issueClient := &mockIssueClient{
		getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
			return &gitea.Issue{Number: 15, State: "open", Ref: "main", Title: "t"}, nil, nil
		},
		listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
			return nil, nil, nil
		},
		createComment: func(_ context.Context, _, _ string, _ int64, _ gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
			createCommentCount++
			return &gitea.Comment{ID: 1}, nil, nil
		},
	}
	fixEnvelope := `{"type":"result","result":"{\"success\":true,\"info_sufficient\":true,\"branch_name\":\"auto-fix/issue-15\",\"commit_sha\":\"abc\",\"modified_files\":[\"a.go\"],\"test_results\":{\"all_passed\":true}}"}`
	pool := &mockFixPoolRunner{result: &worker.ExecutionResult{Output: fixEnvelope}}
	prClient := &stubPRClient{createErr: fmt.Errorf("gitea 500")}

	svc := NewService(issueClient, pool,
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, branch string) (*gitea.Branch, *gitea.Response, error) {
				if branch == "main" {
					return &gitea.Branch{Name: branch}, nil, nil
				}
				return nil, nil, notFoundErr()
			},
		}),
		WithPRClient(prClient),
	)

	_, err := svc.Execute(context.Background(),
		model.TaskPayload{TaskType: model.TaskTypeFixIssue, RepoOwner: "o", RepoName: "r",
			RepoFullName: "o/r", IssueNumber: 15, IssueRef: "main"})
	if err == nil {
		t.Fatal("PR 创建失败应返回可重试错误")
	}
	if createCommentCount == 0 {
		t.Error("应发出 push-成功-PR-失败 评论")
	}
}

func TestExecuteFix_ClaudeReturnsFailure(t *testing.T) {
	createCommentCount := 0
	issueClient := &mockIssueClient{
		getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
			return &gitea.Issue{Number: 15, State: "open", Ref: "main", Title: "t"}, nil, nil
		},
		listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
			return nil, nil, nil
		},
		createComment: func(_ context.Context, _, _ string, _ int64, _ gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
			createCommentCount++
			return &gitea.Comment{ID: 1}, nil, nil
		},
	}
	fixEnvelope := `{"type":"result","result":"{\"success\":false,\"info_sufficient\":true,\"failure_reason\":\"测试未通过\",\"analysis\":\"x\",\"test_results\":{\"passed\":5,\"failed\":3,\"all_passed\":false}}"}`
	pool := &mockFixPoolRunner{result: &worker.ExecutionResult{Output: fixEnvelope}}

	svc := NewService(issueClient, pool,
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, branch string) (*gitea.Branch, *gitea.Response, error) {
				if branch == "main" {
					return &gitea.Branch{Name: branch}, nil, nil
				}
				return nil, nil, notFoundErr()
			},
		}),
	)

	_, err := svc.Execute(context.Background(),
		model.TaskPayload{TaskType: model.TaskTypeFixIssue, RepoOwner: "o", RepoName: "r",
			RepoFullName: "o/r", IssueNumber: 15, IssueRef: "main"})
	if !errors.Is(err, ErrFixFailed) {
		t.Errorf("应返回 ErrFixFailed, got %v", err)
	}
	if createCommentCount == 0 {
		t.Error("应发出失败报告评论")
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

func TestParseResult_ErrorDuringExecution(t *testing.T) {
	// 复现 type=error_during_execution 但 is_error=false 的场景
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	cliJSON := `{
		"type": "error_during_execution",
		"is_error": false,
		"total_cost_usd": 0,
		"duration_ms": 35351,
		"duration_api_ms": 34993,
		"num_turns": 1,
		"session_id": "ea9734bb-78bc-4596-a3a2-420a5ea756f7"
	}`

	result := svc.parseResult(cliJSON)
	if result.ParseError == nil {
		t.Fatal("error_during_execution 响应应返回 ParseError")
	}
	if result.CLIMeta == nil {
		t.Fatal("CLIMeta 应被填充")
	}
	if !result.CLIMeta.IsError {
		t.Error("CLIMeta.IsError 应为 true（type 不是 result）")
	}
	if result.Analysis != nil {
		t.Error("Analysis 应为 nil")
	}
}

func TestParseResult_TotalCostUSD(t *testing.T) {
	// 验证新版 CLI 的 total_cost_usd 字段能正确映射
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	cliJSON := `{
		"type": "result",
		"subtype": "success",
		"is_error": false,
		"total_cost_usd": 0.05,
		"duration_ms": 1000,
		"num_turns": 1,
		"result": "{\"info_sufficient\":true,\"root_cause\":{\"file\":\"a.go\",\"line\":1,\"description\":\"test\"},\"analysis\":\"ok\",\"fix_suggestion\":\"fix\",\"confidence\":\"high\",\"related_files\":[]}",
		"session_id": "sess-cost"
	}`

	result := svc.parseResult(cliJSON)
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
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	cliJSON := `{
		"type": "success",
		"is_error": false,
		"total_cost_usd": 0.03,
		"duration_ms": 12000,
		"num_turns": 1,
		"result": "{\"info_sufficient\":true,\"root_cause\":{\"file\":\"a.go\",\"line\":1,\"description\":\"test\"},\"analysis\":\"ok\",\"fix_suggestion\":\"fix\",\"confidence\":\"high\",\"related_files\":[]}",
		"session_id": "stream-sess"
	}`

	result := svc.parseResult(cliJSON)
	if result.ParseError != nil {
		t.Fatalf("流式 success 不应产生解析错误，实际: %v", result.ParseError)
	}
	if result.CLIMeta == nil || result.CLIMeta.IsError {
		t.Fatal("CLIMeta.IsError 应为 false")
	}
	if result.Analysis == nil {
		t.Fatal("Analysis 应被成功解析")
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

// --- ref 校验测试用例 ---

func TestExecute_MissingIssueRef(t *testing.T) {
	var commentBody string
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return openIssue(10), nil, nil
			},
			createComment: func(_ context.Context, _, _ string, _ int64, opts gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
				commentBody = opts.Body
				return &gitea.Comment{ID: 1}, nil, nil
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{}),
	)

	payload := fixPayload()
	payload.IssueRef = "" // 空 ref

	_, err := svc.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("预期返回错误")
	}
	if !errors.Is(err, ErrMissingIssueRef) {
		t.Errorf("预期 ErrMissingIssueRef，实际: %v", err)
	}
	if !strings.Contains(commentBody, "未设置关联分支") {
		t.Errorf("评论应包含提醒文案，实际: %q", commentBody)
	}
}

func TestExecute_InvalidIssueRef(t *testing.T) {
	var commentBody string
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return openIssue(10), nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
			createComment: func(_ context.Context, _, _ string, _ int64, opts gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
				commentBody = opts.Body
				return &gitea.Comment{ID: 1}, nil, nil
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, _ string) (*gitea.Branch, *gitea.Response, error) {
				return nil, nil, notFoundErr()
			},
			getTag: func(_ context.Context, _, _, _ string) (*gitea.Tag, *gitea.Response, error) {
				return nil, nil, notFoundErr()
			},
		}),
	)

	payload := fixPayload()
	payload.IssueRef = "nonexistent-branch"

	_, err := svc.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("预期返回错误")
	}
	if !errors.Is(err, ErrInvalidIssueRef) {
		t.Errorf("预期 ErrInvalidIssueRef，实际: %v", err)
	}
	if !strings.Contains(commentBody, "nonexistent-branch") {
		t.Errorf("评论应包含 ref 名称，实际: %q", commentBody)
	}
}

func TestExecute_UsesLatestIssueRefFromGitea(t *testing.T) {
	pool := defaultPool()
	issue := openIssue(10)
	issue.Ref = "feature/latest"

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
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, branch string) (*gitea.Branch, *gitea.Response, error) {
				if branch != "feature/latest" {
					t.Fatalf("validateRef 使用了错误 ref: %q", branch)
				}
				return &gitea.Branch{Name: branch}, nil, nil
			},
		}),
	)

	payload := fixPayload()
	payload.IssueRef = "stale-ref"

	result, err := svc.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("Execute 返回错误: %v", err)
	}
	if result == nil || result.IssueContext == nil {
		t.Fatal("result 和 IssueContext 不应为 nil")
	}
	if result.IssueContext.Ref != "feature/latest" {
		t.Fatalf("IssueContext.Ref = %q, want %q", result.IssueContext.Ref, "feature/latest")
	}
	if !strings.Contains(string(pool.lastStdin), "当前代码基于 ref：feature/latest") {
		t.Fatalf("prompt 未包含最新 ref，实际: %s", string(pool.lastStdin))
	}
}

func TestExecute_MissingIssueRefCommentFailure_ReturnsRetryableError(t *testing.T) {
	commentErr := errors.New("Gitea API 500")
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				issue := openIssue(10)
				issue.Ref = ""
				return issue, nil, nil
			},
			createComment: func(_ context.Context, _, _ string, _ int64, _ gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
				return nil, nil, commentErr
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{}),
	)

	payload := fixPayload()
	payload.IssueRef = ""

	_, err := svc.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("预期返回错误")
	}
	if errors.Is(err, ErrMissingIssueRef) {
		t.Fatalf("评论回写失败时不应返回 ErrMissingIssueRef，实际: %v", err)
	}
	if !errors.Is(err, commentErr) {
		t.Fatalf("错误应包含评论回写失败原因，实际: %v", err)
	}
}

func TestExecute_RefValidAsBranch(t *testing.T) {
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return openIssue(10), nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, _ string) (*gitea.Branch, *gitea.Response, error) {
				return &gitea.Branch{Name: "feature/ok"}, nil, nil
			},
		}),
	)

	payload := fixPayload()
	payload.IssueRef = "feature/ok"

	result, err := svc.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("有效分支不应报错，实际: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
}

func TestExecute_RefValidAsTag(t *testing.T) {
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return openIssue(10), nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, _ string) (*gitea.Branch, *gitea.Response, error) {
				return nil, nil, notFoundErr() // 分支不存在
			},
			getTag: func(_ context.Context, _, _, _ string) (*gitea.Tag, *gitea.Response, error) {
				return &gitea.Tag{Name: "v1.0.0"}, nil, nil // 但 tag 存在
			},
		}),
	)

	payload := fixPayload()
	payload.IssueRef = "v1.0.0"

	result, err := svc.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("有效 tag 不应报错，实际: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
}

func TestExecute_RefCommentWritebackFailure_StillReturnsError(t *testing.T) {
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return openIssue(10), nil, nil
			},
			createComment: func(_ context.Context, _, _ string, _ int64, _ gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
				return nil, nil, fmt.Errorf("Gitea API 500")
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{}),
	)

	payload := fixPayload()
	payload.IssueRef = ""

	_, err := svc.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("预期返回错误")
	}
	if errors.Is(err, ErrMissingIssueRef) {
		t.Errorf("评论回写失败时不应返回 ErrMissingIssueRef，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "回写 ref 缺失提示失败") {
		t.Errorf("错误应包含评论回写失败信息，实际: %v", err)
	}
}

// stubFixStaleChecker 返回预设的 TaskRecord
type stubFixStaleChecker struct {
	record *model.TaskRecord
	err    error
}

func (s *stubFixStaleChecker) GetLatestAnalysisByIssue(_ context.Context, _ string, _ int64) (*model.TaskRecord, error) {
	return s.record, s.err
}

func TestCheckPreviousAnalysis_NoPreviousAnalysis(t *testing.T) {
	s := &Service{staleChecker: &stubFixStaleChecker{}, logger: slog.Default()}
	sufficient, err := s.checkPreviousAnalysis(context.Background(), "o/r", 15)
	if err != nil {
		t.Fatalf("无前序分析应返回 nil error, got %v", err)
	}
	if !sufficient {
		t.Error("无前序分析应视为充分（不阻拦，让 Claude 隐式分析）")
	}
}

func TestCheckPreviousAnalysis_InfoInsufficient(t *testing.T) {
	rawResult := `{"type":"result","result":"{\"info_sufficient\":false,\"missing_info\":[\"缺少堆栈\"],\"analysis\":\"x\",\"confidence\":\"low\"}"}`
	rec := &model.TaskRecord{
		ID:       "a-1",
		TaskType: model.TaskTypeAnalyzeIssue,
		Result:   rawResult,
	}
	s := &Service{staleChecker: &stubFixStaleChecker{record: rec}, logger: slog.Default()}
	sufficient, err := s.checkPreviousAnalysis(context.Background(), "o/r", 15)
	if err != nil {
		t.Fatalf("error 应为 nil, got %v", err)
	}
	if sufficient {
		t.Error("info_sufficient=false 应视为不充分（阻断）")
	}
}

func TestCheckPreviousAnalysis_InfoSufficient(t *testing.T) {
	rawResult := `{"type":"result","result":"{\"info_sufficient\":true,\"analysis\":\"x\",\"confidence\":\"high\"}"}`
	rec := &model.TaskRecord{
		ID:       "a-1",
		TaskType: model.TaskTypeAnalyzeIssue,
		Result:   rawResult,
	}
	s := &Service{staleChecker: &stubFixStaleChecker{record: rec}, logger: slog.Default()}
	sufficient, err := s.checkPreviousAnalysis(context.Background(), "o/r", 15)
	if err != nil || !sufficient {
		t.Errorf("info_sufficient=true 时应放行: sufficient=%v err=%v", sufficient, err)
	}
}

func TestCheckPreviousAnalysis_ParseError_FailOpen(t *testing.T) {
	rec := &model.TaskRecord{
		ID:       "a-1",
		TaskType: model.TaskTypeAnalyzeIssue,
		Result:   `不是 JSON`,
	}
	s := &Service{staleChecker: &stubFixStaleChecker{record: rec}, logger: slog.Default()}
	sufficient, err := s.checkPreviousAnalysis(context.Background(), "o/r", 15)
	if err != nil {
		t.Fatalf("解析失败应 fail-open, got err %v", err)
	}
	if !sufficient {
		t.Error("解析失败应放行（fail-open），不误阻断")
	}
}

func TestCheckPreviousAnalysis_NoStaleCheckerInjected(t *testing.T) {
	s := &Service{staleChecker: nil, logger: slog.Default()}
	sufficient, err := s.checkPreviousAnalysis(context.Background(), "o/r", 15)
	if err != nil || !sufficient {
		t.Errorf("未注入 staleChecker 应放行: sufficient=%v err=%v", sufficient, err)
	}
}

func TestService_ParseFixResult_Success(t *testing.T) {
	envelope := `{"type":"result","subtype":"success","duration_ms":12000,"total_cost_usd":0.05,"is_error":false,"num_turns":8,"result":"{\"success\":true,\"info_sufficient\":true,\"branch_name\":\"auto-fix/issue-15\",\"commit_sha\":\"abc123\",\"modified_files\":[\"a.java\"],\"test_results\":{\"passed\":5,\"failed\":0,\"skipped\":0,\"all_passed\":true},\"analysis\":\"x\",\"fix_approach\":\"y\"}","session_id":"s1"}`
	s := &Service{}
	result := s.parseFixResult(envelope)
	if result.ParseError != nil {
		t.Fatalf("解析不应失败: %v", result.ParseError)
	}
	if result.Fix == nil {
		t.Fatal("Fix 不应为 nil")
	}
	if !result.Fix.Success || result.Fix.BranchName != "auto-fix/issue-15" {
		t.Errorf("Fix 字段不正确: %+v", result.Fix)
	}
	if result.CLIMeta == nil || result.CLIMeta.CostUSD != 0.05 {
		t.Errorf("CLIMeta 不正确: %+v", result.CLIMeta)
	}
}

func TestService_ParseFixResult_SuccessInvariantViolation(t *testing.T) {
	// success=true 但 branch_name 为空 → 视为解析异常
	envelope := `{"type":"result","result":"{\"success\":true,\"info_sufficient\":true,\"analysis\":\"x\"}"}`
	s := &Service{}
	result := s.parseFixResult(envelope)
	if result.ParseError == nil {
		t.Error("success=true 时 branch_name 为空应视为解析异常")
	}
}

func TestService_ParseFixResult_InfoInsufficient(t *testing.T) {
	envelope := `{"type":"result","result":"{\"success\":false,\"info_sufficient\":false,\"missing_info\":[\"需要堆栈\"]}"}`
	s := &Service{}
	result := s.parseFixResult(envelope)
	if result.ParseError != nil {
		t.Fatalf("解析不应失败: %v", result.ParseError)
	}
	if result.Fix == nil || result.Fix.InfoSufficient {
		t.Errorf("InfoSufficient 应为 false, got Fix=%+v", result.Fix)
	}
}

func TestService_ParseFixResult_CLIError(t *testing.T) {
	envelope := `{"type":"error","subtype":"context_limit","is_error":true}`
	s := &Service{}
	result := s.parseFixResult(envelope)
	if result.ParseError == nil {
		t.Error("CLI 错误时应有 ParseError")
	}
}

func TestValidateRef_ReturnsRefKind_Branch(t *testing.T) {
	rc := &mockRefClient{
		getBranch: func(_ context.Context, _, _, branch string) (*gitea.Branch, *gitea.Response, error) {
			if branch == "feature/x" {
				return &gitea.Branch{Name: branch}, nil, nil
			}
			return nil, nil, notFoundErr()
		},
	}
	s := &Service{refClient: rc}
	kind, err := s.validateRef(context.Background(), "o", "r", "feature/x")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if kind != RefKindBranch {
		t.Errorf("kind = %v, want RefKindBranch", kind)
	}
}

func TestValidateRef_ReturnsRefKind_Tag(t *testing.T) {
	rc := &mockRefClient{
		getBranch: func(_ context.Context, _, _, _ string) (*gitea.Branch, *gitea.Response, error) {
			return nil, nil, notFoundErr()
		},
		getTag: func(_ context.Context, _, _, tag string) (*gitea.Tag, *gitea.Response, error) {
			if tag == "v1.0.0" {
				return &gitea.Tag{Name: tag}, nil, nil
			}
			return nil, nil, notFoundErr()
		},
	}
	s := &Service{refClient: rc}
	kind, err := s.validateRef(context.Background(), "o", "r", "v1.0.0")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if kind != RefKindTag {
		t.Errorf("kind = %v, want RefKindTag", kind)
	}
}

func TestValidateRef_StripsRefsHeadsPrefix(t *testing.T) {
	rc := &mockRefClient{
		getBranch: func(_ context.Context, _, _, branch string) (*gitea.Branch, *gitea.Response, error) {
			if branch == "main" {
				return &gitea.Branch{Name: branch}, nil, nil
			}
			return nil, nil, notFoundErr()
		},
	}
	s := &Service{refClient: rc}
	kind, err := s.validateRef(context.Background(), "o", "r", "refs/heads/main")
	if err != nil || kind != RefKindBranch {
		t.Fatalf("refs/heads/main 应解析为分支，got kind=%v err=%v", kind, err)
	}
}

func TestValidateRef_NotFound(t *testing.T) {
	rc := &mockRefClient{
		getBranch: func(_ context.Context, _, _, _ string) (*gitea.Branch, *gitea.Response, error) {
			return nil, nil, notFoundErr()
		},
		getTag: func(_ context.Context, _, _, _ string) (*gitea.Tag, *gitea.Response, error) {
			return nil, nil, notFoundErr()
		},
	}
	s := &Service{refClient: rc}
	_, err := s.validateRef(context.Background(), "o", "r", "does-not-exist")
	if !errors.Is(err, ErrInvalidIssueRef) {
		t.Errorf("expected ErrInvalidIssueRef, got %v", err)
	}
}

func TestStripRefPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"refs/heads/main", "main"},
		{"refs/heads/feature/user-auth", "feature/user-auth"},
		{"refs/tags/v1.0.0", "v1.0.0"},
		{"refs/tags/release/2.0", "release/2.0"},
		{"main", "main"},
		{"feature/dev", "feature/dev"},
		{"", ""},
	}
	for _, tt := range tests {
		got := stripRefPrefix(tt.input)
		if got != tt.want {
			t.Errorf("stripRefPrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExecute_RefsHeadsPrefix_ValidatesCorrectly(t *testing.T) {
	var queriedBranch string
	pool := defaultPool()
	issue := openIssue(10)
	issue.Ref = "refs/heads/main"

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
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, branch string) (*gitea.Branch, *gitea.Response, error) {
				queriedBranch = branch
				return &gitea.Branch{Name: branch}, nil, nil
			},
		}),
	)

	payload := fixPayload()
	_, _ = svc.Execute(context.Background(), payload)

	if queriedBranch != "main" {
		t.Errorf("validateRef 应剥离 refs/heads/ 前缀，实际查询分支名: %q", queriedBranch)
	}
}

func TestExecute_RefsTagsPrefix_ValidatesCorrectly(t *testing.T) {
	var queriedTag string
	issue := openIssue(10)
	issue.Ref = "refs/tags/v1.0.0"

	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return issue, nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, _ string) (*gitea.Branch, *gitea.Response, error) {
				return nil, nil, notFoundErr()
			},
			getTag: func(_ context.Context, _, _, tag string) (*gitea.Tag, *gitea.Response, error) {
				queriedTag = tag
				return &gitea.Tag{Name: tag}, nil, nil
			},
		}),
	)

	payload := fixPayload()
	_, _ = svc.Execute(context.Background(), payload)

	if queriedTag != "v1.0.0" {
		t.Errorf("validateRef 应剥离 refs/tags/ 前缀，实际查询 tag 名: %q", queriedTag)
	}
}
