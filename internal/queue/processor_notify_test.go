package queue

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/code"
	"otws19.zicp.vip/kelin/dtworkflow/internal/fix"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	testgen "otws19.zicp.vip/kelin/dtworkflow/internal/test"
)

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

	msg, ok := p.buildNotificationMessage(record, nil, fixResult, nil, nil, nil, nil)
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

	msg, ok := p.buildNotificationMessage(record, nil, fixResult, nil, nil, nil, nil)
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
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil, nil)
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
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil, nil)
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
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil, nil)
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
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil, nil)
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
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil, nil)
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

	msg, ok := p.buildNotificationMessage(record, nil, nil, tr, nil, nil, nil)
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
			msg, ok := p.buildNotificationMessage(record, nil, nil, tr, nil, nil, nil)
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
	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil, nil)
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
	p.sendCompletionNotification(context.Background(), record, nil, nil, tr, nil, nil, nil)
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

	p.sendCompletionNotification(context.Background(), record, nil, nil, tr, nil, nil, nil)

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

	p.sendCompletionNotification(context.Background(), record, nil, nil, tr, nil, nil, nil)

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
	p.sendCompletionNotification(context.Background(), record, nil, nil, nil, nil, nil, nil)
	if len(notifier.messages) != 1 {
		t.Fatalf("review 路径应发送 1 条通知，实际 %d", len(notifier.messages))
	}
	if notifier.messages[0].EventType != notify.EventPRReviewDone {
		t.Errorf("event = %q, want %q", notifier.messages[0].EventType, notify.EventPRReviewDone)
	}
}

func TestBuildStartMessage_CodeFromDoc(t *testing.T) {
	p := &Processor{}
	msg, ok := p.buildStartMessage(model.TaskPayload{
		TaskType:     model.TaskTypeCodeFromDoc,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		DocPath:      "docs/spec.md",
		DocSlug:      "spec",
	})
	if !ok {
		t.Fatal("buildStartMessage 应返回 true")
	}
	if msg.EventType != notify.EventCodeFromDocStarted {
		t.Errorf("EventType = %q, want %q", msg.EventType, notify.EventCodeFromDocStarted)
	}
	if msg.Target.IsPR {
		t.Fatal("code_from_doc started 应为仓库级通知")
	}
	if msg.Metadata[notify.MetaKeyDocPath] != "docs/spec.md" {
		t.Errorf("doc_path = %q", msg.Metadata[notify.MetaKeyDocPath])
	}
	if msg.Metadata[notify.MetaKeyBranchName] != "auto-code/spec" {
		t.Errorf("branch_name = %q", msg.Metadata[notify.MetaKeyBranchName])
	}
}

func TestBuildNotificationMessage_CodeFromDocDone(t *testing.T) {
	p := &Processor{}
	startedAt := time.Now()
	completedAt := startedAt.Add(2 * time.Minute)
	record := &model.TaskRecord{
		ID:          "task-code-1",
		TaskType:    model.TaskTypeCodeFromDoc,
		Status:      model.TaskStatusSucceeded,
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeCodeFromDoc,
			RepoOwner:    "owner",
			RepoName:     "repo",
			RepoFullName: "owner/repo",
			DocPath:      "docs/spec.md",
			DocSlug:      "spec",
			HeadRef:      "feature/spec",
		},
	}
	result := &code.CodeFromDocResult{
		PRNumber: 12,
		PRURL:    "https://gitea.example.com/owner/repo/pulls/12",
		Output: &code.CodeFromDocOutput{
			Success:         true,
			BranchName:      "feature/spec",
			FailureCategory: code.FailureCategoryNone,
			ModifiedFiles: []code.ModifiedFile{
				{Path: "a.go", Action: "created"},
				{Path: "b.go", Action: "modified"},
			},
			TestResults:    code.TestRunResults{Passed: 5, Failed: 0},
			Implementation: "实现了文档要求",
		},
	}

	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil, result)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}
	if msg.EventType != notify.EventCodeFromDocDone {
		t.Errorf("EventType = %q, want %q", msg.EventType, notify.EventCodeFromDocDone)
	}
	if msg.Metadata[notify.MetaKeyPRNumber] != "12" {
		t.Errorf("pr_number = %q", msg.Metadata[notify.MetaKeyPRNumber])
	}
	if msg.Metadata[notify.MetaKeyFilesCreated] != "1" || msg.Metadata[notify.MetaKeyFilesModified] != "1" {
		t.Errorf("文件计数 = created:%q modified:%q", msg.Metadata[notify.MetaKeyFilesCreated], msg.Metadata[notify.MetaKeyFilesModified])
	}
	if msg.Metadata[notify.MetaKeyTestPassed] != "5" || msg.Metadata[notify.MetaKeyTestFailed] != "0" {
		t.Errorf("测试计数 = passed:%q failed:%q", msg.Metadata[notify.MetaKeyTestPassed], msg.Metadata[notify.MetaKeyTestFailed])
	}
}

func TestBuildNotificationMessage_CodeFromDocFailedSeverity(t *testing.T) {
	p := &Processor{}
	record := &model.TaskRecord{
		ID:       "task-code-2",
		TaskType: model.TaskTypeCodeFromDoc,
		Status:   model.TaskStatusFailed,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeCodeFromDoc,
			RepoOwner:    "owner",
			RepoName:     "repo",
			RepoFullName: "owner/repo",
			DocPath:      "docs/spec.md",
			DocSlug:      "spec",
		},
	}
	result := &code.CodeFromDocResult{
		Output: &code.CodeFromDocOutput{
			Success:         false,
			FailureCategory: code.FailureCategoryInfoInsufficient,
		},
	}

	msg, ok := p.buildNotificationMessage(record, nil, nil, nil, nil, nil, result)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}
	if msg.EventType != notify.EventCodeFromDocFailed {
		t.Errorf("EventType = %q, want %q", msg.EventType, notify.EventCodeFromDocFailed)
	}
	if msg.Severity != notify.SeverityInfo {
		t.Errorf("Severity = %q, want %q", msg.Severity, notify.SeverityInfo)
	}
	if msg.Metadata[notify.MetaKeyFailureCategory] != string(code.FailureCategoryInfoInsufficient) {
		t.Errorf("failure_category = %q", msg.Metadata[notify.MetaKeyFailureCategory])
	}
}
