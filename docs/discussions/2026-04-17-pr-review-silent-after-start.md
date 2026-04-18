# PR 评审任务只有"开始"通知之后静默的根因分析

日期：2026-04-17
状态：仅完成代码层面的根因假设（Phase 1），尚未连上远程服务器验证日志与 DB 记录。
下次继续：从"待验证"章节的日志/DB 检查开始，选定命中 A 还是 B 后再修代码。

## 一、问题现象（用户报告）

- 启动 PR 评审任务后，飞书仅收到第一条"开始评审"通知。
- 之后没有任何后续通知（重试中/失败/完成均没有）。
- 等了一个多小时，API 服务端也没有看到任何新的入站请求。
- 飞书通知链路本身是正常的（其它消息能正常发出）。
- 怀疑在中间某个时间点任务就"哑"掉了。

## 二、代码调查路径

调查的关键文件与行号（下次可直接跳转）：

- `internal/queue/processor.go`
  - `ProcessTask`（L147-375）：任务执行与状态流转主入口
  - `sendStartNotification`（L432-446）：仅在 `RetryCount == 0` 时发开始通知（L221-223）
  - `sendCompletionNotification`（L524-539）：完成/重试/失败通知发送
  - `handleSkipRetryFailure`（L816-835）：确定性失败路径，**会**发通知
  - `markTaskCancelled`（L837-858）：取消路径，**不**发通知
- `internal/notify/router.go`
  - `Router.Send`（L121-161）：第 139-142 行在 ctx 已取消时直接 `break`，一条通知都不发
- `internal/queue/options.go`
  - `TaskTimeout` / `defaultTaskTimeout`（L18-52）：asynq 任务级硬超时
    - `review_pr`: 10 分钟
    - `fix_issue`: 30 分钟
    - `analyze_issue`: 15 分钟
    - `gen_tests`: 20 分钟
- `internal/queue/client.go:174`：入队时通过 `asynq.Timeout(...)` 把硬超时塞入任务 option
- `internal/cmd/serve_deps.go:180-190`：`asynq.NewServer` 的 `asynq.Config` 未设全局 `Timeout`，只有 `Concurrency/Queues/RetryDelayFunc`
- `internal/review/service.go:89-194`：`review.Service.Execute`，容器执行通过 `pool.RunWithCommandAndStdin` 进行
- `internal/review/writeback.go:220-231`：Writer 写回前 staleness check，命中则返回 `ErrStaleReview`
- `internal/worker/pool.go:118-306`：容器生命周期 + stream monitor（活跃度超时 `ErrActivityTimeout` 与 ctx 取消分离）
- `internal/queue/enqueue.go:304-342`：`cancelTask` / `CancelProcessing` 路径，对运行中任务发送取消信号

## 三、两条会产生"静默"现象的代码路径

命中任何一条都会让用户看到"只有开始通知、之后完全没消息"。

### 缺陷 A：`markTaskCancelled` 完全没有发送通知

位置：`internal/queue/processor.go:837-858`

```go
func (p *Processor) markTaskCancelled(ctx context.Context, record *model.TaskRecord, reason string) error {
    record.Status = model.TaskStatusCancelled
    record.Error = reason
    ...
    // 原始 ctx 可能已取消；使用后台 context 落库，确保最终状态尽量持久化。
    bgCtx, bgCancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer bgCancel()
    if err := p.store.UpdateTask(bgCtx, record); err != nil { ... }

    return fmt.Errorf("%s: %w", reason, asynq.SkipRetry)
}
```

对照同文件 `handleSkipRetryFailure`（L816-835）是调用了 `p.sendCompletionNotification` 的，显然这里是遗漏。

**`markTaskCancelled` 被触发的位置**（均在 `ProcessTask` 中）：
- L250-251：`errors.Is(runErr, context.Canceled)` → 任务被取消
- L253-255：`errors.Is(runErr, review.ErrStaleReview)` → 评审已过时
- L286-288：`WriteDegraded` 过程中返回 `ErrStaleReview`

**典型触发场景**：
1. 同一 PR 新 push 进来 → `EnqueueHandler.cancelTask`（`internal/queue/enqueue.go:322`）调用 `CancelProcessing` → 老任务 ctx 被 cancel → `markTaskCancelled` → 老任务"开始"通知之后再无消息。
2. 服务被 SIGTERM/SIGINT 重启，正在跑的 handler 收到 ctx cancel。
3. 容器跑了很长时间，最后 Writer 写回时 staleness check 命中（说明期间已有更新任务入队）。

### 缺陷 B：`sendCompletionNotification` 复用 asynq ctx，已死 ctx 直接跳过发送

关键位置：
- `internal/queue/processor.go:360` 把 asynq handler 的 `ctx` 传进通知发送。
- `internal/notify/router.go:139-142`：

```go
for _, ch := range channels {
    if err := ctx.Err(); err != nil {
        errs = append(errs, fmt.Errorf("context 已取消，跳过剩余渠道: %w", err))
        break
    }
    ...
}
```

**当 ctx 已取消/超时，Router.Send 在循环一开始就 `break`，一条通知都发不出去**，上层只在 log 里写一条 `发送任务完成通知失败`。

**最常见触发**：asynq 任务级超时到期（review_pr 默认 10 分钟）。

完整剧本（review_pr 任务被卡住）：
1. 开始通知已发 ✅
2. Claude CLI 在容器内执行 > 10 分钟
3. asynq `Timeout` 到期 → ctx `DeadlineExceeded`
4. Docker `WaitContainer` 返回 → `runErr != nil`（不是 `context.Canceled`，不进 A 分支）
5. processor 走"错误路径" → `record.Status = Retrying` → 调用 `sendCompletionNotification(ctx, ...)`
6. Router.Send 看到 `ctx.Err() != nil` → **直接跳过，所有渠道都不发**
7. asynq 30s 后重试（`RetryCount == 1` 已 > 0），`sendStartNotification` 不再发开始通知
8. 重试又超时 → 再次试图发通知 → 仍然 ctx 死 → 仍然不发
9. 3 次重试耗尽后标 failed，仍然发不出去
10. 用户从始至终只看到最初的那一条开始通知

## 四、哪条路径更像用户遇到的？

两者都完全符合"只收到一条开始通知"。区分需要看日志/DB：

| 观察信号 | 命中 A（取消） | 命中 B（超时） |
| --- | --- | --- |
| 日志含 `任务被取消` / `评审已过时` | 是 | 否 |
| 日志含 `发送任务完成通知失败` 且 error 是 `context canceled`/`deadline exceeded` | 可能（取决于是否到了发送这一步） | **通常是** |
| 日志含 `任务执行失败` 且错误含 `context deadline exceeded` | 否 | 是 |
| DB `tasks` 表最终 `status` | `cancelled` | `retrying` → `failed` |
| 重启服务或新 push 时间点能对齐 | 符合 | 否 |

用户额外报告"一个多小时没有任何请求" — 说明服务还在跑但没有新 webhook 进来，也没有自发重试成功 → **略倾向 B**（重试到 failed 后任务结束），但 A 不能排除（若同一时段有新 push 或服务重启）。

## 五、下次继续的验证步骤

进入远程服务器后按此顺序执行：

1. 查看最近 2 小时服务日志里以下关键字（按优先级）：
   ```sh
   grep -E "发送任务完成通知失败|任务被取消|评审已过时|更新取消任务状态|任务执行失败|context deadline exceeded|context canceled" <服务日志>
   ```
2. 查看 SQLite `tasks` 表中该 PR 任务的记录：
   ```sql
   SELECT id, task_type, status, retry_count, error,
          started_at, completed_at, updated_at
   FROM tasks
   WHERE repo_full_name = '<owner/repo>' AND pr_number = <N>
   ORDER BY created_at DESC LIMIT 5;
   ```
   - `status = cancelled` 且 `error = '任务被取消'`/`'评审已过时，被更新的任务取代'` → **缺陷 A**
   - `status = failed` 且 `error` 含 `deadline exceeded` → **缺陷 B**
   - `status = running` 且 `updated_at` 很老 → handler 崩溃/卡死，需另查（和 A/B 都不同）
3. 查看容器是否还活着：
   ```sh
   docker ps -a --filter "label=managed-by=dtworkflow" --format '{{.ID}} {{.Status}} {{.Labels}}'
   ```
   任务已 failed 但容器仍在 running，说明 ctx cancel 后容器没被真正 stop（另一个潜在问题）。
4. 确认 `asynq.Timeout` 的实际生效值 — 配置文件里 `worker.timeouts.review_pr` 是否被显式改过，或走了 `defaultTaskTimeout` 的 10 分钟。

## 六、拟议修复（定位完再动工）

### 修 A：`markTaskCancelled` 补发通知
- 为 `cancelled` 状态在 `buildNotificationMessage` 中补对应分支（目前只处理 succeeded/failed/retrying，见 L547-552）。
- `markTaskCancelled` 内部在 `UpdateTask` 成功后调用 `sendCompletionNotification`，使用后台 ctx（参见下一条）。

### 修 B：通知发送脱离 asynq ctx
- `sendCompletionNotification`（以及修 A 后在 `markTaskCancelled` 补的调用）内部用独立 ctx：
  ```go
  notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()
  ```
- 这样即便 asynq ctx 已 cancel/deadline，也能发出最终状态通知。
- `sendStartNotification` 在当前 RetryCount==0 情形下通常 ctx 还活着，但为一致性建议一并迁移。

### 修 C：超时值再评估（可选）
- `review_pr` 默认 10 分钟对 Claude CLI 实际执行时长过短，真实 PR 容易超时。
- 建议配置化并把默认值抬到 20-30 分钟；同时保留 `worker.stream_monitor.activity_timeout` 作为卡死判据。

### 测试补全
- 新增 processor 单元测试，覆盖：
  1. `context.Canceled` 路径应发出 cancelled 通知（覆盖修 A）。
  2. asynq ctx 已 DeadlineExceeded 时，completion 通知仍能发出（覆盖修 B，需要把 asynq ctx 替换为 notifyCtx 后的断言）。
- Router.Send 现有 `TestRouter_Send_ContextCancelled`（`internal/notify/router_test.go:301`）表明"ctx 死则跳过"是刻意行为 → 修 B 要改的是调用侧，而不是 Router 行为。

## 七、排除过的路径（供下次回忆）

- **Start 通知本身失败**：已排除。用户确认第一条能收到，说明 `p.notifier.Send` 在 RetryCount==0 时成功。
- **worker.Pool 容器活跃度超时 `ErrActivityTimeout`**：该错误不是 `context.Canceled`，会走正常错误路径；正常错误路径下 ctx 仍可能已被取消（如果外层 asynq 也到期），所以仍可能命中缺陷 B。单独活跃度超时（<2min）并不解释"1 小时静默"。
- **stale review webhook 触发**：只有"同 PR 新 push"才触发，用户说 1 小时无任何入站请求 → 基本可排除 stale 触发。
- **`handleSkipRetryFailure` 分支**：该函数本身有通知，不在嫌疑范围。
- **通知配置/路由规则问题**：用户明确说飞书通知链路正常。

## 八、相关上下文（便于下次 onboarding）

- asynq Config 未设全局 `Timeout`（`internal/cmd/serve_deps.go:180-190`），所有硬超时来自入队选项 `asynq.Timeout(TaskTimeout(taskType, timeouts))`。
- `TaskRetryDelay` 指数退避：30s, 60s, 120s, 240s...（`internal/queue/options.go:65` 起）。
- 开始通知只发一次（`processor.go:221` 以 `RetryCount==0` 守护），所以重试阶段天然没有开始通知。
- Router.Send "ctx 已取消则跳过"是设计上的快速失败，不应改；调用方需自带健壮 ctx。

---

下次接上时，从第五章"下次继续的验证步骤"第 1、2 步开始即可。
