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

// --- M4.2 gen_tests 事件渲染测试 ---

// TestFormatFeishuCard_GenTestsStarted_Blue 验证 gen_tests 开始事件为蓝色卡片且渲染基础字段。
func TestFormatFeishuCard_GenTestsStarted_Blue(t *testing.T) {
	msg := Message{
		EventType: EventGenTestsStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo"},
		Title:     "ignored in favor of event mapping",
		Body:      "正在为仓库 org/repo 生成测试",
		Metadata: map[string]string{
			MetaKeyModule:    "service/user",
			MetaKeyFramework: "junit5",
		},
	}
	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "blue" {
		t.Errorf("template = %v, want blue (gen_tests started)", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "测试生成开始" {
		t.Errorf("title = %q, want %q", title["content"], "测试生成开始")
	}

	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)
	if !strings.Contains(md, "**模块**: service/user") {
		t.Errorf("markdown 缺少 module 字段: %s", md)
	}
	if !strings.Contains(md, "**框架**: junit5") {
		t.Errorf("markdown 缺少 framework 字段: %s", md)
	}
}

// TestFormatFeishuCard_GenTestsDone_GreenWithPR 验证 Done 事件为绿色卡片且包含 PR 按钮。
func TestFormatFeishuCard_GenTestsDone_GreenWithPR(t *testing.T) {
	msg := Message{
		EventType: EventGenTestsDone,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo"},
		Body:      "测试生成成功",
		Metadata: map[string]string{
			MetaKeyPRURL:          "https://gitea.example.com/org/repo/pulls/99",
			MetaKeyModule:         "all",
			MetaKeyFramework:      "vitest",
			MetaKeyGeneratedCount: "12",
			MetaKeyCommittedCount: "10",
			MetaKeySkippedCount:   "2",
		},
	}
	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "green" {
		t.Errorf("template = %v, want green (gen_tests done)", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "测试生成完成" {
		t.Errorf("title = %q, want %q", title["content"], "测试生成完成")
	}

	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)
	for _, want := range []string{
		"**模块**: all",
		"**框架**: vitest",
		"**生成文件数**: 12",
		"**提交文件数**: 10",
		"**跳过数**: 2",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown 缺少字段 %q：%s", want, md)
		}
	}

	if len(elements) < 2 {
		t.Fatalf("Done 事件应包含 PR 按钮 action element")
	}
	action := elements[1].(map[string]any)
	actions := action["actions"].([]any)
	btn := actions[0].(map[string]any)
	if btn["url"] != "https://gitea.example.com/org/repo/pulls/99" {
		t.Errorf("按钮 URL = %v, want pr_url", btn["url"])
	}
	if btn["type"] != "primary" {
		t.Errorf("按钮 type = %v, want primary", btn["type"])
	}
	btnText := btn["text"].(map[string]string)["content"]
	if btnText != "查看测试 PR" {
		t.Errorf("按钮文案 = %q, want %q", btnText, "查看测试 PR")
	}
}

// TestFormatFeishuCard_GenTestsFailed_Infrastructure 验证 infrastructure 失败 → orange (Warning) + 对应提示。
func TestFormatFeishuCard_GenTestsFailed_Infrastructure(t *testing.T) {
	msg := Message{
		EventType: EventGenTestsFailed,
		Severity:  SeverityWarning,
		Target:    Target{Owner: "org", Repo: "repo"},
		Body:      "容器执行失败",
		Metadata: map[string]string{
			MetaKeyModule:          "service/user",
			MetaKeyFailureCategory: "infrastructure",
		},
	}
	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "orange" {
		t.Errorf("template = %v, want orange (infrastructure → Warning)", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "测试生成失败（基础设施故障）" {
		t.Errorf("title = %q", title["content"])
	}

	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)
	if !strings.Contains(md, "基础设施故障，建议重试或检查环境") {
		t.Errorf("markdown 缺少 infrastructure 提示文案: %s", md)
	}
	if !strings.Contains(md, "**失败分类**: infrastructure") {
		t.Errorf("markdown 缺少 failure_category 字段: %s", md)
	}
}

// TestFormatFeishuCard_GenTestsFailed_TestQuality 验证 test_quality 失败 → blue (Info) + 带 generated_count 的提示。
func TestFormatFeishuCard_GenTestsFailed_TestQuality(t *testing.T) {
	msg := Message{
		EventType: EventGenTestsFailed,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo"},
		Metadata: map[string]string{
			MetaKeyFailureCategory: "test_quality",
			MetaKeyGeneratedCount:  "7",
		},
	}
	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "blue" {
		t.Errorf("template = %v, want blue (test_quality → Info)", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "测试生成未达标" {
		t.Errorf("title = %q", title["content"])
	}

	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)
	if !strings.Contains(md, "测试质量未达标，已生成的 7 个测试可参考") {
		t.Errorf("markdown 缺少 test_quality 提示（含 generated_count=7）: %s", md)
	}
}

// TestFormatFeishuCard_GenTestsFailed_InfoInsufficient 验证 info_insufficient → blue (Info) + 补充信息提示。
func TestFormatFeishuCard_GenTestsFailed_InfoInsufficient(t *testing.T) {
	msg := Message{
		EventType: EventGenTestsFailed,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo"},
		Metadata: map[string]string{
			MetaKeyFailureCategory: "info_insufficient",
		},
	}
	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "blue" {
		t.Errorf("template = %v, want blue (info_insufficient → Info)", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "测试生成信息不足" {
		t.Errorf("title = %q", title["content"])
	}

	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)
	if !strings.Contains(md, "信息不足，请补充相关上下文后重试") {
		t.Errorf("markdown 缺少 info_insufficient 提示: %s", md)
	}
}

// TestFormatFeishuCard_GenTestsFailed_Fallback 验证 failure_category 为空或未知时走兜底 orange。
func TestFormatFeishuCard_GenTestsFailed_Fallback(t *testing.T) {
	msg := Message{
		EventType: EventGenTestsFailed,
		Severity:  SeverityWarning,
		Target:    Target{Owner: "org", Repo: "repo"},
		// failure_category 缺失
	}
	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "orange" {
		t.Errorf("template = %v, want orange (fallback)", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "测试生成失败" {
		t.Errorf("title = %q", title["content"])
	}
}

func TestFormatFeishuCard_CodeFromDocFailedIncludesReasonAndMissingInfo(t *testing.T) {
	msg := Message{
		EventType: EventCodeFromDocFailed,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 12, IsPR: true},
		Metadata: map[string]string{
			MetaKeyDocPath:         "docs/spec.md",
			MetaKeyBranchName:      "auto-code/spec",
			MetaKeyFailureCategory: "info_insufficient",
			MetaKeyFailureReason:   "缺少接口错误码定义",
			MetaKeyMissingInfo:     "补充错误码表\n提供鉴权流程",
		},
	}
	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	card := result["card"].(map[string]any)
	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)
	for _, want := range []string{
		"**失败分类**: info_insufficient",
		"**失败原因**: 缺少接口错误码定义",
		"**缺失信息**:",
		"- 补充错误码表",
		"- 提供鉴权流程",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown 缺少字段 %q: %s", want, md)
		}
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

// --- M5.4 E2E 回归分析（triage）事件渲染测试 ---

// TestBuildCard_E2ETriageStarted 验证 triage 开始事件为蓝色卡片，显示 PR 信息。
func TestBuildCard_E2ETriageStarted(t *testing.T) {
	msg := Message{
		EventType: EventE2ETriageStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "app", Number: 55, IsPR: true},
		Title:     "ignored",
		Body:      "正在分析 PR #55 的变更影响",
		Metadata: map[string]string{
			MetaKeyPRURL:   "https://gitea.example.com/org/app/pulls/55",
			MetaKeyPRTitle: "重构用户模块",
		},
	}
	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "blue" {
		t.Errorf("template = %v, want blue", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "E2E 回归分析开始" {
		t.Errorf("title = %q, want %q", title["content"], "E2E 回归分析开始")
	}

	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)
	if !strings.Contains(md, "**PR**: #55 - 重构用户模块") {
		t.Errorf("markdown 缺少 PR 信息: %s", md)
	}
}

// TestBuildCard_E2ETriageDone_WithModules 验证 triage 完成且有选中模块时，
// 绿色卡片 + 选中模块列表 + 跳过模块列表。
func TestBuildCard_E2ETriageDone_WithModules(t *testing.T) {
	msg := Message{
		EventType: EventE2ETriageDone,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "app", Number: 55, IsPR: true},
		Body:      "",
		Metadata: map[string]string{
			MetaKeyPRURL:                "https://gitea.example.com/org/app/pulls/55",
			MetaKeyPRTitle:              "重构用户模块",
			MetaKeyTriageModules:        `[{"name":"user","reason":"用户模块有变更"},{"name":"order","reason":"订单接口被调用"}]`,
			MetaKeyTriageSkippedModules: `[{"name":"payment","reason":"无相关变更"}]`,
			MetaKeyTriageAnalysis:       "本次变更涉及用户模块核心逻辑，需要回归测试",
		},
	}
	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "green" {
		t.Errorf("template = %v, want green", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "E2E 回归分析完成" {
		t.Errorf("title = %q, want %q", title["content"], "E2E 回归分析完成")
	}

	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)
	// 选中模块
	if !strings.Contains(md, "**选中模块**:") {
		t.Errorf("markdown 缺少选中模块标题: %s", md)
	}
	if !strings.Contains(md, "user") {
		t.Errorf("markdown 缺少 user 模块: %s", md)
	}
	if !strings.Contains(md, "order") {
		t.Errorf("markdown 缺少 order 模块: %s", md)
	}
	// 跳过模块
	if !strings.Contains(md, "**跳过模块**:") {
		t.Errorf("markdown 缺少跳过模块标题: %s", md)
	}
	if !strings.Contains(md, "payment") {
		t.Errorf("markdown 缺少 payment 跳过模块: %s", md)
	}
	// 分析摘要
	if !strings.Contains(md, "**分析摘要**:") {
		t.Errorf("markdown 缺少分析摘要: %s", md)
	}
	// 不应出现"无需回归"
	if strings.Contains(md, "本次变更无需 E2E 回归") {
		t.Errorf("有选中模块时不应显示无需回归文案: %s", md)
	}
}

// TestBuildCard_E2ETriageDone_EmptyModules 验证 triage 完成但无需回归时，
// 绿色卡片 + "本次变更无需 E2E 回归"文案。
func TestBuildCard_E2ETriageDone_EmptyModules(t *testing.T) {
	msg := Message{
		EventType: EventE2ETriageDone,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "app", Number: 56, IsPR: true},
		Metadata: map[string]string{
			MetaKeyPRURL:          "https://gitea.example.com/org/app/pulls/56",
			MetaKeyTriageModules:  `[]`,
			MetaKeyTriageAnalysis: "变更仅涉及文档，无需 E2E 回归",
		},
	}
	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "green" {
		t.Errorf("template = %v, want green", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "E2E 回归分析完成" {
		t.Errorf("title = %q, want %q", title["content"], "E2E 回归分析完成")
	}

	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)
	if !strings.Contains(md, "本次变更无需 E2E 回归") {
		t.Errorf("空模块时应显示无需回归文案: %s", md)
	}
	if strings.Contains(md, "**选中模块**") {
		t.Errorf("空模块时不应显示选中模块标题: %s", md)
	}
}

// TestBuildCard_E2ETriageFailed 验证 triage 失败事件为红色卡片 + 手动触发提示。
func TestBuildCard_E2ETriageFailed(t *testing.T) {
	msg := Message{
		EventType: EventE2ETriageFailed,
		Severity:  SeverityWarning,
		Target:    Target{Owner: "org", Repo: "app", Number: 57, IsPR: true},
		Body:      "容器执行超时",
		Metadata: map[string]string{
			MetaKeyPRURL:   "https://gitea.example.com/org/app/pulls/57",
			MetaKeyPRTitle: "新增支付功能",
		},
	}
	result, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "red" {
		t.Errorf("template = %v, want red", header["template"])
	}
	title := header["title"].(map[string]string)
	if title["content"] != "E2E 回归分析失败" {
		t.Errorf("title = %q, want %q", title["content"], "E2E 回归分析失败")
	}

	elements := card["elements"].([]any)
	md := elements[0].(map[string]any)["content"].(string)
	if !strings.Contains(md, "回归分析失败，可手动触发 E2E 测试") {
		t.Errorf("markdown 缺少手动触发提示: %s", md)
	}
}
