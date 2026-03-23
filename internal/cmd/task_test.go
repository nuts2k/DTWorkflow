package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

type mockTaskStore struct {
	tasks     map[string]*model.TaskRecord
	updateErr error
}

func newMockStore() *mockTaskStore {
	return &mockTaskStore{tasks: make(map[string]*model.TaskRecord)}
}

func (m *mockTaskStore) CreateTask(_ context.Context, record *model.TaskRecord) error {
	m.tasks[record.ID] = record
	return nil
}

func (m *mockTaskStore) GetTask(_ context.Context, id string) (*model.TaskRecord, error) {
	r, ok := m.tasks[id]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (m *mockTaskStore) UpdateTask(_ context.Context, record *model.TaskRecord) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.tasks[record.ID] = record
	return nil
}

func (m *mockTaskStore) ListTasks(_ context.Context, _ store.ListOptions) ([]*model.TaskRecord, error) { return nil, nil }
func (m *mockTaskStore) FindByDeliveryID(_ context.Context, _ string, _ model.TaskType) (*model.TaskRecord, error) {
	return nil, nil
}
func (m *mockTaskStore) ListOrphanTasks(_ context.Context, _ time.Duration) ([]*model.TaskRecord, error) {
	return nil, nil
}
func (m *mockTaskStore) PurgeTasks(_ context.Context, _ time.Duration, _ model.TaskStatus) (int64, error) {
	return 0, nil
}
func (m *mockTaskStore) Ping(_ context.Context) error { return nil }
func (m *mockTaskStore) Close() error                 { return nil }

type stubTaskEnqueuer struct {
	asynqID     string
	enqueueErr  error
	called      int
	lastPayload model.TaskPayload
	lastOpts    queue.EnqueueOptions
}

func (s *stubTaskEnqueuer) Enqueue(_ context.Context, payload model.TaskPayload, opts queue.EnqueueOptions) (string, error) {
	s.called++
	s.lastPayload = payload
	s.lastOpts = opts
	if s.enqueueErr != nil {
		return "", s.enqueueErr
	}
	return s.asynqID, nil
}

func newTaskRecord(status model.TaskStatus) *model.TaskRecord {
	now := time.Now()
	started := now.Add(-2 * time.Minute)
	completed := now.Add(-1 * time.Minute)
	return &model.TaskRecord{
		ID:         "task-1",
		TaskType:   model.TaskTypeReviewPR,
		Status:     status,
		Priority:   model.PriorityHigh,
		RepoFullName: "org/repo",
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			DeliveryID:   "delivery-1",
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     42,
		},
		RetryCount:  2,
		MaxRetry:    3,
		Error:       "old error",
		WorkerID:    "worker-1",
		DeliveryID:  "delivery-1",
		CreatedAt:   now.Add(-10 * time.Minute),
		UpdatedAt:   now.Add(-5 * time.Minute),
		StartedAt:   &started,
		CompletedAt: &completed,
	}
}

func TestTaskCommand_HasRedisAddrFlag(t *testing.T) {
	flag := taskCmd.PersistentFlags().Lookup("redis-addr")
	if flag == nil {
		t.Fatal("task command should define --redis-addr flag")
	}
}

func TestRetryTask_FailedTask_EnqueuesImmediately(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{asynqID: "asynq-123"}

	updated, message, err := retryTask(context.Background(), s, q, record.ID)
	if err != nil {
		t.Fatalf("retryTask error: %v", err)
	}
	if message == "" {
		t.Fatal("retryTask should return non-empty message")
	}
	if updated.Status != model.TaskStatusQueued {
		t.Fatalf("status = %q, want %q", updated.Status, model.TaskStatusQueued)
	}
	if updated.AsynqID != "asynq-123" {
		t.Fatalf("asynq id = %q, want %q", updated.AsynqID, "asynq-123")
	}
	if q.called != 1 {
		t.Fatalf("enqueue called %d times, want 1", q.called)
	}
	if q.lastPayload.DeliveryID != record.Payload.DeliveryID {
		t.Fatalf("payload delivery_id = %q, want %q", q.lastPayload.DeliveryID, record.Payload.DeliveryID)
	}
}

func TestRetryTask_CancelledTask_EnqueuesImmediately(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusCancelled)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{asynqID: "asynq-456"}

	updated, _, err := retryTask(context.Background(), s, q, record.ID)
	if err != nil {
		t.Fatalf("retryTask error: %v", err)
	}
	if updated.Status != model.TaskStatusQueued {
		t.Fatalf("status = %q, want %q", updated.Status, model.TaskStatusQueued)
	}
}

func TestRetryTask_InvalidStatus_ReturnsError(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusRunning)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{}

	_, _, err := retryTask(context.Background(), s, q, record.ID)
	if err == nil {
		t.Fatal("retryTask should return error for invalid status")
	}
}

func TestRetryTask_EnqueueFailure_ReturnsError(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{enqueueErr: errors.New("redis down")}

	_, _, err := retryTask(context.Background(), s, q, record.ID)
	if err == nil {
		t.Fatal("retryTask should return enqueue error")
	}
}

func TestRetryTask_TaskIDConflict_TreatAsQueued(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{enqueueErr: asynq.ErrTaskIDConflict}

	updated, message, err := retryTask(context.Background(), s, q, record.ID)
	if err != nil {
		t.Fatalf("retryTask error: %v", err)
	}
	if updated.Status != model.TaskStatusQueued {
		t.Fatalf("status = %q, want %q", updated.Status, model.TaskStatusQueued)
	}
	expectedID := buildRetryTaskID(record.DeliveryID, record.TaskType)
	if updated.AsynqID != expectedID {
		t.Fatalf("asynq id = %q, want %q", updated.AsynqID, expectedID)
	}
	if message == "" {
		t.Fatal("message should not be empty on task id conflict")
	}
}

func TestRetryTask_ResetExecutionFields(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{asynqID: "asynq-789"}

	updated, _, err := retryTask(context.Background(), s, q, record.ID)
	if err != nil {
		t.Fatalf("retryTask error: %v", err)
	}
	if updated.RetryCount != 0 {
		t.Fatalf("retry_count = %d, want 0", updated.RetryCount)
	}
	if updated.Error != "" {
		t.Fatalf("error = %q, want empty", updated.Error)
	}
	if updated.StartedAt != nil {
		t.Fatal("started_at should be nil")
	}
	if updated.CompletedAt != nil {
		t.Fatal("completed_at should be nil")
	}
	if updated.WorkerID != "" {
		t.Fatalf("worker_id = %q, want empty", updated.WorkerID)
	}
}

func TestTaskRetryCommand_SuccessPrintsQueued(t *testing.T) {
	oldStore := taskStore
	oldQueueClient := taskQueueClient
	oldJSON := jsonOutput
	defer func() {
		taskStore = oldStore
		taskQueueClient = oldQueueClient
		jsonOutput = oldJSON
	}()

	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{asynqID: "asynq-cmd-1"}
	taskStore = s
	taskQueueClient = &queue.Client{}
	jsonOutput = false

	oldRetry := taskRetryCmd.RunE
	defer func() { taskRetryCmd.RunE = oldRetry }()
	taskRetryCmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		rec, message, err := retryTask(ctx, taskStore, q, args[0])
		if err != nil {
			return &ExitCodeError{Code: 1, Err: err}
		}
		PrintResult(map[string]any{
			"id":       args[0],
			"status":   string(rec.Status),
			"asynq_id": rec.AsynqID,
			"message":  message,
		}, func(data any) string {
			return "任务已重新入队\n当前状态: queued\n"
		})
		return nil
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe error: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	taskRetryCmd.SetArgs([]string{record.ID})
	err = taskRetryCmd.RunE(taskRetryCmd, []string{record.ID})
	_ = w.Close()
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("ReadAll error: %v", readErr)
	}
	if !strings.Contains(string(out), "queued") {
		t.Fatalf("output = %q, want contains queued", string(out))
	}
	if err != nil {
		t.Fatalf("taskRetryCmd.RunE error: %v", err)
	}
	if s.tasks[record.ID].Status != model.TaskStatusQueued {
		t.Fatalf("status = %q, want %q", s.tasks[record.ID].Status, model.TaskStatusQueued)
	}
	_ = q
}

func TestRetryTask_UpdateStoreFailure_ReturnsSyncError(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	s.updateErr = errors.New("sqlite write failed")
	q := &stubTaskEnqueuer{asynqID: "asynq-999"}

	_, _, err := retryTask(context.Background(), s, q, record.ID)
	if err == nil {
		t.Fatal("retryTask should return sync error")
	}
}
