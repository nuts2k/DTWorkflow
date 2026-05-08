# Gitea API 分页统一治理 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 提供 `PaginateAll` 泛型辅助函数，统一修复所有 Gitea 列表 API 的分页不完整问题。

**Architecture:** 在 `internal/gitea/` 包新增 `PaginateAll[T]` 泛型函数，各调用点通过闭包包装 fetch 回调使用，不改动窄接口定义。

**Tech Stack:** Go 1.25 generics, Gitea REST API Link header 分页

**Spec:** `docs/plans/2026-05-08-gitea-pagination-unification-design.md`

---

### Task 1: PaginateAll 泛型辅助函数

**Files:**
- Create: `internal/gitea/pagination.go`
- Create: `internal/gitea/pagination_test.go`

- [ ] **Step 1: 创建 pagination.go**

```go
// internal/gitea/pagination.go
package gitea

import "context"

const (
	DefaultPageSize = 50
	DefaultMaxPages = 20
)

// PaginateAll 逐页拉取 Gitea 分页 API 的全量结果。
//
// 页码推进使用 resp.NextPage（HATEOAS 模式）；maxPages 按迭代次数计数。
// 截断于 maxPages 时返回已拉取的部分数据 + nil error。
// 中途 error 返回 (nil, err)，丢弃已拉取的部分数据。
func PaginateAll[T any](
	ctx context.Context,
	pageSize, maxPages int,
	fetch func(ctx context.Context, page, pageSize int) ([]T, *Response, error),
) ([]T, error) {
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if maxPages <= 0 {
		maxPages = DefaultMaxPages
	}
	var all []T
	page := 1
	for i := 0; i < maxPages; i++ {
		items, resp, err := fetch(ctx, page, pageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if resp == nil || resp.NextPage == 0 {
			return all, nil
		}
		page = resp.NextPage
	}
	return all, nil
}
```

- [ ] **Step 2: 创建 pagination_test.go**

```go
// internal/gitea/pagination_test.go
package gitea

import (
	"context"
	"fmt"
	"testing"
)

func TestPaginateAll_SinglePage(t *testing.T) {
	result, err := PaginateAll(context.Background(), 50, 10,
		func(_ context.Context, page, pageSize int) ([]string, *Response, error) {
			if page != 1 {
				t.Fatalf("只应请求第 1 页，实际请求第 %d 页", page)
			}
			return []string{"a", "b"}, &Response{NextPage: 0}, nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result))
	}
}

func TestPaginateAll_NilResponse(t *testing.T) {
	result, err := PaginateAll(context.Background(), 50, 10,
		func(_ context.Context, _, _ int) ([]string, *Response, error) {
			return []string{"x"}, nil, nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result))
	}
}

func TestPaginateAll_MultiPage(t *testing.T) {
	result, err := PaginateAll(context.Background(), 2, 10,
		func(_ context.Context, page, _ int) ([]int, *Response, error) {
			switch page {
			case 1:
				return []int{1, 2}, &Response{NextPage: 2}, nil
			case 2:
				return []int{3, 4}, &Response{NextPage: 3}, nil
			case 3:
				return []int{5}, &Response{NextPage: 0}, nil
			default:
				t.Fatalf("unexpected page %d", page)
				return nil, nil, nil
			}
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 5 {
		t.Fatalf("expected 5 items, got %d", len(result))
	}
	for i, v := range result {
		if v != i+1 {
			t.Errorf("result[%d] = %d, want %d", i, v, i+1)
		}
	}
}

func TestPaginateAll_MaxPagesTruncation(t *testing.T) {
	calls := 0
	result, err := PaginateAll(context.Background(), 10, 3,
		func(_ context.Context, _, _ int) ([]string, *Response, error) {
			calls++
			return []string{"item"}, &Response{NextPage: calls + 1}, nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 items, got %d", len(result))
	}
}

func TestPaginateAll_ErrorDiscardsPartialData(t *testing.T) {
	result, err := PaginateAll(context.Background(), 10, 10,
		func(_ context.Context, page, _ int) ([]string, *Response, error) {
			if page == 1 {
				return []string{"ok"}, &Response{NextPage: 2}, nil
			}
			return nil, nil, fmt.Errorf("page 2 error")
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if result != nil {
		t.Fatalf("expected nil result on error, got %v", result)
	}
}

func TestPaginateAll_DefaultValues(t *testing.T) {
	var gotPageSize int
	calls := 0
	_, _ = PaginateAll(context.Background(), 0, 0,
		func(_ context.Context, _, pageSize int) ([]string, *Response, error) {
			gotPageSize = pageSize
			calls++
			return nil, nil, nil
		})
	if gotPageSize != DefaultPageSize {
		t.Errorf("default pageSize = %d, want %d", gotPageSize, DefaultPageSize)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}
```

- [ ] **Step 3: 运行测试确认通过**

Run: `go test ./internal/gitea/ -run TestPaginateAll -v`
Expected: 6 个测试全部 PASS

- [ ] **Step 4: 提交**

```bash
git add internal/gitea/pagination.go internal/gitea/pagination_test.go
git commit -m "feat: 添加 PaginateAll 泛型分页辅助函数

提供统一的多页拉取工具，终止条件基于 resp.NextPage（HATEOAS），
maxPages 安全上限防无限循环，中途 error 丢弃 partial data。"
```

---

### Task 2: ListPullRequestCommits 补全 ListOptions

**Files:**
- Modify: `internal/gitea/pulls.go:191-207`
- Modify: `internal/gitea/pulls_test.go:374`

- [ ] **Step 1: 添加 ListOptions 参数**

将 `ListPullRequestCommits` 签名从：
```go
func (c *Client) ListPullRequestCommits(ctx context.Context, owner, repo string, index int64) ([]*Commit, *Response, error)
```
改为：
```go
func (c *Client) ListPullRequestCommits(ctx context.Context, owner, repo string, index int64, opts ListOptions) ([]*Commit, *Response, error)
```

添加分页参数传递：
```go
params := url.Values{}
if opts.Page > 0 {
    params.Set("page", fmt.Sprintf("%d", opts.Page))
}
if opts.PageSize > 0 {
    params.Set("limit", fmt.Sprintf("%d", opts.PageSize))
}
```

使用 `c.newRequestWithQuery` 替换 `c.newRequest`。

- [ ] **Step 2: 适配包内测试**

`internal/gitea/pulls_test.go:374` 需要补传 `ListOptions{}`：

```go
// 将：
commits, _, err := client.ListPullRequestCommits(context.Background(), "owner", "repo", 42)
// 改为：
commits, _, err := client.ListPullRequestCommits(context.Background(), "owner", "repo", 42, ListOptions{})
```

- [ ] **Step 3: 确认无外部调用方需适配**

Run: `grep -rn "ListPullRequestCommits" internal/ --include="*.go" | grep -v "internal/gitea/"`
Expected: 无输出（当前无外部调用方）

- [ ] **Step 4: 运行 gitea 包测试确认无回归**

Run: `go test ./internal/gitea/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/gitea/pulls.go internal/gitea/pulls_test.go
git commit -m "refactor: ListPullRequestCommits 补全 ListOptions 参数

API 一致性对齐，当前无外部调用方。"
```

---

### Task 3: 改造 findExistingTestPR（test/service.go）

**Files:**
- Modify: `internal/test/service.go:636-659` — `findExistingTestPR`

- [ ] **Step 1: 改造 findExistingTestPR 使用 PaginateAll**

将 `internal/test/service.go` 中的 `findExistingTestPR` 替换为：

```go
func (s *Service) findExistingTestPR(ctx context.Context, owner, repo, headBranch string) (int64, string, bool) {
	if headBranch == "" {
		return 0, "", false
	}
	prs, err := gitea.PaginateAll(ctx, 50, 10,
		func(ctx context.Context, page, pageSize int) ([]*gitea.PullRequest, *gitea.Response, error) {
			return s.prClient.ListRepoPullRequests(ctx, owner, repo, gitea.ListPullRequestsOptions{
				State:       "open",
				ListOptions: gitea.ListOptions{Page: page, PageSize: pageSize},
			})
		})
	if err != nil {
		s.logger.WarnContext(ctx, "查询既有 PR 失败，跳过幂等检查继续创建",
			"owner", owner, "repo", repo, "head", headBranch, "error", err)
		return 0, "", false
	}
	for _, pr := range prs {
		if pr == nil || pr.Head == nil {
			continue
		}
		if pr.Head.Ref == headBranch {
			return pr.Number, pr.HTMLURL, true
		}
	}
	return 0, "", false
}
```

- [ ] **Step 2: 运行现有测试确认无回归**

Run: `go test ./internal/test/ -run "TestService|TestFind" -v -count=1`
Expected: PASS — 现有 mock 返回 `nil` response，`PaginateAll` 在 `resp == nil` 时正确终止

- [ ] **Step 3: 提交**

```bash
git add internal/test/service.go
git commit -m "fix: findExistingTestPR 使用 PaginateAll 多页拉取

修复仓库超过 50 个 open PR 时幂等检查失效的问题。"
```

---

### Task 4: 改造 findExistingFixPR + collectContext（fix/service.go）

**Files:**
- Modify: `internal/fix/service.go:845-869` — `findExistingFixPR`
- Modify: `internal/fix/service.go:373-393` — `collectContext`

- [ ] **Step 1: 改造 findExistingFixPR**

将 `internal/fix/service.go` 中的 `findExistingFixPR` 替换为：

```go
func (s *Service) findExistingFixPR(ctx context.Context, owner, repo, headBranch string) (int64, string, bool) {
	if headBranch == "" {
		return 0, "", false
	}
	prs, err := gitea.PaginateAll(ctx, 50, 10,
		func(ctx context.Context, page, pageSize int) ([]*gitea.PullRequest, *gitea.Response, error) {
			return s.prClient.ListRepoPullRequests(ctx, owner, repo, gitea.ListPullRequestsOptions{
				State:       "open",
				ListOptions: gitea.ListOptions{Page: page, PageSize: pageSize},
			})
		})
	if err != nil {
		s.logger.WarnContext(ctx, "查询既有 PR 失败，跳过幂等检查继续创建",
			"owner", owner, "repo", repo, "head", headBranch, "error", err)
		return 0, "", false
	}
	for _, pr := range prs {
		if pr == nil || pr.Head == nil {
			continue
		}
		if pr.Head.Ref == headBranch {
			return pr.Number, pr.HTMLURL, true
		}
	}
	return 0, "", false
}
```

- [ ] **Step 2: 改造 collectContext**

将 `collectContext` 替换为：

```go
func (s *Service) collectContext(ctx context.Context, owner, repo string, issue *gitea.Issue) (*IssueContext, error) {
	comments, err := gitea.PaginateAll(ctx, 50, 20,
		func(ctx context.Context, page, pageSize int) ([]*gitea.Comment, *gitea.Response, error) {
			return s.gitea.ListIssueComments(ctx, owner, repo, issue.Number, gitea.ListOptions{
				Page: page, PageSize: pageSize,
			})
		})
	if err != nil {
		return nil, fmt.Errorf("获取评论失败: %w", err)
	}

	return &IssueContext{
		Issue:    issue,
		Comments: comments,
	}, nil
}
```

注意：去掉了原来的 `issue.Comments > len(comments)` warn 日志——`PaginateAll` 已拉取所有页，不会出现部分采集的情况（maxPages=20 覆盖 1000 条评论，prompt 层另有截断）。

- [ ] **Step 3: 运行现有测试确认无回归**

Run: `go test ./internal/fix/ -v -count=1`
Expected: PASS — `mockIssueClient.listComments` 返回 `nil` response，终止正确；`stubPRClient` 返回 `nil` response，终止正确

- [ ] **Step 4: 提交**

```bash
git add internal/fix/service.go
git commit -m "fix: findExistingFixPR 和 collectContext 使用 PaginateAll 多页拉取

findExistingFixPR: 修复仓库超过 50 个 open PR 时幂等检查失效。
collectContext: 移除单页 50 条限制，评论量膨胀由 prompt 层截断兜底。"
```

---

### Task 5: 改造 cleanupAllAutoTestBranches（queue/enqueue.go）

**Files:**
- Modify: `internal/queue/enqueue.go:849-863` — `cleanupAllAutoTestBranches` 中的 `ListRepoPullRequests` 调用

- [ ] **Step 1: 改造 cleanupAllAutoTestBranches**

将 `internal/queue/enqueue.go` 中 `cleanupAllAutoTestBranches` 的 PR 列表拉取部分：

```go
prs, _, prErr := h.prClient.ListRepoPullRequests(ctx, owner, repo, gitea.ListPullRequestsOptions{
    ListOptions: gitea.ListOptions{Page: 1, PageSize: 50},
    State:       "open",
})
if prErr != nil {
    h.logger.WarnContext(ctx, "cleanupAll: 列出 open PR 失败",
        "repo", repoFullName, "error", prErr)
    return
}
```

替换为：

```go
prs, prErr := gitea.PaginateAll(ctx, 50, 10,
    func(ctx context.Context, page, pageSize int) ([]*gitea.PullRequest, *gitea.Response, error) {
        return h.prClient.ListRepoPullRequests(ctx, owner, repo, gitea.ListPullRequestsOptions{
            ListOptions: gitea.ListOptions{Page: page, PageSize: pageSize},
            State:       "open",
        })
    })
if prErr != nil {
    h.logger.WarnContext(ctx, "cleanupAll: 列出 open PR 失败",
        "repo", repoFullName, "error", prErr)
    return
}
```

- [ ] **Step 2: 运行相关测试确认无回归**

Run: `go test ./internal/queue/ -run "TestCleanupAll" -v -count=1`
Expected: PASS — `mockGenTestsPRClient` 返回 `nil` response，终止正确

- [ ] **Step 3: 提交**

```bash
git add internal/queue/enqueue.go
git commit -m "fix: cleanupAllAutoTestBranches 使用 PaginateAll 多页拉取

移除 Page=1 硬编码，修复超过 50 个 open PR 时清理不完整的问题。"
```

---

### Task 6: 改造 CleanupAutoTestBranch（queue/branch_cleaner.go）

**Files:**
- Modify: `internal/queue/branch_cleaner.go:61-98` — `CleanupAutoTestBranch`

- [ ] **Step 1: 改造 CleanupAutoTestBranch**

将 PR 列表拉取部分：

```go
prs, _, listErr := c.client.ListRepoPullRequests(ctx, owner, repo, gitea.ListPullRequestsOptions{
    State: "open",
})
if listErr != nil {
    c.logger.WarnContext(ctx, "列出远程 PR 失败，跳过关闭旧 PR 步骤",
        ...
    )
} else {
    for _, pr := range prs {
        ...
```

替换为：

```go
prs, listErr := gitea.PaginateAll(ctx, 50, 10,
    func(ctx context.Context, page, pageSize int) ([]*gitea.PullRequest, *gitea.Response, error) {
        return c.client.ListRepoPullRequests(ctx, owner, repo, gitea.ListPullRequestsOptions{
            ListOptions: gitea.ListOptions{Page: page, PageSize: pageSize},
            State:       "open",
        })
    })
if listErr != nil {
    c.logger.WarnContext(ctx, "列出远程 PR 失败，跳过关闭旧 PR 步骤",
        ...
    )
} else {
    for _, pr := range prs {
        ...
```

逻辑结构不变（if listErr / else 分支保持原样），只是数据源从单页改为 `PaginateAll`。

- [ ] **Step 2: 运行相关测试确认无回归**

Run: `go test ./internal/queue/ -run "TestCleanupAutoTestBranch" -v -count=1`
Expected: 6 个 case 全部 PASS — `fakeBranchCleanerClient` 返回 `nil` response，终止正确

- [ ] **Step 3: 提交**

```bash
git add internal/queue/branch_cleaner.go
git commit -m "fix: CleanupAutoTestBranch 使用 PaginateAll 多页拉取

修复仓库超过 50 个 open PR 时关闭旧 PR 不完整的问题。"
```

---

### Task 7: 重构 adapter.go ListIssueComments

**Files:**
- Modify: `internal/cmd/adapter.go:55-103` — 删除本地分页常量 + 用 PaginateAll 替换手写循环

- [ ] **Step 1: 重构 ListIssueComments**

删除本地常量：
```go
const (
    commentListPageSize = 50
    maxCommentPages     = 20
)
```

将手写分页循环替换为：

```go
func (a *giteaCommentAdapter) ListIssueComments(ctx context.Context, owner, repo string, index int64) ([]notify.GiteaCommentInfo, error) {
	comments, err := gitea.PaginateAll(ctx, 50, 20,
		func(ctx context.Context, page, pageSize int) ([]*gitea.Comment, *gitea.Response, error) {
			return a.client.ListIssueComments(ctx, owner, repo, index, gitea.ListOptions{
				Page: page, PageSize: pageSize,
			})
		})
	if err != nil {
		return nil, err
	}
	result := make([]notify.GiteaCommentInfo, 0, len(comments))
	for _, c := range comments {
		if c == nil {
			continue
		}
		result = append(result, notify.GiteaCommentInfo{
			ID:   c.ID,
			Body: c.Body,
		})
	}
	return result, nil
}
```

注意：移除了冗余的 `resp.Body.Close()` 调用（`doRequest` 内部已关闭）。

**测试覆盖说明**：`adapter_test.go` 当前无 `ListIssueComments` 测试。`PaginateAll` 本身的多页逻辑已由 `pagination_test.go` 覆盖，adapter 层仅做 `[]*gitea.Comment → []notify.GiteaCommentInfo` 映射转换，风险可控。如后续需要可补充集成测试。

- [ ] **Step 2: 运行相关测试确认编译通过**

Run: `go test ./internal/cmd/ -v -count=1`
Expected: PASS（编译 + 现有测试无回归）

- [ ] **Step 3: 提交**

```bash
git add internal/cmd/adapter.go
git commit -m "refactor: adapter ListIssueComments 统一为 PaginateAll

删除本地分页常量和手写循环，移除冗余 resp.Body.Close()。"
```

---

### Task 8: 重构 listAllPullRequestFiles（queue/enqueue.go）

**Files:**
- Modify: `internal/queue/enqueue.go:319-344` — `listAllPullRequestFiles`

- [ ] **Step 1: 重构 listAllPullRequestFiles**

将手写分页循环替换为：

```go
func (h *EnqueueHandler) listAllPullRequestFiles(ctx context.Context, owner, repo string, prNumber int64) ([]*gitea.ChangedFile, error) {
	files, err := gitea.PaginateAll(ctx, 100, 20,
		func(ctx context.Context, page, pageSize int) ([]*gitea.ChangedFile, *gitea.Response, error) {
			return h.prFilesLister.ListPullRequestFiles(ctx, owner, repo, prNumber, gitea.ListOptions{
				Page: page, PageSize: pageSize,
			})
		})
	if err != nil {
		return nil, err
	}
	return files, nil
}
```

- [ ] **Step 2: 保留截断 warn 日志**

原实现在 maxPages 截断时有 warn 日志，`PaginateAll` 静默返回。在调用后补充截断检查，保留可观测性：

```go
func (h *EnqueueHandler) listAllPullRequestFiles(ctx context.Context, owner, repo string, prNumber int64) ([]*gitea.ChangedFile, error) {
	const (
		pageSize = 100
		maxPages = 20
	)
	files, err := gitea.PaginateAll(ctx, pageSize, maxPages,
		func(ctx context.Context, page, pageSize int) ([]*gitea.ChangedFile, *gitea.Response, error) {
			return h.prFilesLister.ListPullRequestFiles(ctx, owner, repo, prNumber, gitea.ListOptions{
				Page: page, PageSize: pageSize,
			})
		})
	if err != nil {
		return nil, err
	}
	if len(files) >= pageSize*maxPages {
		h.logger.WarnContext(ctx, "change-driven: PR file list may be truncated",
			"owner", owner, "repo", repo, "pr", prNumber, "files", len(files))
	}
	return files, nil
}
```

- [ ] **Step 3: 运行相关测试确认无回归**

Run: `go test ./internal/queue/ -run "TestListAllPullRequestFiles|TestChangeDriven" -v -count=1`
Expected: PASS — `mockPRFilesLister` 已正确返回 `*gitea.Response{NextPage: n}`

- [ ] **Step 4: 提交**

```bash
git add internal/queue/enqueue.go
git commit -m "refactor: listAllPullRequestFiles 统一为 PaginateAll

删除本地分页常量和手写循环，与其他调用点模式对齐。"
```

---

### Task 9: 全量测试 + 编译验证

- [ ] **Step 1: 全量编译**

Run: `go build ./...`
Expected: 无编译错误

- [ ] **Step 2: 全量测试**

Run: `go test ./... -count=1`
Expected: 全部 PASS

- [ ] **Step 3: 交叉编译验证**

Run: `GOOS=linux GOARCH=amd64 go build ./...`
Expected: 无编译错误（纯 Go，无 CGO 依赖）

- [ ] **Step 4: lint 检查**

Run: `golangci-lint run ./internal/gitea/ ./internal/test/ ./internal/fix/ ./internal/queue/ ./internal/cmd/`
Expected: 无新增 lint 错误
