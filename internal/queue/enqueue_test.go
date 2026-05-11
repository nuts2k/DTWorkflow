package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/test"
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

	// M4.2: gen_tests 相关
	activeGenTestsModules    []string // ListActiveGenTestsModules 返回值
	activeGenTestsModulesErr error    // ListActiveGenTestsModules 错误
	saveTestGenCalls         int      // SaveTestGenResult 调用计数
	createCalls              int
	createErrAt              int
}

func newMockStore() *mockStore {
	return &mockStore{
		tasks:        make(map[string]*model.TaskRecord),
		byDeliveryID: make(map[string]*model.TaskRecord),
	}
}

func (m *mockStore) CreateTask(_ context.Context, record *model.TaskRecord) error {
	m.createCalls++
	if m.createErr != nil {
		return m.createErr
	}
	if m.createErrAt > 0 && m.createCalls == m.createErrAt {
		return errors.New("create failed at configured call")
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

func (m *mockStore) FindActiveIssueTasks(_ context.Context, repoFullName string, issueNumber int64, taskType model.TaskType) ([]*model.TaskRecord, error) {
	if m.findActivePRTasksErr != nil {
		return nil, m.findActivePRTasksErr
	}
	var tasks []*model.TaskRecord
	for _, task := range m.tasks {
		if task.RepoFullName != repoFullName {
			continue
		}
		if task.TaskType != taskType {
			continue
		}
		if task.Status != model.TaskStatusPending && task.Status != model.TaskStatusQueued && task.Status != model.TaskStatusRunning {
			continue
		}
		if task.Payload.IssueNumber != issueNumber {
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (m *mockStore) FindActiveGenTestsTasks(_ context.Context, repoFullName, module string) ([]*model.TaskRecord, error) {
	if m.findActivePRTasksErr != nil {
		return nil, m.findActivePRTasksErr
	}
	var tasks []*model.TaskRecord
	for _, task := range m.tasks {
		if task.RepoFullName != repoFullName {
			continue
		}
		if task.TaskType != model.TaskTypeGenTests {
			continue
		}
		if task.Status != model.TaskStatusPending &&
			task.Status != model.TaskStatusQueued &&
			task.Status != model.TaskStatusRunning &&
			task.Status != model.TaskStatusRetrying {
			continue
		}
		// 模拟 COALESCE(json_extract(payload, '$.module'), '') = module 的匹配语义
		if task.Payload.Module != module {
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (m *mockStore) HasNewerReviewTask(_ context.Context, _ string, _ int64, _ time.Time) (bool, error) {
	return false, nil
}

func (m *mockStore) ListReviewResultsByTimeRange(_ context.Context, _, _ time.Time) ([]*model.ReviewRecord, error) {
	return nil, nil
}

func (m *mockStore) GetLatestAnalysisByIssue(_ context.Context, _ string, _ int64) (*model.TaskRecord, error) {
	return nil, nil
}

// M4.2: gen_tests 产出持久化与 module 拦截查询（recorder 风格 mock）
func (m *mockStore) SaveTestGenResult(_ context.Context, _ *store.TestGenResultRecord) error {
	m.saveTestGenCalls++
	return nil
}

func (m *mockStore) GetTestGenResultByTaskID(_ context.Context, _ string) (*store.TestGenResultRecord, error) {
	return nil, nil
}

// UpdateTestGenResultReviewEnqueued M4.2 I6 partial UPDATE 语义；测试 mock 只计数，
// 统一复用 saveTestGenCalls 追踪 store 交互次数（避免多字段引入多 assertion）。
func (m *mockStore) UpdateTestGenResultReviewEnqueued(_ context.Context, _ string) error {
	m.saveTestGenCalls++
	return nil
}

func (m *mockStore) SaveE2EResult(_ context.Context, _ *store.E2EResultRecord) error {
	return nil
}

func (m *mockStore) GetE2EResultByTaskID(_ context.Context, _ string) (*store.E2EResultRecord, error) {
	return nil, nil
}

func (m *mockStore) UpdateE2ECreatedIssues(_ context.Context, _ string, _ map[string]int64) error {
	return nil
}

func (m *mockStore) FindActiveTasksByModule(_ context.Context, _, _ string, _ model.TaskType) ([]*model.TaskRecord, error) {
	return nil, nil
}

func (m *mockStore) ListActiveModules(_ context.Context, _ string, _ model.TaskType) ([]string, error) {
	return nil, nil
}

func (m *mockStore) ListActiveGenTestsModules(_ context.Context, _ string) ([]string, error) {
	if m.activeGenTestsModulesErr != nil {
		return nil, m.activeGenTestsModulesErr
	}
	if m.activeGenTestsModules != nil {
		return m.activeGenTestsModules, nil
	}
	var modules []string
	for _, task := range m.tasks {
		if task.TaskType != model.TaskTypeGenTests {
			continue
		}
		if task.Status != model.TaskStatusPending &&
			task.Status != model.TaskStatusQueued &&
			task.Status != model.TaskStatusRunning &&
			task.Status != model.TaskStatusRetrying {
			continue
		}
		modules = append(modules, task.Payload.Module)
	}
	return modules, nil
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
		Action:      "opened",
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
		Action:      "opened",
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
		Action:      "opened",
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
		Action:      "opened",
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
		Action:      "opened",
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
		Action:      "opened",
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
		Action:      "opened",
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
		Action:      "opened",
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
		Action:      "opened",
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
		Action:      "opened",
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
		Action:      "opened",
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

func TestEnqueueManualReview(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-manual-review"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		PRNumber:     42,
		PRTitle:      "test PR",
		HeadSHA:      "abc123",
	}

	taskID, err := h.EnqueueManualReview(context.Background(), payload, "manual:admin")
	if err != nil {
		t.Fatalf("EnqueueManualReview error: %v", err)
	}
	if taskID == "" {
		t.Fatal("返回的 taskID 不应为空")
	}

	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(s.tasks))
	}

	for _, record := range s.tasks {
		if record.TaskType != model.TaskTypeReviewPR {
			t.Errorf("task type = %q, want %q", record.TaskType, model.TaskTypeReviewPR)
		}
		if record.TriggeredBy != "manual:admin" {
			t.Errorf("triggered_by = %q, want %q", record.TriggeredBy, "manual:admin")
		}
		if !strings.HasPrefix(record.DeliveryID, "manual-") {
			t.Errorf("delivery_id = %q, want prefix 'manual-'", record.DeliveryID)
		}
		if record.Priority != model.PriorityHigh {
			t.Errorf("priority = %q, want %q", record.Priority, model.PriorityHigh)
		}
	}
}

func TestEnqueueManualFix(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-manual-fix"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		IssueNumber:  10,
		IssueTitle:   "bug report",
	}

	taskID, err := h.EnqueueManualFix(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("EnqueueManualFix error: %v", err)
	}
	if taskID == "" {
		t.Fatal("返回的 taskID 不应为空")
	}

	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(s.tasks))
	}

	for _, record := range s.tasks {
		if record.TaskType != model.TaskTypeAnalyzeIssue {
			t.Errorf("task type = %q, want %q", record.TaskType, model.TaskTypeAnalyzeIssue)
		}
		if record.TriggeredBy != "manual:ci-bot" {
			t.Errorf("triggered_by = %q, want %q", record.TriggeredBy, "manual:ci-bot")
		}
		if !strings.HasPrefix(record.DeliveryID, "manual-") {
			t.Errorf("delivery_id = %q, want prefix 'manual-'", record.DeliveryID)
		}
		if record.Priority != model.PriorityNormal {
			t.Errorf("priority = %q, want %q", record.Priority, model.PriorityNormal)
		}
	}
}

func TestEnqueueManualFix_CancelsSupersededTasks(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-manual-fix-new"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	oldTask := &model.TaskRecord{
		ID:           "old-fix-task",
		AsynqID:      "asynq-old-fix",
		TaskType:     model.TaskTypeAnalyzeIssue, // M3.4: 默认类型已改为 analyze_issue
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityNormal,
		RepoFullName: "org/repo",
		Payload:      model.TaskPayload{IssueNumber: 10},
	}
	s.tasks[oldTask.ID] = oldTask

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		IssueNumber:  10,
		IssueTitle:   "bug report",
	}

	taskID, err := h.EnqueueManualFix(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("EnqueueManualFix error: %v", err)
	}
	if taskID == "" {
		t.Fatal("返回的 taskID 不应为空")
	}
	if s.tasks[oldTask.ID].Status != model.TaskStatusCancelled {
		t.Errorf("旧任务状态 = %q, want %q", s.tasks[oldTask.ID].Status, model.TaskStatusCancelled)
	}
	if len(canceller.deleteCalls) != 1 || canceller.deleteCalls[0] != "asynq-old-fix" {
		t.Errorf("deleteCalls = %v, want [asynq-old-fix]", canceller.deleteCalls)
	}
}

// TestHandleIssueLabel_CancelsSupersededAnalyzeTasks M3.4: auto-fix 触发 analyze_issue，
// 旧的同类型 analyze_issue 活跃任务应被取消。
func TestHandleIssueLabel_CancelsSupersededAnalyzeTasks(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-webhook-analyze"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	oldTask := &model.TaskRecord{
		ID:           "old-webhook-analyze",
		AsynqID:      "asynq-webhook-old-analyze",
		TaskType:     model.TaskTypeAnalyzeIssue,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityNormal,
		RepoFullName: "org/repo",
		Payload:      model.TaskPayload{IssueNumber: 33},
	}
	s.tasks[oldTask.ID] = oldTask

	event := webhook.IssueLabelEvent{
		DeliveryID:   "delivery-analyze-superseded",
		AutoFixAdded: true,
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		Issue:        webhook.IssueRef{Number: 33, Title: "bug report"},
	}

	if err := h.HandleIssueLabel(context.Background(), event); err != nil {
		t.Fatalf("HandleIssueLabel error: %v", err)
	}
	if s.tasks[oldTask.ID].Status != model.TaskStatusCancelled {
		t.Errorf("旧任务状态 = %q, want %q", s.tasks[oldTask.ID].Status, model.TaskStatusCancelled)
	}
	if len(canceller.deleteCalls) != 1 || canceller.deleteCalls[0] != "asynq-webhook-old-analyze" {
		t.Errorf("deleteCalls = %v, want [asynq-webhook-old-analyze]", canceller.deleteCalls)
	}
}

// TestHandleIssueLabel_FixToPRAdded M3.4: fix-to-pr 标签触发 TaskTypeFixIssue。
func TestHandleIssueLabel_FixToPRAdded(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-fix-to-pr"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.IssueLabelEvent{
		DeliveryID:   "delivery-fix-to-pr-1",
		FixToPRAdded: true,
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		Issue:        webhook.IssueRef{Number: 20, Title: "feature request"},
	}

	if err := h.HandleIssueLabel(context.Background(), event); err != nil {
		t.Fatalf("HandleIssueLabel error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(s.tasks))
	}
	for _, record := range s.tasks {
		if record.TaskType != model.TaskTypeFixIssue {
			t.Errorf("task type = %q, want %q", record.TaskType, model.TaskTypeFixIssue)
		}
		if record.Status != model.TaskStatusQueued {
			t.Errorf("task status = %q, want %q", record.Status, model.TaskStatusQueued)
		}
	}
}

// TestHandleIssueLabel_AutoFixAdded_RoutesToAnalyze M3.4: auto-fix 标签触发 TaskTypeAnalyzeIssue。
func TestHandleIssueLabel_AutoFixAdded_RoutesToAnalyze(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-analyze"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.IssueLabelEvent{
		DeliveryID:   "delivery-auto-fix-analyze-1",
		AutoFixAdded: true,
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		Issue:        webhook.IssueRef{Number: 30, Title: "bug"},
	}

	if err := h.HandleIssueLabel(context.Background(), event); err != nil {
		t.Fatalf("HandleIssueLabel error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(s.tasks))
	}
	for _, record := range s.tasks {
		if record.TaskType != model.TaskTypeAnalyzeIssue {
			t.Errorf("task type = %q, want %q (M3.4: auto-fix 应路由到 analyze_issue)", record.TaskType, model.TaskTypeAnalyzeIssue)
		}
	}
}

// TestHandleIssueLabel_BothLabels_FixToPRPriority M3.4: 同时存在 fix-to-pr + auto-fix 时，
// fix-to-pr 优先，仅入队 TaskTypeFixIssue。
func TestHandleIssueLabel_BothLabels_FixToPRPriority(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-both-labels"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.IssueLabelEvent{
		DeliveryID:   "delivery-both-labels-1",
		FixToPRAdded: true,
		AutoFixAdded: true,
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		Issue:        webhook.IssueRef{Number: 40, Title: "critical bug"},
	}

	if err := h.HandleIssueLabel(context.Background(), event); err != nil {
		t.Fatalf("HandleIssueLabel error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(s.tasks))
	}
	for _, record := range s.tasks {
		if record.TaskType != model.TaskTypeFixIssue {
			t.Errorf("task type = %q, want %q (fix-to-pr 优先级高于 auto-fix)", record.TaskType, model.TaskTypeFixIssue)
		}
	}
}

// TestHandleIssueLabel_FixToPR_CancelsSupersededAnalyzeTask 验证 fix-to-pr 升级为修复时，
// 旧的 analyze_issue 活跃任务也会被取消，避免分析与修复并发执行。
func TestHandleIssueLabel_FixToPR_CancelsSupersededAnalyzeTask(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-fix-replacement"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	oldAnalyze := &model.TaskRecord{
		ID:           "old-analyze-for-fix",
		AsynqID:      "asynq-old-analyze-for-fix",
		TaskType:     model.TaskTypeAnalyzeIssue,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityNormal,
		RepoFullName: "org/repo",
		Payload:      model.TaskPayload{IssueNumber: 41},
	}
	s.tasks[oldAnalyze.ID] = oldAnalyze

	event := webhook.IssueLabelEvent{
		DeliveryID:   "delivery-fix-replaces-analyze-1",
		FixToPRAdded: true,
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo", CloneURL: "https://gitea.example.com/org/repo.git"},
		Issue:        webhook.IssueRef{Number: 41, Title: "critical bug"},
	}

	if err := h.HandleIssueLabel(context.Background(), event); err != nil {
		t.Fatalf("HandleIssueLabel error: %v", err)
	}
	if s.tasks[oldAnalyze.ID].Status != model.TaskStatusCancelled {
		t.Errorf("旧 analyze_issue 状态 = %q, want %q", s.tasks[oldAnalyze.ID].Status, model.TaskStatusCancelled)
	}
	if len(canceller.deleteCalls) != 1 || canceller.deleteCalls[0] != "asynq-old-analyze-for-fix" {
		t.Errorf("deleteCalls = %v, want [asynq-old-analyze-for-fix]", canceller.deleteCalls)
	}
}

func TestGenerateManualDeliveryID(t *testing.T) {
	id1 := generateManualDeliveryID()
	id2 := generateManualDeliveryID()

	if !strings.HasPrefix(id1, "manual-") {
		t.Errorf("id1 = %q, want prefix 'manual-'", id1)
	}
	if id1 == id2 {
		t.Errorf("两次生成的 delivery ID 相同: %q", id1)
	}
}

func TestEnqueueManualReview_WithSuperseded(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-manual-new"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	// 预置旧的活跃任务
	oldTask := &model.TaskRecord{
		ID:           "old-manual-task",
		AsynqID:      "asynq-old-manual",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		PRNumber:     42,
		Payload:      model.TaskPayload{HeadSHA: "oldsha"},
	}
	s.tasks[oldTask.ID] = oldTask
	s.activePRTasks = []*model.TaskRecord{oldTask}

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		PRNumber:     42,
		HeadSHA:      "newsha",
	}

	taskID, err := h.EnqueueManualReview(context.Background(), payload, "manual:admin")
	if err != nil {
		t.Fatalf("EnqueueManualReview error: %v", err)
	}
	if taskID == "" {
		t.Fatal("返回的 taskID 不应为空")
	}

	// 旧任务应被取消
	if s.tasks[oldTask.ID].Status != model.TaskStatusCancelled {
		t.Errorf("旧任务状态 = %q, want %q", s.tasks[oldTask.ID].Status, model.TaskStatusCancelled)
	}

	// 新任务的 payload 应包含 superseded 信息
	var newRecord *model.TaskRecord
	for id, r := range s.tasks {
		if id != oldTask.ID {
			newRecord = r
		}
	}
	if newRecord == nil {
		t.Fatal("未找到新建的任务记录")
	}
	if newRecord.Payload.SupersededCount != 1 {
		t.Errorf("SupersededCount = %d, want 1", newRecord.Payload.SupersededCount)
	}
	if newRecord.Payload.PreviousHeadSHA != "oldsha" {
		t.Errorf("PreviousHeadSHA = %q, want %q", newRecord.Payload.PreviousHeadSHA, "oldsha")
	}
}

// TestEnqueueManualGenTests_NoSuperseded 无旧任务时，EnqueueManualGenTests 创建 1 条 queued record。
func TestEnqueueManualGenTests_NoSuperseded(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-gen-tests-1"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		Module:       "backend/api",
	}

	results, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("EnqueueManualGenTests error: %v", err)
	}
	if len(results) != 1 || results[0].TaskID == "" {
		t.Fatalf("期望 1 个结果且 TaskID 非空，实际 results=%+v", results)
	}
	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(s.tasks))
	}

	for _, record := range s.tasks {
		if record.TaskType != model.TaskTypeGenTests {
			t.Errorf("task type = %q, want %q", record.TaskType, model.TaskTypeGenTests)
		}
		// 手动触发使用 PriorityNormal（webhook/CronJob 自动触发应用 PriorityLow）
		if record.Priority != model.PriorityNormal {
			t.Errorf("priority = %v, want %v (手动触发应为 Normal)", record.Priority, model.PriorityNormal)
		}
		if record.Status != model.TaskStatusQueued {
			t.Errorf("task status = %q, want %q", record.Status, model.TaskStatusQueued)
		}
		if record.TriggeredBy != "manual:ci-bot" {
			t.Errorf("triggered_by = %q, want %q", record.TriggeredBy, "manual:ci-bot")
		}
		if !strings.HasPrefix(record.DeliveryID, "manual-") {
			t.Errorf("delivery_id = %q, want prefix 'manual-'", record.DeliveryID)
		}
		if record.Payload.Module != "backend/api" {
			t.Errorf("payload.Module = %q, want %q", record.Payload.Module, "backend/api")
		}
		if record.Payload.TaskType != model.TaskTypeGenTests {
			t.Errorf("payload.TaskType = %q, want %q", record.Payload.TaskType, model.TaskTypeGenTests)
		}
	}
}

// TestEnqueueManualGenTests_CancelAndReplace 两次触发同 (repo, module) → 第二次取消第一条。
func TestEnqueueManualGenTests_CancelAndReplace(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-gen-tests-replace"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		Module:       "backend/api",
	}

	firstResults, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第一次触发失败: %v", err)
	}
	firstID := firstResults[0].TaskID
	// mockEnqueuer 为所有调用返回同一个 asynqID；手动置一个区分值，模拟两次入队的 asynq 作业独立
	s.tasks[firstID].AsynqID = "asynq-gen-tests-first"

	secondResults, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第二次触发失败: %v", err)
	}
	secondID := secondResults[0].TaskID
	if secondID == firstID {
		t.Fatal("两次触发的 taskID 不应相同")
	}

	// 第一条应被取消
	if s.tasks[firstID].Status != model.TaskStatusCancelled {
		t.Errorf("第一条任务状态 = %q, want %q", s.tasks[firstID].Status, model.TaskStatusCancelled)
	}
	if len(canceller.deleteCalls) != 1 || canceller.deleteCalls[0] != "asynq-gen-tests-first" {
		t.Errorf("deleteCalls = %v, want [asynq-gen-tests-first]", canceller.deleteCalls)
	}
	// 第二条应为 queued
	if s.tasks[secondID].Status != model.TaskStatusQueued {
		t.Errorf("第二条任务状态 = %q, want %q", s.tasks[secondID].Status, model.TaskStatusQueued)
	}
}

// TestEnqueueManualGenTests_ModuleKeyCollisionCancelsExistingTask 验证不同 module 字符串若
// 映射到同一 stable branch key，也必须触发 Cancel-and-Replace，避免并发写同一
// auto-test 分支。
func TestEnqueueManualGenTests_ModuleKeyCollisionCancelsExistingTask(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	cleaner := &recordingBranchCleaner{}
	mc := &mockEnqueuer{enqueuedID: "asynq-gen-tests-collision"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default(), WithBranchCleaner(cleaner))

	firstPayload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		Module:       "services/api",
	}
	secondPayload := firstPayload
	secondPayload.Module = "services-api"

	firstResults, err := h.EnqueueManualGenTests(context.Background(), firstPayload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第一次触发失败: %v", err)
	}
	firstID := firstResults[0].TaskID
	s.tasks[firstID].AsynqID = "asynq-gen-tests-first-collision"

	secondResults, err := h.EnqueueManualGenTests(context.Background(), secondPayload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第二次触发失败: %v", err)
	}
	secondID := secondResults[0].TaskID
	if secondID == firstID {
		t.Fatal("两次触发的 taskID 不应相同")
	}

	if s.tasks[firstID].Status != model.TaskStatusCancelled {
		t.Errorf("第一条任务状态 = %q, want %q", s.tasks[firstID].Status, model.TaskStatusCancelled)
	}
	if s.tasks[secondID].Status != model.TaskStatusQueued {
		t.Errorf("第二条任务状态 = %q, want %q", s.tasks[secondID].Status, model.TaskStatusQueued)
	}
	if len(canceller.deleteCalls) != 1 || canceller.deleteCalls[0] != "asynq-gen-tests-first-collision" {
		t.Errorf("deleteCalls = %v, want [asynq-gen-tests-first-collision]", canceller.deleteCalls)
	}
	if len(cleaner.calls) != 1 || cleaner.calls[0] != "auto-test/services-api" {
		t.Errorf("cleaner.calls = %v, want [auto-test/services-api]", cleaner.calls)
	}
}

// TestEnqueueManualGenTests_DifferentModulesNotCancelled 不同 module 并发放行。
func TestEnqueueManualGenTests_DifferentModulesNotCancelled(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-gen-tests-diff"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	base := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
	}
	backend := base
	backend.Module = "backend"
	frontend := base
	frontend.Module = "frontend"

	firstResults, err := h.EnqueueManualGenTests(context.Background(), backend, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第一次触发失败: %v", err)
	}
	firstID := firstResults[0].TaskID
	s.tasks[firstID].AsynqID = "asynq-gen-tests-backend"

	secondResults, err := h.EnqueueManualGenTests(context.Background(), frontend, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第二次触发失败: %v", err)
	}
	secondID := secondResults[0].TaskID

	// 两条都应是 queued，不互相取消
	if s.tasks[firstID].Status != model.TaskStatusQueued {
		t.Errorf("第一条（backend）状态 = %q, want queued", s.tasks[firstID].Status)
	}
	if s.tasks[secondID].Status != model.TaskStatusQueued {
		t.Errorf("第二条（frontend）状态 = %q, want queued", s.tasks[secondID].Status)
	}
	if len(canceller.deleteCalls) != 0 {
		t.Errorf("deleteCalls = %v, want empty (不同 module 不应互相取消)", canceller.deleteCalls)
	}
}

// TestEnqueueManualGenTests_EmptyModuleCancelAndReplace 两次触发相同 repo 但都不带 module（整仓生成）→
// 第二次取消第一条（验证 COALESCE 对空 module 生效）。
func TestEnqueueManualGenTests_EmptyModuleCancelAndReplace(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-gen-tests-empty"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		// 故意不设置 Module，模拟整仓生成
	}

	firstResults, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第一次触发失败: %v", err)
	}
	firstID := firstResults[0].TaskID
	s.tasks[firstID].AsynqID = "asynq-gen-tests-empty-first"

	secondResults, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第二次触发失败: %v", err)
	}
	secondID := secondResults[0].TaskID

	if s.tasks[firstID].Status != model.TaskStatusCancelled {
		t.Errorf("第一条任务状态 = %q, want %q (整仓 Cancel-and-Replace 必须生效)",
			s.tasks[firstID].Status, model.TaskStatusCancelled)
	}
	if len(canceller.deleteCalls) != 1 || canceller.deleteCalls[0] != "asynq-gen-tests-empty-first" {
		t.Errorf("deleteCalls = %v, want [asynq-gen-tests-empty-first]", canceller.deleteCalls)
	}
	if s.tasks[secondID].Status != model.TaskStatusQueued {
		t.Errorf("第二条任务状态 = %q, want %q", s.tasks[secondID].Status, model.TaskStatusQueued)
	}
}

// TestEnqueueManualGenTests_RetryingTaskAlsoReplaced 验证处于 retrying backoff 窗口的旧任务
// 仍被视作活跃任务，手动重触发时应执行 Cancel-and-Replace。
func TestEnqueueManualGenTests_RetryingTaskAlsoReplaced(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-gen-tests-retrying"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default())

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		Module:       "backend",
	}

	firstResults, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第一次触发失败: %v", err)
	}
	firstID := firstResults[0].TaskID
	s.tasks[firstID].AsynqID = "asynq-gen-tests-retrying-first"
	s.tasks[firstID].Status = model.TaskStatusRetrying

	secondResults, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第二次触发失败: %v", err)
	}
	secondID := secondResults[0].TaskID

	if s.tasks[firstID].Status != model.TaskStatusCancelled {
		t.Errorf("retrying 旧任务状态 = %q, want %q", s.tasks[firstID].Status, model.TaskStatusCancelled)
	}
	if len(canceller.deleteCalls) != 0 {
		t.Errorf("retrying 任务不应走 Delete，实际 deleteCalls = %v", canceller.deleteCalls)
	}
	if len(canceller.cancelCalls) != 0 {
		t.Errorf("retrying 任务不应走 CancelProcessing，实际 cancelCalls = %v", canceller.cancelCalls)
	}
	if s.tasks[secondID].Status != model.TaskStatusQueued {
		t.Errorf("新任务状态 = %q, want %q", s.tasks[secondID].Status, model.TaskStatusQueued)
	}
}

// TestListActiveGenTestsTasks_FiltersByTaskType 过滤 task_type='gen_tests'，
// 不误伤 review_pr / fix_issue 的同 repo 任务。
func TestListActiveGenTestsTasks_FiltersByTaskType(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-filter"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	// 预置同 repo 下不同类型的活跃任务，均不应被当作 gen_tests 取消
	reviewTask := &model.TaskRecord{
		ID:           "review-task",
		AsynqID:      "asynq-review",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusRunning,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		Payload:      model.TaskPayload{Module: "backend"},
	}
	fixTask := &model.TaskRecord{
		ID:           "fix-task",
		AsynqID:      "asynq-fix",
		TaskType:     model.TaskTypeFixIssue,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityNormal,
		RepoFullName: "org/repo",
		Payload:      model.TaskPayload{Module: "backend"},
	}
	s.tasks[reviewTask.ID] = reviewTask
	s.tasks[fixTask.ID] = fixTask

	tasks := h.listActiveGenTestsTasks(context.Background(), "org/repo", "backend")
	if len(tasks) != 0 {
		t.Errorf("期望 0 条 gen_tests 活跃任务（仅有 review/fix 任务），得到 %d", len(tasks))
	}
}

// TestEnqueueManualGenTests_TriggeredByFormat TriggeredBy 正确注入 "manual:ci-bot"。
func TestEnqueueManualGenTests_TriggeredByFormat(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-gen-tests-trig"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		Module:       "svc",
	}

	results, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("EnqueueManualGenTests error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 个结果，实际 %d", len(results))
	}

	got := s.tasks[results[0].TaskID]
	if got == nil {
		t.Fatal("任务记录应存在")
	}
	if got.TriggeredBy != "manual:ci-bot" {
		t.Errorf("TriggeredBy = %q, want %q", got.TriggeredBy, "manual:ci-bot")
	}
}

// TestEnqueueManualGenTests_IncompletePayload payload 缺失 RepoFullName / CloneURL 时拒绝入队。
// 与 HandlePullRequest / HandleIssueLabel 对齐的完整性校验：装配层未能填充 Gitea 元数据即视为编程错误，
// 拒绝入队可避免 Worker 阶段因 entrypoint.sh 依赖 CloneURL 失败而留下"pending → failed"的脏记录。
func TestEnqueueManualGenTests_IncompletePayload(t *testing.T) {
	cases := []struct {
		name    string
		payload model.TaskPayload
	}{
		{
			name: "missing RepoFullName",
			payload: model.TaskPayload{
				RepoOwner: "org",
				RepoName:  "repo",
				CloneURL:  "https://gitea.example.com/org/repo.git",
				Module:    "svc",
			},
		},
		{
			name: "missing CloneURL",
			payload: model.TaskPayload{
				RepoOwner:    "org",
				RepoName:     "repo",
				RepoFullName: "org/repo",
				Module:       "svc",
			},
		},
		{
			name:    "both empty",
			payload: model.TaskPayload{RepoOwner: "org", RepoName: "repo"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newMockStore()
			mc := &mockEnqueuer{enqueuedID: "asynq-should-not-enqueue"}
			h := NewEnqueueHandler(mc, nil, s, slog.Default())

			results, err := h.EnqueueManualGenTests(context.Background(), tc.payload, "manual:ci-bot")
			if err == nil {
				t.Fatalf("期望 error，实际 results=%+v", results)
			}
			if !strings.Contains(err.Error(), "RepoFullName") && !strings.Contains(err.Error(), "CloneURL") {
				t.Errorf("error 消息应提及 RepoFullName 或 CloneURL：%v", err)
			}
			if results != nil {
				t.Errorf("拒绝入队时 results 应为 nil，得 %+v", results)
			}
			if len(s.tasks) != 0 {
				t.Errorf("不应落库任何 task，实际有 %d 条", len(s.tasks))
			}
		})
	}
}

// ==========================================================================
// M4.2: BranchCleaner + HandlePullRequest auto-test/* 拦截测试
// ==========================================================================

// recordingBranchCleaner 满足 BranchCleaner 接口，记录每次调用及 branch。
type recordingBranchCleaner struct {
	calls     []string // 记录 branch 名
	returnErr error
}

func (r *recordingBranchCleaner) CleanupAutoTestBranch(_ context.Context, _, _, branch string) error {
	r.calls = append(r.calls, branch)
	return r.returnErr
}

// TestEnqueueManualGenTests_TriggersBranchCleanerOnReplace：Cancel-and-Replace 时
// cleaner 应被调用一次，branch = "auto-test/" + ModuleKey(payload.Module)。
func TestEnqueueManualGenTests_TriggersBranchCleanerOnReplace(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	cleaner := &recordingBranchCleaner{}
	mc := &mockEnqueuer{enqueuedID: "asynq-replace"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default(), WithBranchCleaner(cleaner))

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		Module:       "svc/user",
	}

	firstResults, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第一次触发失败: %v", err)
	}
	s.tasks[firstResults[0].TaskID].AsynqID = "asynq-first"

	if _, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot"); err != nil {
		t.Fatalf("第二次触发失败: %v", err)
	}

	if len(cleaner.calls) != 1 {
		t.Fatalf("BranchCleaner 应被调用 1 次，实际 %d 次", len(cleaner.calls))
	}
	// ModuleKey("svc/user") = "svc-user" → branch = "auto-test/svc-user"
	if cleaner.calls[0] != "auto-test/svc-user" {
		t.Errorf("cleaner 收到的 branch = %q, want %q", cleaner.calls[0], "auto-test/svc-user")
	}
}

// TestEnqueueManualGenTests_NoCleanupWhenNoActiveTasks：首次触发无旧任务，
// 不应触发 cleaner。
func TestEnqueueManualGenTests_NoCleanupWhenNoActiveTasks(t *testing.T) {
	s := newMockStore()
	cleaner := &recordingBranchCleaner{}
	mc := &mockEnqueuer{enqueuedID: "asynq-first-only"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(), WithBranchCleaner(cleaner))

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		Module:       "svc/user",
	}

	if _, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot"); err != nil {
		t.Fatalf("EnqueueManualGenTests error: %v", err)
	}
	if len(cleaner.calls) != 0 {
		t.Errorf("无活跃旧任务时不应触发 cleaner，实际调用 %d 次", len(cleaner.calls))
	}
}

// TestEnqueueManualGenTests_CleanupFailureDoesNotBlock：cleaner 返回 error 时
// 入队仍成功，不向上冒泡。
func TestEnqueueManualGenTests_CleanupFailureDoesNotBlock(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	cleaner := &recordingBranchCleaner{returnErr: errors.New("cleanup failed")}
	mc := &mockEnqueuer{enqueuedID: "asynq-cleanup-err"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default(), WithBranchCleaner(cleaner))

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		Module:       "svc/user",
	}

	firstResults, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第一次触发失败: %v", err)
	}
	s.tasks[firstResults[0].TaskID].AsynqID = "asynq-first"

	secondResults, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("cleanup 失败不应阻断入队: %v", err)
	}
	if len(secondResults) == 0 || secondResults[0].TaskID == "" {
		t.Fatal("第二次入队应成功并返回 taskID")
	}
	secondID := secondResults[0].TaskID
	if s.tasks[secondID].Status != model.TaskStatusQueued {
		t.Errorf("新任务 status = %q, want queued", s.tasks[secondID].Status)
	}
}

// TestEnqueueManualGenTests_EmptyModuleBranchKey：整仓模式下 branch = auto-test/all。
func TestEnqueueManualGenTests_EmptyModuleBranchKey(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	cleaner := &recordingBranchCleaner{}
	mc := &mockEnqueuer{enqueuedID: "asynq-empty-mod"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default(), WithBranchCleaner(cleaner))

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
	}

	firstResults, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("第一次触发失败: %v", err)
	}
	s.tasks[firstResults[0].TaskID].AsynqID = "asynq-first"

	if _, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot"); err != nil {
		t.Fatalf("第二次触发失败: %v", err)
	}

	if len(cleaner.calls) != 1 || cleaner.calls[0] != "auto-test/all" {
		t.Errorf("空 module 应生成 auto-test/all，实际 calls=%v", cleaner.calls)
	}
}

// TestHandlePullRequest_AutoTestInterceptWhenActive：auto-test/* 前缀 + 存在
// 同 module 活跃 gen_tests → 返回 nil，不入队评审任务。
func TestHandlePullRequest_AutoTestInterceptWhenActive(t *testing.T) {
	s := newMockStore()
	s.activeGenTestsModules = []string{"svc/user"} // ModuleKey("svc/user")="svc-user"
	mc := &mockEnqueuer{enqueuedID: "asynq-should-not-enqueue"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		Action:     "opened",
		DeliveryID: "delivery-autotest-intercept",
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{
			Number:  99,
			HeadRef: "auto-test/svc-user",
		},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest 拦截路径不应返回错误: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("拦截路径不应落库任何 task，实际 %d 条", len(s.tasks))
	}
}

// TestHandlePullRequest_AutoTestDifferentModule：auto-test/* 但 module 不活跃 → 正常入队。
func TestHandlePullRequest_AutoTestDifferentModule(t *testing.T) {
	s := newMockStore()
	s.activeGenTestsModules = []string{"svc/other"} // moduleKey 不匹配 "svc-user"
	mc := &mockEnqueuer{enqueuedID: "asynq-diff-module"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		Action:     "opened",
		DeliveryID: "delivery-autotest-diff",
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{
			Number:  77,
			HeadRef: "auto-test/svc-user",
		},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Errorf("期望入队 1 条评审任务，实际 %d", len(s.tasks))
	}
}

// TestHandlePullRequest_AutoTestNoActiveTasks：无活跃 gen_tests → 正常入队。
func TestHandlePullRequest_AutoTestNoActiveTasks(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-no-active"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		Action:     "opened",
		DeliveryID: "delivery-autotest-inactive",
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{
			Number:  55,
			HeadRef: "auto-test/svc-user",
		},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Errorf("期望入队 1 条评审任务，实际 %d", len(s.tasks))
	}
}

// TestHandlePullRequest_AutoTestStoreFailOpen：ListActiveGenTestsModules 失败时
// fail-open，评审任务仍应入队。
func TestHandlePullRequest_AutoTestStoreFailOpen(t *testing.T) {
	s := newMockStore()
	s.activeGenTestsModulesErr = errors.New("sqlite down")
	mc := &mockEnqueuer{enqueuedID: "asynq-failopen"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		Action:     "opened",
		DeliveryID: "delivery-failopen",
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{
			Number:  88,
			HeadRef: "auto-test/svc-user",
		},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("fail-open 路径不应返回错误: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Errorf("fail-open 时评审应正常入队，实际 %d 条", len(s.tasks))
	}
}

// TestHandlePullRequest_NonAutoTestPrefixUnaffected：非 auto-test/* 分支完全不受影响，
// 即便 ListActiveGenTestsModules 返回活跃 module 也不该被误拦。
func TestHandlePullRequest_NonAutoTestPrefixUnaffected(t *testing.T) {
	s := newMockStore()
	s.activeGenTestsModules = []string{"svc/user"}
	mc := &mockEnqueuer{enqueuedID: "asynq-regular"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		Action:     "opened",
		DeliveryID: "delivery-regular",
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{
			Number:  44,
			HeadRef: "feature/svc-user",
		},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Errorf("非 auto-test/* 应正常入队，实际 %d 条", len(s.tasks))
	}
}

// ==========================================================================
// M4.2.1: mockGenTestsPRClient + cleanupAllAutoTestBranches / shouldSkipAutoTestReview 测试
// ==========================================================================

// mockGenTestsPRClient 满足 genTestsPRClient 接口。
type mockGenTestsPRClient struct {
	prs       []*gitea.PullRequest
	listErr   error
	closeErr  error
	closedPRs []int64
}

func (m *mockGenTestsPRClient) ListRepoPullRequests(_ context.Context, _, _ string,
	_ gitea.ListPullRequestsOptions) ([]*gitea.PullRequest, *gitea.Response, error) {
	if m.listErr != nil {
		return nil, nil, m.listErr
	}
	return m.prs, nil, nil
}

func (m *mockGenTestsPRClient) ClosePullRequest(_ context.Context, _, _ string, index int64) error {
	m.closedPRs = append(m.closedPRs, index)
	return m.closeErr
}

// TestShouldSkipAutoTestReview_FrameworkSuffixedBranch auto-test/all-junit5 分支
// 应被拦截（"all-junit5" 以 "all-" 为前缀，匹配活跃 module ""→ModuleKey="all"）。
func TestShouldSkipAutoTestReview_FrameworkSuffixedBranch(t *testing.T) {
	s := newMockStore()
	s.activeGenTestsModules = []string{""} // 空 module = 整仓，ModuleKey="" → "all"
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		DeliveryID: "delivery-fw-suffix",
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{
			Number:  100,
			HeadRef: "auto-test/all-junit5",
		},
	}

	if !h.shouldSkipAutoTestReview(context.Background(), event) {
		t.Error("auto-test/all-junit5 应被拦截（存在活跃整仓 gen_tests 任务）")
	}
}

// TestShouldSkipAutoTestReview_FrameworkSuffixedBranch_NoMatch auto-test/all-junit5
// 不应被活跃 module="backend" 拦截（"all-junit5" 不以 "backend-" 为前缀）。
func TestShouldSkipAutoTestReview_FrameworkSuffixedBranch_NoMatch(t *testing.T) {
	s := newMockStore()
	s.activeGenTestsModules = []string{"backend"} // ModuleKey="backend"
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		DeliveryID: "delivery-fw-nomatch",
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{
			Number:  101,
			HeadRef: "auto-test/all-junit5",
		},
	}

	if h.shouldSkipAutoTestReview(context.Background(), event) {
		t.Error("auto-test/all-junit5 不应被 module=backend 拦截")
	}
}

// TestCleanupAllAutoTestBranches_NilPRClient prClient 为 nil 时不 panic。
func TestCleanupAllAutoTestBranches_NilPRClient(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())
	// prClient 默认 nil，不应 panic
	h.cleanupAllAutoTestBranches(context.Background(), "org", "repo", "org/repo")
}

// TestCleanupAllAutoTestBranches_CancelsAndClosesPRs 验证全量清理的完整路径。
func TestCleanupAllAutoTestBranches_CancelsAndClosesPRs(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	cleaner := &recordingBranchCleaner{}
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}

	// 预置活跃任务
	oldTask := &model.TaskRecord{
		ID:           "old-cleanup-task",
		AsynqID:      "asynq-old-cleanup",
		TaskType:     model.TaskTypeGenTests,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityNormal,
		RepoFullName: "org/repo",
		Payload:      model.TaskPayload{Module: "backend"},
	}
	s.tasks[oldTask.ID] = oldTask

	// mock PR client
	mockPR := &mockGenTestsPRClient{
		prs: []*gitea.PullRequest{
			{
				Number:  10,
				Head:    &gitea.PRBranch{Ref: "auto-test/backend"},
				HTMLURL: "https://gitea.example.com/org/repo/pulls/10",
			},
			{
				Number:  11,
				Head:    &gitea.PRBranch{Ref: "feature/unrelated"},
				HTMLURL: "https://gitea.example.com/org/repo/pulls/11",
			},
		},
	}

	h := NewEnqueueHandler(mc, canceller, s, slog.Default(),
		WithBranchCleaner(cleaner),
		WithPRClient(mockPR),
	)

	h.cleanupAllAutoTestBranches(context.Background(), "org", "repo", "org/repo")

	// 旧任务应被取消
	if s.tasks[oldTask.ID].Status != model.TaskStatusCancelled {
		t.Errorf("旧任务状态 = %q, want cancelled", s.tasks[oldTask.ID].Status)
	}
	// 只有 auto-test/* 的 PR 被关闭
	if len(mockPR.closedPRs) != 1 || mockPR.closedPRs[0] != 10 {
		t.Errorf("closedPRs = %v, want [10]", mockPR.closedPRs)
	}
	// cleaner 应被调用（auto-test/backend）
	if len(cleaner.calls) != 1 || cleaner.calls[0] != "auto-test/backend" {
		t.Errorf("cleaner.calls = %v, want [auto-test/backend]", cleaner.calls)
	}
}

// ==========================================================================
// M4.2.1: mockModuleScanner + 整仓拆分测试
// ==========================================================================

// mockModuleScanner 满足 test.RepoFileChecker 接口，用于 ScanRepoModules 集成测试。
type mockModuleScanner struct {
	files  map[string]bool     // key: "module/relPath" 或根级 "relPath"
	dirs   map[string][]string // key: dir → 子目录列表
	err    error               // 全局错误（模拟 scan 失败）
	called bool
}

func (m *mockModuleScanner) HasFile(_ context.Context, _, _, _, module, relPath string) (bool, error) {
	m.called = true
	if m.err != nil {
		return false, m.err
	}
	key := module + "/" + relPath
	if module == "" {
		key = relPath
	}
	return m.files[key], nil
}

func (m *mockModuleScanner) ListDir(_ context.Context, _, _, _, dir string) ([]string, error) {
	m.called = true
	if m.err != nil {
		return nil, m.err
	}
	return m.dirs[dir], nil
}

// TestEnqueueManualGenTests_SplitMultiModule 整仓模式下扫描到 2 个模块，拆分为 2 个子任务。
func TestEnqueueManualGenTests_SplitMultiModule(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-split"}
	scanner := &mockModuleScanner{
		files: map[string]bool{
			"backend/pom.xml":       true,
			"frontend/package.json": true,
		},
		dirs: map[string][]string{"": {"backend", "frontend"}},
	}
	mockPR := &mockGenTestsPRClient{}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithModuleScanner(scanner),
		WithPRClient(mockPR),
	)

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		BaseRef:      "main",
	}

	results, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("期望 2 个子任务，实际 %d", len(results))
	}
	if results[0].Module != "backend" || results[0].Framework != string(test.FrameworkJUnit5) {
		t.Errorf("第一个子任务不符合预期: %+v", results[0])
	}
	if results[1].Module != "frontend" || results[1].Framework != string(test.FrameworkVitest) {
		t.Errorf("第二个子任务不符合预期: %+v", results[1])
	}
}

// TestEnqueueManualGenTests_ScanFail_FallbackSingle 扫描失败时回退为单任务入队。
func TestEnqueueManualGenTests_ScanFail_FallbackSingle(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-fallback"}
	scanner := &mockModuleScanner{err: fmt.Errorf("scan failed")}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithModuleScanner(scanner),
	)

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		BaseRef:      "main",
	}

	results, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("scan 失败应回退单任务，实际 error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 个任务（回退），实际 %d", len(results))
	}
}

// TestEnqueueManualGenTests_ModuleNonEmpty_SkipScan module 非空时不触发扫描。
func TestEnqueueManualGenTests_ModuleNonEmpty_SkipScan(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-noscan"}
	scanner := &mockModuleScanner{
		files: map[string]bool{
			"backend/pom.xml": true,
		},
		dirs: map[string][]string{"": {"backend"}},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithModuleScanner(scanner),
	)

	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		Module:       "backend",
		BaseRef:      "main",
	}

	results, err := h.EnqueueManualGenTests(context.Background(), payload, "manual:ci-bot")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("module 非空不应触发扫描，期望 1 个任务，实际 %d", len(results))
	}
	if scanner.called {
		t.Error("module 非空时不应调用 scanner")
	}
}

// ==========================================================================
// M4.3: 变更驱动测试生成 — handleMergedPullRequest 测试
// ==========================================================================

// mockConfigProvider 测试用 ChangeDrivenConfigProvider
type mockConfigProvider struct {
	cfg config.TestGenOverride
}

func (m *mockConfigProvider) ResolveTestGenConfig(_ string) config.TestGenOverride {
	return m.cfg
}

// mockPRFilesLister 测试用 PRFilesLister
type mockPRFilesLister struct {
	files      []*gitea.ChangedFile
	pages      map[int][]*gitea.ChangedFile
	nextPages  map[int]int
	err        error
	calledPage []int
}

func (m *mockPRFilesLister) ListPullRequestFiles(_ context.Context, _, _ string, _ int64, opts gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error) {
	m.calledPage = append(m.calledPage, opts.Page)
	if m.pages != nil {
		next := 0
		if m.nextPages != nil {
			next = m.nextPages[opts.Page]
		}
		return m.pages[opts.Page], &gitea.Response{NextPage: next}, m.err
	}
	return m.files, nil, m.err
}

func boolPtr(v bool) *bool { return &v }

func newMergedPREvent(prNumber int64, headRef, baseRef string) webhook.PullRequestEvent {
	return webhook.PullRequestEvent{
		Action:     "merged",
		DeliveryID: fmt.Sprintf("delivery-merged-%d", prNumber),
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{
			Number:  prNumber,
			BaseRef: baseRef,
			HeadRef: headRef,
		},
	}
}

func TestHandleMergedPR_BotPRSkipped(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
	)

	event := newMergedPREvent(1, "auto-test/backend", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("bot PR (auto-test/*) should be skipped, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_BotFixPRSkipped(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
	)

	event := newMergedPREvent(2, "auto-fix/issue-1", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("bot PR (auto-fix/*) should be skipped, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_Disabled(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(false)},
		}}),
	)

	event := newMergedPREvent(3, "feature/foo", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("disabled change-driven should not enqueue, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_TestGenDisabledSkipped(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			Enabled:      boolPtr(false),
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
	)

	event := newMergedPREvent(31, "feature/foo", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("test_gen.enabled=false should not enqueue, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_ConfigProviderNil(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := newMergedPREvent(4, "feature/foo", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("nil configProvider should silently skip, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_PRFilesListerNil(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
	)

	event := newMergedPREvent(5, "feature/foo", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("nil prFilesLister should silently skip, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_NoSourceFiles(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "README.md", Status: "modified"},
			{Filename: "docs/guide.md", Status: "added"},
		}}),
	)

	event := newMergedPREvent(6, "feature/docs-only", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("pure docs PR should not enqueue, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_PRFilesPagination(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-paged-files"}
	scanner := &mockModuleScanner{
		files: map[string]bool{"pom.xml": true},
	}
	lister := &mockPRFilesLister{
		pages: map[int][]*gitea.ChangedFile{
			1: {
				{Filename: "README.md", Status: "modified"},
			},
			2: {
				{Filename: "src/Main.java", Status: "modified"},
			},
		},
		nextPages: map[int]int{1: 2},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(lister),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(61, "feature/paged-files", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task from source file on page 2, got %d", len(s.tasks))
	}
	if fmt.Sprint(lister.calledPage) != "[1 2]" {
		t.Fatalf("called pages = %v, want [1 2]", lister.calledPage)
	}
}

func TestHandleMergedPR_DeletedFilesExcluded(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	scanner := &mockModuleScanner{
		files: map[string]bool{"pom.xml": true},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "src/Main.java", Status: "deleted"},
		}}),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(7, "feature/cleanup", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("deleted-only files should not enqueue, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_NoFramework(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	scanner := &mockModuleScanner{
		dirs: map[string][]string{"": {"src"}},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "src/main.py", Status: "modified"},
		}}),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(8, "feature/python", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("ErrNoFrameworkDetected should silently skip, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_SingleModule(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-merged-single"}
	scanner := &mockModuleScanner{
		files: map[string]bool{"pom.xml": true},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "src/Main.java", Status: "modified"},
			{Filename: "src/Service.java", Status: "added"},
		}}),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(42, "feature/new-api", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(s.tasks))
	}

	for _, record := range s.tasks {
		if record.TaskType != model.TaskTypeGenTests {
			t.Errorf("task type = %q, want %q", record.TaskType, model.TaskTypeGenTests)
		}
		if record.Payload.BaseRef != "main" {
			t.Errorf("BaseRef = %q, want %q", record.Payload.BaseRef, "main")
		}
		if record.Payload.Framework != string(test.FrameworkJUnit5) {
			t.Errorf("Framework = %q, want %q", record.Payload.Framework, test.FrameworkJUnit5)
		}
	}
}

func TestHandleMergedPR_MultiModule(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-merged-multi"}
	scanner := &mockModuleScanner{
		files: map[string]bool{
			"backend/pom.xml":       true,
			"frontend/package.json": true,
		},
		dirs: map[string][]string{"": {"backend", "frontend"}},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "backend/src/Api.java", Status: "modified"},
			{Filename: "frontend/src/App.vue", Status: "modified"},
		}}),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(99, "feature/fullstack", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 2 {
		t.Fatalf("expected 2 tasks for multi-module, got %d", len(s.tasks))
	}

	var modules []string
	for _, record := range s.tasks {
		modules = append(modules, record.Payload.Module)
	}
	hasBackend := false
	hasFrontend := false
	for _, m := range modules {
		if m == "backend" {
			hasBackend = true
		}
		if m == "frontend" {
			hasFrontend = true
		}
	}
	if !hasBackend || !hasFrontend {
		t.Errorf("expected backend+frontend modules, got %v", modules)
	}
}

func TestHandleMergedPR_PartialModuleFailureReturnsError(t *testing.T) {
	s := newMockStore()
	s.createErrAt = 2
	mc := &mockEnqueuer{enqueuedID: "asynq-merged-partial"}
	scanner := &mockModuleScanner{
		files: map[string]bool{
			"backend/pom.xml":       true,
			"frontend/package.json": true,
		},
		dirs: map[string][]string{"": {"backend", "frontend"}},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "backend/src/Api.java", Status: "modified"},
			{Filename: "frontend/src/App.vue", Status: "modified"},
		}}),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(100, "feature/partial", "main")
	err := h.HandlePullRequest(context.Background(), event)
	if err == nil {
		t.Fatal("partial module enqueue failure should return error")
	}
	if !strings.Contains(err.Error(), "部分模块入队失败") {
		t.Fatalf("error = %v, want partial failure message", err)
	}
	if len(s.tasks) != 1 {
		t.Fatalf("expected first module task to remain persisted, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_TriggeredBy(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-merged-trig"}
	scanner := &mockModuleScanner{
		files: map[string]bool{"pom.xml": true},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "src/Main.java", Status: "modified"},
		}}),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(42, "feature/trig-check", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(s.tasks))
	}

	for _, record := range s.tasks {
		if record.TriggeredBy != "webhook:pr_merged:42" {
			t.Errorf("TriggeredBy = %q, want %q", record.TriggeredBy, "webhook:pr_merged:42")
		}
	}
}

func TestHandleMergedPR_ChangedFilesInPayload(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-merged-files"}
	scanner := &mockModuleScanner{
		files: map[string]bool{"pom.xml": true},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "src/Main.java", Status: "modified"},
			{Filename: "src/Service.java", Status: "added"},
			{Filename: "README.md", Status: "modified"},
		}}),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(50, "feature/payload-check", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(s.tasks))
	}

	for _, record := range s.tasks {
		if len(record.Payload.ChangedFiles) != 2 {
			t.Errorf("ChangedFiles count = %d, want 2 (README.md filtered)", len(record.Payload.ChangedFiles))
		}
		fileSet := make(map[string]bool)
		for _, f := range record.Payload.ChangedFiles {
			fileSet[f] = true
		}
		if !fileSet["src/Main.java"] || !fileSet["src/Service.java"] {
			t.Errorf("ChangedFiles = %v, want src/Main.java + src/Service.java", record.Payload.ChangedFiles)
		}
	}
}

func TestHandleMergedPR_IdempotentByDelivery(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-merged-idem"}
	scanner := &mockModuleScanner{
		files: map[string]bool{"pom.xml": true},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "src/Main.java", Status: "modified"},
		}}),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(51, "feature/replay", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("first HandlePullRequest error: %v", err)
	}
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("second HandlePullRequest error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Fatalf("same delivery should enqueue once, got %d tasks", len(s.tasks))
	}
	for _, record := range s.tasks {
		wantDelivery := buildChangeDrivenDeliveryID(event.DeliveryID, event.PullRequest.Number, "", string(test.FrameworkJUnit5))
		if record.DeliveryID != wantDelivery {
			t.Errorf("DeliveryID = %q, want %q", record.DeliveryID, wantDelivery)
		}
	}
}

func TestHandleMergedPR_AllModulesFailedReturnsError(t *testing.T) {
	s := newMockStore()
	s.createErr = errors.New("sqlite unavailable")
	mc := &mockEnqueuer{enqueuedID: "asynq-merged-fail"}
	scanner := &mockModuleScanner{
		files: map[string]bool{"pom.xml": true},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "src/Main.java", Status: "modified"},
		}}),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(52, "feature/fail", "main")
	err := h.HandlePullRequest(context.Background(), event)
	if err == nil {
		t.Fatal("all module enqueue failures should return error")
	}
	if !strings.Contains(err.Error(), "所有模块入队均失败") {
		t.Fatalf("error = %v, want all-modules-failed message", err)
	}
}

func TestHandleMergedPR_DefaultAction_IgnoresUnknown(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default())

	event := webhook.PullRequestEvent{
		Action:     "closed",
		DeliveryID: "delivery-closed-1",
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{Number: 1},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unknown action should return nil, got: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("unknown action should not enqueue, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_ModuleScannerNil(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "src/Main.java", Status: "modified"},
		}}),
	)

	event := newMergedPREvent(60, "feature/no-scanner", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("nil moduleScanner should silently skip, got %d tasks", len(s.tasks))
	}
}

func TestHandleMergedPR_NoModulesMatched(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	scanner := &mockModuleScanner{
		files: map[string]bool{"backend/pom.xml": true},
		dirs:  map[string][]string{"": {"backend"}},
	}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "scripts/deploy.sh", Status: "modified"},
		}}),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(70, "feature/scripts", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("no matching modules should not enqueue, got %d tasks", len(s.tasks))
	}
}

// I4: ListPullRequestFiles 返回错误时应传播给调用方（触发任务重试）。
func TestHandleMergedPR_ListPRFilesError(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-x"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(&mockConfigProvider{cfg: config.TestGenOverride{
			ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
		}}),
		WithPRFilesLister(&mockPRFilesLister{err: errors.New("gitea api timeout")}),
	)

	event := newMergedPREvent(80, "feature/api-err", "main")
	err := h.HandlePullRequest(context.Background(), event)
	if err == nil {
		t.Fatal("ListPullRequestFiles error should propagate")
	}
	if !strings.Contains(err.Error(), "list PR files") {
		t.Errorf("error = %v, want 'list PR files' wrapper", err)
	}
	if len(s.tasks) != 0 {
		t.Errorf("no tasks should be created on ListPRFiles error, got %d", len(s.tasks))
	}
}

// S5: 全局 test_gen.enabled=false 但仓库级覆盖为 true + change_driven.enabled=true 时应正常入队。
func TestHandleMergedPR_RepoOverrideEnabled(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-override"}
	scanner := &mockModuleScanner{
		files: map[string]bool{"pom.xml": true},
	}

	cfg := &config.Config{
		TestGen: config.TestGenOverride{
			Enabled: boolPtr(false),
		},
		Repos: []config.RepoConfig{
			{
				Name: "org/repo",
				TestGen: &config.TestGenOverride{
					Enabled:      boolPtr(true),
					ChangeDriven: &config.ChangeDrivenConfig{Enabled: boolPtr(true)},
				},
			},
		},
	}

	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithConfigProvider(cfg),
		WithPRFilesLister(&mockPRFilesLister{files: []*gitea.ChangedFile{
			{Filename: "src/Main.java", Status: "modified"},
		}}),
		WithModuleScanner(scanner),
	)

	event := newMergedPREvent(90, "feature/repo-override", "main")
	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Fatalf("repo override should enable gen_tests, expected 1 task, got %d", len(s.tasks))
	}
	for _, record := range s.tasks {
		if record.TaskType != model.TaskTypeGenTests {
			t.Errorf("task type = %q, want %q", record.TaskType, model.TaskTypeGenTests)
		}
	}
}
