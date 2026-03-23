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
	tasks         map[string]*model.TaskRecord
	byDeliveryID  map[string]*model.TaskRecord
	createErr     error
	updateErr     error
	findErr       error
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
	if m.updateErr != nil {
		return m.updateErr
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

func (m *mockStore) Close() error { return nil }

// mockClient 模拟 Client 的 Enqueue 行为
type mockClient struct {
	enqueueErr error
	enqueuedID string
}

func (mc *mockClient) Enqueue(_ context.Context, _ model.TaskPayload, opts EnqueueOptions) (string, error) {
	if mc.enqueueErr != nil {
		return "", mc.enqueueErr
	}
	id := opts.TaskID
	if id == "" {
		id = mc.enqueuedID
	}
	return id, nil
}

// enqueueHandlerWithMockClient 使用 mock client 的 EnqueueHandler（测试辅助）
type enqueueHandlerWithMockClient struct {
	client *mockClient
	store  store.Store
	logger *slog.Logger
}

func (h *enqueueHandlerWithMockClient) HandlePullRequest(ctx context.Context, event webhook.PullRequestEvent) error {
	// 复用 EnqueueHandler 的逻辑，但使用 mockClient
	existing, err := h.store.FindByDeliveryID(ctx, event.DeliveryID, model.TaskTypeReviewPR)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   event.DeliveryID,
		RepoOwner:    event.Repository.Owner,
		RepoName:     event.Repository.Name,
		RepoFullName: event.Repository.FullName,
		CloneURL:     event.Repository.CloneURL,
		PRNumber:     event.PullRequest.Number,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:           "test-id-1",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusPending,
		Priority:     model.PriorityHigh,
		Payload:      payload,
		RepoFullName: event.Repository.FullName,
		DeliveryID:   event.DeliveryID,
		MaxRetry:     TaskMaxRetry(),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.store.CreateTask(ctx, record); err != nil {
		return err
	}

	asynqID, err := h.client.Enqueue(ctx, payload, EnqueueOptions{Priority: model.PriorityHigh, TaskID: record.ID})
	if err != nil {
		return nil // 入队失败保持 pending，由 recovery 处理
	}

	record.AsynqID = asynqID
	record.Status = model.TaskStatusQueued
	record.UpdatedAt = time.Now()
	_ = h.store.UpdateTask(ctx, record)
	return nil
}

func (h *enqueueHandlerWithMockClient) HandleIssueLabel(ctx context.Context, event webhook.IssueLabelEvent) error {
	if !event.AutoFixAdded {
		return nil
	}
	existing, err := h.store.FindByDeliveryID(ctx, event.DeliveryID, model.TaskTypeFixIssue)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   event.DeliveryID,
		RepoOwner:    event.Repository.Owner,
		RepoName:     event.Repository.Name,
		RepoFullName: event.Repository.FullName,
		IssueNumber:  event.Issue.Number,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:           "test-id-2",
		TaskType:     model.TaskTypeFixIssue,
		Status:       model.TaskStatusPending,
		Priority:     model.PriorityNormal,
		Payload:      payload,
		RepoFullName: event.Repository.FullName,
		DeliveryID:   event.DeliveryID,
		MaxRetry:     TaskMaxRetry(),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.store.CreateTask(ctx, record); err != nil {
		return err
	}

	asynqID, err := h.client.Enqueue(ctx, payload, EnqueueOptions{Priority: model.PriorityNormal, TaskID: record.ID})
	if err != nil {
		return nil
	}

	record.AsynqID = asynqID
	record.Status = model.TaskStatusQueued
	record.UpdatedAt = time.Now()
	_ = h.store.UpdateTask(ctx, record)
	return nil
}

func TestHandlePullRequest_CreatesTask(t *testing.T) {
	s := newMockStore()
	mc := &mockClient{enqueuedID: "asynq-123"}
	h := &enqueueHandlerWithMockClient{client: mc, store: s, logger: slog.Default()}

	event := webhook.PullRequestEvent{
		DeliveryID: "delivery-001",
		Repository: webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo"},
		PullRequest: webhook.PullRequestRef{Number: 42},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest error: %v", err)
	}

	// 验证任务已创建且状态为 queued
	record, err := s.GetTask(context.Background(), "test-id-1")
	if err != nil {
		t.Fatalf("GetTask error: %v", err)
	}
	if record.Status != model.TaskStatusQueued {
		t.Errorf("task status = %q, want %q", record.Status, model.TaskStatusQueued)
	}
	if record.AsynqID == "" {
		t.Error("task AsynqID should not be empty")
	}
}

func TestHandlePullRequest_Idempotent(t *testing.T) {
	s := newMockStore()
	mc := &mockClient{enqueuedID: "asynq-456"}
	h := &enqueueHandlerWithMockClient{client: mc, store: s, logger: slog.Default()}

	event := webhook.PullRequestEvent{
		DeliveryID: "delivery-dup",
		Repository: webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo"},
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
	mc := &mockClient{enqueueErr: errors.New("redis unavailable")}
	h := &enqueueHandlerWithMockClient{client: mc, store: s, logger: slog.Default()}

	event := webhook.PullRequestEvent{
		DeliveryID: "delivery-fail",
		Repository: webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo"},
		PullRequest: webhook.PullRequestRef{Number: 99},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest should not return error on enqueue fail: %v", err)
	}

	// 任务应存在，状态仍为 pending（未被更新为 queued）
	record, err := s.GetTask(context.Background(), "test-id-1")
	if err != nil {
		t.Fatalf("GetTask error: %v", err)
	}
	if record.Status != model.TaskStatusPending {
		t.Errorf("task status = %q, want %q", record.Status, model.TaskStatusPending)
	}
}

func TestHandleIssueLabel_OnlyWhenAutoFixAdded(t *testing.T) {
	s := newMockStore()
	mc := &mockClient{enqueuedID: "asynq-789"}
	h := &enqueueHandlerWithMockClient{client: mc, store: s, logger: slog.Default()}

	// AutoFixAdded=false，不应创建任务
	event := webhook.IssueLabelEvent{
		DeliveryID:   "delivery-issue-1",
		AutoFixAdded: false,
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo"},
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
		Repository:   webhook.RepositoryRef{Owner: "org", Name: "repo", FullName: "org/repo"},
		Issue:        webhook.IssueRef{Number: 20},
	}
	if err := h.HandleIssueLabel(context.Background(), event2); err != nil {
		t.Fatalf("HandleIssueLabel error: %v", err)
	}
	if len(s.tasks) != 1 {
		t.Errorf("expected 1 task when AutoFixAdded=true, got %d", len(s.tasks))
	}
}
