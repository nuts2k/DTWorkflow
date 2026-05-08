# Gitea API 分页统一治理设计

## 背景

项目中多处 Gitea 列表 API 调用只获取第一页数据（默认 50 条），在极端场景下可能导致数据截断：幂等检查失效、清理不完整、上下文丢失等。同时项目内已有两处正确的多页分页实现，但写法不统一，分页常量分散定义。

本次改动目标：提供统一的分页辅助函数 `PaginateAll`，修复所有不完整的分页调用，并将已有的正确实现重构到统一模式。

## 改动范围

### 1. 新增 `internal/gitea/pagination.go` — 通用分页辅助函数

```go
const (
    DefaultPageSize = 50
    DefaultMaxPages = 20
)

func PaginateAll[T any](
    ctx context.Context,
    pageSize, maxPages int,
    fetch func(ctx context.Context, page, pageSize int) ([]T, *Response, error),
) ([]T, bool, error)
```

- 泛型函数，适配所有返回 `[]T` 的列表 API
- 终止条件：`resp == nil || resp.NextPage == 0`（依赖已有的 `Response.parsePagination` 解析 Link header）
- `maxPages` 安全上限防无限循环
- 截断于 maxPages 时返回已拉取的部分数据 + `truncated=true` + nil error，调用方按业务语义决定如何处理
- 循环体开头检查 `ctx.Err()`，context 取消时立即返回 `(nil, false, ctx.Err())`
- pageSize/maxPages 传 0 时使用默认值

**页码推进策略**：首页 `page=1`，后续页使用 `resp.NextPage`（HATEOAS 模式，由 Gitea Link header 驱动）。`maxPages` 按迭代次数计数（非页码值），防止跳页导致无限循环。

```go
page := 1
for i := 0; i < maxPages; i++ {
    if err := ctx.Err(); err != nil {
        return nil, false, err
    }
    items, resp, err := fetch(ctx, page, pageSize)
    if err != nil {
        return nil, false, err
    }
    all = append(all, items...)
    if resp == nil || resp.NextPage == 0 {
        return all, false, nil
    }
    page = resp.NextPage
}
return all, true, nil // 截断于 maxPages
```

**错误语义**：中途 error 返回 `(nil, false, err)`——丢弃已拉取的部分数据。所有调用方对 error 路径均做 fail-open 或直接 return err，不消费 partial data，此语义与现有模式一致。

### 2. 客户端方法补全（API 一致性对齐）

`ListPullRequestCommits`（`gitea/pulls.go:193`）添加 `ListOptions` 参数，与其他 List 方法签名对齐。当前无外部调用方，属前瞻性 API 一致性对齐，不修复已知 bug。

`ListDirContents` 不改——Gitea contents API 本身不分页。

### 3. 调用点修复（5 处）

| # | 文件 | 函数 | 当前问题 | 改造方式 |
|---|------|------|----------|----------|
| 1 | `test/service.go:640` | `findExistingTestPR` | PageSize=50 单页 | `PaginateAll` 拉全量 open PR，filter headBranch |
| 2 | `fix/service.go:852` | `findExistingFixPR` | PageSize=50 单页 | 同上 |
| 3 | `fix/service.go:376` | `collectContext` | PageSize=50 单页 + 手动 warn | `PaginateAll` 替换，保留 `issue.Comments > len` 双重校验（兜底 Gitea 计数竞态） |
| 4 | `queue/enqueue.go:855` | `cleanupAllAutoTestBranches` | Page=1 硬编码 | `PaginateAll` 替换 |
| 5 | `queue/branch_cleaner.go:63` | `CleanupAutoTestBranch` | 无分页参数（默认 50） | `PaginateAll` 替换 |

**collectContext 评论量膨胀评估**：改造后最多拉取 1000 条评论（20 pages × 50），但 prompt 构造层已有截断机制——`maxCommentTotalRunes=20000` + 单条 `truncate(body, 2000)`，评论量增加不会导致 prompt 体积膨胀。无需额外处理。

### 4. 已有正确实现重构（2 处）

| # | 文件 | 函数 | 改造方式 |
|---|------|------|----------|
| 6 | `cmd/adapter.go:72` | `ListIssueComments` | `PaginateAll` 替换手写循环，删除本地分页常量，顺便移除冗余的 `resp.Body.Close()`（`doRequest` 内部已关闭） |
| 7 | `queue/enqueue.go:319` | `listAllPullRequestFiles` | `PaginateAll` 替换手写循环，删除本地分页常量 |

**adapter.go 类型转换模式**：`ListIssueComments` 实现 `notify.GiteaPRCommentManager` 接口，返回 `[]notify.GiteaCommentInfo`。改造后先用 `PaginateAll[*gitea.Comment]` 拉取全量，再做映射转换：

```go
func (a *giteaCommentAdapter) ListIssueComments(ctx context.Context, owner, repo string, index int64) ([]notify.GiteaCommentInfo, error) {
    comments, truncated, err := gitea.PaginateAll(ctx, 50, 20,
        func(ctx context.Context, page, pageSize int) ([]*gitea.Comment, *gitea.Response, error) {
            return a.client.ListIssueComments(ctx, owner, repo, index, gitea.ListOptions{
                Page: page, PageSize: pageSize,
            })
        })
    if err != nil {
        return nil, err
    }
    if truncated {
        slog.WarnContext(ctx, "评论列表被截断，锚点评论幂等 upsert 可能退化为 create",
            "owner", owner, "repo", repo, "index", index, "fetched", len(comments))
    }
    result := make([]notify.GiteaCommentInfo, 0, len(comments))
    for _, c := range comments {
        if c == nil { continue }
        result = append(result, notify.GiteaCommentInfo{ID: c.ID, Body: c.Body})
    }
    return result, nil
}
```

### 5. 不改动项（已确认排除）

- `review/service.go:109` — PR 文件列表 PageSize=100，仅用于 prompt 摘要，不影响评审质量
- 其余 List 方法（`ListRepoIssues`、`ListPullReviewComments`、`GetIssueLabels`）当前无 `internal/gitea/` 包外部调用方，不在本次范围内

## 接口影响

所有调用点通过闭包在方法内部构建 `PaginateAll` 的 fetch 回调，窄接口定义不变。`PaginateAll` 不负责 Response Body 生命周期管理——`doRequest` 内部已关闭。

## 各调用点参数选择

| 调用点 | pageSize | maxPages | 理由 |
|--------|----------|----------|------|
| `findExistingTestPR` | 50 | 10 | open PR 通常 < 50，10 页（500 PR）足够极端场景 |
| `findExistingFixPR` | 50 | 10 | 同上 |
| `collectContext` | 50 | 20 | Issue 评论可能较多，prompt 层已有截断 |
| `cleanupAllAutoTestBranches` | 50 | 10 | 同 findExisting 系列 |
| `CleanupAutoTestBranch` | 50 | 10 | 同上 |
| `ListIssueComments`（adapter） | 50 | 20 | 沿用原有语义 |
| `listAllPullRequestFiles` | 100 | 20 | 沿用原有语义 |

## 测试策略

### 新增

- `internal/gitea/pagination_test.go`：
  - 单页即返回（NextPage=0，truncated=false）
  - resp 为 nil 时正确终止（truncated=false）
  - 多页正常拉取（验证 resp.NextPage 推进，truncated=false）
  - 到达 maxPages 截断（truncated=true）
  - 中途 error 返回 (nil, false, err)
  - context 取消时立即返回 ctx.Err()
  - pageSize/maxPages 默认值生效

### 适配

各调用点已有测试中 mock 的列表方法需返回正确的 `*Response`（含 NextPage 字段），确保 `PaginateAll` 终止逻辑生效。完整清单：

| 调用点 | 测试文件 | 需适配的测试函数 |
|--------|----------|------------------|
| `findExistingTestPR` | `test/service_test.go` | findExistingTestPR 相关 case |
| `findExistingFixPR` | `fix/service_test.go` | findExistingFixPR 相关 case |
| `collectContext` | `fix/service_test.go` | collectContext 相关 case |
| `cleanupAllAutoTestBranches` | `queue/enqueue_test.go` | cleanupAll 相关 case |
| `CleanupAutoTestBranch` | `queue/branch_cleaner_test.go` | CleanupAutoTestBranch 相关 case |
| `ListIssueComments`（adapter） | `cmd/adapter_test.go` | ListIssueComments 相关 case |
| `listAllPullRequestFiles` | `queue/enqueue_test.go` | listAllPullRequestFiles 相关 case |
