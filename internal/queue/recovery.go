package queue

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

// RecoveryLoop 定期扫描孤儿任务（pending 超过 maxAge），将其重新入队
type RecoveryLoop struct {
	store    store.Store
	client   Enqueuer
	logger   *slog.Logger
	interval time.Duration // 扫描间隔，默认 60s
	maxAge   time.Duration // 孤儿任务判定阈值，默认 120s

	// recoveryAttempts 跟踪每个任务的恢复重入队尝试次数（内存级别）。
	// 与 record.RetryCount 分离：record.RetryCount 在 Processor 中会被 asynq 的
	// GetRetryCount(ctx) 覆盖为 "asynq 执行重试次数"，语义不同。
	// 此 map 仅在 RecoveryLoop 生命周期内有效，进程重启后重置为 0，
	// 但这是可接受的，因为重启后孤儿任务的恢复尝试理应重新开始计数。
	recoveryAttempts map[string]int
}

// NewRecoveryLoop 创建 RecoveryLoop 实例
// interval 为 0 时使用默认值 60s，maxAge 为 0 时使用默认值 120s
func NewRecoveryLoop(store store.Store, client Enqueuer, logger *slog.Logger, interval, maxAge time.Duration) *RecoveryLoop {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	if maxAge <= 0 {
		maxAge = 120 * time.Second
	}
	return &RecoveryLoop{
		store:            store,
		client:           client,
		logger:           logger,
		interval:         interval,
		maxAge:           maxAge,
		recoveryAttempts: make(map[string]int),
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
	// ctx 取消和 ticker.C 同时就绪时 select 可能随机选择 ticker 分支，
	// 此处提前检查避免用已取消的 ctx 执行无意义的恢复操作
	if ctx.Err() != nil {
		return
	}

	orphans, err := r.store.ListOrphanTasks(ctx, r.maxAge)
	if err != nil {
		r.logger.ErrorContext(ctx, "查询孤儿任务失败", "error", err)
		return
	}

	if len(orphans) == 0 {
		return
	}

	r.logger.InfoContext(ctx, "发现孤儿任务，开始重新入队", "count", len(orphans))

	// 每次恢复最多处理 100 条，避免大量孤儿任务阻塞整个恢复周期
	maxBatch := 100
	for i, record := range orphans {
		if i >= maxBatch {
			r.logger.WarnContext(ctx, "本轮恢复已达批次上限，剩余将在下次扫描处理",
				"processed", maxBatch, "remaining", len(orphans)-maxBatch)
			break
		}
		if ctx.Err() != nil {
			return // 上下文已取消，停止恢复
		}
		r.requeue(ctx, record)
	}
}

// requeue 将单个孤儿任务重新入队；若恢复尝试次数已达上限则标记为 failed
func (r *RecoveryLoop) requeue(ctx context.Context, record *model.TaskRecord) {
	// 使用内部 recoveryAttempts 计数而非 record.RetryCount 判断是否放弃。
	// record.RetryCount 在 Processor 中会被 asynq 执行重试次数覆盖，语义不同。
	attempts := r.recoveryAttempts[record.ID]
	if attempts >= record.MaxRetry {
		record.Status = model.TaskStatusFailed
		record.Error = "RecoveryLoop 恢复重入队尝试次数已达上限，放弃"
		record.UpdatedAt = time.Now()
		if err := r.store.UpdateTask(ctx, record); err != nil {
			r.logger.WarnContext(ctx, "标记孤儿任务为 failed 失败",
				"task_id", record.ID,
				"error", err,
			)
			return
		}
		// 任务已标记为 failed，从 recoveryAttempts 中清除
		delete(r.recoveryAttempts, record.ID)
		r.logger.WarnContext(ctx, "孤儿任务恢复尝试次数已达上限，标记为 failed",
			"task_id", record.ID,
			"task_type", record.TaskType,
			"recovery_attempts", attempts,
			"max_retry", record.MaxRetry,
		)
		return
	}

	// 使用共享的 buildAsynqTaskID 生成确定性 TaskID，
	// 确保与 enqueueTask 首次入队时使用相同的 TaskID，利用 asynq 幂等去重
	taskID := buildAsynqTaskID(record.DeliveryID, record.TaskType)
	asynqID, err := r.client.Enqueue(ctx, record.Payload, EnqueueOptions{
		Priority: record.Priority,
		TaskID:   taskID,
	})
	if err != nil {
		// TaskID 冲突说明任务已在 asynq 队列中，视为已入队
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			r.logger.InfoContext(ctx, "孤儿任务已在 asynq 队列中（TaskID 冲突），更新状态为 queued",
				"task_id", record.ID,
				"task_type", record.TaskType,
			)
			asynqID = taskID
		} else {
			r.logger.WarnContext(ctx, "孤儿任务重新入队失败",
				"task_id", record.ID,
				"task_type", record.TaskType,
				"error", err,
			)
			return
		}
	}

	// 更新状态为 queued，递增恢复尝试计数
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

	// 入队成功后递增恢复计数；任务成功入队后 Processor 处理完会将状态变为
	// succeeded/failed，下次扫描不会再命中此任务，计数不会误累积
	r.recoveryAttempts[record.ID]++

	r.logger.InfoContext(ctx, "孤儿任务已重新入队",
		"task_id", record.ID,
		"asynq_id", asynqID,
		"task_type", record.TaskType,
	)
}
