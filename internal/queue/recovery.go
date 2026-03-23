package queue

import (
	"context"
	"log/slog"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

// RecoveryLoop 定期扫描孤儿任务（pending 超过 maxAge），将其重新入队
type RecoveryLoop struct {
	store    store.Store
	client   *Client
	logger   *slog.Logger
	interval time.Duration // 扫描间隔，默认 60s
	maxAge   time.Duration // 孤儿任务判定阈值，默认 120s
}

// NewRecoveryLoop 创建 RecoveryLoop 实例
// interval 为 0 时使用默认值 60s，maxAge 为 0 时使用默认值 120s
func NewRecoveryLoop(store store.Store, client *Client, logger *slog.Logger, interval, maxAge time.Duration) *RecoveryLoop {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	if maxAge <= 0 {
		maxAge = 120 * time.Second
	}
	return &RecoveryLoop{
		store:    store,
		client:   client,
		logger:   logger,
		interval: interval,
		maxAge:   maxAge,
	}
}

// Run 启动恢复循环，阻塞直到 ctx 被取消
func (r *RecoveryLoop) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	r.logger.InfoContext(ctx, "RecoveryLoop 已启动",
		"interval", r.interval,
		"max_age", r.maxAge,
	)

	for {
		select {
		case <-ctx.Done():
			r.logger.InfoContext(ctx, "RecoveryLoop 已停止")
			return
		case <-ticker.C:
			r.recover(ctx)
		}
	}
}

// recover 执行一次孤儿任务扫描与重新入队
func (r *RecoveryLoop) recover(ctx context.Context) {
	orphans, err := r.store.ListOrphanTasks(ctx, r.maxAge)
	if err != nil {
		r.logger.ErrorContext(ctx, "查询孤儿任务失败", "error", err)
		return
	}

	if len(orphans) == 0 {
		return
	}

	r.logger.InfoContext(ctx, "发现孤儿任务，开始重新入队", "count", len(orphans))

	for _, record := range orphans {
		r.requeue(ctx, record)
	}
}

// requeue 将单个孤儿任务重新入队
func (r *RecoveryLoop) requeue(ctx context.Context, record *model.TaskRecord) {
	asynqID, err := r.client.Enqueue(ctx, record.Payload, EnqueueOptions{
		Priority: record.Priority,
		TaskID:   record.ID,
	})
	if err != nil {
		r.logger.WarnContext(ctx, "孤儿任务重新入队失败",
			"task_id", record.ID,
			"task_type", record.TaskType,
			"error", err,
		)
		return
	}

	// 更新状态为 queued
	record.AsynqID = asynqID
	record.Status = model.TaskStatusQueued
	record.UpdatedAt = time.Now()
	if err := r.store.UpdateTask(ctx, record); err != nil {
		r.logger.WarnContext(ctx, "更新孤儿任务状态失败",
			"task_id", record.ID,
			"error", err,
		)
		return
	}

	r.logger.InfoContext(ctx, "孤儿任务已重新入队",
		"task_id", record.ID,
		"asynq_id", asynqID,
		"task_type", record.TaskType,
	)
}
