package review

import (
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

	body := formatReviewBody(FormatOptions{Review: output, ParseFailed: false, DurationSec: 32.0, CostUSD: 0.0012})

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

	body := formatReviewBody(FormatOptions{Review: output, Unmapped: unmapped, ParseFailed: false, DurationSec: 10, CostUSD: 0.001})

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

	body := formatReviewBody(FormatOptions{Review: output, Unmapped: []ReviewIssue{issue}, ParseFailed: false, DurationSec: 3, CostUSD: 0.001})

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

	body := formatReviewBody(FormatOptions{Review: output, ParseFailed: false, DurationSec: 5, CostUSD: 0.0005})

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

	body := formatReviewBody(FormatOptions{Review: output, ParseFailed: false, DurationSec: 1, CostUSD: 0})

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
	// escapeMarkdown 不影响无特殊字符的中文文本
	if !strings.Contains(body, "SQL 注入风险") {
		t.Error("缺少 message")
	}
	if !strings.Contains(body, "> 建议：使用参数化查询") {
		t.Error("缺少 suggestion")
	}
}

func TestFormatCommentBody_EmptyCategory(t *testing.T) {
	issue := ReviewIssue{
		Severity: "ERROR",
		Message:  "缺少分类时也应保持格式正常",
	}

	body := formatCommentBody(issue)

	if !strings.HasPrefix(body, "**ERROR**\n\n") {
		t.Errorf("空 category 时头部格式错误，body=%q", body)
	}
	if strings.Contains(body, "| ") {
		t.Errorf("空 category 时不应包含悬空分隔符，body=%q", body)
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

func TestFormatReviewBody_StripsNonBMPChars(t *testing.T) {
	output := &ReviewOutput{
		Summary: "总结含🤖符号",
		Verdict: VerdictComment,
	}
	unmapped := []ReviewIssue{
		{File: "a.go", Line: 1, Severity: "WARNING", Category: "logic", Message: "消息含🚀符号"},
	}

	body := formatReviewBody(FormatOptions{Review: output, Unmapped: unmapped, ParseFailed: false, DurationSec: 1, CostUSD: 0})

	for _, bad := range []string{"🤖", "🚀"} {
		if strings.Contains(body, bad) {
			t.Errorf("body 应剔除非 BMP 字符 %q，实际: %q", bad, body)
		}
	}
	if !strings.Contains(body, "总结含符号") || !strings.Contains(body, "消息含符号") {
		t.Errorf("正常文本应保留，实际: %q", body)
	}
}

func TestFormatCommentBody_StripsNonBMPChars(t *testing.T) {
	issue := ReviewIssue{
		Severity:   "ERROR",
		Category:   "logic",
		Message:    "消息含🤖符号",
		Suggestion: "建议含🚀符号",
	}

	body := formatCommentBody(issue)

	for _, bad := range []string{"🤖", "🚀"} {
		if strings.Contains(body, bad) {
			t.Errorf("comment body 应剔除非 BMP 字符 %q，实际: %q", bad, body)
		}
	}
	if !strings.Contains(body, "消息含符号") || !strings.Contains(body, "建议含符号") {
		t.Errorf("正常文本应保留，实际: %q", body)
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

// TestFormatReviewBody_TableEscape message 含 | 经 escapeMarkdown 后保持可读
func TestFormatReviewBody_TableEscape(t *testing.T) {
	unmapped := []ReviewIssue{
		{File: "a.go", Line: 1, Severity: "ERROR", Category: "logic", Message: "err|nil 未处理"},
	}

	body := formatReviewBody(FormatOptions{Unmapped: unmapped, ParseFailed: false, DurationSec: 1, CostUSD: 0})

	// escapeMarkdown 不转义 |，所以 | 保持原样
	if !strings.Contains(body, "err|nil") {
		t.Error("message 中的 | 应保持原样")
	}
}

func TestFormatReviewBody_UnmappedTitleDetailStaysSingleBullet(t *testing.T) {
	unmapped := []ReviewIssue{
		{
			Severity: "ERROR",
			Message:  "权限校验缺失： 接口未校验用户权限",
		},
	}

	body := formatReviewBody(FormatOptions{Unmapped: unmapped, ParseFailed: false, DurationSec: 1, CostUSD: 0})

	if !strings.Contains(body, "- **ERROR**: 权限校验缺失： 接口未校验用户权限") {
		t.Errorf("unmapped issue 应保持单条 bullet，body=%s", body)
	}
}

// TestFormatReviewBody_ParseFailed 降级场景
func TestFormatReviewBody_ParseFailed(t *testing.T) {
	rawOutput := "这是 Claude 的原始文本输出，无法解析为 JSON。"

	body := formatReviewBody(FormatOptions{ParseFailed: true, RawOutput: rawOutput, DurationSec: 15, CostUSD: 0.002})

	if !strings.Contains(body, "## DTWorkflow 自动评审") {
		t.Error("缺少标题")
	}
	if !strings.Contains(body, "评审结果解析失败") {
		t.Error("缺少降级提示")
	}
	// 降级场景不应暴露内部错误详情
	if strings.Contains(body, "invalid JSON") {
		t.Error("不应暴露内部解析错误详情")
	}
	// rawOutput 应被代码块包裹
	if !strings.Contains(body, "```\n"+rawOutput+"\n```") {
		t.Error("原始输出应被代码块包裹")
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

// TestEscapeMarkdown escapeMarkdown 转义 Markdown 特殊字符
func TestEscapeMarkdown(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "链接语法",
			input: "[点击这里](https://evil.com)",
			want:  `\[点击这里\]\(https://evil.com\)`,
		},
		{
			name:  "图片语法",
			input: "![alt](img.png)",
			want:  `\!\[alt\]\(img.png\)`,
		},
		{
			name:  "HTML 标签",
			input: "<script>alert(1)</script>",
			want:  `\<script\>alert\(1\)\</script\>`,
		},
		{
			name:  "无特殊字符",
			input: "普通中文文本 abc 123",
			want:  "普通中文文本 abc 123",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := escapeMarkdown(tc.input)
			if got != tc.want {
				t.Errorf("escapeMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestTruncateString truncateString 截断测试（含中文多字节字符）
func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		wantSafe bool // 结果应为合法 UTF-8
	}{
		{
			name:     "不需要截断",
			input:    "hello",
			maxBytes: 10,
		},
		{
			name:     "ASCII 截断",
			input:    "hello world",
			maxBytes: 5,
		},
		{
			name:     "中文截断不破坏字符",
			input:    "你好世界", // 每个中文 3 字节，总共 12 字节
			maxBytes: 7,      // 截断在第 3 个字符中间
			wantSafe: true,
		},
		{
			name:     "中文截断在字符边界",
			input:    "你好世界",
			maxBytes: 6, // 恰好是 2 个完整中文字符
			wantSafe: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateString(tc.input, tc.maxBytes)
			if len(tc.input) <= tc.maxBytes {
				// 不需要截断时应返回原字符串
				if got != tc.input {
					t.Errorf("不需要截断时应返回原字符串，got=%q", got)
				}
				return
			}
			// 截断后长度不应超过 maxBytes + len("…")
			if len(got) > tc.maxBytes+len("…") {
				t.Errorf("截断后长度超出预期，got len=%d, maxBytes=%d", len(got), tc.maxBytes)
			}
			// 应包含省略号
			if !strings.HasSuffix(got, "…") {
				t.Errorf("截断后应以省略号结尾，got=%q", got)
			}
		})
	}

	// 特别测试：中文截断不产生乱码
	result := truncateString("你好世界", 7)
	// 7 字节 → 回退到 6 字节（2 个完整中文） + "…"
	if !strings.Contains(result, "你好") {
		t.Errorf("中文截断应保留完整字符，got=%q", result)
	}
	if strings.Contains(result, "世") {
		t.Errorf("中文截断应去掉不完整的字符，got=%q", result)
	}
}

// TestFormatReviewBody_SupersededWithSHA M2.4: SupersededCount > 0 且有 PreviousHeadSHA 时，body 应包含替代标注（含短 SHA）
func TestFormatReviewBody_SupersededWithSHA(t *testing.T) {
	output := &ReviewOutput{
		Summary: "代码无问题。",
		Verdict: VerdictApprove,
	}

	body := formatReviewBody(FormatOptions{
		Review:          output,
		ParseFailed:     false,
		DurationSec:     5,
		CostUSD:         0.001,
		SupersededCount: 1,
		PreviousHeadSHA: "abcdef1234567",
	})

	// 应包含短 SHA（前 7 位）的替代标注
	if !strings.Contains(body, "本评审基于最新提交，替代了之前基于 `abcdef1` 的评审。") {
		t.Errorf("body 应包含带短 SHA 的替代标注，实际 body=%s", body)
	}
}

// TestFormatReviewBody_SupersededNoSHA M2.4: SupersededCount > 0 但无 PreviousHeadSHA 时，body 应包含通用替代标注
func TestFormatReviewBody_SupersededNoSHA(t *testing.T) {
	output := &ReviewOutput{
		Summary: "代码无问题。",
		Verdict: VerdictApprove,
	}

	body := formatReviewBody(FormatOptions{
		Review:          output,
		ParseFailed:     false,
		DurationSec:     5,
		CostUSD:         0.001,
		SupersededCount: 2,
		PreviousHeadSHA: "",
	})

	// 应包含通用替代标注（无 SHA）
	if !strings.Contains(body, "本评审基于最新提交，替代了之前的评审。") {
		t.Errorf("body 应包含通用替代标注，实际 body=%s", body)
	}
	// 不应包含反引号括起来的 SHA
	if strings.Contains(body, "基于 `") {
		t.Errorf("无 PreviousHeadSHA 时不应出现 SHA，实际 body=%s", body)
	}
}

// TestFormatReviewBody_NoSuperseded M2.4: SupersededCount == 0 时，body 不应包含替代标注
func TestFormatReviewBody_NoSuperseded(t *testing.T) {
	output := &ReviewOutput{
		Summary: "代码无问题。",
		Verdict: VerdictApprove,
	}

	body := formatReviewBody(FormatOptions{
		Review:          output,
		ParseFailed:     false,
		DurationSec:     5,
		CostUSD:         0.001,
		SupersededCount: 0,
		PreviousHeadSHA: "abcdef1",
	})

	// 不应包含任何替代标注文本
	if strings.Contains(body, "替代") {
		t.Errorf("SupersededCount==0 时 body 不应包含替代标注，实际 body=%s", body)
	}
}

// --- M2.5 过滤测试 ---

// TestFormatReviewBody_FilterHint_SeverityOnly 仅 severity 过滤时显示对应提示
func TestFormatReviewBody_FilterHint_SeverityOnly(t *testing.T) {
	output := &ReviewOutput{
		Summary: "代码评审完成。",
		Verdict: VerdictComment,
	}

	body := formatReviewBody(FormatOptions{
		Review:             output,
		ParseFailed:        false,
		DurationSec:        5,
		CostUSD:            0.001,
		FilteredBySeverity: 3,
		SeverityThreshold:  "warning",
	})

	if !strings.Contains(body, "3 个低于阈值(warning)的问题") {
		t.Errorf("body 应包含 severity 过滤提示，实际: %s", body)
	}
	if !strings.Contains(body, "已按配置过滤") {
		t.Errorf("body 应包含'已按配置过滤'，实际: %s", body)
	}
	// 不应有文件过滤提示
	if strings.Contains(body, "被忽略文件") {
		t.Errorf("无文件过滤时不应包含文件过滤提示，实际: %s", body)
	}
}

// TestFormatReviewBody_FilterHint_FileOnly 仅文件过滤时显示对应提示
func TestFormatReviewBody_FilterHint_FileOnly(t *testing.T) {
	output := &ReviewOutput{
		Summary: "代码评审完成。",
		Verdict: VerdictComment,
	}

	body := formatReviewBody(FormatOptions{
		Review:         output,
		ParseFailed:    false,
		DurationSec:    5,
		CostUSD:        0.001,
		FilteredByFile: 2,
	})

	if !strings.Contains(body, "2 个被忽略文件的问题") {
		t.Errorf("body 应包含文件过滤提示，实际: %s", body)
	}
	if !strings.Contains(body, "已按配置过滤") {
		t.Errorf("body 应包含'已按配置过滤'，实际: %s", body)
	}
	// 不应有 severity 过滤提示
	if strings.Contains(body, "低于阈值") {
		t.Errorf("无 severity 过滤时不应包含阈值提示，实际: %s", body)
	}
}

// TestFormatReviewBody_FilterHint_Both 同时有 severity 和文件过滤时都显示
func TestFormatReviewBody_FilterHint_Both(t *testing.T) {
	output := &ReviewOutput{
		Summary: "代码评审完成。",
		Verdict: VerdictComment,
	}

	body := formatReviewBody(FormatOptions{
		Review:             output,
		ParseFailed:        false,
		DurationSec:        5,
		CostUSD:            0.001,
		FilteredBySeverity: 2,
		FilteredByFile:     1,
		SeverityThreshold:  "error",
	})

	if !strings.Contains(body, "2 个低于阈值(error)的问题") {
		t.Errorf("body 应包含 severity 过滤提示，实际: %s", body)
	}
	if !strings.Contains(body, "1 个被忽略文件的问题") {
		t.Errorf("body 应包含文件过滤提示，实际: %s", body)
	}
	if !strings.Contains(body, "已按配置过滤") {
		t.Errorf("body 应包含'已按配置过滤'，实际: %s", body)
	}
}

// TestFormatReviewBody_FilterHint_None 零过滤时不显示提示
func TestFormatReviewBody_FilterHint_None(t *testing.T) {
	output := &ReviewOutput{
		Summary: "代码评审完成。",
		Verdict: VerdictApprove,
	}

	body := formatReviewBody(FormatOptions{
		Review:      output,
		ParseFailed: false,
		DurationSec: 5,
		CostUSD:     0.001,
		// FilteredBySeverity=0, FilteredByFile=0
	})

	if strings.Contains(body, "已按配置过滤") {
		t.Errorf("零过滤时不应包含过滤提示，实际: %s", body)
	}
}

// TestFormatReviewBody_VisibleIssues_UsedForStats VisibleIssues 非 nil 时统计表格基于它
func TestFormatReviewBody_VisibleIssues_UsedForStats(t *testing.T) {
	output := &ReviewOutput{
		Summary: "发现问题。",
		Verdict: VerdictRequestChanges,
		Issues: []ReviewIssue{
			{Severity: "ERROR", Message: "错误"},
			{Severity: "INFO", Message: "信息"}, // 被过滤掉
		},
	}
	// VisibleIssues 只包含 ERROR（INFO 已被过滤）
	visible := []ReviewIssue{{Severity: "ERROR", Message: "错误"}}

	body := formatReviewBody(FormatOptions{
		Review:             output,
		ParseFailed:        false,
		DurationSec:        3,
		CostUSD:            0.001,
		VisibleIssues:      visible,
		FilteredBySeverity: 1,
		SeverityThreshold:  "error",
	})

	// 统计表格应只显示 ERROR:1
	if !strings.Contains(body, "| ERROR | 1 |") {
		t.Errorf("统计应基于 visible issues（ERROR=1），实际: %s", body)
	}
	// 不应显示 INFO（已被过滤）
	if strings.Contains(body, "| INFO |") {
		t.Errorf("INFO 被过滤，统计表格不应显示，实际: %s", body)
	}
}

// TestFormatReviewBody_FilterHint_EmptySeverityThresholdDefaultsToInfo 空 severity 阈值时提示显示 info
func TestFormatReviewBody_FilterHint_EmptySeverityThresholdDefaultsToInfo(t *testing.T) {
	output := &ReviewOutput{
		Summary: "评审完成。",
		Verdict: VerdictComment,
	}

	body := formatReviewBody(FormatOptions{
		Review:             output,
		ParseFailed:        false,
		DurationSec:        5,
		CostUSD:            0.001,
		FilteredBySeverity: 1,
		SeverityThreshold:  "", // 空时应默认显示 info
	})

	if !strings.Contains(body, "低于阈值(info)") {
		t.Errorf("空 severity 阈值时应默认显示 info，实际: %s", body)
	}
}

// min 辅助函数（Go 1.21 之前无内置 min for int）
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
