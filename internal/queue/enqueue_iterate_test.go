package queue

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/webhook"
)

func assertIterationNotificationMetadata(t *testing.T, msg notify.Message, wantPRTitle string) {
	t.Helper()
	if msg.Metadata == nil {
		t.Fatal("notification metadata 不能为空")
	}
	if msg.Metadata[notify.MetaKeyPRTitle] != wantPRTitle {
		t.Fatalf("pr_title = %q, want %q", msg.Metadata[notify.MetaKeyPRTitle], wantPRTitle)
	}
	notifyTime := msg.Metadata[notify.MetaKeyNotifyTime]
	if notifyTime == "" {
		t.Fatal("notify_time 不能为空")
	}
	if _, err := time.ParseInLocation("2006-01-02 15:04:05", notifyTime, shanghaiZone); err != nil {
		t.Fatalf("notify_time = %q, want Asia/Shanghai 时间格式: %v", notifyTime, err)
	}
}

func TestHandlePullRequest_UserPushDuringIterationCancelsFixReview(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-new-user-review"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default(),
		WithIterateStore(s),
		WithIterateConfig(&mockIterateConfigProvider{cfg: config.IterateConfig{BotLogin: "dtworkflow-bot"}}),
	)

	oldReview := &model.TaskRecord{
		ID:           "old-review-task",
		AsynqID:      "asynq-old-review",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		PRNumber:     42,
		Payload:      model.TaskPayload{HeadSHA: "old-review-sha"},
	}
	oldFixReview := &model.TaskRecord{
		ID:           "old-fix-review-task",
		AsynqID:      "asynq-old-fix-review",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		PRNumber:     42,
	}
	seedRecord(s, oldReview)
	seedRecord(s, oldFixReview)
	s.activePRTasks = []*model.TaskRecord{oldReview}
	s.iterationSession = &store.IterationSessionRecord{
		ID:           7,
		RepoFullName: "org/repo",
		PRNumber:     42,
		Status:       "fixing",
	}

	event := webhook.PullRequestEvent{
		Action:     "synchronized",
		DeliveryID: "delivery-user-push-during-iteration",
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{
			Number:  42,
			HeadSHA: "user-new-sha",
		},
		Sender: webhook.UserRef{Login: "alice"},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest error: %v", err)
	}

	if len(canceller.deleteCalls) != 2 {
		t.Fatalf("deleteCalls = %v, want 2 calls", canceller.deleteCalls)
	}
	if !containsString(canceller.deleteCalls, "asynq-old-review") {
		t.Fatalf("deleteCalls = %v, want contain asynq-old-review", canceller.deleteCalls)
	}
	if !containsString(canceller.deleteCalls, "asynq-old-fix-review") {
		t.Fatalf("deleteCalls = %v, want contain asynq-old-fix-review", canceller.deleteCalls)
	}
	if oldFixReview.Status != model.TaskStatusCancelled {
		t.Fatalf("old fix_review status = %q, want cancelled", oldFixReview.Status)
	}
	if s.updateIterationSessionCalls != 0 {
		t.Fatalf("UpdateIterationSession 调用次数 = %d, want 0", s.updateIterationSessionCalls)
	}
}

func TestHandlePullRequest_BotPushDuringIterationKeepsFixReview(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-new-bot-review"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default(),
		WithIterateStore(s),
		WithIterateConfig(&mockIterateConfigProvider{cfg: config.IterateConfig{BotLogin: "dtworkflow-bot"}}),
	)

	oldReview := &model.TaskRecord{
		ID:           "old-review-task-bot",
		AsynqID:      "asynq-old-review-bot",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		PRNumber:     43,
		Payload:      model.TaskPayload{HeadSHA: "old-review-sha"},
	}
	oldFixReview := &model.TaskRecord{
		ID:           "old-fix-review-task-bot",
		AsynqID:      "asynq-old-fix-review-bot",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		PRNumber:     43,
	}
	seedRecord(s, oldReview)
	seedRecord(s, oldFixReview)
	s.activePRTasks = []*model.TaskRecord{oldReview}
	s.iterationSession = &store.IterationSessionRecord{
		ID:           8,
		RepoFullName: "org/repo",
		PRNumber:     43,
		Status:       "fixing",
	}

	event := webhook.PullRequestEvent{
		Action:     "synchronized",
		DeliveryID: "delivery-bot-push-during-iteration",
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{
			Number:  43,
			HeadSHA: "bot-new-sha",
		},
		Sender: webhook.UserRef{Login: "DTWorkflow-Bot"},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest error: %v", err)
	}

	if len(canceller.deleteCalls) != 1 || canceller.deleteCalls[0] != "asynq-old-review-bot" {
		t.Fatalf("deleteCalls = %v, want [asynq-old-review-bot]", canceller.deleteCalls)
	}
	if oldFixReview.Status != model.TaskStatusQueued {
		t.Fatalf("old fix_review status = %q, want queued", oldFixReview.Status)
	}
	if s.updateIterationSessionCalls != 0 {
		t.Fatalf("UpdateIterationSession 调用次数 = %d, want 0", s.updateIterationSessionCalls)
	}
	if s.iterationSession.Status != "fixing" {
		t.Fatalf("session status = %q, want fixing", s.iterationSession.Status)
	}
}

func TestHandlePullRequest_EmptyBotLoginTreatsPushAsUserPush(t *testing.T) {
	s := newMockStore()
	canceller := &mockTaskCanceller{}
	mc := &mockEnqueuer{enqueuedID: "asynq-new-review-empty-bot"}
	h := NewEnqueueHandler(mc, canceller, s, slog.Default(),
		WithIterateStore(s),
		WithIterateConfig(&mockIterateConfigProvider{cfg: config.IterateConfig{BotLogin: ""}}),
	)

	oldReview := &model.TaskRecord{
		ID:           "old-review-empty-bot",
		AsynqID:      "asynq-old-review-empty-bot",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusQueued,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		PRNumber:     44,
	}
	oldFixReview := &model.TaskRecord{
		ID:           "old-fix-review-empty-bot",
		AsynqID:      "asynq-old-fix-review-empty-bot",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusRetrying,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		PRNumber:     44,
	}
	seedRecord(s, oldReview)
	seedRecord(s, oldFixReview)
	s.activePRTasks = []*model.TaskRecord{oldReview}
	s.iterationSession = &store.IterationSessionRecord{
		ID:           9,
		RepoFullName: "org/repo",
		PRNumber:     44,
		Status:       "fixing",
	}

	event := webhook.PullRequestEvent{
		Action:     "synchronized",
		DeliveryID: "delivery-empty-bot-login",
		Repository: webhook.RepositoryRef{
			Owner: "org", Name: "repo", FullName: "org/repo",
			CloneURL: "https://gitea.example.com/org/repo.git",
		},
		PullRequest: webhook.PullRequestRef{
			Number:  44,
			HeadSHA: "new-sha",
		},
		Sender: webhook.UserRef{Login: "unknown"},
	}

	if err := h.HandlePullRequest(context.Background(), event); err != nil {
		t.Fatalf("HandlePullRequest error: %v", err)
	}

	if !containsString(canceller.deleteCalls, "asynq-old-review-empty-bot") {
		t.Fatalf("deleteCalls = %v, want contain old review", canceller.deleteCalls)
	}
	if oldFixReview.Status != model.TaskStatusCancelled {
		t.Fatalf("old fix_review status = %q, want cancelled", oldFixReview.Status)
	}
	if s.updateIterationSessionCalls != 0 {
		t.Fatalf("UpdateIterationSession 调用次数 = %d, want 0", s.updateIterationSessionCalls)
	}
}

func TestAfterReviewCompleted_EnqueuesFixReviewWithIterationConfig(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-fix-review"}
	notifier := &stubNotifier{}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithIterateStore(s),
		WithIterateConfig(&mockIterateConfigProvider{cfg: config.IterateConfig{
			Enabled:              true,
			MaxRounds:            5,
			Label:                "auto-iterate",
			NotificationMode:     "progress",
			FixSeverityThreshold: "error",
			ReportPath:           "custom/history",
		}}),
		WithIterateNotifier(notifier),
	)
	s.iterationSession = &store.IterationSessionRecord{
		ID:           11,
		RepoFullName: "org/repo",
		PRNumber:     44,
		Status:       "reviewing",
		MaxRounds:    5,
	}
	reviewTask := &model.TaskRecord{ID: "review-task-44"}
	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		PRNumber:     44,
		PRTitle:      "Fix config",
		BaseRef:      "main",
		HeadRef:      "feature/config",
		HeadSHA:      "head-sha",
	}
	issues := []review.ReviewIssue{{
		File:     "main.go",
		Line:     10,
		Severity: "ERROR",
		Message:  "bug",
	}}

	if !h.AfterReviewCompleted(context.Background(), reviewTask, payload, []string{"auto-iterate"}, issues) {
		t.Fatal("AfterReviewCompleted 应触发 fix_review")
	}

	if len(mc.payloads) != 1 {
		t.Fatalf("入队 payload 数量 = %d, want 1", len(mc.payloads))
	}
	fixPayload := mc.payloads[0]
	if fixPayload.TaskType != model.TaskTypeFixReview {
		t.Fatalf("TaskType = %q, want fix_review", fixPayload.TaskType)
	}
	if fixPayload.IterationMaxRounds != 5 {
		t.Fatalf("IterationMaxRounds = %d, want 5", fixPayload.IterationMaxRounds)
	}
	if !strings.HasPrefix(fixPayload.FixReportPath, "custom/history/44-") ||
		!strings.HasSuffix(fixPayload.FixReportPath, "-round1.md") {
		t.Fatalf("FixReportPath = %q, want custom/history 下 round1 报告", fixPayload.FixReportPath)
	}
	if s.iterationSession.Status != "fixing" {
		t.Fatalf("session status = %q, want fixing", s.iterationSession.Status)
	}
	if s.iterationSession.TotalIssuesFound != 1 {
		t.Fatalf("TotalIssuesFound = %d, want 1", s.iterationSession.TotalIssuesFound)
	}
	if s.updateIterationRoundCalls != 1 {
		t.Fatalf("UpdateIterationRound 调用次数 = %d, want 1", s.updateIterationRoundCalls)
	}
	if s.latestIterationRound.FixReportPath != fixPayload.FixReportPath {
		t.Fatalf("round FixReportPath = %q, want %q", s.latestIterationRound.FixReportPath, fixPayload.FixReportPath)
	}
	if s.latestIterationRound.FixTaskID == "" {
		t.Fatal("round FixTaskID 应在 fix_review 入队后写回")
	}
	var fixRecord *model.TaskRecord
	for _, task := range s.tasks {
		if task.TaskType == model.TaskTypeFixReview {
			fixRecord = task
			break
		}
	}
	if fixRecord == nil {
		t.Fatal("未找到入队后的 fix_review task record")
	}
	if s.latestIterationRound.FixTaskID != fixRecord.ID {
		t.Fatalf("round FixTaskID = %q, want %q", s.latestIterationRound.FixTaskID, fixRecord.ID)
	}
	if len(notifier.messages) != 1 || notifier.messages[0].EventType != notify.EventIterationProgress {
		t.Fatalf("progress notifications = %+v, want iteration.progress", notifier.messages)
	}
	assertIterationNotificationMetadata(t, notifier.messages[0], "Fix config")
}

func TestAfterReviewCompleted_WaitsForPreviousFixReviewPersisted(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-next-fix-review"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithIterateStore(s),
		WithIterateConfig(&mockIterateConfigProvider{cfg: config.IterateConfig{
			Enabled:              true,
			MaxRounds:            3,
			Label:                "auto-iterate",
			NotificationMode:     "silent",
			FixSeverityThreshold: "error",
			ReportPath:           "docs/review_history",
		}}),
	)
	h.roundWaitTimeout = time.Second
	h.roundPollInterval = time.Millisecond

	completedAt := time.Now()
	pendingRound := &store.IterationRoundRecord{
		ID:          10,
		SessionID:   21,
		RoundNumber: 1,
		FixTaskID:   "fix-review-round-1",
	}
	completedRound := &store.IterationRoundRecord{
		ID:          10,
		SessionID:   21,
		RoundNumber: 1,
		FixTaskID:   "fix-review-round-1",
		IssuesFixed: 2,
		FixSummary:  "fixed https://secret.example/token",
		CompletedAt: &completedAt,
	}
	s.iterationSession = &store.IterationSessionRecord{
		ID:           21,
		RepoFullName: "org/repo",
		PRNumber:     45,
		Status:       "reviewing",
		MaxRounds:    3,
	}
	s.latestIterationRound = pendingRound
	s.completedIterationRounds = []*store.IterationRoundRecord{completedRound}

	calls := 0
	s.getIterationRoundHook = func(roundNumber int) *store.IterationRoundRecord {
		if roundNumber != 1 {
			return nil
		}
		calls++
		if calls == 1 {
			return pendingRound
		}
		return completedRound
	}

	reviewTask := &model.TaskRecord{ID: "review-task-45"}
	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		PRNumber:     45,
		PRTitle:      "Fix followup",
		BaseRef:      "main",
		HeadRef:      "feature/followup",
		HeadSHA:      "head-sha",
	}
	issues := []review.ReviewIssue{{
		File:     "main.go",
		Line:     10,
		Severity: "ERROR",
		Message:  "bug",
	}}

	if !h.AfterReviewCompleted(context.Background(), reviewTask, payload, []string{"auto-iterate"}, issues) {
		t.Fatal("上一轮落库完成后应继续触发下一轮 fix_review")
	}
	if calls < 2 {
		t.Fatalf("GetIterationRound 调用次数 = %d, want >= 2", calls)
	}
	if len(mc.payloads) != 1 {
		t.Fatalf("入队 payload 数量 = %d, want 1", len(mc.payloads))
	}
	fixPayload := mc.payloads[0]
	if fixPayload.RoundNumber != 2 {
		t.Fatalf("RoundNumber = %d, want 2", fixPayload.RoundNumber)
	}
	if !strings.Contains(fixPayload.PreviousFixes, "[link-redacted]") {
		t.Fatalf("PreviousFixes 应脱敏并包含上一轮摘要，实际: %q", fixPayload.PreviousFixes)
	}
	if strings.Contains(fixPayload.PreviousFixes, "https://") {
		t.Fatalf("PreviousFixes 不应包含原始链接，实际: %q", fixPayload.PreviousFixes)
	}
}

func TestAfterReviewCompleted_RecoversTerminalFixReviewRound(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-recovery-fix-review"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithIterateStore(s),
		WithIterateConfig(&mockIterateConfigProvider{cfg: config.IterateConfig{
			Enabled:              true,
			MaxRounds:            3,
			Label:                "auto-iterate",
			NotificationMode:     "silent",
			FixSeverityThreshold: "error",
			ReportPath:           "docs/review_history",
		}}),
	)

	failedFixTask := &model.TaskRecord{
		ID:           "fix-review-round-1",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusFailed,
		RepoFullName: "org/repo",
		PRNumber:     46,
	}
	seedRecord(s, failedFixTask)
	s.iterationSession = &store.IterationSessionRecord{
		ID:           31,
		RepoFullName: "org/repo",
		PRNumber:     46,
		Status:       "idle",
		MaxRounds:    3,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          20,
		SessionID:   31,
		RoundNumber: 1,
		FixTaskID:   failedFixTask.ID,
	}
	reviewTask := &model.TaskRecord{ID: "review-task-46"}
	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		PRNumber:     46,
		PRTitle:      "Recover fix review",
		BaseRef:      "main",
		HeadRef:      "feature/recover",
		HeadSHA:      "head-sha",
	}
	issues := []review.ReviewIssue{{
		File:     "main.go",
		Line:     10,
		Severity: "ERROR",
		Message:  "bug",
	}}

	if !h.AfterReviewCompleted(context.Background(), reviewTask, payload, []string{"auto-iterate"}, issues) {
		t.Fatal("已终态但未完成的 fix_review 轮次应被自愈并触发恢复轮")
	}
	if s.updateIterationRoundCalls < 2 {
		t.Fatalf("UpdateIterationRound 调用次数 = %d, want >= 2", s.updateIterationRoundCalls)
	}
	if s.latestIterationRound.RoundNumber != 2 {
		t.Fatalf("new round = %d, want 2", s.latestIterationRound.RoundNumber)
	}
	if len(mc.payloads) != 1 || mc.payloads[0].RoundNumber != 2 {
		t.Fatalf("payloads = %+v, want recovery round 2", mc.payloads)
	}
	if !s.latestIterationRound.IsRecovery {
		t.Fatal("恢复轮次应标记 IsRecovery=true")
	}
}

func TestAfterReviewCompleted_RefreshesLabelsBeforeFixReview(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-no-label"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithIterateStore(s),
		WithIterateConfig(&mockIterateConfigProvider{cfg: config.IterateConfig{
			Enabled:              true,
			MaxRounds:            3,
			Label:                "auto-iterate",
			NotificationMode:     "silent",
			FixSeverityThreshold: "error",
			ReportPath:           "docs/review_history",
		}}),
		WithIterateLabels(&mockIterateLabelManager{labels: []string{"bug"}}),
	)
	s.iterationSession = &store.IterationSessionRecord{
		ID:           41,
		RepoFullName: "org/repo",
		PRNumber:     47,
		Status:       "reviewing",
		MaxRounds:    3,
	}
	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		PRNumber:     47,
		BaseRef:      "main",
		HeadRef:      "feature/no-label",
	}
	issues := []review.ReviewIssue{{File: "main.go", Severity: "ERROR", Message: "bug"}}

	if h.AfterReviewCompleted(context.Background(), &model.TaskRecord{ID: "review-47"}, payload, []string{"auto-iterate"}, issues) {
		t.Fatal("当前标签已移除时不应触发 fix_review")
	}
	if len(mc.payloads) != 0 {
		t.Fatalf("不应入队 fix_review，payloads=%v", mc.payloads)
	}
}

func TestAfterReviewCompleted_FailsClosedWhenLatestRoundQueryFails(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-round-error"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithIterateStore(s),
		WithIterateConfig(&mockIterateConfigProvider{cfg: config.IterateConfig{
			Enabled:              true,
			MaxRounds:            3,
			Label:                "auto-iterate",
			NotificationMode:     "silent",
			FixSeverityThreshold: "error",
			ReportPath:           "docs/review_history",
		}}),
	)
	s.iterationSession = &store.IterationSessionRecord{
		ID:           42,
		RepoFullName: "org/repo",
		PRNumber:     48,
		Status:       "reviewing",
		MaxRounds:    3,
	}
	s.getLatestRoundErr = errors.New("db busy")
	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		PRNumber:     48,
		BaseRef:      "main",
		HeadRef:      "feature/db-busy",
	}
	issues := []review.ReviewIssue{{File: "main.go", Severity: "ERROR", Message: "bug"}}

	if h.AfterReviewCompleted(context.Background(), &model.TaskRecord{ID: "review-48"}, payload, []string{"auto-iterate"}, issues) {
		t.Fatal("查询最新轮次失败时应 fail closed")
	}
	if len(mc.payloads) != 0 {
		t.Fatalf("不应入队 fix_review，payloads=%v", mc.payloads)
	}
}

func TestAfterReviewCompleted_RepairsSucceededFixReviewRound(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-repaired-next"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithIterateStore(s),
		WithIterateConfig(&mockIterateConfigProvider{cfg: config.IterateConfig{
			Enabled:              true,
			MaxRounds:            3,
			Label:                "auto-iterate",
			NotificationMode:     "silent",
			FixSeverityThreshold: "error",
			ReportPath:           "docs/review_history",
		}}),
	)
	raw := `{"type":"result","result":"{\"fixes\":[{\"action\":\"modified\"},{\"action\":\"skipped\"}],\"summary\":\"fixed https://secret.example/token\"}"}`
	fixTask := &model.TaskRecord{
		ID:           "fix-review-succeeded-unpersisted",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusSucceeded,
		Result:       raw,
		RepoFullName: "org/repo",
		PRNumber:     49,
		Payload: model.TaskPayload{
			FixReportPath: "docs/review_history/49-round1.md",
		},
	}
	seedRecord(s, fixTask)
	s.iterationSession = &store.IterationSessionRecord{
		ID:               43,
		RepoFullName:     "org/repo",
		PRNumber:         49,
		Status:           "fixing",
		MaxRounds:        3,
		TotalIssuesFixed: 0,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          50,
		SessionID:   43,
		RoundNumber: 1,
		FixTaskID:   fixTask.ID,
	}
	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		PRNumber:     49,
		BaseRef:      "main",
		HeadRef:      "feature/repaired",
	}
	issues := []review.ReviewIssue{{File: "main.go", Severity: "ERROR", Message: "bug"}}

	if !h.AfterReviewCompleted(context.Background(), &model.TaskRecord{ID: "review-49"}, payload, []string{"auto-iterate"}, issues) {
		t.Fatal("已成功但未完成落库的 fix_review 轮次应自愈并继续下一轮")
	}
	if s.iterationSession.TotalIssuesFixed != 1 {
		t.Fatalf("TotalIssuesFixed = %d, want 1", s.iterationSession.TotalIssuesFixed)
	}
	if len(s.completedIterationRounds) == 0 {
		t.Fatal("应记录已完成轮次")
	}
	if strings.Contains(s.completedIterationRounds[0].FixSummary, "https://") {
		t.Fatalf("自愈摘要不应包含原始链接: %q", s.completedIterationRounds[0].FixSummary)
	}
	if len(mc.payloads) != 1 || mc.payloads[0].RoundNumber != 2 {
		t.Fatalf("payloads = %+v, want next round 2", mc.payloads)
	}
}

func TestAfterReviewCompleted_RepairRecomputesTotalIssuesFixed(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "asynq-recomputed-next"}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithIterateStore(s),
		WithIterateConfig(&mockIterateConfigProvider{cfg: config.IterateConfig{
			Enabled:              true,
			MaxRounds:            4,
			Label:                "auto-iterate",
			NotificationMode:     "silent",
			FixSeverityThreshold: "error",
			ReportPath:           "docs/review_history",
		}}),
	)
	now := time.Now()
	raw := `{"type":"result","result":"{\"fixes\":[{\"action\":\"modified\"},{\"action\":\"alternative_chosen\"}],\"summary\":\"fixed\"}"}`
	fixTask := &model.TaskRecord{
		ID:           "fix-review-succeeded-partial-persist",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusSucceeded,
		Result:       raw,
		RepoFullName: "org/repo",
		PRNumber:     50,
	}
	seedRecord(s, fixTask)
	s.iterationSession = &store.IterationSessionRecord{
		ID:               44,
		RepoFullName:     "org/repo",
		PRNumber:         50,
		Status:           "fixing",
		MaxRounds:        4,
		TotalIssuesFixed: 0,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          51,
		SessionID:   44,
		RoundNumber: 2,
		FixTaskID:   fixTask.ID,
	}
	s.completedIterationRounds = []*store.IterationRoundRecord{
		{
			ID:          50,
			SessionID:   44,
			RoundNumber: 1,
			IssuesFixed: 1,
			CompletedAt: &now,
		},
	}
	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		CloneURL:     "https://gitea.example.com/org/repo.git",
		PRNumber:     50,
		BaseRef:      "main",
		HeadRef:      "feature/recomputed",
	}
	issues := []review.ReviewIssue{{File: "main.go", Severity: "ERROR", Message: "bug"}}

	if !h.AfterReviewCompleted(context.Background(), &model.TaskRecord{ID: "review-50"}, payload, []string{"auto-iterate"}, issues) {
		t.Fatal("已成功但未完成落库的 fix_review 轮次应自愈并继续下一轮")
	}
	if s.iterationSession.TotalIssuesFixed != 3 {
		t.Fatalf("TotalIssuesFixed = %d, want 3", s.iterationSession.TotalIssuesFixed)
	}
	if len(mc.payloads) != 1 || mc.payloads[0].RoundNumber != 3 {
		t.Fatalf("payloads = %+v, want next round 3", mc.payloads)
	}
}

func TestAfterIterationApproved_RepairsSucceededFixReviewRoundBeforeComplete(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "unused"}
	notifier := &stubNotifier{}
	labels := &mockIterateLabelManager{}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithIterateStore(s),
		WithIterateNotifier(notifier),
		WithIterateLabels(labels),
	)
	now := time.Now()
	raw := `{"type":"result","result":"{\"fixes\":[{\"action\":\"modified\"},{\"action\":\"alternative_chosen\"}],\"summary\":\"approved after fixes\"}"}`
	fixTask := &model.TaskRecord{
		ID:           "fix-review-approved-unpersisted",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusSucceeded,
		Result:       raw,
		RepoFullName: "org/repo",
		PRNumber:     51,
		Payload: model.TaskPayload{
			FixReportPath: "docs/review_history/51-round2.md",
		},
	}
	seedRecord(s, fixTask)
	s.iterationSession = &store.IterationSessionRecord{
		ID:               45,
		RepoFullName:     "org/repo",
		PRNumber:         51,
		Status:           "fixing",
		CurrentRound:     2,
		MaxRounds:        4,
		TotalIssuesFixed: 0,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          61,
		SessionID:   45,
		RoundNumber: 2,
		FixTaskID:   fixTask.ID,
	}
	s.completedIterationRounds = []*store.IterationRoundRecord{
		{
			ID:          60,
			SessionID:   45,
			RoundNumber: 1,
			IssuesFixed: 1,
			CompletedAt: &now,
		},
	}
	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     51,
		PRTitle:      "Fix approval",
	}

	h.AfterIterationApproved(context.Background(), payload)

	if s.iterationSession.Status != "completed" {
		t.Fatalf("session status = %q, want completed", s.iterationSession.Status)
	}
	if s.iterationSession.TotalIssuesFixed != 3 {
		t.Fatalf("TotalIssuesFixed = %d, want 3", s.iterationSession.TotalIssuesFixed)
	}
	if s.latestIterationRound.CompletedAt == nil {
		t.Fatal("应在完成会话前自愈补齐最新轮次 completed_at")
	}
	if s.latestIterationRound.FixReportPath != fixTask.Payload.FixReportPath {
		t.Fatalf("FixReportPath = %q, want %q", s.latestIterationRound.FixReportPath, fixTask.Payload.FixReportPath)
	}
	if len(notifier.messages) != 1 || notifier.messages[0].EventType != notify.EventIterationPassed {
		t.Fatalf("terminal notifications = %+v, want iteration.passed", notifier.messages)
	}
	assertIterationNotificationMetadata(t, notifier.messages[0], "Fix approval")
	if labels.removeCalls != 1 || labels.addCalls != 1 {
		t.Fatalf("label calls remove=%d add=%d, want 1/1", labels.removeCalls, labels.addCalls)
	}
}

func TestAfterIterationApproved_DoesNotCompleteBeforeFixReviewPersisted(t *testing.T) {
	s := newMockStore()
	mc := &mockEnqueuer{enqueuedID: "unused"}
	notifier := &stubNotifier{}
	labels := &mockIterateLabelManager{}
	h := NewEnqueueHandler(mc, nil, s, slog.Default(),
		WithIterateStore(s),
		WithIterateNotifier(notifier),
		WithIterateLabels(labels),
	)
	h.roundWaitTimeout = time.Millisecond
	h.roundPollInterval = time.Millisecond

	s.iterationSession = &store.IterationSessionRecord{
		ID:               46,
		RepoFullName:     "org/repo",
		PRNumber:         52,
		Status:           "fixing",
		CurrentRound:     1,
		MaxRounds:        3,
		TotalIssuesFixed: 0,
	}
	s.latestIterationRound = &store.IterationRoundRecord{
		ID:          62,
		SessionID:   46,
		RoundNumber: 1,
		FixTaskID:   "fix-review-still-running",
	}
	seedRecord(s, &model.TaskRecord{
		ID:           "fix-review-still-running",
		TaskType:     model.TaskTypeFixReview,
		Status:       model.TaskStatusRunning,
		RepoFullName: "org/repo",
		PRNumber:     52,
	})
	payload := model.TaskPayload{
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     52,
	}

	h.AfterIterationApproved(context.Background(), payload)

	if s.iterationSession.Status == "completed" {
		t.Fatal("上一轮 fix_review 未完成落库时不应完成迭代会话")
	}
	if s.updateIterationSessionCalls != 0 {
		t.Fatalf("UpdateIterationSession 调用次数 = %d, want 0", s.updateIterationSessionCalls)
	}
	if len(notifier.messages) != 0 {
		t.Fatalf("不应发送终态通知，messages=%+v", notifier.messages)
	}
	if labels.removeCalls != 0 || labels.addCalls != 0 {
		t.Fatalf("不应更新终态标签，remove=%d add=%d", labels.removeCalls, labels.addCalls)
	}
}
