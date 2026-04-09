# Issue Ref 分支选择机制实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 让 Issue auto-fix 任务感知 Issue 关联的 ref（分支或 tag），ref 为空或无效时打回并评论提醒，有效时在正确分支上执行分析。

**Architecture:** Webhook 解析层提取 ref → TaskPayload 透传 → Service 层校验 ref 有效性（先 GetBranch 再 GetTag） → 容器层 checkout 到指定 ref → Prompt 注入 ref 上下文。ref 为空或无效时回写评论并返回非重试错误。

**Tech Stack:** Go, Gitea REST API, Docker entrypoint.sh, asynq

**设计文档:** `docs/plans/2026-04-09-issue-ref-branch-design.md`

---

### Task 1: Gitea Tag 类型 + GetTag 方法

**Files:**
- Modify: `internal/gitea/types.go:27-37` — 在 `Branch` 后新增 `Tag` 类型
- Modify: `internal/gitea/repos.go` — 新增 `GetTag` 方法
- Create: `internal/gitea/testdata/tag.json` — 测试夹具
- Modify: `internal/gitea/repos_test.go` — 新增 `TestGetTag`、`TestGetTag_NotFound`

**Step 1: 创建测试夹具**

创建 `internal/gitea/testdata/tag.json`：

```json
{
  "name": "v1.0.0",
  "message": "Release v1.0.0",
  "id": "abc123def456",
  "commit": {
    "id": "abc123",
    "url": "https://gitea.example.com/owner/repo/commit/abc123"
  }
}
```

**Step 2: 写失败测试**

在 `internal/gitea/repos_test.go` 末尾追加：

```go
func TestGetTag(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/tags/v1.0.0", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		writeJSON(w, loadFixture(t, "tag.json"))
	})

	tag, _, err := client.GetTag(context.Background(), "owner", "repo", "v1.0.0")
	if err != nil {
		t.Fatalf("GetTag 返回错误: %v", err)
	}
	if tag.Name != "v1.0.0" {
		t.Errorf("Name = %q, 期望 %q", tag.Name, "v1.0.0")
	}
}

func TestGetTag_NotFound(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/tags/notexist", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, []byte(`{"message":"not found"}`))
	})

	_, _, err := client.GetTag(context.Background(), "owner", "repo", "notexist")
	if err == nil {
		t.Fatal("期望返回错误，但没有")
	}
	if !IsNotFound(err) {
		t.Errorf("期望 IsNotFound 为 true，实际错误: %v", err)
	}
}
```

**Step 3: 运行测试确认编译失败**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/gitea/ -run "TestGetTag" -v -count=1`
Expected: 编译失败 — `client.GetTag` undefined, `Tag` undefined

**Step 4: 添加 Tag 类型**

在 `internal/gitea/types.go` 的 `Branch` 结构体之后（约 L37）新增：

```go
// Tag 表示 Gitea tag
type Tag struct {
	Name    string  `json:"name"`
	ID      string  `json:"id"`
	Message string  `json:"message"`
	Commit  *Commit `json:"commit"`
}
```

**Step 5: 添加 GetTag 方法**

在 `internal/gitea/repos.go` 的 `GetBranch` 方法之后新增：

```go
// GetTag 获取 tag 信息
// GET /api/v1/repos/{owner}/{repo}/tags/{tag}
func (c *Client) GetTag(ctx context.Context, owner, repo, tag string) (*Tag, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/tags/%s",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(tag))
	req, err := c.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, nil, err
	}

	var result Tag
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return &result, resp, nil
}
```

**Step 6: 运行测试确认通过**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/gitea/ -run "TestGetTag" -v -count=1`
Expected: PASS

**Step 7: 提交**

```bash
git add internal/gitea/types.go internal/gitea/repos.go internal/gitea/repos_test.go internal/gitea/testdata/tag.json
git commit -m "feat(gitea): 新增 Tag 类型和 GetTag 方法"
```

---

### Task 2: Webhook Ref 字段透传

**Files:**
- Modify: `internal/webhook/gitea_types.go:38-45` — `giteaIssuePayload` 新增 `Ref`
- Modify: `internal/webhook/event.go:38-44` — `IssueRef` 新增 `Ref`
- Modify: `internal/webhook/parser.go:105-111` — `parseIssue` 填充 `Ref`
- Modify: `internal/webhook/testdata/issue_labeled_auto_fix.json` — 添加 `ref` 字段
- Modify: `internal/webhook/parser_issue_test.go` — 新增 Ref 解析断言

**Step 1: 添加结构体字段**

修改 `internal/webhook/gitea_types.go`，在 `giteaIssuePayload` 的 `State` 字段之后添加：

```go
Ref     string             `json:"ref"`
```

修改 `internal/webhook/event.go`，在 `IssueRef` 的 `State` 字段之后添加：

```go
Ref     string
```

**Step 2: 更新测试夹具并写测试**

修改 `internal/webhook/testdata/issue_labeled_auto_fix.json`，在 `issue` 对象的 `state` 后添加 `ref`：

```json
{
  "action": "labeled",
  "repository": {
    "full_name": "owner/repo",
    "owner": {"login": "owner"},
    "name": "repo"
  },
  "issue": {
    "number": 7,
    "title": "Bug report",
    "body": "Issue body",
    "html_url": "https://gitea.example.com/owner/repo/issues/7",
    "state": "open",
    "ref": "feature/user-auth"
  },
  "label": {"name": "auto-fix", "color": "ff0000"},
  "sender": {"login": "bob", "full_name": "Bob"}
}
```

在 `internal/webhook/parser_issue_test.go` 末尾追加：

```go
func TestParser_ParseIssueRef(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "issue_labeled_auto_fix.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-ref", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent := event.(IssueLabelEvent)
	if issueEvent.Issue.Ref != "feature/user-auth" {
		t.Errorf("Issue.Ref = %q, want %q", issueEvent.Issue.Ref, "feature/user-auth")
	}
}

func TestParser_ParseIssueRefEmpty(t *testing.T) {
	// issue_labeled_other.json 不含 ref 字段，应解析为空字符串
	body, err := os.ReadFile(filepath.Join("testdata", "issue_labeled_other.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parser := NewParser()
	event, err := parser.Parse("issues", "delivery-ref-empty", body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	issueEvent := event.(IssueLabelEvent)
	if issueEvent.Issue.Ref != "" {
		t.Errorf("Issue.Ref = %q, want empty", issueEvent.Issue.Ref)
	}
}
```

**Step 3: 运行测试确认 Ref 断言失败**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/webhook/ -run "TestParser_ParseIssueRef" -v -count=1`
Expected: `TestParser_ParseIssueRef` FAIL（Ref 为空，因为 parser 还没填充）；`TestParser_ParseIssueRefEmpty` PASS

**Step 4: 修改 parser 填充 Ref**

修改 `internal/webhook/parser.go:105-111`，在 `parseIssue` 构造 `IssueRef` 时添加 `Ref`：

```go
		Issue: IssueRef{
			Number:  payload.Issue.Number,
			Title:   payload.Issue.Title,
			Body:    payload.Issue.Body,
			HTMLURL: payload.Issue.HTMLURL,
			State:   payload.Issue.State,
			Ref:     payload.Issue.Ref,
		},
```

**Step 5: 运行全部 webhook 测试确认通过**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/webhook/ -v -count=1`
Expected: 全部 PASS（包括原有测试）

**Step 6: 提交**

```bash
git add internal/webhook/
git commit -m "feat(webhook): Issue 事件解析新增 Ref 字段透传"
```

---

### Task 3: TaskPayload IssueRef + 队列入队填充

**Files:**
- Modify: `internal/model/task.go:90-91` — `TaskPayload` 新增 `IssueRef`
- Modify: `internal/queue/enqueue.go:147-156` — `HandleIssueLabel` 构造 payload 时填入 `IssueRef`

**Step 1: 添加 TaskPayload 字段**

修改 `internal/model/task.go`，在 `IssueTitle` 之后新增：

```go
	IssueRef    string `json:"issue_ref,omitempty"`
```

**Step 2: 修改 HandleIssueLabel 填充 IssueRef**

修改 `internal/queue/enqueue.go:147-156`，在 payload 构造中添加 `IssueRef`：

```go
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   event.DeliveryID,
		RepoOwner:    event.Repository.Owner,
		RepoName:     event.Repository.Name,
		RepoFullName: event.Repository.FullName,
		CloneURL:     event.Repository.CloneURL,
		IssueNumber:  event.Issue.Number,
		IssueTitle:   event.Issue.Title,
		IssueRef:     event.Issue.Ref,
	}
```

**Step 3: 编译验证**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go build ./...`
Expected: 编译成功

**Step 4: 运行队列测试确认无回归**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/queue/ -v -count=1`
Expected: 全部 PASS

**Step 5: 提交**

```bash
git add internal/model/task.go internal/queue/enqueue.go
git commit -m "feat(model): TaskPayload 新增 IssueRef 字段，enqueue 填充"
```

---

### Task 4: Sentinel 错误 + Processor 跳过重试

**Files:**
- Modify: `internal/fix/result.go:9-11` — 新增 `ErrMissingIssueRef`、`ErrInvalidIssueRef`
- Modify: `internal/queue/processor.go:244-246` — 新增跳过重试分支
- Modify: `internal/queue/processor_test.go` — 新增跳过重试测试

**Step 1: 添加 sentinel 错误**

修改 `internal/fix/result.go`，在 `ErrIssueNotOpen` 之后新增：

```go
// ErrMissingIssueRef Issue 未设置关联分支或 tag 时返回此错误，
// Processor 层据此跳过重试（同 ErrIssueNotOpen 模式）。
var ErrMissingIssueRef = errors.New("Issue 未设置关联分支或 tag")

// ErrInvalidIssueRef Issue 关联的分支或 tag 不存在时返回此错误，
// Processor 层据此跳过重试（同 ErrIssueNotOpen 模式）。
var ErrInvalidIssueRef = errors.New("Issue 关联的分支或 tag 不存在")
```

**Step 2: 写失败测试**

在 `internal/queue/processor_test.go` 中新增：

```go
func TestProcessTask_FixIssue_MissingRef_SkipsRetry(t *testing.T) {
	s := newMockStore()
	fixExec := &mockFixExecutor{
		err: fmt.Errorf("Issue #10: %w", fix.ErrMissingIssueRef),
	}
	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0}}
	notifier := &stubNotifier{}
	p := NewProcessor(pool, s, notifier, slog.Default(), WithFixService(fixExec))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "delivery-miss-ref",
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
	}
	task := buildAsynqTask(t, payload)
	record := &model.TaskRecord{
		ID:         "task-miss-ref",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		DeliveryID: "delivery-miss-ref",
	}
	seedRecord(s, record)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("应返回 SkipRetry 错误")
	}
	if !strings.Contains(err.Error(), "跳过分析") {
		t.Errorf("错误信息应包含'跳过分析'，实际: %v", err)
	}
	updated := s.tasks["task-miss-ref"]
	if updated.Status != model.TaskStatusFailed {
		t.Errorf("状态应为 failed，实际: %s", updated.Status)
	}
}

func TestProcessTask_FixIssue_InvalidRef_SkipsRetry(t *testing.T) {
	s := newMockStore()
	fixExec := &mockFixExecutor{
		err: fmt.Errorf("Issue #10 ref=bad: %w", fix.ErrInvalidIssueRef),
	}
	pool := &mockPoolRunner{result: &worker.ExecutionResult{ExitCode: 0}}
	notifier := &stubNotifier{}
	p := NewProcessor(pool, s, notifier, slog.Default(), WithFixService(fixExec))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		DeliveryID:   "delivery-bad-ref",
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
	}
	task := buildAsynqTask(t, payload)
	record := &model.TaskRecord{
		ID:         "task-bad-ref",
		TaskType:   model.TaskTypeFixIssue,
		Status:     model.TaskStatusQueued,
		DeliveryID: "delivery-bad-ref",
	}
	seedRecord(s, record)

	err := p.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("应返回 SkipRetry 错误")
	}
	updated := s.tasks["task-bad-ref"]
	if updated.Status != model.TaskStatusFailed {
		t.Errorf("状态应为 failed，实际: %s", updated.Status)
	}
}
```

**Step 3: 运行测试确认失败**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/queue/ -run "TestProcessTask_FixIssue_(Missing|Invalid)Ref" -v -count=1`
Expected: FAIL — 状态不是 `failed`（当前走通用错误路径，标记为 retrying/failed 取决于 asynq context）

**Step 4: 在 Processor 添加跳过重试分支**

修改 `internal/queue/processor.go`，在 `errors.Is(runErr, fix.ErrIssueNotOpen)` 分支（约 L244）之后，添加：

```go
		if errors.Is(runErr, fix.ErrMissingIssueRef) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, "Issue 未设置关联分支，跳过分析")
		}
		if errors.Is(runErr, fix.ErrInvalidIssueRef) {
			return p.handleSkipRetryFailure(ctx, record, runErr, nil, "Issue 关联的 ref 不存在，跳过分析")
		}
```

**Step 5: 运行测试确认通过**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/queue/ -run "TestProcessTask_FixIssue_(Missing|Invalid)Ref" -v -count=1`
Expected: PASS

**Step 6: 运行全量队列测试确认无回归**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/queue/ -v -count=1`
Expected: 全部 PASS

**Step 7: 提交**

```bash
git add internal/fix/result.go internal/queue/processor.go internal/queue/processor_test.go
git commit -m "feat(fix): 新增 ref 校验 sentinel 错误，Processor 跳过重试"
```

---

### Task 5: Fix Service RefClient + ref 校验（核心逻辑）

**Files:**
- Modify: `internal/fix/service.go` — 新增 `RefClient` 接口、`WithRefClient` option、`validateRef` 方法、`Execute` ref 检查 + 评论回写
- Modify: `internal/fix/service_test.go` — 新增 mock 和多个 ref 校验测试用例

**Step 1: 写失败测试**

在 `internal/fix/service_test.go` 中新增 mock 和测试：

```go
// --- mockRefClient ---

type mockRefClient struct {
	getBranch func(ctx context.Context, owner, repo, branch string) (*gitea.Branch, *gitea.Response, error)
	getTag    func(ctx context.Context, owner, repo, tag string) (*gitea.Tag, *gitea.Response, error)
}

func (m *mockRefClient) GetBranch(ctx context.Context, owner, repo, branch string) (*gitea.Branch, *gitea.Response, error) {
	if m.getBranch != nil {
		return m.getBranch(ctx, owner, repo, branch)
	}
	return &gitea.Branch{Name: branch}, nil, nil
}

func (m *mockRefClient) GetTag(ctx context.Context, owner, repo, tag string) (*gitea.Tag, *gitea.Response, error) {
	if m.getTag != nil {
		return m.getTag(ctx, owner, repo, tag)
	}
	return &gitea.Tag{Name: tag}, nil, nil
}

// 模拟 Gitea 404 错误
func notFoundErr() error {
	return &gitea.ErrorResponse{StatusCode: 404, Message: "not found"}
}

// --- ref 校验测试用例 ---

func TestExecute_MissingIssueRef(t *testing.T) {
	var commentBody string
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return openIssue(10), nil, nil
			},
			createComment: func(_ context.Context, _, _ string, _ int64, opts gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
				commentBody = opts.Body
				return &gitea.Comment{ID: 1}, nil, nil
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{}),
	)

	payload := fixPayload()
	payload.IssueRef = "" // 空 ref

	_, err := svc.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("预期返回错误")
	}
	if !errors.Is(err, ErrMissingIssueRef) {
		t.Errorf("预期 ErrMissingIssueRef，实际: %v", err)
	}
	if !strings.Contains(commentBody, "未设置关联分支") {
		t.Errorf("评论应包含提醒文案，实际: %q", commentBody)
	}
}

func TestExecute_InvalidIssueRef(t *testing.T) {
	var commentBody string
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return openIssue(10), nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
			createComment: func(_ context.Context, _, _ string, _ int64, opts gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
				commentBody = opts.Body
				return &gitea.Comment{ID: 1}, nil, nil
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, _ string) (*gitea.Branch, *gitea.Response, error) {
				return nil, nil, notFoundErr()
			},
			getTag: func(_ context.Context, _, _, _ string) (*gitea.Tag, *gitea.Response, error) {
				return nil, nil, notFoundErr()
			},
		}),
	)

	payload := fixPayload()
	payload.IssueRef = "nonexistent-branch"

	_, err := svc.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("预期返回错误")
	}
	if !errors.Is(err, ErrInvalidIssueRef) {
		t.Errorf("预期 ErrInvalidIssueRef，实际: %v", err)
	}
	if !strings.Contains(commentBody, "nonexistent-branch") {
		t.Errorf("评论应包含 ref 名称，实际: %q", commentBody)
	}
}

func TestExecute_RefValidAsBranch(t *testing.T) {
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return openIssue(10), nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, _ string) (*gitea.Branch, *gitea.Response, error) {
				return &gitea.Branch{Name: "feature/ok"}, nil, nil
			},
		}),
	)

	payload := fixPayload()
	payload.IssueRef = "feature/ok"

	result, err := svc.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("有效分支不应报错，实际: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
}

func TestExecute_RefValidAsTag(t *testing.T) {
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return openIssue(10), nil, nil
			},
			listComments: func(_ context.Context, _, _ string, _ int64, _ gitea.ListOptions) ([]*gitea.Comment, *gitea.Response, error) {
				return nil, nil, nil
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{
			getBranch: func(_ context.Context, _, _, _ string) (*gitea.Branch, *gitea.Response, error) {
				return nil, nil, notFoundErr() // 分支不存在
			},
			getTag: func(_ context.Context, _, _, _ string) (*gitea.Tag, *gitea.Response, error) {
				return &gitea.Tag{Name: "v1.0.0"}, nil, nil // 但 tag 存在
			},
		}),
	)

	payload := fixPayload()
	payload.IssueRef = "v1.0.0"

	result, err := svc.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("有效 tag 不应报错，实际: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
}

func TestExecute_RefCommentWritebackFailure_StillReturnsError(t *testing.T) {
	svc := NewService(
		&mockIssueClient{
			getIssue: func(_ context.Context, _, _ string, _ int64) (*gitea.Issue, *gitea.Response, error) {
				return openIssue(10), nil, nil
			},
			createComment: func(_ context.Context, _, _ string, _ int64, _ gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error) {
				return nil, nil, fmt.Errorf("Gitea API 500")
			},
		},
		defaultPool(),
		WithRefClient(&mockRefClient{}),
	)

	payload := fixPayload()
	payload.IssueRef = ""

	_, err := svc.Execute(context.Background(), payload)
	if !errors.Is(err, ErrMissingIssueRef) {
		t.Errorf("即使评论回写失败，也应返回 ErrMissingIssueRef，实际: %v", err)
	}
}
```

**Step 2: 运行测试确认编译失败**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/fix/ -run "TestExecute_(MissingIssueRef|InvalidIssueRef|RefValid)" -v -count=1`
Expected: 编译失败 — `WithRefClient` undefined, `RefClient` undefined

**Step 3: 实现 RefClient 接口和 WithRefClient**

修改 `internal/fix/service.go`：

1. 在 import 中添加 `"errors"`

2. 在 `IssueClient` 接口之后新增：

```go
// RefClient 窄接口，仅暴露 ref 有效性验证所需的 Gitea API。
type RefClient interface {
	GetBranch(ctx context.Context, owner, repo, branch string) (*gitea.Branch, *gitea.Response, error)
	GetTag(ctx context.Context, owner, repo, tag string) (*gitea.Tag, *gitea.Response, error)
}
```

3. 在 `WithConfigProvider` 之后新增：

```go
// WithRefClient 注入 ref 有效性验证客户端（可选）
func WithRefClient(c RefClient) ServiceOption {
	return func(s *Service) { s.refClient = c }
}
```

4. 在 `Service` struct 中新增字段：

```go
type Service struct {
	gitea     IssueClient
	pool      FixPoolRunner
	cfgProv   FixConfigProvider
	refClient RefClient
	logger    *slog.Logger
}
```

5. 在 `Execute` 方法中，`Issue 状态校验`（step 2）之后、`采集上下文`（step 3）之前，插入 ref 校验逻辑：

```go
	// 3. ref 空值检查
	if payload.IssueRef == "" {
		s.commentRefMissing(ctx, owner, repo, issueNum)
		return nil, fmt.Errorf("Issue #%d: %w", issueNum, ErrMissingIssueRef)
	}

	// 4. ref 有效性检查
	if s.refClient != nil {
		if err := s.validateRef(ctx, owner, repo, payload.IssueRef); err != nil {
			if errors.Is(err, ErrInvalidIssueRef) {
				s.commentRefInvalid(ctx, owner, repo, issueNum, payload.IssueRef)
				return nil, fmt.Errorf("Issue #%d ref=%q: %w", issueNum, payload.IssueRef, ErrInvalidIssueRef)
			}
			return nil, fmt.Errorf("验证 ref %q 失败: %w", payload.IssueRef, err)
		}
	}
```

注意：原来注释中的步骤编号 3~8 变为 5~10，需要相应更新注释。

6. 在文件末尾（`collectContext` 之后）新增辅助方法：

```go
// validateRef 验证 Issue 关联的 ref 是否存在（先检查分支，再检查 tag）。
func (s *Service) validateRef(ctx context.Context, owner, repo, ref string) error {
	_, _, err := s.refClient.GetBranch(ctx, owner, repo, ref)
	if err == nil {
		return nil
	}
	if !gitea.IsNotFound(err) {
		return fmt.Errorf("检查分支 %q 失败: %w", ref, err)
	}
	_, _, err = s.refClient.GetTag(ctx, owner, repo, ref)
	if err == nil {
		return nil
	}
	if !gitea.IsNotFound(err) {
		return fmt.Errorf("检查 tag %q 失败: %w", ref, err)
	}
	return ErrInvalidIssueRef
}

func (s *Service) commentRefMissing(ctx context.Context, owner, repo string, issueNum int64) {
	body := "⚠️ 该 Issue 未设置关联分支，无法确定分析目标。\n\n请在 Issue 右侧边栏「Ref」处指定目标分支或 tag，然后重新添加 `auto-fix` 标签以触发分析。"
	if _, _, err := s.gitea.CreateIssueComment(ctx, owner, repo, issueNum,
		gitea.CreateIssueCommentOption{Body: body}); err != nil {
		s.logger.ErrorContext(ctx, "回写 ref 缺失评论失败",
			"issue", issueNum, "error", err)
	}
}

func (s *Service) commentRefInvalid(ctx context.Context, owner, repo string, issueNum int64, ref string) {
	body := fmt.Sprintf("⚠️ 该 Issue 关联的 ref `%s` 不存在（已检查分支和 tag），无法执行分析。\n\n请在 Issue 右侧边栏「Ref」处修正目标分支或 tag，然后重新添加 `auto-fix` 标签以触发分析。", ref)
	if _, _, err := s.gitea.CreateIssueComment(ctx, owner, repo, issueNum,
		gitea.CreateIssueCommentOption{Body: body}); err != nil {
		s.logger.ErrorContext(ctx, "回写 ref 无效评论失败",
			"issue", issueNum, "error", err)
	}
}
```

**Step 4: 运行新增测试确认通过**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/fix/ -run "TestExecute_(MissingIssueRef|InvalidIssueRef|RefValid|RefComment)" -v -count=1`
Expected: 全部 PASS

**Step 5: 修复现有测试**

现有测试中 `fixPayload()` 返回的 payload 没有 `IssueRef`，但现在 Execute 会因 ref 为空而提前返回错误。需要更新：

1. 修改 `fixPayload()` 函数，添加默认 IssueRef：

```go
func fixPayload() model.TaskPayload {
	return model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
		IssueTitle:   "test bug",
		IssueRef:     "main",
		DeliveryID:   "test-delivery",
	}
}
```

2. 对于不需要 refClient 的现有测试，ref 校验因 `refClient == nil` 自动跳过。但空值检查总是执行，所以所有通过 `Execute` 的测试都需要 `IssueRef` 非空。

**Step 6: 运行全量 fix 测试确认无回归**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/fix/ -v -count=1`
Expected: 全部 PASS

**Step 7: 提交**

```bash
git add internal/fix/service.go internal/fix/service_test.go
git commit -m "feat(fix): 新增 RefClient 接口和 ref 有效性校验逻辑"
```

---

### Task 6: Prompt 注入 ref 信息

**Files:**
- Modify: `internal/fix/context.go` — `IssueContext` 新增 `Ref` 字段
- Modify: `internal/fix/service.go` — 采集上下文后设置 `Ref`
- Modify: `internal/fix/prompt.go:139-181` — `buildPrompt` 注入 ref 行
- Modify: `internal/fix/prompt_test.go` — 验证 ref 出现在 prompt 中

**Step 1: 写失败测试**

在 `internal/fix/prompt_test.go` 中新增：

```go
func TestBuildPrompt_ContainsRef(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	issueCtx := &IssueContext{
		Issue: &gitea.Issue{
			Number: 10,
			Title:  "test bug",
			Body:   "something broken",
		},
		Ref: "feature/user-auth",
	}
	prompt := svc.buildPrompt(issueCtx)
	if !strings.Contains(prompt, "当前代码基于 ref：feature/user-auth") {
		t.Errorf("prompt 应包含 ref 信息，实际:\n%s", prompt)
	}
}

func TestBuildPrompt_NoRefOmitted(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	issueCtx := &IssueContext{
		Issue: &gitea.Issue{
			Number: 10,
			Title:  "test bug",
		},
		Ref: "",
	}
	prompt := svc.buildPrompt(issueCtx)
	if strings.Contains(prompt, "当前代码基于 ref") {
		t.Errorf("ref 为空时不应出现 ref 信息，实际:\n%s", prompt)
	}
}
```

**Step 2: 运行测试确认失败**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/fix/ -run "TestBuildPrompt_(ContainsRef|NoRefOmitted)" -v -count=1`
Expected: `TestBuildPrompt_ContainsRef` FAIL（prompt 中无 ref 行），`TestBuildPrompt_NoRefOmitted` PASS

**Step 3: 实现**

1. 修改 `internal/fix/context.go`，在 `IssueContext` 中新增：

```go
type IssueContext struct {
	Issue    *gitea.Issue
	Comments []*gitea.Comment
	Ref      string // Issue 关联的分支或 tag
}
```

2. 修改 `internal/fix/service.go` 的 `Execute` 方法，在 `collectContext` 成功之后、`buildPrompt` 之前，设置 Ref：

```go
	issueCtx.Ref = payload.IssueRef
```

3. 修改 `internal/fix/prompt.go` 的 `buildPrompt`，在任务上下文段（"你正在分析仓库中的 Issue"之后）插入：

```go
	if issueCtx.Ref != "" {
		b.WriteString(fmt.Sprintf("当前代码基于 ref：%s\n", issueCtx.Ref))
	}
```

具体位置：在 `b.WriteString(fmt.Sprintf("你正在分析仓库中的 Issue #%d。\n", ...))` 行之后，`Issue 标题` 行之前。

**Step 4: 运行测试确认通过**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/fix/ -run "TestBuildPrompt" -v -count=1`
Expected: 全部 PASS

**Step 5: 运行全量 fix 测试确认无回归**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/fix/ -v -count=1`
Expected: 全部 PASS

**Step 6: 提交**

```bash
git add internal/fix/context.go internal/fix/service.go internal/fix/prompt.go internal/fix/prompt_test.go
git commit -m "feat(fix): prompt 注入 Issue 关联 ref 上下文信息"
```

---

### Task 7: Container 环境变量 + Prompt ref 信息

**Files:**
- Modify: `internal/worker/container.go:68-85` — `buildContainerEnv` 新增 `ISSUE_REF`
- Modify: `internal/worker/container.go:136-147` — `buildContainerCmd` fix_issue prompt 补充 ref
- Modify: `internal/worker/container_test.go` — 验证新增的环境变量和 prompt

**Step 1: 写失败测试**

在 `internal/worker/container_test.go` 中新增：

```go
func TestBuildContainerEnv_FixIssueWithRef(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		IssueNumber:  10,
		IssueTitle:   "Bug in login",
		IssueRef:     "feature/user-auth",
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if envMap["ISSUE_REF"] != "feature/user-auth" {
		t.Errorf("ISSUE_REF = %q, 期望 %q", envMap["ISSUE_REF"], "feature/user-auth")
	}
}

func TestBuildContainerEnv_FixIssueWithoutRef(t *testing.T) {
	config := PoolConfig{
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
	}
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		IssueNumber:  10,
		IssueTitle:   "Bug in login",
		IssueRef:     "",
	}

	env := buildContainerEnv(config, payload)
	envMap := envSliceToMap(env)

	if _, ok := envMap["ISSUE_REF"]; ok {
		t.Error("ref 为空时不应包含 ISSUE_REF")
	}
}

func TestBuildContainerCmd_FixIssueWithRef(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		IssueNumber:  10,
		IssueTitle:   "Bug",
		IssueRef:     "feature/auth",
	}

	cmd := buildContainerCmd(payload)
	prompt := strings.Join(cmd, " ")
	if !strings.Contains(prompt, "ref 'feature/auth' is checked out") {
		t.Errorf("prompt 应包含 ref checkout 信息，实际: %s", prompt)
	}
}
```

**Step 2: 运行测试确认失败**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/worker/ -run "TestBuildContainer(Env|Cmd)_FixIssue(With|Without)Ref" -v -count=1`
Expected: FAIL

**Step 3: 实现环境变量变更**

修改 `internal/worker/container.go:76-80`，在 `TaskTypeFixIssue` case 中，`ISSUE_TITLE` 之后添加：

```go
	case model.TaskTypeFixIssue:
		env = append(env,
			fmt.Sprintf("ISSUE_NUMBER=%d", payload.IssueNumber),
			fmt.Sprintf("ISSUE_TITLE=%s", sanitizeEnvValue(payload.IssueTitle)),
		)
		if payload.IssueRef != "" {
			env = append(env, fmt.Sprintf("ISSUE_REF=%s", sanitizeEnvValue(payload.IssueRef)))
		}
```

**Step 4: 实现 prompt 变更**

修改 `internal/worker/container.go:136-147`，`TaskTypeFixIssue` case 改为：

```go
	case model.TaskTypeFixIssue:
		repoInfo := "The repository has been cloned to the current directory."
		if payload.IssueRef != "" {
			repoInfo = fmt.Sprintf(
				"The repository has been cloned and ref '%s' is checked out.",
				sanitizePromptInput(payload.IssueRef, 200))
		}
		return []string{
			"claude", "-p",
			fmt.Sprintf(
				"Fix issue #%d (%s) in repository %s. "+
					"%s "+
					"Analyze the issue, explore the codebase, implement a fix, and commit the changes.",
				payload.IssueNumber,
				sanitizePromptInput(payload.IssueTitle, 500),
				sanitizePromptInput(payload.RepoFullName, 200),
				repoInfo,
			),
		}
```

**Step 5: 运行测试确认通过**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/worker/ -run "TestBuildContainer(Env|Cmd)_FixIssue" -v -count=1`
Expected: 全部 PASS

**Step 6: 运行全量 worker 测试确认无回归**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/worker/ -v -count=1`
Expected: 全部 PASS

**Step 7: 提交**

```bash
git add internal/worker/container.go internal/worker/container_test.go
git commit -m "feat(worker): fix_issue 容器新增 ISSUE_REF 环境变量和 prompt ref 信息"
```

---

### Task 8: Entrypoint checkout 逻辑

**Files:**
- Modify: `build/docker/entrypoint.sh:81-83` — `fix_issue` case 添加 ref checkout

**Step 1: 修改 entrypoint.sh**

将 `build/docker/entrypoint.sh` 中 `fix_issue)` case（约 L81-83）从：

```bash
    fix_issue)
        log "Issue 修复任务，使用默认分支"
        ;;
```

改为：

```bash
    fix_issue)
        if [ -n "${ISSUE_REF:-}" ]; then
            log "checkout 到关联 ref: ${ISSUE_REF}"
            git fetch origin "${ISSUE_REF}" >&2 2>&1
            git checkout FETCH_HEAD >&2 2>&1
        fi
        ;;
```

**Step 2: 语法验证**

Run: `bash -n /Users/kelin/Workspace/DTWorkflow/build/docker/entrypoint.sh`
Expected: 无输出（语法正确）

**Step 3: 提交**

```bash
git add build/docker/entrypoint.sh
git commit -m "feat(docker): fix_issue 入口脚本支持 checkout 到 ISSUE_REF"
```

---

### Task 9: Serve.go 装配布线

**Files:**
- Modify: `internal/cmd/serve.go:207-212` — `fix.NewService` 新增 `WithRefClient`

**Step 1: 修改装配代码**

修改 `internal/cmd/serve.go:207-212`，在 `fix.NewService` 调用中添加 `WithRefClient`：

```go
		fixSvc := fix.NewService(
			deps.GiteaClient,
			deps.Pool,
			fix.WithServiceLogger(slog.Default()),
			fix.WithConfigProvider(cfgAdapter),
			fix.WithRefClient(deps.GiteaClient),
		)
```

**Step 2: 编译验证**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go build ./...`
Expected: 编译成功

**Step 3: 提交**

```bash
git add internal/cmd/serve.go
git commit -m "feat(cmd): serve 装配 fix.Service 时注入 RefClient"
```

---

### Task 10: 全量编译 + 测试验证

**Step 1: 全量编译**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go build ./...`
Expected: 编译成功

**Step 2: 全量单元测试**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./... -count=1`
Expected: 全部 PASS

**Step 3: 静态检查**

Run: `cd /Users/kelin/Workspace/DTWorkflow && golangci-lint run ./...`
Expected: 无新增 lint 错误

**Step 4: 检视变更文件清单**

Run: `git diff --stat HEAD~9`
Expected: 涉及文件与设计文档一致：

| 文件 | 变更类型 |
|------|----------|
| `internal/gitea/types.go` | 修改：新增 `Tag` 类型 |
| `internal/gitea/repos.go` | 修改：新增 `GetTag` 方法 |
| `internal/gitea/testdata/tag.json` | 新增：测试夹具 |
| `internal/gitea/repos_test.go` | 修改：新增测试 |
| `internal/webhook/gitea_types.go` | 修改：`giteaIssuePayload` 新增 `Ref` |
| `internal/webhook/event.go` | 修改：`IssueRef` 新增 `Ref` |
| `internal/webhook/parser.go` | 修改：`parseIssue` 填充 `Ref` |
| `internal/webhook/testdata/issue_labeled_auto_fix.json` | 修改：添加 `ref` |
| `internal/webhook/parser_issue_test.go` | 修改：新增测试 |
| `internal/model/task.go` | 修改：`TaskPayload` 新增 `IssueRef` |
| `internal/queue/enqueue.go` | 修改：填充 `IssueRef` |
| `internal/queue/processor.go` | 修改：新增跳过重试分支 |
| `internal/queue/processor_test.go` | 修改：新增测试 |
| `internal/fix/result.go` | 修改：新增 sentinel errors |
| `internal/fix/service.go` | 修改：`RefClient` + ref 校验 |
| `internal/fix/service_test.go` | 修改：新增 ref 测试 |
| `internal/fix/context.go` | 修改：`IssueContext` 新增 `Ref` |
| `internal/fix/prompt.go` | 修改：注入 ref 信息 |
| `internal/fix/prompt_test.go` | 修改：新增测试 |
| `internal/worker/container.go` | 修改：环境变量 + prompt |
| `internal/worker/container_test.go` | 修改：新增测试 |
| `build/docker/entrypoint.sh` | 修改：checkout 逻辑 |
| `internal/cmd/serve.go` | 修改：装配 `WithRefClient` |
