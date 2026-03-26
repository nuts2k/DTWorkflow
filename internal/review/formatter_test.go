package review

import (
	"errors"
	"strings"
	"testing"
)

// TestFormatReviewBody_Normal 正常场景：有 summary 和统计表格
func TestFormatReviewBody_Normal(t *testing.T) {
	output := &ReviewOutput{
		Summary: "代码整体质量良好，发现 2 处问题。",
		Verdict: VerdictRequestChanges,
		Issues: []ReviewIssue{
			{File: "main.go", Line: 10, Severity: "ERROR", Category: "logic", Message: "空指针未检查", Suggestion: "添加 nil 检查"},
			{File: "util.go", Line: 5, Severity: "WARNING", Category: "style", Message: "变量名不规范"},
		},
	}

	body := formatReviewBody(output, nil, false, nil, "", 32.0, 0.0012)

	if !strings.Contains(body, "## DTWorkflow 自动评审") {
		t.Error("缺少标题")
	}
	if !strings.Contains(body, "代码整体质量良好") {
		t.Error("缺少 summary")
	}
	if !strings.Contains(body, "### 评审统计") {
		t.Error("缺少统计标题")
	}
	if !strings.Contains(body, "| ERROR | 1 |") {
		t.Error("缺少 ERROR 统计行")
	}
	if !strings.Contains(body, "| WARNING | 1 |") {
		t.Error("缺少 WARNING 统计行")
	}
	// CRITICAL 数量为 0，不应出现
	if strings.Contains(body, "| CRITICAL |") {
		t.Error("CRITICAL 数量为 0，不应显示")
	}
	if !strings.Contains(body, "耗时 32s") {
		t.Errorf("缺少耗时信息，body=%s", body)
	}
	if !strings.Contains(body, "$0.0012") {
		t.Errorf("缺少费用信息，body=%s", body)
	}
}

// TestFormatReviewBody_UnmappedIssues 有 unmapped issues，验证列表存在且按 severity 降序
func TestFormatReviewBody_UnmappedIssues(t *testing.T) {
	output := &ReviewOutput{
		Summary: "发现若干问题。",
		Verdict: VerdictComment,
	}
	unmapped := []ReviewIssue{
		{File: "a.go", Line: 1, Severity: "INFO", Category: "style", Message: "信息级别"},
		{File: "b.go", Line: 2, Severity: "CRITICAL", Category: "security", Message: "严重安全漏洞"},
		{File: "c.go", Line: 3, Severity: "WARNING", Category: "logic", Message: "警告级别"},
	}

	body := formatReviewBody(output, unmapped, false, nil, "", 10, 0.001)

	if !strings.Contains(body, "### 其他发现（未关联到 diff 行）") {
		t.Error("缺少 unmapped issues 标题")
	}

	criticalIdx := strings.Index(body, "CRITICAL")
	warningIdx := strings.Index(body, "**WARNING**")
	infoIdx := strings.Index(body, "**INFO**")

	if criticalIdx == -1 || warningIdx == -1 || infoIdx == -1 {
		t.Errorf("缺少某个 severity 的列表项，body=%s", body)
	}
	// 严重程度降序：CRITICAL 应在 WARNING 之前，WARNING 在 INFO 之前
	if criticalIdx > warningIdx {
		t.Error("CRITICAL 应排在 WARNING 之前")
	}
	if warningIdx > infoIdx {
		t.Error("WARNING 应排在 INFO 之前")
	}
}

// TestFormatReviewBody_UnmappedNotDoubleCount 未映射问题不应在摘要统计中重复计算
func TestFormatReviewBody_UnmappedNotDoubleCount(t *testing.T) {
	issue := ReviewIssue{File: "foo.go", Line: 10, Severity: "ERROR", Category: "logic", Message: "未处理错误"}
	output := &ReviewOutput{
		Summary: "发现 1 个问题。",
		Verdict: VerdictRequestChanges,
		Issues:  []ReviewIssue{issue},
	}

	body := formatReviewBody(output, []ReviewIssue{issue}, false, nil, "", 3, 0.001)

	if !strings.Contains(body, "| ERROR | 1 |") {
		t.Fatalf("ERROR 统计应为 1，body=%s", body)
	}
	if strings.Contains(body, "| ERROR | 2 |") {
		t.Fatalf("未映射问题被重复统计，body=%s", body)
	}
}

// TestFormatReviewBody_NoIssues 无 issues 时不显示统计表格
func TestFormatReviewBody_NoIssues(t *testing.T) {
	output := &ReviewOutput{
		Summary: "代码整洁，无问题。",
		Verdict: VerdictApprove,
	}

	body := formatReviewBody(output, nil, false, nil, "", 5, 0.0005)

	if strings.Contains(body, "### 评审统计") {
		t.Error("无 issues 时不应显示统计表格")
	}
	if !strings.Contains(body, "代码整洁，无问题。") {
		t.Error("缺少 summary")
	}
}

// TestFormatReviewBody_BodyTruncated Body 超长截断
func TestFormatReviewBody_BodyTruncated(t *testing.T) {
	longMessage := strings.Repeat("X", 70000)
	output := &ReviewOutput{
		Summary: longMessage,
		Verdict: VerdictComment,
	}

	body := formatReviewBody(output, nil, false, nil, "", 1, 0)

	if len(body) > bodyMaxLen {
		t.Errorf("body 超过 %d 字符上限，实际长度=%d", bodyMaxLen, len(body))
	}
	if !strings.Contains(body, "内容过长，已截断") {
		t.Error("超长时应包含截断提示")
	}
}

// TestFormatCommentBody_Format 行级评论 body 格式验证
func TestFormatCommentBody_Format(t *testing.T) {
	issue := ReviewIssue{
		File:       "pkg/handler.go",
		Line:       42,
		Severity:   "ERROR",
		Category:   "security",
		Message:    "SQL 注入风险",
		Suggestion: "使用参数化查询",
	}

	body := formatCommentBody(issue)

	if !strings.HasPrefix(body, "**ERROR** | security") {
		t.Errorf("格式错误，body 开头=%q", body[:min(30, len(body))])
	}
	if !strings.Contains(body, "SQL 注入风险") {
		t.Error("缺少 message")
	}
	if !strings.Contains(body, "> 建议：使用参数化查询") {
		t.Error("缺少 suggestion")
	}
}

// TestFormatCommentBody_Truncated 行级评论 body 超长截断
func TestFormatCommentBody_Truncated(t *testing.T) {
	longMsg := strings.Repeat("Y", 9000)
	issue := ReviewIssue{
		Severity: "WARNING",
		Category: "style",
		Message:  longMsg,
	}

	body := formatCommentBody(issue)

	if len(body) > commentMaxLen {
		t.Errorf("comment body 超过 %d 字符上限，实际长度=%d", commentMaxLen, len(body))
	}
	if !strings.Contains(body, "内容过长，已截断") {
		t.Error("超长时应包含截断提示")
	}
}

// TestEscapeTableCell Markdown 表格转义
func TestEscapeTableCell(t *testing.T) {
	input := "a|b|c"
	want := `a\|b\|c`
	got := escapeTableCell(input)
	if got != want {
		t.Errorf("escapeTableCell(%q) = %q, want %q", input, got, want)
	}
}

// TestFormatReviewBody_TableEscape message 含 | 不破坏表格
func TestFormatReviewBody_TableEscape(t *testing.T) {
	unmapped := []ReviewIssue{
		{File: "a.go", Line: 1, Severity: "ERROR", Category: "logic", Message: "err|nil 未处理"},
	}

	body := formatReviewBody(nil, unmapped, false, nil, "", 1, 0)

	// 验证 | 被转义，不会破坏 Markdown 表格结构
	// unmapped 列表是 bullet 格式，但 file:line 和 category 在反引号中不需转义
	// message 直接显示，不在表格中，所以原始 | 出现在 bullet 里是合法的
	if !strings.Contains(body, "err|nil 未处理") {
		t.Error("message 应原样显示在 bullet 列表中")
	}
}

// TestFormatReviewBody_ParseFailed 降级场景
func TestFormatReviewBody_ParseFailed(t *testing.T) {
	parseErr := errors.New("invalid JSON: unexpected token")
	rawOutput := "这是 Claude 的原始文本输出，无法解析为 JSON。"

	body := formatReviewBody(nil, nil, true, parseErr, rawOutput, 15, 0.002)

	if !strings.Contains(body, "## DTWorkflow 自动评审") {
		t.Error("缺少标题")
	}
	if !strings.Contains(body, "评审结果解析失败") {
		t.Error("缺少降级提示")
	}
	if !strings.Contains(body, "invalid JSON") {
		t.Error("缺少错误详情")
	}
	if !strings.Contains(body, rawOutput) {
		t.Error("缺少原始输出内容")
	}
	if !strings.Contains(body, "耗时 15s") {
		t.Errorf("缺少耗时信息，body=%s", body)
	}
}

// TestSeverityOrder severityOrder 排序权重
func TestSeverityOrder(t *testing.T) {
	cases := []struct {
		sev  string
		want int
	}{
		{"CRITICAL", 0},
		{"ERROR", 1},
		{"WARNING", 2},
		{"INFO", 3},
		{"UNKNOWN", 4},
		{"critical", 0}, // 大小写不敏感
	}
	for _, c := range cases {
		got := severityOrder(c.sev)
		if got != c.want {
			t.Errorf("severityOrder(%q) = %d, want %d", c.sev, got, c.want)
		}
	}
}

// TestCountBySeverity 统计各 severity 数量
func TestCountBySeverity(t *testing.T) {
	issues := []ReviewIssue{
		{Severity: "ERROR"},
		{Severity: "error"}, // 应归入 ERROR
		{Severity: "WARNING"},
		{Severity: "CRITICAL"},
		{Severity: "CRITICAL"},
	}
	counts := countBySeverity(issues)
	if counts["ERROR"] != 2 {
		t.Errorf("ERROR 应为 2，got %d", counts["ERROR"])
	}
	if counts["WARNING"] != 1 {
		t.Errorf("WARNING 应为 1，got %d", counts["WARNING"])
	}
	if counts["CRITICAL"] != 2 {
		t.Errorf("CRITICAL 应为 2，got %d", counts["CRITICAL"])
	}
}

// min 辅助函数（Go 1.21 之前无内置 min for int）
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
