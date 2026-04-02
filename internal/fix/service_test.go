package fix

import (
	"context"
	"errors"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// --- mock 实现 ---

type mockIssueClient struct {
	getIssue     func(ctx context.Context, owner, repo string, index int64) (*gitea.Issue, *gitea.Response, error)
	listComments func(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error)
}

func (m *mockIssueClient) GetIssue(ctx context.Context, owner, repo string, index int64) (*gitea.Issue, *gitea.Response, error) {
	return m.getIssue(ctx, owner, repo, index)
}

func (m *mockIssueClient) ListIssueComments(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
	return m.listComments(ctx, owner, repo, index, opts)
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
	NewService(nil)
}

func TestExecute_InvalidIssueNumber(t *testing.T) {
	svc := NewService(&mockIssueClient{})

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
	})

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
	})

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
	})

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
		{ID: 1, Body: "I can reproduce this"},
		{ID: 2, Body: "Stack trace: ..."},
	}

	svc := NewService(&mockIssueClient{
		getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
			return issue, nil, nil
		},
		listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
			return comments, nil, nil
		},
	})

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
	if len(result.IssueContext.Labels) != 1 {
		t.Errorf("Labels count = %d, want 1", len(result.IssueContext.Labels))
	}
	if result.CLIMeta != nil {
		t.Error("M3.1 阶段 CLIMeta 应为 nil")
	}
	if result.RawOutput != "" {
		t.Error("M3.1 阶段 RawOutput 应为空")
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
				comments[i] = &gitea.Comment{ID: int64(i + 1)}
			}
			return comments, nil, nil
		},
	})

	result, err := svc.Execute(context.Background(), fixPayload())
	if err != nil {
		t.Fatalf("评论截断不应导致错误，实际: %v", err)
	}
	if len(result.IssueContext.Comments) != 50 {
		t.Errorf("Comments count = %d, want 50", len(result.IssueContext.Comments))
	}
}
