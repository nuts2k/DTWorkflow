package notify

import (
	"encoding/json"
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
			"pr_url":   "https://gitea.example.com/org/repo/pulls/42",
			"pr_title": "修复登录验证逻辑",
		},
	}

	data, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("JSON unmarshal error: %v", err)
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
			"pr_url":        "https://gitea.example.com/org/repo/pulls/42",
			"pr_title":      "修复登录验证逻辑",
			"verdict":       "approve",
			"issue_summary": "2 WARNING, 1 INFO",
		},
	}

	data, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("JSON unmarshal error: %v", err)
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
			"pr_url":  "https://gitea.example.com/org/repo/pulls/42",
			"verdict": "request_changes",
		},
	}

	data, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("JSON unmarshal error: %v", err)
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
			"pr_url": "https://gitea.example.com/org/repo/pulls/42",
		},
	}

	data, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("JSON unmarshal error: %v", err)
	}

	card := result["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "red" {
		t.Errorf("template = %v, want red (error)", header["template"])
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

	data, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("JSON unmarshal error: %v", err)
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

	data, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("空 Body 时也应能生成卡片")
	}
}
