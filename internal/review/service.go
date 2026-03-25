package review

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// PRClient 窄接口，仅暴露评审所需的 Gitea API
type PRClient interface {
	GetPullRequest(ctx context.Context, owner, repo string, index int64) (*gitea.PullRequest, *gitea.Response, error)
	ListPullRequestFiles(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error)
}

// ReviewPoolRunner 评审专用的容器执行接口，与 queue.PoolRunner 独立。
// *worker.Pool 同时满足 queue.PoolRunner（Run）和 ReviewPoolRunner（RunWithCommand）。
type ReviewPoolRunner interface {
	RunWithCommand(ctx context.Context, payload model.TaskPayload, cmd []string) (*worker.ExecutionResult, error)
}

// ConfigProvider 获取评审配置的接口
type ConfigProvider interface {
	ResolveReviewConfig(repoFullName string) config.ReviewOverride
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

// Service 评审编排服务，负责 PR 元数据获取、prompt 构造和结果解析
type Service struct {
	gitea   PRClient
	pool    ReviewPoolRunner
	cfgProv ConfigProvider // 访问评审配置（支持热加载）
	logger  *slog.Logger
}

// NewService 创建评审服务实例
func NewService(gitea PRClient, pool ReviewPoolRunner, cfgProv ConfigProvider, opts ...ServiceOption) *Service {
	s := &Service{
		gitea:   gitea,
		pool:    pool,
		cfgProv: cfgProv,
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Execute 执行 PR 评审的完整流程
func (s *Service) Execute(ctx context.Context, payload model.TaskPayload) (*ReviewResult, error) {
	owner, repo, prNum := payload.RepoOwner, payload.RepoName, payload.PRNumber

	// 0. 前置校验
	if prNum <= 0 {
		return nil, fmt.Errorf("无效的 PR 编号: %d", prNum)
	}

	// 1. PR 状态校验
	pr, _, err := s.gitea.GetPullRequest(ctx, owner, repo, prNum)
	if err != nil {
		return nil, fmt.Errorf("获取 PR #%d 信息失败: %w", prNum, err)
	}
	if pr.State != "open" {
		return nil, fmt.Errorf("PR #%d 状态为 %s: %w", prNum, pr.State, ErrPRNotOpen)
	}

	// 2. 获取变更文件列表（单页，最多 100 个文件）
	// 此列表仅用于 prompt 摘要和大 PR 检测，非评审的完整输入。
	// Claude CLI 在容器内通过 git diff 获取完整变更。
	files, _, err := s.gitea.ListPullRequestFiles(ctx, owner, repo, prNum, gitea.ListOptions{PageSize: 100})
	if err != nil {
		return nil, fmt.Errorf("获取 PR #%d 文件列表失败: %w", prNum, err)
	}

	// 3. 大 PR 警告（不阻断，仅日志）
	totalChanges := sumChanges(files)
	if totalChanges > 10000 {
		s.logger.WarnContext(ctx, "超大 PR，评审质量可能受影响",
			"pr", prNum, "files", len(files), "changes", totalChanges)
	}

	// 4. 获取评审配置（全局 + 仓库级合并）
	cfg := s.resolveConfig(payload.RepoFullName)

	// 5. 构造 prompt + 命令
	prompt := s.buildPrompt(pr, files, cfg)
	cmd := s.buildCommand(prompt)

	// 6. 执行容器
	execResult, err := s.pool.RunWithCommand(ctx, payload, cmd)
	if err != nil {
		return &ReviewResult{RawOutput: safeOutput(execResult)}, fmt.Errorf("容器执行失败: %w", err)
	}

	// 7. 解析结果
	result := s.parseResult(execResult.Output)
	result.RawOutput = execResult.Output
	return result, nil
}

// parseResult 双层 JSON 解析：外层 CLI 信封 -> 内层评审输出
func (s *Service) parseResult(output string) *ReviewResult {
	result := &ReviewResult{}

	// 解析外层 CLI JSON 信封
	var cliResp CLIResponse
	if err := json.Unmarshal([]byte(output), &cliResp); err != nil {
		result.ParseError = fmt.Errorf("CLI JSON 解析失败: %w", err)
		return result
	}
	result.CLIMeta = &CLIMeta{
		CostUSD:    cliResp.CostUSD,
		DurationMs: cliResp.DurationMs,
		IsError:    cliResp.IsError,
		NumTurns:   cliResp.NumTurns,
		SessionID:  cliResp.SessionID,
	}

	// CLI 报告执行错误
	if cliResp.IsError {
		result.ParseError = fmt.Errorf("Claude CLI 报告错误: subtype=%s", cliResp.Subtype)
		return result
	}

	// 解析内层评审 JSON（result 字段是字符串，可能包含 code fence）
	jsonText := extractJSON(cliResp.Result)
	var review ReviewOutput
	if err := json.Unmarshal([]byte(jsonText), &review); err != nil {
		// 优雅降级：内层 JSON 解析失败，保留 RawOutput 供 M2.3 作为普通评论
		result.ParseError = fmt.Errorf("评审 JSON 解析失败: %w", err)
		return result
	}
	result.Review = &review
	return result
}

// resolveConfig 从 ConfigProvider 获取配置并转换为内部 ReviewConfig
func (s *Service) resolveConfig(repoFullName string) ReviewConfig {
	override := s.cfgProv.ResolveReviewConfig(repoFullName)

	cfg := ReviewConfig{
		Instructions:     override.Instructions,
		Dimensions:       override.Dimensions,
		LargePRThreshold: override.LargePRThreshold,
	}

	// 应用默认值
	if cfg.Instructions == "" {
		cfg.Instructions = defaultReviewInstructions
	}
	if len(cfg.Dimensions) == 0 {
		cfg.Dimensions = []string{"security", "logic", "architecture", "style"}
	}
	if cfg.LargePRThreshold <= 0 {
		cfg.LargePRThreshold = 5000
	}

	return cfg
}
