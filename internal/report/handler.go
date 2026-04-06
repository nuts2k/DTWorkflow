package report

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// 编译时检查 *DailyReportHandler 实现 asynq.Handler 接口
var _ asynq.Handler = (*DailyReportHandler)(nil)

// TaskStore 是 DailyReportHandler 对 Store 的最小依赖接口
type TaskStore interface {
	CreateTask(ctx context.Context, record *model.TaskRecord) error
	UpdateTask(ctx context.Context, record *model.TaskRecord) error
}

// Generator 报告生成接口（便于测试 mock）
type Generator interface {
	Generate(ctx context.Context) error
}

// DailyReportHandler 每日报告任务的 asynq Handler，独立于 Processor。
// 日报任务无 payload、无 delivery_id，与现有 Processor 的 TaskPayload 生命周期不兼容。
type DailyReportHandler struct {
	store     TaskStore
	generator Generator
	logger    *slog.Logger
}

// NewDailyReportHandler 创建日报 Handler
func NewDailyReportHandler(store TaskStore, generator Generator) *DailyReportHandler {
	return &DailyReportHandler{
		store:     store,
		generator: generator,
		logger:    slog.Default(),
	}
}

// ProcessTask 实现 asynq.Handler 接口
func (h *DailyReportHandler) ProcessTask(ctx context.Context, _ *asynq.Task) error {
	taskID := fmt.Sprintf("daily-report-%s", uuid.NewString())
	h.logger.InfoContext(ctx, "开始生成每日报告", "task_id", taskID)

	// 1. 创建 TaskRecord
	now := time.Now()
	record := &model.TaskRecord{
		ID:        taskID,
		TaskType:  model.TaskTypeGenDailyReport,
		Status:    model.TaskStatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		StartedAt: &now,
	}
	created := true
	if err := h.store.CreateTask(ctx, record); err != nil {
		created = false
		h.logger.WarnContext(ctx, "创建每日报告任务记录失败", "error", err)
		// 不阻塞执行
	}

	// 2. 执行报告生成
	err := h.generator.Generate(ctx)

	// 3. 更新 TaskRecord
	completedAt := time.Now()
	record.CompletedAt = &completedAt
	record.UpdatedAt = completedAt
	if err != nil {
		record.Status = model.TaskStatusFailed
		record.Error = err.Error()
	} else {
		record.Status = model.TaskStatusSucceeded
	}
	if created {
		if updateErr := h.store.UpdateTask(ctx, record); updateErr != nil {
			h.logger.WarnContext(ctx, "更新每日报告任务状态失败", "error", updateErr)
		}
	}

	if err != nil {
		h.logger.ErrorContext(ctx, "每日报告生成失败", "error", err)
		return fmt.Errorf("每日报告生成失败: %w", err)
	}
	h.logger.InfoContext(ctx, "每日报告生成成功", "task_id", taskID)
	return nil
}
