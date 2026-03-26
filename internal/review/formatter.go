package review

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// bodyMaxLen 评审正文最大字符数（Gitea PR 评论长度限制）
	bodyMaxLen = 60000
	// commentMaxLen 行级评论正文最大字符数
	commentMaxLen = 8000
	// messageMaxLen unmapped issues 列表中单条 message 截断长度
	messageMaxLen = 200
)

// formatReviewBody 生成 PR 评审正文 Markdown。
// 正常场景输出结构化统计表格和未映射问题列表；
// 降级场景（parseFailed=true）将 Claude 原始输出包裹在代码块中附加。
// 注意：parseErr 的日志记录由调用方负责，formatter 不引入 logger 依赖。
func formatReviewBody(
	output *ReviewOutput,
	unmapped []ReviewIssue,
	parseFailed bool,
	rawOutput string,
	durationSec float64,
	costUSD float64,
) string {
	footer := fmt.Sprintf("_由 DTWorkflow 自动生成 | 耗时 %.0fs | 费用 $%.4f_", durationSec, costUSD)

	var sb strings.Builder

	if parseFailed {
		// 降级场景：附加原始输出（代码块内不解析 Markdown，防止注入）
		sb.WriteString("## DTWorkflow 自动评审\n\n")
		sb.WriteString("> 评审结果解析失败，以下为 Claude 原始输出。结构化行级评论不可用。\n\n")
		sb.WriteString("---\n\n")
		raw := rawOutput
		if len(raw) > bodyMaxLen-300 {
			raw = truncateString(raw, bodyMaxLen-300)
		}
		sb.WriteString("```\n")
		sb.WriteString(raw)
		sb.WriteString("\n```\n\n---\n")
		sb.WriteString(footer)
		result := sb.String()
		if len(result) > bodyMaxLen {
			result = truncateString(result, bodyMaxLen)
		}
		return result
	}

	// 正常场景
	sb.WriteString("## DTWorkflow 自动评审\n\n")
	if output != nil && output.Summary != "" {
		sb.WriteString(escapeMarkdown(output.Summary))
		sb.WriteString("\n\n")
	}

	// 统计表格：output.Issues 已经是完整 findings，unmapped 只是其子集，不能重复计数。
	var allIssues []ReviewIssue
	if output != nil {
		allIssues = append(allIssues, output.Issues...)
	}

	counts := countBySeverity(allIssues)
	severities := []string{"CRITICAL", "ERROR", "WARNING", "INFO"}
	hasCount := false
	for _, sev := range severities {
		if counts[sev] > 0 {
			hasCount = true
			break
		}
	}

	if hasCount {
		sb.WriteString("### 评审统计\n")
		sb.WriteString("| 级别 | 数量 |\n")
		sb.WriteString("|------|------|\n")
		for _, sev := range severities {
			if counts[sev] > 0 {
				sb.WriteString(fmt.Sprintf("| %s | %d |\n", sev, counts[sev]))
			}
		}
		sb.WriteString("\n")
	}

	// 未关联到 diff 行的问题列表
	if len(unmapped) > 0 {
		// 按 severity 降序排列
		sorted := make([]ReviewIssue, len(unmapped))
		copy(sorted, unmapped)
		sort.Slice(sorted, func(i, j int) bool {
			return severityOrder(sorted[i].Severity) < severityOrder(sorted[j].Severity)
		})

		sb.WriteString("### 其他发现（未关联到 diff 行）\n")
		for _, issue := range sorted {
			msg := truncateString(issue.Message, messageMaxLen)
			loc := fmt.Sprintf("%s:%d", issue.File, issue.Line)
			sb.WriteString(fmt.Sprintf("- **%s** `%s` (%s): %s\n",
				issue.Severity,
				escapeTableCell(loc),
				escapeTableCell(issue.Category),
				escapeMarkdown(msg),
			))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n")
	sb.WriteString(footer)

	result := sb.String()
	if len(result) > bodyMaxLen {
		truncMsg := "\n\n_（内容过长，已截断）_"
		result = truncateToUTF8(result, bodyMaxLen-len(truncMsg)) + truncMsg
	}
	return result
}

// formatCommentBody 生成行级评论正文 Markdown。
// 格式：**severity** | category\n\nmessage\n\n> 建议：suggestion
func formatCommentBody(issue ReviewIssue) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**%s** | %s\n\n", issue.Severity, issue.Category))
	sb.WriteString(escapeMarkdown(issue.Message))
	if issue.Suggestion != "" {
		sb.WriteString(fmt.Sprintf("\n\n> 建议：%s", escapeMarkdown(issue.Suggestion)))
	}
	result := sb.String()
	if len(result) > commentMaxLen {
		truncMsg := "\n\n_（内容过长，已截断）_"
		result = truncateToUTF8(result, commentMaxLen-len(truncMsg)) + truncMsg
	}
	return result
}

// severityOrder 返回 severity 的排序权重，数值越小优先级越高。
func severityOrder(sev string) int {
	switch strings.ToUpper(sev) {
	case "CRITICAL":
		return 0
	case "ERROR":
		return 1
	case "WARNING":
		return 2
	case "INFO":
		return 3
	default:
		return 4
	}
}

// escapeMarkdown 转义 Markdown 特殊字符，防止注入钓鱼链接等恶意内容。
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

// escapeTableCell 转义 Markdown 表格单元格中的 `|` 和换行字符，同时转义 Markdown 链接语法。
func escapeTableCell(s string) string {
	s = escapeMarkdown(s)
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// truncateString 按字节截断字符串，回退到最近的完整 UTF-8 字符边界，避免截断多字节字符。
// 截断时追加 "…" 后缀。
func truncateString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return truncateToUTF8(s, maxBytes) + "…"
}

// truncateToUTF8 按字节截断字符串到最近的完整 UTF-8 字符边界，不追加后缀。
func truncateToUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// countBySeverity 统计各 severity 级别的 issue 数量。
func countBySeverity(issues []ReviewIssue) map[string]int {
	counts := make(map[string]int)
	for _, issue := range issues {
		counts[strings.ToUpper(issue.Severity)]++
	}
	return counts
}
