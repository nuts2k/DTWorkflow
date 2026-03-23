package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// PoolRunner 抽象 Pool.Run 接口，便于 mock 测试
type PoolRunner interface {
	Run(ctx context.Context, payload model.TaskPayload) (*worker.ExecutionResult, error)
}

// Processor 处理 asynq 任务，协调 Store 状态更新与 PoolRunner 执行
type Processor struct {
	pool   PoolRunner
	store  store.Store
	logger *slog.Logger
}

// NewProcessor 创建 Processor 实例
func NewProcessor(pool PoolRunner, store store.Store, logger *slog.Logger) *Processor {
	return &Processor{
		pool:   pool,
		store:  store,
		logger: logger,
	}
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

	// 4. 执行任务（通过 PoolRunner）
	result, runErr := p.pool.Run(ctx, payload)

	// 5. 根据执行结果更新状态
	completedAt := time.Now()
	record.CompletedAt = &completedAt
	record.UpdatedAt = completedAt

	if runErr != nil {
		record.Status = model.TaskStatusFailed
		record.Error = runErr.Error()
		p.logger.ErrorContext(ctx, "任务执行失败",
			"task_id", taskID,
			"error", runErr,
		)
	} else if result != nil && result.ExitCode != 0 {
		record.Status = model.TaskStatusFailed
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
		}
		p.logger.InfoContext(ctx, "任务执行成功",
			"task_id", taskID,
			"task_type", payload.TaskType,
		)
	}

	if err := p.store.UpdateTask(ctx, record); err != nil {
		p.logger.ErrorContext(ctx, "更新任务最终状态失败",
			"task_id", taskID,
			"status", record.Status,
			"error", err,
		)
	}

	// TODO: 任务完成后通过 Gitea API 发送评论通知（M1.7 通知框架实现后接入）

	if runErr != nil {
		return fmt.Errorf("任务执行失败: %w", runErr)
	}
	if result != nil && result.ExitCode != 0 {
		// 非零退出码属于确定性失败，跳过重试
		return fmt.Errorf("任务执行失败，退出码 %d: %w", result.ExitCode, asynq.SkipRetry)
	}
	return nil
}

// findRecord 根据 payload 中的 delivery_id 或 task_id 查找任务记录
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
	return nil, fmt.Errorf("找不到任务记录, delivery_id=%s, task_type=%s", payload.DeliveryID, payload.TaskType)
}
