package code

import (
	"context"
	"errors"
	"log/slog"
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
	listCalls   int
	createCalls int
	lastHead    string
}

func (m *mockCodePRClient) CreatePullRequest(_ context.Context, _, _ string, opt CreatePullRequestOption) (*PullRequest, error) {
	m.createCalls++
	m.lastHead = opt.Head
	return &PullRequest{Number: 101, HTMLURL: "https://gitea.local/pulls/101"}, nil
}

func (m *mockCodePRClient) ListRepoPullRequests(_ context.Context, _, _ string, opts ListPullRequestsOptions) ([]*PullRequest, error) {
	m.listCalls++
	m.lastHead = opts.Head
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
