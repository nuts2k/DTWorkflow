package queue

import (
	"context"
	"errors"
	"fmt"
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

func (m *mockStoreForRecovery) PurgeTasks(_ context.Context, _ time.Duration, _ model.TaskStatus) (int64, error) {
	return 0, nil
}
func (m *mockStoreForRecovery) Ping(_ context.Context) error { return nil }

func (m *mockStoreForRecovery) Close() error { return nil }

func (m *mockStoreForRecovery) SaveReviewResult(_ context.Context, _ *model.ReviewRecord) error {
	return nil
}

// mockEnqueuerForRecovery 实现 Enqueuer 接口，用于 recovery 测试
type mockEnqueuerForRecovery struct {
	enqueueErr error
	enqueueID  string
}

func (mc *mockEnqueuerForRecovery) Enqueue(_ context.Context, _ model.TaskPayload, opts EnqueueOptions) (string, error) {
	if mc.enqueueErr != nil {
		return "", mc.enqueueErr
	}
	if mc.enqueueID != "" {
		return mc.enqueueID, nil
	}
	return opts.TaskID + "-requeued", nil
}

func TestRecoveryLoop_RequeuesOrphans(t *testing.T) {
	orphan := &model.TaskRecord{
		ID:         "orphan-1",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusPending,
		Priority:   model.PriorityHigh,
		Payload:    model.TaskPayload{TaskType: model.TaskTypeReviewPR},
		DeliveryID: "delivery-orphan-1",
		MaxRetry:   3,
	}

	s := &mockStoreForRecovery{orphans: []*model.TaskRecord{orphan}}
	mc := &mockEnqueuerForRecovery{}
	r := NewRecoveryLoop(s, mc, slog.Default(), 60*time.Second, 120*time.Second)

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
	mc := &mockEnqueuerForRecovery{}
	r := NewRecoveryLoop(s, mc, slog.Default(), 60*time.Second, 120*time.Second)

	r.recover(context.Background())

	if len(s.updated) != 0 {
		t.Errorf("expected 0 updates when no orphans, got %d", len(s.updated))
	}
}

func TestRecoveryLoop_ErrTaskIDConflict_StillUpdatesQueued(t *testing.T) {
	orphan := &model.TaskRecord{
		ID:         "orphan-conflict-1",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusPending,
		Priority:   model.PriorityHigh,
		Payload:    model.TaskPayload{TaskType: model.TaskTypeReviewPR},
		DeliveryID: "delivery-conflict-1",
		MaxRetry:   3,
	}

	s := &mockStoreForRecovery{orphans: []*model.TaskRecord{orphan}}
	// 模拟入队返回 ErrTaskIDConflict（任务已在 asynq 队列中）
	mc := &mockEnqueuerForRecovery{enqueueErr: asynq.ErrTaskIDConflict}
	r := NewRecoveryLoop(s, mc, slog.Default(), 60*time.Second, 120*time.Second)

	r.recover(context.Background())

	if len(s.updated) != 1 {
		t.Fatalf("expected 1 updated record on ErrTaskIDConflict, got %d", len(s.updated))
	}
	if s.updated[0].Status != model.TaskStatusQueued {
		t.Errorf("updated status = %q, want %q", s.updated[0].Status, model.TaskStatusQueued)
	}
	// ErrTaskIDConflict 时 AsynqID 应设为 buildAsynqTaskID 生成的 TaskID
	expectedAsynqID := buildAsynqTaskID(orphan.DeliveryID, orphan.TaskType)
	if s.updated[0].AsynqID != expectedAsynqID {
		t.Errorf("updated AsynqID = %q, want %q", s.updated[0].AsynqID, expectedAsynqID)
	}
}

func TestRecoveryLoop_EnqueueFail_SkipsUpdate(t *testing.T) {
	orphan := &model.TaskRecord{
		ID:         "orphan-2",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusPending,
		Priority:   model.PriorityNormal,
		Payload:    model.TaskPayload{TaskType: model.TaskTypeFixIssue},
		DeliveryID: "delivery-orphan-2",
		MaxRetry:   3,
	}

	s := &mockStoreForRecovery{orphans: []*model.TaskRecord{orphan}}
	mc := &mockEnqueuerForRecovery{enqueueErr: errors.New("redis down")}
	r := NewRecoveryLoop(s, mc, slog.Default(), 60*time.Second, 120*time.Second)

	r.recover(context.Background())

	// 入队失败时不应更新状态
	if len(s.updated) != 0 {
		t.Errorf("expected 0 updates when enqueue fails, got %d", len(s.updated))
	}
}

func TestRecoveryLoop_ListFail_NoUpdate(t *testing.T) {
	s := &mockStoreForRecovery{listErr: errors.New("db error")}
	mc := &mockEnqueuerForRecovery{}
	r := NewRecoveryLoop(s, mc, slog.Default(), 60*time.Second, 120*time.Second)

	r.recover(context.Background())

	if len(s.updated) != 0 {
		t.Errorf("expected 0 updates when list fails, got %d", len(s.updated))
	}
}

func TestRecoveryLoop_MaxRetryExceeded_MarksFailed(t *testing.T) {
	orphan := &model.TaskRecord{
		ID:         "orphan-maxed",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusPending,
		Priority:   model.PriorityHigh,
		Payload:    model.TaskPayload{TaskType: model.TaskTypeReviewPR},
		DeliveryID: "delivery-orphan-maxed",
		MaxRetry:   3,
	}

	s := &mockStoreForRecovery{orphans: []*model.TaskRecord{orphan}}
	mc := &mockEnqueuerForRecovery{}
	r := NewRecoveryLoop(s, mc, slog.Default(), 60*time.Second, 120*time.Second)

	// 预设恢复尝试次数已达上限（使用内部 recoveryAttempts 而非 record.RetryCount）
	r.recoveryAttempts[orphan.ID] = 3

	r.recover(context.Background())

	if len(s.updated) != 1 {
		t.Fatalf("expected 1 updated record, got %d", len(s.updated))
	}
	if s.updated[0].Status != model.TaskStatusFailed {
		t.Errorf("updated status = %q, want %q", s.updated[0].Status, model.TaskStatusFailed)
	}
}

func TestRecoveryLoop_CancelledContext(t *testing.T) {
	orphan := &model.TaskRecord{
		ID:         "orphan-cancelled",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusPending,
		Priority:   model.PriorityHigh,
		Payload:    model.TaskPayload{TaskType: model.TaskTypeReviewPR},
		DeliveryID: "delivery-orphan-cancelled",
		MaxRetry:   3,
	}

	s := &mockStoreForRecovery{orphans: []*model.TaskRecord{orphan}}
	mc := &mockEnqueuerForRecovery{}
	r := NewRecoveryLoop(s, mc, slog.Default(), 60*time.Second, 120*time.Second)

	// 已取消的 context 应跳过恢复
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.recover(ctx)

	if len(s.updated) != 0 {
		t.Errorf("已取消 context 不应有更新，得到 %d 条", len(s.updated))
	}
}

func TestRecoveryLoop_BatchLimit(t *testing.T) {
	// 创建超过 100 条孤儿任务，验证批次上限
	orphans := make([]*model.TaskRecord, 105)
	for i := range orphans {
		orphans[i] = &model.TaskRecord{
			ID:         fmt.Sprintf("orphan-batch-%d", i),
			TaskType:   model.TaskTypeReviewPR,
			Status:     model.TaskStatusPending,
			Priority:   model.PriorityNormal,
			Payload:    model.TaskPayload{TaskType: model.TaskTypeReviewPR},
			DeliveryID: fmt.Sprintf("delivery-batch-%d", i),
			MaxRetry:   3,
		}
	}

	s := &mockStoreForRecovery{orphans: orphans}
	mc := &mockEnqueuerForRecovery{}
	r := NewRecoveryLoop(s, mc, slog.Default(), 60*time.Second, 120*time.Second)

	r.recover(context.Background())

	// 应最多处理 100 条
	if len(s.updated) != 100 {
		t.Errorf("期望处理 100 条，实际处理 %d 条", len(s.updated))
	}
}

func TestRecoveryLoop_UpdateFail_AfterMaxRetry(t *testing.T) {
	orphan := &model.TaskRecord{
		ID:         "orphan-upd-fail",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusPending,
		Priority:   model.PriorityHigh,
		Payload:    model.TaskPayload{TaskType: model.TaskTypeReviewPR},
		DeliveryID: "delivery-orphan-upd-fail",
		MaxRetry:   3,
	}

	s := &mockStoreForRecovery{
		orphans:   []*model.TaskRecord{orphan},
		updateErr: errors.New("db error"),
	}
	mc := &mockEnqueuerForRecovery{}
	r := NewRecoveryLoop(s, mc, slog.Default(), 60*time.Second, 120*time.Second)
	r.recoveryAttempts[orphan.ID] = 3

	r.recover(context.Background())

	// UpdateTask 失败，不应有成功更新
	if len(s.updated) != 0 {
		t.Errorf("UpdateTask 失败时不应有成功更新，得到 %d", len(s.updated))
	}
}

func TestRecoveryLoop_UpdateFail_AfterRequeue(t *testing.T) {
	orphan := &model.TaskRecord{
		ID:         "orphan-upd-fail2",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusPending,
		Priority:   model.PriorityHigh,
		Payload:    model.TaskPayload{TaskType: model.TaskTypeReviewPR},
		DeliveryID: "delivery-orphan-upd-fail2",
		MaxRetry:   3,
	}

	s := &mockStoreForRecovery{
		orphans:   []*model.TaskRecord{orphan},
		updateErr: errors.New("db error"),
	}
	mc := &mockEnqueuerForRecovery{}
	r := NewRecoveryLoop(s, mc, slog.Default(), 60*time.Second, 120*time.Second)

	r.recover(context.Background())

	// 入队成功但 UpdateTask 失败，不应有成功更新
	if len(s.updated) != 0 {
		t.Errorf("UpdateTask 失败时不应有成功更新，得到 %d", len(s.updated))
	}
	// recoveryAttempts 不应递增（因为 update 失败提前 return）
	if r.recoveryAttempts[orphan.ID] != 0 {
		t.Errorf("recoveryAttempts 应为 0，得到 %d", r.recoveryAttempts[orphan.ID])
	}
}

func TestRecoveryLoop_Run_StopsOnCancel(t *testing.T) {
	s := &mockStoreForRecovery{}
	mc := &mockEnqueuerForRecovery{}
	r := NewRecoveryLoop(s, mc, slog.Default(), 10*time.Millisecond, 120*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	// 等待至少一个 tick 然后取消
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Run 正常退出
	case <-time.After(2 * time.Second):
		t.Fatal("RecoveryLoop.Run 未在取消后及时退出")
	}
}

func TestNewRecoveryLoop_Defaults(t *testing.T) {
	s := &mockStoreForRecovery{}
	// 使用 mock 而非 &Client{} 零值，避免对 Client 内部结构的隐式依赖
	mc := &mockEnqueuerForRecovery{}
	loop := NewRecoveryLoop(s, mc, slog.Default(), 0, 0)

	if loop.interval != 60*time.Second {
		t.Errorf("default interval = %v, want 60s", loop.interval)
	}
	if loop.maxAge != 120*time.Second {
		t.Errorf("default maxAge = %v, want 120s", loop.maxAge)
	}
}
