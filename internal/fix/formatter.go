package fix

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// bodyMaxLen Gitea 评论长度上限
	bodyMaxLen = 60000
)

// mdReplacer 转义 Markdown 特殊字符，防止注入钓鱼链接和格式干扰。
var mdReplacer = strings.NewReplacer(
	`[`, `\[`,
	`]`, `\]`,
	`(`, `\(`,
	`)`, `\)`,
	`!`, `\!`,
	`<`, `\<`,
	`>`, `\>`,
	"`", "\\`",
	`#`, `\#`,
)

func escapeMarkdown(s string) string {
	return mdReplacer.Replace(s)
}

// truncateString 按字节截断字符串，回退到最近的完整 UTF-8 字符边界。
// 截断时追加 "…" 后缀。
func truncateString(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return "…"
	}
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes] + "…"
}

// FormatAnalysisComment 根据分析结果生成 Issue 评论 Markdown。
// 三场景分支：正常 / 信息不足 / 降级。
func FormatAnalysisComment(result *FixResult) string {
	var durationSec, costUSD float64
	if result.CLIMeta != nil {
		durationSec = float64(result.CLIMeta.DurationMs) / 1000.0
		costUSD = result.CLIMeta.CostUSD
	}

	// 降级场景
	if result.ParseError != nil || result.Analysis == nil {
		return formatFallback(result.RawOutput, durationSec, costUSD)
	}

	// 信息不足
	if !result.Analysis.InfoSufficient {
		return formatInsufficientInfo(result.Analysis, durationSec, costUSD)
	}

	// 正常
	return formatNormalReport(result.Analysis, durationSec, costUSD)
}

func formatNormalReport(analysis *AnalysisOutput, durationSec, costUSD float64) string {
	var sb strings.Builder
	sb.WriteString("## DTWorkflow Issue 分析报告\n\n")
	sb.WriteString(fmt.Sprintf("> 置信度：**%s** | 分析基于 Issue 描述和评论\n\n", escapeMarkdown(analysis.Confidence)))

	// 根因定位
	if analysis.RootCause != nil {
		rc := analysis.RootCause
		sb.WriteString("### 根因定位\n\n")
		sb.WriteString("| 项目 | 详情 |\n")
		sb.WriteString("|------|------|\n")
		sb.WriteString(fmt.Sprintf("| 文件 | `%s` |\n", escapeMarkdown(rc.File)))
		if rc.Function != "" {
			sb.WriteString(fmt.Sprintf("| 方法 | `%s` |\n", escapeMarkdown(rc.Function)))
		}
		if rc.StartLine > 0 {
			if rc.EndLine > rc.StartLine {
				sb.WriteString(fmt.Sprintf("| 行号 | %d-%d |\n", rc.StartLine, rc.EndLine))
			} else {
				sb.WriteString(fmt.Sprintf("| 行号 | %d |\n", rc.StartLine))
			}
		}
		sb.WriteString(fmt.Sprintf("| 原因 | %s |\n\n", escapeMarkdown(rc.Description)))
	}

	// 详细分析
	if analysis.Analysis != "" {
		sb.WriteString("### 详细分析\n\n")
		sb.WriteString(escapeMarkdown(analysis.Analysis))
		sb.WriteString("\n\n")
	}

	// 修复建议
	if analysis.FixSuggestion != "" {
		sb.WriteString("### 修复建议\n\n")
		sb.WriteString(escapeMarkdown(analysis.FixSuggestion))
		sb.WriteString("\n\n")
	}

	// 相关文件
	if len(analysis.RelatedFiles) > 0 {
		sb.WriteString("### 相关文件\n\n")
		for _, f := range analysis.RelatedFiles {
			sb.WriteString(fmt.Sprintf("- `%s`\n", escapeMarkdown(f)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("_由 DTWorkflow 自动生成 | 耗时 %.0fs | 费用 $%.4f_", durationSec, costUSD))

	body := sb.String()
	if len(body) > bodyMaxLen {
		truncMsg := "\n\n_（内容过长，已截断）_"
		body = truncateString(body, bodyMaxLen-len(truncMsg)) + truncMsg
	}
	return body
}

func formatInsufficientInfo(analysis *AnalysisOutput, durationSec, costUSD float64) string {
	var sb strings.Builder
	sb.WriteString("## DTWorkflow Issue 分析报告\n\n")
	sb.WriteString("> Issue 提供的信息不足以进行根因定位，需要补充以下信息：\n\n")

	if len(analysis.MissingInfo) > 0 {
		sb.WriteString("### 缺失信息\n\n")
		for _, info := range analysis.MissingInfo {
			sb.WriteString(fmt.Sprintf("- %s\n", escapeMarkdown(info)))
		}
		sb.WriteString("\n")
	}

	if analysis.Analysis != "" {
		sb.WriteString("### 初步判断\n\n")
		sb.WriteString(escapeMarkdown(analysis.Analysis))
		sb.WriteString("\n\n")
	}

	sb.WriteString("---\n\n")
	sb.WriteString("**补充信息后**，请移除 `auto-fix` 标签再重新添加以触发分析。\n\n")
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("_由 DTWorkflow 自动生成 | 耗时 %.0fs | 费用 $%.4f_", durationSec, costUSD))

	body := sb.String()
	if len(body) > bodyMaxLen {
		body = truncateString(body, bodyMaxLen)
	}
	return body
}

func formatFallback(rawOutput string, durationSec, costUSD float64) string {
	var sb strings.Builder
	sb.WriteString("## DTWorkflow Issue 分析报告\n\n")
	sb.WriteString("> 分析结果解析失败，以下为 Claude 原始输出。\n\n")
	sb.WriteString("---\n\n")

	raw := rawOutput
	if len(raw) > bodyMaxLen-300 {
		raw = truncateString(raw, bodyMaxLen-300)
	}
	fence := "```"
	for strings.Contains(raw, fence) {
		fence += "`"
	}
	sb.WriteString(fence + "\n")
	sb.WriteString(raw)
	sb.WriteString("\n" + fence + "\n\n")

	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("_由 DTWorkflow 自动生成 | 耗时 %.0fs | 费用 $%.4f_", durationSec, costUSD))

	body := sb.String()
	if len(body) > bodyMaxLen {
		body = truncateString(body, bodyMaxLen)
	}
	return body
}
