package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

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
// *worker.Pool 通过 RunWithCommandAndStdin 同时满足此接口。
type ReviewPoolRunner interface {
	RunWithCommandAndStdin(ctx context.Context, payload model.TaskPayload, cmd []string, stdinData []byte) (*worker.ExecutionResult, error)
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

// WithWriter 注入回写器，Execute 成功后自动将评审结果回写到 Gitea PR。
// writer 为 nil 时跳过回写（向后兼容 M2.2）。
func WithWriter(w *Writer) ServiceOption {
	return func(s *Service) {
		s.writer = w
	}
}

// Service 评审编排服务，负责 PR 元数据获取、prompt 构造和结果解析
type Service struct {
	gitea   PRClient
	pool    ReviewPoolRunner
	cfgProv ConfigProvider // 访问评审配置（支持热加载）
	logger  *slog.Logger
	writer  *Writer // 可选，非 nil 时在 Execute 末尾执行回写
}

// NewService 创建评审服务实例。
// gitea、pool、cfgProv 为必要依赖，传入 nil 属于编程错误。
func NewService(gitea PRClient, pool ReviewPoolRunner, cfgProv ConfigProvider, opts ...ServiceOption) *Service {
	if gitea == nil {
		panic("NewService: gitea 不能为 nil")
	}
	if pool == nil {
		panic("NewService: pool 不能为 nil")
	}
	if cfgProv == nil {
		panic("NewService: cfgProv 不能为 nil")
	}
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
	adds, dels := countChanges(files)
	totalChanges := adds + dels
	if totalChanges > 10000 {
		s.logger.WarnContext(ctx, "超大 PR，评审质量可能受影响",
			"pr", prNum, "files", len(files), "changes", totalChanges)
	}

	// 4. 获取评审配置（全局 + 仓库级合并）
	cfg := s.resolveConfig(payload.RepoFullName)

	// 4.5 检测技术栈（文件列表可能不完整时记录警告）
	techStack, unknownTech := resolveTechStack(files, cfg)
	if len(unknownTech) > 0 {
		s.logger.WarnContext(ctx, "配置中包含无法识别的技术栈，已忽略",
			"pr", prNum, "unknown", unknownTech)
	}
	if len(cfg.TechStack) == 0 && len(files) >= 100 {
		s.logger.WarnContext(ctx, "文件列表可能不完整（单页 100 限制），技术栈自动检测结果可能有遗漏",
			"pr", prNum, "files", len(files), "detected_stack", int(techStack))
	}

	// 5. 构造 prompt + 命令
	prompt := s.buildPrompt(pr, files, cfg, techStack)
	cmd := s.buildCommand(cfg)

	// 6. 通过 stdin 传入 prompt 执行容器
	execResult, err := s.pool.RunWithCommandAndStdin(ctx, payload, cmd, []byte(prompt))
	if err != nil {
		return &ReviewResult{RawOutput: safeOutput(execResult)}, fmt.Errorf("容器执行失败: %w", err)
	}

	// 7. 解析结果
	result := s.parseResult(execResult.Output)
	result.RawOutput = execResult.Output

	// 8. 回写评审结果到 Gitea（writer 非 nil 时执行）
	if s.writer != nil {
		headSHA := ""
		if pr.Head != nil {
			headSHA = pr.Head.SHA
		}
		input := WritebackInput{
			TaskID:            payload.TaskID,
			Owner:             owner,
			Repo:              repo,
			PRNumber:          prNum,
			HeadSHA:           headSHA,
			Result:            result,
			TaskCreatedAt:     payload.CreatedAt,
			SupersededCount:   payload.SupersededCount,
			PreviousHeadSHA:   payload.PreviousHeadSHA,
			SeverityThreshold: cfg.Severity,       // M2.5 新增
			IgnorePatterns:    cfg.IgnorePatterns,  // M2.5 新增
		}
		reviewID, wbErr := s.writer.Write(ctx, input)
		if reviewID != 0 {
			result.GiteaReviewID = reviewID
		}
		if wbErr != nil {
			if errors.Is(wbErr, ErrStaleReview) {
				s.logger.InfoContext(ctx, "评审结果已过时，跳过成功回写",
					"pr", prNum, "task_id", payload.DeliveryID)
				return result, wbErr
			}
			s.logger.ErrorContext(ctx, "回写评审结果失败",
				"pr", prNum, "error", wbErr)
			result.WritebackError = wbErr
		}
	}

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
	result.CLIMeta = &model.CLIMeta{
		CostUSD:    cliResp.EffectiveCostUSD(),
		DurationMs: cliResp.DurationMs,
		IsError:    cliResp.IsExecutionError(),
		NumTurns:   cliResp.NumTurns,
		SessionID:  cliResp.SessionID,
	}

	// CLI 报告执行错误（同时检查 is_error 标志和 type 字段）
	if cliResp.IsExecutionError() {
		result.ParseError = fmt.Errorf("Claude CLI 报告错误: type=%s, subtype=%s", cliResp.Type, cliResp.Subtype)
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
	// Claude 可能使用 location/title/detail 等替代字段名，规范化到标准 ReviewIssue 字段
	normalizeIssues(jsonText, review.Issues)
	return result
}

// normalizeIssues 对 Claude 返回的 issues 做字段名规范化。
// Claude 有时使用 location/title/detail 替代 file+line/category/message，
// 导致标准 json.Unmarshal 无法映射这些字段。此函数从原始 JSON 中
// 提取替代字段并补充到 ReviewIssue 中。
func normalizeIssues(rawJSON string, issues []ReviewIssue) {
	var parsed struct {
		Issues []map[string]any `json:"issues"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil || len(parsed.Issues) == 0 {
		return
	}

	for i := range issues {
		if i >= len(parsed.Issues) {
			break
		}
		raw := parsed.Issues[i]

		// location → file + line（取第一个 file:line 对）
		if loc, _ := raw["location"].(string); loc != "" && (issues[i].File == "" || issues[i].Line <= 0) {
			locFile, locLine := parseLocation(loc)
			if issues[i].File == "" {
				issues[i].File = locFile
			}
			// 仅当 location 中的文件与已有 file 一致时才补充行号
			if issues[i].Line <= 0 && locLine > 0 && issues[i].File == locFile {
				issues[i].Line = locLine
			}
		}

		// detail / description → message
		if issues[i].Message == "" {
			if v, _ := raw["detail"].(string); v != "" {
				issues[i].Message = v
			} else if v, _ := raw["description"].(string); v != "" {
				issues[i].Message = v
			}
		}

		// title → 作为 message 前缀（仅当原始 JSON 中无标准 message 字段时）
		// 避免 Claude 同时返回 message + title 时重复拼接
		if title, _ := raw["title"].(string); title != "" {
			_, hasStdMessage := raw["message"].(string)
			if !hasStdMessage {
				if issues[i].Message != "" {
					issues[i].Message = title + "： " + issues[i].Message
				} else {
					issues[i].Message = title
				}
			}
		}

		// 最终兜底：message 仍为空时用 suggestion 填充
		if issues[i].Message == "" && issues[i].Suggestion != "" {
			issues[i].Message = issues[i].Suggestion
			issues[i].Suggestion = ""
		}
	}
}

// parseLocation 从 Claude 返回的 location 字符串中提取第一个 file:line。
// location 格式可能是：
//   - "path/to/File.java:42"
//   - "path/to/File.java:42; path/to/Other.java:10"（多位置分号分隔）
//   - "path/to/File.java"（无行号）
func parseLocation(loc string) (file string, line int) {
	// 取第一个分号前的部分
	if idx := strings.Index(loc, ";"); idx >= 0 {
		loc = strings.TrimSpace(loc[:idx])
	}
	// 尝试分离 file:line
	if lastColon := strings.LastIndex(loc, ":"); lastColon > 0 {
		lineStr := loc[lastColon+1:]
		if n, err := strconv.Atoi(strings.TrimSpace(lineStr)); err == nil {
			return loc[:lastColon], n
		}
	}
	return loc, 0
}

// resolveConfig 从 ConfigProvider 获取配置并转换为内部 ReviewConfig
func (s *Service) resolveConfig(repoFullName string) ReviewConfig {
	override := s.cfgProv.ResolveReviewConfig(repoFullName)

	cfg := ReviewConfig{
		Instructions:       override.Instructions,
		RepoInstructions:   override.RepoInstructions,
		Dimensions:         override.Dimensions,
		LargePRThreshold:   override.LargePRThreshold,
		TechStack:          override.TechStack,
		CodeStandardsPaths: override.CodeStandardsPaths,
		Severity:           override.Severity,      // M2.5 新增
		IgnorePatterns:     override.IgnorePatterns, // M2.5 新增
		Model:              override.Model,
		Effort:             override.Effort,
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
