package queue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// mockPoolRunner 模拟 PoolRunner 接口
type mockPoolRunner struct {
	result *worker.ExecutionResult
	err    error
}

func (m *mockPoolRunner) Run(_ context.Context, _ model.TaskPayload) (*worker.ExecutionResult, error) {
	return m.result, m.err
}

// buildAsynqTask 构建测试用的 asynq.Task（仅序列化 payload，不依赖 ResultWriter）
func buildAsynqTask(t *testing.T, payload model.TaskPayload) *asynq.Task {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	return asynq.NewTask(AsynqTypeReviewPR, data)
}

// seedRecord 将任务记录预置到 mockStore 的两个索引中
func seedRecord(s *mockStore, record *model.TaskRecord) {
	s.tasks[record.ID] = record
	if record.DeliveryID != "" {
		key := record.DeliveryID + ":" + string(record.TaskType)
		s.byDeliveryID[key] = record
	}
}

func TestProcessTask_Success(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-success-1",
		RepoFullName: "org/repo",
		PRNumber:     1,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-1",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{
		result: &worker.ExecutionResult{
			ExitCode:    0,
			Output:      "review completed",
			ContainerID: "container-abc",
		},
	}

	p := NewProcessor(pool, s, slog.Default())
	task := buildAsynqTask(t, payload)

	if err := p.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}

	got := s.tasks["proc-task-1"]
	if got.Status != model.TaskStatusSucceeded {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
	}
	if got.Result != "review completed" {
		t.Errorf("result = %q, want %q", got.Result, "review completed")
	}
	if got.WorkerID != "container-abc" {
		t.Errorf("worker_id = %q, want %q", got.WorkerID, "container-abc")
	}
	if got.CompletedAt == nil {
		t.Error("completed_at should be set")
	}
}

func TestProcessTask_PoolRunFail(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "dlv-fail-1",
		RepoFullName: "org/repo",
		IssueNumber:  5,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-2",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{
		err: errors.New("docker daemon unavailable"),
	}

	p := NewProcessor(pool, s, slog.Default())
	task := buildAsynqTask(t, payload)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("ProcessTask should return error when pool.Run fails")
	}

	got := s.tasks["proc-task-2"]
	if got.Status != model.TaskStatusFailed {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if got.Error == "" {
		t.Error("error field should be set on failure")
	}
}

func TestProcessTask_NonZeroExitCode(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		DeliveryID:   "dlv-exitcode-1",
		RepoFullName: "org/repo",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-3",
		TaskType:   model.TaskTypeGenTests,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{
		result: &worker.ExecutionResult{
			ExitCode: 1,
			Output:   "some output",
			Error:    "test generation failed",
		},
	}

	p := NewProcessor(pool, s, slog.Default())
	task := buildAsynqTask(t, payload)

	// 非零退出码应返回 error
	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("非零退出码应返回 error")
	}

	got := s.tasks["proc-task-3"]
	if got.Status != model.TaskStatusFailed {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
}

func TestProcessTask_InvalidPayload(t *testing.T) {
	s := newMockStore()
	pool := &mockPoolRunner{}
	p := NewProcessor(pool, s, slog.Default())

	// 构造损坏的 payload
	badTask := asynq.NewTask(AsynqTypeReviewPR, []byte("not-json"))
	err := p.ProcessTask(context.Background(), badTask)
	if err == nil {
		t.Fatal("ProcessTask should return error on invalid payload")
	}
}

func TestShouldRetry(t *testing.T) {
	// shouldRetry 依赖 asynq context 中的 retry_count 和 max_retry，
	// 但 asynq 的 context key 是未导出的，无法在外部注入。
	// 因此这里直接用 context.Background() 测试"无 asynq 上下文"路径，
	// 验证 shouldRetry 在获取不到重试信息时返回 false。
	if shouldRetry(context.Background()) {
		t.Error("shouldRetry 应在无 asynq 上下文时返回 false")
	}
}

func TestProcessTask_RetryingStatus(t *testing.T) {
	// 验证非零退出码（非确定性失败）在无 asynq 重试上下文时标记为 failed
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-retrying-1",
		RepoFullName: "org/repo",
		PRNumber:     10,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-retrying",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{
		err: errors.New("temporary failure"),
	}

	p := NewProcessor(pool, s, slog.Default())
	task := buildAsynqTask(t, payload)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("ProcessTask 应在 pool.Run 失败时返回 error")
	}

	got := s.tasks["proc-task-retrying"]
	// 无 asynq 重试上下文时 shouldRetry 返回 false，应标记为 failed
	if got.Status != model.TaskStatusFailed {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
}

func TestProcessTask_RecordNotFound(t *testing.T) {
	s := newMockStore()
	pool := &mockPoolRunner{}
	p := NewProcessor(pool, s, slog.Default())

	// payload 有 DeliveryID 但 store 中没有对应记录
	payload := model.TaskPayload{
		TaskType:   model.TaskTypeReviewPR,
		DeliveryID: "nonexistent-delivery",
	}
	task := buildAsynqTask(t, payload)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("ProcessTask should return error when record not found")
	}
}
