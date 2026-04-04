package fix

import (
	"strings"
	"unicode/utf8"
)

const (
	// bodyMaxLen Gitea 评论长度上限
	bodyMaxLen = 60000
)

// escapeMarkdown 转义 Markdown 特殊字符，防止注入钓鱼链接。
func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		`[`, `\[`,
		`]`, `\]`,
		`(`, `\(`,
		`)`, `\)`,
		`!`, `\!`,
		`<`, `\<`,
		`>`, `\>`,
	)
	return replacer.Replace(s)
}

// truncateString 按字节截断字符串，回退到最近的完整 UTF-8 字符边界。
// 截断时追加 "…" 后缀。
func truncateString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes] + "…"
}
