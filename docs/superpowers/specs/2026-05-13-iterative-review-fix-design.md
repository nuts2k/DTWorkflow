# 迭代式 PR 评审修复设计

## 概述

自动化 PR 评审→修复→再评审的迭代闭环。当 DTWorkflow 评审发现阻碍合并的问题时，自动在容器中修复并 push 新提交，触发重新评审，循环直到通过或达到迭代上限。

## 触发条件

- PR 上存在 `auto-iterate` 标签（用户/程序手动设置，系统不主动移除）
- `review_pr` 完成后 verdict 为 `request_changes`
- 全局配置 `iterate.enabled` 为 true
- 标签可在 PR 创建时由程序自动设置，确保全自动化流程

## 整体数据流

```
用户/程序 给 PR 加 auto-iterate 标签
          │
          ▼
  ┌─── review_pr ◄──────────────────────┐
  │   (现有流程，无改动)                  │
  │       │                              │
  │       ▼                              │
  │  verdict = approve?                  │
  │   ├── 是 → 终态通知 + 标签更新        │
  │   └── 否 → 查询迭代状态               │
  │              │                       │
  │         超过上限? ──是→ 终态通知       │
  │              │                       │
  │             否                       │
  │              ▼                       │
  │       入队 fix_review                │
  │          │                           │
  │          ▼                           │
  │    容器内修复 + push                  │
  │    + 写修复报告到仓库                  │
  │    + PR 评论摘要                      │
  │          │                           │
  │          ▼                           │
  │    synchronized 事件 ────────────────┘
  │    (Gitea Webhook 自动触发)
```

## 区分 fix_review push 与用户 push

fix_review 容器内 push 会触发 `synchronized` 事件。如果不加区分，`handleReviewPullRequest` 会对该事件执行 Cancel-and-Replace，可能取消尚未完成状态写入的 fix_review 任务。

**解决方案：基于会话状态判断**

push 在容器内完成，host 侧无法保证在 webhook 到达前写入标记（存在时序竞态）。因此不使用 SHA 匹配，而是**基于迭代会话的 `fixing` 状态**来判断 push 来源：

1. `fix_review` 入队时，会话状态已更新为 `fixing`
2. `handleReviewPullRequest` 收到 `synchronized` 事件时，查询该 PR 是否有活跃迭代会话且状态为 `fixing`
3. 状态为 `fixing` → 这是 fix_review 的 push：仅取消旧 review_pr，**跳过对 fix_review 的 Cancel-and-Replace**
4. 无活跃会话或状态非 `fixing` → 这是用户手动 push：执行完整的 Cancel-and-Replace（取消 review_pr + fix_review）

该方案无时序竞态：会话状态在 fix_review 入队时就已写入 DB，远早于容器启动和 push。

```go
func (e *Enqueuer) handleReviewPullRequest(ctx, event) {
    // ... 现有逻辑 ...

    session := store.FindActiveIterationSession(repo, prNumber)
    isFixPush := session != nil && session.Status == "fixing"

    // 1. 先入队新 review_pr（遵循"先持久化再取消"模式）
    enqueueReviewPR(...)

    // 2. 再取消旧任务
    if isFixPush {
        // fix_review 自身的 push，仅取消旧 review_pr
        cancelActiveTasks(repo, pr, []TaskType{TaskTypeReviewPR})
        // 更新会话状态：fixing → reviewing
        store.UpdateSessionStatus(session.ID, "reviewing")
    } else {
        // 用户手动 push 或无迭代会话，取消 review_pr + fix_review
        cancelActiveTasks(repo, pr, []TaskType{TaskTypeReviewPR, TaskTypeFixReview})
    }
}
```

## 迭代状态机

每个 PR 的迭代会话有以下状态：

```
idle → reviewing → fixing → reviewing → fixing → ... → completed / exhausted
```

- **idle**：标签已加但尚未开始
- **reviewing**：review_pr 正在执行
- **fixing**：fix_review 正在执行
- **completed**：评审通过（verdict=approve）或用户手动移除标签
- **exhausted**：达到迭代上限仍未通过

## 数据模型

### iteration_sessions 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 自增 |
| repo_full_name | TEXT | 仓库全名 |
| pr_number | INTEGER | PR 号 |
| head_branch | TEXT | PR 源分支 |
| status | TEXT | idle/reviewing/fixing/completed/exhausted |
| current_round | INTEGER | 当前轮次（从 1 开始） |
| max_rounds | INTEGER | 上限（来自配置） |
| total_issues_found | INTEGER | 累计发现的问题数 |
| total_issues_fixed | INTEGER | 累计修复的问题数 |
| last_error | TEXT | 最近一次错误信息，加速故障排查 |
| created_at | DATETIME | 创建时间 |
| updated_at | DATETIME | 更新时间 |

唯一约束：使用 partial unique index 确保同一 PR 同时只有一个活跃会话，但不阻止终态会话后创建新会话：

```sql
CREATE UNIQUE INDEX idx_iteration_sessions_active
  ON iteration_sessions(repo_full_name, pr_number)
  WHERE status NOT IN ('completed', 'exhausted');
```

`FindOrCreateIterationSession` 仅查找非终态会话，终态会话保留为历史记录。

### iteration_rounds 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER PK | 自增 |
| session_id | INTEGER FK | 关联 iteration_sessions |
| round_number | INTEGER | 轮次号 |
| review_task_id | TEXT | 对应 review_pr 的 task_id |
| fix_task_id | TEXT | 对应 fix_review 的 task_id |
| issues_found | INTEGER | 本轮发现的问题数 |
| issues_fixed | INTEGER | 本轮修复的问题数 |
| fix_report_path | TEXT | 仓库内修复报告路径 |
| is_recovery | INTEGER NOT NULL DEFAULT 0 | 是否为恢复轮次（不计入 max_rounds 限额） |
| started_at | DATETIME | 本轮开始时间 |
| completed_at | DATETIME | 本轮完成时间 |

唯一约束：`UNIQUE(session_id, round_number)`，防止重试或竞态下同一轮次插入多行。

### 数据库迁移

新增迁移版本 v24（`DisableForeignKeys: true`）：

1. 创建 `iteration_sessions` 表（含唯一约束）
2. 创建 `iteration_rounds` 表（含唯一约束 + 外键）
3. 重建 `tasks` 表，CHECK 约束追加 `'fix_review'`

## fix_review 任务设计

### 任务 Payload（合并到 model.TaskPayload）

fix_review 特有字段合并到现有 `model.TaskPayload` 扁平结构体，与其他任务类型做法一致：

```go
// model.TaskPayload 新增字段
SessionID     int64  `json:"session_id,omitempty"`
RoundNumber   int    `json:"round_number,omitempty"`
ReviewIssues  string `json:"review_issues,omitempty"`  // JSON 序列化的 []review.ReviewIssue
PreviousFixes string `json:"previous_fixes,omitempty"` // JSON 序列化的 []iterate.FixSummary
```

`ReviewIssues` 和 `PreviousFixes` 使用 JSON 字符串避免 `model.TaskPayload` 引入 `review`/`iterate` 包的类型依赖。`iterate.Service.Execute` 内部反序列化为强类型。

### 修复结果 JSON Schema

以下类型定义在 `internal/iterate/result.go`：

```go
type FixReviewOutput struct {
    Fixes   []FixItem `json:"fixes"`
    Summary string    `json:"summary"`
}

type FixItem struct {
    File         string        `json:"file"`
    Line         int           `json:"line"`
    IssueRef     string        `json:"issue_ref"`
    Severity     string        `json:"severity"`
    Action       string        `json:"action"`        // modified / skipped / alternative_chosen
    What         string        `json:"what"`
    Why          string        `json:"why"`
    Alternatives []Alternative `json:"alternatives,omitempty"`
}

type Alternative struct {
    Description string `json:"description"`
    Pros        string `json:"pros"`
    Cons        string `json:"cons"`
    WhyNot      string `json:"why_not"`
}
```

### 容器执行流程

1. 克隆仓库，checkout 到 PR 的 head 分支
2. 构造 prompt：评审问题列表 + 前几轮修复上下文 + 修复指引
3. Claude Code 执行修复 + 生成修复报告 markdown 文件
4. 容器内将修复代码和报告文件合并为**同一个 commit** + push（单次 push，避免触发多个 synchronized 事件）
5. 解析修复结果（结构化 JSON）
6. Host 侧接收结果后写入 DB

### Token 和凭证

- 容器使用 fix 账号凭证（需要 push 权限）
- 需同步扩展 `worker.PoolConfig` + `selectGiteaToken` + `buildContainerEnv`

### 超时配置

fix_review 默认超时 20 分钟，需扩展 `TaskTimeoutsConfig` 和 `Lookup` 方法：

```go
// TaskTimeoutsConfig 新增
FixReview time.Duration

// defaultTaskTimeout 新增
case model.TaskTypeFixReview:
    return 20 * time.Minute
```

### 任务优先级

fix_review 使用 `PriorityHigh`（与 review_pr 一致），保证迭代闭环不被低优先级任务阻塞。

### 容器镜像

fix_review 使用 `ImageFull`（有 push 权限），需在 `resolveImage` 中新增 `case model.TaskTypeFixReview: return ImageFull`。

### 错误脱敏

fix_review 执行 Claude Code，其输出中的自由文本字段（`FixReviewOutput.Summary`、`FixItem.What/Why` 等）在写入 error、飞书通知、PR 评论时必须经 `SanitizeErrorMessage` 或等效脱敏函数处理，遵循 CLAUDE.md 跨切面约定。

## review_pr 扩展

### 标签数据传递

`ReviewResult` 新增 `Labels []string` 字段，`review.Service` 在获取 PR 信息时顺带填充。避免为 Processor 增加 Gitea API 依赖。

```go
// review/result.go
type ReviewResult struct {
    // ... 现有字段 ...
    Labels []string // PR 标签列表，用于迭代链式入队判断
}
```

### 链式入队逻辑

在结果处理阶段尾部追加，不修改核心执行逻辑：

```go
func (p *Processor) afterReviewCompleted(ctx, task, result) {
    if result.Review == nil || result.Review.Verdict != "request_changes" { return }
    if !containsLabel(result.Labels, config.Iterate.Label) { return }
    if !config.Iterate.Enabled { return }

    // 按严重等级过滤需要修复的问题
    filteredIssues := filterBySeverity(result.Review.Issues, config.Iterate.FixSeverityThreshold)
    if len(filteredIssues) == 0 { return } // 无需修复的阻塞问题

    session := store.FindOrCreateIterationSession(repo, prNumber)

    // 仅统计非 recovery 轮次，recovery 不计入限额
    nonRecoveryRounds := store.CountNonRecoveryRounds(session.ID)
    if nonRecoveryRounds >= session.MaxRounds {
        session.Status = "exhausted"
        notify(exhausted)
        return
    }

    // 标记抑制本次 review_pr 的独立通知
    suppressNotification = true

    enqueueFixReview(session, filteredIssues)
}
```

### 通知抑制机制

在 `afterReviewCompleted` 和 `afterFixReviewCompleted` 中设置 `suppressNotification` 标记。Processor 在调用 `sendCompletionNotification` 前检查此标记：

```go
// processor.go ProcessTask 尾部
if !suppressNotification {
    p.sendCompletionNotification(ctx, record, result)
}
```

迭代进度通知和终态通知由 `iterate.Service` 通过 `notify` 包直接发送，不走 Processor 的通用通知路径。

## 修复报告与留档

### 仓库内修复报告

路径：`docs/review_history/{pr_number}-{date}-round{N}.md`

修复代码和报告文件在容器内合并为同一个 commit，单次 push。

报告包含每个问题的处理结果：
- **modified**：直接修复，记录修改内容和决策依据
- **alternative_chosen**：选择了备选方案，必须列出所有考虑过的方案（含描述、优劣势、未选原因）
- **skipped**：跳过并说明原因

Prompt 明确要求：任何存在多种合理修改方式的问题，必须列出所有考虑过的方案及取舍理由。

### PR 评论摘要

每轮修复后在 PR 评论中发精简摘要，指向详细报告文件路径。

### 飞书通知

终态时发汇总通知（通过/超限），每轮进度通知根据配置决定。

## 通知策略

### 可配置模式

- **progress**（默认）：每轮修复完成发进度通知 + 终态汇总
- **silent**：迭代过程静默，仅终态汇总 + 基础设施异常即时告警

### 通知内容示例

进度通知：「迭代 1/3，修复了 3 个问题，待重新评审」

通过终态：「✅ PR #42 迭代完成：评审通过，共 2 轮修复 5 个问题」

超限终态：「⚠️ PR #42 迭代修复达到上限，3 轮后仍有 2 个问题，需人工介入」

## 配置结构

```yaml
iterate:
  enabled: false                    # 总开关，默认关闭
  max_rounds: 3                     # 迭代上限
  label: "auto-iterate"             # 触发标签名
  notification_mode: "progress"     # progress / silent
  fix_severity_threshold: "error"   # 修复 ERROR 及以上等级
  report_path: "docs/review_history" # 修复报告存放目录
```

支持仓库级 `.dtworkflow.yaml` 覆盖 `max_rounds`、`fix_severity_threshold` 等字段。

## Gitea 标签管理

| 时机 | 标签操作 |
|------|----------|
| 迭代开始（首次 fix_review 入队） | 添加 `iterating` |
| 评审通过 | 移除 `iterating`，添加 `iterate-passed` |
| 达到上限 | 移除 `iterating`，添加 `iterate-exhausted` |
| 用户移除 `auto-iterate` | 下一轮 review 后自然停止 |

`auto-iterate` 由用户/程序控制，系统不主动移除。

**责任组件与账号：**
- 标签操作由 `iterate.Service` 通过 review 账号的 Gitea client 执行（与评审评论同账号，只需读写标签权限）
- 新增 notify EventType 常量：`EventIterationProgress`（进度通知）、`EventIterationPassed`（通过终态）、`EventIterationExhausted`（超限终态）、`EventIterationError`（基础设施异常）

## 边界情况与防护

### 防自触发循环

迭代会话的 round 计数 + max_rounds 上限控制，超限自动停止。

### Cancel-and-Replace

**区分 push 来源（见"区分 fix_review push 与用户 push"章节）：**
- fix_review 自身 push：仅取消旧 review_pr，不取消 fix_review（已完成或正在收尾）
- 用户手动 push：取消 review_pr + fix_review

**扩展 Cancel-and-Replace 取消范围：** 现有 `FindActivePRTasks` 接受单个 `taskType model.TaskType`。需新增 `FindActivePRTasksMulti(ctx, repoFullName, prNumber string, taskTypes []model.TaskType)` 方法（避免修改现有签名破坏调用方），用户 push 场景下同时取消 `TaskTypeReviewPR` 和 `TaskTypeFixReview`。新方法定义在 `store/iterate_session.go` 中。

轮次计数在用户手动 push 时**不重置**，避免无限配额。

### 标签移除中断

已入队的 `fix_review` 执行完成，但下一轮 review 后检查标签不存在，不再链式入队。自然停止，无需特殊处理。

### 幂等保护

`fix_review` DeliveryID：`iterate-{session_id}:fix_review:{round_number}`，与现有 DeliveryID 前缀风格一致。

### 失败处理

- **超时/容器崩溃**：沿用 asynq 重试机制（最多 3 次，指数退避 30s/60s/120s），重试耗尽后发异常通知，`last_error` 字段记录错误摘要
- **确定性失败**（配置缺失、凭证错误）：`asynq.SkipRetry`，立即停止
- **修复质量失败**（无法解析、无实际变更）：`SkipRetry`，记录 `issues_fixed=0`，继续下一轮。连续两轮零修复提前终止（`exhausted`）。判定逻辑在 `afterFixReviewCompleted` 中：查询最近两个 round 的 `issues_fixed`，均为 0 时设置 `session.Status = "exhausted"` 并发终态通知

### 手动恢复

重试全部失败后，迭代会话卡在 `fixing` 状态。三种恢复方式：

1. **push 新提交** → `synchronized` → `handleReviewPullRequest` 发现会话状态为 `fixing`（但对应的 fix_review 任务已耗尽重试）→ 取消卡住的任务 → 正常入队 `review_pr` → `afterReviewCompleted` 检测到会话状态为 `fixing` → 重置为 `reviewing`，创建新 round（标记为 `is_recovery = true`），不沿用失败轮次的 round_number
2. **关闭再 reopen PR** → `reopened` 事件 → 同上
3. **CLI 命令** `dtworkflow iterate retry --repo X --pr Y` → 直接重置会话状态 + 重新入队 `fix_review`

恢复创建的新 round 使用下一个递增 round_number，但在 `iteration_rounds` 表中标记为 `is_recovery = true`，不计入 max_rounds 限额（给用户一次恢复机会）。

## 模块划分

```
internal/
  iterate/              # 新增包
    service.go          # 核心业务逻辑
    result.go           # FixReviewOutput / FixItem / Alternative 类型定义
    prompt.go           # Prompt 模板构造
    report.go           # 修复报告 markdown 生成
    errors.go           # 错误定义
    session.go          # 迭代会话状态管理
  store/
    iterate_session.go  # 新增：两表 CRUD + FindActivePRTasksMulti 方法
    migrations.go       # 新增 v24 迁移（两表创建 + partial unique index + tasks CHECK 约束更新）
  queue/
    processor.go        # 扩展：fix_review 处理分支 + suppressNotification 标记 + IterateExecutor 窄接口
    enqueue.go          # 扩展：enqueueFixReview + handleReviewPullRequest 中会话状态检查
  review/
    result.go           # 扩展：ReviewResult 新增 Labels 字段
    service.go          # 最小改动：填充 Labels 字段
  worker/
    pool.go             # 扩展：PoolConfig 新增 FixReview
    types.go            # 扩展：TaskTimeoutsConfig 新增 FixReview
  model/
    task.go             # 新增 TaskTypeFixReview + TaskPayload 新增迭代字段
  config/
    config.go           # 新增 iterate 配置段
```

核心原则：iterate 是独立包，review 和 queue 对它的依赖单向且最小。
