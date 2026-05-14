package queue

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/e2e"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

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
