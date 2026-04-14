package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

// RetryTask 复用原任务记录并立即重新入队。
func RetryTask(ctx context.Context, s store.Store, q Enqueuer, id string) (*model.TaskRecord, string, error) {
	if q == nil {
		return nil, "", fmt.Errorf("任务重新入队失败: enqueuer 不能为 nil")
	}

	record, err := s.GetTask(ctx, id)
	if err != nil {
		return nil, "", fmt.Errorf("查询任务失败: %w", err)
	}
	if record == nil {
		return nil, "", fmt.Errorf("任务 %s 不存在", id)
	}
	if record.Status != model.TaskStatusFailed && record.Status != model.TaskStatusCancelled {
		return nil, "", fmt.Errorf("任务 %s 状态为 %s，只有 failed 或 cancelled 状态的任务可以重试", id, record.Status)
	}

	payload := record.Payload
	if !payload.TaskType.IsValid() {
		if !record.TaskType.IsValid() {
			return nil, "", fmt.Errorf("任务 %s 的 TaskType 非法: %s", id, record.TaskType)
		}
		payload.TaskType = record.TaskType
	}

	taskID := buildAsynqTaskID(record.DeliveryID, record.TaskType)
	asynqID, err := q.Enqueue(ctx, payload, EnqueueOptions{Priority: record.Priority, TaskID: taskID})
	message := "任务已重新入队"
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		asynqID = taskID
		message = "任务已在队列中，状态已同步为 queued"
	} else if err != nil {
		return nil, "", fmt.Errorf("任务重新入队失败: %w", err)
	}

	record.Payload = payload
	record.RetryCount = 0
	record.Error = ""
	record.StartedAt = nil
	record.CompletedAt = nil
	record.WorkerID = ""
	record.Status = model.TaskStatusQueued
	record.AsynqID = asynqID
	record.UpdatedAt = time.Now()
	if err := s.UpdateTask(ctx, record); err != nil {
		return nil, "", fmt.Errorf("任务可能已重新入队，但状态同步失败: %w", err)
	}

	return record, message, nil
}
