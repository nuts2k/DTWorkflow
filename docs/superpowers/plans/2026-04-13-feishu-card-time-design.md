# 飞书卡片通知增加通知时间与耗时 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为所有飞书卡片通知增加"通知时间"字段，为评审成功完成通知增加"耗时"字段。

**Architecture:** 沿用现有 Metadata 模式，新增 `MetaKeyNotifyTime` 和 `MetaKeyDuration` 两个常量。processor 在公共路径统一注入时间元数据，feishu_card 消费并渲染。不改动 Message 结构体，不影响 Gitea 等其他通知渠道。

**Tech Stack:** Go, time 标准库, time.FixedZone（避免 tzdata 依赖）

**Spec:** `docs/superpowers/specs/2026-04-13-feishu-card-time-design.md`

---

## 文件结构

| 文件 | 变更类型 | 职责 |
|------|---------|------|
| `internal/notify/notifier.go` | Modify | +2 MetaKey 常量 |
| `internal/queue/processor.go` | Modify | +2 辅助函数 + 重构 buildStartMessage/buildNotificationMessage 公共路径注入时间 |
| `internal/notify/feishu_card.go` | Modify | 渲染通知时间和耗时字段 |
| `internal/notify/feishu_card_test.go` | Modify | 更新现有用例 + 新增用例 |
| `internal/queue/processor_test.go` | Modify | 新增时间字段断言 |

---

### Task 1: 数据层 - 新增 MetaKey 常量

**Files:**
- Modify: `internal/notify/notifier.go:22-31`

- [ ] **Step 1: 在 MetaKey 常量块末尾新增两个常量**

在 `internal/notify/notifier.go:31`（`MetaKeyTaskStatus` 之后）追加：

```go
MetaKeyNotifyTime = "notify_time" // 通知发送时间
MetaKeyDuration   = "duration"    // 任务耗时（仅 succeeded）
```

- [ ] **Step 2: 运行编译验证**

Run: `cd /Users/kelin/Work/DTWorkflow && go build ./internal/notify/...`
Expected: 编译成功，无错误

- [ ] **Step 3: 提交**

```bash
git add internal/notify/notifier.go
git commit -m "feat(notify): 新增 MetaKeyNotifyTime 和 MetaKeyDuration 常量"
```

---

### Task 2: 辅助函数 - formatNotifyTime 和 formatDuration

**Files:**
- Modify: `internal/queue/processor.go`（文件顶部，import 之后、Processor 定义之前）
- Modify: `internal/queue/processor_test.go`

- [ ] **Step 1: 写失败测试 - formatNotifyTime**

在 `internal/queue/processor_test.go` 末尾新增：

```go
func TestFormatNotifyTime(t *testing.T) {
	result := formatNotifyTime()
	// 格式应为 "2006-01-02 15:04:05"（19 字符）
	if len(result) != 19 {
		t.Errorf("formatNotifyTime() = %q, length = %d, want 19", result, len(result))
	}
	// 验证可被反向解析（格式正确性）
	_, err := time.Parse("2006-01-02 15:04:05", result)
	if err != nil {
		t.Errorf("formatNotifyTime() = %q, 无法解析: %v", result, err)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input time.Duration
		want  string
	}{
		{32 * time.Second, "32s"},
		{2*time.Minute + 30*time.Second, "2m30s"},
		{1*time.Hour + 5*time.Minute + 30*time.Second, "1h5m30s"},
		{500 * time.Millisecond, "0s"},
		{0, "0s"},
	}
	for _, tc := range tests {
		got := formatDuration(tc.input)
		if got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/kelin/Work/DTWorkflow && go test ./internal/queue/ -run "TestFormatNotifyTime|TestFormatDuration" -v`
Expected: FAIL - `undefined: formatNotifyTime` 和 `undefined: formatDuration`

- [ ] **Step 3: 实现辅助函数**

在 `internal/queue/processor.go` 的 import 块之后（第 20 行附近，`var _ asynq.Handler` 之前）新增：

```go
var shanghaiZone = time.FixedZone("CST", 8*3600)

func formatNotifyTime() string {
	return time.Now().In(shanghaiZone).Format("2006-01-02 15:04:05")
}

func formatDuration(d time.Duration) string {
	return d.Truncate(time.Second).String()
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/kelin/Work/DTWorkflow && go test ./internal/queue/ -run "TestFormatNotifyTime|TestFormatDuration" -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/queue/processor.go internal/queue/processor_test.go
git commit -m "feat(queue): 新增 formatNotifyTime 和 formatDuration 辅助函数"
```

---

### Task 3: 飞书卡片渲染 - 通知时间与耗时字段（TDD）

**Files:**
- Modify: `internal/notify/feishu_card_test.go`
- Modify: `internal/notify/feishu_card.go:30-41`

- [ ] **Step 1: 更新 feishu_card_test.go import 并更新现有测试**

首先在 `internal/notify/feishu_card_test.go` 头部添加 `"strings"` import：

```go
import (
	"strings"
	"testing"
)
```

然后更新现有测试用例，在 Metadata 中加入 `MetaKeyNotifyTime`，使其反映真实生产消息。以下三个测试需要修改：

**TestFormatFeishuCard_PRReviewStarted**（第 14 行 Metadata）：
```go
		Metadata: map[string]string{
			MetaKeyPRURL:      "https://gitea.example.com/org/repo/pulls/42",
			MetaKeyPRTitle:    "修复登录验证逻辑",
			MetaKeyNotifyTime: "2026-04-13 14:30:05",
		},
```

**TestFormatFeishuCard_PRReviewDone_Approve**（第 49 行 Metadata）：
```go
		Metadata: map[string]string{
			MetaKeyPRURL:        "https://gitea.example.com/org/repo/pulls/42",
			MetaKeyPRTitle:      "修复登录验证逻辑",
			MetaKeyVerdict:      "approve",
			MetaKeyIssueSummary: "2 WARNING, 1 INFO",
			MetaKeyNotifyTime:   "2026-04-13 14:31:37",
			MetaKeyDuration:     "32s",
		},
```

**TestFormatFeishuCard_SystemErrorRetrying**（第 129 行 Metadata）：
```go
		Metadata: map[string]string{
			MetaKeyPRURL:      "https://gitea.example.com/org/repo/pulls/42",
			MetaKeyTaskStatus: "retrying",
			MetaKeyRetryCount: "2",
			MetaKeyMaxRetry:   "3",
			MetaKeyNotifyTime: "2026-04-13 14:32:10",
		},
```

- [ ] **Step 2: 写失败测试 - 通知时间渲染**

在 `internal/notify/feishu_card_test.go` 末尾新增：

```go
func TestFormatFeishuCard_NotifyTimeRendered(t *testing.T) {
	msg := Message{
		EventType: EventPRReviewStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 42, IsPR: true},
		Title:     "PR 自动评审开始",
		Body:      "正在评审 PR #42",
		Metadata: map[string]string{
			MetaKeyPRURL:      "https://gitea.example.com/org/repo/pulls/42",
			MetaKeyNotifyTime: "2026-04-13 14:30:05",
		},
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	card := result["card"].(map[string]any)
	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)

	if !strings.Contains(md, "**通知时间**: 2026-04-13 14:30:05") {
		t.Errorf("markdown 未包含通知时间，got:\n%s", md)
	}
}

func TestFormatFeishuCard_DurationRendered(t *testing.T) {
	msg := Message{
		EventType: EventPRReviewDone,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 42, IsPR: true},
		Title:     "PR 自动评审任务完成",
		Body:      "任务执行完成",
		Metadata: map[string]string{
			MetaKeyPRURL:      "https://gitea.example.com/org/repo/pulls/42",
			MetaKeyVerdict:    "approve",
			MetaKeyNotifyTime: "2026-04-13 14:31:37",
			MetaKeyDuration:   "32s",
		},
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	card := result["card"].(map[string]any)
	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)

	if !strings.Contains(md, "**通知时间**: 2026-04-13 14:31:37") {
		t.Errorf("markdown 未包含通知时间，got:\n%s", md)
	}
	if !strings.Contains(md, "**耗时**: 32s") {
		t.Errorf("markdown 未包含耗时，got:\n%s", md)
	}
}

func TestFormatFeishuCard_FailedNoDuration(t *testing.T) {
	msg := Message{
		EventType: EventSystemError,
		Severity:  SeverityWarning,
		Target:    Target{Owner: "org", Repo: "repo", Number: 42, IsPR: true},
		Title:     "PR 自动评审任务失败",
		Body:      "容器执行超时",
		Metadata: map[string]string{
			MetaKeyPRURL:      "https://gitea.example.com/org/repo/pulls/42",
			MetaKeyNotifyTime: "2026-04-13 14:32:10",
		},
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	card := result["card"].(map[string]any)
	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)

	if !strings.Contains(md, "**通知时间**: 2026-04-13 14:32:10") {
		t.Errorf("失败通知也应包含通知时间，got:\n%s", md)
	}
	if strings.Contains(md, "**耗时**") {
		t.Errorf("失败通知不应包含耗时，got:\n%s", md)
	}
}
```

- [ ] **Step 3: 运行测试验证失败**

Run: `cd /Users/kelin/Work/DTWorkflow && go test ./internal/notify/ -run "TestFormatFeishuCard" -v`
Expected: FAIL - 更新后的现有测试和新测试均因 markdown 未包含通知时间而失败

- [ ] **Step 4: 实现渲染逻辑**

在 `internal/notify/feishu_card.go` 的 `FormatFeishuCard` 函数中，重试信息之后（第 37 行之后）、Body 之前（第 39 行之前）新增：

```go
	if notifyTime := msg.Metadata[MetaKeyNotifyTime]; notifyTime != "" {
		mdParts = append(mdParts, fmt.Sprintf("**通知时间**: %s", notifyTime))
	}
	if duration := msg.Metadata[MetaKeyDuration]; duration != "" {
		mdParts = append(mdParts, fmt.Sprintf("**耗时**: %s", duration))
	}
```

- [ ] **Step 5: 运行测试验证通过**

Run: `cd /Users/kelin/Work/DTWorkflow && go test ./internal/notify/ -run "TestFormatFeishuCard" -v`
Expected: 全部 PASS（包括更新后的原有测试和新增测试）

- [ ] **Step 6: 提交**

```bash
git add internal/notify/feishu_card.go internal/notify/feishu_card_test.go
git commit -m "feat(notify): 飞书卡片渲染通知时间与耗时字段"
```

---

### Task 4: 重构 buildStartMessage - 公共路径注入 MetaKeyNotifyTime（TDD）

**Files:**
- Modify: `internal/queue/processor_test.go`
- Modify: `internal/queue/processor.go:421-464`

- [ ] **Step 1: 写失败测试 - buildStartMessage 包含 notify_time**

在 `internal/queue/processor_test.go` 末尾新增：

```go
func TestBuildStartMessage_ReviewPR_HasNotifyTime(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		PRNumber:     42,
	}

	msg, ok := p.buildStartMessage(payload)
	if !ok {
		t.Fatal("buildStartMessage 应返回 true")
	}
	notifyTime := msg.Metadata[notify.MetaKeyNotifyTime]
	if notifyTime == "" {
		t.Error("开始通知应包含 notify_time")
	}
	// 格式校验
	if _, err := time.Parse("2006-01-02 15:04:05", notifyTime); err != nil {
		t.Errorf("notify_time 格式错误: %q, error: %v", notifyTime, err)
	}
}

func TestBuildStartMessage_FixIssue_HasNotifyTime(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixIssue,
		RepoOwner:    "org",
		RepoName:     "repo",
		RepoFullName: "org/repo",
		IssueNumber:  10,
	}

	msg, ok := p.buildStartMessage(payload)
	if !ok {
		t.Fatal("buildStartMessage 应返回 true")
	}
	notifyTime := msg.Metadata[notify.MetaKeyNotifyTime]
	if notifyTime == "" {
		t.Error("FixIssue 开始通知应包含 notify_time")
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/kelin/Work/DTWorkflow && go test ./internal/queue/ -run "TestBuildStartMessage_(ReviewPR|FixIssue)_HasNotifyTime" -v`
Expected: FAIL - `notify_time` 为空

- [ ] **Step 3: 重构 buildStartMessage**

将 `internal/queue/processor.go:421-464` 的 `buildStartMessage` 方法改为赋值 + 公共路径注入模式：

```go
func (p *Processor) buildStartMessage(payload model.TaskPayload) (notify.Message, bool) {
	if payload.RepoOwner == "" || payload.RepoName == "" {
		return notify.Message{}, false
	}

	var msg notify.Message
	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		if payload.PRNumber <= 0 {
			return notify.Message{}, false
		}
		msg = notify.Message{
			EventType: notify.EventPRReviewStarted,
			Severity:  notify.SeverityInfo,
			Target:    buildPRTarget(payload),
			Title:     "PR 自动评审开始",
			Body:      fmt.Sprintf("正在评审 PR #%d\n\n仓库：%s", payload.PRNumber, payload.RepoFullName),
			Metadata:  p.buildPRMetadata(payload),
		}
	case model.TaskTypeFixIssue:
		if payload.IssueNumber <= 0 {
			return notify.Message{}, false
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata[notify.MetaKeyIssueURL] = fmt.Sprintf("%s/%s/%s/issues/%d",
				p.giteaBaseURL, payload.RepoOwner, payload.RepoName, payload.IssueNumber)
		}
		msg = notify.Message{
			EventType: notify.EventIssueFixStarted,
			Severity:  notify.SeverityInfo,
			Target: notify.Target{
				Owner:  payload.RepoOwner,
				Repo:   payload.RepoName,
				Number: payload.IssueNumber,
				IsPR:   false,
			},
			Title:    "Issue 自动分析开始",
			Body:     fmt.Sprintf("正在分析 Issue #%d\n\n仓库：%s", payload.IssueNumber, payload.RepoFullName),
			Metadata: metadata,
		}
	default:
		return notify.Message{}, false
	}

	// 公共路径：统一注入通知时间
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata[notify.MetaKeyNotifyTime] = formatNotifyTime()
	return msg, true
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/kelin/Work/DTWorkflow && go test ./internal/queue/ -run "TestBuildStartMessage|TestProcessTask_ReviewPR_Send" -v`
Expected: 全部 PASS（新测试 + 原有测试不回归）

- [ ] **Step 5: 提交**

```bash
git add internal/queue/processor.go internal/queue/processor_test.go
git commit -m "feat(queue): buildStartMessage 公共路径注入 notify_time"
```

---

### Task 5: 重构 buildNotificationMessage - 公共路径注入时间字段（TDD）

**Files:**
- Modify: `internal/queue/processor_test.go`
- Modify: `internal/queue/processor.go:484-608`

- [ ] **Step 1: 写失败测试 - succeeded 包含 notify_time + duration**

在 `internal/queue/processor_test.go` 末尾新增：

```go
func TestBuildNotificationMessage_Succeeded_HasNotifyTimeAndDuration(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	startedAt := time.Now().Add(-32 * time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:       "time-succeeded",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusSucceeded,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     42,
		},
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
	}

	msg, ok := p.buildNotificationMessage(record, nil)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	notifyTime := msg.Metadata[notify.MetaKeyNotifyTime]
	if notifyTime == "" {
		t.Error("succeeded 通知应包含 notify_time")
	}
	if _, err := time.Parse("2006-01-02 15:04:05", notifyTime); err != nil {
		t.Errorf("notify_time 格式错误: %q", notifyTime)
	}

	duration := msg.Metadata[notify.MetaKeyDuration]
	if duration == "" {
		t.Error("succeeded 通知应包含 duration")
	}
	if duration != "32s" {
		t.Errorf("duration = %q, want %q", duration, "32s")
	}
}

func TestBuildNotificationMessage_Failed_HasNotifyTimeNoDuration(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	startedAt := time.Now().Add(-10 * time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:       "time-failed",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusFailed,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     42,
		},
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
	}

	msg, ok := p.buildNotificationMessage(record, nil)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	if msg.Metadata[notify.MetaKeyNotifyTime] == "" {
		t.Error("failed 通知应包含 notify_time")
	}
	if msg.Metadata[notify.MetaKeyDuration] != "" {
		t.Errorf("failed 通知不应包含 duration，got %q", msg.Metadata[notify.MetaKeyDuration])
	}
}

func TestBuildNotificationMessage_Retrying_HasNotifyTimeNoDuration(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	startedAt := time.Now().Add(-5 * time.Second)
	record := &model.TaskRecord{
		ID:       "time-retrying",
		TaskType: model.TaskTypeReviewPR,
		Status:   model.TaskStatusRetrying,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     42,
		},
		RetryCount: 1,
		MaxRetry:   3,
		StartedAt:  &startedAt,
	}

	msg, ok := p.buildNotificationMessage(record, nil)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	if msg.Metadata[notify.MetaKeyNotifyTime] == "" {
		t.Error("retrying 通知应包含 notify_time")
	}
	if msg.Metadata[notify.MetaKeyDuration] != "" {
		t.Errorf("retrying 通知不应包含 duration（CompletedAt 为 nil），got %q", msg.Metadata[notify.MetaKeyDuration])
	}
}

func TestBuildNotificationMessage_FixIssue_Succeeded_HasDuration(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	startedAt := time.Now().Add(-1*time.Minute - 30*time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:       "time-fix-succeeded",
		TaskType: model.TaskTypeFixIssue,
		Status:   model.TaskStatusSucceeded,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeFixIssue,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			IssueNumber:  10,
		},
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
	}

	msg, ok := p.buildNotificationMessage(record, nil)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	if msg.Metadata[notify.MetaKeyNotifyTime] == "" {
		t.Error("FixIssue succeeded 通知应包含 notify_time")
	}
	if msg.Metadata[notify.MetaKeyDuration] == "" {
		t.Error("FixIssue succeeded 通知应包含 duration")
	}
}

func TestBuildNotificationMessage_FixIssue_Failed_NoDuration(t *testing.T) {
	p := NewProcessor(&mockPoolRunner{}, newMockStore(), nil, slog.Default(),
		WithGiteaBaseURL("https://gitea.example.com"))

	startedAt := time.Now().Add(-10 * time.Second)
	completedAt := time.Now()
	record := &model.TaskRecord{
		ID:       "time-fix-failed",
		TaskType: model.TaskTypeFixIssue,
		Status:   model.TaskStatusFailed,
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeFixIssue,
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			IssueNumber:  10,
		},
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
	}

	msg, ok := p.buildNotificationMessage(record, nil)
	if !ok {
		t.Fatal("buildNotificationMessage 应返回 true")
	}

	if msg.Metadata[notify.MetaKeyNotifyTime] == "" {
		t.Error("FixIssue failed 通知应包含 notify_time")
	}
	if msg.Metadata[notify.MetaKeyDuration] != "" {
		t.Errorf("FixIssue failed 通知不应包含 duration，got %q", msg.Metadata[notify.MetaKeyDuration])
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/kelin/Work/DTWorkflow && go test ./internal/queue/ -run "TestBuildNotificationMessage_(Succeeded_Has|Failed_Has|Retrying_Has|FixIssue)" -v`
Expected: FAIL - `notify_time` 和 `duration` 为空

- [ ] **Step 3: 重构 buildNotificationMessage**

将 `internal/queue/processor.go:484-608` 的 `buildNotificationMessage` 方法改为赋值 + 公共路径注入模式：

```go
func (p *Processor) buildNotificationMessage(record *model.TaskRecord, reviewResult *review.ReviewResult) (notify.Message, bool) {
	if record == nil {
		return notify.Message{}, false
	}
	switch record.Status {
	case model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusRetrying:
		// 这三种状态都需要发送通知
	default:
		return notify.Message{}, false
	}

	payload := record.Payload
	if payload.RepoOwner == "" || payload.RepoName == "" {
		return notify.Message{}, false
	}

	// 构建通知正文
	var body string
	if record.Status == model.TaskStatusRetrying {
		body = fmt.Sprintf("任务执行失败，即将重试\n\n仓库：%s\n任务类型：%s", payload.RepoFullName, payload.TaskType)
	} else {
		body = fmt.Sprintf("任务 %s 执行完成\n\n仓库：%s\n任务类型：%s\n状态：%s", record.ID, payload.RepoFullName, payload.TaskType, record.Status)
	}
	if record.Error != "" {
		body += fmt.Sprintf("\n错误：%s", record.Error)
	}

	var msg notify.Message
	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		if payload.PRNumber <= 0 {
			return notify.Message{}, false
		}
		metadata := p.buildPRMetadata(payload)
		if reviewResult != nil && reviewResult.Review != nil {
			metadata[notify.MetaKeyVerdict] = string(reviewResult.Review.Verdict)
			metadata[notify.MetaKeyIssueSummary] = formatIssueSummary(reviewResult.Review.Issues)
		}
		if record.Status == model.TaskStatusRetrying {
			metadata[notify.MetaKeyRetryCount] = fmt.Sprintf("%d", record.RetryCount+1)
			metadata[notify.MetaKeyMaxRetry] = fmt.Sprintf("%d", record.MaxRetry)
			metadata[notify.MetaKeyTaskStatus] = string(record.Status)
		}
		target := buildPRTarget(payload)
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventPRReviewDone,
				Severity:  notify.SeverityInfo,
				Target:    target,
				Title:     "PR 自动评审任务完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "PR 自动评审重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default: // failed
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "PR 自动评审任务失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeFixIssue:
		if payload.IssueNumber <= 0 {
			return notify.Message{}, false
		}
		issueTarget := notify.Target{
			Owner:  payload.RepoOwner,
			Repo:   payload.RepoName,
			Number: payload.IssueNumber,
			IsPR:   false,
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata[notify.MetaKeyIssueURL] = fmt.Sprintf("%s/%s/%s/issues/%d",
				p.giteaBaseURL, payload.RepoOwner, payload.RepoName, payload.IssueNumber)
		}
		if record.Status == model.TaskStatusRetrying {
			metadata[notify.MetaKeyRetryCount] = fmt.Sprintf("%d", record.RetryCount+1)
			metadata[notify.MetaKeyMaxRetry] = fmt.Sprintf("%d", record.MaxRetry)
			metadata[notify.MetaKeyTaskStatus] = string(record.Status)
		}
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventFixIssueDone,
				Severity:  notify.SeverityInfo,
				Target:    issueTarget,
				Title:     "Issue 自动修复任务完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    issueTarget,
				Title:     "Issue 自动修复重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default: // failed
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    issueTarget,
				Title:     "Issue 自动修复任务失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	default:
		return notify.Message{}, false
	}

	// 公共路径：统一注入通知时间和耗时
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata[notify.MetaKeyNotifyTime] = formatNotifyTime()
	if record.Status == model.TaskStatusSucceeded &&
		record.StartedAt != nil && record.CompletedAt != nil {
		msg.Metadata[notify.MetaKeyDuration] = formatDuration(
			record.CompletedAt.Sub(*record.StartedAt))
	}
	return msg, true
}
```

- [ ] **Step 4: 运行全量测试验证通过**

Run: `cd /Users/kelin/Work/DTWorkflow && go test ./internal/queue/ -v`
Expected: 全部 PASS（新测试 + 全部原有测试不回归）

- [ ] **Step 5: 运行 notify 包测试确认不回归**

Run: `cd /Users/kelin/Work/DTWorkflow && go test ./internal/notify/ -v`
Expected: 全部 PASS

- [ ] **Step 6: 提交**

```bash
git add internal/queue/processor.go internal/queue/processor_test.go
git commit -m "feat(queue): buildNotificationMessage 公共路径注入 notify_time 和 duration"
```

---

### Task 6: 全量验证与最终提交

- [ ] **Step 1: 运行全项目测试**

Run: `cd /Users/kelin/Work/DTWorkflow && go test ./...`
Expected: 全部 PASS

- [ ] **Step 2: 运行 lint 检查**

Run: `cd /Users/kelin/Work/DTWorkflow && golangci-lint run ./...`
Expected: 无新增 warning/error

- [ ] **Step 3: 确认编译通过（含交叉编译）**

Run: `cd /Users/kelin/Work/DTWorkflow && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...`
Expected: 编译成功
