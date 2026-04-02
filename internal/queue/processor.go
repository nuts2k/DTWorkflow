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

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// 编译时检查 *Processor 实现 asynq.Handler 接口
var _ asynq.Handler = (*Processor)(nil)

// PoolRunner 抽象 Pool.Run 接口，便于 mock 测试
type PoolRunner interface {
	Run(ctx context.Context, payload model.TaskPayload) (*worker.ExecutionResult, error)
}

// TaskNotifier 抽象通知发送接口，便于在 Processor 中解耦 Router 实现并进行测试。
type TaskNotifier interface {
	Send(ctx context.Context, msg notify.Message) error
}

// ReviewExecutor 窄接口，解耦 review 包
type ReviewExecutor interface {
	Execute(ctx context.Context, payload model.TaskPayload) (*review.ReviewResult, error)
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
	var result *worker.ExecutionResult
	var runErr error
	switch {
	case payload.TaskType == model.TaskTypeReviewPR && p.reviewService != nil:
		reviewResult, runErr = p.reviewService.Execute(ctx, payload)
		if reviewResult != nil {
			result = adaptReviewResult(reviewResult)
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

		// ErrPRNotOpen 是确定性失败，直接标记 failed 并跳过重试
		if errors.Is(runErr, review.ErrPRNotOpen) {
			record.Status = model.TaskStatusFailed
			record.Error = runErr.Error()
			completedAt := time.Now()
			record.CompletedAt = &completedAt
			p.logger.WarnContext(ctx, "PR 不处于 open 状态，跳过评审",
				"task_id", taskID,
				"error", runErr,
			)
			if err := p.store.UpdateTask(ctx, record); err != nil {
				p.logger.ErrorContext(ctx, "更新任务最终状态失败",
					"task_id", taskID,
					"status", record.Status,
					"error", err,
				)
			} else {
				p.sendCompletionNotification(ctx, record, reviewResult)
			}
			return fmt.Errorf("PR 不处于 open 状态: %w", asynq.SkipRetry)
		}
		// 根据 shouldRetry 判断是否还有剩余重试机会：
		// - 有剩余重试：设为 retrying，asynq 将自动安排下次重试
		// - 无剩余重试或无法获取重试信息：设为 failed
		if shouldRetry(ctx) {
			record.Status = model.TaskStatusRetrying
		} else {
			record.Status = model.TaskStatusFailed
		}
		record.Error = runErr.Error()
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

	// CompletedAt 仅在任务达到最终状态时设置
	if record.Status == model.TaskStatusSucceeded || record.Status == model.TaskStatusFailed {
		completedAt := time.Now()
		record.CompletedAt = &completedAt
	}

	finalStatePersisted := true
	if err := p.store.UpdateTask(ctx, record); err != nil {
		finalStatePersisted = false
		p.logger.ErrorContext(ctx, "更新任务最终状态失败",
			"task_id", taskID,
			"status", record.Status,
			"error", err,
		)
	}

	if finalStatePersisted {
		p.sendCompletionNotification(ctx, record, reviewResult)
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

// buildPRURL 基于 Gitea 配置构造 PR 页面链接
func buildPRURL(giteaBaseURL string, payload model.TaskPayload) string {
	return fmt.Sprintf("%s/%s/%s/pulls/%d",
		strings.TrimRight(giteaBaseURL, "/"),
		payload.RepoOwner,
		payload.RepoName,
		payload.PRNumber,
	)
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

	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		if payload.PRNumber <= 0 {
			return notify.Message{}, false
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata["pr_url"] = buildPRURL(p.giteaBaseURL, payload)
		}
		if payload.PRTitle != "" {
			metadata["pr_title"] = payload.PRTitle
		}
		return notify.Message{
			EventType: notify.EventPRReviewStarted,
			Severity:  notify.SeverityInfo,
			Target: notify.Target{
				Owner:  payload.RepoOwner,
				Repo:   payload.RepoName,
				Number: payload.PRNumber,
				IsPR:   true,
			},
			Title:    "PR 自动评审开始",
			Body:     fmt.Sprintf("正在评审 PR #%d\n\n仓库：%s", payload.PRNumber, payload.RepoFullName),
			Metadata: metadata,
		}, true
	default:
		return notify.Message{}, false
	}
}

// findRecord 根据 payload 中的 delivery_id 查找任务记录，
// 当 delivery_id 查找不到时回退到按 task ID 查找（支持 RecoveryLoop 场景）
func (p *Processor) sendCompletionNotification(ctx context.Context, record *model.TaskRecord, reviewResult *review.ReviewResult) {
	if p.notifier == nil || record == nil {
		return
	}
	msg, ok := p.buildNotificationMessage(record, reviewResult)
	if !ok {
		return
	}
	if err := p.notifier.Send(ctx, msg); err != nil {
		p.logger.ErrorContext(ctx, "发送任务完成通知失败",
			"task_id", record.ID,
			"status", record.Status,
			"error", err,
		)
	}
}

func (p *Processor) buildNotificationMessage(record *model.TaskRecord, reviewResult *review.ReviewResult) (notify.Message, bool) {
	if record == nil {
		return notify.Message{}, false
	}
	if record.Status != model.TaskStatusSucceeded && record.Status != model.TaskStatusFailed {
		return notify.Message{}, false
	}

	payload := record.Payload
	if payload.RepoOwner == "" || payload.RepoName == "" {
		return notify.Message{}, false
	}

	body := fmt.Sprintf("任务 %s 执行完成\n\n仓库：%s\n任务类型：%s\n状态：%s", record.ID, payload.RepoFullName, payload.TaskType, record.Status)
	if record.Error != "" {
		body += fmt.Sprintf("\n错误：%s", record.Error)
	}

	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		if payload.PRNumber <= 0 {
			return notify.Message{}, false
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata["pr_url"] = buildPRURL(p.giteaBaseURL, payload)
		}
		if payload.PRTitle != "" {
			metadata["pr_title"] = payload.PRTitle
		}
		if reviewResult != nil && reviewResult.Review != nil {
			metadata["verdict"] = string(reviewResult.Review.Verdict)
			metadata["issue_summary"] = formatIssueSummary(reviewResult.Review.Issues)
		}
		if record.Status == model.TaskStatusSucceeded {
			return notify.Message{
				EventType: notify.EventPRReviewDone,
				Severity:  notify.SeverityInfo,
				Target: notify.Target{
					Owner:  payload.RepoOwner,
					Repo:   payload.RepoName,
					Number: payload.PRNumber,
					IsPR:   true,
				},
				Title:    "PR 自动评审任务完成",
				Body:     body,
				Metadata: metadata,
			}, true
		}
		return notify.Message{
			EventType: notify.EventSystemError,
			Severity:  notify.SeverityWarning,
			Target: notify.Target{
				Owner:  payload.RepoOwner,
				Repo:   payload.RepoName,
				Number: payload.PRNumber,
				IsPR:   true,
			},
			Title:    "PR 自动评审任务失败",
			Body:     body,
			Metadata: metadata,
		}, true
	case model.TaskTypeFixIssue:
		if payload.IssueNumber <= 0 {
			return notify.Message{}, false
		}
		if record.Status == model.TaskStatusSucceeded {
			return notify.Message{
				EventType: notify.EventFixIssueDone,
				Severity:  notify.SeverityInfo,
				Target: notify.Target{
					Owner:  payload.RepoOwner,
					Repo:   payload.RepoName,
					Number: payload.IssueNumber,
					IsPR:   false,
				},
				Title: "Issue 自动修复任务完成",
				Body:  body,
			}, true
		}
		return notify.Message{
			EventType: notify.EventSystemError,
			Severity:  notify.SeverityWarning,
			Target: notify.Target{
				Owner:  payload.RepoOwner,
				Repo:   payload.RepoName,
				Number: payload.IssueNumber,
				IsPR:   false,
			},
			Title: "Issue 自动修复任务失败",
			Body:  body,
		}, true
	default:
		return notify.Message{}, false
	}
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
