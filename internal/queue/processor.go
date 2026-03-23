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

// 编译时检查 *Processor 实现 asynq.Handler 接口
var _ asynq.Handler = (*Processor)(nil)

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
	if pool == nil {
		panic("NewProcessor: pool 不能为 nil")
	}
	if store == nil {
		panic("NewProcessor: store 不能为 nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Processor{
		pool:   pool,
		store:  store,
		logger: logger,
	}
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
	record.UpdatedAt = time.Now()

	if runErr != nil {
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

	if err := p.store.UpdateTask(ctx, record); err != nil {
		p.logger.ErrorContext(ctx, "更新任务最终状态失败",
			"task_id", taskID,
			"status", record.Status,
			"error", err,
		)
	}

	// TODO: 接入 notify.Router 发送任务完成通知（M1.8 配置管理完成后集成）

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

// findRecord 根据 payload 中的 delivery_id 查找任务记录
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
