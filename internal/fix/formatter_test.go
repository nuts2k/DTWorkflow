package fix

import (
	"strings"
	"testing"
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
