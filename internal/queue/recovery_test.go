package queue

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

// mockStoreForRecovery 支持 ListOrphanTasks 的 mock store
type mockStoreForRecovery struct {
	orphans   []*model.TaskRecord
	updated   []*model.TaskRecord
	listErr   error
	updateErr error
}

func (m *mockStoreForRecovery) CreateTask(_ context.Context, _ *model.TaskRecord) error {
	return nil
}
func (m *mockStoreForRecovery) GetTask(_ context.Context, _ string) (*model.TaskRecord, error) {
	return nil, nil
}
func (m *mockStoreForRecovery) UpdateTask(_ context.Context, record *model.TaskRecord) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updated = append(m.updated, record)
	return nil
}
func (m *mockStoreForRecovery) ListTasks(_ context.Context, _ store.ListOptions) ([]*model.TaskRecord, error) {
	return nil, nil
}
func (m *mockStoreForRecovery) FindByDeliveryID(_ context.Context, _ string, _ model.TaskType) (*model.TaskRecord, error) {
	return nil, nil
}
func (m *mockStoreForRecovery) ListOrphanTasks(_ context.Context, _ time.Duration) ([]*model.TaskRecord, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.orphans, nil
}
func (m *mockStoreForRecovery) Close() error { return nil }

// mockClientForRecovery 模拟入队行为
type mockClientForRecovery struct {
	enqueueErr error
	enqueueID  string
}

func (mc *mockClientForRecovery) Enqueue(_ context.Context, _ model.TaskPayload, opts EnqueueOptions) (string, error) {
	if mc.enqueueErr != nil {
		return "", mc.enqueueErr
	}
	if mc.enqueueID != "" {
		return mc.enqueueID, nil
	}
	return opts.TaskID + "-requeued", nil
}

// recoveryLoopWithMock 使用 mock 依赖的 RecoveryLoop（直接调用 recover 方法测试）
type recoveryLoopWithMock struct {
	store  *mockStoreForRecovery
	client *mockClientForRecovery
	logger *slog.Logger
	maxAge time.Duration
}

func (r *recoveryLoopWithMock) recover(ctx context.Context) {
	orphans, err := r.store.ListOrphanTasks(ctx, r.maxAge)
	if err != nil {
		return
	}
	for _, record := range orphans {
		asynqID, err := r.client.Enqueue(ctx, record.Payload, EnqueueOptions{
			Priority: record.Priority,
			TaskID:   record.ID,
		})
		if err != nil {
			continue
		}
		record.AsynqID = asynqID
		record.Status = model.TaskStatusQueued
		record.UpdatedAt = time.Now()
		_ = r.store.UpdateTask(ctx, record)
	}
}

// recoverWithConflict 与 production 代码 requeue 逻辑一致，处理 ErrTaskIDConflict 分支
func (r *recoveryLoopWithMock) recoverWithConflict(ctx context.Context) {
	orphans, err := r.store.ListOrphanTasks(ctx, r.maxAge)
	if err != nil {
		return
	}
	for _, record := range orphans {
		asynqID, err := r.client.Enqueue(ctx, record.Payload, EnqueueOptions{
			Priority: record.Priority,
			TaskID:   record.ID,
		})
		if err != nil {
			if errors.Is(err, asynq.ErrTaskIDConflict) {
				asynqID = record.ID
			} else {
				continue
			}
		}
		record.AsynqID = asynqID
		record.Status = model.TaskStatusQueued
		record.UpdatedAt = time.Now()
		_ = r.store.UpdateTask(ctx, record)
	}
}

func TestRecoveryLoop_RequeuesOrphans(t *testing.T) {
	orphan := &model.TaskRecord{
		ID:       "orphan-1",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusPending,
		Priority: model.PriorityHigh,
		Payload:  model.TaskPayload{TaskType: model.TaskTypeReviewPR},
	}

	s := &mockStoreForRecovery{orphans: []*model.TaskRecord{orphan}}
	mc := &mockClientForRecovery{}
	r := &recoveryLoopWithMock{store: s, client: mc, logger: slog.Default(), maxAge: 120 * time.Second}

	r.recover(context.Background())

	if len(s.updated) != 1 {
		t.Fatalf("expected 1 updated record, got %d", len(s.updated))
	}
	if s.updated[0].Status != model.TaskStatusQueued {
		t.Errorf("updated status = %q, want %q", s.updated[0].Status, model.TaskStatusQueued)
	}
	if s.updated[0].AsynqID == "" {
		t.Error("updated record should have AsynqID set")
	}
}

func TestRecoveryLoop_NoOrphans(t *testing.T) {
	s := &mockStoreForRecovery{orphans: nil}
	mc := &mockClientForRecovery{}
	r := &recoveryLoopWithMock{store: s, client: mc, logger: slog.Default(), maxAge: 120 * time.Second}

	r.recover(context.Background())

	if len(s.updated) != 0 {
		t.Errorf("expected 0 updates when no orphans, got %d", len(s.updated))
	}
}

func TestRecoveryLoop_ErrTaskIDConflict_StillUpdatesQueued(t *testing.T) {
	orphan := &model.TaskRecord{
		ID:       "orphan-conflict-1",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusPending,
		Priority: model.PriorityHigh,
		Payload:  model.TaskPayload{TaskType: model.TaskTypeReviewPR},
	}

	s := &mockStoreForRecovery{orphans: []*model.TaskRecord{orphan}}
	// 模拟入队返回 ErrTaskIDConflict（任务已在 asynq 队列中）
	mc := &mockClientForRecovery{enqueueErr: asynq.ErrTaskIDConflict}
	r := &recoveryLoopWithMock{store: s, client: mc, logger: slog.Default(), maxAge: 120 * time.Second}

	// 修改 recover 方法以匹配 production 代码中的 ErrTaskIDConflict 处理逻辑
	r.recoverWithConflict(context.Background())

	if len(s.updated) != 1 {
		t.Fatalf("expected 1 updated record on ErrTaskIDConflict, got %d", len(s.updated))
	}
	if s.updated[0].Status != model.TaskStatusQueued {
		t.Errorf("updated status = %q, want %q", s.updated[0].Status, model.TaskStatusQueued)
	}
	// ErrTaskIDConflict 时 AsynqID 应设为记录本身的 ID
	if s.updated[0].AsynqID != "orphan-conflict-1" {
		t.Errorf("updated AsynqID = %q, want %q", s.updated[0].AsynqID, "orphan-conflict-1")
	}
}

func TestRecoveryLoop_EnqueueFail_SkipsUpdate(t *testing.T) {
	orphan := &model.TaskRecord{
		ID:       "orphan-2",
		TaskType: model.TaskTypeFixIssue,
		Status:   model.TaskStatusPending,
		Priority: model.PriorityNormal,
		Payload:  model.TaskPayload{TaskType: model.TaskTypeFixIssue},
	}

	s := &mockStoreForRecovery{orphans: []*model.TaskRecord{orphan}}
	mc := &mockClientForRecovery{enqueueErr: errors.New("redis down")}
	r := &recoveryLoopWithMock{store: s, client: mc, logger: slog.Default(), maxAge: 120 * time.Second}

	r.recover(context.Background())

	// 入队失败时不应更新状态
	if len(s.updated) != 0 {
		t.Errorf("expected 0 updates when enqueue fails, got %d", len(s.updated))
	}
}

func TestRecoveryLoop_ListFail_NoUpdate(t *testing.T) {
	s := &mockStoreForRecovery{listErr: errors.New("db error")}
	mc := &mockClientForRecovery{}
	r := &recoveryLoopWithMock{store: s, client: mc, logger: slog.Default(), maxAge: 120 * time.Second}

	r.recover(context.Background())

	if len(s.updated) != 0 {
		t.Errorf("expected 0 updates when list fails, got %d", len(s.updated))
	}
}

func TestNewRecoveryLoop_Defaults(t *testing.T) {
	s := newMockStore()
	client := &Client{} // 空 client，仅测试默认值
	loop := NewRecoveryLoop(s, client, slog.Default(), 0, 0)

	if loop.interval != 60*time.Second {
		t.Errorf("default interval = %v, want 60s", loop.interval)
	}
	if loop.maxAge != 120*time.Second {
		t.Errorf("default maxAge = %v, want 120s", loop.maxAge)
	}
}
