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

	// 非零退出码不返回 Go error，但状态应为 failed
	_ = p.ProcessTask(context.Background(), task)

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
