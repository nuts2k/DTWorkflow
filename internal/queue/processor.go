package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/e2e"
	"otws19.zicp.vip/kelin/dtworkflow/internal/fix"
	"otws19.zicp.vip/kelin/dtworkflow/internal/iterate"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/test"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

var shanghaiZone = time.FixedZone("Asia/Shanghai", 8*3600)

func formatNotifyTime() string {
	return time.Now().In(shanghaiZone).Format("2006-01-02 15:04:05")
}

func formatDuration(d time.Duration) string {
	return d.Truncate(time.Second).String()
}

// 编译时检查 *Processor 实现 asynq.Handler 接口
var _ asynq.Handler = (*Processor)(nil)

// PoolRunner 抽象 Pool.Run 接口，便于 mock 测试
type PoolRunner interface {
	Run(ctx context.Context, payload model.TaskPayload) (*worker.ExecutionResult, error)
	RunWithCommandAndStdin(ctx context.Context, payload model.TaskPayload,
		cmd []string, stdinData []byte) (*worker.ExecutionResult, error)
}

// TaskNotifier 抽象通知发送接口，便于在 Processor 中解耦 Router 实现并进行测试。
type TaskNotifier interface {
	Send(ctx context.Context, msg notify.Message) error
}

// GenTestsPRCommenter 是可选扩展接口：
// 当通知装配层具备 Gitea PR 评论 upsert 能力时，Processor 会在 gen_tests 终态
// 额外同步一条带锚点的 PR 评论，用于刷新稳定分支上的最新测试生成结果。
type GenTestsPRCommenter interface {
	CommentOnGenTestsPR(ctx context.Context, owner, repo string, prNumber int64, body string) error
}

// ReviewExecutor 窄接口，解耦 review 包
type ReviewExecutor interface {
	Execute(ctx context.Context, payload model.TaskPayload) (*review.ReviewResult, error)
	// WriteDegraded 在重试耗尽后发送降级评论（原始输出作为 COMMENT）。
	// 仅在 Execute 因 ErrParseFailure 失败且所有重试用完时调用。
	WriteDegraded(ctx context.Context, payload model.TaskPayload, result *review.ReviewResult) error
	ResolveConfig(repoFullName string) review.ReviewConfig
}

// FixExecutor 窄接口，解耦 fix 包。
// M3.2 激活：当 fix.Service 实现容器执行后，Processor 将通过此接口路由 fix_issue 任务。
type FixExecutor interface {
	Execute(ctx context.Context, payload model.TaskPayload) (*fix.FixResult, error)
	// WriteDegraded 在修复结果解析失败且重试耗尽后发送降级 Issue 评论。
	WriteDegraded(ctx context.Context, payload model.TaskPayload, result *fix.FixResult) error
}

// WithFixService 注入 Issue 分析服务。
// M3.2 激活：serve.go 装配层将调用此函数注入 fix.Service。
func WithFixService(svc FixExecutor) ProcessorOption {
	return func(p *Processor) {
		p.fixService = svc
	}
}

// TestExecutor 窄接口，解耦 test 包。
// Processor 通过此接口路由 gen_tests 任务；接口签名对齐 ReviewExecutor / FixExecutor，
// 确保三者可复用同一套状态机与降级回写骨架。
type TestExecutor interface {
	Execute(ctx context.Context, payload model.TaskPayload) (*test.TestGenResult, error)
	// WriteDegraded 在测试生成结果解析失败且重试耗尽后触发。
	WriteDegraded(ctx context.Context, payload model.TaskPayload, result *test.TestGenResult) error
}

// WithTestService 注入测试生成服务。
// serve.go 装配层会调用此函数注入 test.Service。
func WithTestService(svc TestExecutor) ProcessorOption {
	return func(p *Processor) {
		p.testService = svc
	}
}

// E2EExecutor 窄接口，解耦 e2e 包。
type E2EExecutor interface {
	Execute(ctx context.Context, payload model.TaskPayload) (*e2e.E2EResult, error)
}

// WithE2EService 注入 E2E 执行服务。
func WithE2EService(svc E2EExecutor) ProcessorOption {
	return func(p *Processor) {
		p.e2eService = svc
	}
}

// WithEnqueueHandler 注入 EnqueueHandler，triage_e2e 成功后链式入队 run_e2e。
func WithEnqueueHandler(h *EnqueueHandler) ProcessorOption {
	return func(p *Processor) { p.enqueueHandler = h }
}

// IterateExecutor 窄接口，解耦 iterate 包。
type IterateExecutor interface {
	Execute(ctx context.Context, payload model.TaskPayload) (*iterate.FixReviewResult, error)
}

// WithIterateService 注入迭代修复服务。
func WithIterateService(svc IterateExecutor) ProcessorOption {
	return func(p *Processor) {
		p.iterateService = svc
	}
}

// ReviewEnabledChecker 是 Processor 层的窄接口（ISP）
// 仅暴露 Enabled 检查所需的最小能力
type ReviewEnabledChecker interface {
	IsReviewEnabled(repoFullName string) bool
}

// ProcessorOption Processor 配置选项
type ProcessorOption func(*Processor)

// WithReviewService 注入评审服务
func WithReviewService(svc ReviewExecutor) ProcessorOption {
	return func(p *Processor) {
		p.reviewService = svc
	}
}

// WithReviewEnabledChecker 注入评审开关检查器
func WithReviewEnabledChecker(c ReviewEnabledChecker) ProcessorOption {
	return func(p *Processor) { p.reviewEnabledChecker = c }
}

// WithGiteaBaseURL 注入 Gitea 实例 URL，用于通知消息中的跳转链接
func WithGiteaBaseURL(url string) ProcessorOption {
	return func(p *Processor) {
		p.giteaBaseURL = strings.TrimRight(url, "/")
	}
}

// Processor 处理 asynq 任务，协调 Store 状态更新与 PoolRunner 执行
type Processor struct {
	pool                 PoolRunner
	store                store.Store
	notifier             TaskNotifier
	logger               *slog.Logger
	reviewService        ReviewExecutor
	reviewEnabledChecker ReviewEnabledChecker // 可选，nil 时默认启用
	fixService           FixExecutor          // M3.2 激活；M3.1 始终为 nil，fix_issue 走 pool.Run()
	testService          TestExecutor         // 可选；注入后 gen_tests 走 test.Service，否则回退 pool.Run()
	e2eService           E2EExecutor          // 可选；注入后 run_e2e 走 e2e.Service，否则回退 pool.Run()
	enqueueHandler       *EnqueueHandler      // 可选；triage_e2e 成功后链式入队 run_e2e
	iterateService       IterateExecutor      // 可选；注入后 fix_review 走 iterate.Service
	giteaBaseURL         string               // Gitea 实例 URL，用于构造 PR 跳转链接
}

// NewProcessor 创建 Processor 实例。
// 参数 pool 和 store 为必要依赖，传入 nil 属于编程错误（programming error），
// 因此使用 panic 而非返回 error，与 Go 标准库的惯例一致。
// notifier 为可选依赖，传入 nil 表示当前运行模式未启用通知。
func NewProcessor(pool PoolRunner, store store.Store, notifier TaskNotifier, logger *slog.Logger, opts ...ProcessorOption) *Processor {
	if pool == nil {
		panic("NewProcessor: pool 不能为 nil")
	}
	if store == nil {
		panic("NewProcessor: store 不能为 nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	p := &Processor{
		pool:     pool,
		store:    store,
		notifier: notifier,
		logger:   logger,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// shouldRetry 判断任务是否应标记为 retrying 状态。
// asynq 语义：GetRetryCount 返回当前已重试次数（从 0 开始），
// GetMaxRetry 返回最大重试总次数。当 retryCount == maxRetry-1 时，
// 这是最后一次重试尝试，handler 返回错误后 asynq 不会再重试，
// 因此此时应标记为 failed 而非 retrying。
func shouldRetry(ctx context.Context) bool {
	retryCount, rcOk := asynq.GetRetryCount(ctx)
	maxRetry, mrOk := asynq.GetMaxRetry(ctx)
	return rcOk && mrOk && maxRetry > 0 && retryCount < maxRetry-1
}

// ProcessTask 是 asynq.Handler 的实现，处理单个任务
func (p *Processor) ProcessTask(ctx context.Context, task *asynq.Task) error {
	// 1. 反序列化 payload
	var payload model.TaskPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("反序列化 TaskPayload 失败: %w", err)
	}

	// 2. 从 store 中查找对应任务记录
	// 优先通过 delivery_id + task_type 查找（与入队时的幂等 key 一致）
	record, err := p.findRecord(ctx, payload)
	if err != nil {
		return err
	}
	taskID := record.ID

	// M2.4: 从 SQLite record 覆盖 payload.CreatedAt，确保与数据库一致
	payload.CreatedAt = record.CreatedAt
	payload.TaskID = record.ID

	if record.Status == model.TaskStatusCancelled {
		p.logger.InfoContext(ctx, "任务已标记为 cancelled，跳过执行",
			"task_id", taskID,
			"task_type", payload.TaskType,
		)
		return fmt.Errorf("任务已取消，跳过执行: %w", asynq.SkipRetry)
	}

	// 从 asynq context 中获取当前重试次数并更新记录
	if retryCount, ok := asynq.GetRetryCount(ctx); ok {
		record.RetryCount = retryCount
	}

	p.logger.InfoContext(ctx, "开始处理任务",
		"task_id", taskID,
		"task_type", payload.TaskType,
		"repo", payload.RepoFullName,
	)

	// 3. 更新状态为 running
	now := time.Now()
	record.Status = model.TaskStatusRunning
	record.StartedAt = &now
	record.UpdatedAt = now
	if err := p.store.UpdateTask(ctx, record); err != nil {
		// 状态更新失败不中断执行，仅记录警告
		p.logger.WarnContext(ctx, "更新任务状态为 running 失败",
			"task_id", taskID,
			"error", err,
		)
	}

	// 4. 执行任务（通过 PoolRunner 或 ReviewExecutor）

	// M2.5: 评审开关检查
	if payload.TaskType == model.TaskTypeReviewPR && p.reviewEnabledChecker != nil {
		if !p.reviewEnabledChecker.IsReviewEnabled(payload.RepoFullName) {
			p.logger.InfoContext(ctx, "评审已禁用，跳过任务",
				"task_id", taskID,
				"repo", payload.RepoFullName,
			)
			// 跳过的任务标记为成功
			record.Status = model.TaskStatusSucceeded
			record.UpdatedAt = time.Now()
			completedAt := time.Now()
			record.CompletedAt = &completedAt
			if err := p.store.UpdateTask(ctx, record); err != nil {
				p.logger.WarnContext(ctx, "更新跳过任务状态失败",
					"task_id", taskID, "error", err)
			}
			return nil
		}
	}

	// M2.6: 评审开关检查通过后、实际执行前，发送"开始"通知
	if record.RetryCount == 0 {
		p.sendStartNotification(ctx, payload)
	}

	var reviewResult *review.ReviewResult
	var fixResult *fix.FixResult
	var testResult *test.TestGenResult
	var e2eResult *e2e.E2EResult
	var triageResult *e2e.TriageE2EOutput
	var triageDispatchErr error
	var iterateResult *iterate.FixReviewResult
	var result *worker.ExecutionResult
	var runErr error

	if payload.TaskType == model.TaskTypeFixReview {
		var prepareErr error
		payload, prepareErr = p.prepareFixReviewPayload(ctx, payload)
		record.Payload = payload
		if prepareErr != nil {
			runErr = prepareErr
		}
	}

	// 路由：review_pr → reviewService，analyze_issue/fix_issue → fixService，
	// gen_tests → testService；对应 service 未注入时回退到 pool.Run。
	switch {
	case runErr != nil:
	case payload.TaskType == model.TaskTypeReviewPR && p.reviewService != nil:
		reviewResult, runErr = p.reviewService.Execute(ctx, payload)
		if reviewResult != nil {
			result = adaptReviewResult(reviewResult)
		}
	case (payload.TaskType == model.TaskTypeAnalyzeIssue || payload.TaskType == model.TaskTypeFixIssue) && p.fixService != nil:
		fixResult, runErr = p.fixService.Execute(ctx, payload)
		if fixResult != nil {
			result = adaptFixResult(fixResult)
		}
	case payload.TaskType == model.TaskTypeGenTests && p.testService != nil:
		testResult, runErr = p.testService.Execute(ctx, payload)
		if testResult != nil {
			result = adaptTestResult(testResult)
		}
	case payload.TaskType == model.TaskTypeRunE2E && p.e2eService != nil:
		e2eResult, runErr = p.e2eService.Execute(ctx, payload)
		if e2eResult != nil {
			result = adaptE2EResult(e2eResult)
		}
	case payload.TaskType == model.TaskTypeFixReview && p.iterateService != nil:
		iterateResult, runErr = p.iterateService.Execute(ctx, payload)
		if iterateResult != nil {
			result = &worker.ExecutionResult{
				Output: iterateResult.RawOutput,
			}
		}
	case payload.TaskType == model.TaskTypeTriageE2E:
		triagePrompt := e2e.BuildTriagePromptWithContext(e2e.TriagePromptContext{
			Repo:           payload.RepoFullName,
			BaseRef:        payload.BaseRef,
			BaseSHA:        payload.BaseSHA,
			HeadSHA:        payload.HeadSHA,
			MergeCommitSHA: payload.MergeCommitSHA,
			ChangedFiles:   payload.ChangedFiles,
		})
		triageCmd := []string{"claude", "-p", "--output-format", "json", "--disallowedTools", "Edit,Write,MultiEdit,NotebookEdit", "-"}
		result, runErr = p.pool.RunWithCommandAndStdin(ctx, payload, triageCmd, []byte(triagePrompt))
		if runErr == nil && result != nil && result.ExitCode == 0 {
			if strings.TrimSpace(result.Output) == "" {
				runErr = fmt.Errorf("%w: 输出为空", e2e.ErrE2ETriageParseFailure)
				break
			}
			var parseErr error
			triageResult, parseErr = e2e.ParseTriageResult(result.Output)
			if parseErr != nil {
				runErr = parseErr
			}
		}
	default:
		result, runErr = p.pool.Run(ctx, payload)
	}
	// 5. 根据执行结果更新状态
	record.UpdatedAt = time.Now()

	if runErr != nil {
		// M2.4: context.Canceled 表示任务被取消（新评审取代旧评审）
		if errors.Is(runErr, context.Canceled) {
			return p.markTaskCancelled(ctx, record, "任务被取消")
		}
		if errors.Is(runErr, review.ErrStaleReview) {
			return p.markTaskCancelled(ctx, record, "评审已过时，被更新的任务取代")
		}

		// ErrPRNotOpen / ErrIssueNotOpen 是确定性失败，直接标记 failed 并跳过重试
		if errors.Is(runErr, review.ErrPRNotOpen) {
			return p.handleSkipRetryFailure(ctx, record, runErr, reviewResult, nil, nil, "PR 不处于 open 状态，跳过评审")
		}
		if errors.Is(runErr, fix.ErrIssueNotOpen) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, fixResult, nil, "Issue 不处于 open 状态，跳过分析")
		}
		if errors.Is(runErr, fix.ErrMissingIssueRef) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, fixResult, nil, "Issue 未设置关联分支，跳过分析")
		}
		if errors.Is(runErr, fix.ErrInvalidIssueRef) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, fixResult, nil, "Issue 关联的 ref 不存在，跳过分析")
		}
		if errors.Is(runErr, fix.ErrInfoInsufficient) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, fixResult, nil, "前序分析信息不足，跳过修复")
		}
		if errors.Is(runErr, fix.ErrFixFailed) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, fixResult, nil, "Claude 返回 success=false，跳过重试")
		}
		if errors.Is(runErr, test.ErrTestGenDisabled) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, "仓库已禁用 gen_tests，跳过重试")
		}
		if errors.Is(runErr, test.ErrInvalidModule) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, "module 路径非法，跳过测试生成")
		}
		if errors.Is(runErr, test.ErrModuleOutOfScope) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, "module 超出允许范围，跳过测试生成")
		}
		if errors.Is(runErr, test.ErrModuleNotFound) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, "module 子路径不存在于仓库，跳过测试生成")
		}
		if errors.Is(runErr, test.ErrInvalidRef) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, "gen_tests 指定 ref 不存在，跳过重试")
		}
		if errors.Is(runErr, test.ErrAmbiguousFramework) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, "无法确定测试框架，跳过重试")
		}
		if errors.Is(runErr, test.ErrNoFrameworkDetected) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, "未检测到测试框架，跳过重试")
		}
		if errors.Is(runErr, test.ErrInfoInsufficient) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, "测试生成信息不足，跳过重试")
		}
		if errors.Is(runErr, test.ErrTestGenFailed) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, "Claude 返回 success=false，跳过重试")
		}
		if errors.Is(runErr, e2e.ErrE2EDisabled) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, nil, "E2E 已禁用，跳过任务")
		}
		if errors.Is(runErr, e2e.ErrEnvironmentNotFound) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, nil, "E2E 环境未找到，跳过任务")
		}
		if errors.Is(runErr, e2e.ErrNoCasesFound) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, nil, "未发现 E2E 用例，跳过任务")
		}
		if errors.Is(runErr, iterate.ErrFixReviewDeterministicFailure) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, nil, "fix_review 确定性失败，跳过重试")
		}
		if errors.Is(runErr, iterate.ErrFixReviewParseFailure) {
			return p.handleIterationQualityFailure(ctx, record, runErr, iterateResult, "fix_review 修复结果解析失败，跳过重试")
		}
		if errors.Is(runErr, iterate.ErrNoChanges) {
			return p.handleIterationQualityFailure(ctx, record, runErr, iterateResult, "fix_review 未产生实际变更，跳过重试")
		}
		// fix 解析失败且重试耗尽：发送降级评论，让用户至少能在 Issue 上看到原始输出
		if errors.Is(runErr, fix.ErrFixParseFailure) && !shouldRetry(ctx) && fixResult != nil {
			if wbErr := p.fixService.WriteDegraded(ctx, payload, fixResult); wbErr != nil {
				p.logger.ErrorContext(ctx, "修复解析失败降级回写失败",
					"task_id", taskID, "error", wbErr)
			}
		}
		// 解析失败且重试耗尽：发送降级评论，让用户至少能在 PR 上看到原始输出
		if errors.Is(runErr, review.ErrParseFailure) && !shouldRetry(ctx) && reviewResult != nil {
			if wbErr := p.reviewService.WriteDegraded(ctx, payload, reviewResult); wbErr != nil {
				if errors.Is(wbErr, review.ErrStaleReview) {
					return p.markTaskCancelled(ctx, record, "评审已过时，被更新的任务取代")
				}
				p.logger.ErrorContext(ctx, "解析失败降级回写失败",
					"task_id", taskID, "error", wbErr)
			}
		}
		if errors.Is(runErr, test.ErrTestGenParseFailure) && !shouldRetry(ctx) && testResult != nil {
			if wbErr := p.testService.WriteDegraded(ctx, payload, testResult); wbErr != nil {
				p.logger.ErrorContext(ctx, "测试生成解析失败降级回写失败",
					"task_id", taskID, "error", wbErr)
			}
		}
		// 根据 shouldRetry 判断是否还有剩余重试机会：
		// - 有剩余重试：设为 retrying，asynq 将自动安排下次重试
		// - 无剩余重试或无法获取重试信息：设为 failed
		if shouldRetry(ctx) {
			record.Status = model.TaskStatusRetrying
		} else {
			record.Status = model.TaskStatusFailed
		}
		// gen_tests 任务的 runErr 可能由 test.ErrTestGenParseFailure 派生或
		// 夹带仓库 / module 等 Claude 可间接控制的字段，统一走 SanitizeErrorMessage
		// 兜底过滤控制字符 / URL，保证 record.Error 不成为 prompt injection 落点。
		if payload.TaskType == model.TaskTypeGenTests || payload.TaskType == model.TaskTypeRunE2E || payload.TaskType == model.TaskTypeTriageE2E || payload.TaskType == model.TaskTypeFixReview {
			record.Error = test.SanitizeErrorMessage(runErr.Error())
		} else {
			record.Error = runErr.Error()
		}
		if result != nil {
			record.Result = result.Output
		}
		retryCount, _ := asynq.GetRetryCount(ctx)
		maxRetry, _ := asynq.GetMaxRetry(ctx)
		p.logger.ErrorContext(ctx, "任务执行失败",
			"task_id", taskID,
			"error", runErr,
			"retry_count", retryCount,
			"max_retry", maxRetry,
		)
	} else if result != nil && result.ExitCode != 0 {
		// 退出码 2 为确定性失败（如参数错误），直接标记 failed 不重试
		// 其他非零退出码可能是暂时性问题，按 shouldRetry 判断
		if result.ExitCode == 2 {
			record.Status = model.TaskStatusFailed
		} else if shouldRetry(ctx) {
			record.Status = model.TaskStatusRetrying
		} else {
			record.Status = model.TaskStatusFailed
		}
		record.Error = result.Error
		record.Result = result.Output
		p.logger.ErrorContext(ctx, "任务执行返回非零退出码",
			"task_id", taskID,
			"exit_code", result.ExitCode,
			"container_id", result.ContainerID,
		)
	} else {
		record.Status = model.TaskStatusSucceeded
		if result != nil {
			record.Result = result.Output
			record.WorkerID = result.ContainerID
			record.Error = result.Error
		}
		p.logger.InfoContext(ctx, "任务执行成功",
			"task_id", taskID,
			"task_type", payload.TaskType,
		)
	}

	if record.Status == model.TaskStatusSucceeded && triageResult != nil {
		dispatchResult, err := p.handleTriageE2EResult(ctx, record, triageResult)
		if err != nil {
			triageDispatchErr = err
			if errors.Is(err, errTriageDispatchPartialFailure) && shouldRetry(ctx) {
				record.Status = model.TaskStatusRetrying
			} else {
				record.Status = model.TaskStatusFailed
			}
			record.Error = test.SanitizeErrorMessage(err.Error())
			p.logger.ErrorContext(ctx, "triage_e2e: 链式入队失败，任务状态已更新",
				"task_id", record.ID,
				"status", record.Status,
				"requested", dispatchResult.Requested,
				"enqueued", dispatchResult.Enqueued,
				"failed", dispatchResult.Failed,
				"error", err)
		}
	}

	// CompletedAt 仅在任务达到最终状态时设置
	if record.Status == model.TaskStatusSucceeded || record.Status == model.TaskStatusFailed {
		completedAt := time.Now()
		record.CompletedAt = &completedAt
	}

	// 使用后台 context 写最终状态，避免 asynq ctx 超时后 DB 更新失败
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()
	finalStatePersisted := true
	if err := p.store.UpdateTask(persistCtx, record); err != nil {
		finalStatePersisted = false
		p.logger.ErrorContext(ctx, "更新任务最终状态失败",
			"task_id", taskID,
			"status", record.Status,
			"error", err,
		)
	}

	if finalStatePersisted && payload.TaskType == model.TaskTypeFixReview {
		switch record.Status {
		case model.TaskStatusSucceeded:
			p.persistIterationFixResult(ctx, payload, record, iterateResult)
		case model.TaskStatusFailed:
			p.persistIterationFixFailure(ctx, payload, record.Error)
			p.sendIterationErrorNotification(ctx, record)
		}
	}

	// M6.1: fix_review 的完成通知由迭代层独立发送，不走 Processor 通用通知路径
	suppressNotification := payload.TaskType == model.TaskTypeFixReview

	// M6.1: review_pr 成功后检查迭代闭环
	if finalStatePersisted && !suppressNotification &&
		payload.TaskType == model.TaskTypeReviewPR &&
		record.Status == model.TaskStatusSucceeded &&
		reviewResult != nil && reviewResult.Review != nil &&
		p.enqueueHandler != nil {

		switch reviewResult.Review.Verdict {
		case review.VerdictRequestChanges:
			filteredReview := p.filteredReviewOutput(payload.RepoFullName, reviewResult.Review)
			if filteredReview.Verdict != review.VerdictRequestChanges {
				break
			}
			if p.enqueueHandler.AfterReviewCompleted(ctx, record, payload,
				reviewResult.Labels, filteredReview.Issues) {
				suppressNotification = true
			}
		case review.VerdictApprove:
			p.enqueueHandler.AfterIterationApproved(ctx, payload)
		}
	}

	if finalStatePersisted && !suppressNotification {
		p.sendCompletionNotification(ctx, record, reviewResult, fixResult, testResult, e2eResult, triageResult)
	}

	if triageDispatchErr != nil {
		if errors.Is(triageDispatchErr, errTriageDispatchPartialFailure) {
			return fmt.Errorf("triage_e2e 链式入队部分失败: %w", triageDispatchErr)
		}
		return fmt.Errorf("triage_e2e 链式入队失败: %v: %w", triageDispatchErr, asynq.SkipRetry)
	}
	if runErr != nil {
		return fmt.Errorf("任务执行失败: %w", runErr)
	}
	if result != nil && result.ExitCode != 0 {
		// 退出码 2 为确定性失败（如参数错误），跳过重试
		// 其他非零退出码允许 asynq 自动重试
		if result.ExitCode == 2 {
			return fmt.Errorf("任务确定性失败，退出码 %d: %w", result.ExitCode, asynq.SkipRetry)
		}
		return fmt.Errorf("任务执行失败，退出码 %d", result.ExitCode)
	}
	return nil
}

func (p *Processor) filteredReviewOutput(repoFullName string, output *review.ReviewOutput) *review.ReviewOutput {
	if output == nil || p.reviewService == nil {
		return output
	}
	cfg := p.reviewService.ResolveConfig(repoFullName)
	filtered, _ := review.ApplyFilters(output, cfg.Severity, cfg.IgnorePatterns)
	if filtered == nil {
		return output
	}
	return filtered
}

func (p *Processor) prepareFixReviewPayload(ctx context.Context, payload model.TaskPayload) (model.TaskPayload, error) {
	if payload.SessionID == 0 || payload.RoundNumber <= 1 {
		return payload, nil
	}
	payload.PreviousFixes = ""

	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	completedRounds, err := p.store.GetCompletedRoundsForSession(persistCtx, payload.SessionID)
	if err != nil {
		p.logger.WarnContext(ctx, "查询已完成迭代轮次失败，继续使用空 PreviousFixes",
			"session_id", payload.SessionID,
			"round", payload.RoundNumber,
			"error", err)
		return payload, nil
	}
	var fixes []iterate.FixSummary
	for _, r := range completedRounds {
		if r.RoundNumber >= payload.RoundNumber {
			continue
		}
		fixes = append(fixes, iterate.FixSummary{
			Round:       r.RoundNumber,
			IssuesFixed: r.IssuesFixed,
			Summary:     iterate.SanitizeFixReviewError(r.FixSummary),
		})
	}
	if len(fixes) == 0 {
		return payload, nil
	}
	data, err := json.Marshal(fixes)
	if err != nil {
		return payload, fmt.Errorf("%w: 序列化 PreviousFixes 失败: %v", iterate.ErrFixReviewDeterministicFailure, err)
	}
	payload.PreviousFixes = string(data)
	return payload, nil
}

func (p *Processor) persistIterationFixResult(ctx context.Context, payload model.TaskPayload, record *model.TaskRecord, result *iterate.FixReviewResult) {
	if payload.SessionID == 0 || payload.RoundNumber == 0 || record == nil || result == nil || result.Output == nil {
		return
	}

	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	round, err := p.store.GetIterationRound(persistCtx, payload.SessionID, payload.RoundNumber)
	if err != nil {
		p.logger.ErrorContext(ctx, "查询迭代轮次失败",
			"session_id", payload.SessionID, "round", payload.RoundNumber, "error", err)
		return
	}
	if round == nil {
		p.logger.WarnContext(ctx, "迭代轮次不存在，跳过修复结果落库",
			"session_id", payload.SessionID, "round", payload.RoundNumber)
		return
	}
	if round.FixTaskID != "" && round.FixTaskID != record.ID {
		p.logger.WarnContext(ctx, "迭代轮次 fix_task_id 不匹配，跳过修复结果落库",
			"session_id", payload.SessionID,
			"round", payload.RoundNumber,
			"round_fix_task_id", round.FixTaskID,
			"task_id", record.ID)
		return
	}

	alreadyCompleted := round.CompletedAt != nil
	issuesFixed := iterate.CountFixedIssues(result.Output)
	if !alreadyCompleted {
		now := time.Now()
		round.FixTaskID = record.ID
		round.IssuesFixed = issuesFixed
		round.FixSummary = iterate.SanitizeFixReviewError(result.Output.Summary)
		if strings.TrimSpace(round.FixReportPath) == "" {
			round.FixReportPath = payload.FixReportPath
		}
		if strings.TrimSpace(round.FixReportPath) == "" {
			round.FixReportPath = iterate.BuildReportPath("docs/review_history", payload.PRNumber, payload.RoundNumber)
		}
		round.CompletedAt = &now
		if err := p.store.UpdateIterationRound(persistCtx, round); err != nil {
			p.logger.ErrorContext(ctx, "更新迭代轮次修复结果失败",
				"session_id", payload.SessionID, "round", payload.RoundNumber, "error", err)
			return
		}
	}

	session, err := p.store.FindActiveIterationSession(persistCtx, payload.RepoFullName, payload.PRNumber)
	if err != nil {
		p.logger.ErrorContext(ctx, "查询迭代会话失败",
			"repo", payload.RepoFullName, "pr", payload.PRNumber, "error", err)
		return
	}
	if session == nil || session.ID != payload.SessionID {
		return
	}
	if !alreadyCompleted {
		session.TotalIssuesFixed += issuesFixed
	}
	if session.Status == "fixing" {
		session.Status = "reviewing"
	}
	session.LastError = ""
	if err := p.store.UpdateIterationSession(persistCtx, session); err != nil {
		p.logger.ErrorContext(ctx, "更新迭代会话修复统计失败",
			"session_id", session.ID, "error", err)
	}
}

func (p *Processor) persistIterationFixFailure(ctx context.Context, payload model.TaskPayload, errMsg string) {
	if payload.SessionID == 0 {
		return
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := p.store.FindActiveIterationSession(persistCtx, payload.RepoFullName, payload.PRNumber)
	if err != nil {
		p.logger.ErrorContext(ctx, "查询迭代会话失败",
			"repo", payload.RepoFullName, "pr", payload.PRNumber, "error", err)
		return
	}
	if session == nil || session.ID != payload.SessionID {
		return
	}
	session.Status = "idle"
	session.LastError = iterate.SanitizeFixReviewError(errMsg)
	if err := p.store.UpdateIterationSession(persistCtx, session); err != nil {
		p.logger.ErrorContext(ctx, "更新迭代会话失败状态失败",
			"session_id", session.ID, "error", err)
	}
}

func (p *Processor) persistIterationZeroFixResult(ctx context.Context, payload model.TaskPayload, record *model.TaskRecord, summary string) {
	if payload.SessionID == 0 || payload.RoundNumber == 0 || record == nil {
		return
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	round, err := p.store.GetIterationRound(persistCtx, payload.SessionID, payload.RoundNumber)
	if err != nil {
		p.logger.ErrorContext(ctx, "查询迭代轮次失败",
			"session_id", payload.SessionID, "round", payload.RoundNumber, "error", err)
		return
	}
	if round == nil {
		return
	}
	if round.FixTaskID != "" && round.FixTaskID != record.ID {
		p.logger.WarnContext(ctx, "迭代轮次 fix_task_id 不匹配，跳过零修复结果落库",
			"session_id", payload.SessionID,
			"round", payload.RoundNumber,
			"round_fix_task_id", round.FixTaskID,
			"task_id", record.ID)
		return
	}

	if round.CompletedAt == nil {
		now := time.Now()
		round.FixTaskID = record.ID
		round.IssuesFixed = 0
		round.FixSummary = iterate.SanitizeFixReviewError(summary)
		if strings.TrimSpace(round.FixReportPath) == "" {
			round.FixReportPath = payload.FixReportPath
		}
		if strings.TrimSpace(round.FixReportPath) == "" {
			round.FixReportPath = iterate.BuildReportPath("docs/review_history", payload.PRNumber, payload.RoundNumber)
		}
		round.CompletedAt = &now
		if err := p.store.UpdateIterationRound(persistCtx, round); err != nil {
			p.logger.ErrorContext(ctx, "更新迭代轮次零修复结果失败",
				"session_id", payload.SessionID, "round", payload.RoundNumber, "error", err)
			return
		}
	}

	session, err := p.store.FindActiveIterationSession(persistCtx, payload.RepoFullName, payload.PRNumber)
	if err != nil {
		p.logger.ErrorContext(ctx, "查询迭代会话失败",
			"repo", payload.RepoFullName, "pr", payload.PRNumber, "error", err)
		return
	}
	if session == nil || session.ID != payload.SessionID {
		return
	}
	if session.Status == "fixing" {
		session.Status = "reviewing"
	}
	session.LastError = iterate.SanitizeFixReviewError(summary)
	if err := p.store.UpdateIterationSession(persistCtx, session); err != nil {
		p.logger.ErrorContext(ctx, "更新迭代会话零修复状态失败",
			"session_id", session.ID, "error", err)
	}
}

func (p *Processor) sendIterationErrorNotification(ctx context.Context, record *model.TaskRecord) {
	if p.notifier == nil || record == nil {
		return
	}
	payload := record.Payload
	if payload.RepoOwner == "" || payload.RepoName == "" || payload.PRNumber <= 0 {
		return
	}

	body := fmt.Sprintf("PR #%d 迭代修复异常\n\n仓库：%s\n任务：%s\n状态：%s",
		payload.PRNumber, payload.RepoFullName, record.ID, record.Status)
	if record.Error != "" {
		body += fmt.Sprintf("\n错误：%s", record.Error)
	}
	msg := notify.Message{
		EventType: notify.EventIterationError,
		Severity:  notify.SeverityWarning,
		Target:    buildPRTarget(payload),
		Title:     fmt.Sprintf("PR #%d 迭代修复异常", payload.PRNumber),
		Body:      body,
		Metadata: map[string]string{
			notify.MetaKeyIterationRound:     fmt.Sprintf("%d", payload.RoundNumber),
			notify.MetaKeyIterationMaxRounds: fmt.Sprintf("%d", payload.IterationMaxRounds),
			notify.MetaKeyIterationSessionID: fmt.Sprintf("%d", payload.SessionID),
			notify.MetaKeyTaskStatus:         string(record.Status),
			notify.MetaKeyNotifyTime:         formatNotifyTime(),
		},
	}
	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	if err := p.notifier.Send(notifyCtx, msg); err != nil {
		p.logger.ErrorContext(ctx, "发送迭代异常通知失败",
			"task_id", record.ID,
			"error", err)
	}
}

// buildPRURL 基于 Gitea 配置构造 PR 页面链接。
// 调用方（WithGiteaBaseURL）已保证 giteaBaseURL 无尾斜杠。
func buildPRURL(giteaBaseURL string, payload model.TaskPayload) string {
	return fmt.Sprintf("%s/%s/%s/pulls/%d",
		giteaBaseURL,
		payload.RepoOwner,
		payload.RepoName,
		payload.PRNumber,
	)
}

// buildPRMetadata 构造 PR 相关通知的公共 metadata 字段
func (p *Processor) buildPRMetadata(payload model.TaskPayload) map[string]string {
	metadata := map[string]string{}
	if p.giteaBaseURL != "" && payload.PRNumber > 0 {
		metadata[notify.MetaKeyPRURL] = buildPRURL(p.giteaBaseURL, payload)
	}
	if payload.PRTitle != "" {
		metadata[notify.MetaKeyPRTitle] = payload.PRTitle
	}
	return metadata
}

// buildPRTarget 构造 PR 相关通知的 Target
func buildPRTarget(payload model.TaskPayload) notify.Target {
	return notify.Target{
		Owner:  payload.RepoOwner,
		Repo:   payload.RepoName,
		Number: payload.PRNumber,
		IsPR:   payload.PRNumber > 0,
	}
}

// formatIssueSummary 从 ReviewIssue 列表生成 issue 统计摘要
func formatIssueSummary(issues []review.ReviewIssue) string {
	if len(issues) == 0 {
		return ""
	}
	counts := make(map[string]int)
	for _, issue := range issues {
		severity := strings.ToUpper(issue.Severity)
		if severity == "" {
			severity = "UNKNOWN"
		}
		counts[severity]++
	}
	var parts []string
	for _, sev := range []string{"CRITICAL", "ERROR", "WARNING", "INFO"} {
		if c, ok := counts[sev]; ok {
			parts = append(parts, fmt.Sprintf("%d %s", c, sev))
		}
	}
	return strings.Join(parts, ", ")
}

func (p *Processor) sendStartNotification(ctx context.Context, payload model.TaskPayload) {
	if p.notifier == nil {
		return
	}
	msg, ok := p.buildStartMessage(payload)
	if !ok {
		return
	}
	if err := p.notifier.Send(ctx, msg); err != nil {
		p.logger.ErrorContext(ctx, "发送任务开始通知失败",
			"task_type", payload.TaskType,
			"error", err,
		)
	}
}

func (p *Processor) buildStartMessage(payload model.TaskPayload) (notify.Message, bool) {
	if payload.RepoOwner == "" || payload.RepoName == "" {
		return notify.Message{}, false
	}

	var msg notify.Message
	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		if payload.PRNumber <= 0 {
			return notify.Message{}, false
		}
		msg = notify.Message{
			EventType: notify.EventPRReviewStarted,
			Severity:  notify.SeverityInfo,
			Target:    buildPRTarget(payload),
			Title:     "PR 自动评审开始",
			Body:      fmt.Sprintf("正在评审 PR #%d\n\n仓库：%s", payload.PRNumber, payload.RepoFullName),
			Metadata:  p.buildPRMetadata(payload),
		}
	case model.TaskTypeAnalyzeIssue:
		if payload.IssueNumber <= 0 {
			return notify.Message{}, false
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata[notify.MetaKeyIssueURL] = fmt.Sprintf("%s/%s/%s/issues/%d",
				p.giteaBaseURL, payload.RepoOwner, payload.RepoName, payload.IssueNumber)
		}
		msg = notify.Message{
			EventType: notify.EventIssueAnalyzeStarted,
			Severity:  notify.SeverityInfo,
			Target: notify.Target{
				Owner:  payload.RepoOwner,
				Repo:   payload.RepoName,
				Number: payload.IssueNumber,
				IsPR:   false,
			},
			Title:    "Issue 自动分析开始",
			Body:     fmt.Sprintf("正在分析 Issue #%d\n\n仓库：%s", payload.IssueNumber, payload.RepoFullName),
			Metadata: metadata,
		}
	case model.TaskTypeFixIssue:
		if payload.IssueNumber <= 0 {
			return notify.Message{}, false
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata[notify.MetaKeyIssueURL] = fmt.Sprintf("%s/%s/%s/issues/%d",
				p.giteaBaseURL, payload.RepoOwner, payload.RepoName, payload.IssueNumber)
		}
		msg = notify.Message{
			EventType: notify.EventIssueFixStarted,
			Severity:  notify.SeverityInfo,
			Target: notify.Target{
				Owner:  payload.RepoOwner,
				Repo:   payload.RepoName,
				Number: payload.IssueNumber,
				IsPR:   false,
			},
			Title:    "Issue 自动修复开始",
			Body:     fmt.Sprintf("正在修复 Issue #%d\n\n仓库：%s", payload.IssueNumber, payload.RepoFullName),
			Metadata: metadata,
		}
	case model.TaskTypeGenTests:
		// gen_tests 的 fail-open 判据为 RepoFullName 非空（非 Number>0：整仓
		// 生成模式下 module 可空，不应因此屏蔽通知）。
		if payload.RepoFullName == "" {
			return notify.Message{}, false
		}
		metadata := map[string]string{
			notify.MetaKeyModule:    genTestsModuleLabel(payload.Module),
			notify.MetaKeyFramework: payload.Framework,
		}
		msg = notify.Message{
			EventType: notify.EventGenTestsStarted,
			Severity:  notify.SeverityInfo,
			Target: notify.Target{
				Owner: payload.RepoOwner,
				Repo:  payload.RepoName,
				IsPR:  false,
			},
			Title:    "自动测试生成开始",
			Body:     fmt.Sprintf("正在生成测试\n\n仓库：%s\nmodule：%s", payload.RepoFullName, genTestsModuleLabel(payload.Module)),
			Metadata: metadata,
		}
	case model.TaskTypeRunE2E:
		if payload.RepoFullName == "" {
			return notify.Message{}, false
		}
		metadata := map[string]string{}
		if payload.Environment != "" {
			metadata[notify.MetaKeyE2EEnv] = payload.Environment
		}
		if payload.Module != "" {
			metadata[notify.MetaKeyE2EModule] = payload.Module
		}
		if payload.BaseRef != "" {
			metadata[notify.MetaKeyE2EBaseRef] = payload.BaseRef
		}
		msg = notify.Message{
			EventType: notify.EventE2EStarted,
			Severity:  notify.SeverityInfo,
			Target: notify.Target{
				Owner: payload.RepoOwner,
				Repo:  payload.RepoName,
				IsPR:  false,
			},
			Title:    "E2E 测试开始",
			Body:     fmt.Sprintf("正在执行 E2E 测试\n\n仓库：%s", payload.RepoFullName),
			Metadata: metadata,
		}
	case model.TaskTypeTriageE2E:
		if payload.RepoFullName == "" {
			return notify.Message{}, false
		}
		metadata := p.buildPRMetadata(payload)
		msg = notify.Message{
			EventType: notify.EventE2ETriageStarted,
			Severity:  notify.SeverityInfo,
			Target:    buildPRTarget(payload),
			Title:     "E2E 回归分析开始",
			Body:      fmt.Sprintf("正在分析变更影响的 E2E 模块\n\n仓库：%s", payload.RepoFullName),
			Metadata:  metadata,
		}
	case model.TaskTypeFixReview:
		// 迭代修复的开始通知由迭代层管理，Processor 跳过
		return notify.Message{}, false
	default:
		return notify.Message{}, false
	}

	// 公共路径：统一注入通知时间
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata[notify.MetaKeyNotifyTime] = formatNotifyTime()
	return msg, true
}

// sendCompletionNotification 在任务达到最终状态且状态已持久化后发送完成通知。
// 内部创建独立后台 context，与 asynq 任务 ctx 的生命周期解耦，
// 确保即使 asynq ctx 已过期（如任务超时）仍能发出通知。
func (p *Processor) sendCompletionNotification(ctx context.Context, record *model.TaskRecord, reviewResult *review.ReviewResult, fixResult *fix.FixResult, testResult *test.TestGenResult, e2eResult *e2e.E2EResult, triageResult *e2e.TriageE2EOutput) {
	if p.notifier == nil || record == nil {
		return
	}
	msg, ok := p.buildNotificationMessage(record, reviewResult, fixResult, testResult, e2eResult, triageResult)
	if !ok {
		// 主消息未构建成功（例如 gen_tests 缺 RepoFullName）仍尝试 Warnings 追加——
		// 但无主消息时 Warnings 也无处附着，直接返回。
		p.sendTestGenWarnings(ctx, record, testResult)
		return
	}
	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	if err := p.notifier.Send(notifyCtx, msg); err != nil {
		p.logger.ErrorContext(ctx, "发送任务完成通知失败",
			"task_id", record.ID,
			"status", record.Status,
			"error", err,
		)
	}
	// 主消息发送后，如果 gen_tests 产出含 Warnings（例如分支强制对齐失败），
	// 追加一条独立的 Warning 消息，保持主消息干净 + 告警信息可达。
	p.sendTestGenWarnings(ctx, record, testResult)
	p.syncGenTestsPRComment(ctx, record, testResult)
}

// buildNotificationMessage 构建任务完成通知消息。
// M4.2：新增 testResult 形参，用于 gen_tests 任务（EventGenTestsDone / EventGenTestsFailed）。
// M5.1：新增 e2eResult 形参，用于 run_e2e 任务（EventE2EDone / EventE2EFailed）。
// M5.4：新增 triageResult 形参，用于 triage_e2e 任务（EventE2ETriageDone / EventE2ETriageFailed）。
// 其它类型调用时传 nil。
func (p *Processor) buildNotificationMessage(record *model.TaskRecord, reviewResult *review.ReviewResult, fixResult *fix.FixResult, testResult *test.TestGenResult, e2eResult *e2e.E2EResult, triageResult *e2e.TriageE2EOutput) (notify.Message, bool) {
	if record == nil {
		return notify.Message{}, false
	}
	switch record.Status {
	case model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusRetrying:
		// 这三种状态都需要发送通知
	default:
		return notify.Message{}, false
	}

	payload := record.Payload
	if payload.RepoOwner == "" || payload.RepoName == "" {
		return notify.Message{}, false
	}

	// 构建通知正文
	var body string
	if record.Status == model.TaskStatusRetrying {
		body = fmt.Sprintf("任务执行失败，即将重试\n\n仓库：%s\n任务类型：%s", payload.RepoFullName, payload.TaskType)
	} else {
		body = fmt.Sprintf("任务 %s 执行完成\n\n仓库：%s\n任务类型：%s\n状态：%s", record.ID, payload.RepoFullName, payload.TaskType, record.Status)
	}
	if record.Error != "" {
		body += fmt.Sprintf("\n错误：%s", record.Error)
	}

	var msg notify.Message
	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		if payload.PRNumber <= 0 {
			return notify.Message{}, false
		}
		metadata := p.buildPRMetadata(payload)
		if reviewResult != nil && reviewResult.Review != nil {
			metadata[notify.MetaKeyVerdict] = string(reviewResult.Review.Verdict)
			metadata[notify.MetaKeyIssueSummary] = formatIssueSummary(reviewResult.Review.Issues)
		}
		if record.Status == model.TaskStatusRetrying {
			metadata[notify.MetaKeyRetryCount] = fmt.Sprintf("%d", record.RetryCount+1)
			metadata[notify.MetaKeyMaxRetry] = fmt.Sprintf("%d", record.MaxRetry)
			metadata[notify.MetaKeyTaskStatus] = string(record.Status)
		}
		target := buildPRTarget(payload)
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventPRReviewDone,
				Severity:  notify.SeverityInfo,
				Target:    target,
				Title:     "PR 自动评审任务完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "PR 自动评审重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default: // failed
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "PR 自动评审任务失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeAnalyzeIssue:
		if payload.IssueNumber <= 0 {
			return notify.Message{}, false
		}
		issueTarget := notify.Target{
			Owner:  payload.RepoOwner,
			Repo:   payload.RepoName,
			Number: payload.IssueNumber,
			IsPR:   false,
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata[notify.MetaKeyIssueURL] = fmt.Sprintf("%s/%s/%s/issues/%d",
				p.giteaBaseURL, payload.RepoOwner, payload.RepoName, payload.IssueNumber)
		}
		if record.Status == model.TaskStatusRetrying {
			metadata[notify.MetaKeyRetryCount] = fmt.Sprintf("%d", record.RetryCount+1)
			metadata[notify.MetaKeyMaxRetry] = fmt.Sprintf("%d", record.MaxRetry)
			metadata[notify.MetaKeyTaskStatus] = string(record.Status)
		}
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventIssueAnalyzeDone,
				Severity:  notify.SeverityInfo,
				Target:    issueTarget,
				Title:     "Issue 自动分析完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    issueTarget,
				Title:     "Issue 自动分析重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default: // failed
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    issueTarget,
				Title:     "Issue 自动分析失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeFixIssue:
		if payload.IssueNumber <= 0 {
			return notify.Message{}, false
		}
		issueTarget := notify.Target{
			Owner:  payload.RepoOwner,
			Repo:   payload.RepoName,
			Number: payload.IssueNumber,
			IsPR:   false,
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata[notify.MetaKeyIssueURL] = fmt.Sprintf("%s/%s/%s/issues/%d",
				p.giteaBaseURL, payload.RepoOwner, payload.RepoName, payload.IssueNumber)
		}
		if record.Status == model.TaskStatusRetrying {
			metadata[notify.MetaKeyRetryCount] = fmt.Sprintf("%d", record.RetryCount+1)
			metadata[notify.MetaKeyMaxRetry] = fmt.Sprintf("%d", record.MaxRetry)
			metadata[notify.MetaKeyTaskStatus] = string(record.Status)
		}
		if record.Status == model.TaskStatusSucceeded && fixResult != nil && fixResult.Fix != nil && fixResult.PRNumber > 0 {
			metadata[notify.MetaKeyPRURL] = fixResult.PRURL
			metadata[notify.MetaKeyPRNumber] = fmt.Sprintf("%d", fixResult.PRNumber)
			metadata[notify.MetaKeyModifiedFiles] = fmt.Sprintf("%d", len(fixResult.Fix.ModifiedFiles))
		}
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventFixIssueDone,
				Severity:  notify.SeverityInfo,
				Target:    issueTarget,
				Title:     "Issue 自动修复任务完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    issueTarget,
				Title:     "Issue 自动修复重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default: // failed
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    issueTarget,
				Title:     "Issue 自动修复任务失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeGenTests:
		// gen_tests 不绑定具体 PR/Issue 编号，Target 以整仓为单位。module 与 framework
		// 通过 metadata 透传；Severity 依 FailureCategory 区分。
		genTarget := notify.Target{
			Owner: payload.RepoOwner,
			Repo:  payload.RepoName,
			IsPR:  false,
		}
		metadata := p.buildGenTestsMetadata(payload, testResult)
		if record.Status == model.TaskStatusRetrying {
			metadata[notify.MetaKeyRetryCount] = fmt.Sprintf("%d", record.RetryCount+1)
			metadata[notify.MetaKeyMaxRetry] = fmt.Sprintf("%d", record.MaxRetry)
			metadata[notify.MetaKeyTaskStatus] = string(record.Status)
		}
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventGenTestsDone,
				Severity:  notify.SeverityInfo,
				Target:    genTarget,
				Title:     "自动测试生成完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			// 对齐 review/fix：重试中统一走 EventSystemError + Warning severity
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    genTarget,
				Title:     "自动测试生成重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default: // failed
			severity, title := genTestsFailedSeverity(testResult)
			msg = notify.Message{
				EventType: notify.EventGenTestsFailed,
				Severity:  severity,
				Target:    genTarget,
				Title:     title,
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeRunE2E:
		if payload.RepoFullName == "" {
			return notify.Message{}, false
		}
		metadata := map[string]string{}
		if payload.Environment != "" {
			metadata[notify.MetaKeyE2EEnv] = payload.Environment
		}
		if e2eResult != nil && e2eResult.Output != nil {
			metadata[notify.MetaKeyE2ETotalCases] = fmt.Sprintf("%d", e2eResult.Output.TotalCases)
			metadata[notify.MetaKeyE2EPassedCases] = fmt.Sprintf("%d", e2eResult.Output.PassedCases)
			metadata[notify.MetaKeyE2EFailedCases] = fmt.Sprintf("%d", e2eResult.Output.FailedCases)
			metadata[notify.MetaKeyE2EErrorCases] = fmt.Sprintf("%d", e2eResult.Output.ErrorCases)
			metadata[notify.MetaKeyE2ESkippedCases] = fmt.Sprintf("%d", e2eResult.Output.SkippedCases)

			// 构建失败用例列表 JSON
			if e2eResult.Output.FailedCases > 0 || e2eResult.Output.ErrorCases > 0 {
				type failedItem struct {
					Name     string `json:"name"`
					Category string `json:"category"`
					Analysis string `json:"analysis"`
				}
				var items []failedItem
				for _, c := range e2eResult.Output.Cases {
					if c.Status != "failed" && c.Status != "error" {
						continue
					}
					analysis := c.FailureAnalysis
					if len([]rune(analysis)) > 80 {
						analysis = string([]rune(analysis)[:80]) + "..."
					}
					items = append(items, failedItem{
						Name:     c.Module + "/" + c.Name,
						Category: c.FailureCategory,
						Analysis: analysis,
					})
				}
				if data, err := json.Marshal(items); err == nil {
					metadata[notify.MetaKeyE2EFailedList] = string(data)
				}
			}
		}
		// M5.2: 已创建的 Issue 号
		if e2eResult != nil && len(e2eResult.CreatedIssues) > 0 {
			var nums []string
			for _, n := range e2eResult.CreatedIssues {
				nums = append(nums, fmt.Sprintf("%d", n))
			}
			sort.Strings(nums)
			metadata[notify.MetaKeyE2ECreatedIssues] = strings.Join(nums, ",")
		}
		target := notify.Target{
			Owner: payload.RepoOwner,
			Repo:  payload.RepoName,
			IsPR:  false,
		}
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventE2EDone,
				Severity:  notify.SeverityInfo,
				Target:    target,
				Title:     "E2E 测试完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventE2EFailed,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "E2E 测试重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default:
			msg = notify.Message{
				EventType: notify.EventE2EFailed,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "E2E 测试失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeTriageE2E:
		if payload.RepoFullName == "" {
			return notify.Message{}, false
		}
		metadata := p.buildPRMetadata(payload)
		if triageResult != nil {
			if data, err := json.Marshal(triageResult.Modules); err == nil {
				metadata[notify.MetaKeyTriageModules] = string(data)
			}
			if data, err := json.Marshal(triageResult.SkippedModules); err == nil {
				metadata[notify.MetaKeyTriageSkippedModules] = string(data)
			}
			if triageResult.Analysis != "" {
				metadata[notify.MetaKeyTriageAnalysis] = triageResult.Analysis
			}
		}
		target := buildPRTarget(payload)
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventE2ETriageDone,
				Severity:  notify.SeverityInfo,
				Target:    target,
				Title:     "E2E 回归分析完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventE2ETriageFailed,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "E2E 回归分析重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default:
			msg = notify.Message{
				EventType: notify.EventE2ETriageFailed,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "E2E 回归分析失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	default:
		return notify.Message{}, false
	}

	// 公共路径：统一注入通知时间和耗时
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata[notify.MetaKeyNotifyTime] = formatNotifyTime()
	if record.Status == model.TaskStatusSucceeded &&
		record.StartedAt != nil && record.CompletedAt != nil {
		msg.Metadata[notify.MetaKeyDuration] = formatDuration(
			record.CompletedAt.Sub(*record.StartedAt))
	}
	return msg, true
}

// adaptFixResult 将 fix.FixResult 适配为 worker.ExecutionResult。
// M3.3: ParseError 仅保留信息到 Error 字段供调试，不再导致 ExitCode=1。
// fix 解析失败的降级评论由 processor 在重试耗尽后统一触发。
// CLIMeta.IsError 仍保留 ExitCode=1（与 review 包对齐）。
func adaptFixResult(r *fix.FixResult) *worker.ExecutionResult {
	if r == nil {
		return &worker.ExecutionResult{ExitCode: 0}
	}
	res := &worker.ExecutionResult{
		Output:   r.RawOutput,
		ExitCode: 0,
	}
	if r.ExitCode != 0 {
		res.ExitCode = r.ExitCode
		res.Error = fmt.Sprintf("fix worker 退出码非零: %d", r.ExitCode)
	}
	if r.CLIMeta != nil {
		res.Duration = r.CLIMeta.DurationMs
		if r.CLIMeta.IsError {
			res.ExitCode = 1
			res.Error = "Claude CLI 报告错误"
		}
	}
	// ParseError 信息保留到 Error 字段供调试，但不影响退出码
	if r.ParseError != nil {
		if res.Error == "" {
			res.Error = r.ParseError.Error()
		} else if !strings.Contains(res.Error, r.ParseError.Error()) {
			res.Error = res.Error + "; " + r.ParseError.Error()
		}
	}
	// fix 流程的用户可见结果只有 Issue 评论；回写失败必须失败并允许重试。
	if r.WritebackError != nil {
		msg := fmt.Sprintf("回写失败: %v", r.WritebackError)
		if res.ExitCode == 0 {
			res.ExitCode = 1
		}
		if res.Error == "" {
			res.Error = msg
		} else if !strings.Contains(res.Error, msg) {
			res.Error = res.Error + "; " + msg
		}
	}
	return res
}

// adaptReviewResult 将 review.ReviewResult 适配为 worker.ExecutionResult
func adaptReviewResult(r *review.ReviewResult) *worker.ExecutionResult {
	if r == nil {
		return nil
	}
	result := &worker.ExecutionResult{
		ExitCode: 0,
		Output:   r.RawOutput,
	}
	if r.CLIMeta != nil {
		result.Duration = r.CLIMeta.DurationMs
		if r.CLIMeta.IsError {
			result.ExitCode = 1
			result.Error = "Claude CLI 报告错误"
		}
	}
	// 保留 ParseError 信息到任务记录，便于调试（优雅降级场景）
	if r.ParseError != nil && result.Error == "" {
		result.Error = r.ParseError.Error()
	}
	// WritebackError 不影响任务退出码，但需要保留到任务记录供调试。
	if r.WritebackError != nil {
		msg := fmt.Sprintf("回写失败: %v", r.WritebackError)
		if result.Error == "" {
			result.Error = msg
		} else if !strings.Contains(result.Error, msg) {
			result.Error = result.Error + "; " + msg
		}
	}
	return result
}

// genTestsModuleLabel gen_tests 通知里 module 字段的显示值：空串回落 "all"
// 以呼应 test.ModuleKey 的语义，避免飞书卡片出现空值。
func genTestsModuleLabel(module string) string {
	if strings.TrimSpace(module) == "" {
		return "all"
	}
	return module
}

// buildGenTestsMetadata 为 gen_tests 通知消息构造 metadata。
// testResult 为 nil 时仅回填 payload 维度字段（module / framework）；有输出时
// 追加 PR 元数据 / 文件计数 / failure_category（若有）。
func (p *Processor) buildGenTestsMetadata(payload model.TaskPayload, testResult *test.TestGenResult) map[string]string {
	metadata := map[string]string{
		notify.MetaKeyModule: genTestsModuleLabel(payload.Module),
	}
	framework := payload.Framework
	if testResult != nil && testResult.Framework != "" {
		framework = string(testResult.Framework)
	}
	if framework != "" {
		metadata[notify.MetaKeyFramework] = framework
	}
	if testResult == nil {
		return metadata
	}
	if testResult.PRURL != "" {
		metadata[notify.MetaKeyPRURL] = testResult.PRURL
	}
	if testResult.PRNumber > 0 {
		metadata[notify.MetaKeyPRNumber] = fmt.Sprintf("%d", testResult.PRNumber)
	}
	out := testResult.Output
	if out == nil {
		return metadata
	}
	metadata[notify.MetaKeyGeneratedCount] = fmt.Sprintf("%d", len(out.GeneratedFiles))
	metadata[notify.MetaKeyCommittedCount] = fmt.Sprintf("%d", len(out.CommittedFiles))
	metadata[notify.MetaKeySkippedCount] = fmt.Sprintf("%d", len(out.SkippedTargets))
	if out.FailureCategory != "" && out.FailureCategory != test.FailureCategoryNone {
		metadata[notify.MetaKeyFailureCategory] = string(out.FailureCategory)
	}
	return metadata
}

// genTestsFailedSeverity 按 FailureCategory 映射失败通知的 severity 与标题。
// 映射来源 §4.9.4：
//   - infrastructure   → Warning，"基础设施故障"
//   - test_quality     → Info，"测试质量未达标"
//   - info_insufficient → Info，"生成信息不足"
//   - 未知枚举值 / nil → Info，"自动测试生成失败"（默认 Info 避免误触发运维告警）
//   - testResult 为 nil（极端情况，parseResult 未产出）→ Warning，保留可见性
func genTestsFailedSeverity(testResult *test.TestGenResult) (notify.Severity, string) {
	if testResult == nil || testResult.Output == nil {
		return notify.SeverityWarning, "自动测试生成失败"
	}
	switch testResult.Output.FailureCategory {
	case test.FailureCategoryInfrastructure:
		return notify.SeverityWarning, "基础设施故障"
	case test.FailureCategoryTestQuality:
		return notify.SeverityInfo, "测试质量未达标"
	case test.FailureCategoryInfoInsufficient:
		return notify.SeverityInfo, "生成信息不足"
	default:
		// M7：未知 FailureCategory（例如未来 Claude 新增分类但后端未升级）默认走 Info，
		// 避免误触发运维分页。若真的是基础设施问题会由 infrastructure 枚举命中。
		return notify.SeverityInfo, "自动测试生成失败"
	}
}

// sendTestGenWarnings 若 gen_tests 产出含 Warnings（例如 entrypoint 写的
// AUTO_TEST_BRANCH_RESET_REMOTE_FAILED 提示），追加一条独立的 Warning 消息。
// 与主消息拆分的原因：主消息在 Succeeded 时走 Info 语义，Warnings 作为附加
// 告警需单独 Severity，便于飞书卡片着色 / 运维专人处理。
func (p *Processor) sendTestGenWarnings(ctx context.Context, record *model.TaskRecord, testResult *test.TestGenResult) {
	if p.notifier == nil || record == nil || testResult == nil || testResult.Output == nil {
		return
	}
	// 仅在最终态追加 Warnings 消息：retrying 态下 Warnings 可能随重试反复出现，
	// 每次都发会让运维误以为出了事；主消息已走 EventSystemError 通道。
	if record.Status != model.TaskStatusSucceeded && record.Status != model.TaskStatusFailed {
		return
	}
	warnings := testResult.Output.Warnings
	if len(warnings) == 0 {
		return
	}
	payload := record.Payload
	if payload.RepoOwner == "" || payload.RepoName == "" {
		return
	}
	metadata := p.buildGenTestsMetadata(payload, testResult)
	metadata[notify.MetaKeyNotifyTime] = formatNotifyTime()
	body := "gen_tests 产出附加告警：\n- " + strings.Join(warnings, "\n- ")
	msg := notify.Message{
		EventType: notify.EventGenTestsFailed,
		Severity:  notify.SeverityWarning,
		Target: notify.Target{
			Owner: payload.RepoOwner,
			Repo:  payload.RepoName,
			IsPR:  false,
		},
		Title:    "自动测试生成存在告警",
		Body:     body,
		Metadata: metadata,
	}
	if err := p.notifier.Send(ctx, msg); err != nil {
		p.logger.ErrorContext(ctx, "发送 gen_tests 附加告警消息失败",
			"task_id", record.ID,
			"error", err,
		)
	}
}

// syncGenTestsPRComment 在 gen_tests 最终态时，把最新结果 upsert 到目标 PR 评论中。
//
// 与通用 Router.Send 分离的原因：
//   - gen_tests started/done/failed 属于 repo 级通知，目标可能没有 PR/Issue 编号
//   - gen_tests PR 评论是显式的 Gitea 写回能力，应直接走专用接口，而非经路由配置
func (p *Processor) syncGenTestsPRComment(ctx context.Context, record *model.TaskRecord, testResult *test.TestGenResult) {
	if p.notifier == nil || record == nil || testResult == nil || testResult.Output == nil {
		return
	}
	if record.Payload.TaskType != model.TaskTypeGenTests {
		return
	}
	if record.Status != model.TaskStatusSucceeded && record.Status != model.TaskStatusFailed {
		return
	}
	if testResult.PRNumber <= 0 {
		return
	}
	payload := record.Payload
	if payload.RepoOwner == "" || payload.RepoName == "" {
		return
	}
	commenter, ok := p.notifier.(GenTestsPRCommenter)
	if !ok {
		return
	}

	body := test.FormatTestGenPRBody(testResult.Output, payload, string(testResult.Framework))
	if err := commenter.CommentOnGenTestsPR(ctx, payload.RepoOwner, payload.RepoName, testResult.PRNumber, body); err != nil {
		p.logger.ErrorContext(ctx, "同步 gen_tests PR 评论失败",
			"task_id", record.ID,
			"repo", payload.RepoFullName,
			"pr_number", testResult.PRNumber,
			"error", err,
		)
	}
}

func adaptE2EResult(r *e2e.E2EResult) *worker.ExecutionResult {
	if r == nil {
		return nil
	}
	exitCode := 0
	errMsg := ""
	if r.Output != nil && !r.Output.Success {
		exitCode = 1
		if isDeterministicE2EFailure(r.Output) {
			exitCode = 2
		}
		errMsg = fmt.Sprintf("E2E 测试未通过: total=%d passed=%d failed=%d error=%d skipped=%d",
			r.Output.TotalCases,
			r.Output.PassedCases,
			r.Output.FailedCases,
			r.Output.ErrorCases,
			r.Output.SkippedCases,
		)
	}
	return &worker.ExecutionResult{
		ExitCode: exitCode,
		Output:   r.RawOutput,
		Duration: r.DurationMs,
		Error:    errMsg,
	}
}

func isDeterministicE2EFailure(o *e2e.E2EOutput) bool {
	if o == nil {
		return false
	}
	hasFailure := false
	for _, c := range o.Cases {
		switch c.FailureCategory {
		case "environment":
			return false
		case "bug", "script_outdated":
			hasFailure = true
		}
	}
	return hasFailure && o.ErrorCases == 0
}

// adaptTestResult 将 test.TestGenResult 适配为 worker.ExecutionResult。
// ParseError 仅保留到 Error 字段，不直接改写退出码；真正的重试策略由 Processor
// 依据 ErrTestGenParseFailure 判断。
func adaptTestResult(r *test.TestGenResult) *worker.ExecutionResult {
	if r == nil {
		return &worker.ExecutionResult{ExitCode: 0}
	}
	res := &worker.ExecutionResult{
		Output:   r.RawOutput,
		ExitCode: 0,
	}
	if r.ExitCode != 0 {
		res.ExitCode = r.ExitCode
		res.Error = fmt.Sprintf("gen_tests worker 退出码非零: %d", r.ExitCode)
	}
	if r.CLIMeta != nil {
		res.Duration = r.CLIMeta.DurationMs
		if r.CLIMeta.IsError {
			res.ExitCode = 1
			res.Error = "Claude CLI 报告错误"
		}
	}
	if r.ParseError != nil {
		if res.Error == "" {
			res.Error = r.ParseError.Error()
		} else if !strings.Contains(res.Error, r.ParseError.Error()) {
			res.Error = res.Error + "; " + r.ParseError.Error()
		}
	}
	if res.Output == "" && r.Output != nil {
		if data, err := json.Marshal(r.Output); err == nil {
			res.Output = string(data)
		}
	}
	return res
}

// handleSkipRetryFailure 处理确定性失败（如 PR/Issue 不处于 open 状态），
// 标记任务 failed、尽可能保留结构化结果、持久化、发送通知，并返回 SkipRetry 错误。
func (p *Processor) handleSkipRetryFailure(ctx context.Context, record *model.TaskRecord, runErr error, reviewResult *review.ReviewResult, fixResult *fix.FixResult, testResult *test.TestGenResult, logMsg string) error {
	record.Status = model.TaskStatusFailed
	if record.Payload.TaskType == model.TaskTypeGenTests || record.Payload.TaskType == model.TaskTypeRunE2E || record.Payload.TaskType == model.TaskTypeTriageE2E || record.Payload.TaskType == model.TaskTypeFixReview {
		record.Error = test.SanitizeErrorMessage(runErr.Error())
	} else {
		record.Error = runErr.Error()
	}
	switch {
	case reviewResult != nil:
		if result := adaptReviewResult(reviewResult); result != nil {
			record.Result = result.Output
		}
	case fixResult != nil:
		if result := adaptFixResult(fixResult); result != nil {
			record.Result = result.Output
		}
	case testResult != nil:
		if result := adaptTestResult(testResult); result != nil {
			record.Result = result.Output
		}
	}
	completedAt := time.Now()
	record.CompletedAt = &completedAt
	p.logger.WarnContext(ctx, logMsg,
		"task_id", record.ID,
		"error", runErr,
	)
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()
	if err := p.store.UpdateTask(persistCtx, record); err != nil {
		p.logger.ErrorContext(ctx, "更新任务最终状态失败",
			"task_id", record.ID,
			"status", record.Status,
			"error", err,
		)
	} else if record.Payload.TaskType == model.TaskTypeFixReview {
		p.persistIterationFixFailure(ctx, record.Payload, record.Error)
		p.sendIterationErrorNotification(ctx, record)
	} else {
		p.sendCompletionNotification(ctx, record, reviewResult, fixResult, testResult, nil, nil)
	}
	return fmt.Errorf("%s: %w", logMsg, asynq.SkipRetry)
}

func (p *Processor) handleIterationQualityFailure(ctx context.Context, record *model.TaskRecord, runErr error, iterateResult *iterate.FixReviewResult, logMsg string) error {
	record.Status = model.TaskStatusFailed
	record.Error = test.SanitizeErrorMessage(runErr.Error())
	if iterateResult != nil {
		record.Result = iterateResult.RawOutput
	}
	completedAt := time.Now()
	record.CompletedAt = &completedAt
	p.logger.WarnContext(ctx, logMsg,
		"task_id", record.ID,
		"error", runErr,
	)

	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()
	if err := p.store.UpdateTask(persistCtx, record); err != nil {
		p.logger.ErrorContext(ctx, "更新任务最终状态失败",
			"task_id", record.ID,
			"status", record.Status,
			"error", err,
		)
	} else {
		p.persistIterationZeroFixResult(ctx, record.Payload, record, record.Error)
		p.sendIterationErrorNotification(ctx, record)
	}
	return fmt.Errorf("%s: %w", logMsg, asynq.SkipRetry)
}

func (p *Processor) markTaskCancelled(ctx context.Context, record *model.TaskRecord, reason string) error {
	record.Status = model.TaskStatusCancelled
	record.Error = reason
	completedAt := time.Now()
	record.CompletedAt = &completedAt
	record.UpdatedAt = completedAt

	p.logger.InfoContext(ctx, reason,
		"task_id", record.ID,
	)

	// 原始 ctx 可能已取消；使用后台 context 落库，确保最终状态尽量持久化。
	bgCtx, bgCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer bgCancel()
	if err := p.store.UpdateTask(bgCtx, record); err != nil {
		p.logger.ErrorContext(ctx, "更新取消任务状态失败",
			"task_id", record.ID, "error", err,
		)
	}

	return fmt.Errorf("%s: %w", reason, asynq.SkipRetry)
}

// findRecord 根据 payload 中的 delivery_id 查找任务记录，
// 当 delivery_id 查找不到时回退到按 task ID 查找（支持 RecoveryLoop 场景）
func (p *Processor) findRecord(ctx context.Context, payload model.TaskPayload) (*model.TaskRecord, error) {
	// 优先通过 delivery_id 查找（适用于 webhook 触发的任务）
	if payload.DeliveryID != "" {
		record, err := p.store.FindByDeliveryID(ctx, payload.DeliveryID, payload.TaskType)
		if err != nil {
			return nil, fmt.Errorf("按 delivery_id 查找任务记录失败: %w", err)
		}
		if record != nil {
			return record, nil
		}
	}

	// Fallback：尝试通过 buildAsynqTaskID 生成的 TaskID 查找。
	// 当 delivery_id 查找无结果时（例如记录的 delivery_id 字段在存储中不匹配，
	// 或者任务通过非 webhook 方式创建），尝试按 TaskID 直接查找。
	taskID := buildAsynqTaskID(payload.DeliveryID, payload.TaskType)
	if taskID != "" {
		record, err := p.store.GetTask(ctx, taskID)
		if err != nil {
			p.logger.WarnContext(ctx, "按 TaskID fallback 查找任务记录失败",
				"task_id", taskID,
				"error", err,
			)
			// fallback 失败不中断，继续返回未找到错误
		} else if record != nil {
			return record, nil
		}
	}

	return nil, fmt.Errorf("找不到任务记录, delivery_id=%s, task_type=%s", payload.DeliveryID, payload.TaskType)
}

type triageDispatchResult struct {
	Requested int
	Enqueued  int
	Failed    int
}

var errTriageDispatchPartialFailure = errors.New("triage_e2e 部分 run_e2e 子任务入队失败")

// handleTriageE2EResult 处理 triage_e2e 成功后的链式入队。
// 遍历 triageResult.Modules，逐模块调用 EnqueueManualE2E 入队 run_e2e。
// 入队前基于仓库实际 e2e/ 模块做白名单校验，避免 LLM 输出直接驱动任意任务。
func (p *Processor) handleTriageE2EResult(ctx context.Context, record *model.TaskRecord, output *e2e.TriageE2EOutput) (triageDispatchResult, error) {
	var dispatch triageDispatchResult
	if len(output.Modules) == 0 {
		p.logger.InfoContext(ctx, "triage_e2e: 无需回归，modules 为空",
			"task_id", record.ID)
		return dispatch, nil
	}
	dispatch.Requested = len(output.Modules)
	if p.enqueueHandler == nil {
		p.logger.ErrorContext(ctx, "triage_e2e: enqueueHandler 未注入，无法链式入队",
			"task_id", record.ID)
		return dispatch, fmt.Errorf("enqueueHandler 未注入，无法链式入队 run_e2e")
	}

	available, err := p.loadAvailableE2EModules(ctx, record)
	if err != nil {
		return dispatch, err
	}
	for _, mod := range output.Modules {
		if _, ok := available[mod.Name]; !ok {
			return dispatch, fmt.Errorf("triage 输出模块 %q 不存在于仓库 e2e/ 可用模块", mod.Name)
		}
	}

	triggeredBy := fmt.Sprintf("triage_e2e:%s", record.ID)
	for _, mod := range output.Modules {
		payload := model.TaskPayload{
			TaskType:       model.TaskTypeRunE2E,
			RepoOwner:      record.Payload.RepoOwner,
			RepoName:       record.Payload.RepoName,
			RepoFullName:   record.Payload.RepoFullName,
			CloneURL:       record.Payload.CloneURL,
			Module:         mod.Name,
			BaseRef:        record.Payload.BaseRef,
			HeadSHA:        record.Payload.HeadSHA,
			MergeCommitSHA: record.Payload.MergeCommitSHA,
			Environment:    record.Payload.Environment,
		}
		tasks, err := p.enqueueHandler.EnqueueManualE2E(ctx, payload, triggeredBy)
		if err != nil {
			dispatch.Failed++
			p.logger.WarnContext(ctx, "triage_e2e: 链式入队失败",
				"task_id", record.ID,
				"module", mod.Name, "error", err)
			continue
		}
		if len(tasks) == 0 {
			dispatch.Failed++
			p.logger.WarnContext(ctx, "triage_e2e: 链式入队未返回任务",
				"task_id", record.ID,
				"module", mod.Name)
			continue
		}
		dispatch.Enqueued += len(tasks)
	}
	if dispatch.Enqueued == 0 {
		return dispatch, fmt.Errorf("所有 run_e2e 子任务入队均失败，modules=%d", dispatch.Requested)
	}
	if dispatch.Failed > 0 {
		p.logger.WarnContext(ctx, "triage_e2e: 部分 run_e2e 子任务入队失败",
			"task_id", record.ID,
			"requested", dispatch.Requested,
			"enqueued", dispatch.Enqueued,
			"failed", dispatch.Failed)
		return dispatch, fmt.Errorf("%w: requested=%d enqueued=%d failed=%d",
			errTriageDispatchPartialFailure, dispatch.Requested, dispatch.Enqueued, dispatch.Failed)
	}
	return dispatch, nil
}

func (p *Processor) loadAvailableE2EModules(ctx context.Context, record *model.TaskRecord) (map[string]struct{}, error) {
	if p.enqueueHandler.e2eModuleScanner == nil {
		return nil, fmt.Errorf("E2E 模块扫描器未注入，无法校验 triage 输出模块")
	}
	payload := record.Payload
	ref := triageModuleScanRef(payload)
	if ref == "" {
		return nil, fmt.Errorf("缺少可用于扫描 E2E 模块的 ref")
	}
	modules, err := e2e.ScanE2EModules(ctx, p.enqueueHandler.e2eModuleScanner,
		payload.RepoOwner, payload.RepoName, ref)
	if err != nil {
		return nil, fmt.Errorf("扫描 E2E 模块失败: %w", err)
	}
	available := make(map[string]struct{}, len(modules))
	for _, mod := range modules {
		available[mod] = struct{}{}
	}
	return available, nil
}

func triageModuleScanRef(payload model.TaskPayload) string {
	switch {
	case payload.MergeCommitSHA != "":
		return payload.MergeCommitSHA
	case payload.HeadSHA != "":
		return payload.HeadSHA
	default:
		return payload.BaseRef
	}
}
