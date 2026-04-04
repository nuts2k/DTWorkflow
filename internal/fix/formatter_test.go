package fix

import (
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
