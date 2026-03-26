package queue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
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

type stubNotifier struct {
	messages []notify.Message
	err      error
}

func (s *stubNotifier) Send(_ context.Context, msg notify.Message) error {
	s.messages = append(s.messages, msg)
	return s.err
}

type mockReviewExecutor struct {
	result *review.ReviewResult
	err    error
}

func (m *mockReviewExecutor) Execute(_ context.Context, _ model.TaskPayload) (*review.ReviewResult, error) {
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

func TestNewProcessor_WithNotifier(t *testing.T) {
	s := newMockStore()
	pool := &mockPoolRunner{}
	notifier := &stubNotifier{}

	p := NewProcessor(pool, s, notifier, slog.Default())
	if p == nil {
		t.Fatal("NewProcessor should return non-nil processor")
	}
}

func TestProcessTask_Success_SendReviewNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-review-success-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     42,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-review-success",
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
			Output:      "review ok",
			ContainerID: "container-review-1",
		},
	}

	p := NewProcessor(pool, s, notifier, slog.Default())
	task := buildAsynqTask(t, payload)

	if err := p.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("notification count = %d, want 1", len(notifier.messages))
	}
	msg := notifier.messages[0]
	if msg.EventType != notify.EventPRReviewDone {
		t.Errorf("event type = %q, want %q", msg.EventType, notify.EventPRReviewDone)
	}
	if msg.Target.Number != 42 {
		t.Errorf("target number = %d, want 42", msg.Target.Number)
	}
	if !msg.Target.IsPR {
		t.Error("target IsPR should be true")
	}
	if msg.Target.Owner != "org" || msg.Target.Repo != "repo" {
		t.Errorf("target repo = %s/%s, want org/repo", msg.Target.Owner, msg.Target.Repo)
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

	p := NewProcessor(pool, s, nil, slog.Default())
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
	if got.Error != "" {
		t.Errorf("success task error = %q, want empty", got.Error)
	}
	if got.CompletedAt == nil {
		t.Error("completed_at should be set")
	}
}

func TestProcessTask_ReviewSuccess_PreservesWritebackError(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-review-writeback-warn-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     23,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-review-writeback-warn",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	p := NewProcessor(
		&mockPoolRunner{},
		s,
		nil,
		slog.Default(),
		WithReviewService(&mockReviewExecutor{
			result: &review.ReviewResult{
				RawOutput:      "review output",
				WritebackError: errors.New("inline mapping degraded"),
			},
		}),
	)

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}

	got := s.tasks["proc-task-review-writeback-warn"]
	if got.Status != model.TaskStatusSucceeded {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
	}
	if got.Error == "" {
		t.Fatal("成功任务也应保留 writeback 调试错误")
	}
	if got.Error != "回写失败: inline mapping degraded" {
		t.Fatalf("error = %q, want %q", got.Error, "回写失败: inline mapping degraded")
	}
}

func TestProcessTask_FailedReview_SendNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-review-failed-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     7,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-review-failed",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	p := NewProcessor(&mockPoolRunner{err: errors.New("review failed")}, s, notifier, slog.Default())
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("ProcessTask should return error when review task fails")
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("notification count = %d, want 1", len(notifier.messages))
	}
	msg := notifier.messages[0]
	if msg.EventType != notify.EventSystemError {
		t.Errorf("event type = %q, want %q", msg.EventType, notify.EventSystemError)
	}
	if msg.Title != "PR 自动评审任务失败" {
		t.Errorf("title = %q, want %q", msg.Title, "PR 自动评审任务失败")
	}
	if msg.Target.Number != 7 || !msg.Target.IsPR {
		t.Errorf("unexpected target: %+v", msg.Target)
	}
}

func TestProcessTask_NotificationFailure_DoesNotAffectTaskResult(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{err: errors.New("notify failed")}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-review-notify-fail-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     9,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-review-notify-failed",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	expectedErr := "docker daemon unavailable"
	p := NewProcessor(&mockPoolRunner{err: errors.New(expectedErr)}, s, notifier, slog.Default())
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("ProcessTask should return original execution error")
	}
	if err.Error() != "任务执行失败: "+expectedErr {
		t.Fatalf("error = %q, want %q", err.Error(), "任务执行失败: "+expectedErr)
	}
	got := s.tasks["proc-task-review-notify-failed"]
	if got.Status != model.TaskStatusFailed {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("notification count = %d, want 1", len(notifier.messages))
	}
}

func TestProcessTask_Success_NotificationFailure_DoesNotAffectTaskResult(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{err: errors.New("notify failed")}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-review-success-notify-fail-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     11,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-review-success-notify-failed",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	p := NewProcessor(&mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0, Output: "review ok"}}, s, notifier, slog.Default())
	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask should return nil on success even when notify fails: %v", err)
	}
	got := s.tasks["proc-task-review-success-notify-failed"]
	if got.Status != model.TaskStatusSucceeded {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("notification count = %d, want 1", len(notifier.messages))
	}
}

func TestProcessTask_Success_SendFixIssueNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "dlv-fix-success-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  15,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-fix-success",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	p := NewProcessor(&mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0, Output: "fix done"}}, s, notifier, slog.Default())
	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("notification count = %d, want 1", len(notifier.messages))
	}
	msg := notifier.messages[0]
	if msg.EventType != notify.EventFixIssueDone {
		t.Errorf("event type = %q, want %q", msg.EventType, notify.EventFixIssueDone)
	}
	if msg.Title != "Issue 自动修复任务完成" {
		t.Errorf("title = %q, want %q", msg.Title, "Issue 自动修复任务完成")
	}
	if msg.Target.Number != 15 || msg.Target.IsPR {
		t.Errorf("unexpected target: %+v", msg.Target)
	}
}

func TestProcessTask_FailedFixIssue_SendNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "dlv-fix-failed-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  16,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-fix-failed",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	p := NewProcessor(&mockPoolRunner{err: errors.New("fix failed")}, s, notifier, slog.Default())
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("ProcessTask should return error when fix task fails")
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("notification count = %d, want 1", len(notifier.messages))
	}
	msg := notifier.messages[0]
	if msg.EventType != notify.EventSystemError {
		t.Errorf("event type = %q, want %q", msg.EventType, notify.EventSystemError)
	}
	if msg.Title != "Issue 自动修复任务失败" {
		t.Errorf("title = %q, want %q", msg.Title, "Issue 自动修复任务失败")
	}
	if msg.Target.Number != 16 || msg.Target.IsPR {
		t.Errorf("unexpected target: %+v", msg.Target)
	}
}

func TestProcessTask_GenTests_NoNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		DeliveryID:   "dlv-gentests-success-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-gentests-success",
		TaskType:   model.TaskTypeGenTests,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	p := NewProcessor(&mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0, Output: "tests done"}}, s, notifier, slog.Default())
	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}
	if len(notifier.messages) != 0 {
		t.Fatalf("notification count = %d, want 0", len(notifier.messages))
	}
}

func TestProcessTask_FinalStatusPersistFailure_NoNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-review-persist-fail-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     12,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-review-persist-fail",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	s.failUpdateAt = 2
	p := NewProcessor(&mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0, Output: "review ok"}}, s, notifier, slog.Default())
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err != nil {
		t.Fatalf("ProcessTask should still return nil when final status persist fails: %v", err)
	}
	if s.updateCalls != 2 {
		t.Fatalf("update calls = %d, want 2", s.updateCalls)
	}
	if len(notifier.messages) != 0 {
		t.Fatalf("notification count = %d, want 0", len(notifier.messages))
	}
}

func TestProcessTask_InvalidTarget_NoNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-review-invalid-target-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     0,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-review-invalid-target",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	p := NewProcessor(&mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0, Output: "done"}}, s, notifier, slog.Default())
	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}
	if len(notifier.messages) != 0 {
		t.Fatalf("notification count = %d, want 0", len(notifier.messages))
	}
}

func TestProcessTask_Retrying_NoNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	record := &model.TaskRecord{
		ID:       "proc-task-retrying-no-notify",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusRetrying,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     1,
		},
	}
	p := NewProcessor(&mockPoolRunner{}, s, notifier, slog.Default())
	p.sendCompletionNotification(context.Background(), record)
	if len(notifier.messages) != 0 {
		t.Fatalf("notification count = %d, want 0", len(notifier.messages))
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

	p := NewProcessor(pool, s, nil, slog.Default())
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

	p := NewProcessor(pool, s, nil, slog.Default())
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
	p := NewProcessor(pool, s, nil, slog.Default())

	// 构造损坏的 payload
	badTask := asynq.NewTask(AsynqTypeReviewPR, []byte("not-json"))
	err := p.ProcessTask(context.Background(), badTask)
	if err == nil {
		t.Fatal("ProcessTask should return error on invalid payload")
	}
}

func TestAdaptReviewResult_CLIError(t *testing.T) {
	r := &review.ReviewResult{
		RawOutput: "some raw output",
		CLIMeta: &review.CLIMeta{
			IsError:    true,
			DurationMs: 500,
		},
	}

	result := adaptReviewResult(r)

	if result == nil {
		t.Fatal("adaptReviewResult 应返回非 nil")
	}
	if result.ExitCode != 1 {
		t.Errorf("CLIMeta.IsError=true 时 ExitCode 应为 1，实际: %d", result.ExitCode)
	}
	if !strings.Contains(result.Error, "Claude CLI 报告错误") {
		t.Errorf("Error 应包含\"Claude CLI 报告错误\"，实际: %q", result.Error)
	}
}

func TestProcessTask_ReviewSuccess_SeverityCounts(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-severity-counts-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     55,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-severity-counts",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	// 构造包含 2 CRITICAL + 1 ERROR + 1 WARNING 的评审结果
	reviewResult := &review.ReviewResult{
		RawOutput: "severity test output",
		CLIMeta:   &review.CLIMeta{IsError: false},
		Review: &review.ReviewOutput{
			Summary: "有若干问题",
			Verdict: review.VerdictRequestChanges,
			Issues: []review.ReviewIssue{
				{Severity: "CRITICAL", File: "a.go", Line: 1, Message: "严重问题1"},
				{Severity: "CRITICAL", File: "b.go", Line: 2, Message: "严重问题2"},
				{Severity: "ERROR", File: "c.go", Line: 3, Message: "错误问题"},
				{Severity: "WARNING", File: "d.go", Line: 4, Message: "警告问题"},
			},
		},
	}

	p := NewProcessor(
		&mockPoolRunner{},
		s,
		nil,
		slog.Default(),
		WithReviewService(&mockReviewExecutor{result: reviewResult}),
	)

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}

	got := s.tasks["proc-task-severity-counts"]
	if got.Status != model.TaskStatusSucceeded {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
	}
	// 2 CRITICAL + 1 ERROR = 安全网触发，adaptReviewResult 退出码仍为 0（非 CLIMeta error）
	// 验证任务记录状态正确（评审结果成功处理）
	if got.Error != "" {
		t.Errorf("无 writeback 错误时 Error 应为空，实际: %q", got.Error)
	}
}

func TestProcessTask_AdaptReviewResult_WritebackError(t *testing.T) {
	// 测试 adaptReviewResult 中同时存在 Error 和 WritebackError 的拼接分支
	r := &review.ReviewResult{
		RawOutput:      "partial output",
		CLIMeta:        &review.CLIMeta{IsError: false},
		ParseError:     errors.New("parse failed"),
		WritebackError: errors.New("gitea api timeout"),
	}

	result := adaptReviewResult(r)

	if result == nil {
		t.Fatal("adaptReviewResult 应返回非 nil")
	}
	if !strings.Contains(result.Error, "parse failed") {
		t.Errorf("Error 应包含原始 ParseError 信息，实际: %q", result.Error)
	}
	if !strings.Contains(result.Error, "gitea api timeout") {
		t.Errorf("Error 应包含 WritebackError 信息，实际: %q", result.Error)
	}
	// 验证两部分通过分隔符拼接
	if !strings.Contains(result.Error, "; ") {
		t.Errorf("Error 应包含 '; ' 分隔符拼接两段错误，实际: %q", result.Error)
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

func TestNewProcessor_PanicOnNilPool(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewProcessor(nil pool) 应 panic")
		}
	}()
	NewProcessor(nil, newMockStore(), nil, slog.Default())
}

func TestNewProcessor_PanicOnNilStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewProcessor(nil store) 应 panic")
		}
	}()
	NewProcessor(&mockPoolRunner{}, nil, nil, slog.Default())
}

func TestNewProcessor_NilLoggerUsesDefault(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, nil)
	if p == nil {
		t.Fatal("NewProcessor 应返回非 nil")
	}
}

func TestProcessTask_ExitCode2_SkipRetry(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-exitcode2",
		RepoFullName: "org/repo",
		PRNumber:     1,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-exitcode2",
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
			ExitCode: 2,
			Error:    "parameter error",
		},
	}

	p := NewProcessor(pool, s, nil, slog.Default())
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("退出码 2 应返回错误")
	}
	// 验证返回的错误包含 asynq.SkipRetry
	if !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("退出码 2 应包含 SkipRetry，得到: %v", err)
	}

	got := s.tasks["proc-task-exitcode2"]
	if got.Status != model.TaskStatusFailed {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
}

func TestBuildNotificationMessage_EmptyRepoOwner(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default())

	record := &model.TaskRecord{
		ID:       "msg-no-owner",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusSucceeded,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			RepoOwner:    "",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     1,
		},
	}
	_, ok := p.buildNotificationMessage(record)
	if ok {
		t.Error("RepoOwner 为空时不应生成通知消息")
	}
}

func TestBuildNotificationMessage_NilRecord(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default())
	_, ok := p.buildNotificationMessage(nil)
	if ok {
		t.Error("nil record 不应生成通知消息")
	}
}

func TestBuildNotificationMessage_FixIssue_InvalidNumber(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default())

	record := &model.TaskRecord{
		ID:       "msg-fix-no-number",
		TaskType: model.TaskTypeFixIssue,
		Status:   model.TaskStatusSucceeded,
		Payload: model.TaskPayload{
			TaskType:    model.TaskTypeFixIssue,
			RepoOwner:   "org",
			RepoName:    "repo",
			IssueNumber: 0,
		},
	}
	_, ok := p.buildNotificationMessage(record)
	if ok {
		t.Error("IssueNumber=0 不应生成通知消息")
	}
}

func TestFindRecord_EmptyDeliveryID(t *testing.T) {
	s := newMockStore()
	// 任务没有 deliveryID，通过 ID 直接存储
	record := &model.TaskRecord{
		ID:       "direct-id-task",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusQueued,
		Payload: model.TaskPayload{
			TaskType: model.TaskTypeReviewPR,
		},
	}
	s.tasks["direct-id-task"] = record

	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0, Output: "ok"}}
	p := NewProcessor(pool, s, nil, slog.Default())

	// payload 没有 DeliveryID，findRecord 应失败（无法通过空 delivery 查找）
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "",
		RepoFullName: "org/repo",
	}
	task := buildAsynqTask(t, payload)
	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("空 DeliveryID 且无匹配记录应返回错误")
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

	p := NewProcessor(pool, s, nil, slog.Default())
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
	p := NewProcessor(pool, s, nil, slog.Default())

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
