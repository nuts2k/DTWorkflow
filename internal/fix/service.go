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

// PRClient M3.5 窄接口，仅暴露修复模式创建 PR 所需的 Gitea API。
// *gitea.Client 通过 CreatePullRequest 和 GetRepo 满足此接口。
type PRClient interface {
	CreatePullRequest(ctx context.Context, owner, repo string,
		opts gitea.CreatePullRequestOption) (*gitea.PullRequest, *gitea.Response, error)
	// GetRepo 用于 Tag-as-Ref 场景下获取仓库默认分支作为 PR Base。
	GetRepo(ctx context.Context, owner, repo string) (*gitea.Repository, *gitea.Response, error)
}

// FixStaleChecker M3.5 窄接口，查询前序分析结果以支持"信息不足"前置检查。
// *store.SQLiteStore 通过 GetLatestAnalysisByIssue 满足此接口。
type FixStaleChecker interface {
	GetLatestAnalysisByIssue(ctx context.Context, repoFullName string,
		issueNumber int64) (*model.TaskRecord, error)
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

// WithPRClient M3.5: 注入创建 PR 所需的 Gitea API 客户端（可选）。
// 仅在 fix_issue 修复模式执行 PR 创建时使用；analyze_issue 无需。
func WithPRClient(c PRClient) ServiceOption {
	return func(s *Service) { s.prClient = c }
}

// WithFixStaleChecker M3.5: 注入前序分析查询接口（可选）。
// 仅在 fix_issue 模式下用于"信息不足"前置检查。未注入时跳过检查（Claude 自行判断）。
func WithFixStaleChecker(c FixStaleChecker) ServiceOption {
	return func(s *Service) { s.staleChecker = c }
}

// Service Issue 分析编排服务，负责 analyze_issue 的上下文采集和分析执行。
type Service struct {
	gitea        IssueClient
	pool         FixPoolRunner
	cfgProv      FixConfigProvider
	refClient    RefClient
	prClient     PRClient        // M3.5: fix_issue 创建 PR 使用
	staleChecker FixStaleChecker // M3.5: fix_issue 前置检查使用
	logger       *slog.Logger
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
		if _, err := s.validateRef(ctx, owner, repo, effectiveRef); err != nil {
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

// parseFixResult M3.5: 双层 JSON 解析 — 外层 CLI 信封 → 内层 FixOutput。
// 与 parseResult（分析模式）的区别仅在于内层 schema；外层解析逻辑相同。
// 成功场景不变量：success=true 时 BranchName 和 CommitSHA 必须非空，否则视为 ParseError。
func (s *Service) parseFixResult(output string) *FixResult {
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

	// 内层 FixOutput JSON
	jsonText := extractJSON(cliResp.Result)
	var fix FixOutput
	if err := json.Unmarshal([]byte(jsonText), &fix); err != nil {
		result.ParseError = fmt.Errorf("FixOutput JSON 解析失败: %w", err)
		return result
	}

	// 成功不变量校验：success=true 必须有 branch_name 和 commit_sha
	if fix.Success {
		if fix.BranchName == "" || fix.CommitSHA == "" {
			result.ParseError = fmt.Errorf("FixOutput 不变量违反：success=true 但 branch_name=%q commit_sha=%q",
				fix.BranchName, fix.CommitSHA)
			return result
		}
	}

	result.Fix = &fix
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

// validateRef M3.5: 验证 Issue 关联的 ref 是否存在，并返回 ref 类型。
// 先查分支再查 tag；RefKind 用于 PR 创建时决定 Base 字段（tag 不能直接作为 Base）。
func (s *Service) validateRef(ctx context.Context, owner, repo, ref string) (RefKind, error) {
	bare := stripRefPrefix(ref)
	_, _, err := s.refClient.GetBranch(ctx, owner, repo, bare)
	if err == nil {
		return RefKindBranch, nil
	}
	if !gitea.IsNotFound(err) {
		return RefKindUnknown, fmt.Errorf("检查分支 %q 失败: %w", bare, err)
	}
	_, _, err = s.refClient.GetTag(ctx, owner, repo, bare)
	if err == nil {
		return RefKindTag, nil
	}
	if !gitea.IsNotFound(err) {
		return RefKindUnknown, fmt.Errorf("检查 tag %q 失败: %w", bare, err)
	}
	return RefKindUnknown, ErrInvalidIssueRef
}

// checkPreviousAnalysis M3.5: 查询最新 analyze_issue 结果，判断信息是否充分。
// 返回 (true, nil) 表示可继续修复；(false, nil) 表示前序分析明确标记为信息不足。
// 错误和解析失败一律 fail-open（返回 true），避免因辅助检查失败而阻断主流程。
func (s *Service) checkPreviousAnalysis(ctx context.Context, repoFullName string, issueNumber int64) (bool, error) {
	if s.staleChecker == nil {
		return true, nil // 未注入时跳过检查
	}
	rec, err := s.staleChecker.GetLatestAnalysisByIssue(ctx, repoFullName, issueNumber)
	if err != nil {
		s.logger.WarnContext(ctx, "查询前序分析失败，fail-open 继续修复",
			"repo", repoFullName, "issue", issueNumber, "error", err)
		return true, nil
	}
	if rec == nil || rec.Result == "" {
		return true, nil // 无前序分析，放行
	}
	// 解析 rec.Result：外层 CLI 信封 → 内层 AnalysisOutput
	var cliResp CLIResponse
	if err := json.Unmarshal([]byte(rec.Result), &cliResp); err != nil {
		s.logger.WarnContext(ctx, "前序分析 CLI 信封解析失败，fail-open",
			"task_id", rec.ID, "error", err)
		return true, nil
	}
	if cliResp.IsExecutionError() {
		return true, nil // 前序分析本身就失败，无有效结论，放行让 Claude 重新分析
	}
	var analysis AnalysisOutput
	if err := json.Unmarshal([]byte(extractJSON(cliResp.Result)), &analysis); err != nil {
		s.logger.WarnContext(ctx, "前序分析内层 JSON 解析失败，fail-open",
			"task_id", rec.ID, "error", err)
		return true, nil
	}
	if !analysis.InfoSufficient {
		s.logger.InfoContext(ctx, "前序分析标记为信息不足，阻断修复",
			"task_id", rec.ID, "missing_info", analysis.MissingInfo)
		return false, nil
	}
	return true, nil
}

// latestMissingInfo 读取最新分析的 missing_info 列表，用于生成"信息不足"评论。
// 解析失败时返回 nil。
func (s *Service) latestMissingInfo(ctx context.Context, repoFullName string, issueNumber int64) []string {
	if s.staleChecker == nil {
		return nil
	}
	rec, err := s.staleChecker.GetLatestAnalysisByIssue(ctx, repoFullName, issueNumber)
	if err != nil || rec == nil {
		return nil
	}
	var cliResp CLIResponse
	if err := json.Unmarshal([]byte(rec.Result), &cliResp); err != nil {
		return nil
	}
	var analysis AnalysisOutput
	if err := json.Unmarshal([]byte(extractJSON(cliResp.Result)), &analysis); err != nil {
		return nil
	}
	return analysis.MissingInfo
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
