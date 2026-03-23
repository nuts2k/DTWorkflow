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

// EnqueueHandler 实现 webhook.Handler 接口，将 webhook 事件转换为任务并入队
type EnqueueHandler struct {
	client *Client
	store  store.Store
	logger *slog.Logger
}

// NewEnqueueHandler 创建 EnqueueHandler 实例
func NewEnqueueHandler(client *Client, store store.Store, logger *slog.Logger) *EnqueueHandler {
	return &EnqueueHandler{
		client: client,
		store:  store,
		logger: logger,
	}
}

// HandlePullRequest 处理 PR 事件，执行幂等检查后创建任务并入队
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

	now := time.Now()
	record := &model.TaskRecord{
		ID:           uuid.New().String(),
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusPending,
		Priority:     model.PriorityHigh,
		Payload:      payload,
		RepoFullName: event.Repository.FullName,
		DeliveryID:   event.DeliveryID,
		MaxRetry:     TaskMaxRetry(),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// 1. 先持久化到 SQLite（status=pending）
	if err := h.store.CreateTask(ctx, record); err != nil {
		return fmt.Errorf("创建任务记录失败: %w", err)
	}

	// 2. 入队到 Redis
	asynqID, err := h.client.Enqueue(ctx, payload, EnqueueOptions{
		Priority: model.PriorityHigh,
		TaskID:   record.ID,
	})
	if err != nil {
		// 入队失败，任务保持 pending 状态，由 RecoveryLoop 处理
		h.logger.WarnContext(ctx, "PR 评审任务入队失败，将由 RecoveryLoop 重试",
			"task_id", record.ID,
			"error", err,
		)
		return nil
	}

	// 3. 更新状态为 queued
	record.AsynqID = asynqID
	record.Status = model.TaskStatusQueued
	record.UpdatedAt = time.Now()
	if err := h.store.UpdateTask(ctx, record); err != nil {
		h.logger.WarnContext(ctx, "更新任务状态为 queued 失败",
			"task_id", record.ID,
			"error", err,
		)
		// 不返回错误，任务已成功入队
	}

	h.logger.InfoContext(ctx, "PR 评审任务已入队",
		"task_id", record.ID,
		"asynq_id", asynqID,
		"repo", event.Repository.FullName,
		"pr", event.PullRequest.Number,
	)
	return nil
}

// HandleIssueLabel 处理 Issue 标签事件，仅在 AutoFixAdded 时创建修复任务
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

	now := time.Now()
	record := &model.TaskRecord{
		ID:           uuid.New().String(),
		TaskType:     model.TaskTypeFixIssue,
		Status:       model.TaskStatusPending,
		Priority:     model.PriorityNormal,
		Payload:      payload,
		RepoFullName: event.Repository.FullName,
		DeliveryID:   event.DeliveryID,
		MaxRetry:     TaskMaxRetry(),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// 1. 先持久化到 SQLite（status=pending）
	if err := h.store.CreateTask(ctx, record); err != nil {
		return fmt.Errorf("创建任务记录失败: %w", err)
	}

	// 2. 入队到 Redis
	asynqID, err := h.client.Enqueue(ctx, payload, EnqueueOptions{
		Priority: model.PriorityNormal,
		TaskID:   record.ID,
	})
	if err != nil {
		// 入队失败，任务保持 pending 状态，由 RecoveryLoop 处理
		h.logger.WarnContext(ctx, "Issue 修复任务入队失败，将由 RecoveryLoop 重试",
			"task_id", record.ID,
			"error", err,
		)
		return nil
	}

	// 3. 更新状态为 queued
	record.AsynqID = asynqID
	record.Status = model.TaskStatusQueued
	record.UpdatedAt = time.Now()
	if err := h.store.UpdateTask(ctx, record); err != nil {
		h.logger.WarnContext(ctx, "更新任务状态为 queued 失败",
			"task_id", record.ID,
			"error", err,
		)
	}

	h.logger.InfoContext(ctx, "Issue 修复任务已入队",
		"task_id", record.ID,
		"asynq_id", asynqID,
		"repo", event.Repository.FullName,
		"issue", event.Issue.Number,
	)
	return nil
}
