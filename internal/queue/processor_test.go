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

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/e2e"
	"otws19.zicp.vip/kelin/dtworkflow/internal/fix"
	"otws19.zicp.vip/kelin/dtworkflow/internal/iterate"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	testgen "otws19.zicp.vip/kelin/dtworkflow/internal/test"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// mockPoolRunner 模拟 PoolRunner 接口
type mockPoolRunner struct {
	result    *worker.ExecutionResult
	err       error
	calls     int
	lastCmd   []string
	lastStdin []byte
}

func (m *mockPoolRunner) Run(_ context.Context, _ model.TaskPayload) (*worker.ExecutionResult, error) {
	m.calls++
	return m.result, m.err
}

func (m *mockPoolRunner) RunWithCommandAndStdin(_ context.Context, _ model.TaskPayload, cmd []string, stdin []byte) (*worker.ExecutionResult, error) {
	m.calls++
	m.lastCmd = append([]string(nil), cmd...)
	m.lastStdin = append([]byte(nil), stdin...)
	return m.result, m.err
}

type stubNotifier struct {
	messages     []notify.Message
	err          error
	commentErr   error
	commentCalls []genTestsPRCommentCall
}

func (s *stubNotifier) Send(_ context.Context, msg notify.Message) error {
	s.messages = append(s.messages, msg)
	return s.err
}

type genTestsPRCommentCall struct {
	owner string
	repo  string
	pr    int64
	body  string
}

func (s *stubNotifier) CommentOnGenTestsPR(_ context.Context, owner, repo string, prNumber int64, body string) error {
	s.commentCalls = append(s.commentCalls, genTestsPRCommentCall{
		owner: owner,
		repo:  repo,
		pr:    prNumber,
		body:  body,
	})
	return s.commentErr
}

type mockReviewExecutor struct {
	result             *review.ReviewResult
	err                error
	writeDegradedErr   error
	cfg                review.ReviewConfig
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

func (m *mockReviewExecutor) ResolveConfig(_ string) review.ReviewConfig {
	return m.cfg
}

type mockFixExecutor struct {
	result             *fix.FixResult
	err                error
	writeDegradedErr   error
	calls              int
	writeDegradedCalls int
}

func (m *mockFixExecutor) Execute(_ context.Context, _ model.TaskPayload) (*fix.FixResult, error) {
	m.calls++
	return m.result, m.err
}

func (m *mockFixExecutor) WriteDegraded(_ context.Context, _ model.TaskPayload, _ *fix.FixResult) error {
	m.writeDegradedCalls++
	return m.writeDegradedErr
}

type mockTestExecutor struct {
	result             *testgen.TestGenResult
	err                error
	writeDegradedErr   error
	calls              int
	writeDegradedCalls int
}

func (m *mockTestExecutor) Execute(_ context.Context, _ model.TaskPayload) (*testgen.TestGenResult, error) {
	m.calls++
	return m.result, m.err
}

func (m *mockTestExecutor) WriteDegraded(_ context.Context, _ model.TaskPayload, _ *testgen.TestGenResult) error {
	m.writeDegradedCalls++
	return m.writeDegradedErr
}

type mockE2EExecutor struct {
	result *e2e.E2EResult
	err    error
	calls  int
}

func (m *mockE2EExecutor) Execute(_ context.Context, _ model.TaskPayload) (*e2e.E2EResult, error) {
	m.calls++
	return m.result, m.err
}

type mockIterateExecutor struct {
	result      *iterate.FixReviewResult
	err         error
	calls       int
	lastPayload model.TaskPayload
}

func (m *mockIterateExecutor) Execute(_ context.Context, payload model.TaskPayload) (*iterate.FixReviewResult, error) {
	m.calls++
	m.lastPayload = payload
	return m.result, m.err
}

type mockProcessorE2EScanner struct {
	modules []string
	err     error
	refs    []string
}

func (m *mockProcessorE2EScanner) ListDir(_ context.Context, _, _, ref, dir string) ([]string, error) {
	m.refs = append(m.refs, ref)
	if m.err != nil {
		return nil, m.err
	}
	if dir == "e2e" {
		return append([]string(nil), m.modules...), nil
	}
	for _, mod := range m.modules {
		if dir == "e2e/"+mod {
			return []string{"cases"}, nil
		}
	}
	return nil, e2e.ErrDirNotFound
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

func TestProcessTask_FixReviewSuccessPersistsIterationState(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:      model.TaskTypeFixReview,
		DeliveryID:    "iterate-7:fix_review:2",
		RepoOwner:     "org",
		RepoName:      "repo",
		RepoFullName:  "org/repo",
		PRNumber:      42,
		SessionID:     7,
		RoundNumber:   2,
		FixReportPath: "docs/review_history/42-round2.md",
	}
	now := time.Now()
	record := &model.TaskRecord{
		ID:           "fix-review-task-2",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: payload.RepoFullName,
		PRNumber:     payload.PRNumber,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)
	s.iterationSession = &store.IterationSessionRecord{
		ID:               7,
		RepoFullName:     "org/repo",
		PRNumber:         42,
		Status:           "fixing",
		TotalIssuesFixed: 1,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          99,
		SessionID:   7,
		RoundNumber: 2,
		FixTaskID:   record.ID,
	}
	s.completedIterationRounds = []*store.IterationRoundRecord{
		{
			ID:          98,
			SessionID:   7,
			RoundNumber: 1,
			IssuesFixed: 1,
			FixSummary:  "fixed https://secret.example/token",
			CompletedAt: &now,
		},
	}
	iterExec := &mockIterateExecutor{
		result: &iterate.FixReviewResult{
			RawOutput: "raw-fix-review-output",
			Output: &iterate.FixReviewOutput{
				Fixes: []iterate.FixItem{
					{Action: "modified"},
					{Action: "skipped"},
					{Action: "alternative_chosen"},
				},
				Summary: "fixed",
			},
		},
	}
	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithIterateService(iterExec))

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}

	if iterExec.calls != 1 {
		t.Fatalf("iterate Execute 调用次数 = %d, want 1", iterExec.calls)
	}
	if !strings.Contains(iterExec.lastPayload.PreviousFixes, "[link-redacted]") {
		t.Fatalf("PreviousFixes 未重新构建或未脱敏: %q", iterExec.lastPayload.PreviousFixes)
	}
	if s.updateIterationRoundCalls != 1 {
		t.Fatalf("UpdateIterationRound 调用次数 = %d, want 1", s.updateIterationRoundCalls)
	}
	if s.latestIterationRound.IssuesFixed != 2 {
		t.Fatalf("IssuesFixed = %d, want 2", s.latestIterationRound.IssuesFixed)
	}
	if s.latestIterationRound.CompletedAt == nil {
		t.Fatal("CompletedAt 应被设置")
	}
	if s.latestIterationRound.FixReportPath != payload.FixReportPath {
		t.Fatalf("FixReportPath = %q, want %q", s.latestIterationRound.FixReportPath, payload.FixReportPath)
	}
	if strings.Contains(s.latestIterationRound.FixSummary, "https://") {
		t.Fatalf("FixSummary 应脱敏，实际: %q", s.latestIterationRound.FixSummary)
	}
	if s.iterationSession.TotalIssuesFixed != 3 {
		t.Fatalf("TotalIssuesFixed = %d, want 3", s.iterationSession.TotalIssuesFixed)
	}
	if s.iterationSession.Status != "reviewing" {
		t.Fatalf("session status = %q, want reviewing", s.iterationSession.Status)
	}
}

func TestProcessTask_FixReviewParseFailureRecordsZeroFixAndNotifies(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:           model.TaskTypeFixReview,
		DeliveryID:         "iterate-8:fix_review:1",
		RepoOwner:          "org",
		RepoName:           "repo",
		RepoFullName:       "org/repo",
		PRNumber:           42,
		SessionID:          8,
		RoundNumber:        1,
		IterationMaxRounds: 3,
	}
	now := time.Now()
	record := &model.TaskRecord{
		ID:           "fix-review-parse-failure",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: payload.RepoFullName,
		PRNumber:     payload.PRNumber,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)
	s.iterationSession = &store.IterationSessionRecord{
		ID:           8,
		RepoFullName: "org/repo",
		PRNumber:     42,
		Status:       "fixing",
		MaxRounds:    3,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          101,
		SessionID:   8,
		RoundNumber: 1,
		FixTaskID:   record.ID,
	}
	iterExec := &mockIterateExecutor{
		result: &iterate.FixReviewResult{RawOutput: "not-json"},
		err:    fmt.Errorf("%w: bad json", iterate.ErrFixReviewParseFailure),
	}
	p := NewProcessor(&mockPoolRunner{}, s, notifier, slog.Default(), WithIterateService(iterExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("error = %v, want SkipRetry", err)
	}
	if s.latestIterationRound.IssuesFixed != 0 {
		t.Fatalf("IssuesFixed = %d, want 0", s.latestIterationRound.IssuesFixed)
	}
	if s.latestIterationRound.CompletedAt == nil {
		t.Fatal("解析失败应记录轮次完成时间")
	}
	if s.iterationSession.Status != "reviewing" {
		t.Fatalf("session status = %q, want reviewing", s.iterationSession.Status)
	}
	if len(notifier.messages) != 1 || notifier.messages[0].EventType != notify.EventIterationError {
		t.Fatalf("应发送 iteration.error 通知，messages=%v", notifier.messages)
	}
}

func TestProcessTask_FixReviewInfrastructureFailureNotifies(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:           model.TaskTypeFixReview,
		DeliveryID:         "iterate-9:fix_review:1",
		RepoOwner:          "org",
		RepoName:           "repo",
		RepoFullName:       "org/repo",
		PRNumber:           42,
		SessionID:          9,
		RoundNumber:        1,
		IterationMaxRounds: 3,
	}
	now := time.Now()
	record := &model.TaskRecord{
		ID:           "fix-review-infra-failure",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: payload.RepoFullName,
		PRNumber:     payload.PRNumber,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)
	s.iterationSession = &store.IterationSessionRecord{
		ID:           9,
		RepoFullName: "org/repo",
		PRNumber:     42,
		Status:       "fixing",
		MaxRounds:    3,
	}
	iterExec := &mockIterateExecutor{err: errors.New("docker failed")}
	p := NewProcessor(&mockPoolRunner{}, s, notifier, slog.Default(), WithIterateService(iterExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("基础设施失败应返回 error")
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("notification count = %d, want 1", len(notifier.messages))
	}
	msg := notifier.messages[0]
	if msg.EventType != notify.EventIterationError {
		t.Fatalf("event = %q, want %q", msg.EventType, notify.EventIterationError)
	}
	if msg.Metadata[notify.MetaKeyIterationSessionID] != "9" {
		t.Fatalf("iteration_session_id = %q, want 9", msg.Metadata[notify.MetaKeyIterationSessionID])
	}
	if s.iterationSession.Status != "idle" {
		t.Fatalf("session status = %q, want idle", s.iterationSession.Status)
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

// TestProcessTask_GenTests_SendsStartAndDoneNotification M4.2：gen_tests 成功路径
// 应发送 2 条通知（Start + Done）。
func TestProcessTask_GenTests_SendsStartAndDoneNotification(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		DeliveryID:   "dlv-gentests-success-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		Module:       "svc/user",
		Framework:    "junit5",
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
	if len(notifier.messages) != 2 {
		t.Fatalf("notification count = %d, want 2 (start + done)", len(notifier.messages))
	}
	if notifier.messages[0].EventType != notify.EventGenTestsStarted {
		t.Errorf("第 1 条通知 event = %q, want %q", notifier.messages[0].EventType, notify.EventGenTestsStarted)
	}
	if notifier.messages[1].EventType != notify.EventGenTestsDone {
		t.Errorf("第 2 条通知 event = %q, want %q", notifier.messages[1].EventType, notify.EventGenTestsDone)
	}
}

func TestProcessTask_GenTests_UsesTestService(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		DeliveryID:   "dlv-gentests-service-1",
		RepoFullName: "org/repo",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-gentests-service",
		TaskType:   model.TaskTypeGenTests,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0, Output: "pool fallback"}}
	testExec := &mockTestExecutor{
		result: &testgen.TestGenResult{
			RawOutput: "service output",
			Output:    &testgen.TestGenOutput{Success: true, InfoSufficient: true},
		},
	}

	p := NewProcessor(pool, s, nil, slog.Default(), WithTestService(testExec))
	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}
	if testExec.calls != 1 {
		t.Fatalf("test service calls = %d, want 1", testExec.calls)
	}
	if pool.calls != 0 {
		t.Fatalf("pool calls = %d, want 0", pool.calls)
	}
	got := s.tasks["proc-task-gentests-service"]
	if got.Result != "service output" {
		t.Fatalf("result = %q, want %q", got.Result, "service output")
	}
}

// TestProcessTask_GenTests_DisabledSkipRetry 验证：当 test.Service 返回
// ErrTestGenDisabled（仓库级 test_gen.enabled=*false）时，Processor 直接标记
// failed 并返回 SkipRetry，避免对已关闭仓库的空转重试。
func TestProcessTask_GenTests_DisabledSkipRetry(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		DeliveryID:   "dlv-gentests-disabled-1",
		RepoFullName: "org/repo",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-gentests-disabled",
		TaskType:   model.TaskTypeGenTests,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	testExec := &mockTestExecutor{
		err: fmt.Errorf("org/repo: %w", testgen.ErrTestGenDisabled),
	}
	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithTestService(testExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("错误应包含 asynq.SkipRetry，实际: %v", err)
	}
	got := s.tasks["proc-task-gentests-disabled"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if !strings.Contains(got.Error, "禁用") {
		t.Errorf("Error 应提及禁用原因，实际: %q", got.Error)
	}
}

func TestProcessTask_GenTests_DeterministicFailureSkipRetry(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		DeliveryID:   "dlv-gentests-skipretry-1",
		RepoFullName: "org/repo",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-gentests-skipretry",
		TaskType:   model.TaskTypeGenTests,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	testExec := &mockTestExecutor{err: testgen.ErrNoFrameworkDetected}
	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithTestService(testExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("错误应包含 asynq.SkipRetry，实际: %v", err)
	}
	got := s.tasks["proc-task-gentests-skipretry"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
}

func TestProcessTask_E2EFailureSkipRetry(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeRunE2E,
		DeliveryID:   "dlv-e2e-failed-1",
		RepoFullName: "org/repo",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-e2e-failed",
		TaskType:   model.TaskTypeRunE2E,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	e2eExec := &mockE2EExecutor{result: &e2e.E2EResult{
		RawOutput: `{"success":false}`,
		Output: &e2e.E2EOutput{
			Success:      false,
			TotalCases:   1,
			PassedCases:  0,
			FailedCases:  1,
			ErrorCases:   0,
			SkippedCases: 0,
			Cases: []e2e.CaseResult{{
				Name:            "checkout",
				Module:          "order",
				Status:          "failed",
				FailureCategory: "bug",
			}},
		},
		DurationMs: 1000,
	}}
	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithE2EService(e2eExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("E2E success=false 应包含 asynq.SkipRetry，实际: %v", err)
	}
	got := s.tasks["proc-task-e2e-failed"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if !strings.Contains(got.Error, "E2E 测试未通过") {
		t.Errorf("Error 应包含 E2E 失败摘要，实际: %q", got.Error)
	}
	if got.Result != `{"success":false}` {
		t.Errorf("Result = %q, want raw E2E output", got.Result)
	}
}

func TestAdaptE2EResult_EnvironmentFailureRetryable(t *testing.T) {
	result := adaptE2EResult(&e2e.E2EResult{
		Output: &e2e.E2EOutput{
			Success:      false,
			TotalCases:   1,
			FailedCases:  1,
			PassedCases:  0,
			ErrorCases:   0,
			SkippedCases: 0,
			Cases: []e2e.CaseResult{{
				Name:            "login",
				Module:          "auth",
				Status:          "failed",
				FailureCategory: "environment",
			}},
		},
	})
	if result.ExitCode != 1 {
		t.Fatalf("environment 类 E2E 失败应保留可重试 exit code 1，实际: %d", result.ExitCode)
	}
}

func TestProcessTask_GenTests_InfoInsufficientPreservesStructuredResult(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		DeliveryID:   "dlv-gentests-info-1",
		RepoFullName: "org/repo",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-gentests-info",
		TaskType:   model.TaskTypeGenTests,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	rawOutput := `{"type":"result","result":"{\"success\":false,\"info_sufficient\":false,\"missing_info\":[\"缺少仓库上下文\"]}"}`
	testExec := &mockTestExecutor{
		result: &testgen.TestGenResult{
			RawOutput: rawOutput,
			Output: &testgen.TestGenOutput{
				Success:        false,
				InfoSufficient: false,
				MissingInfo:    []string{"缺少仓库上下文"},
			},
		},
		err: fmt.Errorf("org/repo: %w", testgen.ErrInfoInsufficient),
	}
	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithTestService(testExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("错误应包含 asynq.SkipRetry，实际: %v", err)
	}

	got := s.tasks["proc-task-gentests-info"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if got.Result != rawOutput {
		t.Fatalf("result = %q, want %q", got.Result, rawOutput)
	}
	if !strings.Contains(got.Result, "missing_info") {
		t.Fatalf("应保留结构化失败结果，实际: %q", got.Result)
	}
}

// TestProcessTask_GenTests_ModuleNotFoundSkipRetry 覆盖 test.ErrModuleNotFound
// 分发分支：module 子路径不存在于仓库时应标记 failed + SkipRetry。
// 这一 sentinel 在 M4.1 的 Processor 增加分发后，与 ErrModuleOutOfScope 等对齐。
func TestProcessTask_GenTests_ModuleNotFoundSkipRetry(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		DeliveryID:   "dlv-gentests-notfound-1",
		RepoFullName: "org/repo",
		Module:       "nonexistent/module",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-gentests-notfound",
		TaskType:   model.TaskTypeGenTests,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	testExec := &mockTestExecutor{err: testgen.ErrModuleNotFound}
	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithTestService(testExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("错误应包含 asynq.SkipRetry，实际: %v", err)
	}
	got := s.tasks["proc-task-gentests-notfound"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if !strings.Contains(got.Error, "module") {
		t.Errorf("Error 应提及 module，实际: %q", got.Error)
	}
}

func TestProcessTask_GenTests_ParseFailureCallsWriteDegraded(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		DeliveryID:   "dlv-gentests-parsefail-1",
		RepoFullName: "org/repo",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-gentests-parsefail",
		TaskType:   model.TaskTypeGenTests,
		Status:     model.TaskStatusQueued,
		Payload:    payload,
		DeliveryID: payload.DeliveryID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	seedRecord(s, record)

	testExec := &mockTestExecutor{
		result: &testgen.TestGenResult{
			RawOutput:  "bad output",
			ParseError: errors.New("json parse failed"),
		},
		err: testgen.ErrTestGenParseFailure,
	}
	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithTestService(testExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("ProcessTask 应返回错误")
	}
	if testExec.writeDegradedCalls != 1 {
		t.Fatalf("writeDegradedCalls = %d, want 1", testExec.writeDegradedCalls)
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
	p.sendCompletionNotification(context.Background(), record, nil, nil, nil, nil, nil)
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

func TestProcessTask_ReviewRequestChangesFilteredOutDoesNotIterate(t *testing.T) {
	s := newMockStore()
	enqueuer := &mockEnqueuer{enqueuedID: "asynq-filtered-iterate"}
	enqueueHandler := NewEnqueueHandler(enqueuer, nil, s, slog.Default(),
		WithIterateStore(s),
		WithIterateConfig(&mockIterateConfigProvider{cfg: config.IterateConfig{
			Enabled:              true,
			MaxRounds:            3,
			Label:                "auto-iterate",
			NotificationMode:     "progress",
			FixSeverityThreshold: "error",
			ReportPath:           "docs/review_history",
		}}),
	)
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "dlv-filtered-iterate",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		PRNumber:     77,
		HeadRef:      "feature/docs",
	}
	now := time.Now()
	record := &model.TaskRecord{
		ID:           "review-filtered-iterate",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: payload.RepoFullName,
		PRNumber:     payload.PRNumber,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)

	reviewExec := &mockReviewExecutor{
		cfg: review.ReviewConfig{IgnorePatterns: []string{"docs/**"}},
		result: &review.ReviewResult{
			RawOutput: "filtered review",
			Labels:    []string{"auto-iterate"},
			Review: &review.ReviewOutput{
				Summary: "docs issue",
				Verdict: review.VerdictRequestChanges,
				Issues: []review.ReviewIssue{
					{File: "docs/guide.md", Severity: "ERROR", Message: "ignored docs issue"},
				},
			},
		},
	}
	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(),
		WithReviewService(reviewExec),
		WithEnqueueHandler(enqueueHandler))

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}
	if len(enqueuer.payloads) != 0 {
		t.Fatalf("过滤后不应入队 fix_review，实际 payloads=%v", enqueuer.payloads)
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

func TestProcessTask_FixExitCode2_SkipRetry(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "dlv-fix-exitcode2",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  9,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-fix-exitcode2",
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
			RawOutput: "bad args",
			ExitCode:  2,
		},
	}

	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithFixService(fixExec))
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("fix 退出码 2 应返回错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("fix 退出码 2 应包含 SkipRetry，实际: %v", err)
	}

	got := s.tasks["proc-task-fix-exitcode2"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusFailed)
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
	_, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil)
	if ok {
		t.Error("RepoOwner 为空时不应生成通知消息")
	}
}

func TestBuildNotificationMessage_NilRecord(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default())
	_, ok := p.buildNotificationMessage(nil, nil, nil, nil, nil, nil)
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
	_, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil)
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

func TestProcessTask_FixParseFailure_PersistsRawOutputAndWritesDegraded(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "dlv-fix-parse-failed",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  12,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-task-fix-parse-failed",
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
			RawOutput:  "raw fix output",
			ParseError: errors.New("bad fix json"),
		},
		err: fmt.Errorf("%w: bad fix json", fix.ErrFixParseFailure),
	}

	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithFixService(fixExec))
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("解析失败应返回错误")
	}
	if errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("解析失败不应 SkipRetry，实际: %v", err)
	}
	if fixExec.writeDegradedCalls != 1 {
		t.Fatalf("WriteDegraded 调用次数 = %d, want 1", fixExec.writeDegradedCalls)
	}

	got := s.tasks["proc-task-fix-parse-failed"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if got.Result != "raw fix output" {
		t.Fatalf("result = %q, want %q", got.Result, "raw fix output")
	}
	if !strings.Contains(got.Error, fix.ErrFixParseFailure.Error()) {
		t.Fatalf("error = %q，应包含 %q", got.Error, fix.ErrFixParseFailure.Error())
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

func (c *cancelPoolRunner) RunWithCommandAndStdin(_ context.Context, _ model.TaskPayload, _ []string, _ []byte) (*worker.ExecutionResult, error) {
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

func TestBuildPRMetadataAndTarget_SkipPRFieldsWithoutNumber(t *testing.T) {
	p := &Processor{giteaBaseURL: "https://gitea.example.com"}
	payload := model.TaskPayload{
		RepoOwner: "org",
		RepoName:  "repo",
		PRTitle:   "缺少 PR 编号的回归分析",
	}

	metadata := p.buildPRMetadata(payload)
	if metadata[notify.MetaKeyPRURL] != "" {
		t.Errorf("pr_url = %q, want empty", metadata[notify.MetaKeyPRURL])
	}
	if metadata[notify.MetaKeyPRTitle] != "缺少 PR 编号的回归分析" {
		t.Errorf("pr_title = %q, want 缺少 PR 编号的回归分析", metadata[notify.MetaKeyPRTitle])
	}

	target := buildPRTarget(payload)
	if target.IsPR {
		t.Errorf("target.IsPR = true, want false")
	}
	if target.Number != 0 {
		t.Errorf("target.Number = %d, want 0", target.Number)
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

// TestProcessTask_AnalyzeIssue_WithService 验证 analyze_issue 任务走 fixService.Execute 路径。
func TestProcessTask_AnalyzeIssue_WithService(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeAnalyzeIssue,
		DeliveryID:   "dlv-analyze-svc-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  5,
		IssueTitle:   "bug report",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-analyze-svc-1",
		TaskType:   model.TaskTypeAnalyzeIssue,
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

	got := s.tasks["proc-analyze-svc-1"]
	if got.Status != model.TaskStatusSucceeded {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
	}
}

func TestProcessTask_AnalyzeIssue_WithService_WritebackErrorFailsTask(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeAnalyzeIssue,
		DeliveryID:   "dlv-analyze-svc-writeback-failed-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  5,
		IssueTitle:   "bug report",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-analyze-svc-writeback-failed-1",
		TaskType:   model.TaskTypeAnalyzeIssue,
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

	got := s.tasks["proc-analyze-svc-writeback-failed-1"]
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

// TestProcessTask_FixIssue_WithService_StillUsesPoolRun 已被 Task 11 (M3.5) 反转：
// fix_issue + fixService 注入后，应路由到 fixService.Execute，不再走 pool.Run。
// 该测试重命名为 TestProcessTask_FixIssue_WithService_RoutesToFixService（见下）。

// TestProcessor_RouteFixIssueToFixService 验证 fix_issue 任务在注入 fixService 后
// 路由到 fixService.Execute，不再走 pool.Run（M3.5 Task 11）。
func TestProcessor_RouteFixIssueToFixService(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "dlv-fix-route-svc-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  7,
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:         "proc-fix-route-svc-1",
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
			RawOutput: `{"type":"result","subtype":"success","is_error":false}`,
			CLIMeta:   &model.CLIMeta{IsError: false, DurationMs: 5000},
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

	got := s.tasks["proc-fix-route-svc-1"]
	if got.Status != model.TaskStatusSucceeded {
		t.Errorf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
	}
}

// TestProcessTask_FixIssue_InfoInsufficient_SkipsRetry 验证 fix_issue 收到
// ErrInfoInsufficient 时返回 SkipRetry（M3.5 Task 11）。
func TestProcessTask_FixIssue_InfoInsufficient_SkipsRetry(t *testing.T) {
	s := newMockStore()
	fixExec := &mockFixExecutor{
		err: fmt.Errorf("Issue #7: %w", fix.ErrInfoInsufficient),
	}
	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0}}
	notifier := &stubNotifier{}
	p := NewProcessor(pool, s, notifier, slog.Default(), WithFixService(fixExec))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "delivery-fix-info-insufficient",
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  7,
	}
	task := buildAsynqTask(t, payload)
	record := &model.TaskRecord{
		ID:         "task-fix-info-insufficient",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		DeliveryID: payload.DeliveryID,
	}
	seedRecord(s, record)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("应返回 SkipRetry 错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("错误应包含 asynq.SkipRetry，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "信息不足") {
		t.Errorf("错误信息应包含'信息不足'，实际: %v", err)
	}
	updated := s.tasks["task-fix-info-insufficient"]
	if updated.Status != model.TaskStatusFailed {
		t.Errorf("状态应为 failed，实际: %s", updated.Status)
	}
}

// TestProcessTask_FixIssue_FixFailed_SkipsRetry 验证 fix_issue 收到
// ErrFixFailed 时返回 SkipRetry（M3.5 Task 11）。
func TestProcessTask_FixIssue_FixFailed_SkipsRetry(t *testing.T) {
	s := newMockStore()
	fixExec := &mockFixExecutor{
		err: fmt.Errorf("Issue #7: %w", fix.ErrFixFailed),
	}
	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0}}
	notifier := &stubNotifier{}
	p := NewProcessor(pool, s, notifier, slog.Default(), WithFixService(fixExec))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "delivery-fix-failed-skip",
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  7,
	}
	task := buildAsynqTask(t, payload)
	record := &model.TaskRecord{
		ID:         "task-fix-failed-skip",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		DeliveryID: payload.DeliveryID,
	}
	seedRecord(s, record)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("应返回 SkipRetry 错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("错误应包含 asynq.SkipRetry，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "跳过重试") {
		t.Errorf("错误信息应包含'跳过重试'，实际: %v", err)
	}
	updated := s.tasks["task-fix-failed-skip"]
	if updated.Status != model.TaskStatusFailed {
		t.Errorf("状态应为 failed，实际: %s", updated.Status)
	}
}

func TestProcessTask_AnalyzeIssue_MissingRef_SkipsRetry(t *testing.T) {
	s := newMockStore()
	fixExec := &mockFixExecutor{
		err: fmt.Errorf("Issue #10: %w", fix.ErrMissingIssueRef),
	}
	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0}}
	notifier := &stubNotifier{}
	p := NewProcessor(pool, s, notifier, slog.Default(), WithFixService(fixExec))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeAnalyzeIssue,
		DeliveryID:   "delivery-miss-ref",
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
	}
	task := buildAsynqTask(t, payload)
	record := &model.TaskRecord{
		ID:         "task-miss-ref",
		TaskType:   model.TaskTypeAnalyzeIssue,
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

func TestProcessTask_AnalyzeIssue_InvalidRef_SkipsRetry(t *testing.T) {
	s := newMockStore()
	fixExec := &mockFixExecutor{
		err: fmt.Errorf("Issue #10 ref=bad: %w", fix.ErrInvalidIssueRef),
	}
	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0}}
	notifier := &stubNotifier{}
	p := NewProcessor(pool, s, notifier, slog.Default(), WithFixService(fixExec))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeAnalyzeIssue,
		DeliveryID:   "delivery-bad-ref",
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
	}
	task := buildAsynqTask(t, payload)
	record := &model.TaskRecord{
		ID:         "task-bad-ref",
		TaskType:   model.TaskTypeAnalyzeIssue,
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

func TestProcessTask_AnalyzeIssue_RefHintCommentFailure_Retries(t *testing.T) {
	s := newMockStore()
	commentErr := errors.New("Gitea API 500")
	fixExec := &mockFixExecutor{
		err: fmt.Errorf("Issue #10: 回写 ref 缺失提示失败: %w", commentErr),
	}
	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0}}
	notifier := &stubNotifier{}
	p := NewProcessor(pool, s, notifier, slog.Default(), WithFixService(fixExec))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeAnalyzeIssue,
		DeliveryID:   "delivery-ref-comment-failed",
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
	}
	task := buildAsynqTask(t, payload)
	record := &model.TaskRecord{
		ID:         "task-ref-comment-failed",
		TaskType:   model.TaskTypeAnalyzeIssue,
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

const notifyTimeLayout = "2006-01-02 15:04:05"

func assertNotifyTimeInShanghai(t *testing.T, got, before, after string) {
	t.Helper()
	if len(got) != 19 {
		t.Errorf("notify_time = %q, length = %d, want 19", got, len(got))
	}
	if _, err := time.ParseInLocation(notifyTimeLayout, got, shanghaiZone); err != nil {
		t.Errorf("notify_time = %q, 无法按 Asia/Shanghai 解析: %v", got, err)
	}
	if got != before && got != after {
		t.Errorf("notify_time = %q, want Asia/Shanghai time between %q and %q", got, before, after)
	}
}

func TestFormatNotifyTime(t *testing.T) {
	before := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	result := formatNotifyTime()
	after := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	assertNotifyTimeInShanghai(t, result, before, after)
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input time.Duration
		want  string
	}{
		{32 * time.Second, "32s"},
		{2*time.Minute + 30*time.Second, "2m30s"},
		{1*time.Hour + 5*time.Minute + 30*time.Second, "1h5m30s"},
		{500 * time.Millisecond, "0s"},
		{0, "0s"},
	}
	for _, tc := range tests {
		got := formatDuration(tc.input)
		if got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestBuildNotificationMessage_FixIssue_SuccessInjectsPRMetadata 验证 fix_issue succeeded
// 时，fixResult 中的 PRNumber/PRURL/ModifiedFiles 被注入到通知 Metadata（M3.5 Task 12）。
func TestBuildNotificationMessage_FixIssue_SuccessInjectsPRMetadata(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	startedAt := time.Now().Add(-10 * time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:       "fix-pr-meta-success",
		TaskType: model.TaskTypeFixIssue,
		Status:   model.TaskStatusSucceeded,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeFixIssue,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			IssueNumber:  10,
		},
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
	}

	fixResult := &fix.FixResult{
		PRNumber: 42,
		PRURL:    "https://gitea.example.com/org/repo/pulls/42",
		Fix: &fix.FixOutput{
			ModifiedFiles: []string{"src/a.go", "src/b.go"},
		},
	}

	msg, ok := p.buildNotificationMessage(record, nil, fixResult, nil, nil, nil)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	if msg.Metadata[notify.MetaKeyPRURL] != fixResult.PRURL {
		t.Errorf("MetaKeyPRURL = %q, want %q", msg.Metadata[notify.MetaKeyPRURL], fixResult.PRURL)
	}
	if msg.Metadata[notify.MetaKeyPRNumber] != "42" {
		t.Errorf("MetaKeyPRNumber = %q, want %q", msg.Metadata[notify.MetaKeyPRNumber], "42")
	}
	if msg.Metadata[notify.MetaKeyModifiedFiles] != "2" {
		t.Errorf("MetaKeyModifiedFiles = %q, want %q", msg.Metadata[notify.MetaKeyModifiedFiles], "2")
	}
}

// TestBuildNotificationMessage_FixIssue_FailureOmitsPRMetadata 验证 fix_issue failed
// 时，PR 元数据不被注入（M3.5 Task 12）。
func TestBuildNotificationMessage_FixIssue_FailureOmitsPRMetadata(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	record := &model.TaskRecord{
		ID:       "fix-pr-meta-failed",
		TaskType: model.TaskTypeFixIssue,
		Status:   model.TaskStatusFailed,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeFixIssue,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			IssueNumber:  10,
		},
	}

	fixResult := &fix.FixResult{
		PRNumber: 42,
		PRURL:    "https://gitea.example.com/org/repo/pulls/42",
		Fix: &fix.FixOutput{
			ModifiedFiles: []string{"src/a.go"},
		},
	}

	msg, ok := p.buildNotificationMessage(record, nil, fixResult, nil, nil, nil)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	if msg.Metadata[notify.MetaKeyPRNumber] != "" {
		t.Errorf("failed 通知不应包含 MetaKeyPRNumber，got %q", msg.Metadata[notify.MetaKeyPRNumber])
	}
	if msg.Metadata[notify.MetaKeyModifiedFiles] != "" {
		t.Errorf("failed 通知不应包含 MetaKeyModifiedFiles，got %q", msg.Metadata[notify.MetaKeyModifiedFiles])
	}
}

func TestBuildStartMessage_ReviewPR_HasNotifyTime(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     42,
	}

	before := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	msg, ok := p.buildStartMessage(payload)
	after := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	if !ok {
		t.Fatal("buildStartMessage 应返回 true")
	}
	notifyTime := msg.Metadata[notify.MetaKeyNotifyTime]
	if notifyTime == "" {
		t.Error("开始通知应包含 notify_time")
	}
	assertNotifyTimeInShanghai(t, notifyTime, before, after)
}

func TestBuildStartMessage_FixIssue_HasNotifyTime(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  10,
	}

	before := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	msg, ok := p.buildStartMessage(payload)
	after := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	if !ok {
		t.Fatal("buildStartMessage 应返回 true")
	}
	notifyTime := msg.Metadata[notify.MetaKeyNotifyTime]
	if notifyTime == "" {
		t.Error("FixIssue 开始通知应包含 notify_time")
	}
	assertNotifyTimeInShanghai(t, notifyTime, before, after)
}

func TestBuildNotificationMessage_Succeeded_HasNotifyTimeAndDuration(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	startedAt := time.Now().Add(-32 * time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:       "time-succeeded",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusSucceeded,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     42,
		},
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
	}

	before := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil)
	after := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	notifyTime := msg.Metadata[notify.MetaKeyNotifyTime]
	if notifyTime == "" {
		t.Error("succeeded 通知应包含 notify_time")
	}
	assertNotifyTimeInShanghai(t, notifyTime, before, after)

	duration := msg.Metadata[notify.MetaKeyDuration]
	if duration == "" {
		t.Error("succeeded 通知应包含 duration")
	}
	if duration != "32s" {
		t.Errorf("duration = %q, want %q", duration, "32s")
	}
}

func TestBuildNotificationMessage_Failed_HasNotifyTimeNoDuration(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	startedAt := time.Now().Add(-10 * time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:       "time-failed",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusFailed,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     42,
		},
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
	}

	before := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil)
	after := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	notifyTime := msg.Metadata[notify.MetaKeyNotifyTime]
	if notifyTime == "" {
		t.Error("failed 通知应包含 notify_time")
	}
	assertNotifyTimeInShanghai(t, notifyTime, before, after)
	if msg.Metadata[notify.MetaKeyDuration] != "" {
		t.Errorf("failed 通知不应包含 duration，got %q", msg.Metadata[notify.MetaKeyDuration])
	}
}

func TestBuildNotificationMessage_Retrying_HasNotifyTimeNoDuration(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	startedAt := time.Now().Add(-5 * time.Second)
	record := &model.TaskRecord{
		ID:       "time-retrying",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusRetrying,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     42,
		},
		RetryCount: 1,
		MaxRetry:   3,
		StartedAt:  &startedAt,
	}

	before := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil)
	after := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	notifyTime := msg.Metadata[notify.MetaKeyNotifyTime]
	if notifyTime == "" {
		t.Error("retrying 通知应包含 notify_time")
	}
	assertNotifyTimeInShanghai(t, notifyTime, before, after)
	if msg.Metadata[notify.MetaKeyDuration] != "" {
		t.Errorf("retrying 通知不应包含 duration（CompletedAt 为 nil），got %q", msg.Metadata[notify.MetaKeyDuration])
	}
}

func TestBuildNotificationMessage_FixIssue_Succeeded_HasDuration(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	startedAt := time.Now().Add(-1*time.Minute - 30*time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:       "time-fix-succeeded",
		TaskType: model.TaskTypeFixIssue,
		Status:   model.TaskStatusSucceeded,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeFixIssue,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			IssueNumber:  10,
		},
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
	}

	before := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil)
	after := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	notifyTime := msg.Metadata[notify.MetaKeyNotifyTime]
	if notifyTime == "" {
		t.Error("FixIssue succeeded 通知应包含 notify_time")
	}
	assertNotifyTimeInShanghai(t, notifyTime, before, after)
	if msg.Metadata[notify.MetaKeyDuration] == "" {
		t.Error("FixIssue succeeded 通知应包含 duration")
	}
}

func TestBuildNotificationMessage_FixIssue_Failed_NoDuration(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	startedAt := time.Now().Add(-10 * time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:       "time-fix-failed",
		TaskType: model.TaskTypeFixIssue,
		Status:   model.TaskStatusFailed,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeFixIssue,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			IssueNumber:  10,
		},
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
	}

	before := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil)
	after := time.Now().In(shanghaiZone).Format(notifyTimeLayout)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	notifyTime := msg.Metadata[notify.MetaKeyNotifyTime]
	if notifyTime == "" {
		t.Error("FixIssue failed 通知应包含 notify_time")
	}
	assertNotifyTimeInShanghai(t, notifyTime, before, after)
	if msg.Metadata[notify.MetaKeyDuration] != "" {
		t.Errorf("FixIssue failed 通知不应包含 duration，got %q", msg.Metadata[notify.MetaKeyDuration])
	}
}

// ==========================================================================
// M4.2 gen_tests 通知路径测试
// ==========================================================================

// TestBuildStartMessage_GenTests_FailOpen：RepoFullName 空 → 不发；非空 → 发。
func TestBuildStartMessage_GenTests_FailOpen(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default())

	// 非空：返回 Start 消息
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		Framework:    "junit5",
	}
	msg, ok := p.buildStartMessage(payload)
	if !ok {
		t.Fatal("RepoFullName 非空应返回 true")
	}
	if msg.EventType != notify.EventGenTestsStarted {
		t.Errorf("event = %q, want %q", msg.EventType, notify.EventGenTestsStarted)
	}
	if msg.Metadata[notify.MetaKeyModule] != "all" {
		t.Errorf("空 module 应显示 all，实际 %q", msg.Metadata[notify.MetaKeyModule])
	}
	if msg.Metadata[notify.MetaKeyFramework] != "junit5" {
		t.Errorf("framework metadata = %q, want junit5", msg.Metadata[notify.MetaKeyFramework])
	}

	// RepoFullName 空（实际场景少见，但 RepoOwner/RepoName 空会早退）：不发
	empty := model.TaskPayload{TaskType: model.TaskTypeGenTests}
	if _, ok := p.buildStartMessage(empty); ok {
		t.Error("空 payload 应返回 false")
	}
}

// TestBuildNotificationMessage_GenTests_SucceededMetadata：Succeeded 路径
// 应回填 pr_url/pr_number/generated_count/committed_count/skipped_count 等。
func TestBuildNotificationMessage_GenTests_SucceededMetadata(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default())

	startedAt := time.Now().Add(-30 * time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:          "gen-task-done",
		TaskType:    model.TaskTypeGenTests,
		Status:      model.TaskStatusSucceeded,
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeGenTests,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			Module:       "svc/user",
			Framework:    "junit5",
		},
	}
	tr := &testgen.TestGenResult{
		PRNumber: 99,
		PRURL:    "https://gitea.example.com/org/repo/pulls/99",
		Output: &testgen.TestGenOutput{
			Success:        true,
			InfoSufficient: true,
			GeneratedFiles: []testgen.GeneratedFile{
				{Path: "a_test.go", Operation: "create"},
				{Path: "b_test.go", Operation: "create"},
				{Path: "c_test.go", Operation: "create"},
			},
			CommittedFiles: []string{"a_test.go", "b_test.go"},
			SkippedTargets: []testgen.SkippedTarget{{Path: "c", Reason: "time_budget_exhausted"}},
		},
	}

	msg, ok := p.buildNotificationMessage(record, nil, nil, tr, nil, nil)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}
	if msg.EventType != notify.EventGenTestsDone {
		t.Errorf("event = %q, want %q", msg.EventType, notify.EventGenTestsDone)
	}
	if msg.Severity != notify.SeverityInfo {
		t.Errorf("severity = %q, want %q", msg.Severity, notify.SeverityInfo)
	}
	if msg.Metadata[notify.MetaKeyPRURL] != tr.PRURL {
		t.Errorf("pr_url = %q, want %q", msg.Metadata[notify.MetaKeyPRURL], tr.PRURL)
	}
	if msg.Metadata[notify.MetaKeyPRNumber] != "99" {
		t.Errorf("pr_number = %q, want 99", msg.Metadata[notify.MetaKeyPRNumber])
	}
	if msg.Metadata[notify.MetaKeyGeneratedCount] != "3" {
		t.Errorf("generated_count = %q, want 3", msg.Metadata[notify.MetaKeyGeneratedCount])
	}
	if msg.Metadata[notify.MetaKeyCommittedCount] != "2" {
		t.Errorf("committed_count = %q, want 2", msg.Metadata[notify.MetaKeyCommittedCount])
	}
	if msg.Metadata[notify.MetaKeySkippedCount] != "1" {
		t.Errorf("skipped_count = %q, want 1", msg.Metadata[notify.MetaKeySkippedCount])
	}
	if msg.Metadata[notify.MetaKeyModule] != "svc/user" {
		t.Errorf("module = %q, want svc/user", msg.Metadata[notify.MetaKeyModule])
	}
	if msg.Metadata[notify.MetaKeyFramework] != "junit5" {
		t.Errorf("framework = %q, want junit5", msg.Metadata[notify.MetaKeyFramework])
	}
}

// TestBuildNotificationMessage_GenTests_FailureCategorySeverity：三态 severity 映射。
func TestBuildNotificationMessage_GenTests_FailureCategorySeverity(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default())

	cases := []struct {
		name      string
		category  testgen.FailureCategory
		wantSev   notify.Severity
		wantTitle string
	}{
		{"infrastructure→Warning", testgen.FailureCategoryInfrastructure, notify.SeverityWarning, "基础设施故障"},
		{"test_quality→Info", testgen.FailureCategoryTestQuality, notify.SeverityInfo, "测试质量未达标"},
		{"info_insufficient→Info", testgen.FailureCategoryInfoInsufficient, notify.SeverityInfo, "生成信息不足"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := &model.TaskRecord{
				ID:       "gen-task-fail",
				TaskType: model.TaskTypeGenTests,
				Status:   model.TaskStatusFailed,
				Payload: model.TaskPayload{
					TaskType:     model.TaskTypeGenTests,
					RepoOwner:    "org",
					RepoName:     "repo",
					RepoFullName: "org/repo",
					Module:       "svc/user",
				},
			}
			output := &testgen.TestGenOutput{
				Success:         false,
				FailureCategory: tc.category,
			}
			if tc.category == testgen.FailureCategoryInfoInsufficient {
				output.InfoSufficient = false
			} else {
				output.InfoSufficient = true
			}
			tr := &testgen.TestGenResult{Output: output}
			msg, ok := p.buildNotificationMessage(record, nil, nil, tr, nil, nil)
			if !ok {
				t.Fatal("buildNotificationMessage 应返回 true")
			}
			if msg.EventType != notify.EventGenTestsFailed {
				t.Errorf("event = %q, want %q", msg.EventType, notify.EventGenTestsFailed)
			}
			if msg.Severity != tc.wantSev {
				t.Errorf("severity = %q, want %q", msg.Severity, tc.wantSev)
			}
			if msg.Title != tc.wantTitle {
				t.Errorf("title = %q, want %q", msg.Title, tc.wantTitle)
			}
			if msg.Metadata[notify.MetaKeyFailureCategory] != string(tc.category) {
				t.Errorf("failure_category = %q, want %q",
					msg.Metadata[notify.MetaKeyFailureCategory], string(tc.category))
			}
		})
	}
}

// TestBuildNotificationMessage_GenTests_Retrying：Retrying 路径走 EventSystemError + Warning。
func TestBuildNotificationMessage_GenTests_Retrying(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default())
	record := &model.TaskRecord{
		ID:         "gen-task-retrying",
		TaskType:   model.TaskTypeGenTests,
		Status:     model.TaskStatusRetrying,
		RetryCount: 1,
		MaxRetry:   3,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeGenTests,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			Module:       "svc/user",
		},
	}
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}
	if msg.EventType != notify.EventSystemError {
		t.Errorf("event = %q, want %q", msg.EventType, notify.EventSystemError)
	}
	if msg.Severity != notify.SeverityWarning {
		t.Errorf("severity = %q, want %q", msg.Severity, notify.SeverityWarning)
	}
	if msg.Metadata[notify.MetaKeyTaskStatus] != string(model.TaskStatusRetrying) {
		t.Errorf("task_status = %q, want retrying", msg.Metadata[notify.MetaKeyTaskStatus])
	}
}

// TestSendCompletionNotification_GenTests_WarningsAppended：Warnings 非空时
// 在主消息外追加一条 Warning 消息。
func TestSendCompletionNotification_GenTests_WarningsAppended(t *testing.T) {
	notifier := &stubNotifier{}
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), notifier, slog.Default())

	startedAt := time.Now().Add(-30 * time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:          "gen-task-warnings",
		TaskType:    model.TaskTypeGenTests,
		Status:      model.TaskStatusSucceeded,
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeGenTests,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			Module:       "svc/user",
		},
	}
	tr := &testgen.TestGenResult{
		Output: &testgen.TestGenOutput{
			Success:        true,
			InfoSufficient: true,
			Warnings:       []string{"AUTO_TEST_BRANCH_RESET_REMOTE_FAILED"},
		},
	}
	p.sendCompletionNotification(context.Background(), record, nil, nil, tr, nil, nil)
	if len(notifier.messages) != 2 {
		t.Fatalf("应发出 2 条通知（Done + Warnings），实际 %d", len(notifier.messages))
	}
	if notifier.messages[0].EventType != notify.EventGenTestsDone {
		t.Errorf("第 1 条应为 Done，实际 %q", notifier.messages[0].EventType)
	}
	if notifier.messages[1].Severity != notify.SeverityWarning {
		t.Errorf("第 2 条应为 Warning severity，实际 %q", notifier.messages[1].Severity)
	}
	if !strings.Contains(notifier.messages[1].Body, "AUTO_TEST_BRANCH_RESET_REMOTE_FAILED") {
		t.Errorf("第 2 条 body 应包含 warning 内容，实际 %q", notifier.messages[1].Body)
	}
}

func TestSendCompletionNotification_GenTests_SyncsPRComment(t *testing.T) {
	notifier := &stubNotifier{}
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), notifier, slog.Default())

	startedAt := time.Now().Add(-20 * time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:          "gen-task-comment",
		TaskType:    model.TaskTypeGenTests,
		Status:      model.TaskStatusSucceeded,
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeGenTests,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			Module:       "svc/user",
			BaseRef:      "main",
		},
	}
	tr := &testgen.TestGenResult{
		Framework: testgen.FrameworkJUnit5,
		PRNumber:  42,
		Output: &testgen.TestGenOutput{
			Success:            true,
			InfoSufficient:     true,
			VerificationPassed: true,
			BranchName:         "auto-test/svc-user",
			CommitSHA:          "deadbeef",
			GeneratedFiles: []testgen.GeneratedFile{{
				Path:      "svc/user/UserServiceTest.java",
				Operation: "create",
				Framework: "junit5",
				TestCount: 3,
			}},
			CommittedFiles: []string{"svc/user/UserServiceTest.java"},
			TestResults: &testgen.TestRunResults{
				Passed:    3,
				AllPassed: true,
			},
		},
	}

	p.sendCompletionNotification(context.Background(), record, nil, nil, tr, nil, nil)

	if len(notifier.commentCalls) != 1 {
		t.Fatalf("应同步 1 条 PR 评论，实际 %d 条", len(notifier.commentCalls))
	}
	got := notifier.commentCalls[0]
	if got.owner != "org" || got.repo != "repo" || got.pr != 42 {
		t.Fatalf("PR 评论目标错误: %+v", got)
	}
	if !strings.Contains(got.body, "DTWorkflow gen_tests") {
		t.Fatalf("评论正文应包含格式化结果摘要，实际: %q", got.body)
	}
}

func TestSendCompletionNotification_GenTests_RetryingDoesNotSyncPRComment(t *testing.T) {
	notifier := &stubNotifier{}
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), notifier, slog.Default())

	record := &model.TaskRecord{
		ID:       "gen-task-retrying-comment",
		TaskType: model.TaskTypeGenTests,
		Status:   model.TaskStatusRetrying,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeGenTests,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
		},
	}
	tr := &testgen.TestGenResult{
		Framework: testgen.FrameworkJUnit5,
		PRNumber:  42,
		Output: &testgen.TestGenOutput{
			Success:            true,
			InfoSufficient:     true,
			VerificationPassed: true,
			BranchName:         "auto-test/svc-user",
			CommitSHA:          "deadbeef",
			GeneratedFiles: []testgen.GeneratedFile{{
				Path:      "svc/user/UserServiceTest.java",
				Operation: "create",
				Framework: "junit5",
				TestCount: 3,
			}},
			CommittedFiles: []string{"svc/user/UserServiceTest.java"},
			TestResults: &testgen.TestRunResults{
				Passed:    3,
				AllPassed: true,
			},
		},
	}

	p.sendCompletionNotification(context.Background(), record, nil, nil, tr, nil, nil)

	if len(notifier.commentCalls) != 0 {
		t.Fatalf("retrying 阶段不应同步 PR 评论，实际 %d 条", len(notifier.commentCalls))
	}
}

// TestSendCompletionNotification_ReviewStillWorksWithNilTestResult：
// review 路径仍传 nil testResult，行为不变。
func TestSendCompletionNotification_ReviewStillWorksWithNilTestResult(t *testing.T) {
	notifier := &stubNotifier{}
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), notifier, slog.Default())

	startedAt := time.Now().Add(-time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:          "review-task",
		TaskType:    model.TaskTypeReviewPR,
		Status:      model.TaskStatusSucceeded,
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     1,
		},
	}
	p.sendCompletionNotification(context.Background(), record, nil, nil, nil, nil, nil)
	if len(notifier.messages) != 1 {
		t.Fatalf("review 路径应发送 1 条通知，实际 %d", len(notifier.messages))
	}
	if notifier.messages[0].EventType != notify.EventPRReviewDone {
		t.Errorf("event = %q, want %q", notifier.messages[0].EventType, notify.EventPRReviewDone)
	}
}

// TestProcessor_TriageE2E_SuccessWithModules 验证 triage_e2e 成功且输出包含模块时：
// 1. 任务标记为 succeeded
// 2. 链式入队 run_e2e 任务（每个模块一个）
// 3. 发送 E2ETriageStarted + E2ETriageDone 通知
func TestProcessor_TriageE2E_SuccessWithModules(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:       model.TaskTypeTriageE2E,
		DeliveryID:     "dlv-triage-success-1",
		RepoOwner:      "org",
		RepoName:       "repo",
		RepoFullName:   "org/repo",
		CloneURL:       "https://gitea.example.com/org/repo.git",
		PRNumber:       42,
		PRTitle:        "重构登录流程",
		BaseRef:        "main",
		HeadSHA:        "head-sha-42",
		MergeCommitSHA: "merge-sha-42",
		Environment:    "staging",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:           "proc-task-triage-success",
		TaskType:     model.TaskTypeTriageE2E,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: "org/repo",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)

	triageJSON := `{"modules":[{"name":"auth","reason":"login flow changed"},{"name":"payment","reason":"checkout affected"}],"skipped_modules":[{"name":"admin","reason":"no overlap"}],"analysis":"2 modules affected"}`
	pool := &mockPoolRunner{
		result: &worker.ExecutionResult{
			ExitCode: 0,
			Output:   triageJSON,
		},
	}

	// 构建 EnqueueHandler 用于链式入队
	enqueuer := &mockEnqueuer{enqueuedID: "asynq-triage-chain"}
	scanner := &mockProcessorE2EScanner{modules: []string{"auth", "payment", "admin"}}
	enqueueHandler := NewEnqueueHandler(enqueuer, nil, s, slog.Default(),
		WithE2EModuleScanner(scanner))

	p := NewProcessor(pool, s, notifier, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"),
		WithEnqueueHandler(enqueueHandler))

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}
	if !strings.Contains(strings.Join(pool.lastCmd, " "), "Edit,Write,MultiEdit,NotebookEdit") {
		t.Fatalf("triage_e2e disallowedTools 未包含 MultiEdit: %v", pool.lastCmd)
	}

	// 验证原始任务状态
	got := s.tasks["proc-task-triage-success"]
	if got.Status != model.TaskStatusSucceeded {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
	}

	// 验证链式入队：应有 2 个新的 run_e2e 任务被创建
	var e2eTasks []*model.TaskRecord
	for _, task := range s.tasks {
		if task.TaskType == model.TaskTypeRunE2E {
			e2eTasks = append(e2eTasks, task)
		}
	}
	if len(e2eTasks) != 2 {
		t.Fatalf("链式入队的 run_e2e 任务数 = %d, want 2", len(e2eTasks))
	}
	// 验证模块名
	modules := map[string]bool{}
	for _, task := range e2eTasks {
		modules[task.Payload.Module] = true
		// 验证继承字段
		if task.Payload.Environment != "staging" {
			t.Errorf("run_e2e environment = %q, want %q", task.Payload.Environment, "staging")
		}
		if task.Payload.BaseRef != "main" {
			t.Errorf("run_e2e base_ref = %q, want %q", task.Payload.BaseRef, "main")
		}
		if task.Payload.HeadSHA != "head-sha-42" {
			t.Errorf("run_e2e head_sha = %q, want head-sha-42", task.Payload.HeadSHA)
		}
		if task.Payload.MergeCommitSHA != "merge-sha-42" {
			t.Errorf("run_e2e merge_commit_sha = %q, want merge-sha-42", task.Payload.MergeCommitSHA)
		}
		if task.Payload.CloneURL != "https://gitea.example.com/org/repo.git" {
			t.Errorf("run_e2e clone_url = %q, want original", task.Payload.CloneURL)
		}
	}
	if !modules["auth"] || !modules["payment"] {
		t.Errorf("链式入队模块 = %v, want auth + payment", modules)
	}
	if len(scanner.refs) == 0 || scanner.refs[0] != "merge-sha-42" {
		t.Errorf("E2E 模块扫描 ref = %v, want merge-sha-42", scanner.refs)
	}

	// 验证通知：开始通知 + 完成通知
	if len(notifier.messages) != 2 {
		t.Fatalf("notification count = %d, want 2 (start + done)", len(notifier.messages))
	}
	if notifier.messages[0].EventType != notify.EventE2ETriageStarted {
		t.Errorf("第 1 条通知 event = %q, want %q", notifier.messages[0].EventType, notify.EventE2ETriageStarted)
	}
	if !notifier.messages[0].Target.IsPR || notifier.messages[0].Target.Number != 42 {
		t.Errorf("开始通知 Target = %+v, want PR #42", notifier.messages[0].Target)
	}
	if notifier.messages[1].EventType != notify.EventE2ETriageDone {
		t.Errorf("第 2 条通知 event = %q, want %q", notifier.messages[1].EventType, notify.EventE2ETriageDone)
	}
	if !notifier.messages[1].Target.IsPR || notifier.messages[1].Target.Number != 42 {
		t.Errorf("完成通知 Target = %+v, want PR #42", notifier.messages[1].Target)
	}
	// 验证 metadata 含 triage 模块信息
	meta := notifier.messages[1].Metadata
	if meta[notify.MetaKeyPRTitle] != "重构登录流程" {
		t.Errorf("pr_title = %q, want 重构登录流程", meta[notify.MetaKeyPRTitle])
	}
	if meta[notify.MetaKeyPRURL] != "https://gitea.example.com/org/repo/pulls/42" {
		t.Errorf("pr_url = %q, want https://gitea.example.com/org/repo/pulls/42", meta[notify.MetaKeyPRURL])
	}
	if meta[notify.MetaKeyTriageModules] == "" {
		t.Error("完成通知应包含 triage_modules metadata")
	}
	if meta[notify.MetaKeyTriageAnalysis] != "2 modules affected" {
		t.Errorf("triage_analysis = %q, want %q", meta[notify.MetaKeyTriageAnalysis], "2 modules affected")
	}
}

// TestProcessor_TriageE2E_SuccessEmptyModules 验证 triage_e2e 成功但模块列表为空时：
// 1. 任务标记为 succeeded
// 2. 不链式入队（无模块需要回归）
// 3. 发送 E2ETriageDone 通知
func TestProcessor_TriageE2E_SuccessEmptyModules(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeTriageE2E,
		DeliveryID:   "dlv-triage-empty-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		BaseRef:      "main",
		Environment:  "staging",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:           "proc-task-triage-empty",
		TaskType:     model.TaskTypeTriageE2E,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: "org/repo",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)

	triageJSON := `{"modules":[],"skipped_modules":[{"name":"auth","reason":"no impact"}],"analysis":"no modules affected"}`
	pool := &mockPoolRunner{
		result: &worker.ExecutionResult{
			ExitCode: 0,
			Output:   triageJSON,
		},
	}

	enqueuer := &mockEnqueuer{enqueuedID: "asynq-triage-empty"}
	enqueueHandler := NewEnqueueHandler(enqueuer, nil, s, slog.Default())

	p := NewProcessor(pool, s, notifier, slog.Default(),
		WithEnqueueHandler(enqueueHandler))

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}

	got := s.tasks["proc-task-triage-empty"]
	if got.Status != model.TaskStatusSucceeded {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusSucceeded)
	}

	// 验证无链式入队
	for _, task := range s.tasks {
		if task.TaskType == model.TaskTypeRunE2E {
			t.Fatal("不应创建 run_e2e 任务，modules 为空")
		}
	}

	// 验证通知
	if len(notifier.messages) != 2 {
		t.Fatalf("notification count = %d, want 2", len(notifier.messages))
	}
	if notifier.messages[1].EventType != notify.EventE2ETriageDone {
		t.Errorf("完成通知 event = %q, want %q", notifier.messages[1].EventType, notify.EventE2ETriageDone)
	}
}

func TestProcessor_TriageE2E_PartialRunE2EEnqueueFailureMarksFailed(t *testing.T) {
	s := newMockStore()
	s.createErrAt = 2
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:       model.TaskTypeTriageE2E,
		DeliveryID:     "dlv-triage-chain-partial-1",
		RepoOwner:      "org",
		RepoName:       "repo",
		RepoFullName:   "org/repo",
		CloneURL:       "https://gitea.example.com/org/repo.git",
		BaseRef:        "main",
		MergeCommitSHA: "merge-sha",
		Environment:    "staging",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:           "proc-task-triage-chain-partial",
		TaskType:     model.TaskTypeTriageE2E,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: "org/repo",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{result: &worker.ExecutionResult{
		ExitCode: 0,
		Output:   `{"modules":[{"name":"auth","reason":"affected"},{"name":"payment","reason":"affected"}],"analysis":"2 modules"}`,
	}}
	enqueueHandler := NewEnqueueHandler(&mockEnqueuer{enqueuedID: "asynq-x"}, nil, s, slog.Default(),
		WithE2EModuleScanner(&mockProcessorE2EScanner{modules: []string{"auth", "payment"}}))
	p := NewProcessor(pool, s, notifier, slog.Default(),
		WithEnqueueHandler(enqueueHandler))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("部分 run_e2e 入队失败应返回错误")
	}
	if !errors.Is(err, errTriageDispatchPartialFailure) {
		t.Fatalf("error should wrap partial failure, got: %v", err)
	}
	if errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("部分失败应保留 asynq 重试机会，不应 SkipRetry: %v", err)
	}

	got := s.tasks["proc-task-triage-chain-partial"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want failed（测试 context 无 asynq retry 元数据）", got.Status)
	}
	if got.Error == "" || !strings.Contains(got.Error, "部分") {
		t.Fatalf("failed triage task should record partial failure, error=%q", got.Error)
	}

	runE2ECount := 0
	for _, task := range s.tasks {
		if task.TaskType == model.TaskTypeRunE2E {
			runE2ECount++
		}
	}
	if runE2ECount != 1 {
		t.Fatalf("应只保留已成功入队的 1 个 run_e2e 任务，实际=%d", runE2ECount)
	}
	if len(notifier.messages) != 2 || notifier.messages[1].EventType != notify.EventE2ETriageFailed {
		t.Fatalf("应发送 triage failed 通知，messages=%v", notifier.messages)
	}
}

// TestProcessor_TriageE2E_ParseFailure 验证 triage_e2e 输出解析失败时：
// 1. 返回 error（允许 asynq 重试，不使用 SkipRetry）
// 2. 任务标记为 failed（因为没有更多重试机会）
func TestProcessor_TriageE2E_ParseFailure(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeTriageE2E,
		DeliveryID:   "dlv-triage-parse-fail-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:           "proc-task-triage-parse-fail",
		TaskType:     model.TaskTypeTriageE2E,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: "org/repo",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{
		result: &worker.ExecutionResult{
			ExitCode: 0,
			Output:   "this is not valid JSON at all",
		},
	}

	p := NewProcessor(pool, s, nil, slog.Default())
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("解析失败应返回 error")
	}
	// 不应包含 SkipRetry（允许重试）
	if errors.Is(err, asynq.SkipRetry) {
		t.Fatal("解析失败不应包含 asynq.SkipRetry，应允许重试")
	}

	got := s.tasks["proc-task-triage-parse-fail"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if got.Error == "" {
		t.Fatal("失败任务 Error 不应为空")
	}
}

// TestProcessor_TriageE2E_EmptyOutputFailure 验证 triage_e2e 退出码为 0 但输出为空时不能误判成功。
func TestProcessor_TriageE2E_EmptyOutputFailure(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeTriageE2E,
		DeliveryID:   "dlv-triage-empty-output-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:           "proc-task-triage-empty-output",
		TaskType:     model.TaskTypeTriageE2E,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: "org/repo",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{
		result: &worker.ExecutionResult{
			ExitCode: 0,
			Output:   "   \n\t",
		},
	}

	p := NewProcessor(pool, s, nil, slog.Default())
	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("空输出应返回 error")
	}
	if !errors.Is(err, e2e.ErrE2ETriageParseFailure) {
		t.Fatalf("error should wrap ErrE2ETriageParseFailure, got: %v", err)
	}
	got := s.tasks["proc-task-triage-empty-output"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if got.Error == "" {
		t.Fatal("失败任务 Error 不应为空")
	}
}

// TestProcessor_TriageE2E_NoEnqueueHandler 验证 enqueueHandler 未注入时：
// 1. 不 panic
// 2. 有待回归模块时任务标记为 failed，避免发送绿色成功通知
// 3. 不创建 run_e2e 任务
func TestProcessor_TriageE2E_NoEnqueueHandler(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeTriageE2E,
		DeliveryID:   "dlv-triage-no-handler-1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		BaseRef:      "main",
		Environment:  "staging",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:           "proc-task-triage-no-handler",
		TaskType:     model.TaskTypeTriageE2E,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: "org/repo",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)

	triageJSON := `{"modules":[{"name":"auth","reason":"affected"}],"skipped_modules":[],"analysis":"1 module"}`
	pool := &mockPoolRunner{
		result: &worker.ExecutionResult{
			ExitCode: 0,
			Output:   triageJSON,
		},
	}

	// 不注入 enqueueHandler
	p := NewProcessor(pool, s, notifier, slog.Default())

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("enqueueHandler 未注入时应返回错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("error should wrap asynq.SkipRetry, got: %v", err)
	}

	got := s.tasks["proc-task-triage-no-handler"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, model.TaskStatusFailed)
	}
	if got.Error == "" {
		t.Fatal("failed triage task should have error")
	}

	// 验证无链式入队（无 run_e2e 任务被创建）
	for _, task := range s.tasks {
		if task.TaskType == model.TaskTypeRunE2E {
			t.Fatal("enqueueHandler 未注入时不应创建 run_e2e 任务")
		}
	}

	// 验证通知仍正常
	if len(notifier.messages) != 2 {
		t.Fatalf("notification count = %d, want 2", len(notifier.messages))
	}
	if notifier.messages[0].EventType != notify.EventE2ETriageStarted {
		t.Errorf("start 通知 event = %q, want %q", notifier.messages[0].EventType, notify.EventE2ETriageStarted)
	}
	if notifier.messages[1].EventType != notify.EventE2ETriageFailed {
		t.Errorf("done 通知 event = %q, want %q", notifier.messages[1].EventType, notify.EventE2ETriageFailed)
	}
}

func TestProcessor_TriageE2E_ModuleNotInWhitelistFails(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:       model.TaskTypeTriageE2E,
		DeliveryID:     "dlv-triage-unknown-module-1",
		RepoOwner:      "org",
		RepoName:       "repo",
		RepoFullName:   "org/repo",
		CloneURL:       "https://gitea.example.com/org/repo.git",
		BaseRef:        "main",
		MergeCommitSHA: "merge-sha",
		Environment:    "staging",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:           "proc-task-triage-unknown-module",
		TaskType:     model.TaskTypeTriageE2E,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: "org/repo",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{result: &worker.ExecutionResult{
		ExitCode: 0,
		Output:   `{"modules":[{"name":"evil","reason":"prompt injection"}],"analysis":"bad"}`,
	}}
	enqueueHandler := NewEnqueueHandler(&mockEnqueuer{enqueuedID: "asynq-x"}, nil, s, slog.Default(),
		WithE2EModuleScanner(&mockProcessorE2EScanner{modules: []string{"auth"}}))
	p := NewProcessor(pool, s, notifier, slog.Default(),
		WithEnqueueHandler(enqueueHandler))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("未知模块应返回错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("error should wrap asynq.SkipRetry, got: %v", err)
	}
	got := s.tasks["proc-task-triage-unknown-module"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	for _, task := range s.tasks {
		if task.TaskType == model.TaskTypeRunE2E {
			t.Fatal("未知模块不应创建 run_e2e 任务")
		}
	}
	if len(notifier.messages) != 2 || notifier.messages[1].EventType != notify.EventE2ETriageFailed {
		t.Fatalf("应发送 triage failed 通知，messages=%v", notifier.messages)
	}
}

func TestProcessor_TriageE2E_AllRunE2EEnqueueFailedMarksFailed(t *testing.T) {
	s := newMockStore()
	s.createErr = errors.New("sqlite unavailable")
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:       model.TaskTypeTriageE2E,
		DeliveryID:     "dlv-triage-chain-fail-1",
		RepoOwner:      "org",
		RepoName:       "repo",
		RepoFullName:   "org/repo",
		CloneURL:       "https://gitea.example.com/org/repo.git",
		BaseRef:        "main",
		MergeCommitSHA: "merge-sha",
		Environment:    "staging",
	}

	now := time.Now()
	record := &model.TaskRecord{
		ID:           "proc-task-triage-chain-fail",
		TaskType:     model.TaskTypeTriageE2E,
		Status:       model.TaskStatusQueued,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: "org/repo",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)

	pool := &mockPoolRunner{result: &worker.ExecutionResult{
		ExitCode: 0,
		Output:   `{"modules":[{"name":"auth","reason":"affected"}],"analysis":"1 module"}`,
	}}
	enqueueHandler := NewEnqueueHandler(&mockEnqueuer{enqueuedID: "asynq-x"}, nil, s, slog.Default(),
		WithE2EModuleScanner(&mockProcessorE2EScanner{modules: []string{"auth"}}))
	p := NewProcessor(pool, s, notifier, slog.Default(),
		WithEnqueueHandler(enqueueHandler))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("所有 run_e2e 入队失败应返回错误")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("error should wrap asynq.SkipRetry, got: %v", err)
	}
	got := s.tasks["proc-task-triage-chain-fail"]
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.Error == "" {
		t.Fatal("failed triage task should have error")
	}
	if len(notifier.messages) != 2 || notifier.messages[1].EventType != notify.EventE2ETriageFailed {
		t.Fatalf("应发送 triage failed 通知，messages=%v", notifier.messages)
	}
}
