package queue

import (
	"context"
	"log/slog"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
)

// BranchCleaner 对 auto-test/{module} 分支的"关 PR + 删分支"收尾接口。
//
// M4.2 Cancel-and-Replace 场景中，当新的 gen_tests 任务取代前一次任务时，
// 旧的远程工作分支与尚未合并的 PR 都应该被统一清理，避免在 Gitea 上遗留
// 陈旧的评论/状态。任一子步骤失败只记日志，不阻断上层入队流程。
type BranchCleaner interface {
	// CleanupAutoTestBranch 依次执行：
	//   1. ListRepoPullRequests(state=open) 找到 head 指向 branch 的 open PR；
	//      若命中则 ClosePullRequest（关 PR 可让 Gitea 取消 webhook 语义，
	//      避免后续拉新分支时旧 PR 仍对 head 起作用）。
	//   2. DeleteBranch 删除远程分支。
	// 任一子步骤失败仅 warn，方法始终返回 nil，调用方无需处理 error。
	CleanupAutoTestBranch(ctx context.Context, owner, repo, branch string) error
}

// branchCleanerClient 是 giteaBranchCleaner 依赖的窄接口。
// *gitea.Client 天然满足；窄化便于单测注入 stub。
type branchCleanerClient interface {
	ListRepoPullRequests(ctx context.Context, owner, repo string, opts gitea.ListPullRequestsOptions) ([]*gitea.PullRequest, *gitea.Response, error)
	ClosePullRequest(ctx context.Context, owner, repo string, index int64) error
	DeleteBranch(ctx context.Context, owner, repo, branch string) error
}

// giteaBranchCleaner BranchCleaner 的默认 Gitea 实现。
type giteaBranchCleaner struct {
	client branchCleanerClient
	logger *slog.Logger
}

// NewBranchCleaner 返回一个基于 *gitea.Client 的 BranchCleaner。
// client 为 nil 属编程错误，panic 与其他 queue 构造器一致。
func NewBranchCleaner(client *gitea.Client, logger *slog.Logger) BranchCleaner {
	if client == nil {
		panic("NewBranchCleaner: client 不能为 nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &giteaBranchCleaner{client: client, logger: logger}
}

// newBranchCleanerWithClient 内部构造函数，接受窄接口便于单测。
func newBranchCleanerWithClient(client branchCleanerClient, logger *slog.Logger) *giteaBranchCleaner {
	if logger == nil {
		logger = slog.Default()
	}
	return &giteaBranchCleaner{client: client, logger: logger}
}

// CleanupAutoTestBranch 按"关 PR → 删分支"顺序执行收尾清理。
// 任一步失败仅 warn，方法始终返回 nil——上层入队流程不应因此受阻。
func (c *giteaBranchCleaner) CleanupAutoTestBranch(ctx context.Context, owner, repo, branch string) error {
	// 1. 列出所有 open PR；失败时记 warn 后继续尝试删分支（删分支本身有 404 幂等）。
	prs, listErr := gitea.PaginateAll(ctx, 50, 10,
		func(ctx context.Context, page, pageSize int) ([]*gitea.PullRequest, *gitea.Response, error) {
			return c.client.ListRepoPullRequests(ctx, owner, repo, gitea.ListPullRequestsOptions{
				ListOptions: gitea.ListOptions{Page: page, PageSize: pageSize},
				State:       "open",
			})
		})
	if listErr != nil {
		c.logger.WarnContext(ctx, "列出远程 PR 失败，跳过关闭旧 PR 步骤",
			"owner", owner,
			"repo", repo,
			"branch", branch,
			"error", listErr,
		)
	} else {
		for _, pr := range prs {
			if pr == nil || pr.Head == nil {
				continue
			}
			if pr.Head.Ref != branch {
				continue
			}
			if err := c.client.ClosePullRequest(ctx, owner, repo, pr.Number); err != nil {
				c.logger.WarnContext(ctx, "关闭旧 auto-test PR 失败，继续删除分支",
					"owner", owner,
					"repo", repo,
					"branch", branch,
					"pr", pr.Number,
					"error", err,
				)
			} else {
				c.logger.InfoContext(ctx, "已关闭旧 auto-test PR",
					"owner", owner,
					"repo", repo,
					"branch", branch,
					"pr", pr.Number,
				)
			}
		}
	}

	// 2. 删除远程分支。ClosePullRequest / DeleteBranch 在底层已实现 404 幂等；
	//    其它错误仅 warn，不向上冒泡。
	if err := c.client.DeleteBranch(ctx, owner, repo, branch); err != nil {
		c.logger.WarnContext(ctx, "删除 auto-test 远程分支失败",
			"owner", owner,
			"repo", repo,
			"branch", branch,
			"error", err,
		)
	} else {
		c.logger.InfoContext(ctx, "已删除 auto-test 远程分支",
			"owner", owner,
			"repo", repo,
			"branch", branch,
		)
	}
	return nil
}
