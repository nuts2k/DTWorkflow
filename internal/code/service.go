package code

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// PRClient 创建 PR 的窄接口。
type PRClient interface {
	CreatePullRequest(ctx context.Context, owner, repo string, opt CreatePullRequestOption) (*PullRequest, error)
	ListRepoPullRequests(ctx context.Context, owner, repo string, opts ListPullRequestsOptions) ([]*PullRequest, error)
}

// CreatePullRequestOption PR 创建参数。
type CreatePullRequestOption struct {
	Title string
	Head  string
	Base  string
	Body  string
}

// ListPullRequestsOptions PR 列表查询参数。
type ListPullRequestsOptions struct {
	State string
	Head  string
}

// PullRequest 简化的 PR 结构。
type PullRequest struct {
	Number  int64
	HTMLURL string
}

// LabelClient 标签操作窄接口。
type LabelClient interface {
	AddLabel(ctx context.Context, owner, repo string, prNumber int64, label string) error
}

// CodeFromDocPoolRunner 容器执行窄接口。
type CodeFromDocPoolRunner interface {
	RunWithCommandAndStdin(ctx context.Context, payload model.TaskPayload,
		cmd []string, stdinData []byte) (*worker.ExecutionResult, error)
}

// ConfigProvider 配置读取窄接口。
type ConfigProvider interface {
	ResolveCodeFromDocEnabled(repoFullName string) bool
	ResolveCodeFromDocConfig(repoFullName string) CodeFromDocConfig
	ResolveAPIKey(taskType model.TaskType) string
}

// CodeFromDocConfig 传入 Service 的已解析配置。
type CodeFromDocConfig struct {
	Enabled         bool
	AutoIterate     bool
	MaxRetryRounds  int
	ReviewOnFailure bool
}

// ResultStore 结果持久化窄接口。
type ResultStore interface {
	SaveCodeFromDocResult(ctx context.Context, record *CodeFromDocResultRecord) error
	GetCodeFromDocResultByTaskID(ctx context.Context, taskID string) (*CodeFromDocResultRecord, error)
	UpdateCodeFromDocReviewEnqueued(ctx context.Context, taskID string) error
}

// ReviewEnqueuer code_from_doc 完成后触发 PR review 入队的窄接口。
type ReviewEnqueuer interface {
	EnqueueManualReview(ctx context.Context, payload model.TaskPayload, triggeredBy string) (string, error)
}

// CodeFromDocResultRecord 对应 code_from_doc_results 表的单行。
type CodeFromDocResultRecord struct {
	ID              string
	TaskID          string
	Repo            string
	Branch          string
	DocPath         string
	Success         bool
	PRNumber        int64
	PRURL           string
	FailureCategory string
	FailureReason   string
	FilesCreated    int
	FilesModified   int
	TestPassed      int
	TestFailed      int
	Implementation  string
	ReviewEnqueued  bool
}

// ServiceOption Service 可选配置。
type ServiceOption func(*Service)

// WithLabelClient 注入标签操作能力。
func WithLabelClient(c LabelClient) ServiceOption {
	return func(s *Service) { s.labelClient = c }
}

// WithResultStore 注入结果持久化能力。
func WithResultStore(st ResultStore) ServiceOption {
	return func(s *Service) { s.resultStore = st }
}

// WithReviewEnqueuer 注入 review 入队器。
func WithReviewEnqueuer(e ReviewEnqueuer) ServiceOption {
	return func(s *Service) { s.reviewEnqueuer = e }
}

// Service 文档驱动编码核心服务。
type Service struct {
	pool           CodeFromDocPoolRunner
	prClient       PRClient
	labelClient    LabelClient
	reviewEnqueuer ReviewEnqueuer
	cfgProv        ConfigProvider
	resultStore    ResultStore
	logger         *slog.Logger
}

// NewService 创建 Service 实例。
func NewService(pool CodeFromDocPoolRunner, prClient PRClient, cfgProv ConfigProvider, logger *slog.Logger, opts ...ServiceOption) *Service {
	if pool == nil {
		panic("code.NewService: pool 不能为 nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{
		pool:     pool,
		prClient: prClient,
		cfgProv:  cfgProv,
		logger:   logger,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Execute 执行 code_from_doc 任务。
func (s *Service) Execute(ctx context.Context, payload model.TaskPayload) (*CodeFromDocResult, error) {
	// 1. 配置检查
	if s.cfgProv != nil && !s.cfgProv.ResolveCodeFromDocEnabled(payload.RepoFullName) {
		return nil, ErrCodeFromDocDisabled
	}

	var cfg CodeFromDocConfig
	if s.cfgProv != nil {
		cfg = s.cfgProv.ResolveCodeFromDocConfig(payload.RepoFullName)
	}
	if cfg.MaxRetryRounds <= 0 {
		cfg.MaxRetryRounds = 3
	}

	// 2. 确定分支名
	branch := payload.HeadRef
	if branch == "" {
		branch = "auto-code/" + payload.DocSlug
	}

	// 3. 构建 prompt
	prompt := BuildCodeFromDocPrompt(PromptContext{
		Owner:          payload.RepoOwner,
		Repo:           payload.RepoName,
		Branch:         branch,
		BaseRef:        payload.BaseRef,
		DocPath:        payload.DocPath,
		MaxRetryRounds: cfg.MaxRetryRounds,
	})

	// 4. 容器执行
	cmd := []string{"claude", "-p", "--output-format", "json", "--dangerously-skip-permissions", "-"}
	execResult, err := s.pool.RunWithCommandAndStdin(ctx, payload, cmd, []byte(prompt))
	if err != nil {
		return &CodeFromDocResult{ExitCode: -1}, err
	}

	result := &CodeFromDocResult{
		RawOutput: execResult.Output,
		ExitCode:  execResult.ExitCode,
	}

	if execResult.ExitCode != 0 {
		return result, fmt.Errorf("容器退出码 %d: %s", execResult.ExitCode, execResult.Error)
	}

	// 5. 解析输出
	output, parseErr := ParseCodeFromDocOutput(execResult.Output)
	result.Output = output
	if parseErr != nil {
		return result, parseErr
	}

	// 6. 脱敏
	SanitizeCodeFromDocOutput(output)

	// 7. 按 FailureCategory 路由
	switch output.FailureCategory {
	case FailureCategoryInfoInsufficient:
		s.persistResult(ctx, payload, output, 0, "")
		return result, ErrInfoInsufficient
	case FailureCategoryInfrastructure:
		s.persistResult(ctx, payload, output, 0, "")
		return result, fmt.Errorf("code_from_doc infrastructure failure: %s", output.FailureReason)
	}

	// 8. 创建 PR（Success=true 或 test_failure 均创建）
	var prNumber int64
	var prURL string
	if output.BranchName != "" && s.prClient != nil {
		if output.BranchName != branch {
			return result, fmt.Errorf("%w: branch_name %q 与目标分支 %q 不一致",
				ErrCodeFromDocParseFailure, output.BranchName, branch)
		}
		var prErr error
		prNumber, prURL, prErr = s.createOrReusePR(ctx, payload, output, output.BranchName)
		if prErr != nil {
			return result, prErr
		}
		result.PRNumber = prNumber
		result.PRURL = prURL
	}

	// 9. auto-iterate 标签（仅 Success=true 时）
	if output.Success && cfg.AutoIterate && prNumber > 0 && s.labelClient != nil {
		if labelErr := s.labelClient.AddLabel(ctx, payload.RepoOwner, payload.RepoName, prNumber, "auto-iterate"); labelErr != nil {
			s.logger.WarnContext(ctx, "添加 auto-iterate 标签失败",
				"repo", payload.RepoFullName, "pr", prNumber, "error", labelErr)
		}
	}

	// 10. 持久化结果
	s.persistResult(ctx, payload, output, prNumber, prURL)

	// 10b. 主动入队 review：成功默认入队；test_failure 受 review_on_failure 控制。
	if prNumber > 0 && s.reviewEnqueuer != nil && (output.Success || (output.FailureCategory == FailureCategoryTestFailure && cfg.ReviewOnFailure)) {
		if s.shouldEnqueueReview(ctx, payload.TaskID, prNumber) {
			reviewPayload := model.TaskPayload{
				TaskType:     model.TaskTypeReviewPR,
				RepoOwner:    payload.RepoOwner,
				RepoName:     payload.RepoName,
				RepoFullName: payload.RepoFullName,
				CloneURL:     payload.CloneURL,
				PRNumber:     prNumber,
				PRTitle:      fmt.Sprintf("feat: 自动实现 %s", payload.DocPath),
				BaseRef:      payload.BaseRef,
				HeadRef:      branch,
				HeadSHA:      output.CommitSHA,
			}
			if _, enqErr := s.reviewEnqueuer.EnqueueManualReview(ctx, reviewPayload, "code_from_doc:"+payload.TaskID); enqErr != nil {
				s.logger.WarnContext(ctx, "code_from_doc 完成后入队 review 失败",
					"task_id", payload.TaskID, "repo", payload.RepoFullName,
					"pr_number", prNumber, "error", enqErr)
			} else {
				s.markReviewEnqueued(ctx, payload)
			}
		}
	}

	// 11. test_failure 场景：仍返回 sentinel error
	if output.FailureCategory == FailureCategoryTestFailure {
		return result, ErrTestFailure
	}

	return result, nil
}

// WriteDegraded 在解析失败且重试耗尽时的降级处理（当前为 no-op 占位）。
func (s *Service) WriteDegraded(ctx context.Context, payload model.TaskPayload, result *CodeFromDocResult) error {
	s.logger.WarnContext(ctx, "code_from_doc 降级：输出解析失败",
		"repo", payload.RepoFullName, "doc_path", payload.DocPath,
		"raw_len", len(result.RawOutput))
	return nil
}

func (s *Service) createOrReusePR(ctx context.Context, payload model.TaskPayload, output *CodeFromDocOutput, branch string) (int64, string, error) {
	if s.prClient == nil {
		return 0, "", nil
	}

	// 检查目标分支是否已有 open PR。自动派生分支和显式分支都必须复用。
	if branch != "" {
		existing, err := s.prClient.ListRepoPullRequests(ctx, payload.RepoOwner, payload.RepoName,
			ListPullRequestsOptions{State: "open", Head: branch})
		if err == nil && len(existing) > 0 {
			s.logger.InfoContext(ctx, "分支已有 open PR，跳过创建",
				"repo", payload.RepoFullName, "branch", branch, "pr", existing[0].Number)
			return existing[0].Number, existing[0].HTMLURL, nil
		}
	}

	// 创建 PR
	base := payload.BaseRef
	if base == "" {
		base = "main"
	}

	title := fmt.Sprintf("feat: 自动实现 %s", payload.DocPath)
	body := buildPRBody(output)

	pr, err := s.prClient.CreatePullRequest(ctx, payload.RepoOwner, payload.RepoName,
		CreatePullRequestOption{
			Title: title,
			Head:  branch,
			Base:  base,
			Body:  body,
		})
	if err != nil {
		s.logger.ErrorContext(ctx, "创建 PR 失败",
			"repo", payload.RepoFullName, "branch", branch, "error", err)
		return 0, "", fmt.Errorf("创建 code_from_doc PR 失败: %w", err)
	}
	return pr.Number, pr.HTMLURL, nil
}

func buildPRBody(output *CodeFromDocOutput) string {
	var sb strings.Builder
	sb.WriteString("## 自动实现摘要\n\n")
	if output.Implementation != "" {
		sb.WriteString(output.Implementation)
		sb.WriteString("\n\n")
	}
	if len(output.ModifiedFiles) > 0 {
		sb.WriteString("### 变更文件\n\n")
		for _, f := range output.ModifiedFiles {
			sb.WriteString(fmt.Sprintf("- `%s` (%s)\n", f.Path, f.Action))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("### 测试结果\n\n通过: %d / 失败: %d / 跳过: %d\n",
		output.TestResults.Passed, output.TestResults.Failed, output.TestResults.Skipped))
	return sb.String()
}

func (s *Service) persistResult(ctx context.Context, payload model.TaskPayload, output *CodeFromDocOutput, prNumber int64, prURL string) {
	if s.resultStore == nil {
		return
	}
	filesCreated, filesModified := countFileActions(output.ModifiedFiles)
	record := &CodeFromDocResultRecord{
		TaskID:          payload.TaskID,
		Repo:            payload.RepoFullName,
		Branch:          output.BranchName,
		DocPath:         payload.DocPath,
		Success:         output.Success,
		PRNumber:        prNumber,
		PRURL:           prURL,
		FailureCategory: string(output.FailureCategory),
		FailureReason:   output.FailureReason,
		FilesCreated:    filesCreated,
		FilesModified:   filesModified,
		TestPassed:      output.TestResults.Passed,
		TestFailed:      output.TestResults.Failed,
		Implementation:  output.Implementation,
	}
	if err := s.resultStore.SaveCodeFromDocResult(ctx, record); err != nil {
		s.logger.ErrorContext(ctx, "持久化 code_from_doc 结果失败",
			"task_id", payload.TaskID, "error", err)
	}
}

func (s *Service) shouldEnqueueReview(ctx context.Context, taskID string, prNumber int64) bool {
	if s.resultStore == nil || taskID == "" {
		return true
	}
	record, err := s.resultStore.GetCodeFromDocResultByTaskID(ctx, taskID)
	if err != nil {
		s.logger.WarnContext(ctx, "查询 code_from_doc review 入队状态失败，按未入队处理",
			"task_id", taskID, "error", err)
		return true
	}
	if record == nil {
		return true
	}
	return !(record.ReviewEnqueued && record.PRNumber == prNumber)
}

func (s *Service) markReviewEnqueued(ctx context.Context, payload model.TaskPayload) {
	if s.resultStore == nil || payload.TaskID == "" {
		return
	}
	if err := s.resultStore.UpdateCodeFromDocReviewEnqueued(ctx, payload.TaskID); err != nil {
		s.logger.WarnContext(ctx, "刷新 code_from_doc review_enqueued 失败，忽略",
			"task_id", payload.TaskID, "repo", payload.RepoFullName, "error", err)
	}
}

func countFileActions(files []ModifiedFile) (created, modified int) {
	for _, f := range files {
		switch f.Action {
		case "created":
			created++
		case "modified":
			modified++
		}
	}
	return
}

// DocSlug 从 doc_path 派生分支名后缀。
func DocSlug(docPath string) string {
	normalized := strings.ReplaceAll(docPath, "\\", "/")
	// 取 basename
	parts := strings.Split(normalized, "/")
	name := parts[len(parts)-1]
	// 去扩展名
	if idx := strings.LastIndex(name, "."); idx > 0 {
		name = name[:idx]
	}
	// 字符清洗
	re := regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	name = re.ReplaceAllString(name, "-")
	// 连续 - 合并
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	name = strings.Trim(name, "-")
	if name == "" {
		name = "doc"
	}
	sum := sha256.Sum256([]byte(normalized))
	suffix := fmt.Sprintf("%x", sum[:4])
	// 分支后缀总长控制在 50：basename + "-" + 8 位路径 hash。
	if len(name) > 41 {
		name = name[:41]
	}
	return name + "-" + suffix
}
