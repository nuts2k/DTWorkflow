# Auto-Fix Issue 分支选择机制设计

> **日期**：2026-04-09
> **状态**：已批准
> **范围**：Issue auto-fix 任务的目标分支确定策略

## 背景

当前 auto-fix 流程没有任何分支选择机制，始终使用 `git clone` 默认拉到的分支（仓库 default branch）。
Gitea Issue 有 `ref` 字段，用户可以在 Issue 右侧边栏指定关联的分支或 tag，但该字段从未被消费。

在多分支并行开发的团队中，Issue 可能针对任意分支。基于错误分支进行根因分析，
会产生看似合理但实际无关的结论，比直接拒绝执行更难接受。

## 设计决策

### 核心策略：ref 为空时打回，不做回退

- **不回退到 default_branch**：避免基于错误分支产生误导性分析
- **回写评论提醒用户设置 ref**：简洁明了，不列分支列表（多分支场景下列表无意义）
- **ref 指向不存在的分支或 tag 时同样打回**：和空值处理一致，宁可打回也不给错误结果

### 检查位置：`internal/fix/service.go` 的 `Execute` 方法

选择在 Service 层而非 EnqueueHandler 层判断，理由：
1. `Service` 已有 `IssueClient` 依赖，天然能回写评论，不需要给 EnqueueHandler 引入新依赖
2. ref 检查是"能不能执行分析"的业务判断，属于 fix 模块职责
3. 消耗极小（最多两次 Gitea API 调用，无容器开销）

## 数据流改造

### Webhook 解析层

`internal/webhook/gitea_types.go` — `giteaIssuePayload` 新增：
```go
Ref string `json:"ref"`
```

`internal/webhook/event.go` — `IssueRef` 新增 `Ref` 字段，`parseIssue` 中填充。

### 任务 Payload

`internal/model/task.go` — `TaskPayload` 新增：
```go
IssueRef string `json:"issue_ref,omitempty"`
```

`internal/queue/enqueue.go` — `HandleIssueLabel` 构造 payload 时填入 `event.Issue.Ref`。

### 容器层

`internal/worker/container.go` — `fix_issue` 分支追加环境变量 `ISSUE_REF`。

`build/docker/entrypoint.sh` — `fix_issue` case 中，若 `ISSUE_REF` 非空则 `git fetch` + `git checkout`：
```bash
fix_issue)
    if [ -n "${ISSUE_REF:-}" ]; then
        log "checkout 到关联分支: ${ISSUE_REF}"
        git fetch origin "${ISSUE_REF}" >&2 2>&1
        git checkout FETCH_HEAD >&2 2>&1
    fi
    ;;
```

Service 层已保证 ref 非空且有效，entrypoint.sh 不处理异常。

### 分析 Prompt

`internal/fix/prompt.go` — `buildPrompt` 任务上下文中注入分支信息：
```
当前代码基于分支：feature/user-auth
```

`internal/worker/container.go` — `fix_issue` 的 prompt 补充分支信息：
```
The repository has been cloned and branch 'feature/user-auth' is checked out.
```

## ref 空值与有效性检查

### Execute 完整流程

```
Execute(ctx, payload)
  1. 前置校验：IssueNumber > 0
  2. Issue 状态校验：must be open
  3. ref 空值检查：payload.IssueRef == "" → 评论 + ErrMissingIssueRef
  4. ref 有效性检查：GetBranch 404 → GetTag 404 → 评论 + ErrInvalidIssueRef
  5. 采集上下文
  6. 构造 prompt（含分支信息）+ 容器执行
  7. 解析结果
  8. 回写分析评论
```

### 新增错误类型

`internal/fix/result.go`：
```go
ErrMissingIssueRef = errors.New("Issue 未设置关联分支或 tag")
ErrInvalidIssueRef = errors.New("Issue 关联的分支或 tag 不存在")
```

两者均为非重试类错误，Processor 层据此跳过重试，直接标记任务完成（同 `ErrIssueNotOpen` 模式）。

### 新增接口

新建 `RefClient` 窄接口（与 `IssueClient` 平行，保持接口分离）：
```go
// RefClient 窄接口，仅暴露 ref 有效性验证所需的 Gitea API。
type RefClient interface {
	GetBranch(ctx context.Context, owner, repo, branch string) (*Branch, *Response, error)
	GetTag(ctx context.Context, owner, repo, tag string) (*Tag, *Response, error)
}
```

`Service` struct 新增 `refClient RefClient` 字段，配套 `WithRefClient(c RefClient) ServiceOption`。
`*gitea.Client` 已实现 `GetBranch`（`repos.go`），需新增 `GetTag` 方法和 `Tag` 类型。

ref 有效性验证策略：先调 `GetBranch`，若 404 再调 `GetTag`，两者均 404 时才判定无效。

### 评论模板

**ref 为空时：**
```markdown
⚠️ 该 Issue 未设置关联分支，无法确定分析目标。

请在 Issue 右侧边栏「Ref」处指定目标分支或 tag，然后重新添加 `auto-fix` 标签以触发分析。
```

**ref 指向的分支或 tag 不存在时：**
```markdown
⚠️ 该 Issue 关联的 ref `xxx` 不存在（已检查分支和 tag），无法执行分析。

请在 Issue 右侧边栏「Ref」处修正目标分支或 tag，然后重新添加 `auto-fix` 标签以触发分析。
```

## 涉及文件清单

| 文件 | 变更类型 |
|------|----------|
| `internal/webhook/gitea_types.go` | 修改：新增 `Ref` 字段 |
| `internal/webhook/event.go` | 修改：`IssueRef` 新增 `Ref` |
| `internal/webhook/parser.go` | 修改：`parseIssue` 填充 `Ref` |
| `internal/model/task.go` | 修改：`TaskPayload` 新增 `IssueRef` |
| `internal/queue/enqueue.go` | 修改：填充 `IssueRef` |
| `internal/queue/processor.go` | 修改：新增 `ErrMissingIssueRef`/`ErrInvalidIssueRef` 跳过重试分支 |
| `internal/gitea/types.go` | 修改：`Issue` 新增 `Ref` 字段；新增 `Tag` 类型 |
| `internal/gitea/repos.go` | 修改：新增 `GetTag` 方法 |
| `internal/fix/result.go` | 修改：新增两个 sentinel error |
| `internal/fix/service.go` | 修改：新增 `RefClient` 接口、`WithRefClient` option、`Execute` ref 检查 + 评论回写 |
| `internal/fix/prompt.go` | 修改：`buildPrompt` 注入分支信息 |
| `internal/worker/container.go` | 修改：`fix_issue` 环境变量和 prompt |
| `build/docker/entrypoint.sh` | 修改：`fix_issue` case checkout 逻辑 |
| 各模块对应的 `_test.go` | 新增/修改测试 |
