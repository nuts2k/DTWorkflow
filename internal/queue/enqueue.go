package queue

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/webhook"
)

// 编译时检查 *EnqueueHandler 实现 webhook.Handler 接口
var _ webhook.Handler = (*EnqueueHandler)(nil)

// EnqueueHandler 实现 webhook.Handler 接口，将 webhook 事件转换为任务并入队
type EnqueueHandler struct {
	client Enqueuer
	store  store.Store
	logger *slog.Logger
}

// NewEnqueueHandler 创建 EnqueueHandler 实例。
// 参数 client 和 store 为必要依赖，传入 nil 属于编程错误（programming error），
// 因此使用 panic 而非返回 error，与 Go 标准库（如 http.NewServeMux）的惯例一致。
func NewEnqueueHandler(client Enqueuer, store store.Store, logger *slog.Logger) *EnqueueHandler {
	if client == nil {
		panic("NewEnqueueHandler: client 不能为 nil")
	}
	if store == nil {
		panic("NewEnqueueHandler: store 不能为 nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &EnqueueHandler{
		client: client,
		store:  store,
		logger: logger,
	}
}

// HandlePullRequest 处理 PR 事件，执行幂等检查后创建任务并入队。
// 注意：HandlePullRequest 与 HandleIssueLabel 有相似的流程结构（幂等检查 -> 构建 payload ->
// 构建 record -> enqueueTask -> 日志），但因各事件的 payload 字段差异较大且日志消息不同，
// 提取通用模板方法反而会引入复杂的泛型或 interface 抽象，得不偿失。
// 核心逻辑已下沉到 enqueueTask，当前的重复仅在 payload/record 构建和日志层面。
func (h *EnqueueHandler) HandlePullRequest(ctx context.Context, event webhook.PullRequestEvent) error {
	// 幂等检查：相同 delivery_id + task_type 不重复创建
	existing, err := h.store.FindByDeliveryID(ctx, event.DeliveryID, model.TaskTypeReviewPR)
	if err != nil {
		return fmt.Errorf("幂等检查失败: %w", err)
	}
	if existing != nil {
		h.logger.InfoContext(ctx, "PR 评审任务已存在，跳过",
			"delivery_id", event.DeliveryID,
			"task_id", existing.ID,
			"status", existing.Status,
		)
		return nil
	}

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   event.DeliveryID,
		RepoOwner:    event.Repository.Owner,
		RepoName:     event.Repository.Name,
		RepoFullName: event.Repository.FullName,
		CloneURL:     event.Repository.CloneURL,
		PRNumber:     event.PullRequest.Number,
		BaseRef:      event.PullRequest.BaseRef,
		HeadRef:      event.PullRequest.HeadRef,
		HeadSHA:      event.PullRequest.HeadSHA,
	}

	if payload.RepoFullName == "" || payload.CloneURL == "" {
		return fmt.Errorf("webhook 数据不完整: RepoFullName 或 CloneURL 为空")
	}

	record := &model.TaskRecord{
		TaskType:     model.TaskTypeReviewPR,
		Priority:     model.PriorityHigh,
		RepoFullName: event.Repository.FullName,
		DeliveryID:   event.DeliveryID,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return err
	}

	if record.Status == model.TaskStatusQueued {
		h.logger.InfoContext(ctx, "PR 评审任务已入队",
			"task_id", record.ID,
			"asynq_id", record.AsynqID,
			"repo", event.Repository.FullName,
			"pr", event.PullRequest.Number,
		)
	} else {
		h.logger.InfoContext(ctx, "PR 评审任务已创建（pending），等待 RecoveryLoop 入队",
			"task_id", record.ID,
			"repo", event.Repository.FullName,
			"pr", event.PullRequest.Number,
		)
	}
	return nil
}

// HandleIssueLabel 处理 Issue 标签事件，仅在 AutoFixAdded 时创建修复任务。
// 流程结构与 HandlePullRequest 类似，参见其注释了解未提取模板方法的原因。
func (h *EnqueueHandler) HandleIssueLabel(ctx context.Context, event webhook.IssueLabelEvent) error {
	// 仅处理添加了 auto-fix 标签的事件
	if !event.AutoFixAdded {
		return nil
	}

	// 幂等检查
	existing, err := h.store.FindByDeliveryID(ctx, event.DeliveryID, model.TaskTypeFixIssue)
	if err != nil {
		return fmt.Errorf("幂等检查失败: %w", err)
	}
	if existing != nil {
		h.logger.InfoContext(ctx, "Issue 修复任务已存在，跳过",
			"delivery_id", event.DeliveryID,
			"task_id", existing.ID,
			"status", existing.Status,
		)
		return nil
	}

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   event.DeliveryID,
		RepoOwner:    event.Repository.Owner,
		RepoName:     event.Repository.Name,
		RepoFullName: event.Repository.FullName,
		CloneURL:     event.Repository.CloneURL,
		IssueNumber:  event.Issue.Number,
		IssueTitle:   event.Issue.Title,
	}

	if payload.RepoFullName == "" || payload.CloneURL == "" {
		return fmt.Errorf("webhook 数据不完整: RepoFullName 或 CloneURL 为空")
	}

	record := &model.TaskRecord{
		TaskType:     model.TaskTypeFixIssue,
		Priority:     model.PriorityNormal,
		RepoFullName: event.Repository.FullName,
		DeliveryID:   event.DeliveryID,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return err
	}

	if record.Status == model.TaskStatusQueued {
		h.logger.InfoContext(ctx, "Issue 修复任务已入队",
			"task_id", record.ID,
			"asynq_id", record.AsynqID,
			"repo", event.Repository.FullName,
			"issue", event.Issue.Number,
		)
	} else {
		h.logger.InfoContext(ctx, "Issue 修复任务已创建（pending），等待 RecoveryLoop 入队",
			"task_id", record.ID,
			"repo", event.Repository.FullName,
			"issue", event.Issue.Number,
		)
	}
	return nil
}

// buildAsynqTaskID 根据 deliveryID 和 taskType 构建确定性的 asynq TaskID。
// 当 deliveryID 非空时，使用 "deliveryID:taskType" 格式，保证 asynq 层面的幂等去重；
// 当 deliveryID 为空时返回空字符串，让 asynq 自动生成 TaskID。
//
// 此函数被 enqueueTask 和 RecoveryLoop.requeue 共享，确保同一任务无论首次入队
// 还是恢复重入队都使用相同的 TaskID，避免因 TaskID 不一致导致任务重复执行。
func buildAsynqTaskID(deliveryID string, taskType model.TaskType) string {
	if deliveryID != "" {
		return fmt.Sprintf("%s:%s", deliveryID, taskType)
	}
	return "" // 让 asynq 自动生成
}

// enqueueTask 持久化任务记录并将其入队，record 字段 TaskType/Priority/RepoFullName/DeliveryID 需预先填充
func (h *EnqueueHandler) enqueueTask(ctx context.Context, payload model.TaskPayload, record *model.TaskRecord) error {
	now := time.Now()
	record.ID = uuid.New().String()
	record.Status = model.TaskStatusPending
	record.Payload = payload
	record.MaxRetry = TaskMaxRetry()
	record.CreatedAt = now
	record.UpdatedAt = now

	// 1. 先持久化到 SQLite（status=pending）
	if err := h.store.CreateTask(ctx, record); err != nil {
		return fmt.Errorf("创建任务记录失败: %w", err)
	}

	// 2. 入队到 Redis
	// 设计决策："先持久化再入队" 的 eventually consistent 模式。
	// Step 1 成功但 Step 2 失败时，任务保持 pending 状态，不向调用方返回错误。
	// RecoveryLoop 会定期扫描长时间处于 pending 的孤儿任务并重新入队，
	// 从而保证最终一致性。这避免了分布式事务的复杂性，代价是入队可能有延迟。
	// 使用共享的 buildAsynqTaskID 生成确定性 TaskID，确保与 RecoveryLoop 一致
	taskID := buildAsynqTaskID(record.DeliveryID, record.TaskType)
	asynqID, err := h.client.Enqueue(ctx, payload, EnqueueOptions{
		Priority: record.Priority,
		TaskID:   taskID,
	})
	if err != nil {
		// 设计决策：入队失败不返回错误给调用方。
		// 设计文档（docs/M1.5-task-queue-design.md）中描述入队失败应返回 error，
		// 但实际实现采用了 "先持久化再入队" 的 eventually consistent 模式：
		// 任务已成功持久化到 SQLite（status=pending），RecoveryLoop 会定期扫描
		// 长时间处于 pending 的孤儿任务并重新入队，最终保证一致性。
		// 若此处返回 error，webhook handler 会向 Gitea 返回 500，触发 Gitea 重发
		// 同一 webhook，而任务实际已被持久化，这会造成不必要的重试噪音。
		// 因此选择静默降级：记录警告日志，依赖 RecoveryLoop 补偿入队，
		// 代价是入队可能有最多 interval（默认 60s）的延迟。
		h.logger.WarnContext(ctx, "任务入队失败，将由 RecoveryLoop 重试",
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
		// 不返回错误，任务已成功入队
	}

	return nil
}
