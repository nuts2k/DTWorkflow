# Fix-to-PR 异步解耦设计

- 日期：2026-04-21
- 范围：`internal/fix`、`internal/queue`、`internal/model`、`internal/store`、`internal/dtw`、`cmd/dtworkflow`
- 状态：设计定稿，待实施计划拆解

## 1. 背景与问题陈述

M3.5 的 `fix-to-pr` 链路当前采用**单任务一路到底**的结构：

```
fix_issue 任务
  └ 容器内：Claude 分析 → 改码 → push auto-fix/issue-N
  └ 容器外：createFixPR 通过 Gitea API 建 PR → 评论 Issue
```

近期在 `createFixPR` 一步反复踩到 Gitea 返回 500 的场景。围绕这点已经加了大量防御（`service.go` 参见最近 5 个 commit）：指数退避、body 控制字符清洗、title rune 截断、诊断段持久化、幂等查询。但这些都是**载荷侧**的加固，**没有命中根因**——整条昂贵任务（含 Claude 容器）在 PR 创建失败时会被整体 retry，调试成本巨大且根因仍未定位。

## 2. 诊断结论

关键观察：**程序运行中立刻调 `POST /pulls` 必现 500；同一分支分钟级后再调（body 随意写）必成功**。

据此排除：
- 不是 title/body/控制字符等载荷问题
- 不是 base/head/权限配置问题
- 不是偶发网络抖动

几乎锁定为：**Gitea 服务端在 `git push` 返回之后、PR API 可用之前存在一段异步就绪窗**。push 完成时 ref 已写入，但 pull request 候选、compare 索引、merge-base 缓存还在刷新，此时 `POST /pulls` 内部查 base…head diff / ref 一致性触发 panic 或 SQL 约束 → 500。

**推论**：当前 `service.go` 总重试窗口约 15 秒，远不足以覆盖分钟级就绪窗；继续在载荷侧加固无效。

## 3. 方案对比

### 方案 A — 同步延迟门 + 就绪探针（最小改动）

同步调用前轮询就绪信号（`GET /compare`、`GET /branches`），失败退避重试，窗口拉到 2–3 分钟。

- 优点：改动最小；保留现状。
- 缺点：每条任务同步拉长 30–120s；阻塞 worker 槽位；**重试失败仍然触发 Claude 容器重跑**（最昂贵环节）。
- 对症度：★★★☆☆

### 方案 B — 解耦 push 与 PR 创建（异步 `create_fix_pr` 任务）—— **采纳**

把 fix_issue 切成两段独立任务：`fix_issue`（Claude 执行 + push）+ `create_fix_pr`（纯 Gitea API）。PR 创建失败永远不回灌到 Claude 任务。

- 优点：彻底解耦 Claude 成本和 PR 可靠性；`create_fix_pr` 可从容长退避；状态可观测；未来切 webhook 触发只换消费者。
- 缺点：引入新 TaskType；用户感知时序分两步；持久化多一份 PR 元数据（几百字节）。
- 对症度：★★★★★

### 方案 C — Gitea push webhook 触发 PR 创建

容器只 push；监听 push webhook 触发 PR 创建。

- 致命缺点：**push webhook 到达和 PR API 就绪不是同一件事**，仍会踩 500；webhook 不可靠需要补扫；branch→Issue 映射脆弱。本质上不解决根因，只换触发源。
- 对症度：★★☆☆☆（除非叠加就绪探针 + 补扫，那时已退化成方案 B）

**选择方案 B，配合就绪探针 + 内置兜底扫描 worker。**

## 4. 方案 B 详细设计

### 4.1 整体架构与状态流转

```
Issue 打 fix-to-pr 标签
  │
  ▼
[ TaskType: fix_issue ]        ← 昂贵（Claude 容器）
  容器内：分析 → 改码 → push
  容器外：构造 PR 元数据 → enqueue create_fix_pr → 评论"修复已推送"
  本任务 Status = succeeded（Claude 链路到此结束，不再重跑）
  │
  ▼
[ TaskType: create_fix_pr ]    ← 廉价（纯 Gitea API）
  ProcessIn 延迟 3 分钟（避开就绪窗最急迫期）
  就绪探针（GET branch / GET compare）→ POST /pulls → 评论 PR 链接
  允许长退避、大重试窗口（约 1.5 小时）
  独立成败，不影响 fix_issue 的 succeeded 状态
```

**不新增 TaskStatus 枚举**；"pending_pr" 通过"父任务 succeeded + 子任务未完成"隐含表达。

### 4.2 CLI `--fix` 语义

- **默认立即返回 fix_issue task_id**（与 asynq 异步模型一致）。
- 新增 `--wait-pr` 显式同步等 PR 建成；`dtw wait` 通过 `parent_task_id` 轮询 `create_fix_pr` 的 succeeded 状态并打印 PR URL。
- 默认 `--wait-pr-timeout=10m`。

### 4.3 数据模型变更

**`internal/model/task.go`**：

```go
TaskTypeCreateFixPR TaskType = "create_fix_pr"    // 新增，priority=PriorityHigh

// TaskPayload 新增 create_fix_pr 专属字段（沿用平铺风格）：
ParentTaskID      string `json:"parent_task_id,omitempty"`
PRHead            string `json:"pr_head,omitempty"`
PRBase            string `json:"pr_base,omitempty"`      // 已解析完 tag 回退
PRTitle           string `json:"pr_title,omitempty"`
PRBody            string `json:"pr_body,omitempty"`
RefKind           string `json:"ref_kind,omitempty"`
ModifiedCount     int    `json:"modified_count,omitempty"`
ExpectedCommitSHA string `json:"expected_commit_sha,omitempty"`  // 就绪探针严格匹配用
```

**`TaskRecord`** 新增冗余列 `ParentTaskID`（与 `repo_full_name/pr_number` 同 pattern）。

### 4.4 SQLite migration

- 扩 `task_type` CHECK 约束到 `create_fix_pr`（照搬之前扩 `gen_daily_report` 的 pattern）。
- `ALTER TABLE tasks ADD COLUMN parent_task_id TEXT NOT NULL DEFAULT ''`。
- `CREATE INDEX idx_tasks_parent ON tasks(parent_task_id) WHERE parent_task_id != ''`。
- `CREATE INDEX idx_tasks_create_pr_pending ON tasks(task_type, status, created_at) WHERE task_type='create_fix_pr' AND status IN ('pending','queued','retrying')`。

**不引入 JSON virtual 索引**；幂等由 asynq TaskID `create_fix_pr:{parent_task_id}` 承担。

### 4.5 Store 新增方法

```go
FindByParentTaskID(ctx, parentID) ([]*TaskRecord, error)
FindStaleCreateFixPR(ctx, olderThan time.Time) ([]*TaskRecord, error)
```

### 4.6 主链路切分（`fix.Service.executeFix`）

原 `service.go:663-690` 的第 11 步改为：

```
success=true
  ├ 计算 base（含 tag→默认分支回退，保留在主链路）
  ├ 构造 PR 元数据（title/body 的截断和清洗仍在此做）
  ├ EnqueueCreateFixPR（asynq TaskID = "create_fix_pr:"+parentTaskID, ProcessIn=3min）
  │   enqueue 失败 → 返回 error 允许 fix_issue 重试（Claude 结果已持久化，重试时上游应跳过容器执行——此点留给实施计划评估）
  ├ Issue 评论第 1 条：FormatFixPushedPendingPRComment（已推送 + 修改文件数 + 子任务 ID）
  └ fix_issue Status = succeeded
```

Issue 评论变为三条独立评论（采纳方案 i）：

1. **第一步 fix_issue 完成**：✅ 修复已推送到 `auto-fix/issue-N`，📝 修改 X 个文件，🕐 PR 创建任务已排队。
2. **create_fix_pr 成功**：🎉 Pull Request 已创建：#N（复用 `FormatFixSuccessComment`）。
3. **create_fix_pr 失败（仅失败时）**：⚠️ PR 自动创建未成功，分支可用，附 compare 链接；最外层错误不含诊断细节。

### 4.7 `create_fix_pr` Processor

**就绪探针**：

```
Probe 1: GET /repos/{owner}/{repo}/branches/{head}
  if ExpectedCommitSHA != "" → 严格 SHA 匹配
  else → 宽松（分支存在 + 有 commit）
  探针 API 自身 404 → failure-open，跳过该 probe

Probe 2: GET /repos/{owner}/{repo}/compare/{base}...{head}
  成功 → OK
  空 commits 数组 → 空提交特例，不建 PR，直接 succeed 并记录
  5xx → asynq retry
  API 404（旧 Gitea 无该端点）→ failure-open
```

两个 probe 都过才调 `POST /pulls`。

**退避（保险窗口）**：

```go
const createFixPRInitialDelay = 3 * time.Minute  // 首次入队延迟

var createFixPRBackoffs = []time.Duration{
    2 * time.Minute,   // 5min 累计
    5 * time.Minute,   // 10min
    10 * time.Minute,  // 20min
    15 * time.Minute,  // 35min
    30 * time.Minute,  // 65min
    30 * time.Minute,  // 95min
}
// MaxRetry = 6，总窗口 ≈ 1.5 小时
```

用 **asynq 原生重试**（return error 让 asynq 按自定义 retryDelay 重排），不在 processor 内部做 sleep 循环——避免占用 worker 槽位，且重启后 asynq 能继续捡起。

**错误分类**：

| Gitea 返回 | 策略 |
|------------|------|
| 探针 2xx + POST 201 | ✅ 记录 PR、评论 Issue |
| 探针 404/5xx | 🔄 asynq 重试 |
| POST 500（就绪窗） | 🔄 重试 |
| POST 409/422 already exists | ✅ findExistingFixPR 复用 |
| POST 404 base/head | 🔄 N 次后降级 |
| POST 401/403 | ❌ 不重试，失败评论 |
| POST 其他 4xx | ❌ 不重试 |

**三道幂等门**：
1. asynq TaskID 固定 `create_fix_pr:{parent_task_id}`，重入去重。
2. Processor 入口先 `findExistingFixPR(owner, repo, head)`，命中复用。
3. POST 422 时再次查询复用（沿用 `service.go:784` pattern）。

**单次超时**：`context.WithTimeout(ctx, 45*time.Second)`。

**失败态**：MaxRetry 用完 → Error 字段写最外层错误 + 诊断段（探针状态、尝试次数）；Issue 评论第 3 条；飞书通知；**不回退父任务**（代码修复仍成功）。

### 4.8 兜底扫描 worker（内置 goroutine）

单实例部署，挂在 `dtworkflow serve` 内：

```
每 5 分钟扫描一次：
  SELECT * FROM tasks
  WHERE task_type='create_fix_pr'
    AND status IN ('pending','queued','retrying')
    AND updated_at < now - 10min
    AND created_at > now - 24h
  LIMIT 50

对每条 record：
  1. asynq.Inspector 查 TaskID 是否仍在队列 → 在则跳过
  2. 不在 → 重新 enqueue（TaskID 幂等去重）+ warn log + metric++

created_at > 24h 仍未完成：
  → 不 requeue，标 failed
  → 评论第 3 条（失败）
  → 飞书通知
```

### 4.9 可观测性（当前以 slog + SQL 为主）

- `fix_pr_pending_count`（SQL 可直接查）：告警线 > 10。
- `fix_pr_rescue_requeued_total`：兜底扫描 requeue 计数。
- `fix_pr_created_duration_seconds`：fix_issue succeed → create_fix_pr succeed 时间。

这些数据上线后能直接回答"真实就绪窗是多少"，无需 Prometheus 集成（留作未来增强）。

### 4.10 配置

```yaml
fix:
  async_pr_creation: true   # 主开关，false 走老同步路径（回滚）
  pr_initial_delay: 3m
  pr_max_retry: 6
  pr_rescue:
    enabled: true
    scan_interval: 5m
    stale_threshold: 10m
    give_up_after: 24h
```

## 5. 影响面清单

```
[M] internal/model/task.go                新 TaskType / TaskPayload 字段 / TaskRecord.ParentTaskID
[M] internal/store/migrations.go          CHECK 扩展 / ALTER 新列 / 新索引
[M] internal/store/sqlite.go              FindByParentTaskID / FindStaleCreateFixPR
[M] internal/queue/enqueue.go             EnqueueCreateFixPR (TaskID/ProcessIn)
[M] internal/queue/processor.go           路由 TaskTypeCreateFixPR
[N] internal/fix/pr_processor.go          就绪探针 + 错误分类 + 幂等 + 评论回写
[N] internal/fix/rescue.go                RescueWorker: ticker + scan + requeue
[M] internal/fix/service.go               executeFix 第 11 步改 enqueue；老 createFixPR 保留作 fallback
[M] internal/fix/formatter.go             两个新 formatter（Pushed / PendingPRFailed）
[M] cmd/dtworkflow / internal/cmd         serve 启动时挂 rescue goroutine
[M] internal/config                       新配置项
[M] internal/dtw/cmd                      fix-issue --wait-pr；wait 按 parent_task_id 轮询
[M] docs/PRD.md / CLAUDE.md               更新 M3.5 描述
[T] 每个 [M][N] 的 _test.go
```

约 10 源文件 + 5 测试文件 + 1 migration。

## 6. 测试策略

**单测**（fake gitea + fake asynq client）：
- EnqueueCreateFixPR：TaskID 幂等、延迟投递、payload 齐全。
- executeFix 切分：push 成功 → enqueue 成功 → succeeded；enqueue 失败 → error。
- 就绪探针：SHA 匹配/不匹配/空字段/分支 404 / 探针 API 404 failure-open。
- 错误分类：500/422/404/401 → 各自分支。
- 三道幂等门。
- 失败不回退父任务。
- 兜底扫描：stale 记录 requeue、give_up_after 标 failed。

**集成测**（真实 Gitea docker）：
- 端到端：webhook → fix_issue → push → create_fix_pr → PR 建成 → 两条 Issue 评论。
- 就绪窗复现：mock Gitea 前 60s `POST /pulls` 返 500、之后放行 → 确认自动重试最终成功。
- CLI `--wait-pr`：父子任务状态轮询。

## 7. 灰度与回滚

**灰度**：
- D1：合并 PR，默认 `async_pr_creation=false`，验证老路径无回归。
- D2–D3：测试环境切 true，观察 duration 指标。
- D4–D5：生产切 true，连续观察一周。
- D6：移除同步 `createFixPR` 老代码（或保留加 deprecated 注释）。

**快回滚**：YAML 切 false + 重启 → 老同步路径。已入队 create_fix_pr 仍会被消费（向前兼容）。

**深回滚**：git revert。migration 向前兼容（老代码 `IsValid()` 过滤未知 TaskType）；pending create_fix_pr 记录可手动 cancelled 或等 24h 自动 failed。

## 8. YAGNI 审查（剔除项）

- ❌ 独立 `pending_prs` 表（tasks + task_type 过滤即可）
- ❌ 新 TaskStatus 枚举
- ❌ Gitea push webhook 触发（方案 C 否决）
- ❌ PR 元数据 JSON virtual 索引
- ❌ Leader election（单实例部署）
- ❌ Prometheus 正式集成（暂用 slog + SQL）

## 9. 风险清单

| 风险 | 缓解 |
|------|------|
| asynq Redis 丢数据 | 兜底扫描 requeue |
| enqueue 之前进程崩溃 | 兜底扫描（task record 已入库） |
| SQLite CHECK ALTER 方式 | 照搬历史 migration pattern |
| Issue 三条评论噪音 | 用户已采纳 |
| Gitea 探针 API 差异 | failure-open 跳过探针 |
| fix_issue enqueue 失败时的 Claude 重跑 | 实施计划阶段明确"容器执行结果短路复用"策略 |

## 10. 决策记录

| 编号 | 决策 | 理由 |
|------|------|------|
| Q3 | CLI `--fix` 默认异步 + `--wait-pr` 可选 | 与 asynq 模型一致；保留同步体验选项 |
| Q4a | 就绪探针严格 SHA 匹配（兼容宽松） | FixOutput.CommitSHA 已存在，最稳 |
| Q4b | 首延迟 3min + 长退避 | 用户倾向保险，覆盖分钟级就绪窗 |
| Q5 | 兜底扫描内置 goroutine | 单实例部署，简化运维 |
| 评论时序 | 3 条独立评论 | 用户选 (i)，时序清晰 |
| base 计算位置 | 保留在 executeFix 主链路 | prClient 已注入，异步任务更纯粹 |
| 父子关联 | 冗余列 `parent_task_id` + 索引 | 与现有 pattern 一致，查询便宜 |

---

**下一步**：本设计文档作为实施计划的输入；实施计划编写由后续命令单独触发（本次不进入 writing-plans 阶段）。
