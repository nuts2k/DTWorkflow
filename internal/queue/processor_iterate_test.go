package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/iterate"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

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

func TestProcessTask_FixReviewAlreadySucceededRepairsIterationState(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:      model.TaskTypeFixReview,
		DeliveryID:    "iterate-70:fix_review:1",
		RepoOwner:     "org",
		RepoName:      "repo",
		RepoFullName:  "org/repo",
		PRNumber:      42,
		SessionID:     70,
		RoundNumber:   1,
		FixReportPath: "docs/review_history/42-round1.md",
	}
	now := time.Now()
	raw := `{"type":"result","result":"{\"fixes\":[{\"action\":\"modified\"}],\"summary\":\"fixed\"}"}`
	record := &model.TaskRecord{
		ID:           "fix-review-already-succeeded",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusSucceeded,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: payload.RepoFullName,
		PRNumber:     payload.PRNumber,
		Result:       raw,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)
	s.iterationSession = &store.IterationSessionRecord{
		ID:           70,
		RepoFullName: "org/repo",
		PRNumber:     42,
		Status:       "fixing",
		MaxRounds:    3,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          170,
		SessionID:   70,
		RoundNumber: 1,
		FixTaskID:   record.ID,
	}
	iterExec := &mockIterateExecutor{
		err: errors.New("should not execute"),
	}
	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithIterateService(iterExec))

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}
	if iterExec.calls != 0 {
		t.Fatalf("已成功任务不应重跑容器，calls=%d", iterExec.calls)
	}
	if s.latestIterationRound.CompletedAt == nil {
		t.Fatal("应补齐 round completed_at")
	}
	if s.iterationSession.Status != "reviewing" {
		t.Fatalf("session status = %q, want reviewing", s.iterationSession.Status)
	}
}

func TestProcessTask_FixReviewSuccessIterationPersistFailureReturnsError(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixReview,
		DeliveryID:   "iterate-71:fix_review:1",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     42,
		SessionID:    71,
		RoundNumber:  1,
	}
	now := time.Now()
	record := &model.TaskRecord{
		ID:           "fix-review-persist-failure",
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
		ID:           71,
		RepoFullName: "org/repo",
		PRNumber:     42,
		Status:       "fixing",
		MaxRounds:    3,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          171,
		SessionID:   71,
		RoundNumber: 1,
		FixTaskID:   record.ID,
	}
	s.updateIterationRoundErr = errors.New("db down")
	iterExec := &mockIterateExecutor{
		result: &iterate.FixReviewResult{
			RawOutput: `{"type":"result","result":"{\"fixes\":[{\"action\":\"modified\"}],\"summary\":\"fixed\"}"}`,
			Output: &iterate.FixReviewOutput{
				Fixes:   []iterate.FixItem{{Action: "modified"}},
				Summary: "fixed",
			},
		},
	}
	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithIterateService(iterExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("迭代状态落库失败应返回错误")
	}
	if !strings.Contains(err.Error(), "迭代状态落库失败") {
		t.Fatalf("error = %v, want contains 迭代状态落库失败", err)
	}
}

func TestProcessTask_FixReviewAlreadyCompletedRecomputesTotalIssuesFixed(t *testing.T) {
	s := newMockStore()
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixReview,
		DeliveryID:   "iterate-72:fix_review:2",
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     42,
		SessionID:    72,
		RoundNumber:  2,
	}
	now := time.Now()
	record := &model.TaskRecord{
		ID:           "fix-review-already-completed",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusSucceeded,
		Payload:      payload,
		DeliveryID:   payload.DeliveryID,
		RepoFullName: payload.RepoFullName,
		PRNumber:     payload.PRNumber,
		Result:       `{"type":"result","result":"{\"fixes\":[{\"action\":\"modified\"},{\"action\":\"alternative_chosen\"}],\"summary\":\"fixed\"}"}`,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	seedRecord(s, record)
	s.iterationSession = &store.IterationSessionRecord{
		ID:               72,
		RepoFullName:     "org/repo",
		PRNumber:         42,
		Status:           "fixing",
		MaxRounds:        3,
		TotalIssuesFixed: 0,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          172,
		SessionID:   72,
		RoundNumber: 2,
		FixTaskID:   record.ID,
		IssuesFixed: 2,
		CompletedAt: &now,
	}
	s.completedIterationRounds = []*store.IterationRoundRecord{
		{
			ID:          171,
			SessionID:   72,
			RoundNumber: 1,
			IssuesFixed: 1,
			CompletedAt: &now,
		},
		s.latestIterationRound,
	}
	iterExec := &mockIterateExecutor{err: errors.New("should not execute")}
	p := NewProcessor(&mockPoolRunner{}, s, nil, slog.Default(), WithIterateService(iterExec))

	if err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload)); err != nil {
		t.Fatalf("ProcessTask error: %v", err)
	}
	if iterExec.calls != 0 {
		t.Fatalf("已完成任务不应重跑容器，calls=%d", iterExec.calls)
	}
	if s.iterationSession.TotalIssuesFixed != 3 {
		t.Fatalf("TotalIssuesFixed = %d, want 3", s.iterationSession.TotalIssuesFixed)
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
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          102,
		SessionID:   9,
		RoundNumber: 1,
		FixTaskID:   record.ID,
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
	if s.latestIterationRound.CompletedAt == nil {
		t.Fatal("基础设施失败应标记轮次完成，避免恢复链路卡死")
	}
	if s.latestIterationRound.IssuesFixed != 0 {
		t.Fatalf("IssuesFixed = %d, want 0", s.latestIterationRound.IssuesFixed)
	}
}

func TestProcessTask_FixReviewCancellationCompletesIterationRound(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:           model.TaskTypeFixReview,
		DeliveryID:         "iterate-10:fix_review:1",
		RepoOwner:          "org",
		RepoName:           "repo",
		RepoFullName:       "org/repo",
		PRNumber:           42,
		SessionID:          10,
		RoundNumber:        1,
		IterationMaxRounds: 3,
	}
	now := time.Now()
	record := &model.TaskRecord{
		ID:           "fix-review-cancelled",
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
		ID:           10,
		RepoFullName: "org/repo",
		PRNumber:     42,
		Status:       "fixing",
		MaxRounds:    3,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          103,
		SessionID:   10,
		RoundNumber: 1,
		FixTaskID:   record.ID,
	}
	iterExec := &mockIterateExecutor{err: context.Canceled}
	p := NewProcessor(&mockPoolRunner{}, s, notifier, slog.Default(), WithIterateService(iterExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("error = %v, want SkipRetry", err)
	}
	if record.Status != model.TaskStatusCancelled {
		t.Fatalf("record status = %q, want cancelled", record.Status)
	}
	if s.iterationSession.Status != "idle" {
		t.Fatalf("session status = %q, want idle", s.iterationSession.Status)
	}
	if s.latestIterationRound.CompletedAt == nil {
		t.Fatal("取消 fix_review 应标记轮次完成，避免恢复链路卡死")
	}
	if len(notifier.messages) != 1 || notifier.messages[0].EventType != notify.EventIterationError {
		t.Fatalf("应发送 iteration.error 通知，messages=%v", notifier.messages)
	}
}

func TestProcessTask_FixReviewNoNewCommitsStopsIterationAndPreservesRawOutput(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:           model.TaskTypeFixReview,
		DeliveryID:         "iterate-11:fix_review:1",
		RepoOwner:          "org",
		RepoName:           "repo",
		RepoFullName:       "org/repo",
		PRNumber:           42,
		SessionID:          11,
		RoundNumber:        1,
		IterationMaxRounds: 3,
	}
	now := time.Now()
	record := &model.TaskRecord{
		ID:           "fix-review-no-new-commits",
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
		ID:           11,
		RepoFullName: "org/repo",
		PRNumber:     42,
		Status:       "fixing",
		MaxRounds:    3,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          104,
		SessionID:   11,
		RoundNumber: 1,
		FixTaskID:   record.ID,
	}
	rawOutput := "[entrypoint] ERROR: fix_review 未产生新提交，跳过受控 push"
	iterExec := &mockIterateExecutor{
		result: &iterate.FixReviewResult{RawOutput: rawOutput, ExitCode: 11},
		err:    fmt.Errorf("%w: Claude 未产生新提交", iterate.ErrNoNewCommits),
	}
	p := NewProcessor(&mockPoolRunner{}, s, notifier, slog.Default(), WithIterateService(iterExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("error = %v, want SkipRetry", err)
	}
	if record.Status != model.TaskStatusFailed {
		t.Fatalf("record status = %q, want failed", record.Status)
	}
	if record.Result != rawOutput {
		t.Fatalf("record.Result = %q, want raw output", record.Result)
	}
	if s.iterationSession.Status != "idle" {
		t.Fatalf("session status = %q, want idle", s.iterationSession.Status)
	}
	if s.latestIterationRound.CompletedAt == nil {
		t.Fatal("未产生新提交应标记轮次完成，避免等待不存在的 review webhook")
	}
	if s.latestIterationRound.IssuesFixed != 0 {
		t.Fatalf("IssuesFixed = %d, want 0", s.latestIterationRound.IssuesFixed)
	}
	if len(notifier.messages) != 1 || notifier.messages[0].EventType != notify.EventIterationError {
		t.Fatalf("应发送 iteration.error 通知，messages=%v", notifier.messages)
	}
}

func TestProcessTask_FixReviewRetryableExitCodeDoesNotSkipRetry(t *testing.T) {
	s := newMockStore()
	notifier := &stubNotifier{}
	payload := model.TaskPayload{
		TaskType:           model.TaskTypeFixReview,
		DeliveryID:         "iterate-12:fix_review:1",
		RepoOwner:          "org",
		RepoName:           "repo",
		RepoFullName:       "org/repo",
		PRNumber:           42,
		SessionID:          12,
		RoundNumber:        1,
		IterationMaxRounds: 3,
	}
	now := time.Now()
	record := &model.TaskRecord{
		ID:           "fix-review-retryable-exit",
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
		ID:           12,
		RepoFullName: "org/repo",
		PRNumber:     42,
		Status:       "fixing",
		MaxRounds:    3,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          105,
		SessionID:   12,
		RoundNumber: 1,
		FixTaskID:   record.ID,
	}
	rawOutput := "claude cli temporary failure"
	iterExec := &mockIterateExecutor{
		result: &iterate.FixReviewResult{RawOutput: rawOutput, ExitCode: 2},
		err:    fmt.Errorf("容器执行失败，退出码 2"),
	}
	p := NewProcessor(&mockPoolRunner{}, s, notifier, slog.Default(), WithIterateService(iterExec))

	err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))
	if err == nil {
		t.Fatal("retryable exit code should return error")
	}
	if errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("普通非零退出码不应包含 SkipRetry: %v", err)
	}
	if record.Result != rawOutput {
		t.Fatalf("record.Result = %q, want raw output", record.Result)
	}
}

