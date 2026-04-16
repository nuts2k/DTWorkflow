package notify

import (
	"strings"
	"testing"
)

func TestFormatFeishuCard_PRReviewStarted(t *testing.T) {
	msg := Message{
		EventType: EventPRReviewStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 42, IsPR: true},
		Title:     "PR 自动评审开始",
		Body:      "正在评审 PR #42\n\n仓库：org/repo",
		Metadata: map[string]string{
			MetaKeyPRURL:      "https://gitea.example.com/org/repo/pulls/42",
			MetaKeyPRTitle:    "修复登录验证逻辑",
			MetaKeyNotifyTime: "2026-04-13 14:30:05",
		},
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	if result["msg_type"] != "interactive" {
		t.Errorf("msg_type = %v, want interactive", result["msg_type"])
	}

	card, ok := result["card"].(map[string]any)
	if !ok {
		t.Fatal("card 字段缺失")
	}
	header, ok := card["header"].(map[string]any)
	if !ok {
		t.Fatal("header 字段缺失")
	}
	if header["template"] != "blue" {
		t.Errorf("template = %v, want blue", header["template"])
	}
}

func TestFormatFeishuCard_PRReviewDone_Approve(t *testing.T) {
	msg := Message{
		EventType: EventPRReviewDone,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 42, IsPR: true},
		Title:     "PR 自动评审任务完成",
		Body:      "任务执行完成",
		Metadata: map[string]string{
			MetaKeyPRURL:        "https://gitea.example.com/org/repo/pulls/42",
			MetaKeyPRTitle:      "修复登录验证逻辑",
			MetaKeyVerdict:      "approve",
			MetaKeyIssueSummary: "2 WARNING, 1 INFO",
			MetaKeyNotifyTime:   "2026-04-13 14:31:37",
			MetaKeyDuration:     "32s",
		},
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "green" {
		t.Errorf("template = %v, want green (approve)", header["template"])
	}
}

func TestFormatFeishuCard_PRReviewDone_RequestChanges(t *testing.T) {
	msg := Message{
		EventType: EventPRReviewDone,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 42, IsPR: true},
		Title:     "PR 自动评审任务完成",
		Body:      "任务执行完成",
		Metadata: map[string]string{
			MetaKeyPRURL:   "https://gitea.example.com/org/repo/pulls/42",
			MetaKeyVerdict: "request_changes",
		},
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "orange" {
		t.Errorf("template = %v, want orange (request_changes)", header["template"])
	}
}

func TestFormatFeishuCard_SystemError(t *testing.T) {
	msg := Message{
		EventType: EventSystemError,
		Severity:  SeverityWarning,
		Target:    Target{Owner: "org", Repo: "repo", Number: 42, IsPR: true},
		Title:     "PR 自动评审任务失败",
		Body:      "容器执行超时",
		Metadata: map[string]string{
			MetaKeyPRURL: "https://gitea.example.com/org/repo/pulls/42",
		},
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "red" {
		t.Errorf("template = %v, want red (error)", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "PR 自动评审任务失败" {
		t.Errorf("title = %q, want %q", title["content"], "PR 自动评审任务失败")
	}
}

func TestFormatFeishuCard_SystemErrorRetrying(t *testing.T) {
	msg := Message{
		EventType: EventSystemError,
		Severity:  SeverityWarning,
		Target:    Target{Owner: "org", Repo: "repo", Number: 42, IsPR: true},
		Title:     "PR 自动评审重试中",
		Body:      "任务执行失败，即将重试",
		Metadata: map[string]string{
			MetaKeyPRURL:      "https://gitea.example.com/org/repo/pulls/42",
			MetaKeyTaskStatus: "retrying",
			MetaKeyRetryCount: "2",
			MetaKeyMaxRetry:   "3",
			MetaKeyNotifyTime: "2026-04-13 14:32:10",
		},
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "orange" {
		t.Errorf("template = %v, want orange (retrying)", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "PR 自动评审重试中" {
		t.Errorf("title = %q, want %q", title["content"], "PR 自动评审重试中")
	}
}

func TestFormatFeishuCard_NoMetadata_Degrades(t *testing.T) {
	msg := Message{
		EventType: EventPRReviewDone,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 42, IsPR: true},
		Title:     "PR 自动评审任务完成",
		Body:      "任务执行完成",
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	card := result["card"].(map[string]any)
	elements, ok := card["elements"].([]any)
	if !ok || len(elements) == 0 {
		t.Fatal("即使无 Metadata，elements 也应包含 markdown 内容")
	}
}

func TestFormatFeishuCard_EmptyBody(t *testing.T) {
	msg := Message{
		EventType: EventPRReviewStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1, IsPR: true},
		Title:     "PR 自动评审开始",
		Body:      "",
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	if result == nil {
		t.Fatal("空 Body 时也应能生成卡片")
	}
}

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

// TestFormatFeishuCard_FixIssueDone_GreenWithPRButton 验证 fix 完成事件应绿色卡片 + "查看修复 PR" 按钮。
func TestFormatFeishuCard_FixIssueDone_GreenWithPRButton(t *testing.T) {
	msg := Message{
		EventType: EventFixIssueDone,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 7, IsPR: false},
		Title:     "Issue 修复 PR 已创建",
		Body:      "已为 Issue #7 创建修复 PR",
		Metadata: map[string]string{
			MetaKeyIssueURL: "https://gitea.example.com/org/repo/issues/7",
			MetaKeyPRURL:    "https://gitea.example.com/org/repo/pulls/8",
		},
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "green" {
		t.Errorf("template = %v, want green (fix issue done)", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "Issue 修复 PR 已创建" {
		t.Errorf("title = %q, want %q", title["content"], "Issue 修复 PR 已创建")
	}

	elements := card["elements"].([]any)
	if len(elements) < 2 {
		t.Fatal("应包含按钮 action element")
	}
	action := elements[1].(map[string]any)
	actions := action["actions"].([]any)
	btn := actions[0].(map[string]any)
	btnText := btn["text"].(map[string]string)["content"]
	if btnText != "查看修复 PR" {
		t.Errorf("按钮文案 = %q, want %q", btnText, "查看修复 PR")
	}
	if btn["type"] != "primary" {
		t.Errorf("按钮类型 = %v, want primary", btn["type"])
	}
}

// TestFormatFeishuCard_IssueAnalyzeStarted_Blue 验证分析开始事件应蓝色卡片 + "查看 Issue" 按钮。
func TestFormatFeishuCard_IssueAnalyzeStarted_Blue(t *testing.T) {
	msg := Message{
		EventType: EventIssueAnalyzeStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 7, IsPR: false},
		Title:     "Issue 分析开始",
		Body:      "正在分析 Issue #7",
		Metadata: map[string]string{
			MetaKeyIssueURL: "https://gitea.example.com/org/repo/issues/7",
		},
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "blue" {
		t.Errorf("template = %v, want blue (issue analyze started)", header["template"])
	}

	elements := card["elements"].([]any)
	if len(elements) < 2 {
		t.Fatal("应包含按钮 action element")
	}
	action := elements[1].(map[string]any)
	actions := action["actions"].([]any)
	btn := actions[0].(map[string]any)
	btnText := btn["text"].(map[string]string)["content"]
	if btnText != "查看 Issue" {
		t.Errorf("按钮文案 = %q, want %q", btnText, "查看 Issue")
	}
}

// TestFormatFeishuCard_IssueFixStarted_Blue 验证修复开始事件应蓝色卡片 + "查看 Issue" 按钮。
func TestFormatFeishuCard_IssueFixStarted_Blue(t *testing.T) {
	msg := Message{
		EventType: EventIssueFixStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 7, IsPR: false},
		Title:     "Issue 修复开始",
		Body:      "正在修复 Issue #7",
		Metadata: map[string]string{
			MetaKeyIssueURL: "https://gitea.example.com/org/repo/issues/7",
		},
	}

	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "blue" {
		t.Errorf("template = %v, want blue (issue fix started)", header["template"])
	}

	elements := card["elements"].([]any)
	if len(elements) < 2 {
		t.Fatal("应包含按钮 action element")
	}
	action := elements[1].(map[string]any)
	actions := action["actions"].([]any)
	btn := actions[0].(map[string]any)
	btnText := btn["text"].(map[string]string)["content"]
	if btnText != "查看 Issue" {
		t.Errorf("按钮文案 = %q, want %q", btnText, "查看 Issue")
	}
}
