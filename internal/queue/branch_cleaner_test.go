package queue

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
)

// fakeBranchCleanerClient 内存 stub，实现 branchCleanerClient 窄接口。
type fakeBranchCleanerClient struct {
	listResult []*gitea.PullRequest
	listErr    error

	closeErr    error
	closedIndex []int64

	deleteErr      error
	deleteCalls    int
	deletedBranch  string
}

func (f *fakeBranchCleanerClient) ListRepoPullRequests(
	_ context.Context, _, _ string, _ gitea.ListPullRequestsOptions,
) ([]*gitea.PullRequest, *gitea.Response, error) {
	if f.listErr != nil {
		return nil, nil, f.listErr
	}
	return f.listResult, nil, nil
}

func (f *fakeBranchCleanerClient) ClosePullRequest(
	_ context.Context, _, _ string, index int64,
) error {
	f.closedIndex = append(f.closedIndex, index)
	return f.closeErr
}

func (f *fakeBranchCleanerClient) DeleteBranch(
	_ context.Context, _, _, branch string,
) error {
	f.deleteCalls++
	f.deletedBranch = branch
	return f.deleteErr
}

// 成功路径：List 命中 → Close → Delete，均成功。
func TestCleanupAutoTestBranch_Success(t *testing.T) {
	fc := &fakeBranchCleanerClient{
		listResult: []*gitea.PullRequest{
			{
				Number: 10,
				Head:   &gitea.PRBranch{Ref: "auto-test/svc-user"},
			},
		},
	}
	c := newBranchCleanerWithClient(fc, slog.Default())

	if err := c.CleanupAutoTestBranch(context.Background(), "org", "repo", "auto-test/svc-user"); err != nil {
		t.Fatalf("CleanupAutoTestBranch 不应返回 error，实际: %v", err)
	}
	if len(fc.closedIndex) != 1 || fc.closedIndex[0] != 10 {
		t.Errorf("closedIndex = %v，期望 [10]", fc.closedIndex)
	}
	if fc.deleteCalls != 1 || fc.deletedBranch != "auto-test/svc-user" {
		t.Errorf("DeleteBranch 应被调用 1 次且 branch=auto-test/svc-user，实际 calls=%d branch=%q",
			fc.deleteCalls, fc.deletedBranch)
	}
}

// PR 不存在：List 返回空列表，不调 Close，仍调 Delete。
func TestCleanupAutoTestBranch_NoMatchingPR(t *testing.T) {
	fc := &fakeBranchCleanerClient{listResult: nil}
	c := newBranchCleanerWithClient(fc, slog.Default())

	if err := c.CleanupAutoTestBranch(context.Background(), "org", "repo", "auto-test/svc-user"); err != nil {
		t.Fatalf("CleanupAutoTestBranch 不应返回 error，实际: %v", err)
	}
	if len(fc.closedIndex) != 0 {
		t.Errorf("无匹配 PR 时不应调用 ClosePullRequest，实际 closedIndex=%v", fc.closedIndex)
	}
	if fc.deleteCalls != 1 {
		t.Errorf("DeleteBranch 应仍被调用 1 次，实际 %d", fc.deleteCalls)
	}
}

// List 同时返回非目标分支的 PR：仅关闭 head 匹配的 PR。
func TestCleanupAutoTestBranch_OnlyMatchingHead(t *testing.T) {
	fc := &fakeBranchCleanerClient{
		listResult: []*gitea.PullRequest{
			{Number: 11, Head: &gitea.PRBranch{Ref: "feature/other"}},
			{Number: 22, Head: &gitea.PRBranch{Ref: "auto-test/svc-user"}},
			{Number: 33, Head: nil}, // 畸形：Head=nil 应被跳过
		},
	}
	c := newBranchCleanerWithClient(fc, slog.Default())

	_ = c.CleanupAutoTestBranch(context.Background(), "org", "repo", "auto-test/svc-user")

	if len(fc.closedIndex) != 1 || fc.closedIndex[0] != 22 {
		t.Errorf("应只关闭 head=auto-test/svc-user 的 PR（#22），实际 closedIndex=%v", fc.closedIndex)
	}
}

// Close 失败：warn 不阻断，仍继续删除分支，方法返回 nil。
func TestCleanupAutoTestBranch_CloseFailure_StillDeletes(t *testing.T) {
	fc := &fakeBranchCleanerClient{
		listResult: []*gitea.PullRequest{
			{Number: 7, Head: &gitea.PRBranch{Ref: "auto-test/svc-user"}},
		},
		closeErr: errors.New("403 forbidden"),
	}
	c := newBranchCleanerWithClient(fc, slog.Default())

	if err := c.CleanupAutoTestBranch(context.Background(), "org", "repo", "auto-test/svc-user"); err != nil {
		t.Fatalf("Close 失败时仍应返回 nil，实际: %v", err)
	}
	if fc.deleteCalls != 1 {
		t.Errorf("Close 失败后仍应调 DeleteBranch，实际 calls=%d", fc.deleteCalls)
	}
}

// List 失败：warn 不阻断，直接尝试删除分支，方法返回 nil。
func TestCleanupAutoTestBranch_ListFailure_StillDeletes(t *testing.T) {
	fc := &fakeBranchCleanerClient{listErr: errors.New("500 upstream")}
	c := newBranchCleanerWithClient(fc, slog.Default())

	if err := c.CleanupAutoTestBranch(context.Background(), "org", "repo", "auto-test/svc-user"); err != nil {
		t.Fatalf("List 失败时仍应返回 nil，实际: %v", err)
	}
	if len(fc.closedIndex) != 0 {
		t.Errorf("List 失败时不应尝试 Close，实际 closedIndex=%v", fc.closedIndex)
	}
	if fc.deleteCalls != 1 {
		t.Errorf("List 失败后仍应调 DeleteBranch，实际 calls=%d", fc.deleteCalls)
	}
}

// Delete 失败：warn，方法仍返回 nil。
func TestCleanupAutoTestBranch_DeleteFailure_ReturnsNil(t *testing.T) {
	fc := &fakeBranchCleanerClient{
		listResult: nil,
		deleteErr:  errors.New("409 conflict"),
	}
	c := newBranchCleanerWithClient(fc, slog.Default())

	if err := c.CleanupAutoTestBranch(context.Background(), "org", "repo", "auto-test/svc-user"); err != nil {
		t.Fatalf("Delete 失败时仍应返回 nil，实际: %v", err)
	}
}

// NewBranchCleaner(nil) 应 panic，保持 queue 包构造器惯例。
func TestNewBranchCleaner_PanicOnNilClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewBranchCleaner(nil) 应 panic")
		}
	}()
	NewBranchCleaner(nil, slog.Default())
}
