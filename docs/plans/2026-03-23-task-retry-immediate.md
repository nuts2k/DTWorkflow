# task retry 立即重入队 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 让 `dtworkflow task retry <task-id>` 对 `failed` / `cancelled` 任务立即重新入队，并在成功后将状态更新为 `queued`，不再依赖 RecoveryLoop 的延迟补偿。

**Architecture:** 在 `internal/cmd/task.go` 中为 task 命令增加 queue client 和 `redis-addr` 配置，复用现有 `queue.Client.Enqueue(...)` 立即重新入队任务。重试成功后同步更新 SQLite 任务状态；若遇到 TaskID 冲突则视为已在队列中并同步为 `queued`，若 SQLite 更新失败则返回“可能已入队但状态同步失败”的错误。

**Tech Stack:** Go, Cobra, asynq, SQLite, Redis, 现有 `internal/queue` / `internal/store` 抽象

---

### Task 1: 为 task 命令增加 queue client 与 redis-addr 配置

**Files:**
- Modify: `internal/cmd/task.go`
- Create: `internal/cmd/task_test.go`

**Step 1: 写失败测试**

在 `internal/cmd/task_test.go` 新建测试文件，先补最小测试，验证 task 命令初始化时能够持有 queue client 所需配置。例如：

```go
func TestTaskCommand_HasRedisAddrFlag(t *testing.T) {
    flag := taskCmd.PersistentFlags().Lookup("redis-addr")
    if flag == nil {
        t.Fatal("task command should define --redis-addr flag")
    }
}
```

再补一个更直接的测试，要求 `PersistentPreRunE` 在 store 初始化之外也会初始化 queue client 所需的全局依赖（建议通过抽出 helper 便于测试，见 Step 3）。

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/cmd -run 'TestTaskCommand_HasRedisAddrFlag|TestTaskCommand_InitDependencies' -v`
Expected: FAIL，因为当前没有 `--redis-addr`，也没有 queue client 依赖。

**Step 3: 写最小实现**

在 `internal/cmd/task.go`：

1. 新增包级变量：

```go
var taskQueueClient *queue.Client
var taskRedisAddr string
```

2. 新增 `--redis-addr` flag，默认值复用：

```go
getEnvDefault("DTWORKFLOW_REDIS_ADDR", "localhost:6379")
```

3. 抽一个小 helper，避免把初始化逻辑直接写死在 `PersistentPreRunE`，便于测试：

```go
func initTaskDeps(dbPath, redisAddr string) (store.Store, *queue.Client, error)
```

helper 里：
- `store.NewSQLiteStore(dbPath)`
- `queue.NewClient(asynq.RedisClientOpt{Addr: redisAddr})`

4. `PersistentPreRunE` 调用 helper 给 `taskStore` / `taskQueueClient` 赋值。

5. `PersistentPostRunE` 里按顺序关闭 `taskQueueClient` 和 `taskStore`。

**Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd -run 'TestTaskCommand_HasRedisAddrFlag|TestTaskCommand_InitDependencies' -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/cmd/task.go internal/cmd/task_test.go
git commit -m "feat: initialize queue client for task commands"
```

---

### Task 2: 先写 failed 任务立即 retry 成功的失败测试

**Files:**
- Modify: `internal/cmd/task_test.go`
- Modify: `internal/cmd/task.go`
- Reference: `internal/queue/enqueue.go:182-245`

**Step 1: 写失败测试**

在 `internal/cmd/task_test.go` 中补一个 task retry 行为测试。建议不要直接跑整个 Cobra CLI，先抽出可测试 helper，再用命令做薄封装。

先写测试：

```go
func TestRetryTask_FailedTask_EnqueuesImmediately(t *testing.T) {
    s := newStubTaskStoreWithRecord(&model.TaskRecord{ ... Status: model.TaskStatusFailed ... })
    q := &stubTaskEnqueuer{asynqID: "asynq-123"}

    result, err := retryTask(context.Background(), s, q, "task-1")

    if err != nil { t.Fatalf(...) }
    if result.Status != model.TaskStatusQueued { ... }
    if q.called != 1 { ... }
}
```

为此建议抽出：

```go
type taskEnqueuer interface {
    Enqueue(ctx context.Context, payload model.TaskPayload, opts queue.EnqueueOptions) (string, error)
}

func retryTask(ctx context.Context, s store.Store, q taskEnqueuer, id string) (*model.TaskRecord, string, error)
```

测试断言：
- `Enqueue(...)` 被调用一次
- 使用 `record.Payload`
- 状态更新为 `queued`
- `AsynqID == "asynq-123"`
- `Error == ""`
- `RetryCount == 0`

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/cmd -run TestRetryTask_FailedTask_EnqueuesImmediately -v`
Expected: FAIL，因为还没有 `retryTask(...)` helper，也没有立即入队逻辑。

**Step 3: 写最小实现**

在 `internal/cmd/task.go` 中：

1. 定义窄接口：

```go
type taskEnqueuer interface {
    Enqueue(ctx context.Context, payload model.TaskPayload, opts queue.EnqueueOptions) (string, error)
}
```

2. 抽出 `retryTask(...)` helper，先只支持：
- 任务存在
- 状态为 `failed`
- 成功入队后更新为 `queued`

3. 复用队列层确定性 TaskID 规则。若无法直接调用 `buildAsynqTaskID`（未导出），本轮在 `internal/cmd/task.go` 新增一个小 helper，逻辑与其保持一致：

```go
func buildRetryTaskID(deliveryID string, taskType model.TaskType) string
```

只做同样的字符串拼接，不引入新的语义。

4. `retryTask(...)` 最小逻辑：
- 读 record
- 校验 `failed`
- `q.Enqueue(..., queue.EnqueueOptions{Priority: record.Priority, TaskID: buildRetryTaskID(...)})`
- 重置运行期字段
- 更新状态为 `queued`

**Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd -run TestRetryTask_FailedTask_EnqueuesImmediately -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/cmd/task.go internal/cmd/task_test.go
git commit -m "feat: retry failed tasks immediately"
```

---

### Task 3: 补齐 cancelled 任务立即 retry 成功

**Files:**
- Modify: `internal/cmd/task_test.go`
- Modify: `internal/cmd/task.go`

**Step 1: 写失败测试**

新增测试：

```go
func TestRetryTask_CancelledTask_EnqueuesImmediately(t *testing.T)
```

断言与 failed 类似，但输入状态为 `cancelled`。

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/cmd -run TestRetryTask_CancelledTask_EnqueuesImmediately -v`
Expected: FAIL（当前 helper 仅支持 failed）。

**Step 3: 写最小实现**

扩展 `retryTask(...)` 的状态校验，允许：
- `model.TaskStatusFailed`
- `model.TaskStatusCancelled`

错误信息保持和 CLI 一致。

**Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd -run TestRetryTask_CancelledTask_EnqueuesImmediately -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/cmd/task.go internal/cmd/task_test.go
git commit -m "feat: allow retrying cancelled tasks"
```

---

### Task 4: 补齐错误路径：非允许状态、入队失败、TaskID 冲突

**Files:**
- Modify: `internal/cmd/task_test.go`
- Modify: `internal/cmd/task.go`
- Reference: `internal/queue/recovery.go:133-168`

**Step 1: 写失败测试**

新增三个测试：

1. `TestRetryTask_InvalidStatus_ReturnsError`
2. `TestRetryTask_EnqueueFailure_ReturnsError`
3. `TestRetryTask_TaskIDConflict_TreatAsQueued`

重点断言：
- 非 `failed/cancelled` -> 返回错误，不更新状态
- 普通入队失败 -> 返回错误，不更新状态
- `asynq.ErrTaskIDConflict` -> 视为已入队，状态更新为 `queued`
- 冲突时 `AsynqID` 应等于确定性 TaskID

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/cmd -run 'TestRetryTask_InvalidStatus_ReturnsError|TestRetryTask_EnqueueFailure_ReturnsError|TestRetryTask_TaskIDConflict_TreatAsQueued' -v`
Expected: FAIL

**Step 3: 写最小实现**

在 `retryTask(...)` 中加入：

```go
if errors.Is(err, asynq.ErrTaskIDConflict) {
    asynqID = buildRetryTaskID(record.DeliveryID, record.TaskType)
} else if err != nil {
    return nil, "", fmt.Errorf("任务重新入队失败: %w", err)
}
```

并保持：
- 非允许状态直接返回 error
- 普通入队失败不修改 SQLite

**Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd -run 'TestRetryTask_InvalidStatus_ReturnsError|TestRetryTask_EnqueueFailure_ReturnsError|TestRetryTask_TaskIDConflict_TreatAsQueued' -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/cmd/task.go internal/cmd/task_test.go
git commit -m "fix: handle retry enqueue edge cases"
```

---

### Task 5: 补齐 SQLite 更新失败语义与字段重置细节

**Files:**
- Modify: `internal/cmd/task_test.go`
- Modify: `internal/cmd/task.go`

**Step 1: 写失败测试**

新增两个测试：

1. `TestRetryTask_ResetExecutionFields`
2. `TestRetryTask_UpdateStoreFailure_ReturnsSyncError`

测试 1 断言这些字段会被正确重置：
- `RetryCount = 0`
- `Error = ""`
- `StartedAt = nil`
- `CompletedAt = nil`
- `WorkerID = ""`

测试 2 断言：
- 入队成功
- SQLite 更新失败
- 返回错误文本包含“可能已重新入队，但状态同步失败”

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/cmd -run 'TestRetryTask_ResetExecutionFields|TestRetryTask_UpdateStoreFailure_ReturnsSyncError' -v`
Expected: FAIL

**Step 3: 写最小实现**

在 `retryTask(...)` 中重置字段：

```go
record.RetryCount = 0
record.Error = ""
record.StartedAt = nil
record.CompletedAt = nil
record.WorkerID = ""
record.Status = model.TaskStatusQueued
record.AsynqID = asynqID
record.UpdatedAt = time.Now()
```

若 `s.UpdateTask(...)` 失败：

```go
return nil, "", fmt.Errorf("任务可能已重新入队，但状态同步失败: %w", err)
```

**Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd -run 'TestRetryTask_ResetExecutionFields|TestRetryTask_UpdateStoreFailure_ReturnsSyncError' -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/cmd/task.go internal/cmd/task_test.go
git commit -m "fix: reset retry state fields consistently"
```

---

### Task 6: 将 retryTask helper 接入 Cobra 命令输出

**Files:**
- Modify: `internal/cmd/task.go`
- Modify: `internal/cmd/task_test.go`

**Step 1: 写失败测试**

补一个命令级测试（或轻量 helper 输出测试），验证 `task retry` 成功后输出 `queued` 而不是 `pending`。例如：

```go
func TestTaskRetryCommand_SuccessPrintsQueued(t *testing.T)
```

若当前命令级测试较重，可退一步测试返回结果 map 的构造 helper，例如：

```go
func buildRetryResult(record *model.TaskRecord, message string) map[string]any
```

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/cmd -run TestTaskRetryCommand_SuccessPrintsQueued -v`
Expected: FAIL

**Step 3: 写最小实现**

在 `taskRetryCmd.RunE` 中改为调用：

```go
record, message, err := retryTask(ctx, taskStore, taskQueueClient, id)
```

成功输出：
- `id`
- `status = queued`
- `message = 任务已重新入队` 或 `任务已在队列中，状态已同步为 queued`

默认人类可读输出示例：

```text
任务 <id> 已重新入队
当前状态: queued
```

**Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd -run TestTaskRetryCommand_SuccessPrintsQueued -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/cmd/task.go internal/cmd/task_test.go
git commit -m "feat: wire immediate retry into task command"
```

---

### Task 7: 做聚焦回归与全量验证

**Files:**
- Modify (if needed): `internal/cmd/task.go`
- Modify (if needed): `internal/cmd/task_test.go`

**Step 1: 运行 task/cmd 聚焦测试**

Run:

```bash
go test ./internal/cmd -run 'TestTask|TestRetryTask' -v
```

Expected: 所有 task 相关测试 PASS

**Step 2: 运行更广回归**

Run:

```bash
go test ./internal/cmd ./internal/queue ./internal/store -v
```

Expected: PASS

**Step 3: 运行全量测试**

Run:

```bash
go test ./...
```

Expected: 仓库所有 Go 包 PASS

**Step 4: 人工检查需求清单**

逐项核对：
- `failed` / `cancelled` 都支持立即 retry
- 成功后状态为 `queued`
- 普通入队失败不伪成功
- TaskID 冲突视为已在队列中
- SQLite 更新失败返回“可能已入队但状态同步失败”

**Step 5: 提交**

```bash
git add internal/cmd/task.go internal/cmd/task_test.go docs/plans/2026-03-23-task-retry-immediate-design.md docs/plans/2026-03-23-task-retry-immediate.md
git commit -m "feat: make task retry enqueue immediately"
```
