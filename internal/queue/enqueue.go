package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	e2esvc "otws19.zicp.vip/kelin/dtworkflow/internal/e2e"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/test"
	"otws19.zicp.vip/kelin/dtworkflow/internal/webhook"
)

// 编译时检查 *EnqueueHandler 实现 webhook.Handler 接口
var _ webhook.Handler = (*EnqueueHandler)(nil)

const (
	defaultIterationRoundWaitTimeout  = 10 * time.Second
	defaultIterationRoundPollInterval = 200 * time.Millisecond
)

// IterateSessionStore 迭代会话的窄 Store 接口。
type IterateSessionStore interface {
	FindActiveIterationSession(ctx context.Context, repoFullName string, prNumber int64) (*store.IterationSessionRecord, error)
	FindOrCreateIterationSession(ctx context.Context, repoFullName string, prNumber int64, headBranch string, maxRounds int) (*store.IterationSessionRecord, error)
	UpdateIterationSession(ctx context.Context, session *store.IterationSessionRecord) error
	CreateIterationRound(ctx context.Context, round *store.IterationRoundRecord) error
	UpdateIterationRound(ctx context.Context, round *store.IterationRoundRecord) error
	GetLatestRound(ctx context.Context, sessionID int64) (*store.IterationRoundRecord, error)
	GetIterationRound(ctx context.Context, sessionID int64, roundNumber int) (*store.IterationRoundRecord, error)
	CountNonRecoveryRounds(ctx context.Context, sessionID int64) (int, error)
	GetRecentRoundsIssuesFixed(ctx context.Context, sessionID int64, n int) ([]int, error)
	GetCompletedRoundsForSession(ctx context.Context, sessionID int64) ([]*store.IterationRoundRecord, error)
	FindActivePRTasksMulti(ctx context.Context, repoFullName string, prNumber int64, taskTypes []model.TaskType) ([]*model.TaskRecord, error)
}

type completedIterationRoundsLister interface {
	GetCompletedRoundsForSession(ctx context.Context, sessionID int64) ([]*store.IterationRoundRecord, error)
}

// IterateConfigProvider 迭代配置读取接口。
type IterateConfigProvider interface {
	ResolveIterateConfig(repoFullName string) config.IterateConfig
}

// IterateLabelManager Gitea 标签管理窄接口。
type IterateLabelManager interface {
	ListLabels(ctx context.Context, owner, repo string, prNumber int64) ([]string, error)
	AddLabel(ctx context.Context, owner, repo string, prNumber int64, label string) error
	RemoveLabel(ctx context.Context, owner, repo string, prNumber int64, label string) error
}

// IteratePRCommenter PR 评论窄接口。
type IteratePRCommenter interface {
	CreateComment(ctx context.Context, owner, repo string, prNumber int64, body string) error
}

// EnqueueHandler 实现 webhook.Handler 接口,将 webhook 事件转换为任务并入队
type EnqueueHandler struct {
	client            Enqueuer
	canceller         TaskCanceller // M2.4: 任务取消能力
	store             store.Store
	logger            *slog.Logger
	branchCleaner     BranchCleaner               // M4.2: 可选,Cancel-and-Replace 时清理旧 auto-test 分支
	prClient          genTestsPRClient            // M4.2.1: cleanupAllAutoTestBranches
	moduleScanner     test.RepoFileChecker        // M4.2.1: ScanRepoModules
	configProvider    ChangeDrivenConfigProvider  // M4.3: 变更驱动配置读取
	prFilesLister     PRFilesLister               // M4.3: PR 变更文件列表查询
	e2eModuleScanner  e2esvc.E2EModuleScanner     // M5.3: 可选，nil 时全量模式退化为单任务
	e2eConfigProvider E2ERegressionConfigProvider // M5.4: 可选，nil 时跳过 E2E 回归入队
	iterateStore      IterateSessionStore         // M6.1: 迭代会话 Store
	iterateCfg        IterateConfigProvider       // M6.1: 迭代配置
	iterateLabels     IterateLabelManager         // M6.1: Gitea 标签管理
	iterateCommenter  IteratePRCommenter          // M6.1: PR 评论
	iterateNotifier   TaskNotifier                // M6.1: 迭代通知发送
	roundWaitTimeout  time.Duration               // M6.1: 等待上一轮 fix_review 精确落库的最长时间
	roundPollInterval time.Duration               // M6.1: 轮询上一轮落库状态的间隔
}

// EnqueueOption EnqueueHandler 可选配置。
type EnqueueOption func(*EnqueueHandler)

// WithBranchCleaner 注入 BranchCleaner 用于 M4.2 gen_tests Cancel-and-Replace 阶段
// 清理旧 auto-test/{module} 远程分支。未注入时 EnqueueManualGenTests 会跳过清理。
func WithBranchCleaner(c BranchCleaner) EnqueueOption {
	return func(h *EnqueueHandler) {
		h.branchCleaner = c
	}
}

// EnqueuedTask 多模块入队结果条目。
type EnqueuedTask struct {
	TaskID    string
	Module    string
	Framework string
}

// genTestsPRClient 全量清理所需的 PR 操作窄接口。
type genTestsPRClient interface {
	ListRepoPullRequests(ctx context.Context, owner, repo string,
		opts gitea.ListPullRequestsOptions) ([]*gitea.PullRequest, *gitea.Response, error)
	ClosePullRequest(ctx context.Context, owner, repo string, index int64) error
}

// WithPRClient 注入 genTestsPRClient 用于 M4.2.1 全量清理 auto-test 分支。
func WithPRClient(c genTestsPRClient) EnqueueOption {
	return func(h *EnqueueHandler) { h.prClient = c }
}

// WithModuleScanner 注入 RepoFileChecker 用于 M4.2.1 ScanRepoModules。
func WithModuleScanner(c test.RepoFileChecker) EnqueueOption {
	return func(h *EnqueueHandler) { h.moduleScanner = c }
}

// ChangeDrivenConfigProvider 变更驱动配置读取窄接口。
type ChangeDrivenConfigProvider interface {
	ResolveTestGenConfig(repoFullName string) config.TestGenOverride
}

// WithConfigProvider 注入变更驱动配置读取能力。
func WithConfigProvider(p ChangeDrivenConfigProvider) EnqueueOption {
	return func(h *EnqueueHandler) { h.configProvider = p }
}

// PRFilesLister 列出 PR 变更文件的窄接口。
type PRFilesLister interface {
	ListPullRequestFiles(ctx context.Context, owner, repo string, index int64, opts gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error)
}

// WithPRFilesLister 注入 PR 文件列表查询能力。
func WithPRFilesLister(lister PRFilesLister) EnqueueOption {
	return func(h *EnqueueHandler) { h.prFilesLister = lister }
}

// WithE2EModuleScanner 注入 E2EModuleScanner 用于 M5.3 全量模式模块发现。
func WithE2EModuleScanner(s e2esvc.E2EModuleScanner) EnqueueOption {
	return func(h *EnqueueHandler) { h.e2eModuleScanner = s }
}

// E2ERegressionConfigProvider E2E 回归配置读取窄接口。
type E2ERegressionConfigProvider interface {
	ResolveE2EConfig(repoFullName string) config.E2EOverride
}

// WithE2ERegressionConfigProvider 注入 E2E 回归配置读取能力。
func WithE2ERegressionConfigProvider(p E2ERegressionConfigProvider) EnqueueOption {
	return func(h *EnqueueHandler) { h.e2eConfigProvider = p }
}

// WithIterateStore 注入迭代会话 Store。
func WithIterateStore(s IterateSessionStore) EnqueueOption {
	return func(h *EnqueueHandler) { h.iterateStore = s }
}

// WithIterateConfig 注入迭代配置读取能力。
func WithIterateConfig(c IterateConfigProvider) EnqueueOption {
	return func(h *EnqueueHandler) { h.iterateCfg = c }
}

// WithIterateLabels 注入 Gitea 标签管理能力。
func WithIterateLabels(l IterateLabelManager) EnqueueOption {
	return func(h *EnqueueHandler) { h.iterateLabels = l }
}

// WithIterateCommenter 注入 PR 评论能力。
func WithIterateCommenter(c IteratePRCommenter) EnqueueOption {
	return func(h *EnqueueHandler) { h.iterateCommenter = c }
}

// WithIterateNotifier 注入迭代通知发送器。
func WithIterateNotifier(n TaskNotifier) EnqueueOption {
	return func(h *EnqueueHandler) { h.iterateNotifier = n }
}

// NewEnqueueHandler 创建 EnqueueHandler 实例。
// 参数 client 和 store 为必要依赖,传入 nil 属于编程错误（programming error）,
// 因此使用 panic 而非返回 error,与 Go 标准库（如 http.NewServeMux）的惯例一致。
// canceller 为可选依赖,nil 时跳过取消逻辑。
func NewEnqueueHandler(client Enqueuer, canceller TaskCanceller, store store.Store, logger *slog.Logger, opts ...EnqueueOption) *EnqueueHandler {
	if client == nil {
		panic("NewEnqueueHandler: client 不能为 nil")
	}
	if store == nil {
		panic("NewEnqueueHandler: store 不能为 nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	h := &EnqueueHandler{
		client:            client,
		canceller:         canceller,
		store:             store,
		logger:            logger,
		roundWaitTimeout:  defaultIterationRoundWaitTimeout,
		roundPollInterval: defaultIterationRoundPollInterval,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// HandlePullRequest 处理 PR 事件，按 action 路由到评审或合并后处理。
func (h *EnqueueHandler) HandlePullRequest(ctx context.Context, event webhook.PullRequestEvent) error {
	switch event.Action {
	case "opened", "synchronized", "reopened":
		return h.handleReviewPullRequest(ctx, event)
	case "merged":
		return h.handleMergedPullRequest(ctx, event)
	default:
		return nil
	}
}

// handleReviewPullRequest 处理 PR 评审事件,执行幂等检查后创建任务并入队。
func (h *EnqueueHandler) handleReviewPullRequest(ctx context.Context, event webhook.PullRequestEvent) error {
	// M4.2: 拦截来自 auto-test/{moduleKey} 分支的 PR webhook——这些 PR 由 gen_tests
	// 自己产出,上游已发过 gen_tests 完成通知,不应再为其触发自动评审。
	// 仅在命中活跃 gen_tests 任务的 module 时拦截；查询失败 fail-open,继续原流程
	// 保证评审能力不被 gen_tests 拖垮。
	if h.shouldSkipAutoTestReview(ctx, event) {
		return nil
	}

	// 幂等检查：相同 delivery_id + task_type 不重复创建
	existing, err := h.store.FindByDeliveryID(ctx, event.DeliveryID, model.TaskTypeReviewPR)
	if err != nil {
		return fmt.Errorf("幂等检查失败: %w", err)
	}
	if existing != nil {
		h.logger.InfoContext(ctx, "PR 评审任务已存在,跳过",
			"delivery_id", event.DeliveryID,
			"task_id", existing.ID,
			"status", existing.Status,
		)
		return nil
	}

	// M2.4: 先识别被替代的旧任务,等新任务持久化成功后再取消旧任务,
	// 避免 replacement 创建失败时把当前唯一可运行任务也一起取消掉。
	activeTasks, superseded := h.listActivePRTasks(ctx, event.Repository.FullName, event.PullRequest.Number)

	payload := model.TaskPayload{
		TaskType:        model.TaskTypeReviewPR,
		DeliveryID:      event.DeliveryID,
		RepoOwner:       event.Repository.Owner,
		RepoName:        event.Repository.Name,
		RepoFullName:    event.Repository.FullName,
		CloneURL:        event.Repository.CloneURL,
		PRNumber:        event.PullRequest.Number,
		PRTitle:         event.PullRequest.Title,
		BaseRef:         event.PullRequest.BaseRef,
		HeadRef:         event.PullRequest.HeadRef,
		HeadSHA:         event.PullRequest.HeadSHA,
		SupersededCount: superseded.Count,
		PreviousHeadSHA: superseded.LastHeadSHA,
	}

	if payload.RepoFullName == "" || payload.CloneURL == "" {
		return fmt.Errorf("webhook 数据不完整: RepoFullName 或 CloneURL 为空")
	}

	record := &model.TaskRecord{
		TaskType:     model.TaskTypeReviewPR,
		Priority:     model.PriorityHigh,
		RepoFullName: event.Repository.FullName,
		PRNumber:     event.PullRequest.Number,
		DeliveryID:   event.DeliveryID,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return err
	}

	// M6.1: 区分 fix_review push 与用户 push，避免 Cancel-and-Replace 误杀 fix_review 任务
	if h.iterateStore != nil {
		session, findErr := h.iterateStore.FindActiveIterationSession(ctx, event.Repository.FullName, event.PullRequest.Number)
		if findErr != nil {
			h.logger.WarnContext(ctx, "查询迭代会话失败，按用户 push 处理",
				"error", findErr)
			h.cancelTasks(ctx, activeTasks)
		} else if session != nil && session.Status == "fixing" && h.isFixReviewPush(event) {
			// fix_review 自身的 push：仅取消旧 review_pr，不取消 fix_review
			h.cancelTasks(ctx, filterTasksByType(activeTasks, model.TaskTypeReviewPR))
			// 状态仍保持 fixing，等待 fix_review 任务完成后由 Processor 精确落库并切换为 reviewing。
			// 这避免 review_pr 过快完成时，在上一轮修复结果尚未持久化前创建下一轮。
		} else if session != nil {
			// 用户手动 push 且存在迭代会话：同时取消 review_pr 和 fix_review
			fixTasks, fixErr := h.iterateStore.FindActivePRTasksMulti(ctx,
				event.Repository.FullName, event.PullRequest.Number,
				[]model.TaskType{model.TaskTypeFixReview})
			if fixErr != nil {
				h.logger.WarnContext(ctx, "查询活跃 fix_review 任务失败，无法完整取消旧迭代修复",
					"repo", event.Repository.FullName,
					"pr", event.PullRequest.Number,
					"error", fixErr)
			}
			h.cancelTasks(ctx, append(activeTasks, fixTasks...))
		} else {
			// 无迭代会话：正常取消旧 review_pr
			h.cancelTasks(ctx, activeTasks)
		}
	} else {
		h.cancelTasks(ctx, activeTasks)
	}

	if record.Status == model.TaskStatusQueued {
		h.logger.InfoContext(ctx, "PR 评审任务已入队",
			"task_id", record.ID,
			"asynq_id", record.AsynqID,
			"repo", event.Repository.FullName,
			"pr", event.PullRequest.Number,
			"superseded", superseded.Count,
		)
	} else {
		h.logger.InfoContext(ctx, "PR 评审任务已创建（pending）,等待 RecoveryLoop 入队",
			"task_id", record.ID,
			"repo", event.Repository.FullName,
			"pr", event.PullRequest.Number,
		)
	}
	return nil
}

func (h *EnqueueHandler) isFixReviewPush(event webhook.PullRequestEvent) bool {
	if h.iterateCfg == nil {
		h.logger.WarnContext(context.Background(), "iterate 配置未注入，无法可信识别 fix_review push；按用户 push 处理",
			"repo", event.Repository.FullName)
		return false
	}
	cfg := h.iterateCfg.ResolveIterateConfig(event.Repository.FullName)
	botLogin := strings.TrimSpace(cfg.BotLogin)
	if botLogin == "" {
		h.logger.WarnContext(context.Background(), "iterate.bot_login 未配置，无法可信识别 fix_review push；按用户 push 处理",
			"repo", event.Repository.FullName)
		return false
	}
	return strings.EqualFold(event.Sender.Login, botLogin)
}

// handleMergedPullRequest 处理 PR 合并事件，调度变更驱动 gen_tests 和 E2E 回归。
// Bot PR（auto-test/* / auto-fix/*）在此层统一过滤，子处理函数不再重复检查。
func (h *EnqueueHandler) handleMergedPullRequest(ctx context.Context, event webhook.PullRequestEvent) error {
	pr := event.PullRequest

	// Bot PR 过滤：防自触发循环
	if strings.HasPrefix(pr.HeadRef, "auto-test/") || strings.HasPrefix(pr.HeadRef, "auto-fix/") || strings.HasPrefix(pr.HeadRef, "auto-code/") {
		h.logger.DebugContext(ctx, "merged: skipping bot PR",
			"repo", event.Repository.FullName, "head_ref", pr.HeadRef)
		return nil
	}

	// 变更驱动 gen_tests（M4.3）
	genErr := h.handleMergedGenTests(ctx, event)

	// E2E 回归入队（M5.4）— 独立于 gen_tests，失败不阻断彼此
	h.handleMergedE2ERegression(ctx, event)

	return genErr
}

// handleMergedGenTests 处理 PR 合并事件，按变更驱动策略入队 gen_tests。
// 调用方已完成 Bot PR 过滤，此函数不再重复检查。
func (h *EnqueueHandler) handleMergedGenTests(ctx context.Context, event webhook.PullRequestEvent) error {
	repo := event.Repository.FullName
	pr := event.PullRequest

	// 1. 配置检查
	if h.configProvider == nil {
		return nil
	}
	tgCfg := h.configProvider.ResolveTestGenConfig(repo)
	if tgCfg.Enabled != nil && !*tgCfg.Enabled {
		h.logger.DebugContext(ctx, "change-driven: test_gen disabled for repo", "repo", repo)
		return nil
	}
	changeDriven := resolveChangeDrivenConfig(tgCfg)
	if !changeDriven.IsEnabled() {
		h.logger.DebugContext(ctx, "change-driven: disabled for repo", "repo", repo)
		return nil
	}

	// 2. 获取变更文件
	if h.prFilesLister == nil {
		return nil
	}
	changedFiles, err := h.listAllPullRequestFiles(ctx, event.Repository.Owner, event.Repository.Name, pr.Number)
	if err != nil {
		return fmt.Errorf("list PR files: %w", err)
	}

	// 3. 预过滤
	filenames := extractFilenames(changedFiles)
	sourceFiles := filterSourceFiles(filenames, changeDriven.IgnorePaths)
	if len(sourceFiles) == 0 {
		h.logger.DebugContext(ctx, "change-driven: no source files after filtering",
			"repo", repo, "pr", pr.Number, "total_files", len(filenames))
		return nil
	}

	// 4. 模块扫描
	if h.moduleScanner == nil {
		return nil
	}
	modules, err := test.ScanRepoModules(ctx, h.moduleScanner,
		event.Repository.Owner, event.Repository.Name, pr.BaseRef)
	if err != nil {
		if errors.Is(err, test.ErrNoFrameworkDetected) {
			h.logger.DebugContext(ctx, "change-driven: no framework detected", "repo", repo)
			return nil
		}
		return fmt.Errorf("scan modules: %w", err)
	}

	// 5. 文件→模块归组
	moduleFiles := matchFilesToModules(sourceFiles, modules)
	if len(moduleFiles) == 0 {
		h.logger.DebugContext(ctx, "change-driven: no modules matched",
			"repo", repo, "source_files", len(sourceFiles))
		return nil
	}

	// 6. 逐模块入队
	triggeredBy := fmt.Sprintf("webhook:pr_merged:%d", pr.Number)
	h.logger.InfoContext(ctx, "change-driven: enqueueing gen_tests",
		"repo", repo, "pr", pr.Number,
		"source_files", len(sourceFiles),
		"modules", len(moduleFiles),
		"triggered_by", triggeredBy)

	successes := 0
	failures := 0
	for _, group := range moduleFiles {
		payload := model.TaskPayload{
			TaskType:     model.TaskTypeGenTests,
			RepoOwner:    event.Repository.Owner,
			RepoName:     event.Repository.Name,
			RepoFullName: repo,
			CloneURL:     event.Repository.CloneURL,
			Module:       group.Module.Path,
			Framework:    string(group.Module.Framework),
			BaseRef:      pr.BaseRef,
			ChangedFiles: group.Files,
		}
		deliveryID := buildChangeDrivenDeliveryID(event.DeliveryID, pr.Number, group.Module.Path, string(group.Module.Framework))
		if _, err := h.enqueueChangeDrivenGenTests(ctx, payload, triggeredBy, deliveryID); err != nil {
			h.logger.WarnContext(ctx, "change-driven: failed to enqueue module",
				"repo", repo, "module", group.Module.Path, "error", err)
			failures++
			continue
		}
		successes++
	}
	// 与 handleReviewPullRequest（吞掉 enqueue 失败以避免 webhook 重试）不同，
	// merged 路径选择传播错误：部分失败时返回 error 可触发 Gitea webhook 重试，
	// 已成功的模块由 buildChangeDrivenDeliveryID 幂等保护不会重复入队。
	if successes == 0 {
		return fmt.Errorf("change-driven: 所有模块入队均失败: repo=%s pr=%d failures=%d", repo, pr.Number, failures)
	}
	if failures > 0 {
		return fmt.Errorf("change-driven: 部分模块入队失败: repo=%s pr=%d successes=%d failures=%d", repo, pr.Number, successes, failures)
	}
	return nil
}

// handleMergedE2ERegression 处理 PR 合并事件，按 E2E 回归策略入队 triage_e2e。
// 调用方已完成 Bot PR 过滤，此函数不再重复检查。
// 失败仅记 warn 日志，不返回 error，不阻断 gen_tests 主流程。
func (h *EnqueueHandler) handleMergedE2ERegression(ctx context.Context, event webhook.PullRequestEvent) {
	repo := event.Repository.FullName
	pr := event.PullRequest

	// 1. 配置检查
	if h.e2eConfigProvider == nil {
		return
	}
	e2eCfg := h.e2eConfigProvider.ResolveE2EConfig(repo)
	if e2eCfg.Enabled != nil && !*e2eCfg.Enabled {
		h.logger.DebugContext(ctx, "e2e-regression: e2e disabled for repo", "repo", repo)
		return
	}
	if e2eCfg.Regression == nil || !e2eCfg.Regression.IsEnabled() {
		return
	}

	// 2. 宿主侧初筛
	if h.prFilesLister == nil {
		return
	}
	allFiles, err := h.listAllPullRequestFiles(ctx, event.Repository.Owner, event.Repository.Name, pr.Number)
	if err != nil {
		h.logger.WarnContext(ctx, "e2e-regression: 获取 PR 文件失败",
			"repo", repo, "pr", pr.Number, "error", err)
		return
	}
	filenames := extractRegressionFilenames(allFiles)
	regressionFiles := filterRegressionFiles(filenames, e2eCfg.Regression.IgnorePaths)
	if len(regressionFiles) == 0 {
		h.logger.DebugContext(ctx, "e2e-regression: 无需回归的变更",
			"repo", repo, "pr", pr.Number)
		return
	}

	// 3. 构建 payload 入队 triage_e2e
	deliveryID := buildE2ERegressionDeliveryID(event.DeliveryID)

	// 幂等检查
	if deliveryID != "" {
		existing, findErr := h.store.FindByDeliveryID(ctx, deliveryID, model.TaskTypeTriageE2E)
		if findErr != nil {
			h.logger.WarnContext(ctx, "e2e-regression: 幂等检查失败",
				"repo", repo, "delivery_id", deliveryID, "error", findErr)
			return
		}
		if existing != nil {
			h.logger.InfoContext(ctx, "e2e-regression: triage_e2e 任务已存在,跳过",
				"repo", repo, "delivery_id", deliveryID,
				"task_id", existing.ID, "status", existing.Status)
			return
		}
	}

	payload := model.TaskPayload{
		TaskType:       model.TaskTypeTriageE2E,
		DeliveryID:     deliveryID,
		RepoOwner:      event.Repository.Owner,
		RepoName:       event.Repository.Name,
		RepoFullName:   repo,
		CloneURL:       event.Repository.CloneURL,
		PRNumber:       pr.Number,
		PRTitle:        pr.Title,
		BaseRef:        pr.BaseRef,
		BaseSHA:        pr.BaseSHA,
		HeadSHA:        pr.HeadSHA,
		MergeCommitSHA: pr.MergeCommitSHA,
		Environment:    e2eCfg.DefaultEnv,
		ChangedFiles:   regressionFiles,
	}

	// Cancel-and-Replace：取消同仓库旧 triage
	oldTasks, _ := h.store.FindActiveTasksByModule(ctx, repo, "", model.TaskTypeTriageE2E)

	triggeredBy := fmt.Sprintf("webhook:pr_merged:%d", pr.Number)
	record := &model.TaskRecord{
		TaskType:     model.TaskTypeTriageE2E,
		Priority:     model.PriorityNormal,
		RepoFullName: repo,
		DeliveryID:   deliveryID,
		TriggeredBy:  triggeredBy,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		h.logger.ErrorContext(ctx, "e2e-regression: 入队失败，该 webhook 的 triage 触发将丢失",
			"repo", repo, "pr", pr.Number, "error", err)
		return
	}

	h.cancelTasks(ctx, oldTasks)
	h.logger.InfoContext(ctx, "e2e-regression: triage_e2e 已入队",
		"repo", repo, "pr", pr.Number,
		"task_id", record.ID, "regression_files", len(regressionFiles))
}

// buildE2ERegressionDeliveryID 构建 E2E 回归复合 delivery ID。
func buildE2ERegressionDeliveryID(webhookDeliveryID string) string {
	if webhookDeliveryID == "" {
		return ""
	}
	return webhookDeliveryID + ":triage_e2e"
}

func (h *EnqueueHandler) listAllPullRequestFiles(ctx context.Context, owner, repo string, prNumber int64) ([]*gitea.ChangedFile, error) {
	const (
		pageSize = 100
		maxPages = 20
	)
	files, truncated, err := gitea.PaginateAll(ctx, pageSize, maxPages,
		func(ctx context.Context, page, pageSize int) ([]*gitea.ChangedFile, *gitea.Response, error) {
			return h.prFilesLister.ListPullRequestFiles(ctx, owner, repo, prNumber, gitea.ListOptions{
				Page: page, PageSize: pageSize,
			})
		})
	if err != nil {
		return nil, err
	}
	if truncated {
		h.logger.WarnContext(ctx, "change-driven: PR file list may be truncated",
			"owner", owner, "repo", repo, "pr", prNumber, "files", len(files),
			"page_size", pageSize, "max_pages", maxPages)
	}
	return files, nil
}

// resolveChangeDrivenConfig 从合并后的 TestGenOverride 中提取 ChangeDrivenConfig。
func resolveChangeDrivenConfig(tgCfg config.TestGenOverride) config.ChangeDrivenConfig {
	if tgCfg.ChangeDriven == nil {
		return config.ChangeDrivenConfig{}
	}
	return *tgCfg.ChangeDriven
}

// buildChangeDrivenDeliveryID 构建复合 delivery ID，实现 webhook 重放幂等：
// 同一 webhook + 同一模块/框架组合只会入队一次，无需额外 store 查询。
func buildChangeDrivenDeliveryID(webhookDeliveryID string, prNumber int64, module, framework string) string {
	moduleKey := test.ModuleKey(module)
	if framework != "" {
		moduleKey += "-" + framework
	}
	if webhookDeliveryID != "" {
		return fmt.Sprintf("%s:gen_tests:%s", webhookDeliveryID, moduleKey)
	}
	return fmt.Sprintf("pr-merged-%d:gen_tests:%s", prNumber, moduleKey)
}

func (h *EnqueueHandler) enqueueChangeDrivenGenTests(ctx context.Context, payload model.TaskPayload, triggeredBy, deliveryID string) (EnqueuedTask, error) {
	if deliveryID != "" {
		existing, err := h.store.FindByDeliveryID(ctx, deliveryID, model.TaskTypeGenTests)
		if err != nil {
			return EnqueuedTask{}, fmt.Errorf("change-driven 幂等检查失败: %w", err)
		}
		if existing != nil {
			h.logger.InfoContext(ctx, "change-driven gen_tests 任务已存在,跳过",
				"delivery_id", deliveryID,
				"task_id", existing.ID,
				"status", existing.Status,
				"module", payload.Module,
				"framework", payload.Framework,
			)
			return EnqueuedTask{TaskID: existing.ID, Module: payload.Module, Framework: payload.Framework}, nil
		}
	}
	return h.enqueueGenTestsWithDeliveryID(ctx, payload, triggeredBy, deliveryID)
}

// HandleIssueLabel 处理 Issue 标签事件,按标签类型路由到不同任务。
// M3.4: fix-to-pr 标签 → TaskTypeFixIssue；auto-fix 标签 → TaskTypeAnalyzeIssue。
// fix-to-pr 优先级高于 auto-fix（同时存在时仅入队 fix_issue）。
// 流程结构与 HandlePullRequest 类似,参见其注释了解未提取模板方法的原因。
func (h *EnqueueHandler) HandleIssueLabel(ctx context.Context, event webhook.IssueLabelEvent) error {
	// M3.4: 双标签分流,fix-to-pr 优先于 auto-fix
	var taskType model.TaskType
	switch {
	case event.FixToPRAdded:
		taskType = model.TaskTypeFixIssue
	case event.AutoFixAdded:
		taskType = model.TaskTypeAnalyzeIssue
	default:
		return nil
	}

	// 幂等检查（按各自 TaskType 独立查询）
	existing, err := h.store.FindByDeliveryID(ctx, event.DeliveryID, taskType)
	if err != nil {
		return fmt.Errorf("幂等检查失败: %w", err)
	}
	if existing != nil {
		h.logger.InfoContext(ctx, "Issue 任务已存在,跳过",
			"delivery_id", event.DeliveryID,
			"task_id", existing.ID,
			"task_type", taskType,
			"status", existing.Status,
		)
		return nil
	}

	payload := model.TaskPayload{
		TaskType:     taskType,
		DeliveryID:   event.DeliveryID,
		RepoOwner:    event.Repository.Owner,
		RepoName:     event.Repository.Name,
		RepoFullName: event.Repository.FullName,
		CloneURL:     event.Repository.CloneURL,
		IssueNumber:  event.Issue.Number,
		IssueTitle:   event.Issue.Title,
		IssueRef:     event.Issue.Ref,
	}

	if payload.RepoFullName == "" || payload.CloneURL == "" {
		return fmt.Errorf("webhook 数据不完整: RepoFullName 或 CloneURL 为空")
	}

	record := &model.TaskRecord{
		TaskType:     taskType,
		Priority:     model.PriorityNormal,
		RepoFullName: event.Repository.FullName,
		DeliveryID:   event.DeliveryID,
	}

	activeTasks := h.listReplacementIssueTasks(ctx, event.Repository.FullName, event.Issue.Number, taskType)

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return err
	}
	h.cancelTasks(ctx, activeTasks)

	taskTypeName := "Issue 分析"
	if taskType == model.TaskTypeFixIssue {
		taskTypeName = "Issue 修复"
	}
	if record.Status == model.TaskStatusQueued {
		h.logger.InfoContext(ctx, taskTypeName+"任务已入队",
			"task_id", record.ID,
			"asynq_id", record.AsynqID,
			"task_type", taskType,
			"repo", event.Repository.FullName,
			"issue", event.Issue.Number,
		)
	} else {
		h.logger.InfoContext(ctx, taskTypeName+"任务已创建（pending）,等待 RecoveryLoop 入队",
			"task_id", record.ID,
			"task_type", taskType,
			"repo", event.Repository.FullName,
			"issue", event.Issue.Number,
		)
	}
	return nil
}

// buildAsynqTaskID 根据 deliveryID 和 taskType 构建确定性的 asynq TaskID。
// 当 deliveryID 非空时,使用 "deliveryID:taskType" 格式,保证 asynq 层面的幂等去重；
// 当 deliveryID 为空时返回空字符串,让 asynq 自动生成 TaskID。
//
// 此函数被 enqueueTask 和 RecoveryLoop.requeue 共享,确保同一任务无论首次入队
// 还是恢复重入队都使用相同的 TaskID,避免因 TaskID 不一致导致任务重复执行。
func buildAsynqTaskID(deliveryID string, taskType model.TaskType) string {
	if deliveryID != "" {
		return fmt.Sprintf("%s:%s", deliveryID, taskType)
	}
	return "" // 让 asynq 自动生成
}

// autoTestBranchPrefix 标识由 gen_tests 产出的稳定工作分支前缀；
// 与 test.BuildAutoTestBranchName 保持一致（auto-test/{moduleKey}）。
const autoTestBranchPrefix = "auto-test/"

// shouldSkipAutoTestReview 判断 PR webhook 是否来自 gen_tests 自己产出的
// auto-test/{moduleKey} 分支；若同仓库当前仍有该 module 的活跃 gen_tests 任务,
// 则拦截评审入队。其它情况（非 auto-test/* 前缀、查询失败等）均 fail-open。
//
// M4.2.1：branchKey 采用前缀匹配（candidateKey == branchKey 或 branchKey 以
// candidateKey+"-" 开头），正确处理 framework 后缀分支，如 auto-test/all-junit5
// 会被活跃 module=""（candidateKey="all"）命中。
func (h *EnqueueHandler) shouldSkipAutoTestReview(ctx context.Context, event webhook.PullRequestEvent) bool {
	head := event.PullRequest.HeadRef
	if !strings.HasPrefix(head, autoTestBranchPrefix) {
		return false
	}
	branchKey := strings.TrimPrefix(head, autoTestBranchPrefix)
	if branchKey == "" {
		return false
	}

	candidates, err := h.store.ListActiveGenTestsModules(ctx, event.Repository.FullName)
	if err != nil {
		// fail-open：查询失败时不阻断评审,只记日志。按 §4.5 要求,字段覆盖
		// repo / module_key / pr / head / delivery_id / error 全量,便于排障。
		h.logger.WarnContext(ctx, "查询活跃 gen_tests module 失败，auto-test PR 仍按评审流程放行",
			"repo", event.Repository.FullName, "module_key", branchKey,
			"pr", event.PullRequest.Number, "head", head,
			"delivery_id", event.DeliveryID, "error", err)
		return false
	}
	for _, candidate := range candidates {
		candidateKey := test.ModuleKey(candidate)
		if candidateKey == branchKey || strings.HasPrefix(branchKey, candidateKey+"-") {
			h.logger.InfoContext(ctx, "拦截 auto-test PR 评审入队：存在活跃 gen_tests 任务",
				"repo", event.Repository.FullName, "module_key", branchKey,
				"pr", event.PullRequest.Number, "head", head,
				"delivery_id", event.DeliveryID)
			return true
		}
	}
	return false
}

// SupersededInfo 取消旧任务后的汇总信息
type SupersededInfo struct {
	Count       int    // 取消的旧任务数量
	LastHeadSHA string // 最近一个旧任务的 HeadSHA
}

// listActivePRTasks 查找同一 PR 的活跃旧评审任务,并汇总 superseded 信息。
func (h *EnqueueHandler) listActivePRTasks(ctx context.Context, repoFullName string, prNumber int64) ([]*model.TaskRecord, SupersededInfo) {
	tasks, err := h.store.FindActivePRTasks(ctx, repoFullName, prNumber, model.TaskTypeReviewPR)
	if err != nil {
		h.logger.WarnContext(ctx, "查找活跃旧任务失败,跳过取消",
			"repo", repoFullName, "pr", prNumber, "error", err)
		return nil, SupersededInfo{}
	}
	if len(tasks) == 0 {
		return nil, SupersededInfo{}
	}

	info := SupersededInfo{Count: len(tasks)}
	for _, task := range tasks {
		if task.Payload.HeadSHA != "" {
			info.LastHeadSHA = task.Payload.HeadSHA
		}
	}
	return tasks, info
}

// listActiveIssueTasks 查找同一 Issue 的活跃任务。
func (h *EnqueueHandler) listActiveIssueTasks(ctx context.Context, repoFullName string, issueNumber int64, taskType model.TaskType) []*model.TaskRecord {
	tasks, err := h.store.FindActiveIssueTasks(ctx, repoFullName, issueNumber, taskType)
	if err != nil {
		h.logger.WarnContext(ctx, "查找活跃 Issue 任务失败,跳过取消",
			"repo", repoFullName, "issue", issueNumber, "error", err)
		return nil
	}
	return tasks
}

// listReplacementIssueTasks 查找新任务创建后需要取消的旧任务。
// analyze_issue 仅替换旧 analyze_issue；fix_issue 会同时替换旧 fix_issue 和 analyze_issue,
// 确保 fix-to-pr 升级为修复时不会与前序分析任务并发运行。
func (h *EnqueueHandler) listReplacementIssueTasks(ctx context.Context, repoFullName string, issueNumber int64, taskType model.TaskType) []*model.TaskRecord {
	tasks := h.listActiveIssueTasks(ctx, repoFullName, issueNumber, taskType)
	if taskType != model.TaskTypeFixIssue {
		return tasks
	}

	analyzeTasks := h.listActiveIssueTasks(ctx, repoFullName, issueNumber, model.TaskTypeAnalyzeIssue)
	if len(analyzeTasks) == 0 {
		return tasks
	}

	merged := make([]*model.TaskRecord, 0, len(tasks)+len(analyzeTasks))
	seen := make(map[string]struct{}, len(tasks)+len(analyzeTasks))
	for _, task := range append(tasks, analyzeTasks...) {
		if task == nil {
			continue
		}
		if _, ok := seen[task.ID]; ok {
			continue
		}
		seen[task.ID] = struct{}{}
		merged = append(merged, task)
	}
	return merged
}

// cancelTasks 在 replacement 已持久化后,逐个取消旧任务（best-effort）。
func (h *EnqueueHandler) cancelTasks(ctx context.Context, tasks []*model.TaskRecord) {
	var failCount int
	for _, task := range tasks {
		if !h.cancelTask(ctx, task) {
			failCount++
		}
	}
	if failCount > 0 {
		h.logger.WarnContext(ctx, "部分旧任务取消失败,可能存在并行评审",
			"total", len(tasks), "failed", failCount)
	}
}

// cancelTask 取消单个旧评审任务（best-effort）
func (h *EnqueueHandler) cancelTask(ctx context.Context, task *model.TaskRecord) bool {
	prevStatus := task.Status

	if task.AsynqID != "" {
		if h.canceller == nil {
			h.logger.WarnContext(ctx, "缺少任务取消器,保留旧任务为可运行状态",
				"task_id", task.ID, "asynq_id", task.AsynqID, "status", task.Status)
			return false
		}
		switch task.Status {
		case model.TaskStatusPending, model.TaskStatusQueued:
			queueName := PriorityToQueue(task.Priority)
			if err := h.canceller.Delete(ctx, queueName, task.AsynqID); err != nil {
				h.logger.WarnContext(ctx, "从 asynq 删除任务失败",
					"task_id", task.ID, "asynq_id", task.AsynqID, "error", err)
				return false
			}
		case model.TaskStatusRunning:
			if err := h.canceller.CancelProcessing(ctx, task.AsynqID); err != nil {
				h.logger.WarnContext(ctx, "取消运行中任务失败",
					"task_id", task.ID, "asynq_id", task.AsynqID, "error", err)
				return false
			}
		}
	}

	now := time.Now()
	task.Status = model.TaskStatusCancelled
	task.Error = "被同一 PR 的新评审任务取代"
	task.UpdatedAt = now
	task.CompletedAt = &now
	if err := h.store.UpdateTask(ctx, task); err != nil {
		h.logger.WarnContext(ctx, "更新旧任务状态为 cancelled 失败（asynq 已操作但 SQLite 更新失败）",
			"task_id", task.ID, "error", err)
		return false
	}
	h.logger.InfoContext(ctx, "已取消旧评审任务",
		"task_id", task.ID, "prev_status", prevStatus, "pr", task.PRNumber)
	return true
}

// enqueueTask 持久化任务记录并将其入队,record 字段 TaskType/Priority/RepoFullName/DeliveryID 需预先填充
func (h *EnqueueHandler) enqueueTask(ctx context.Context, payload model.TaskPayload, record *model.TaskRecord) error {
	now := time.Now()
	record.ID = uuid.New().String()
	record.Status = model.TaskStatusPending
	record.MaxRetry = TaskMaxRetry()
	record.CreatedAt = now
	record.UpdatedAt = now

	// M2.4: 设置 payload.CreatedAt 用于 staleness check
	payload.CreatedAt = record.CreatedAt
	record.Payload = payload

	// 1. 先持久化到 SQLite（status=pending）
	if err := h.store.CreateTask(ctx, record); err != nil {
		return fmt.Errorf("创建任务记录失败: %w", err)
	}

	// 2. 入队到 Redis
	// 设计决策："先持久化再入队" 的 eventually consistent 模式。
	// Step 1 成功但 Step 2 失败时,任务保持 pending 状态,不向调用方返回错误。
	// RecoveryLoop 会定期扫描长时间处于 pending 的孤儿任务并重新入队,
	// 从而保证最终一致性。这避免了分布式事务的复杂性,代价是入队可能有延迟。
	// 使用共享的 buildAsynqTaskID 生成确定性 TaskID,确保与 RecoveryLoop 一致
	taskID := buildAsynqTaskID(record.DeliveryID, record.TaskType)
	asynqID, err := h.client.Enqueue(ctx, payload, EnqueueOptions{
		Priority: record.Priority,
		TaskID:   taskID,
	})
	if err != nil {
		// 设计决策：入队失败不返回错误给调用方。
		// 设计文档（docs/M1.5-task-queue-design.md）中描述入队失败应返回 error,
		// 但实际实现采用了 "先持久化再入队" 的 eventually consistent 模式：
		// 任务已成功持久化到 SQLite（status=pending）,RecoveryLoop 会定期扫描
		// 长时间处于 pending 的孤儿任务并重新入队,最终保证一致性。
		// 若此处返回 error,webhook handler 会向 Gitea 返回 500,触发 Gitea 重发
		// 同一 webhook,而任务实际已被持久化,这会造成不必要的重试噪音。
		// 因此选择静默降级：记录警告日志,依赖 RecoveryLoop 补偿入队,
		// 代价是入队可能有最多 interval（默认 60s）的延迟。
		h.logger.WarnContext(ctx, "任务入队失败,将由 RecoveryLoop 重试",
			"task_id", record.ID,
			"task_type", record.TaskType,
			"error", err,
		)
		return nil
	}

	// 3. 更新状态为 queued
	record.AsynqID = asynqID
	record.Status = model.TaskStatusQueued
	record.UpdatedAt = time.Now()
	if err := h.store.UpdateTask(ctx, record); err != nil {
		h.logger.ErrorContext(ctx, "更新任务状态为 queued 失败",
			"task_id", record.ID,
			"error", err,
		)
		// 不返回错误,任务已成功入队
	}

	return nil
}

// EnqueueManualReview 手动触发 PR 评审入队。
// payload 由 API handler 组装（含完整 PR 信息）,triggeredBy 格式为 "manual:{identity}"。
func (h *EnqueueHandler) EnqueueManualReview(ctx context.Context, payload model.TaskPayload, triggeredBy string) (string, error) {
	payload.TaskType = model.TaskTypeReviewPR
	payload.DeliveryID = generateManualDeliveryID()

	// Cancel-and-Replace：查找同一 PR 的活跃旧任务
	activeTasks, superseded := h.listActivePRTasks(ctx, payload.RepoFullName, payload.PRNumber)
	payload.SupersededCount = superseded.Count
	payload.PreviousHeadSHA = superseded.LastHeadSHA

	record := &model.TaskRecord{
		TaskType:     model.TaskTypeReviewPR,
		Priority:     model.PriorityHigh,
		RepoFullName: payload.RepoFullName,
		PRNumber:     payload.PRNumber,
		DeliveryID:   payload.DeliveryID,
		TriggeredBy:  triggeredBy,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return "", err
	}
	h.cancelTasks(ctx, activeTasks)

	h.logger.InfoContext(ctx, "手动 PR 评审任务已入队",
		"task_id", record.ID, "repo", payload.RepoFullName,
		"pr", payload.PRNumber, "triggered_by", triggeredBy)
	return record.ID, nil
}

// EnqueueManualFix 手动触发 Issue 分析或修复入队。
// M3.4: 支持调用方通过 payload.TaskType 指定任务类型（analyze_issue 或 fix_issue）,
// 未指定时默认为 analyze_issue。
func (h *EnqueueHandler) EnqueueManualFix(ctx context.Context, payload model.TaskPayload, triggeredBy string) (string, error) {
	// M3.4: 支持调用方指定 TaskType（analyze_issue 或 fix_issue）
	if payload.TaskType == "" {
		payload.TaskType = model.TaskTypeAnalyzeIssue // 默认分析模式
	}
	// 校验仅允许 Issue 相关类型
	if payload.TaskType != model.TaskTypeAnalyzeIssue && payload.TaskType != model.TaskTypeFixIssue {
		return "", fmt.Errorf("EnqueueManualFix 不支持任务类型: %s", payload.TaskType)
	}
	payload.DeliveryID = generateManualDeliveryID()

	activeTasks := h.listReplacementIssueTasks(ctx, payload.RepoFullName, payload.IssueNumber, payload.TaskType)

	record := &model.TaskRecord{
		TaskType:     payload.TaskType,
		Priority:     model.PriorityNormal,
		RepoFullName: payload.RepoFullName,
		DeliveryID:   payload.DeliveryID,
		TriggeredBy:  triggeredBy,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return "", err
	}
	h.cancelTasks(ctx, activeTasks)

	taskTypeName := "Issue 分析"
	if payload.TaskType == model.TaskTypeFixIssue {
		taskTypeName = "Issue 修复"
	}
	h.logger.InfoContext(ctx, "手动"+taskTypeName+"任务已入队",
		"task_id", record.ID, "task_type", payload.TaskType,
		"repo", payload.RepoFullName,
		"issue", payload.IssueNumber, "triggered_by", triggeredBy)
	return record.ID, nil
}

// enqueueSingleGenTests 封装单个 gen_tests 任务的入队逻辑,供 EnqueueManualGenTests
// 及未来的多模块批量入队复用。调用方需确保 payload.RepoFullName / CloneURL 已填充。
func (h *EnqueueHandler) enqueueSingleGenTests(ctx context.Context, payload model.TaskPayload, triggeredBy string) (EnqueuedTask, error) {
	return h.enqueueGenTestsWithDeliveryID(ctx, payload, triggeredBy, generateManualDeliveryID())
}

func (h *EnqueueHandler) enqueueGenTestsWithDeliveryID(ctx context.Context, payload model.TaskPayload, triggeredBy, deliveryID string) (EnqueuedTask, error) {
	payload.TaskType = model.TaskTypeGenTests
	payload.DeliveryID = deliveryID
	activeTasks := h.listActiveGenTestsTasksByBranchKey(ctx, payload.RepoFullName, payload.Module, payload.Framework)

	if len(activeTasks) > 0 && h.branchCleaner != nil {
		branchName := test.BuildAutoTestBranchName(payload.Module, payload.Framework)
		if err := h.branchCleaner.CleanupAutoTestBranch(ctx, payload.RepoOwner, payload.RepoName, branchName); err != nil {
			h.logger.WarnContext(ctx, "清理旧 auto-test 分支失败,继续入队新任务",
				"repo", payload.RepoFullName, "module", payload.Module,
				"branch", branchName, "error", err)
		}
	}

	h.cancelTasks(ctx, activeTasks)

	record := &model.TaskRecord{
		TaskType:     model.TaskTypeGenTests,
		Priority:     model.PriorityNormal,
		RepoFullName: payload.RepoFullName,
		DeliveryID:   payload.DeliveryID,
		TriggeredBy:  triggeredBy,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return EnqueuedTask{}, err
	}

	h.logger.InfoContext(ctx, "gen_tests 任务已入队",
		"task_id", record.ID, "repo", payload.RepoFullName,
		"module", payload.Module, "framework", payload.Framework,
		"triggered_by", triggeredBy)
	return EnqueuedTask{TaskID: record.ID, Module: payload.Module, Framework: payload.Framework}, nil
}

// cleanupAllAutoTestBranches 全量清理同仓库下所有活跃 gen_tests 任务及 auto-test/* 分支。
// 用于整仓拆分前（M4.2.1）清理旧的整仓 auto-test/* 资源，为多模块并行生成腾出空间。
// 各步骤 best-effort，任意失败仅记 warn 日志，不阻断后续操作。
func (h *EnqueueHandler) cleanupAllAutoTestBranches(ctx context.Context, owner, repo, repoFullName string) {
	modules, err := h.store.ListActiveGenTestsModules(ctx, repoFullName)
	if err != nil {
		h.logger.WarnContext(ctx, "cleanupAll: 查询活跃 gen_tests module 列表失败",
			"repo", repoFullName, "error", err)
	} else {
		for _, mod := range modules {
			tasks, tErr := h.store.FindActiveGenTestsTasks(ctx, repoFullName, mod)
			if tErr != nil {
				h.logger.WarnContext(ctx, "cleanupAll: 查询活跃 gen_tests 任务失败",
					"repo", repoFullName, "module", mod, "error", tErr)
				continue
			}
			for _, task := range tasks {
				h.logger.InfoContext(ctx, "cleanupAll: 即将取消任务",
					"task_id", task.ID, "module", mod)
			}
			h.cancelTasks(ctx, tasks)
		}
	}

	if h.prClient == nil {
		h.logger.WarnContext(ctx, "cleanupAll: prClient 未注入，跳过 PR 关闭和分支删除",
			"repo", repoFullName)
		return
	}

	prs, truncated, prErr := gitea.PaginateAll(ctx, 50, 10,
		func(ctx context.Context, page, pageSize int) ([]*gitea.PullRequest, *gitea.Response, error) {
			return h.prClient.ListRepoPullRequests(ctx, owner, repo, gitea.ListPullRequestsOptions{
				ListOptions: gitea.ListOptions{Page: page, PageSize: pageSize},
				State:       "open",
			})
		})
	if prErr != nil {
		h.logger.WarnContext(ctx, "cleanupAll: 列出 open PR 失败",
			"repo", repoFullName, "error", prErr)
		return
	}
	if truncated {
		h.logger.WarnContext(ctx, "cleanupAll: open PR 列表被截断，auto-test 资源清理可能不完整",
			"repo", repoFullName, "fetched", len(prs))
	}

	for _, pr := range prs {
		if pr.Head == nil || !strings.HasPrefix(pr.Head.Ref, autoTestBranchPrefix) {
			continue
		}
		h.logger.InfoContext(ctx, "cleanupAll: 即将关闭 PR 并删除分支",
			"repo", repoFullName, "pr", pr.Number, "branch", pr.Head.Ref)

		if closeErr := h.prClient.ClosePullRequest(ctx, owner, repo, pr.Number); closeErr != nil {
			h.logger.WarnContext(ctx, "cleanupAll: 关闭 PR 失败",
				"repo", repoFullName, "pr", pr.Number, "error", closeErr)
		}
		if h.branchCleaner != nil {
			_ = h.branchCleaner.CleanupAutoTestBranch(ctx, owner, repo, pr.Head.Ref)
		}
	}
}

// EnqueueManualGenTests 手动触发 gen_tests 任务入队。
// payload 由 API handler / CLI handler 组装；triggeredBy 格式为 "manual:{identity}"。
// Cancel-and-Replace 按 stable branch key 粒度：同仓库同 branch key 的活跃任务在本次入队后被取消。
//
// M4.2.1：整仓模式（module 空 + framework 空 + moduleScanner 已注入 + baseRef 非空）时
// 自动调用 ScanRepoModules 拆分子任务；多模块时先全量清理旧 auto-test 资源；
// 扫描失败 fail-open 回退单任务。
func (h *EnqueueHandler) EnqueueManualGenTests(ctx context.Context, payload model.TaskPayload, triggeredBy string) ([]EnqueuedTask, error) {
	if payload.RepoFullName == "" || payload.CloneURL == "" {
		return nil, fmt.Errorf("payload 数据不完整: RepoFullName 或 CloneURL 为空")
	}

	// 整仓模式：module 空 + framework 空 + moduleScanner 已注入 + baseRef 非空
	if payload.Module == "" && payload.Framework == "" && h.moduleScanner != nil && payload.BaseRef != "" {
		discovered, err := test.ScanRepoModules(ctx, h.moduleScanner,
			payload.RepoOwner, payload.RepoName, payload.BaseRef)
		if err != nil {
			if errors.Is(err, test.ErrNoFrameworkDetected) {
				return nil, err
			}
			h.logger.WarnContext(ctx, "ScanRepoModules 失败，回退到单任务逻辑",
				"repo", payload.RepoFullName, "error", err)
			result, singleErr := h.enqueueSingleGenTests(ctx, payload, triggeredBy)
			if singleErr != nil {
				return nil, singleErr
			}
			return []EnqueuedTask{result}, nil
		}

		if len(discovered) >= 2 {
			h.cleanupAllAutoTestBranches(ctx, payload.RepoOwner, payload.RepoName, payload.RepoFullName)
		}

		var results []EnqueuedTask
		for _, mod := range discovered {
			subPayload := payload
			subPayload.Module = mod.Path
			subPayload.Framework = string(mod.Framework)
			result, subErr := h.enqueueSingleGenTests(ctx, subPayload, triggeredBy)
			if subErr != nil {
				h.logger.WarnContext(ctx, "子任务入队失败",
					"repo", payload.RepoFullName, "module", mod.Path,
					"framework", mod.Framework, "error", subErr)
				continue
			}
			results = append(results, result)
		}
		if len(results) == 0 {
			return nil, fmt.Errorf("所有子任务入队均失败")
		}

		h.logger.InfoContext(ctx, "整仓拆分 gen_tests 入队完成",
			"repo", payload.RepoFullName, "discovered", len(discovered),
			"enqueued", len(results), "triggered_by", triggeredBy)
		return results, nil
	}

	// 非整仓模式或未注入 scanner：单任务入队
	result, err := h.enqueueSingleGenTests(ctx, payload, triggeredBy)
	if err != nil {
		return nil, err
	}
	return []EnqueuedTask{result}, nil
}

// listActiveGenTestsTasks EnqueueHandler 私有 helper,封装错误日志,
// 与 listActivePRTasks / listActiveIssueTasks 风格一致。
func (h *EnqueueHandler) listActiveGenTestsTasks(ctx context.Context, repoFullName, module string) []*model.TaskRecord {
	tasks, err := h.store.FindActiveGenTestsTasks(ctx, repoFullName, module)
	if err != nil {
		h.logger.WarnContext(ctx, "查找活跃 gen_tests 任务失败,跳过取消",
			"repo", repoFullName, "module", module, "error", err)
		return nil
	}
	return tasks
}

// listActiveGenTestsTasksByBranchKey 查找会落到同一 stable branch 的活跃 gen_tests 任务。
//
// 设计背景：stable branch 名由 test.ModuleKey(module) 派生,原始 module 字符串不同但
// branch key 相同（例如 "svc/api" 与 "svc-api"）时,本质上会写同一 remote branch。
// 若仍按原始 module 精确匹配 Cancel-and-Replace,会放行两条任务并发写同一分支。
//
// M4.2.1：framework 非空时,targetKey 追加 "-{framework}" 后缀,使同 module 不同 framework
// 的任务（例如 junit5 与 vitest）不互相取消；同时在内层循环增加二次过滤,排除
// Payload.Framework 不匹配的历史任务。
//
// 实现上先取活跃 module 列表,再按 ModuleKey 过滤并回查具体任务。若列表查询失败则回退
// 到旧的"按原始 module 精确匹配"逻辑,保持 fail-open 行为与既有兼容性。
func (h *EnqueueHandler) listActiveGenTestsTasksByBranchKey(ctx context.Context,
	repoFullName, module, framework string) []*model.TaskRecord {
	targetKey := test.ModuleKey(module)
	if framework != "" {
		targetKey = targetKey + "-" + framework
	}

	modules, err := h.store.ListActiveGenTestsModules(ctx, repoFullName)
	if err != nil {
		h.logger.WarnContext(ctx, "查询活跃 gen_tests module 列表失败,回退到原始 module 精确匹配",
			"repo", repoFullName, "module", module, "module_key", targetKey, "error", err)
		return h.listActiveGenTestsTasks(ctx, repoFullName, module)
	}

	seenModule := make(map[string]struct{}, len(modules))
	seenTask := make(map[string]struct{})
	var merged []*model.TaskRecord
	for _, candidate := range modules {
		if test.ModuleKey(candidate) != test.ModuleKey(module) {
			continue
		}
		if _, ok := seenModule[candidate]; ok {
			continue
		}
		seenModule[candidate] = struct{}{}

		tasks, err := h.store.FindActiveGenTestsTasks(ctx, repoFullName, candidate)
		if err != nil {
			h.logger.WarnContext(ctx, "按 stable branch key 回查活跃 gen_tests 任务失败,跳过该 module",
				"repo", repoFullName,
				"module", module,
				"candidate_module", candidate,
				"module_key", targetKey,
				"error", err,
			)
			continue
		}
		for _, task := range tasks {
			if task == nil {
				continue
			}
			if _, ok := seenTask[task.ID]; ok {
				continue
			}
			if framework != "" && task.Payload.Framework != framework {
				continue
			}
			seenTask[task.ID] = struct{}{}
			merged = append(merged, task)
		}
	}
	return merged
}

// generateManualDeliveryID 生成手动触发的合成 delivery ID
func generateManualDeliveryID() string {
	return fmt.Sprintf("manual-%d-%s", time.Now().UnixMilli(), uuid.New().String()[:8])
}

// EnqueueCodeFromDoc 手动触发文档驱动编码任务入队。
// Cancel-and-Replace 按 (repo, branch) 聚合——同一分支同时只能有一个 code_from_doc 任务。
func (h *EnqueueHandler) EnqueueCodeFromDoc(ctx context.Context, payload model.TaskPayload, triggeredBy string) (string, error) {
	if payload.RepoFullName == "" || payload.CloneURL == "" {
		return "", fmt.Errorf("payload 数据不完整: RepoFullName 或 CloneURL 为空")
	}

	payload.TaskType = model.TaskTypeCodeFromDoc

	// 构造幂等 DeliveryID
	branch := payload.HeadRef
	if branch == "" {
		branch = "auto-code/" + payload.DocSlug
	}
	payload.DeliveryID = fmt.Sprintf("manual:code_from_doc:%s:%s", payload.RepoFullName, branch)

	// Cancel-and-Replace：按 (repo, branch) 聚合
	activeTasks, _ := h.store.FindActiveTasksByModule(ctx, payload.RepoFullName, branch, model.TaskTypeCodeFromDoc)

	record := &model.TaskRecord{
		TaskType:     model.TaskTypeCodeFromDoc,
		Priority:     model.PriorityNormal,
		RepoFullName: payload.RepoFullName,
		DeliveryID:   payload.DeliveryID,
		TriggeredBy:  triggeredBy,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return "", err
	}
	h.cancelTasks(ctx, activeTasks)

	h.logger.InfoContext(ctx, "code_from_doc 任务已入队",
		"task_id", record.ID, "repo", payload.RepoFullName,
		"doc_path", payload.DocPath, "branch", branch,
		"triggered_by", triggeredBy)
	return record.ID, nil
}

// filterTasksByType 从任务列表中过滤指定类型的任务。
func filterTasksByType(tasks []*model.TaskRecord, taskType model.TaskType) []*model.TaskRecord {
	var result []*model.TaskRecord
	for _, t := range tasks {
		if t.TaskType == taskType {
			result = append(result, t)
		}
	}
	return result
}

// listActiveE2ETasks 查找同模块活跃 E2E 任务（fail-open）。
func (h *EnqueueHandler) listActiveE2ETasks(ctx context.Context, repoFullName, module string) []*model.TaskRecord {
	tasks, err := h.store.FindActiveTasksByModule(ctx, repoFullName, module, model.TaskTypeRunE2E)
	if err != nil {
		h.logger.WarnContext(ctx, "查找活跃 run_e2e 任务失败,跳过取消",
			"repo", repoFullName, "module", module, "error", err)
		return nil
	}
	return tasks
}

// cleanupAllActiveE2ETasks 全量拆分前清理同仓库所有活跃 run_e2e 任务。
func (h *EnqueueHandler) cleanupAllActiveE2ETasks(ctx context.Context, repoFullName string) {
	modules, err := h.store.ListActiveModules(ctx, repoFullName, model.TaskTypeRunE2E)
	if err != nil {
		h.logger.WarnContext(ctx, "cleanupAllE2E: 查询活跃 run_e2e module 列表失败",
			"repo", repoFullName, "error", err)
		return
	}
	// ListActiveModules 无 DISTINCT，可能返回重复 module；cancelTasks 幂等，重复调用无副作用
	for _, mod := range modules {
		tasks, tErr := h.store.FindActiveTasksByModule(ctx, repoFullName, mod, model.TaskTypeRunE2E)
		if tErr != nil {
			h.logger.WarnContext(ctx, "cleanupAllE2E: 查询活跃 run_e2e 任务失败",
				"repo", repoFullName, "module", mod, "error", tErr)
			continue
		}
		h.cancelTasks(ctx, tasks)
	}
}

// enqueueSingleE2E 封装单个 E2E 任务的入队 + Cancel-and-Replace。
func (h *EnqueueHandler) enqueueSingleE2E(ctx context.Context, payload model.TaskPayload, triggeredBy string) (EnqueuedTask, error) {
	payload.TaskType = model.TaskTypeRunE2E
	if payload.DeliveryID == "" {
		payload.DeliveryID = generateManualDeliveryID()
	}

	activeTasks := h.listActiveE2ETasks(ctx, payload.RepoFullName, payload.Module)

	record := &model.TaskRecord{
		TaskType:     model.TaskTypeRunE2E,
		Priority:     model.PriorityNormal,
		RepoFullName: payload.RepoFullName,
		DeliveryID:   payload.DeliveryID,
		TriggeredBy:  triggeredBy,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return EnqueuedTask{}, err
	}
	h.cancelTasks(ctx, activeTasks)

	h.logger.InfoContext(ctx, "run_e2e 任务已入队",
		"task_id", record.ID, "repo", payload.RepoFullName,
		"module", payload.Module, "triggered_by", triggeredBy)
	return EnqueuedTask{TaskID: record.ID, Module: payload.Module}, nil
}

// EnqueueManualE2E 手动入队 E2E 测试任务。
// M5.3：全量模式（module 空 + e2eModuleScanner 已注入 + baseRef 非空）时
// 自动扫描 e2e/ 目录拆分子任务。
func (h *EnqueueHandler) EnqueueManualE2E(ctx context.Context, payload model.TaskPayload, triggeredBy string) ([]EnqueuedTask, error) {
	if payload.RepoFullName == "" || payload.CloneURL == "" {
		return nil, fmt.Errorf("payload 数据不完整: RepoFullName 或 CloneURL 为空")
	}

	// 全量模式：module 空 + scanner 已注入 + baseRef 非空
	if payload.Module == "" && h.e2eModuleScanner != nil && payload.BaseRef != "" {
		modules, err := e2esvc.ScanE2EModules(ctx, h.e2eModuleScanner,
			payload.RepoOwner, payload.RepoName, payload.BaseRef)
		if err != nil {
			if errors.Is(err, e2esvc.ErrNoE2EModulesFound) {
				return nil, err
			}
			h.logger.WarnContext(ctx, "ScanE2EModules 失败，回退到单任务逻辑",
				"repo", payload.RepoFullName, "error", err)
			result, singleErr := h.enqueueSingleE2E(ctx, payload, triggeredBy)
			if singleErr != nil {
				return nil, singleErr
			}
			return []EnqueuedTask{result}, nil
		}

		h.cleanupAllActiveE2ETasks(ctx, payload.RepoFullName)

		var results []EnqueuedTask
		for _, mod := range modules {
			subPayload := payload
			subPayload.Module = mod
			subPayload.DeliveryID = ""
			result, subErr := h.enqueueSingleE2E(ctx, subPayload, triggeredBy)
			if subErr != nil {
				h.logger.WarnContext(ctx, "E2E 子任务入队失败",
					"repo", payload.RepoFullName, "module", mod, "error", subErr)
				continue
			}
			results = append(results, result)
		}
		if len(results) == 0 {
			return nil, fmt.Errorf("所有 E2E 子任务入队均失败")
		}

		h.logger.InfoContext(ctx, "E2E 全量拆分入队完成",
			"repo", payload.RepoFullName, "discovered", len(modules),
			"enqueued", len(results), "triggered_by", triggeredBy)
		return results, nil
	}

	// 非全量模式：单任务入队
	result, err := h.enqueueSingleE2E(ctx, payload, triggeredBy)
	if err != nil {
		return nil, err
	}
	return []EnqueuedTask{result}, nil
}
