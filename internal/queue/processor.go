package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/code"
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

// CodeExecutor 窄接口，解耦 code 包。
type CodeExecutor interface {
	Execute(ctx context.Context, payload model.TaskPayload) (*code.CodeFromDocResult, error)
	WriteDegraded(ctx context.Context, payload model.TaskPayload, result *code.CodeFromDocResult) error
}

// WithCodeService 注入文档驱动编码服务。
func WithCodeService(svc CodeExecutor) ProcessorOption {
	return func(p *Processor) {
		p.codeService = svc
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
	codeService          CodeExecutor         // 可选；注入后 code_from_doc 走 code.Service
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
	if record.Status == model.TaskStatusSucceeded && payload.TaskType == model.TaskTypeFixReview {
		return p.repairSucceededFixReviewIterationState(ctx, record, payload)
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
	var codeResult *code.CodeFromDocResult
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
				Output:   iterateResult.RawOutput,
				ExitCode: iterateResult.ExitCode,
			}
		}
	case payload.TaskType == model.TaskTypeCodeFromDoc && p.codeService != nil:
		codeResult, runErr = p.codeService.Execute(ctx, payload)
		if codeResult != nil {
			result = &worker.ExecutionResult{
				Output:   codeResult.RawOutput,
				ExitCode: codeResult.ExitCode,
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
			return p.handleSkipRetryFailure(ctx, record, runErr, reviewResult, nil, nil, nil, "PR 不处于 open 状态，跳过评审")
		}
		if errors.Is(runErr, fix.ErrIssueNotOpen) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, fixResult, nil, nil, "Issue 不处于 open 状态，跳过分析")
		}
		if errors.Is(runErr, fix.ErrMissingIssueRef) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, fixResult, nil, nil, "Issue 未设置关联分支，跳过分析")
		}
		if errors.Is(runErr, fix.ErrInvalidIssueRef) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, fixResult, nil, nil, "Issue 关联的 ref 不存在，跳过分析")
		}
		if errors.Is(runErr, fix.ErrInfoInsufficient) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, fixResult, nil, nil, "前序分析信息不足，跳过修复")
		}
		if errors.Is(runErr, fix.ErrFixFailed) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, fixResult, nil, nil, "Claude 返回 success=false，跳过重试")
		}
		if errors.Is(runErr, test.ErrTestGenDisabled) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, nil, "仓库已禁用 gen_tests，跳过重试")
		}
		if errors.Is(runErr, test.ErrInvalidModule) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, nil, "module 路径非法，跳过测试生成")
		}
		if errors.Is(runErr, test.ErrModuleOutOfScope) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, nil, "module 超出允许范围，跳过测试生成")
		}
		if errors.Is(runErr, test.ErrModuleNotFound) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, nil, "module 子路径不存在于仓库，跳过测试生成")
		}
		if errors.Is(runErr, test.ErrInvalidRef) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, nil, "gen_tests 指定 ref 不存在，跳过重试")
		}
		if errors.Is(runErr, test.ErrAmbiguousFramework) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, nil, "无法确定测试框架，跳过重试")
		}
		if errors.Is(runErr, test.ErrNoFrameworkDetected) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, nil, "未检测到测试框架，跳过重试")
		}
		if errors.Is(runErr, test.ErrInfoInsufficient) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, nil, "测试生成信息不足，跳过重试")
		}
		if errors.Is(runErr, test.ErrTestGenFailed) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, testResult, nil, "Claude 返回 success=false，跳过重试")
		}
		if errors.Is(runErr, e2e.ErrE2EDisabled) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, nil, nil, "E2E 已禁用，跳过任务")
		}
		if errors.Is(runErr, e2e.ErrEnvironmentNotFound) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, nil, nil, "E2E 环境未找到，跳过任务")
		}
		if errors.Is(runErr, e2e.ErrNoCasesFound) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, nil, nil, "未发现 E2E 用例，跳过任务")
		}
		if errors.Is(runErr, iterate.ErrFixReviewDeterministicFailure) {
			if iterateResult != nil {
				record.Result = iterateResult.RawOutput
			}
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, nil, nil, "fix_review 确定性失败，跳过重试")
		}
		if errors.Is(runErr, iterate.ErrNoNewCommits) {
			if iterateResult != nil {
				record.Result = iterateResult.RawOutput
			}
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, nil, nil, "fix_review 未产生新提交，跳过重试")
		}
		if errors.Is(runErr, iterate.ErrFixReviewParseFailure) {
			return p.handleIterationQualityFailure(ctx, record, runErr, iterateResult, "fix_review 修复结果解析失败，跳过重试")
		}
		if errors.Is(runErr, iterate.ErrNoChanges) {
			return p.handleIterationQualityFailure(ctx, record, runErr, iterateResult, "fix_review 未产生实际变更，跳过重试")
		}
		if errors.Is(runErr, code.ErrInfoInsufficient) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, nil, codeResult, "设计文档信息不足，跳过重试")
		}
		if errors.Is(runErr, code.ErrCodeFromDocDisabled) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, nil, nil, codeResult, "仓库已禁用 code_from_doc，跳过重试")
		}
		if errors.Is(runErr, code.ErrTestFailure) {
			// test_failure：仍标记 succeeded（有 PR 成果），不走 SkipRetry
			record.Status = model.TaskStatusSucceeded
			if codeResult != nil {
				record.Result = codeResult.RawOutput
			}
			completedAt := time.Now()
			record.CompletedAt = &completedAt
			persistCtx2, persistCancel2 := context.WithTimeout(context.Background(), 5*time.Second)
			defer persistCancel2()
			if err := p.store.UpdateTask(persistCtx2, record); err != nil {
				p.logger.ErrorContext(ctx, "更新 code_from_doc test_failure 状态失败",
					"task_id", record.ID, "error", err)
			}
			p.sendCompletionNotification(ctx, record, nil, nil, nil, nil, nil, codeResult)
			return nil
		}
		if errors.Is(runErr, code.ErrCodeFromDocParseFailure) && !shouldRetry(ctx) && codeResult != nil {
			if wbErr := p.codeService.WriteDegraded(ctx, payload, codeResult); wbErr != nil {
				p.logger.ErrorContext(ctx, "code_from_doc 解析失败降级回写失败",
					"task_id", record.ID, "error", wbErr)
			}
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
		if payload.TaskType == model.TaskTypeGenTests || payload.TaskType == model.TaskTypeRunE2E || payload.TaskType == model.TaskTypeTriageE2E || payload.TaskType == model.TaskTypeFixReview || payload.TaskType == model.TaskTypeCodeFromDoc {
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
			if err := p.persistIterationFixResult(ctx, payload, record, iterateResult); err != nil {
				p.logger.ErrorContext(ctx, "fix_review 任务成功但迭代状态落库失败，触发重试",
					"task_id", record.ID,
					"session_id", payload.SessionID,
					"round", payload.RoundNumber,
					"error", err)
				return fmt.Errorf("fix_review 迭代状态落库失败: %w", err)
			}
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
		p.sendCompletionNotification(ctx, record, reviewResult, fixResult, testResult, e2eResult, triageResult, codeResult)
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
