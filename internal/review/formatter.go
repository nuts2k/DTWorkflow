package review

import (
	"fmt"
	"sort"
	"strings"
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
// 降级场景（parseFailed=true）直接附加 Claude 原始输出。
func formatReviewBody(
	output *ReviewOutput,
	unmapped []ReviewIssue,
	parseFailed bool,
	parseErr error,
	rawOutput string,
	durationSec float64,
	costUSD float64,
) string {
	footer := fmt.Sprintf("_由 DTWorkflow 自动生成 | 耗时 %.0fs | 费用 $%.4f_", durationSec, costUSD)

	var sb strings.Builder

	if parseFailed {
		// 降级场景：附加原始输出
		sb.WriteString("## DTWorkflow 自动评审\n\n")
		if parseErr != nil {
			sb.WriteString(fmt.Sprintf("> 评审结果解析失败，以下为 Claude 原始输出。结构化行级评论不可用。\n> 错误详情：%v\n\n", parseErr))
		} else {
			sb.WriteString("> 评审结果解析失败，以下为 Claude 原始输出。结构化行级评论不可用。\n\n")
		}
		sb.WriteString("---\n\n")
		raw := rawOutput
		if len(raw) > bodyMaxLen-200 {
			raw = raw[:bodyMaxLen-200]
		}
		sb.WriteString(raw)
		sb.WriteString("\n\n---\n")
		sb.WriteString(footer)
		result := sb.String()
		if len(result) > bodyMaxLen {
			result = result[:bodyMaxLen]
		}
		return result
	}

	// 正常场景
	sb.WriteString("## DTWorkflow 自动评审\n\n")
	if output != nil && output.Summary != "" {
		sb.WriteString(output.Summary)
		sb.WriteString("\n\n")
	}

	// 统计表格：收集所有 issues（含 unmapped）
	var allIssues []ReviewIssue
	if output != nil {
		allIssues = append(allIssues, output.Issues...)
	}
	allIssues = append(allIssues, unmapped...)

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
			msg := issue.Message
			if len(msg) > messageMaxLen {
				msg = msg[:messageMaxLen]
			}
			loc := fmt.Sprintf("%s:%d", issue.File, issue.Line)
			sb.WriteString(fmt.Sprintf("- **%s** `%s` (%s): %s\n",
				issue.Severity,
				escapeTableCell(loc),
				escapeTableCell(issue.Category),
				msg,
			))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n")
	sb.WriteString(footer)

	result := sb.String()
	if len(result) > bodyMaxLen {
		truncMsg := "\n\n_（内容过长，已截断）_"
		result = result[:bodyMaxLen-len(truncMsg)] + truncMsg
	}
	return result
}

// formatCommentBody 生成行级评论正文 Markdown。
// 格式：**severity** | category\n\nmessage\n\n> 建议：suggestion
func formatCommentBody(issue ReviewIssue) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**%s** | %s\n\n", issue.Severity, issue.Category))
	sb.WriteString(issue.Message)
	if issue.Suggestion != "" {
		sb.WriteString(fmt.Sprintf("\n\n> 建议：%s", issue.Suggestion))
	}
	result := sb.String()
	if len(result) > commentMaxLen {
		truncMsg := "\n\n_（内容过长，已截断）_"
		result = result[:commentMaxLen-len(truncMsg)] + truncMsg
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

// escapeTableCell 转义 Markdown 表格单元格中的 `|` 字符。
func escapeTableCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// countBySeverity 统计各 severity 级别的 issue 数量。
func countBySeverity(issues []ReviewIssue) map[string]int {
	counts := make(map[string]int)
	for _, issue := range issues {
		counts[strings.ToUpper(issue.Severity)]++
	}
	return counts
}
