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

// stripControlChars 移除 ASCII 控制字符（\x00–\x08, \x0B, \x0C, \x0E–\x1F, \x7F），
// 保留 \t (\x09) 和 \n (\x0A) 以维持格式。
//
// 背景：Gitea 1.21 及部分版本在 PR/Issue body 含 NUL 等控制字符时，
// 写库阶段（MySQL utf8mb4 拒 NUL；Postgres TEXT 拒 \x00）会抛出
// 未 JSON 化的内部错误 → HTTP 500 Internal Server Error（message 为空）。
// Claude 输出偶发夹带控制字符，此处统一清洗做最终防线。
func stripControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t', r == '\n':
			return r
		case r < 0x20:
			return -1
		case r == 0x7F:
			return -1
		default:
			return r
		}
	}, s)
}

// stripNonBMPChars 移除 U+10000 以上的非 BMP 字符。
//
// 背景：目标 Gitea 实例底层 MySQL 使用 utf8（非 utf8mb4），
// 写入 4 字节字符会在服务端触发 500。这里做最后一层兜底清洗，
// 避免 Issue 标题或 Claude 输出中的非 BMP 字符直接进入 Gitea 请求体。
func stripNonBMPChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r > 0xFFFF {
			return -1
		}
		return r
	}, s)
}

func sanitizeGiteaText(s string) string {
	return stripNonBMPChars(stripControlChars(s))
}

// containsControlChars 判断字符串是否包含需要清洗的控制字符，仅用于诊断日志。
func containsControlChars(s string) bool {
	for _, r := range s {
		if r == '\t' || r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7F {
			return true
		}
	}
	return false
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
	body = sanitizeGiteaText(body)
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
	body = sanitizeGiteaText(body)
	if len(body) > bodyMaxLen {
		body = truncateString(body, bodyMaxLen)
	}
	return body
}

// ==================== M3.5: fix_issue 修复模式格式化 ====================

// FormatFixPRBody M3.5: 构造修复 PR 的 body。
// refKind=RefKindTag 时，baseBranch 为仓库默认分支，body 中注明 base 差异。
func FormatFixPRBody(fix *FixOutput, issueNum int64, refKind RefKind, baseBranch string) string {
	var sb strings.Builder
	sb.WriteString("## 关联 Issue\n\n")
	sb.WriteString(fmt.Sprintf("fixes #%d\n\n", issueNum))

	if refKind == RefKindTag && baseBranch != "" {
		sb.WriteString("> ⚠️ 原 Issue 指定的 ref 为 tag，Gitea PR 不支持 tag 作为 base 分支。\n")
		sb.WriteString(fmt.Sprintf("> PR base 已改用仓库默认分支 `%s`，修复代码仍基于 tag 对应的 commit。\n\n", escapeMarkdown(baseBranch)))
	}

	if fix.Analysis != "" {
		sb.WriteString("## 根因分析\n\n")
		sb.WriteString(escapeMarkdown(fix.Analysis))
		sb.WriteString("\n\n")
	}

	if fix.FixApproach != "" {
		sb.WriteString("## 修复方案\n\n")
		sb.WriteString(escapeMarkdown(fix.FixApproach))
		sb.WriteString("\n\n")
	}

	if len(fix.ModifiedFiles) > 0 {
		sb.WriteString("## 修改文件\n\n")
		for _, f := range fix.ModifiedFiles {
			sb.WriteString(fmt.Sprintf("- `%s`\n", escapeMarkdown(f)))
		}
		sb.WriteString("\n")
	}

	if fix.TestResults != nil {
		sb.WriteString("## 测试结果\n\n")
		tr := fix.TestResults
		sb.WriteString(fmt.Sprintf("通过 **%d** / 失败 **%d** / 跳过 **%d**（全部通过：%v）\n\n",
			tr.Passed, tr.Failed, tr.Skipped, tr.AllPassed))
	}

	sb.WriteString("---\n")
	sb.WriteString("由 DTWorkflow 自动生成")

	body := sb.String()
	// 清洗控制字符：Gitea 1.21 对含 NUL 等字符的 body 会 500（未 JSON 化的内部错误）。
	// 也同步移除非 BMP 字符，规避目标 MySQL utf8 charset 对 4 字节字符的写库失败。
	// 清洗在截断之前做，避免清洗后长度变化触发二次截断判断偏差。
	body = sanitizeGiteaText(body)
	if len(body) > bodyMaxLen {
		body = truncateString(body, bodyMaxLen)
	}
	return body
}

// FormatFixSuccessComment M3.5: 修复成功后发送给 Issue 的评论。
func FormatFixSuccessComment(prNumber int64, prURL string, modifiedFileCount int) string {
	return sanitizeGiteaText(fmt.Sprintf(
		"✅ 已创建修复 PR [#%d](%s)\n\n"+
			"修改了 **%d 个文件**，测试全部通过。\n"+
			"PR 将自动进入评审流程。\n\n"+
			"---\n_由 DTWorkflow 自动生成_",
		prNumber, prURL, modifiedFileCount))
}

// FormatFixFailureComment M3.5: 修复失败（测试未通过、分支未创建等）。
func FormatFixFailureComment(fix *FixOutput, durationSec, costUSD float64) string {
	var sb strings.Builder
	sb.WriteString("## DTWorkflow Issue 修复失败\n\n")

	if fix.FailureReason != "" {
		sb.WriteString("### 失败原因\n\n")
		sb.WriteString(escapeMarkdown(fix.FailureReason))
		sb.WriteString("\n\n")
	}

	if fix.Analysis != "" {
		sb.WriteString("### 尝试的分析\n\n")
		sb.WriteString(escapeMarkdown(fix.Analysis))
		sb.WriteString("\n\n")
	}

	if fix.TestResults != nil {
		tr := fix.TestResults
		sb.WriteString("### 测试结果\n\n")
		sb.WriteString(fmt.Sprintf("通过 %d / 失败 %d / 跳过 %d\n\n",
			tr.Passed, tr.Failed, tr.Skipped))
	}

	sb.WriteString("---\n")
	sb.WriteString("**建议**：人工检查失败原因，补充信息或修正代码后重新触发修复。\n\n")
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("_由 DTWorkflow 自动生成 | 耗时 %.0fs | 费用 $%.4f_", durationSec, costUSD))

	body := sb.String()
	body = sanitizeGiteaText(body)
	if len(body) > bodyMaxLen {
		body = truncateString(body, bodyMaxLen)
	}
	return body
}

// FormatFixInfoInsufficientComment M3.5: 前序分析信息不足时的提醒。
func FormatFixInfoInsufficientComment(missingInfo []string) string {
	var sb strings.Builder
	sb.WriteString("## DTWorkflow Issue 修复前置检查\n\n")
	sb.WriteString("> 之前的 `auto-fix` 分析表明 Issue 信息不足以自动修复。\n\n")

	if len(missingInfo) > 0 {
		sb.WriteString("### 缺失信息\n\n")
		for _, info := range missingInfo {
			sb.WriteString(fmt.Sprintf("- %s\n", escapeMarkdown(info)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n\n")
	sb.WriteString("**补充信息后**，请移除 `auto-fix` 标签再重新添加以更新分析；\n")
	sb.WriteString("若新的分析确认信息充分，再添加 `fix-to-pr` 标签触发自动修复。\n\n")
	sb.WriteString("---\n")
	sb.WriteString("_由 DTWorkflow 自动生成_")

	body := sb.String()
	body = sanitizeGiteaText(body)
	if len(body) > bodyMaxLen {
		body = truncateString(body, bodyMaxLen)
	}
	return body
}

// FormatFixPushButNoPRComment M3.5: 容器 push 成功但容器外 PR 创建失败。
func FormatFixPushButNoPRComment(branchName, apiError string) string {
	return sanitizeGiteaText(fmt.Sprintf(
		"⚠️ 修复分支 `%s` 已推送成功，但 PR 创建失败。\n\n"+
			"错误：%s\n\n"+
			"系统将自动重试。如持续失败，请人工从 `%s` 分支手动创建 PR。\n\n"+
			"---\n_由 DTWorkflow 自动生成_",
		escapeMarkdown(branchName),
		escapeMarkdown(apiError),
		escapeMarkdown(branchName)))
}

// FormatFixDegradedComment M3.5: 修复结果解析失败后的降级评论。
func FormatFixDegradedComment(result *FixResult) string {
	var durationSec, costUSD float64
	if result != nil && result.CLIMeta != nil {
		durationSec = float64(result.CLIMeta.DurationMs) / 1000.0
		costUSD = result.CLIMeta.CostUSD
	}
	raw := ""
	if result != nil {
		raw = result.RawOutput
	}
	return formatRawOutputFallback(
		"## DTWorkflow Issue 自动修复降级报告\n\n",
		"> 修复结果解析失败，以下为 Claude 原始输出。\n\n",
		raw,
		durationSec,
		costUSD,
	)
}

func formatFallback(rawOutput string, durationSec, costUSD float64) string {
	return formatRawOutputFallback(
		"## DTWorkflow Issue 分析报告\n\n",
		"> 分析结果解析失败，以下为 Claude 原始输出。\n\n",
		rawOutput,
		durationSec,
		costUSD,
	)
}

func formatRawOutputFallback(title, lead, rawOutput string, durationSec, costUSD float64) string {
	var sb strings.Builder
	sb.WriteString(title)
	sb.WriteString(lead)
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
	body = sanitizeGiteaText(body)
	if len(body) > bodyMaxLen {
		body = truncateString(body, bodyMaxLen)
	}
	return body
}
