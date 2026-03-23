# M1.7 通知主流程接线 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在不引入 M1.8 配置管理的前提下，将现有通知框架接入 `serve -> queue.Processor -> notify.Router` 主链路，使 `review_pr` 和 `fix_issue` 任务在最终成功/失败后自动向对应 PR/Issue 发送 Gitea 评论通知。

**Architecture:** 在 `internal/cmd/serve.go` 的依赖构建阶段按最小闭环方案构造可选 `notify.Router`，并将其注入 `queue.Processor`。Processor 在任务最终状态落库后构造通知消息并尝试发送；通知失败仅记录日志，不改变任务执行状态或返回语义。`gen_tests` 因缺少稳定目标对象，本轮保持不发送通知。

**Tech Stack:** Go, Cobra, Gin, asynq, SQLite, 现有 `internal/notify` / `internal/gitea` 抽象

---

### Task 1: 为 Processor 增加可选通知依赖并锁定构造签名

**Files:**
- Modify: `internal/queue/processor.go`
- Modify: `internal/cmd/serve.go:327`
- Test: `internal/queue/processor_test.go`

**Step 1: 写一个失败测试，要求 Processor 能接收通知依赖**

在 `internal/queue/processor_test.go` 中新增一个最小 stub notifier，并增加一个测试覆盖新的构造签名，例如：

```go
type stubNotifier struct {
    messages []notify.Message
    err      error
}

func (s *stubNotifier) Send(_ context.Context, msg notify.Message) error {
    s.messages = append(s.messages, msg)
    return s.err
}
```

新增测试：使用 `NewProcessor(pool, store, notifier, logger)` 创建 Processor，确认不会 panic。

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/queue -run TestNewProcessor_WithNotifier -v`
Expected: 编译失败，因为 `NewProcessor` 还不接受 notifier 参数。

**Step 3: 做最小实现**

在 `internal/queue/processor.go`：
- 引入一个窄接口，例如：

```go
type TaskNotifier interface {
    Send(ctx context.Context, msg notify.Message) error
}
```

- 在 `Processor` 中增加字段：

```go
notifier TaskNotifier
```

- 修改构造函数签名：

```go
func NewProcessor(pool PoolRunner, store store.Store, notifier TaskNotifier, logger *slog.Logger) *Processor
```

- `pool` / `store` 仍然保持必需依赖；`notifier` 可为 nil。

同步修改 `internal/cmd/serve.go:327` 和现有测试中的调用点。

**Step 4: 运行测试确认通过**

Run: `go test ./internal/queue -run TestNewProcessor_WithNotifier -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/queue/processor.go internal/queue/processor_test.go internal/cmd/serve.go
git commit -m "feat: inject notifier into task processor"
```

---

### Task 2: 先写 review_pr 成功通知的失败测试

**Files:**
- Modify: `internal/queue/processor_test.go`
- Modify: `internal/queue/processor.go`

**Step 1: 写失败测试**

在 `internal/queue/processor_test.go` 新增测试 `TestProcessTask_Success_SendReviewNotification`：

```go
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
    record := seededRecord(...)
    pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0, Output: "review ok"}}

    p := NewProcessor(pool, s, notifier, slog.Default())
    err := p.ProcessTask(context.Background(), buildAsynqTask(t, payload))

    require.NoError(t, err)
    require.Len(t, notifier.messages, 1)
    msg := notifier.messages[0]
    assert.Equal(t, notify.EventPRReviewDone, msg.EventType)
    assert.Equal(t, int64(42), msg.Target.Number)
    assert.True(t, msg.Target.IsPR)
}
```

避免引入新断言库；沿用标准库 `if` 判断即可。

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/queue -run TestProcessTask_Success_SendReviewNotification -v`
Expected: FAIL，因为还没有发送通知逻辑。

**Step 3: 做最小实现**

在 `internal/queue/processor.go` 中新增私有辅助函数：
- `buildNotificationMessage(record *model.TaskRecord) (*notify.Message, bool)`
- `sendCompletionNotification(ctx context.Context, record *model.TaskRecord)`

先只支持：
- `review_pr` + `succeeded`

消息内容最小可用：

```go
notify.Message{
    EventType: notify.EventPRReviewDone,
    Severity:  notify.SeverityInfo,
    Target: notify.Target{
        Owner:  payload.RepoOwner,
        Repo:   payload.RepoName,
        Number: payload.PRNumber,
        IsPR:   true,
    },
    Title: "PR 自动评审任务完成",
    Body:  "...最小摘要...",
}
```

在 `ProcessTask` 中：
- `p.store.UpdateTask(...)` 之后
- 且 `record.Status == model.TaskStatusSucceeded || record.Status == model.TaskStatusFailed` 时
- 调用 `p.sendCompletionNotification(ctx, record)`

**Step 4: 运行测试确认通过**

Run: `go test ./internal/queue -run TestProcessTask_Success_SendReviewNotification -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/queue/processor.go internal/queue/processor_test.go
git commit -m "feat: notify on successful review tasks"
```

---

### Task 3: 补齐 review_pr 失败通知，并保证通知失败不影响主流程

**Files:**
- Modify: `internal/queue/processor_test.go`
- Modify: `internal/queue/processor.go`

**Step 1: 写失败测试**

新增两个测试：

1. `TestProcessTask_FailedReview_SendNotification`
2. `TestProcessTask_NotificationFailure_DoesNotAffectTaskResult`

测试 1 验证：
- `pool.Run` 返回错误
- 最终状态为 `failed`
- 发送 1 条通知
- `EventType == notify.EventSystemError`
- 标题为 `PR 自动评审任务失败`

测试 2 验证：
- notifier 返回错误
- `ProcessTask` 仍然返回原始执行错误
- store 中任务状态仍正确为 `failed`

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/queue -run 'TestProcessTask_FailedReview_SendNotification|TestProcessTask_NotificationFailure_DoesNotAffectTaskResult' -v`
Expected: FAIL

**Step 3: 做最小实现**

扩展 `buildNotificationMessage` 支持：
- `review_pr` + `failed`

文案：

```go
EventType: notify.EventSystemError
Severity:  notify.SeverityWarning
Title:     "PR 自动评审任务失败"
```

并确保 `sendCompletionNotification`：
- notifier 为 nil 时直接返回
- 构造失败时写 warning 日志并返回
- `Send` 失败时写 error 日志并返回
- 绝不覆盖 `ProcessTask` 原本的执行错误

**Step 4: 运行测试确认通过**

Run: `go test ./internal/queue -run 'TestProcessTask_FailedReview_SendNotification|TestProcessTask_NotificationFailure_DoesNotAffectTaskResult' -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/queue/processor.go internal/queue/processor_test.go
git commit -m "feat: notify on failed review tasks"
```

---

### Task 4: 为 fix_issue 成功/失败补齐通知映射

**Files:**
- Modify: `internal/queue/processor_test.go`
- Modify: `internal/queue/processor.go`

**Step 1: 写失败测试**

新增两个测试：
- `TestProcessTask_Success_SendFixIssueNotification`
- `TestProcessTask_FailedFixIssue_SendNotification`

成功路径断言：
- `EventType == notify.EventFixIssueDone`
- `Target.Number == IssueNumber`
- `Target.IsPR == false`
- 标题 `Issue 自动修复任务完成`

失败路径断言：
- `EventType == notify.EventSystemError`
- 标题 `Issue 自动修复任务失败`

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/queue -run 'TestProcessTask_Success_SendFixIssueNotification|TestProcessTask_FailedFixIssue_SendNotification' -v`
Expected: FAIL

**Step 3: 做最小实现**

扩展 `buildNotificationMessage`：
- 支持 `fix_issue` + `succeeded`
- 支持 `fix_issue` + `failed`

目标映射：

```go
notify.Target{
    Owner:  payload.RepoOwner,
    Repo:   payload.RepoName,
    Number: payload.IssueNumber,
    IsPR:   false,
}
```

正文中明确写：
- 成功：任务执行完成
- 不宣称已创建 PR

**Step 4: 运行测试确认通过**

Run: `go test ./internal/queue -run 'TestProcessTask_Success_SendFixIssueNotification|TestProcessTask_FailedFixIssue_SendNotification' -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/queue/processor.go internal/queue/processor_test.go
git commit -m "feat: notify on fix issue tasks"
```

---

### Task 5: 明确 gen_tests 不通知、无效目标不通知、retrying 不通知

**Files:**
- Modify: `internal/queue/processor_test.go`
- Modify: `internal/queue/processor.go`

**Step 1: 写失败测试**

新增三个测试：
- `TestProcessTask_GenTests_NoNotification`
- `TestProcessTask_InvalidTarget_NoNotification`
- `TestProcessTask_Retrying_NoNotification`

测试覆盖：
- `gen_tests` 非零/零退出码都不发送通知
- `review_pr` 缺少 `PRNumber` 时不发送通知
- `fix_issue` 缺少 `IssueNumber` 时不发送通知
- 非最终状态（如 `retrying`）不发送通知

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/queue -run 'TestProcessTask_GenTests_NoNotification|TestProcessTask_InvalidTarget_NoNotification|TestProcessTask_Retrying_NoNotification' -v`
Expected: FAIL

**Step 3: 做最小实现**

在 `buildNotificationMessage` 中：
- 对 `gen_tests` 返回 `(nil, false)`
- 对缺失 `RepoOwner` / `RepoName` / `PRNumber` / `IssueNumber` 的情况返回 `(nil, false)`
- 仅在 `record.Status` 为 `succeeded` 或 `failed` 时允许构造消息

**Step 4: 运行测试确认通过**

Run: `go test ./internal/queue -run 'TestProcessTask_GenTests_NoNotification|TestProcessTask_InvalidTarget_NoNotification|TestProcessTask_Retrying_NoNotification' -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/queue/processor.go internal/queue/processor_test.go
git commit -m "test: cover notification skip conditions"
```

---

### Task 6: 在 BuildServiceDeps 中构造最小可用 Router

**Files:**
- Modify: `internal/cmd/serve.go`
- Modify: `internal/cmd/adapter.go`
- Test: `internal/cmd/serve_test.go`

**Step 1: 写失败测试**

在 `internal/cmd/serve_test.go` 中新增两类轻量测试，建议直接测 `BuildServiceDeps`：

1. `TestBuildServiceDeps_WithGiteaConfig_BuildsNotifier`
2. `TestBuildServiceDeps_WithoutGiteaConfig_NotifierIsNil`

如当前 `BuildServiceDeps` 依赖 Docker / Redis 导致测试沉重，可先提炼一个小函数：

```go
func buildNotifier(giteaClient *gitea.Client) (*notify.Router, error)
```

然后测试该函数：
- `giteaClient == nil` -> `nil, nil`
- 有 client -> 返回非 nil router

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/cmd -run 'TestBuildServiceDeps_WithGiteaConfig_BuildsNotifier|TestBuildServiceDeps_WithoutGiteaConfig_NotifierIsNil' -v`
Expected: FAIL

**Step 3: 做最小实现**

在 `internal/cmd/serve.go`：
- 为 `ServiceDeps` 增加字段：

```go
Notifier *notify.Router
```

- 在 Gitea client 构造完成后增加 helper：

```go
func buildNotifier(giteaClient *gitea.Client) (*notify.Router, error)
```

最小实现逻辑：
- `nil client` -> return nil, nil
- 否则：
  - `adapter := &giteaCommentAdapter{client: giteaClient}`
  - `giteaNotifier, err := notify.NewGiteaNotifier(adapter, notify.WithLogger(slog.Default()))`
  - `router, err := notify.NewRouter(
        notify.WithNotifier(giteaNotifier),
        notify.WithRules([]notify.RoutingRule{{RepoPattern: "*", Channels: []string{"gitea"}}}),
        notify.WithFallback("gitea"),
        notify.WithRouterLogger(slog.Default()),
    )`

并把返回值塞入 `ServiceDeps.Notifier`。

**Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd -run 'TestBuildServiceDeps_WithGiteaConfig_BuildsNotifier|TestBuildServiceDeps_WithoutGiteaConfig_NotifierIsNil' -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/cmd/serve.go internal/cmd/adapter.go internal/cmd/serve_test.go
git commit -m "feat: build default notifier router in serve"
```

---

### Task 7: 把 Router 注入服务运行链路并跑完整测试集

**Files:**
- Modify: `internal/cmd/serve.go:327`
- Modify: `internal/queue/processor.go`
- Modify: `internal/queue/processor_test.go`
- Modify: `internal/cmd/serve_test.go`

**Step 1: 写最后一个集成向测试**

新增一个轻量测试，确认 `runServeWithConfig` 在构造 Processor 时使用了 `deps.Notifier`。如果不方便直接观察内部值，则通过以下替代方式：
- 让 `buildNotifier` 测试覆盖构造
- 让 `Processor` 测试覆盖通知行为
- 在本任务只做调用点接线并通过编译 + 全量测试验证

**Step 2: 运行测试并确认当前失败或未覆盖**

Run: `go test ./internal/cmd ./internal/queue -v`
Expected: 可能因构造签名未同步、未接线而失败。

**Step 3: 做最小实现**

在 `internal/cmd/serve.go:327` 修改为：

```go
processor := queue.NewProcessor(deps.Pool, deps.Store, deps.Notifier, slog.Default())
```

确认所有调用点、测试、编译都已同步。

**Step 4: 运行完整验证**

Run:

```bash
go test ./internal/queue ./internal/cmd -v
```

然后再运行更广一些的回归：

```bash
go test ./internal/notify ./internal/queue ./internal/cmd ./internal/worker ./internal/store -v
```

Expected:
- 所有相关测试 PASS
- 没有新增编译错误
- `notify`、`queue`、`cmd` 相关包均通过

**Step 5: 提交**

```bash
git add internal/cmd/serve.go internal/queue/processor.go internal/queue/processor_test.go internal/cmd/serve_test.go
git commit -m "feat: wire task notifications into serve pipeline"
```

---

### Task 8: 手工代码审查与收尾验证

**Files:**
- Review only: `internal/queue/processor.go`
- Review only: `internal/cmd/serve.go`
- Review only: `docs/plans/2026-03-23-notify-integration-design.md`
- Review only: `docs/plans/2026-03-23-notify-integration.md`

**Step 1: 检查消息语义是否准确**

逐项检查：
- `fix_issue` 成功文案是否只写“任务完成”，没有谎称“已创建修复 PR”
- `review_pr` / `fix_issue` 的失败文案是否清晰
- `gen_tests` 是否确实未发送通知

**Step 2: 检查失败隔离是否成立**

确认：
- 通知失败只写日志
- 不会覆盖 store 最终状态
- 不会替换 `ProcessTask` 的原始返回错误

**Step 3: 再跑一次聚焦验证**

Run:

```bash
go test ./internal/queue -run 'TestProcessTask_' -v
```

Expected: 全部 PASS

**Step 4: 最终回归**

Run:

```bash
go test ./...
```

如果仓库中有环境依赖导致全量测试不稳定，则至少保留：

```bash
go test ./internal/notify ./internal/queue ./internal/cmd ./internal/worker ./internal/store -v
```

**Step 5: 提交**

```bash
git add internal/queue/processor.go internal/queue/processor_test.go internal/cmd/serve.go internal/cmd/serve_test.go docs/plans/2026-03-23-notify-integration-design.md docs/plans/2026-03-23-notify-integration.md
git commit -m "feat: integrate notifications into task execution flow"
```
