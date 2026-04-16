package fix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// IssueClient 窄接口，仅暴露 fix 所需的 Gitea API。
type IssueClient interface {
	GetIssue(ctx context.Context, owner, repo string, index int64) (*gitea.Issue, *gitea.Response, error)
	ListIssueComments(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error)
	CreateIssueComment(ctx context.Context, owner, repo string, index int64, opts gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error)
}

// RefClient 窄接口，仅暴露 ref 有效性验证所需的 Gitea API。
type RefClient interface {
	GetBranch(ctx context.Context, owner, repo, branch string) (*gitea.Branch, *gitea.Response, error)
	GetTag(ctx context.Context, owner, repo, tag string) (*gitea.Tag, *gitea.Response, error)
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

// WithRefClient 注入 ref 有效性验证客户端（可选）
func WithRefClient(c RefClient) ServiceOption {
	return func(s *Service) { s.refClient = c }
}

// Service Issue 分析编排服务，负责 analyze_issue 的上下文采集和分析执行。
type Service struct {
	gitea     IssueClient
	pool      FixPoolRunner
	cfgProv   FixConfigProvider
	refClient RefClient
	logger    *slog.Logger
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

// Execute 执行 analyze_issue 的完整流程。
// fix_issue 已在 M3.4 拆分为独立修复链路，不应再落到只读分析实现。
func (s *Service) Execute(ctx context.Context, payload model.TaskPayload) (*FixResult, error) {
	switch payload.TaskType {
	case "", model.TaskTypeAnalyzeIssue:
		return s.executeAnalysis(ctx, payload)
	case model.TaskTypeFixIssue:
		return nil, fmt.Errorf("fix.Service 不支持任务类型 %q，请走 fix_issue 修复执行链路", payload.TaskType)
	default:
		return nil, fmt.Errorf("fix.Service 不支持任务类型: %s", payload.TaskType)
	}
}

func (s *Service) executeAnalysis(ctx context.Context, payload model.TaskPayload) (*FixResult, error) {
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

	effectiveRef := issue.Ref
	if effectiveRef == "" {
		effectiveRef = payload.IssueRef
	}

	// 3. ref 空值检查
	if effectiveRef == "" {
		if err := s.commentRefMissing(ctx, owner, repo, issueNum); err != nil {
			return nil, fmt.Errorf("Issue #%d: 回写 ref 缺失提示失败: %w", issueNum, err)
		}
		return nil, fmt.Errorf("Issue #%d: %w", issueNum, ErrMissingIssueRef)
	}

	// 4. ref 有效性检查
	if s.refClient != nil {
		if err := s.validateRef(ctx, owner, repo, effectiveRef); err != nil {
			if errors.Is(err, ErrInvalidIssueRef) {
				if commentErr := s.commentRefInvalid(ctx, owner, repo, issueNum, effectiveRef); commentErr != nil {
					return nil, fmt.Errorf("Issue #%d ref=%q: 回写 ref 无效提示失败: %w", issueNum, effectiveRef, commentErr)
				}
				return nil, fmt.Errorf("Issue #%d ref=%q: %w", issueNum, effectiveRef, ErrInvalidIssueRef)
			}
			return nil, fmt.Errorf("验证 ref %q 失败: %w", effectiveRef, err)
		}
	}

	// 5. 采集上下文
	issueCtx, err := s.collectContext(ctx, owner, repo, issue)
	if err != nil {
		return nil, fmt.Errorf("采集 Issue #%d 上下文失败: %w", issueNum, err)
	}

	s.logger.InfoContext(ctx, "Issue 上下文采集完成",
		"issue", issueNum,
		"comments", len(issueCtx.Comments),
		"labels", len(issue.Labels),
	)
	issueCtx.Ref = effectiveRef
	payload.IssueRef = effectiveRef

	// 6. 构造 prompt + 容器执行
	prompt := s.buildPrompt(issueCtx)
	cmd := s.buildCommand()
	execResult, err := s.pool.RunWithCommandAndStdin(ctx, payload, cmd, []byte(prompt))
	if err != nil {
		return &FixResult{
			IssueContext: issueCtx,
			RawOutput:    safeOutput(execResult),
		}, fmt.Errorf("容器执行失败: %w", err)
	}
	if execResult == nil {
		return &FixResult{
			IssueContext: issueCtx,
		}, fmt.Errorf("容器执行结果为空")
	}
	if execResult.ExitCode != 0 {
		s.logger.ErrorContext(ctx, "Issue 分析 worker 非零退出",
			"issue", issueNum,
			"exit_code", execResult.ExitCode,
		)
		return &FixResult{
			IssueContext: issueCtx,
			RawOutput:    execResult.Output,
			ExitCode:     execResult.ExitCode,
		}, nil
	}

	// 7. 解析结果
	result := s.parseResult(execResult.Output)
	result.IssueContext = issueCtx
	result.RawOutput = execResult.Output

	// 8. 回写 Issue 评论
	// Claude 已明确返回可重试错误时，不在这里发送兜底评论，避免每次重试都重复刷屏。
	if result.CLIMeta == nil || !result.CLIMeta.IsError {
		comment := FormatAnalysisComment(result)
		if _, _, err := s.gitea.CreateIssueComment(ctx, owner, repo, issueNum,
			gitea.CreateIssueCommentOption{Body: comment}); err != nil {
			s.logger.ErrorContext(ctx, "回写分析评论失败",
				"issue", issueNum, "error", err)
			result.WritebackError = fmt.Errorf("回写分析评论失败: %w", err)
		}
	}

	return result, nil
}

// parseResult 双层 JSON 解析：外层 CLI 信封 -> 内层分析输出
func (s *Service) parseResult(output string) *FixResult {
	result := &FixResult{}

	// 外层 CLI JSON 信封
	var cliResp CLIResponse
	if err := json.Unmarshal([]byte(output), &cliResp); err != nil {
		result.ParseError = fmt.Errorf("CLI JSON 解析失败: %w", err)
		return result
	}
	result.CLIMeta = &model.CLIMeta{
		CostUSD:    cliResp.EffectiveCostUSD(),
		DurationMs: cliResp.DurationMs,
		IsError:    cliResp.IsExecutionError(),
		NumTurns:   cliResp.NumTurns,
		SessionID:  cliResp.SessionID,
	}

	if cliResp.IsExecutionError() {
		result.ParseError = fmt.Errorf("Claude CLI 报告错误: type=%s, subtype=%s", cliResp.Type, cliResp.Subtype)
		return result
	}

	// 内层分析 JSON
	jsonText := extractJSON(cliResp.Result)
	var analysis AnalysisOutput
	if err := json.Unmarshal([]byte(jsonText), &analysis); err != nil {
		result.ParseError = fmt.Errorf("分析 JSON 解析失败: %w", err)
		return result
	}
	result.Analysis = &analysis
	return result
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

// stripRefPrefix 剥离 Gitea Issue.Ref 返回的完整 refspec 前缀，
// 返回裸分支名或 tag 名，供 REST API 查询使用。
func stripRefPrefix(ref string) string {
	if after, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
		return after
	}
	if after, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
		return after
	}
	return ref
}

// validateRef 验证 Issue 关联的 ref 是否存在（先检查分支，再检查 tag）。
func (s *Service) validateRef(ctx context.Context, owner, repo, ref string) error {
	bare := stripRefPrefix(ref)
	_, _, err := s.refClient.GetBranch(ctx, owner, repo, bare)
	if err == nil {
		return nil
	}
	if !gitea.IsNotFound(err) {
		return fmt.Errorf("检查分支 %q 失败: %w", bare, err)
	}
	_, _, err = s.refClient.GetTag(ctx, owner, repo, bare)
	if err == nil {
		return nil
	}
	if !gitea.IsNotFound(err) {
		return fmt.Errorf("检查 tag %q 失败: %w", bare, err)
	}
	return ErrInvalidIssueRef
}

func (s *Service) commentRefMissing(ctx context.Context, owner, repo string, issueNum int64) error {
	body := "⚠️ 该 Issue 未设置关联分支，无法确定分析目标。\n\n请在 Issue 右侧边栏「Ref」处指定目标分支或 tag，然后重新添加 `auto-fix` 标签以触发分析。"
	if _, _, err := s.gitea.CreateIssueComment(ctx, owner, repo, issueNum,
		gitea.CreateIssueCommentOption{Body: body}); err != nil {
		s.logger.ErrorContext(ctx, "回写 ref 缺失评论失败",
			"issue", issueNum, "error", err)
		return err
	}
	return nil
}

func (s *Service) commentRefInvalid(ctx context.Context, owner, repo string, issueNum int64, ref string) error {
	body := fmt.Sprintf("⚠️ 该 Issue 关联的 ref `%s` 不存在（已检查分支和 tag），无法执行分析。\n\n请在 Issue 右侧边栏「Ref」处修正目标分支或 tag，然后重新添加 `auto-fix` 标签以触发分析。", ref)
	if _, _, err := s.gitea.CreateIssueComment(ctx, owner, repo, issueNum,
		gitea.CreateIssueCommentOption{Body: body}); err != nil {
		s.logger.ErrorContext(ctx, "回写 ref 无效评论失败",
			"issue", issueNum, "error", err)
		return err
	}
	return nil
}
