package code

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

type mockCodePool struct {
	result *worker.ExecutionResult
}

func (m *mockCodePool) RunWithCommandAndStdin(_ context.Context, _ model.TaskPayload,
	_ []string, _ []byte) (*worker.ExecutionResult, error) {
	return m.result, nil
}

type mockCodePRClient struct {
	existing    []*PullRequest
	listErr     error
	createErr   error
	listCalls   int
	createCalls int
	lastHead    string
}

func (m *mockCodePRClient) CreatePullRequest(_ context.Context, _, _ string, opt CreatePullRequestOption) (*PullRequest, error) {
	m.createCalls++
	m.lastHead = opt.Head
	if m.createErr != nil {
		return nil, m.createErr
	}
	return &PullRequest{Number: 101, HTMLURL: "https://gitea.local/pulls/101"}, nil
}

func (m *mockCodePRClient) ListRepoPullRequests(_ context.Context, _, _ string, opts ListPullRequestsOptions) ([]*PullRequest, error) {
	m.listCalls++
	m.lastHead = opts.Head
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.existing, nil
}

func TestExecute_ReusesExistingPRAutoBranch(t *testing.T) {
	pool := &mockCodePool{result: &worker.ExecutionResult{
		ExitCode: 0,
		Output:   `{"success":true,"info_sufficient":true,"branch_name":"auto-code/spec","commit_sha":"abc123","modified_files":[],"test_results":{"passed":1,"failed":0,"skipped":0,"all_passed":true},"failure_category":"none"}`,
	}}
	pr := &mockCodePRClient{
		existing: []*PullRequest{{Number: 42, HTMLURL: "https://gitea.local/pulls/42"}},
	}
	svc := NewService(pool, pr, nil, slog.Default())

	result, err := svc.Execute(context.Background(), model.TaskPayload{
		TaskType:     model.TaskTypeCodeFromDoc,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		DocPath:      "docs/spec.md",
		DocSlug:      "spec",
		BaseRef:      "main",
	})
	if err != nil {
		t.Fatalf("Execute 返回错误: %v", err)
	}
	if result.PRNumber != 42 {
		t.Fatalf("PRNumber = %d, want 42", result.PRNumber)
	}
	if pr.listCalls != 1 {
		t.Fatalf("ListRepoPullRequests 应调用 1 次，实际 %d", pr.listCalls)
	}
	if pr.lastHead != "auto-code/spec" {
		t.Fatalf("ListRepoPullRequests Head = %q, want auto-code/spec", pr.lastHead)
	}
	if pr.createCalls != 0 {
		t.Fatalf("命中既有 PR 后不应创建新 PR，createCalls=%d", pr.createCalls)
	}
}

func TestExecute_RejectsMismatchedBranchName(t *testing.T) {
	pool := &mockCodePool{result: &worker.ExecutionResult{
		ExitCode: 0,
		Output:   `{"success":true,"info_sufficient":true,"branch_name":"auto-code/other","commit_sha":"abc123","modified_files":[],"test_results":{"passed":1,"failed":0,"skipped":0,"all_passed":true},"failure_category":"none"}`,
	}}
	pr := &mockCodePRClient{}
	svc := NewService(pool, pr, nil, slog.Default())

	_, err := svc.Execute(context.Background(), model.TaskPayload{
		TaskType:     model.TaskTypeCodeFromDoc,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		DocPath:      "docs/spec.md",
		DocSlug:      "spec",
		BaseRef:      "main",
	})
	if !errors.Is(err, ErrCodeFromDocParseFailure) {
		t.Fatalf("err = %v, want ErrCodeFromDocParseFailure", err)
	}
	if pr.createCalls != 0 || pr.listCalls != 0 {
		t.Fatalf("分支不一致时不应触发 PR 操作，list=%d create=%d", pr.listCalls, pr.createCalls)
	}
}

func TestExecute_ReturnsErrorWhenCreatePRFails(t *testing.T) {
	pool := &mockCodePool{result: &worker.ExecutionResult{
		ExitCode: 0,
		Output:   `{"success":true,"info_sufficient":true,"branch_name":"auto-code/spec","commit_sha":"abc123","modified_files":[],"test_results":{"passed":1,"failed":0,"skipped":0,"all_passed":true},"failure_category":"none"}`,
	}}
	pr := &mockCodePRClient{createErr: errors.New("gitea unavailable")}
	svc := NewService(pool, pr, nil, slog.Default())

	result, err := svc.Execute(context.Background(), model.TaskPayload{
		TaskType:     model.TaskTypeCodeFromDoc,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		DocPath:      "docs/spec.md",
		DocSlug:      "spec",
		BaseRef:      "main",
	})
	if err == nil {
		t.Fatal("CreatePullRequest 失败时 Execute 应返回错误")
	}
	if result == nil {
		t.Fatal("CreatePullRequest 失败时应保留执行结果")
	}
	if result.PRNumber != 0 || result.PRURL != "" {
		t.Fatalf("PR 创建失败不应填充 PR 信息: number=%d url=%q", result.PRNumber, result.PRURL)
	}
	if pr.createCalls != 1 {
		t.Fatalf("CreatePullRequest 调用次数 = %d, want 1", pr.createCalls)
	}
}

func TestExecute_ReturnsErrorWhenListExistingPRFails(t *testing.T) {
	pool := &mockCodePool{result: &worker.ExecutionResult{
		ExitCode: 0,
		Output:   `{"success":true,"info_sufficient":true,"branch_name":"auto-code/spec","commit_sha":"abc123","modified_files":[],"test_results":{"passed":1,"failed":0,"skipped":0,"all_passed":true},"failure_category":"none"}`,
	}}
	pr := &mockCodePRClient{listErr: errors.New("gitea list unavailable")}
	svc := NewService(pool, pr, nil, slog.Default())

	_, err := svc.Execute(context.Background(), model.TaskPayload{
		TaskType:     model.TaskTypeCodeFromDoc,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		DocPath:      "docs/spec.md",
		DocSlug:      "spec",
		BaseRef:      "main",
	})
	if err == nil {
		t.Fatal("查询既有 PR 失败时应返回错误")
	}
	if pr.createCalls != 0 {
		t.Fatalf("查询既有 PR 失败时不应继续创建 PR，createCalls=%d", pr.createCalls)
	}
}

func TestBuildPRTitle_TruncatesLongDocPath(t *testing.T) {
	title := buildPRTitle("docs/" + strings.Repeat("很长", 200) + ".md")
	if len([]rune(title)) > 240 {
		t.Fatalf("title 长度 = %d，期望 <= 240", len([]rune(title)))
	}
	if !strings.HasSuffix(title, "...") {
		t.Fatalf("超长 title 应以省略号结尾，实际 %q", title)
	}
}

func TestDocSlug_IncludesPathHash(t *testing.T) {
	got := DocSlug("docs/plans/user-auth-design.md")
	if !strings.HasPrefix(got, "user-auth-design-") {
		t.Fatalf("DocSlug 前缀 = %q, want user-auth-design-", got)
	}
	if len(got) != len("user-auth-design-")+8 {
		t.Fatalf("DocSlug 长度 = %d, want %d", len(got), len("user-auth-design-")+8)
	}
	if DocSlug("docs/a/spec.md") == DocSlug("docs/b/spec.md") {
		t.Fatal("不同路径的同名文档不应生成相同 slug")
	}
}

func TestDocSlug_NonASCIIUsesHashFallback(t *testing.T) {
	got := DocSlug("docs/登录设计.md")
	if !strings.HasPrefix(got, "doc-") {
		t.Fatalf("非 ASCII 文档名应回落 doc- 前缀，实际 %q", got)
	}
	if got == DocSlug("docs/支付设计.md") {
		t.Fatal("不同中文文档不应生成相同 slug")
	}
}
