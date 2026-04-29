package test

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// prBodyMaxLen Gitea PR 描述长度上限（与 fix.bodyMaxLen 对齐）。
const prBodyMaxLen = 60000

// prBodyMaxGapAnalysisItems GapAnalysis 摘要最多展示条数（防爆长）。
const prBodyMaxGapAnalysisItems = 5

// mdReplacer 转义 Markdown 特殊字符，防止注入钓鱼链接和格式干扰。
// 与 fix.mdReplacer 对齐。
var prMDReplacer = strings.NewReplacer(
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

func escapePRMarkdown(s string) string {
	return prMDReplacer.Replace(s)
}

// truncatePRString 按字节截断字符串，回退到最近的完整 UTF-8 字符边界。
// 截断时追加 "…" 后缀。
func truncatePRString(s string, maxBytes int) string {
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

// FormatTestGenPRBody 根据 TestGenOutput 渲染 gen_tests PR 描述 markdown。
//
// 分节：
//  1. 概览（模块 / 框架 / 基线 ref / 生成 / 提交 / 跳过计数）
//  2. 失败分类（Success=false 或 FailureCategory != none 时）
//  3. GapAnalysis 摘要（前 5 个 UntestedModules）
//  4. GeneratedFiles 列表
//  5. TestRunResults
//  6. SkippedTargets（若有）
//  7. Warnings（若有）
func FormatTestGenPRBody(out *TestGenOutput, payload model.TaskPayload, framework string) string {
	var sb strings.Builder
	sb.WriteString("## DTWorkflow gen_tests 测试补全\n\n")

	// 1. 概览（所有字段值都经过 escapePRMarkdown 且包裹在 ` 中）
	moduleLabel := payload.Module
	if strings.TrimSpace(moduleLabel) == "" {
		moduleLabel = "<整仓>"
	}
	sb.WriteString("### 概览\n\n")
	sb.WriteString("| 项目 | 值 |\n")
	sb.WriteString("|------|----|\n")
	sb.WriteString(fmt.Sprintf("| 仓库 | `%s` |\n", escapePRMarkdown(payload.RepoFullName)))
	sb.WriteString(fmt.Sprintf("| 模块 | `%s` |\n", escapePRMarkdown(moduleLabel)))
	sb.WriteString(fmt.Sprintf("| 测试框架 | `%s` |\n", escapePRMarkdown(framework)))

	generatedCount := 0
	committedCount := 0
	skippedCount := 0
	if out != nil {
		generatedCount = len(out.GeneratedFiles)
		committedCount = len(out.CommittedFiles)
		skippedCount = len(out.SkippedTargets)
		if out.BranchName != "" {
			sb.WriteString(fmt.Sprintf("| 分支 | `%s` |\n", escapePRMarkdown(out.BranchName)))
		}
		if out.CommitSHA != "" {
			sb.WriteString(fmt.Sprintf("| 最新 commit | `%s` |\n", escapePRMarkdown(out.CommitSHA)))
		}
	}
	sb.WriteString(fmt.Sprintf("| 基线 ref | `%s` |\n", escapePRMarkdown(payload.BaseRef)))
	sb.WriteString(fmt.Sprintf("| 生成文件 | **%d** |\n", generatedCount))
	sb.WriteString(fmt.Sprintf("| 已提交 | **%d** |\n", committedCount))
	sb.WriteString(fmt.Sprintf("| 跳过目标 | **%d** |\n", skippedCount))
	sb.WriteString("\n")

	// 部分交付提示：Success=false 但有文件已提交时，在概览后显式说明状态
	// 避免 PR 审阅者误以为这是正常的成功 PR
	if out != nil && !out.Success && committedCount > 0 {
		sb.WriteString(fmt.Sprintf("> ⚠️ **部分交付**：测试生成执行失败，但已有 **%d** 个文件提交到分支。请查看下方「失败分类」了解原因，合并前需人工审查生成内容。\n\n", committedCount))
	}

	if out == nil {
		sb.WriteString("> 本次任务未产出内层 TestGenOutput，详见任务记录与容器日志。\n\n")
		sb.WriteString("---\n[bot] 由 DTWorkflow 自动生成")
		return finalizePRBody(sb.String())
	}

	// 2. 失败分类（Success=false 或显式非 none；这是 M4.2 通知层 severity 的依据）
	writeFailureCategorySection(&sb, out)

	// 3. GapAnalysis 摘要
	writeGapAnalysisSection(&sb, out.Analysis)

	// 4. GeneratedFiles 列表
	if generatedCount > 0 {
		sb.WriteString("### 已生成文件\n\n")
		sb.WriteString("| 路径 | Operation | 测试数 | 框架 |\n")
		sb.WriteString("|------|-----------|--------|------|\n")
		for _, gf := range out.GeneratedFiles {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %d | `%s` |\n",
				escapePRMarkdown(gf.Path),
				escapePRMarkdown(gf.Operation),
				gf.TestCount,
				escapePRMarkdown(gf.Framework)))
		}
		sb.WriteString("\n")
	}

	// 5. TestRunResults
	if out.TestResults != nil {
		tr := out.TestResults
		sb.WriteString("### 测试结果\n\n")
		sb.WriteString(fmt.Sprintf(
			"通过 **%d** / 失败 **%d** / 跳过 **%d**（全部通过：%v，耗时 %d ms）\n\n",
			tr.Passed, tr.Failed, tr.Skipped, tr.AllPassed, tr.DurationMs))
	}

	// 6. SkippedTargets
	if skippedCount > 0 {
		sb.WriteString("### 已跳过目标\n\n")
		sb.WriteString("| 目标 | 原因 |\n")
		sb.WriteString("|------|------|\n")
		for _, st := range out.SkippedTargets {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` |\n",
				escapePRMarkdown(st.Path), escapePRMarkdown(st.Reason)))
		}
		sb.WriteString("\n")
	}

	// 7. Warnings
	if len(out.Warnings) > 0 {
		sb.WriteString("### 告警\n\n")
		for _, w := range out.Warnings {
			sb.WriteString(fmt.Sprintf("- `%s`\n", escapePRMarkdown(w)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n[bot] 由 DTWorkflow 自动生成")
	return finalizePRBody(sb.String())
}

// writeFailureCategorySection 写入失败分类节：
//   - Success=true 且 FailureCategory 为空或 "none" → 不渲染
//   - 其它情况（含 Success=false 或 Success=true 但 FailureCategory 被非法填写）→ 渲染
func writeFailureCategorySection(sb *strings.Builder, out *TestGenOutput) {
	shouldRender := !out.Success ||
		(out.FailureCategory != "" && out.FailureCategory != FailureCategoryNone)
	if !shouldRender {
		return
	}
	sb.WriteString("### 失败分类\n\n")
	sb.WriteString("| 项目 | 值 |\n")
	sb.WriteString("|------|----|\n")

	category := string(out.FailureCategory)
	if category == "" {
		category = "(未填)"
	}
	sb.WriteString(fmt.Sprintf("| failure_category | `%s` |\n", escapePRMarkdown(category)))
	sb.WriteString(fmt.Sprintf("| Success | %s |\n", boolDisplay(out.Success, "✅ 通过", "❌ 失败")))
	sb.WriteString(fmt.Sprintf("| InfoSufficient | %s |\n", boolDisplay(out.InfoSufficient, "✅ 充足", "❌ 不足")))
	if out.FailureReason != "" {
		sb.WriteString(fmt.Sprintf("| failure_reason | %s |\n", escapePRMarkdown(out.FailureReason)))
	}
	if len(out.MissingInfo) > 0 {
		sb.WriteString("\n**缺失信息**：\n")
		for _, m := range out.MissingInfo {
			sb.WriteString(fmt.Sprintf("- %s\n", escapePRMarkdown(m)))
		}
	}
	sb.WriteString("\n")
}

// writeGapAnalysisSection 渲染 GapAnalysis 摘要（最多 prBodyMaxGapAnalysisItems 个 UntestedModules）。
func writeGapAnalysisSection(sb *strings.Builder, analysis *GapAnalysis) {
	if analysis == nil || len(analysis.UntestedModules) == 0 {
		return
	}
	sb.WriteString("### 缺口分析（TOP ")
	items := analysis.UntestedModules
	truncated := false
	if len(items) > prBodyMaxGapAnalysisItems {
		items = items[:prBodyMaxGapAnalysisItems]
		truncated = true
	}
	sb.WriteString(fmt.Sprintf("%d）\n\n", len(items)))
	sb.WriteString("| 路径 | 类型 | 优先级 | 原因 |\n")
	sb.WriteString("|------|------|--------|------|\n")
	for _, u := range items {
		sb.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` | %s |\n",
			escapePRMarkdown(u.Path),
			escapePRMarkdown(u.Kind),
			escapePRMarkdown(u.Priority),
			escapePRMarkdown(u.Reason)))
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("\n_（共 %d 个目标，仅展示前 %d 个）_\n",
			len(analysis.UntestedModules), prBodyMaxGapAnalysisItems))
	}
	sb.WriteString("\n")
}

// boolDisplay 把 bool 渲染成带图标的中文描述，提升 PR body 可读性。
func boolDisplay(v bool, yes, no string) string {
	if v {
		return yes
	}
	return no
}

// stripNonBMPChars 移除 U+10000 以上的非 BMP 字符（对齐 fix.stripNonBMPChars）。
func stripNonBMPChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r > 0xFFFF {
			return -1
		}
		return r
	}, s)
}

// finalizePRBody 把超长 body 按 prBodyMaxLen 截断（UTF-8 安全），并追加截断说明。
func finalizePRBody(body string) string {
	if len(body) <= prBodyMaxLen {
		return body
	}
	truncMsg := "\n\n_（内容过长，已截断）_"
	return truncatePRString(body, prBodyMaxLen-len(truncMsg)) + truncMsg
}
