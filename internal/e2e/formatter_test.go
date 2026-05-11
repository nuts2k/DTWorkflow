package e2e

import (
	"strings"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

func TestFormatBugIssueBody(t *testing.T) {
	c := CaseResult{
		Name:            "create-order",
		Module:          "order",
		CasePath:        "e2e/order/cases/create-order",
		FailureCategory: "bug",
		FailureAnalysis: "取消按钮点击后状态未变更",
		Expectations: []Expectation{
			{Step: "点击取消按钮", Expect: "订单状态变为已取消"},
		},
		TestResult: &PhaseResult{
			Status: "failed",
			Scripts: []ScriptResult{
				{Name: "create-order.spec.ts", ExitCode: 1, ErrorMsg: "expect(status).toBe('cancelled')"},
			},
		},
	}
	payload := model.TaskPayload{
		TaskID:       "task-123",
		RepoFullName: "owner/repo",
		BaseRef:      "main",
	}

	body := formatBugIssueBody(c, payload, "staging", "https://app.example.com")
	if !strings.Contains(body, "E2E 测试失败") {
		t.Error("期望包含标题")
	}
	if !strings.Contains(body, "create-order") {
		t.Error("期望包含用例名")
	}
	if !strings.Contains(body, "staging") {
		t.Error("期望包含环境名")
	}
	if !strings.Contains(body, "取消按钮点击后状态未变更") {
		t.Error("期望包含失败分析")
	}
	if !strings.Contains(body, "点击取消按钮") {
		t.Error("期望包含 expectations")
	}
	if !strings.Contains(body, "dtworkflow:e2e:task-123") {
		t.Error("期望包含锚点")
	}
	if len(body) > 60000 {
		t.Errorf("body 超过 60000 字节: %d", len(body))
	}
}

func TestFormatScriptOutdatedIssueBody(t *testing.T) {
	c := CaseResult{
		Name:            "login",
		Module:          "auth",
		CasePath:        "e2e/auth/cases/login",
		FailureCategory: "script_outdated",
		FailureAnalysis: "登录按钮选择器 #login-btn 已变更为 .btn-login",
		TestResult: &PhaseResult{
			Status: "failed",
			Scripts: []ScriptResult{
				{Name: "login.spec.ts", ExitCode: 1, ErrorMsg: "locator not found"},
			},
		},
	}
	payload := model.TaskPayload{
		TaskID:       "task-456",
		RepoFullName: "owner/repo",
		BaseRef:      "main",
	}

	body := formatScriptOutdatedIssueBody(c, payload, "production", "https://app.example.com")
	if !strings.Contains(body, "修复指引") {
		t.Error("期望包含修复指引段")
	}
	if !strings.Contains(body, "fix-to-pr") {
		t.Error("期望提及 fix-to-pr 标签")
	}
	if !strings.Contains(body, "login.spec.ts") {
		t.Error("期望包含脚本名")
	}
}

func TestFormatIssueTitle(t *testing.T) {
	title := formatIssueTitle("order", "create-order", "bug")
	if title != "[E2E] order/create-order — bug" {
		t.Errorf("标题不匹配: %q", title)
	}
}

func TestFormatBugIssueBody_MarkdownEscape(t *testing.T) {
	c := CaseResult{
		Name:            "test",
		Module:          "mod",
		CasePath:        "e2e/mod/cases/test",
		FailureCategory: "bug",
		FailureAnalysis: "测试 [link](http://evil.com) 注入",
	}
	payload := model.TaskPayload{TaskID: "t1", RepoFullName: "o/r", BaseRef: "main"}
	body := formatBugIssueBody(c, payload, "dev", "http://localhost")
	if strings.Contains(body, "[link](http://evil.com)") {
		t.Error("failure_analysis 中的 Markdown 链接未被转义")
	}
}

func TestFormatBugIssueBody_LongAnalysisTruncation(t *testing.T) {
	longText := strings.Repeat("a", 5000)
	c := CaseResult{
		Name:            "test",
		Module:          "mod",
		CasePath:        "e2e/mod/cases/test",
		FailureCategory: "bug",
		FailureAnalysis: longText,
	}
	payload := model.TaskPayload{TaskID: "t1", RepoFullName: "o/r", BaseRef: "main"}
	body := formatBugIssueBody(c, payload, "dev", "http://localhost")
	if len(body) > 60000 {
		t.Errorf("body 超过 60000 字节限制: %d", len(body))
	}
}
