package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/fix"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// mockPoolRunner 模拟 PoolRunner 接口
type mockPoolRunner struct {
	result *worker.ExecutionResult
	err    error
	calls  int
}

func (m *mockPoolRunner) Run(_ context.Context, _ model.TaskPayload) (*worker.ExecutionResult, error) {
	m.calls++
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
	result             *review.ReviewResult
	err                error
	writeDegradedErr   error
	calls              int
	writeDegradedCalls int
}

func (m *mockReviewExecutor) Execute(_ context.Context, _ model.TaskPayload) (*review.ReviewResult, error) {
	m.calls++
	return m.result, m.err
}

func (m *mockReviewExecutor) WriteDegraded(_ context.Context, _ model.TaskPayload, _ *review.ReviewResult) error {
	m.writeDegradedCalls++
	return m.writeDegradedErr
}

type mockFixExecutor struct {
	result *fix.FixResult
	err    error
	calls  int
}

func (m *mockFixExecutor) Execute(_ context.Context, _ model.TaskPayload) (*fix.FixResult, error) {
	m.calls++
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
	// M2.6: 新增开始通知，预期 2 条（开始 + 完成）
	if len(notifier.messages) != 2 {
		t.Fatalf("notification count = %d, want 2", len(notifier.messages))
	}
	msg := notifier.messages[1] // 完成通知
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

func TestProcessTask_StaleReview_IsCancelledWithoutNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-review-stale-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     24,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-review-stale",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	reviewSvc := &mockReviewExecutor{
		result: &review.ReviewResult{RawOutput: "stale raw output"},
		err:    review.ErrStaleReview,
	}
	p := NewProcessor(
		&mockPoolRunner{},
		s,
		notifier,
		slog.Default(),
		WithReviewService(reviewSvc),
	)

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("stale review 应返回非 nil 错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("stale review 应包含 asynq.SkipRetry，实际: %v", err)
	}

	got := s.tasks["proc-task-review-stale"]
	if got.Status != model.TaskStatusCancelled {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusCancelled)
	}
	if got.CompletedAt == nil {
		t.Fatal("stale review 应设置 CompletedAt")
	}
	if !strings.Contains(got.Error, "评审已过时") {
		t.Fatalf("error = %q, want contains %q", got.Error, "评审已过时")
	}
	// M2.6: 开始通知在执行前发送，stale review 仅阻止完成通知
	if len(notifier.messages) != 1 {
		t.Fatalf("notification count = %d, want 1 (仅开始通知)", len(notifier.messages))
	}
	if notifier.messages[0].EventType != notify.EventPRReviewStarted {
		t.Errorf("唯一通知应为开始通知，实际 event type = %q", notifier.messages[0].EventType)
	}
	if reviewSvc.calls != 1 {
		t.Fatalf("review service calls = %d, want 1", reviewSvc.calls)
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
	// M2.6: 开始通知 + 完成通知
	if len(notifier.messages) != 2 {
		t.Fatalf("notification count = %d, want 2", len(notifier.messages))
	}
	msg := notifier.messages[1] // 完成通知
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
	// M2.6: 开始通知 + 完成通知（即使 notifier 返回 err，仍会尝试发送两条）
	if len(notifier.messages) != 2 {
		t.Fatalf("notification count = %d, want 2", len(notifier.messages))
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
	// M2.6: 开始通知 + 完成通知
	if len(notifier.messages) != 2 {
		t.Fatalf("notification count = %d, want 2", len(notifier.messages))
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
	if len(notifier.messages) != 2 {
		t.Fatalf("notification count = %d, want 2 (start + done)", len(notifier.messages))
	}
	// 第一条是 start 通知
	if notifier.messages[0].EventType != notify.EventIssueFixStarted {
		t.Errorf("first event type = %q, want %q", notifier.messages[0].EventType, notify.EventIssueFixStarted)
	}
	// 第二条是 completion 通知
	msg := notifier.messages[1]
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
	if len(notifier.messages) != 2 {
		t.Fatalf("notification count = %d, want 2 (start + failed)", len(notifier.messages))
	}
	// 第一条是 start 通知
	if notifier.messages[0].EventType != notify.EventIssueFixStarted {
		t.Errorf("first event type = %q, want %q", notifier.messages[0].EventType, notify.EventIssueFixStarted)
	}
	// 第二条是 completion 通知
	msg := notifier.messages[1]
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
	// M2.6: 开始通知仍会发送，完成通知因持久化失败而跳过
	if len(notifier.messages) != 1 {
		t.Fatalf("notification count = %d, want 1 (仅开始通知)", len(notifier.messages))
	}
	if notifier.messages[0].EventType != notify.EventPRReviewStarted {
		t.Errorf("唯一通知应为开始通知，实际 event type = %q", notifier.messages[0].EventType)
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

func TestProcessTask_Retrying_SendsNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	record := &model.TaskRecord{
		ID:       "proc-task-retrying-notify",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusRetrying,
		MaxRetry: 3,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     1,
		},
	}
	p := NewProcessor(&mockPoolRunner{}, s, notifier, slog.Default())
	p.sendCompletionNotification(context.Background(), record, nil)
	if len(notifier.messages) != 1 {
		t.Fatalf("notification count = %d, want 1", len(notifier.messages))
	}
	msg := notifier.messages[0]
	if msg.EventType != notify.EventSystemError {
		t.Errorf("event type = %q, want %q", msg.EventType, notify.EventSystemError)
	}
	if msg.Metadata[notify.MetaKeyRetryCount] == "" {
		t.Error("metadata should contain retry_count")
	}
	if msg.Metadata[notify.MetaKeyMaxRetry] == "" {
		t.Error("metadata should contain max_retry")
	}
	if msg.Metadata[notify.MetaKeyTaskStatus] != string(model.TaskStatusRetrying) {
		t.Errorf("task_status = %q, want %q", msg.Metadata[notify.MetaKeyTaskStatus], model.TaskStatusRetrying)
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
		CLIMeta: &model.CLIMeta{
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
		CLIMeta:   &model.CLIMeta{IsError: false},
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
		CLIMeta:        &model.CLIMeta{IsError: false},
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

func TestAdaptFixResult_ParseErrorNoLongerFails(t *testing.T) {
	r := &fix.FixResult{
		RawOutput:  "plain text output",
		ParseError: errors.New("analysis parse failed"),
	}

	result := adaptFixResult(r)

	if result == nil {
		t.Fatal("adaptFixResult 应返回非 nil")
	}
	// M3.3: ParseError 不再导致任务失败
	if result.ExitCode != 0 {
		t.Fatalf("ParseError 不应导致非零退出码，实际: %d", result.ExitCode)
	}
	if result.Error != "analysis parse failed" {
		t.Fatalf("Error 应保留 ParseError 信息，实际: %q", result.Error)
	}
}

func TestAdaptFixResult_ParseErrorWithoutCLIError_Succeeds(t *testing.T) {
	r := &fix.FixResult{
		RawOutput:  "plain text output",
		CLIMeta:    &model.CLIMeta{IsError: false, DurationMs: 5000},
		ParseError: errors.New("analysis parse failed"),
	}

	result := adaptFixResult(r)

	if result == nil {
		t.Fatal("adaptFixResult 应返回非 nil")
	}
	// M3.3: ParseError 不再导致 ExitCode=1（降级评论已发）
	if result.ExitCode != 0 {
		t.Fatalf("ParseError（无 CLIError）应 ExitCode=0，实际: %d", result.ExitCode)
	}
	// 错误信息仍保留在 Error 字段供调试
	if result.Error != "analysis parse failed" {
		t.Fatalf("Error 应保留 ParseError 信息，实际: %q", result.Error)
	}
}

func TestAdaptFixResult_PreservesCLIErrorAndParseError(t *testing.T) {
	r := &fix.FixResult{
		RawOutput:  "bad output",
		CLIMeta:    &model.CLIMeta{IsError: true},
		ParseError: errors.New("analysis parse failed"),
	}

	result := adaptFixResult(r)

	if result == nil {
		t.Fatal("adaptFixResult 应返回非 nil")
	}
	if result.ExitCode != 1 {
		t.Fatalf("exit_code = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Error, "Claude CLI 报告错误") {
		t.Fatalf("Error 应保留 CLI 错误，实际: %q", result.Error)
	}
	if !strings.Contains(result.Error, "analysis parse failed") {
		t.Fatalf("Error 应附加 ParseError，实际: %q", result.Error)
	}
}

func TestAdaptFixResult_WritebackErrorPreserved(t *testing.T) {
	r := &fix.FixResult{
		RawOutput:      "output",
		CLIMeta:        &model.CLIMeta{IsError: false},
		WritebackError: fmt.Errorf("Gitea API 500"),
	}

	result := adaptFixResult(r)

	if result == nil {
		t.Fatal("adaptFixResult 应返回非 nil")
	}
	if result.ExitCode != 1 {
		t.Fatalf("WritebackError 应触发可重试失败，实际退出码: %d", result.ExitCode)
	}
	if !strings.Contains(result.Error, "回写失败") {
		t.Fatalf("Error 应包含回写失败信息，实际: %q", result.Error)
	}
}

func TestAdaptFixResult_PreservesWorkerExitCode(t *testing.T) {
	r := &fix.FixResult{
		RawOutput: "invalid model",
		ExitCode:  17,
	}

	result := adaptFixResult(r)

	if result == nil {
		t.Fatal("adaptFixResult 应返回非 nil")
	}
	if result.ExitCode != 17 {
		t.Fatalf("ExitCode = %d, want 17", result.ExitCode)
	}
	if !strings.Contains(result.Error, "17") {
		t.Fatalf("Error 应包含原始退出码，实际: %q", result.Error)
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
	_, ok := p.buildNotificationMessage(record, nil)
	if ok {
		t.Error("RepoOwner 为空时不应生成通知消息")
	}
}

func TestBuildNotificationMessage_NilRecord(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default())
	_, ok := p.buildNotificationMessage(nil, nil)
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
	_, ok := p.buildNotificationMessage(record, nil)
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

func TestProcessTask_ReviewParseFailure_PersistsRawOutputAndWritesDegraded(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-review-parse-failed",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     10,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-review-parse-failed",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	reviewExec := &mockReviewExecutor{
		result: &review.ReviewResult{
			RawOutput:  "raw review output",
			ParseError: errors.New("inner json invalid"),
		},
		err: fmt.Errorf("%w: inner json invalid", review.ErrParseFailure),
	}

	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithReviewService(reviewExec))
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("解析失败应返回错误")
	}
	if errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("解析失败不应 SkipRetry，实际: %v", err)
	}
	if reviewExec.writeDegradedCalls != 1 {
		t.Fatalf("WriteDegraded 调用次数 = %d, want 1", reviewExec.writeDegradedCalls)
	}

	got := s.tasks["proc-task-review-parse-failed"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if got.Result != "raw review output" {
		t.Fatalf("result = %q, want %q", got.Result, "raw review output")
	}
	if !strings.Contains(got.Error, review.ErrParseFailure.Error()) {
		t.Fatalf("error = %q，应包含 %q", got.Error, review.ErrParseFailure.Error())
	}
}

func TestProcessTask_ReviewParseFailure_DegradedStaleMarksCancelled(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-review-parse-stale",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     11,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-review-parse-stale",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	reviewExec := &mockReviewExecutor{
		result: &review.ReviewResult{
			RawOutput:  "raw review output",
			ParseError: errors.New("inner json invalid"),
		},
		err:              fmt.Errorf("%w: inner json invalid", review.ErrParseFailure),
		writeDegradedErr: review.ErrStaleReview,
	}

	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithReviewService(reviewExec))
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("stale 降级回写应返回错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("stale 降级回写应包含 SkipRetry，实际: %v", err)
	}
	if reviewExec.writeDegradedCalls != 1 {
		t.Fatalf("WriteDegraded 调用次数 = %d, want 1", reviewExec.writeDegradedCalls)
	}

	got := s.tasks["proc-task-review-parse-stale"]
	if got.Status != model.TaskStatusCancelled {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusCancelled)
	}
	if !strings.Contains(got.Error, "评审已过时，被更新的任务取代") {
		t.Fatalf("error = %q，应包含过时取消原因", got.Error)
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

// cancelPoolRunner 是 pool runner 的 mock，其 Run 方法固定返回 context.Canceled
type cancelPoolRunner struct{}

func (c *cancelPoolRunner) Run(_ context.Context, _ model.TaskPayload) (*worker.ExecutionResult, error) {
	return nil, context.Canceled
}

func TestProcessTask_ContextCanceled(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-canceled-1",
		RepoFullName: "org/repo",
		PRNumber:     99,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-canceled",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	// cancelPoolRunner 模拟任务被取消（新评审取代旧评审场景）
	p := NewProcessor(&cancelPoolRunner{}, s, nil, slog.Default())
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))

	// 1. ProcessTask 应返回包含 asynq.SkipRetry 的错误
	if err == nil {
		t.Fatal("context.Canceled 应使 ProcessTask 返回非 nil 错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("返回错误应包含 asynq.SkipRetry，实际: %v", err)
	}

	got := s.tasks["proc-task-canceled"]

	// 2. record.Status 应变为 "cancelled"
	if got.Status != model.TaskStatusCancelled {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusCancelled)
	}

	// 3. record.Error 应包含 "任务被取消"
	if !strings.Contains(got.Error, "任务被取消") {
		t.Errorf("record.Error = %q，应包含 \"任务被取消\"", got.Error)
	}

	// 4. record.CompletedAt 应已设置
	if got.CompletedAt == nil {
		t.Error("record.CompletedAt 应在取消时设置，实际为 nil")
	}
}

// mockReviewEnabledChecker 模拟 ReviewEnabledChecker 接口
type mockReviewEnabledChecker struct {
	enabled map[string]bool
}

func (m *mockReviewEnabledChecker) IsReviewEnabled(repoFullName string) bool {
	if m.enabled == nil {
		return true
	}
	v, ok := m.enabled[repoFullName]
	if !ok {
		return true
	}
	return v
}

func TestProcessTask_ReviewDisabled(t *testing.T) {
	t.Run("Enabled=false 时 review_pr 任务跳过并标记成功", func(t *testing.T) {
		s := newMockStore()
		payload := model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			DeliveryID:   "dlv-disabled-1",
			RepoFullName: "org/repo",
			PRNumber:     10,
		}
		now := time.Now()
		record := &model.TaskRecord{
			ID:         "proc-task-disabled-1",
			TaskType:   model.TaskTypeReviewPR,
			Status:     model.TaskStatusQueued,
			Payload:    payload,
			DeliveryID: payload.DeliveryID,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		seedRecord(s, record)

		pool := &mockPoolRunner{}
		reviewSvc := &mockReviewExecutor{}
		checker := &mockReviewEnabledChecker{enabled: map[string]bool{"org/repo": false}}

		p := NewProcessor(pool, s, nil, slog.Default(),
			WithReviewService(reviewSvc),
			WithReviewEnabledChecker(checker),
		)

		if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
			t.Fatalf("Enabled=false 时 ProcessTask 应返回 nil，实际: %v", err)
		}
		if pool.calls != 0 {
			t.Errorf("pool.calls = %d, want 0", pool.calls)
		}
		if reviewSvc.calls != 0 {
			t.Errorf("reviewSvc.calls = %d, want 0", reviewSvc.calls)
		}
		got := s.tasks["proc-task-disabled-1"]
		if got.Status != model.TaskStatusSucceeded {
			t.Errorf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
		}
		if got.CompletedAt == nil {
			t.Error("CompletedAt 应在跳过时设置")
		}
	})

	t.Run("Enabled=true 时 review_pr 任务正常执行", func(t *testing.T) {
		s := newMockStore()
		payload := model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			DeliveryID:   "dlv-enabled-1",
			RepoFullName: "org/repo",
			PRNumber:     11,
		}
		now := time.Now()
		record := &model.TaskRecord{
			ID:         "proc-task-enabled-1",
			TaskType:   model.TaskTypeReviewPR,
			Status:     model.TaskStatusQueued,
			Payload:    payload,
			DeliveryID: payload.DeliveryID,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		seedRecord(s, record)

		reviewSvc := &mockReviewExecutor{result: &review.ReviewResult{RawOutput: "ok"}}
		checker := &mockReviewEnabledChecker{enabled: map[string]bool{"org/repo": true}}

		p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(),
			WithReviewService(reviewSvc),
			WithReviewEnabledChecker(checker),
		)

		if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
			t.Fatalf("Enabled=true 时 ProcessTask 应返回 nil，实际: %v", err)
		}
		if reviewSvc.calls != 1 {
			t.Errorf("reviewSvc.calls = %d, want 1", reviewSvc.calls)
		}
		got := s.tasks["proc-task-enabled-1"]
		if got.Status != model.TaskStatusSucceeded {
			t.Errorf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
		}
	})

	t.Run("reviewEnabledChecker=nil 时默认启用（向后兼容）", func(t *testing.T) {
		s := newMockStore()
		payload := model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			DeliveryID:   "dlv-nocheck-1",
			RepoFullName: "org/repo",
			PRNumber:     12,
		}
		now := time.Now()
		record := &model.TaskRecord{
			ID:         "proc-task-nocheck-1",
			TaskType:   model.TaskTypeReviewPR,
			Status:     model.TaskStatusQueued,
			Payload:    payload,
			DeliveryID: payload.DeliveryID,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		seedRecord(s, record)

		reviewSvc := &mockReviewExecutor{result: &review.ReviewResult{RawOutput: "ok"}}

		// 不注入 reviewEnabledChecker
		p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(),
			WithReviewService(reviewSvc),
		)

		if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
			t.Fatalf("checker=nil 时 ProcessTask 应返回 nil，实际: %v", err)
		}
		if reviewSvc.calls != 1 {
			t.Errorf("reviewSvc.calls = %d, want 1（checker=nil 不应跳过执行）", reviewSvc.calls)
		}
	})

	t.Run("非 review_pr 任务不受 Enabled 影响", func(t *testing.T) {
		s := newMockStore()
		payload := model.TaskPayload{
			TaskType:     model.TaskTypeFixIssue,
			DeliveryID:   "dlv-fixissue-nocheck-1",
			RepoFullName: "org/repo",
			IssueNumber:  5,
		}
		now := time.Now()
		record := &model.TaskRecord{
			ID:         "proc-task-fixissue-nocheck-1",
			TaskType:   model.TaskTypeFixIssue,
			Status:     model.TaskStatusQueued,
			Payload:    payload,
			DeliveryID: payload.DeliveryID,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		seedRecord(s, record)

		pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0, Output: "fixed"}}
		// checker 对 org/repo 返回 false，但 fix_issue 不应受影响
		checker := &mockReviewEnabledChecker{enabled: map[string]bool{"org/repo": false}}

		p := NewProcessor(pool, s, nil, slog.Default(),
			WithReviewEnabledChecker(checker),
		)

		if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
			t.Fatalf("fix_issue 任务不受 Enabled 影响，应返回 nil，实际: %v", err)
		}
		if pool.calls != 1 {
			t.Errorf("pool.calls = %d, want 1", pool.calls)
		}
		got := s.tasks["proc-task-fixissue-nocheck-1"]
		if got.Status != model.TaskStatusSucceeded {
			t.Errorf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
		}
	})
}

func TestProcessTask_PreCancelledRecord_SkipsExecution(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-pre-cancelled-1",
		RepoFullName: "org/repo",
		PRNumber:     100,
	}

	now := time.Now()
	completedAt := now.Add(-time.Minute)
	record := &model.TaskRecord{
		ID:          "proc-task-pre-cancelled",
		TaskType:    model.TaskTypeReviewPR,
		Status:      model.TaskStatusCancelled,
		Payload:     payload,
		DeliveryID:  payload.DeliveryID,
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now,
		CompletedAt: &completedAt,
		Error:       "被同一 PR 的新评审任务取代",
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0, Output: "should not run"}}
	p := NewProcessor(pool, s, nil, slog.Default())

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("已取消任务应返回 SkipRetry 错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("错误应包含 SkipRetry，实际: %v", err)
	}
	if pool.calls != 0 {
		t.Fatalf("pool.Run calls = %d, want 0", pool.calls)
	}
	got := s.tasks["proc-task-pre-cancelled"]
	if got.Status != model.TaskStatusCancelled {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusCancelled)
	}
}

func TestProcessTask_ReviewPR_SendStartNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-start-notify-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     50,
		PRTitle:      "修复登录验证逻辑",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-start-notify",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	reviewSvc := &mockReviewExecutor{result: &review.ReviewResult{RawOutput: "ok"}}
	checker := &mockReviewEnabledChecker{enabled: map[string]bool{"org/repo": true}}

	p := NewProcessor(&mockPoolRunner{}, s, notifier, slog.Default(),
		WithReviewService(reviewSvc),
		WithReviewEnabledChecker(checker),
		WithGiteaBaseURL("https://gitea.example.com"),
	)

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}

	// 应有 2 条通知：开始 + 完成
	if len(notifier.messages) != 2 {
		t.Fatalf("notification count = %d, want 2", len(notifier.messages))
	}

	startMsg := notifier.messages[0]
	if startMsg.EventType != notify.EventPRReviewStarted {
		t.Errorf("start msg event type = %q, want %q", startMsg.EventType, notify.EventPRReviewStarted)
	}
	if startMsg.Target.Number != 50 || !startMsg.Target.IsPR {
		t.Errorf("start msg target: %+v", startMsg.Target)
	}
	if startMsg.Metadata[notify.MetaKeyPRURL] != "https://gitea.example.com/org/repo/pulls/50" {
		t.Errorf("start msg pr_url = %q", startMsg.Metadata[notify.MetaKeyPRURL])
	}
	if startMsg.Metadata[notify.MetaKeyPRTitle] != "修复登录验证逻辑" {
		t.Errorf("start msg pr_title = %q", startMsg.Metadata[notify.MetaKeyPRTitle])
	}

	completeMsg := notifier.messages[1]
	if completeMsg.EventType != notify.EventPRReviewDone {
		t.Errorf("complete msg event type = %q, want %q", completeMsg.EventType, notify.EventPRReviewDone)
	}
}

func TestProcessTask_ReviewPR_RetryDoesNotResendStartNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-start-notify-retry-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     53,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-start-notify-retry",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusRetrying,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		RetryCount: 1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	reviewSvc := &mockReviewExecutor{result: &review.ReviewResult{RawOutput: "ok"}}
	p := NewProcessor(&mockPoolRunner{}, s, notifier, slog.Default(),
		WithReviewService(reviewSvc),
		WithGiteaBaseURL("https://gitea.example.com"),
	)

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}

	if len(notifier.messages) != 1 {
		t.Fatalf("notification count = %d, want 1", len(notifier.messages))
	}
	if notifier.messages[0].EventType != notify.EventPRReviewDone {
		t.Fatalf("retry attempt should only send completion notification, got %q", notifier.messages[0].EventType)
	}
}

func TestProcessTask_ReviewDisabled_NoStartNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-disabled-no-start-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     51,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-disabled-no-start",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	checker := &mockReviewEnabledChecker{enabled: map[string]bool{"org/repo": false}}
	p := NewProcessor(&mockPoolRunner{}, s, notifier, slog.Default(),
		WithReviewEnabledChecker(checker),
		WithGiteaBaseURL("https://gitea.example.com"),
	)

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}

	if len(notifier.messages) != 0 {
		t.Fatalf("notification count = %d, want 0", len(notifier.messages))
	}
}

func TestProcessTask_CompletionNotification_HasMetadata(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-metadata-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     52,
		PRTitle:      "重构登录模块",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-metadata",
		TaskType:   model.TaskTypeReviewPR,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	reviewResult := &review.ReviewResult{
		RawOutput: "review output",
		CLIMeta:   &model.CLIMeta{IsError: false},
		Review: &review.ReviewOutput{
			Summary: "有问题",
			Verdict: review.VerdictRequestChanges,
			Issues: []review.ReviewIssue{
				{Severity: "CRITICAL", File: "a.go", Line: 1, Message: "严重问题"},
				{Severity: "WARNING", File: "b.go", Line: 2, Message: "警告问题"},
			},
		},
	}

	p := NewProcessor(&mockPoolRunner{}, s, notifier, slog.Default(),
		WithReviewService(&mockReviewExecutor{result: reviewResult}),
		WithReviewEnabledChecker(&mockReviewEnabledChecker{enabled: map[string]bool{"org/repo": true}}),
		WithGiteaBaseURL("https://gitea.example.com"),
	)

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}

	if len(notifier.messages) < 2 {
		t.Fatalf("notification count = %d, want >= 2", len(notifier.messages))
	}

	completeMsg := notifier.messages[1]
	if completeMsg.Metadata[notify.MetaKeyPRURL] != "https://gitea.example.com/org/repo/pulls/52" {
		t.Errorf("pr_url = %q", completeMsg.Metadata[notify.MetaKeyPRURL])
	}
	if completeMsg.Metadata[notify.MetaKeyPRTitle] != "重构登录模块" {
		t.Errorf("pr_title = %q", completeMsg.Metadata[notify.MetaKeyPRTitle])
	}
	if completeMsg.Metadata[notify.MetaKeyVerdict] != "request_changes" {
		t.Errorf("verdict = %q", completeMsg.Metadata[notify.MetaKeyVerdict])
	}
	if completeMsg.Metadata[notify.MetaKeyIssueSummary] == "" {
		t.Error("issue_summary 不应为空")
	}
}

func TestBuildPRURL(t *testing.T) {
	tests := []struct {
		baseURL  string
		owner    string
		repo     string
		number   int64
		expected string
	}{
		{"https://gitea.example.com", "org", "repo", 42, "https://gitea.example.com/org/repo/pulls/42"},
		{"https://gitea.example.com", "org", "repo", 1, "https://gitea.example.com/org/repo/pulls/1"},
	}
	for _, tc := range tests {
		got := buildPRURL(tc.baseURL, model.TaskPayload{RepoOwner: tc.owner, RepoName: tc.repo, PRNumber: tc.number})
		if got != tc.expected {
			t.Errorf("buildPRURL(%q, ...) = %q, want %q", tc.baseURL, got, tc.expected)
		}
	}
}

func TestFormatIssueSummary(t *testing.T) {
	issues := []review.ReviewIssue{
		{Severity: "CRITICAL"},
		{Severity: "CRITICAL"},
		{Severity: "WARNING"},
		{Severity: "INFO"},
	}
	got := formatIssueSummary(issues)
	if got == "" {
		t.Fatal("formatIssueSummary 不应返回空字符串")
	}
	if !strings.Contains(got, "CRITICAL") || !strings.Contains(got, "WARNING") || !strings.Contains(got, "INFO") {
		t.Errorf("formatIssueSummary = %q, should contain severity counts", got)
	}
}

// TestProcessTask_FixIssue_WithService 验证 M3.2 激活后，
// fix_issue 任务走 fixService.Execute 路径，pool.Run 不被调用。
func TestProcessTask_FixIssue_WithService(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "dlv-fix-svc-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  5,
		IssueTitle:   "bug report",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-fix-svc-1",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	cliJSON := `{"type":"result","subtype":"success","is_error":false,"cost_usd":0.03,"duration_ms":8000,"duration_api_ms":7000,"num_turns":2,"session_id":"sess-ok","result":"{\"info_sufficient\":true,\"analysis\":\"test\",\"confidence\":\"high\"}"}`
	fixExec := &mockFixExecutor{
		result: &fix.FixResult{
			IssueContext: &fix.IssueContext{},
			RawOutput:    cliJSON,
			CLIMeta: &model.CLIMeta{
				CostUSD:    0.03,
				DurationMs: 8000,
			},
		},
	}
	pool := &mockPoolRunner{
		result: &worker.ExecutionResult{ExitCode: 0, Output: "should not be used"},
	}

	p := NewProcessor(pool, s, nil, slog.Default(), WithFixService(fixExec))
	task := buildAsynqTask(t, payload)

	if err := p.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}

	if fixExec.calls != 1 {
		t.Errorf("fixService.Execute 应被调用 1 次，实际 %d 次", fixExec.calls)
	}
	if pool.calls != 0 {
		t.Errorf("pool.Run 不应被调用，实际 %d 次", pool.calls)
	}

	got := s.tasks["proc-fix-svc-1"]
	if got.Status != model.TaskStatusSucceeded {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
	}
}

func TestProcessTask_FixIssue_WithService_WritebackErrorFailsTask(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "dlv-fix-svc-writeback-failed-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  5,
		IssueTitle:   "bug report",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-fix-svc-writeback-failed-1",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	fixExec := &mockFixExecutor{
		result: &fix.FixResult{
			RawOutput:      `{"type":"result"}`,
			CLIMeta:        &model.CLIMeta{IsError: false, DurationMs: 8000},
			WritebackError: errors.New("Gitea API 500"),
		},
	}
	pool := &mockPoolRunner{
		result: &worker.ExecutionResult{ExitCode: 0, Output: "should not be used"},
	}

	p := NewProcessor(pool, s, nil, slog.Default(), WithFixService(fixExec))
	task := buildAsynqTask(t, payload)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("writeback 失败应导致任务失败")
	}

	if fixExec.calls != 1 {
		t.Errorf("fixService.Execute 应被调用 1 次，实际 %d 次", fixExec.calls)
	}
	if pool.calls != 0 {
		t.Errorf("pool.Run 不应被调用，实际 %d 次", pool.calls)
	}

	got := s.tasks["proc-fix-svc-writeback-failed-1"]
	if got.Status != model.TaskStatusFailed {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if !strings.Contains(got.Error, "回写失败") {
		t.Errorf("error = %q, should contain writeback failure", got.Error)
	}
}

func TestProcessTask_FixIssue_WithoutService(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "dlv-fix-fallback-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  5,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-fix-fallback-1",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{
		result: &worker.ExecutionResult{ExitCode: 0, Output: "pool result"},
	}

	// fixService 未注入
	p := NewProcessor(pool, s, nil, slog.Default())
	task := buildAsynqTask(t, payload)

	if err := p.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}

	if pool.calls != 1 {
		t.Errorf("pool.Run 应被调用 1 次，实际 %d 次", pool.calls)
	}
}

func TestProcessTask_FixIssue_MissingRef_SkipsRetry(t *testing.T) {
	s := newMockStore()
	fixExec := &mockFixExecutor{
		err: fmt.Errorf("Issue #10: %w", fix.ErrMissingIssueRef),
	}
	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0}}
	notifier := &stubNotifier{}
	p := NewProcessor(pool, s, notifier, slog.Default(), WithFixService(fixExec))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "delivery-miss-ref",
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
	}
	task := buildAsynqTask(t, payload)
	record := &model.TaskRecord{
		ID:         "task-miss-ref",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		DeliveryID: "delivery-miss-ref",
	}
	seedRecord(s, record)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("应返回 SkipRetry 错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("错误应包含 asynq.SkipRetry，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "跳过分析") {
		t.Errorf("错误信息应包含'跳过分析'，实际: %v", err)
	}
	updated := s.tasks["task-miss-ref"]
	if updated.Status != model.TaskStatusFailed {
		t.Errorf("状态应为 failed，实际: %s", updated.Status)
	}
}

func TestProcessTask_FixIssue_InvalidRef_SkipsRetry(t *testing.T) {
	s := newMockStore()
	fixExec := &mockFixExecutor{
		err: fmt.Errorf("Issue #10 ref=bad: %w", fix.ErrInvalidIssueRef),
	}
	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0}}
	notifier := &stubNotifier{}
	p := NewProcessor(pool, s, notifier, slog.Default(), WithFixService(fixExec))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "delivery-bad-ref",
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
	}
	task := buildAsynqTask(t, payload)
	record := &model.TaskRecord{
		ID:         "task-bad-ref",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		DeliveryID: "delivery-bad-ref",
	}
	seedRecord(s, record)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("应返回 SkipRetry 错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("错误应包含 asynq.SkipRetry，实际: %v", err)
	}
	updated := s.tasks["task-bad-ref"]
	if updated.Status != model.TaskStatusFailed {
		t.Errorf("状态应为 failed，实际: %s", updated.Status)
	}
}

func TestProcessTask_FixIssue_RefHintCommentFailure_Retries(t *testing.T) {
	s := newMockStore()
	commentErr := errors.New("Gitea API 500")
	fixExec := &mockFixExecutor{
		err: fmt.Errorf("Issue #10: 回写 ref 缺失提示失败: %w", commentErr),
	}
	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0}}
	notifier := &stubNotifier{}
	p := NewProcessor(pool, s, notifier, slog.Default(), WithFixService(fixExec))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "delivery-ref-comment-failed",
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
	}
	task := buildAsynqTask(t, payload)
	record := &model.TaskRecord{
		ID:         "task-ref-comment-failed",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		DeliveryID: payload.DeliveryID,
	}
	seedRecord(s, record)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("应返回错误")
	}
	if errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("评论回写失败不应 SkipRetry，实际: %v", err)
	}
	updated := s.tasks["task-ref-comment-failed"]
	if updated.Status != model.TaskStatusRetrying && updated.Status != model.TaskStatusFailed {
		t.Fatalf("状态应为 retrying 或 failed，实际: %s", updated.Status)
	}
	if !strings.Contains(updated.Error, "回写 ref 缺失提示失败") {
		t.Fatalf("错误应保留评论回写失败信息，实际: %q", updated.Error)
	}
}
