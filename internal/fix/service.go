package fix

import (
	"context"
	"fmt"
	"log/slog"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// IssueClient 窄接口，仅暴露 fix 所需的 Gitea API。
// M3.3 回写时再扩展 CreateIssueComment。
type IssueClient interface {
	GetIssue(ctx context.Context, owner, repo string, index int64) (*gitea.Issue, *gitea.Response, error)
	ListIssueComments(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error)
}

// FixPoolRunner fix 专用的容器执行接口。
// *worker.Pool 通过 RunWithCommandAndStdin 满足此接口。
type FixPoolRunner interface {
	RunWithCommandAndStdin(ctx context.Context, payload model.TaskPayload, cmd []string, stdinData []byte) (*worker.ExecutionResult, error)
}

// FixConfigProvider 获取全局 Claude 配置的接口
type FixConfigProvider interface {
	GetClaudeModel() string
	GetClaudeEffort() string
}

// ServiceOption Service 配置选项
type ServiceOption func(*Service)

// WithServiceLogger 设置自定义日志记录器
func WithServiceLogger(logger *slog.Logger) ServiceOption {
	return func(s *Service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// WithConfigProvider 注入配置提供者（可选）
func WithConfigProvider(p FixConfigProvider) ServiceOption {
	return func(s *Service) { s.cfgProv = p }
}

// Service Issue 分析编排服务，负责 Issue 上下文采集和分析执行
type Service struct {
	gitea   IssueClient
	pool    FixPoolRunner
	cfgProv FixConfigProvider
	logger  *slog.Logger
}

// NewService 创建 Issue 分析服务实例。
// gitea 和 pool 为必要依赖，传入 nil 属于编程错误。
func NewService(gitea IssueClient, pool FixPoolRunner, opts ...ServiceOption) *Service {
	if gitea == nil {
		panic("NewService: gitea 不能为 nil")
	}
	if pool == nil {
		panic("NewService: pool 不能为 nil")
	}
	s := &Service{
		gitea:  gitea,
		pool:   pool,
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Execute 执行 Issue 分析的完整流程。
// M3.1 只完成上下文采集，M3.2 补充容器执行，M3.3 补充结果回写。
func (s *Service) Execute(ctx context.Context, payload model.TaskPayload) (*FixResult, error) {
	owner, repo, issueNum := payload.RepoOwner, payload.RepoName, payload.IssueNumber

	// 1. 前置校验
	if issueNum <= 0 {
		return nil, fmt.Errorf("无效的 Issue 编号: %d", issueNum)
	}

	// 2. Issue 状态校验
	issue, _, err := s.gitea.GetIssue(ctx, owner, repo, issueNum)
	if err != nil {
		return nil, fmt.Errorf("获取 Issue #%d 信息失败: %w", issueNum, err)
	}
	if issue.State != "open" {
		return nil, fmt.Errorf("Issue #%d 状态为 %s: %w", issueNum, issue.State, ErrIssueNotOpen)
	}

	// 3. 采集上下文
	issueCtx, err := s.collectContext(ctx, owner, repo, issue)
	if err != nil {
		return nil, fmt.Errorf("采集 Issue #%d 上下文失败: %w", issueNum, err)
	}

	s.logger.InfoContext(ctx, "Issue 上下文采集完成",
		"issue", issueNum,
		"comments", len(issueCtx.Comments),
		"labels", len(issue.Labels),
	)

	// 4. M3.2 在此处插入：构造 prompt + 容器执行
	// 5. M3.3 在此处插入：解析结果 + 回写

	return &FixResult{IssueContext: issueCtx}, nil
}

// collectContext 采集 Issue 富上下文
func (s *Service) collectContext(ctx context.Context, owner, repo string, issue *gitea.Issue) (*IssueContext, error) {
	// 单页获取评论（最多 50 条）
	comments, _, err := s.gitea.ListIssueComments(ctx, owner, repo, issue.Number, gitea.ListOptions{PageSize: 50})
	if err != nil {
		return nil, fmt.Errorf("获取评论失败: %w", err)
	}

	if issue.Comments > len(comments) {
		s.logger.WarnContext(ctx, "Issue 评论数超过单页上限，部分评论未采集",
			"issue", issue.Number,
			"total", issue.Comments,
			"fetched", len(comments),
		)
	}

	return &IssueContext{
		Issue:    issue,
		Comments: comments,
	}, nil
}
