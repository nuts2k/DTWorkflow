package fix

import (
	"fmt"
	"strings"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

func TestEscapeMarkdown(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"[link](url)", `\[link\]\(url\)`},
		{"![img](x)", `\!\[img\]\(x\)`},
		{"<script>", `\<script\>`},
		{"`inline code`", "\\`inline code\\`"},
		{"# heading", `\# heading`},
		{"普通文本 abc", "普通文本 abc"},
	}
	for _, tc := range tests {
		got := escapeMarkdown(tc.input)
		if got != tc.want {
			t.Errorf("escapeMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestTruncateString_UTF8Safe(t *testing.T) {
	// 不需要截断
	if got := truncateString("hello", 10); got != "hello" {
		t.Errorf("不截断时应返回原字符串，got=%q", got)
	}
	// 中文截断
	result := truncateString("你好世界", 7)
	if !strings.HasPrefix(result, "你好") {
		t.Errorf("中文截断应保留完整字符，got=%q", result)
	}
	if !strings.HasSuffix(result, "…") {
		t.Errorf("截断后应以省略号结尾，got=%q", result)
	}
}

func TestTruncateString_BodyMaxLen(t *testing.T) {
	long := strings.Repeat("X", 70000)
	result := truncateString(long, bodyMaxLen)
	if len(result) > bodyMaxLen+len("…") {
		t.Errorf("截断后长度不应超过 bodyMaxLen，got=%d", len(result))
	}
}

func TestTruncateString_ZeroOrNegativeMaxBytes(t *testing.T) {
	if got := truncateString("hello", 0); got != "…" {
		t.Errorf("maxBytes=0 应返回省略号，got=%q", got)
	}
	if got := truncateString("hello", -1); got != "…" {
		t.Errorf("maxBytes=-1 应返回省略号，got=%q", got)
	}
}

func TestFormatAnalysisComment_Normal(t *testing.T) {
	result := &FixResult{
		Analysis: &AnalysisOutput{
			InfoSufficient: true,
			Confidence:     "high",
			RootCause: &RootCause{
				File:        "src/main/java/com/example/UserService.java",
				Function:    "getUserById",
				StartLine:   42,
				EndLine:     55,
				Description: "空指针未检查",
			},
			Analysis:      "详细分析内容",
			FixSuggestion: "添加空值检查",
			RelatedFiles:  []string{"util.go", "service.go"},
		},
		CLIMeta: &model.CLIMeta{
			DurationMs: 45000,
			CostUSD:    0.032,
		},
	}

	body := FormatAnalysisComment(result)

	checks := []struct {
		name string
		want string
	}{
		{"标题", "## DTWorkflow Issue 分析报告"},
		{"置信度", "**high**"},
		{"根因标题", "### 根因定位"},
		{"文件", "UserService.java"},
		{"方法", "getUserById"},
		{"行号", "42-55"},
		{"原因", "空指针未检查"},
		{"分析标题", "### 详细分析"},
		{"分析内容", "详细分析内容"},
		{"建议标题", "### 修复建议"},
		{"建议内容", "添加空值检查"},
		{"相关文件标题", "### 相关文件"},
		{"相关文件1", "util.go"},
		{"相关文件2", "service.go"},
		{"耗时", "耗时 45s"},
		{"费用", "$0.0320"},
		{"签名", "DTWorkflow 自动生成"},
	}
	for _, c := range checks {
		if !strings.Contains(body, c.want) {
			t.Errorf("[%s] body 应包含 %q", c.name, c.want)
		}
	}
}

func TestFormatAnalysisComment_InsufficientInfo(t *testing.T) {
	result := &FixResult{
		Analysis: &AnalysisOutput{
			InfoSufficient: false,
			MissingInfo:    []string{"缺少错误堆栈信息", "缺少复现步骤"},
			Analysis:       "初步判断可能是配置问题",
			Confidence:     "low",
		},
		CLIMeta: &model.CLIMeta{
			DurationMs: 12000,
			CostUSD:    0.008,
		},
	}

	body := FormatAnalysisComment(result)

	checks := []struct {
		name string
		want string
	}{
		{"标题", "## DTWorkflow Issue 分析报告"},
		{"信息不足提示", "信息不足"},
		{"缺失标题", "### 缺失信息"},
		{"缺失1", "缺少错误堆栈信息"},
		{"缺失2", "缺少复现步骤"},
		{"初步判断标题", "### 初步判断"},
		{"初步判断内容", "初步判断可能是配置问题"},
		{"重新触发提示", "补充信息后"},
		{"耗时", "耗时 12s"},
		{"费用", "$0.0080"},
	}
	for _, c := range checks {
		if !strings.Contains(body, c.want) {
			t.Errorf("[%s] body 应包含 %q", c.name, c.want)
		}
	}
}

func TestFormatAnalysisComment_Fallback_ParseError(t *testing.T) {
	result := &FixResult{
		RawOutput:  "这是 Claude 原始输出文本",
		ParseError: fmt.Errorf("JSON 解析失败"),
		CLIMeta: &model.CLIMeta{
			DurationMs: 30000,
			CostUSD:    0.025,
		},
	}

	body := FormatAnalysisComment(result)

	checks := []struct {
		name string
		want string
	}{
		{"标题", "## DTWorkflow Issue 分析报告"},
		{"降级提示", "分析结果解析失败"},
		{"原始输出", "这是 Claude 原始输出文本"},
		{"代码块", "```"},
		{"耗时", "耗时 30s"},
		{"费用", "$0.0250"},
	}
	for _, c := range checks {
		if !strings.Contains(body, c.want) {
			t.Errorf("[%s] body 应包含 %q", c.name, c.want)
		}
	}
	// 不应暴露内部错误详情
	if strings.Contains(body, "JSON 解析失败") {
		t.Error("不应暴露内部 ParseError 详情到评论")
	}
}

func TestFormatAnalysisComment_Fallback_NilAnalysis(t *testing.T) {
	result := &FixResult{
		RawOutput: "raw output",
		CLIMeta:   &model.CLIMeta{DurationMs: 5000, CostUSD: 0.01},
	}

	body := FormatAnalysisComment(result)

	if !strings.Contains(body, "分析结果解析失败") {
		t.Error("Analysis 为 nil 时应走降级场景")
	}
}

func TestFormatAnalysisComment_Fallback_LongOutput(t *testing.T) {
	result := &FixResult{
		RawOutput:  strings.Repeat("X", 70000),
		ParseError: fmt.Errorf("parse error"),
	}

	body := FormatAnalysisComment(result)

	if len(body) > bodyMaxLen {
		t.Errorf("降级评论长度不应超过 %d，实际=%d", bodyMaxLen, len(body))
	}
}

func TestFormatAnalysisComment_Fallback_BacktickEscape(t *testing.T) {
	rawWithBackticks := "some output\n```\ncode block\n```\nmore output"
	result := &FixResult{
		RawOutput:  rawWithBackticks,
		ParseError: fmt.Errorf("parse error"),
		CLIMeta:    &model.CLIMeta{DurationMs: 5000, CostUSD: 0.01},
	}

	body := FormatAnalysisComment(result)

	// 应使用更长的 fence 包裹，避免代码块提前关闭
	if !strings.Contains(body, "````\n") {
		t.Error("原始输出包含 ``` 时应使用更长的 fence")
	}
	// 原始内容应完整保留
	if !strings.Contains(body, rawWithBackticks) {
		t.Error("原始输出内容应完整保留")
	}
}

func TestFormatFixPRBody_Success(t *testing.T) {
	fix := &FixOutput{
		Success:       true,
		BranchName:    "auto-fix/issue-15",
		CommitSHA:     "abc123",
		ModifiedFiles: []string{"src/a.java", "test/a_test.java"},
		TestResults:   &TestResults{Passed: 12, Failed: 0, Skipped: 1, AllPassed: true},
		Analysis:      "NPE 由未校验空密码引起",
		FixApproach:   "在 login 前添加 isEmpty 判断",
	}
	body := FormatFixPRBody(fix, 15, RefKindBranch, "")

	checks := []string{"fixes #15", "NPE 由未校验空密码引起", "src/a.java", "12", "DTWorkflow"}
	for _, s := range checks {
		if !strings.Contains(body, s) {
			t.Errorf("PR body 应包含 %q", s)
		}
	}
}

func TestFormatFixPRBody_TagRef_NotesBaseBranchFallback(t *testing.T) {
	fix := &FixOutput{Success: true, BranchName: "auto-fix/issue-15", CommitSHA: "abc",
		ModifiedFiles: []string{"x.go"}, TestResults: &TestResults{AllPassed: true}}
	body := FormatFixPRBody(fix, 15, RefKindTag, "main")
	if !strings.Contains(body, "tag") || !strings.Contains(body, "main") {
		t.Errorf("Tag 场景 PR body 应注明 base 为默认分支 main, got:\n%s", body)
	}
}

func TestFormatFixSuccessComment(t *testing.T) {
	body := FormatFixSuccessComment(42, "https://gitea/owner/repo/pulls/42", 3)
	checks := []string{"#42", "https://gitea/owner/repo/pulls/42", "3 个文件"}
	for _, s := range checks {
		if !strings.Contains(body, s) {
			t.Errorf("成功评论应包含 %q", s)
		}
	}
}

func TestFormatFixFailureComment(t *testing.T) {
	fix := &FixOutput{
		Success:       false,
		FailureReason: "3 个测试未通过",
		TestResults:   &TestResults{Passed: 5, Failed: 3, AllPassed: false},
		Analysis:      "根因是边界条件",
	}
	body := FormatFixFailureComment(fix, 2.5, 0.1)
	if !strings.Contains(body, "3 个测试未通过") {
		t.Error("失败评论应包含 failure_reason")
	}
	if !strings.Contains(body, "根因是边界条件") {
		t.Error("失败评论应包含分析说明帮助用户定位")
	}
}

func TestFormatFixInfoInsufficientComment(t *testing.T) {
	body := FormatFixInfoInsufficientComment([]string{"缺少堆栈", "缺少复现步骤"})
	checks := []string{"信息不足", "缺少堆栈", "缺少复现步骤", "auto-fix", "fix-to-pr"}
	for _, s := range checks {
		if !strings.Contains(body, s) {
			t.Errorf("评论应包含 %q", s)
		}
	}
}

func TestFormatFixPushButNoPRComment(t *testing.T) {
	body := FormatFixPushButNoPRComment("auto-fix/issue-15", "gitea API 返回 500")
	if !strings.Contains(body, "auto-fix/issue-15") || !strings.Contains(body, "PR 创建失败") {
		t.Errorf("push 成功但 PR 失败的评论应包含分支名和说明: %s", body)
	}
}

func TestFormatFixDegradedComment(t *testing.T) {
	body := FormatFixDegradedComment(&FixResult{
		RawOutput:  "raw fix output",
		ParseError: fmt.Errorf("bad json"),
		CLIMeta:    &model.CLIMeta{DurationMs: 6000, CostUSD: 0.02},
	})
	checks := []string{"自动修复降级报告", "修复结果解析失败", "raw fix output", "耗时 6s", "$0.0200"}
	for _, s := range checks {
		if !strings.Contains(body, s) {
			t.Errorf("降级评论应包含 %q", s)
		}
	}
	if strings.Contains(body, "bad json") {
		t.Error("不应暴露内部 ParseError 详情到评论")
	}
}

func TestFormatAnalysisComment_NilCLIMeta(t *testing.T) {
	result := &FixResult{
		Analysis: &AnalysisOutput{
			InfoSufficient: true,
			Analysis:       "分析内容",
			Confidence:     "medium",
		},
	}

	body := FormatAnalysisComment(result)

	if !strings.Contains(body, "耗时 0s") {
		t.Errorf("CLIMeta 为 nil 时耗时应为 0s，body=%s", body)
	}
}

// TestStripControlChars 验证控制字符清洗：过滤 NUL/SOH/ESC 等，保留 \t \n 及多字节字符。
func TestStripControlChars(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii", "hello world", "hello world"},
		{"keep tab", "a\tb", "a\tb"},
		{"keep newline", "a\nb", "a\nb"},
		{"strip NUL", "a\x00b", "ab"},
		{"strip SOH", "a\x01b", "ab"},
		{"strip ESC", "a\x1bb", "ab"},
		{"strip DEL", "a\x7fb", "ab"},
		{"keep chinese", "中文\tabc", "中文\tabc"},
		{"mixed", "a\x00中\x01文\x7fb\n", "a中文b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripControlChars(tc.in)
			if got != tc.want {
				t.Errorf("stripControlChars(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestContainsControlChars 验证诊断用的控制字符检测。
func TestContainsControlChars(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"normal text", false},
		{"中文 abc", false},
		{"tab\tand\nnewline", false},
		{"has NUL \x00 char", true},
		{"has DEL \x7f char", true},
		{"has ESC \x1b char", true},
	}
	for _, tc := range cases {
		got := containsControlChars(tc.in)
		if got != tc.want {
			t.Errorf("containsControlChars(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestFormatFixPRBody_StripsControlChars 验证 PR body 在格式化阶段
// 会被清洗掉控制字符，不会把 Claude 偶发的 NUL 等字符带到 Gitea 请求中。
func TestFormatFixPRBody_StripsControlChars(t *testing.T) {
	fix := &FixOutput{
		Success:     true,
		BranchName:  "auto-fix/issue-1",
		CommitSHA:   "abc",
		Analysis:    "分析包含\x00NUL\x01和\x1bESC",
		FixApproach: "方案含\x7fDEL",
	}
	body := FormatFixPRBody(fix, 1, RefKindBranch, "")
	for _, bad := range []string{"\x00", "\x01", "\x1b", "\x7f"} {
		if strings.Contains(body, bad) {
			t.Errorf("body 应剔除控制字符 %q，实际仍含: %q", bad, body)
		}
	}
	// 保留正常文本
	if !strings.Contains(body, "分析包含") || !strings.Contains(body, "方案含") {
		t.Errorf("正常文本应保留: %s", body)
	}
}

func TestFormatFixPRBody_StripsNonBMPChars(t *testing.T) {
	fix := &FixOutput{
		Success:     true,
		BranchName:  "auto-fix/issue-1",
		CommitSHA:   "abc",
		Analysis:    "分析包含🤖机器人提示",
		FixApproach: "方案包含🚀发布符号",
	}
	body := FormatFixPRBody(fix, 1, RefKindBranch, "")

	for _, bad := range []string{"🤖", "🚀"} {
		if strings.Contains(body, bad) {
			t.Errorf("body 应剔除非 BMP 字符 %q，实际仍含: %q", bad, body)
		}
	}
	if !strings.Contains(body, "分析包含机器人提示") || !strings.Contains(body, "方案包含发布符号") {
		t.Errorf("正常文本应保留: %s", body)
	}
}
