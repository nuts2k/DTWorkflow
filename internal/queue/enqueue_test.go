package queue

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/webhook"
)

// mockStore 实现 store.Store 接口的内存 mock
type mockStore struct {
	tasks                map[string]*model.TaskRecord
	byDeliveryID         map[string]*model.TaskRecord
	createErr            error
	updateErr            error
	failUpdateAt         int
	updateCalls          int
	findErr              error
	activePRTasks        []*model.TaskRecord // FindActivePRTasks 返回的任务列表
	findActivePRTasksErr error               // FindActivePRTasks 返回的错误
}

func newMockStore() *mockStore {
	return &mockStore{
		tasks:        make(map[string]*model.TaskRecord),
		byDeliveryID: make(map[string]*model.TaskRecord),
	}
}

func (m *mockStore) CreateTask(_ context.Context, record *model.TaskRecord) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.tasks[record.ID] = record
	if record.DeliveryID != "" {
		key := record.DeliveryID + ":" + string(record.TaskType)
		m.byDeliveryID[key] = record
	}
	return nil
}

func (m *mockStore) GetTask(_ context.Context, id string) (*model.TaskRecord, error) {
	r, ok := m.tasks[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return r, nil
}

func (m *mockStore) UpdateTask(_ context.Context, record *model.TaskRecord) error {
	m.updateCalls++
	if m.updateErr != nil {
		return m.updateErr
	}
	if m.failUpdateAt > 0 && m.updateCalls == m.failUpdateAt {
		return errors.New("update failed at configured call")
	}
	m.tasks[record.ID] = record
	return nil
}

func (m *mockStore) ListTasks(_ context.Context, _ store.ListOptions) ([]*model.TaskRecord, error) {
	return nil, nil
}

func (m *mockStore) FindByDeliveryID(_ context.Context, deliveryID string, taskType model.TaskType) (*model.TaskRecord, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	key := deliveryID + ":" + string(taskType)
	r, ok := m.byDeliveryID[key]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (m *mockStore) ListOrphanTasks(_ context.Context, _ time.Duration) ([]*model.TaskRecord, error) {
	return nil, nil
}

func (m *mockStore) PurgeTasks(_ context.Context, _ time.Duration, _ model.TaskStatus) (int64, error) {
	return 0, nil
}

func (m *mockStore) Ping(_ context.Context) error { return nil }

func (m *mockStore) Close() error { return nil }

func (m *mockStore) GetReviewResult(_ context.Context, _ string) (*model.ReviewRecord, error) {
	return nil, nil
}
func (m *mockStore) ListReviewResults(_ context.Context, _ string, _ int, _ int) ([]*model.ReviewRecord, error) {
	return nil, nil
}
func (m *mockStore) SaveReviewResult(_ context.Context, _ *model.ReviewRecord) error {
	return nil
}

func (m *mockStore) FindActivePRTasks(_ context.Context, _ string, _ int64, _ model.TaskType) ([]*model.TaskRecord, error) {
	if m.findActivePRTasksErr != nil {
		return nil, m.findActivePRTasksErr
	}
	return m.activePRTasks, nil
}

func (m *mockStore) HasNewerReviewTask(_ context.Context, _ string, _ int64, _ time.Time) (bool, error) {
	return false, nil
}

func (m *mockStore) ListReviewResultsByTimeRange(_ context.Context, _, _ time.Time) ([]*model.ReviewRecord, error) {
	return nil, nil
}

// mockEnqueuer 实现 Enqueuer 接口的 mock
type mockEnqueuer struct {
	enqueueErr error
	enqueuedID string
}

func (mc *mockEnqueuer) Enqueue(_ context.Context, _ model.TaskPayload, opts EnqueueOptions) (string, error) {
	if mc.enqueueErr != nil {
		return "", mc.enqueueErr
	}
	id := opts.TaskID
	if id == "" {
		id = mc.enqueuedID
	}
	return id, nil
}

func TestHandlePullRequest_CreatesTask(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-123"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		DeliveryID:  "delivery-001",
		Repository:  webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		PullRequest: webhook.PullRequestRef{Number: 42},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest error: %v", err)
	}

	// 验证任务已创建且状态为 queued
	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(s.tasks))
	}
	for _, record := range s.tasks {
		if record.Status != model.TaskStatusQueued {
			t.Errorf("task status = %q, want %q", record.Status, model.TaskStatusQueued)
		}
		if record.AsynqID == "" {
			t.Error("task AsynqID should not be empty")
		}
	}
}

func TestHandlePullRequest_Idempotent(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-456"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		DeliveryID:  "delivery-dup",
		Repository:  webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		PullRequest: webhook.PullRequestRef{Number: 1},
	}

	// 第一次调用
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	// 第二次调用（相同 delivery_id）
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("second call error: %v", err)
	}

	// 只应有一条任务记录
	if len(s.tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(s.tasks))
	}
}

func TestHandlePullRequest_EnqueueFail_KeepsPending(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueueErr: errors.New("redis unavailable")}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		DeliveryID:  "delivery-fail",
		Repository:  webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		PullRequest: webhook.PullRequestRef{Number: 99},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest should not return error on enqueue fail: %v", err)
	}

	// 任务应存在，状态仍为 pending（未被更新为 queued）
	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(s.tasks))
	}
	for _, record := range s.tasks {
		if record.Status != model.TaskStatusPending {
			t.Errorf("task status = %q, want %q", record.Status, model.TaskStatusPending)
		}
	}
}

func TestNewEnqueueHandler_PanicOnNilClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewEnqueueHandler(nil client) 应 panic")
		}
	}()
	NewEnqueueHandler(nil, nil, newMockStore(), slog.Default())
}

func TestNewEnqueueHandler_PanicOnNilStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewEnqueueHandler(nil store) 应 panic")
		}
	}()
	NewEnqueueHandler(&mockEnqueuer{}, nil, nil, slog.Default())
}

func TestNewEnqueueHandler_NilLoggerUsesDefault(t *testing.T) {
	h := NewEnqueueHandler(&mockEnqueuer{}, nil, newMockStore(), nil)
	if h == nil {
		t.Fatal("NewEnqueueHandler 应返回非 nil")
	}
}

func TestHandlePullRequest_IncompleteData(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-incomplete"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	// RepoFullName 为空
	event := webhook.PullRequestEvent{
		DeliveryID:  "delivery-incomplete-1",
		Repository:  webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "", CloneURL: "https://gitea.example.com/org/repo.git"},
		PullRequest: webhook.PullRequestRef{Number: 1},
	}
	err := h.HandlePullRequest(context.Background(), event)
	if err == nil {
		t.Fatal("RepoFullName 为空应返回错误")
	}

	// CloneURL 为空
	event2 := webhook.PullRequestEvent{
		DeliveryID:  "delivery-incomplete-2",
		Repository:  webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: ""},
		PullRequest: webhook.PullRequestRef{Number: 1},
	}
	err = h.HandlePullRequest(context.Background(), event2)
	if err == nil {
		t.Fatal("CloneURL 为空应返回错误")
	}
}

func TestHandlePullRequest_FindByDeliveryIDError(t *testing.T) {
	s := newMockStore()
	s.findErr = errors.New("db error")
	mc := &mockEnqueuer{enqueuedID: "asynq-err"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		DeliveryID:  "delivery-find-err",
		Repository:  webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		PullRequest: webhook.PullRequestRef{Number: 1},
	}
	err := h.HandlePullRequest(context.Background(), event)
	if err == nil {
		t.Fatal("FindByDeliveryID 错误应传播")
	}
}

func TestHandlePullRequest_CreateTaskError(t *testing.T) {
	s := newMockStore()
	s.createErr = errors.New("db write error")
	mc := &mockEnqueuer{enqueuedID: "asynq-create-err"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		DeliveryID:  "delivery-create-err",
		Repository:  webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		PullRequest: webhook.PullRequestRef{Number: 1},
	}
	err := h.HandlePullRequest(context.Background(), event)
	if err == nil {
		t.Fatal("CreateTask 错误应传播")
	}
}

func TestHandlePullRequest_UpdateTaskError_StillSucceeds(t *testing.T) {
	s := newMockStore()
	s.updateErr = errors.New("db update error")
	mc := &mockEnqueuer{enqueuedID: "asynq-upd-err"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		DeliveryID:  "delivery-upd-err",
		Repository:  webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		PullRequest: webhook.PullRequestRef{Number: 1},
	}
	// UpdateTask 失败不应返回错误（任务已成功入队）
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("UpdateTask 失败不应导致整体失败: %v", err)
	}
}

func TestHandleIssueLabel_Idempotent(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-issue-dup"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.IssueLabelEvent{
		DeliveryID:   "delivery-issue-dup",
		AutoFixAdded: true,
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		Issue:        webhook.IssueRef{Number: 10},
	}
	if err := h.HandleIssueLabel(context.Background(), event); err != nil {
		t.Fatalf("第一次调用失败: %v", err)
	}
	if err := h.HandleIssueLabel(context.Background(), event); err != nil {
		t.Fatalf("第二次调用失败: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Errorf("幂等检查失败: 期望 1 条任务，得到 %d", len(s.tasks))
	}
}

func TestHandleIssueLabel_FindByDeliveryIDError(t *testing.T) {
	s := newMockStore()
	s.findErr = errors.New("db error")
	mc := &mockEnqueuer{enqueuedID: "asynq-err"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.IssueLabelEvent{
		DeliveryID:   "delivery-issue-find-err",
		AutoFixAdded: true,
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		Issue:        webhook.IssueRef{Number: 10},
	}
	err := h.HandleIssueLabel(context.Background(), event)
	if err == nil {
		t.Fatal("FindByDeliveryID 错误应传播")
	}
}

func TestHandleIssueLabel_IncompleteData(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-incomplete"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.IssueLabelEvent{
		DeliveryID:   "delivery-issue-incomplete",
		AutoFixAdded: true,
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "", CloneURL: "https://gitea.example.com/org/repo.git"},
		Issue:        webhook.IssueRef{Number: 10},
	}
	err := h.HandleIssueLabel(context.Background(), event)
	if err == nil {
		t.Fatal("RepoFullName 为空应返回错误")
	}
}

func TestBuildAsynqTaskID(t *testing.T) {
	// 非空 deliveryID
	id := buildAsynqTaskID("delivery-123", model.TaskTypeReviewPR)
	if id != "delivery-123:review_pr" {
		t.Errorf("buildAsynqTaskID = %q, want %q", id, "delivery-123:review_pr")
	}

	// 空 deliveryID
	id = buildAsynqTaskID("", model.TaskTypeReviewPR)
	if id != "" {
		t.Errorf("buildAsynqTaskID('', ...) = %q, want empty", id)
	}
}

func TestHandleIssueLabel_OnlyWhenAutoFixAdded(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-789"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	// AutoFixAdded=false，不应创建任务
	event := webhook.IssueLabelEvent{
		DeliveryID:   "delivery-issue-1",
		AutoFixAdded: false,
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		Issue:        webhook.IssueRef{Number: 10},
	}
	if err := h.HandleIssueLabel(context.Background(), event); err != nil {
		t.Fatalf("HandleIssueLabel error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("expected 0 tasks when AutoFixAdded=false, got %d", len(s.tasks))
	}

	// AutoFixAdded=true，应创建任务
	event2 := webhook.IssueLabelEvent{
		DeliveryID:   "delivery-issue-2",
		AutoFixAdded: true,
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		Issue:        webhook.IssueRef{Number: 20},
	}
	if err := h.HandleIssueLabel(context.Background(), event2); err != nil {
		t.Fatalf("HandleIssueLabel error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Errorf("expected 1 task when AutoFixAdded=true, got %d", len(s.tasks))
	}
}

// mockTaskCanceller 实现 TaskCanceller 接口的内存 mock
type mockTaskCanceller struct {
	deleteErr   error
	cancelErr   error
	deleteCalls []string // 记录 Delete 被调用时的 taskID
	cancelCalls []string // 记录 CancelProcessing 被调用时的 taskID
}

func (m *mockTaskCanceller) Delete(_ context.Context, _ string, taskID string) error {
	m.deleteCalls = append(m.deleteCalls, taskID)
	return m.deleteErr
}

func (m *mockTaskCanceller) CancelProcessing(_ context.Context, taskID string) error {
	m.cancelCalls = append(m.cancelCalls, taskID)
	return m.cancelErr
}

// TestCancelActivePRTasks_HasOldTasks 存在旧任务时，cancelActivePRTasks 应调用 cancelTask
// 并返回正确的 SupersededInfo（Count=1, LastHeadSHA 为旧任务的 HeadSHA）
func TestCancelActivePRTasks_HasOldTasks(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-new"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	// 预置一条旧的活跃任务
	oldTask := &model.TaskRecord{
		ID:           "old-task-1",
		AsynqID:      "asynq-old-1",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		PRNumber:     42,
		Payload:      model.TaskPayload{HeadSHA: "abc123"},
	}
	s.tasks[oldTask.ID] = oldTask
	s.activePRTasks = []*model.TaskRecord{oldTask}

	tasks, info := h.listActivePRTasks(context.Background(), "org/repo", 42)
	h.cancelTasks(context.Background(), tasks)

	// 应取消了 1 个旧任务
	if info.Count != 1 {
		t.Errorf("SupersededInfo.Count = %d, want 1", info.Count)
	}
	if info.LastHeadSHA != "abc123" {
		t.Errorf("SupersededInfo.LastHeadSHA = %q, want %q", info.LastHeadSHA, "abc123")
	}
	// canceller.Delete 应被调用（queued 状态走 Delete 路径）
	if len(canceller.deleteCalls) != 1 || canceller.deleteCalls[0] != "asynq-old-1" {
		t.Errorf("deleteCalls = %v, want [asynq-old-1]", canceller.deleteCalls)
	}
	// 旧任务状态应被更新为 cancelled
	updated := s.tasks[oldTask.ID]
	if updated.Status != model.TaskStatusCancelled {
		t.Errorf("旧任务状态 = %q, want %q", updated.Status, model.TaskStatusCancelled)
	}
}

// TestCancelActivePRTasks_NoOldTasks 无旧任务时，cancelActivePRTasks 应返回空 SupersededInfo
func TestCancelActivePRTasks_NoOldTasks(t *testing.T) {
	s := newMockStore()
	// activePRTasks 默认为 nil，返回空列表
	mc := &mockEnqueuer{enqueuedID: "asynq-new"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	tasks, info := h.listActivePRTasks(context.Background(), "org/repo", 1)

	if info.Count != 0 {
		t.Errorf("SupersededInfo.Count = %d, want 0", info.Count)
	}
	if info.LastHeadSHA != "" {
		t.Errorf("SupersededInfo.LastHeadSHA = %q, want empty", info.LastHeadSHA)
	}
	if len(tasks) != 0 {
		t.Errorf("tasks len = %d, want 0", len(tasks))
	}
}

// TestCancelActivePRTasks_QueryFailed FindActivePRTasks 失败时，应记日志并返回空 SupersededInfo
func TestCancelActivePRTasks_QueryFailed(t *testing.T) {
	s := newMockStore()
	s.findActivePRTasksErr = errors.New("db error")
	mc := &mockEnqueuer{enqueuedID: "asynq-new"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	tasks, info := h.listActivePRTasks(context.Background(), "org/repo", 1)

	// 查询失败时不应 panic，应静默返回空 info
	if info.Count != 0 {
		t.Errorf("SupersededInfo.Count = %d, want 0", info.Count)
	}
	if len(tasks) != 0 {
		t.Errorf("tasks len = %d, want 0", len(tasks))
	}
}

// TestCancelTask_PendingDelete pending 状态任务应调用 Delete，而不调用 CancelProcessing
func TestCancelTask_PendingDelete(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	task := &model.TaskRecord{
		ID:       "task-pending",
		AsynqID:  "asynq-pending-1",
		Status:   model.TaskStatusPending,
		Priority: model.PriorityHigh,
	}
	s.tasks[task.ID] = task

	h.cancelTask(context.Background(), task)

	if len(canceller.deleteCalls) != 1 || canceller.deleteCalls[0] != "asynq-pending-1" {
		t.Errorf("deleteCalls = %v, want [asynq-pending-1]", canceller.deleteCalls)
	}
	if len(canceller.cancelCalls) != 0 {
		t.Errorf("cancelCalls = %v, want empty", canceller.cancelCalls)
	}
	if task.Status != model.TaskStatusCancelled {
		t.Errorf("task.Status = %q, want %q", task.Status, model.TaskStatusCancelled)
	}
}

func TestCancelTask_DeleteFailure_LeavesTaskRunnable(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{deleteErr: errors.New("redis delete failed")}
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	task := &model.TaskRecord{
		ID:       "task-delete-fail",
		AsynqID:  "asynq-delete-fail",
		Status:   model.TaskStatusQueued,
		Priority: model.PriorityHigh,
	}
	s.tasks[task.ID] = task

	cancelled := h.cancelTask(context.Background(), task)

	if cancelled {
		t.Fatal("Delete 失败时不应返回 cancelled=true")
	}
	if task.Status != model.TaskStatusQueued {
		t.Errorf("task.Status = %q, want %q", task.Status, model.TaskStatusQueued)
	}
	if task.CompletedAt != nil {
		t.Fatal("Delete 失败时不应设置 CompletedAt")
	}
}

// TestCancelTask_RunningCancel running 状态任务应调用 CancelProcessing，而不调用 Delete
func TestCancelTask_RunningCancel(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	task := &model.TaskRecord{
		ID:       "task-running",
		AsynqID:  "asynq-running-1",
		Status:   model.TaskStatusRunning,
		Priority: model.PriorityHigh,
	}
	s.tasks[task.ID] = task

	h.cancelTask(context.Background(), task)

	if len(canceller.cancelCalls) != 1 || canceller.cancelCalls[0] != "asynq-running-1" {
		t.Errorf("cancelCalls = %v, want [asynq-running-1]", canceller.cancelCalls)
	}
	if len(canceller.deleteCalls) != 0 {
		t.Errorf("deleteCalls = %v, want empty", canceller.deleteCalls)
	}
	if task.Status != model.TaskStatusCancelled {
		t.Errorf("task.Status = %q, want %q", task.Status, model.TaskStatusCancelled)
	}
}

func TestCancelTask_CancelProcessingFailure_LeavesTaskRunnable(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{cancelErr: errors.New("cancel failed")}
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	task := &model.TaskRecord{
		ID:       "task-cancel-fail",
		AsynqID:  "asynq-cancel-fail",
		Status:   model.TaskStatusRunning,
		Priority: model.PriorityHigh,
	}
	s.tasks[task.ID] = task

	cancelled := h.cancelTask(context.Background(), task)

	if cancelled {
		t.Fatal("CancelProcessing 失败时不应返回 cancelled=true")
	}
	if task.Status != model.TaskStatusRunning {
		t.Errorf("task.Status = %q, want %q", task.Status, model.TaskStatusRunning)
	}
	if task.CompletedAt != nil {
		t.Fatal("CancelProcessing 失败时不应设置 CompletedAt")
	}
}

// TestCancelTask_NoAsynqID 无 AsynqID 时，应跳过 asynq 操作，仅更新 SQLite 状态为 cancelled
func TestCancelTask_NoAsynqID(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	task := &model.TaskRecord{
		ID:       "task-no-asynqid",
		AsynqID:  "", // 无 AsynqID
		Status:   model.TaskStatusPending,
		Priority: model.PriorityHigh,
	}
	s.tasks[task.ID] = task

	h.cancelTask(context.Background(), task)

	// canceller 不应被调用
	if len(canceller.deleteCalls) != 0 {
		t.Errorf("deleteCalls = %v, want empty（无 AsynqID 不应调用 Delete）", canceller.deleteCalls)
	}
	if len(canceller.cancelCalls) != 0 {
		t.Errorf("cancelCalls = %v, want empty（无 AsynqID 不应调用 CancelProcessing）", canceller.cancelCalls)
	}
	// SQLite 状态仍应更新为 cancelled
	if task.Status != model.TaskStatusCancelled {
		t.Errorf("task.Status = %q, want %q", task.Status, model.TaskStatusCancelled)
	}
}

// TestHandlePullRequest_WithSuperseded HandlePullRequest 完整流程：存在旧任务时
// 应取消旧任务并在新任务 payload 中记录 SupersededCount 和 PreviousHeadSHA
func TestHandlePullRequest_WithSuperseded(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-new-pr"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	// 预置旧的活跃任务
	oldTask := &model.TaskRecord{
		ID:           "old-pr-task",
		AsynqID:      "asynq-old-pr",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		PRNumber:     7,
		Payload:      model.TaskPayload{HeadSHA: "deadbeef"},
	}
	s.tasks[oldTask.ID] = oldTask
	s.activePRTasks = []*model.TaskRecord{oldTask}

	event := webhook.PullRequestEvent{
		DeliveryID:  "delivery-superseded-1",
		Repository:  webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		PullRequest: webhook.PullRequestRef{Number: 7, HeadSHA: "cafebabe"},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest error: %v", err)
	}

	// 应有 2 条任务记录（旧的 cancelled + 新的 queued）
	if len(s.tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(s.tasks))
	}

	// 找到新建的任务（非 old-pr-task）
	var newRecord *model.TaskRecord
	for id, r := range s.tasks {
		if id != oldTask.ID {
			newRecord = r
		}
	}
	if newRecord == nil {
		t.Fatal("未找到新建的任务记录")
	}

	// 验证新任务 payload 中的 SupersededCount 和 PreviousHeadSHA
	if newRecord.Payload.SupersededCount != 1 {
		t.Errorf("SupersededCount = %d, want 1", newRecord.Payload.SupersededCount)
	}
	if newRecord.Payload.PreviousHeadSHA != "deadbeef" {
		t.Errorf("PreviousHeadSHA = %q, want %q", newRecord.Payload.PreviousHeadSHA, "deadbeef")
	}

	// 旧任务应已被标记为 cancelled
	if s.tasks[oldTask.ID].Status != model.TaskStatusCancelled {
		t.Errorf("旧任务状态 = %q, want %q", s.tasks[oldTask.ID].Status, model.TaskStatusCancelled)
	}

	// canceller.Delete 应被调用（旧任务为 queued 状态）
	if len(canceller.deleteCalls) != 1 || canceller.deleteCalls[0] != "asynq-old-pr" {
		t.Errorf("deleteCalls = %v, want [asynq-old-pr]", canceller.deleteCalls)
	}
}

func TestHandlePullRequest_InvalidPayload_DoesNotCancelOldTask(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-new-pr"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	oldTask := &model.TaskRecord{
		ID:           "old-pr-task-invalid-payload",
		AsynqID:      "asynq-old-pr-invalid-payload",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		PRNumber:     7,
		Payload:      model.TaskPayload{HeadSHA: "deadbeef"},
	}
	s.tasks[oldTask.ID] = oldTask
	s.activePRTasks = []*model.TaskRecord{oldTask}

	event := webhook.PullRequestEvent{
		DeliveryID:  "delivery-invalid-payload",
		Repository:  webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo"},
		PullRequest: webhook.PullRequestRef{Number: 7, HeadSHA: "cafebabe"},
	}

	err := h.HandlePullRequest(context.Background(), event)
	if err == nil {
		t.Fatal("webhook 数据不完整时应返回错误")
	}
	if s.tasks[oldTask.ID].Status != model.TaskStatusQueued {
		t.Errorf("旧任务状态 = %q, want %q", s.tasks[oldTask.ID].Status, model.TaskStatusQueued)
	}
	if len(canceller.deleteCalls) != 0 {
		t.Errorf("deleteCalls = %v, want empty", canceller.deleteCalls)
	}
}

func TestHandlePullRequest_CreateTaskFailed_DoesNotCancelOldTask(t *testing.T) {
	s := newMockStore()
	s.createErr = errors.New("sqlite down")
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-new-pr"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	oldTask := &model.TaskRecord{
		ID:           "old-pr-task-create-fail",
		AsynqID:      "asynq-old-pr-create-fail",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		PRNumber:     7,
		Payload:      model.TaskPayload{HeadSHA: "deadbeef"},
	}
	s.tasks[oldTask.ID] = oldTask
	s.activePRTasks = []*model.TaskRecord{oldTask}

	event := webhook.PullRequestEvent{
		DeliveryID:  "delivery-create-failed",
		Repository:  webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		PullRequest: webhook.PullRequestRef{Number: 7, HeadSHA: "cafebabe"},
	}

	err := h.HandlePullRequest(context.Background(), event)
	if err == nil {
		t.Fatal("CreateTask 失败时应返回错误")
	}
	if s.tasks[oldTask.ID].Status != model.TaskStatusQueued {
		t.Errorf("旧任务状态 = %q, want %q", s.tasks[oldTask.ID].Status, model.TaskStatusQueued)
	}
	if len(canceller.deleteCalls) != 0 {
		t.Errorf("deleteCalls = %v, want empty", canceller.deleteCalls)
	}
}
