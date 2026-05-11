package e2e

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

const (
	maxAnalysisBytes  = 4096
	maxErrorMsgBytes  = 4096
	maxIssueBodyBytes = 60000
)

func formatIssueTitle(module, caseName, category string) string {
	return sanitizeGiteaText(fmt.Sprintf("[E2E] %s/%s — %s", module, caseName, category))
}

func formatBugIssueBody(c CaseResult, payload model.TaskPayload, envName, baseURL string) string {
	var b strings.Builder

	b.WriteString("## E2E 测试失败 — 疑似应用缺陷\n\n")
	writeCommonFields(&b, c, payload, envName, baseURL)
	writeFailureAnalysis(&b, c)
	writeErrorMsg(&b, c)
	writeExpectations(&b, c)
	writeFailedScripts(&b, c)
	b.WriteString("\n### 截图\n\n见下方附件。\n")
	writeAnchor(&b, payload.TaskID, c.CasePath)

	return enforceBodyLimit(b.String())
}

func formatScriptOutdatedIssueBody(c CaseResult, payload model.TaskPayload, envName, baseURL string) string {
	var b strings.Builder

	b.WriteString("## E2E 测试失败 — 脚本过时\n\n")
	writeCommonFields(&b, c, payload, envName, baseURL)
	writeFailureAnalysis(&b, c)
	writeErrorMsg(&b, c)
	writeExpectations(&b, c)
	writeFailedScripts(&b, c)

	// 修复指引
	scriptPath := ""
	if c.TestResult != nil && len(c.TestResult.Scripts) > 0 {
		scriptPath = c.CasePath + "/" + c.TestResult.Scripts[0].Name
	}
	b.WriteString("\n### 修复指引\n\n")
	b.WriteString("此用例因页面元素或流程变更导致脚本无法正常执行。\n")
	if scriptPath != "" {
		fmt.Fprintf(&b, "请修复 `%s` 以匹配当前页面结构，保持上述 expectations 中描述的业务意图不变。\n\n",
			escapeMarkdown(scriptPath))
	}
	b.WriteString("> 此 Issue 已标记 `fix-to-pr`，DTWorkflow 将尝试自动修复。\n")

	b.WriteString("\n### 截图\n\n见下方附件。\n")
	writeAnchor(&b, payload.TaskID, c.CasePath)

	return enforceBodyLimit(b.String())
}

func writeCommonFields(b *strings.Builder, c CaseResult, payload model.TaskPayload, envName, baseURL string) {
	fmt.Fprintf(b, "**用例**：%s\n", escapeMarkdown(c.Name))
	fmt.Fprintf(b, "**模块**：%s\n", escapeMarkdown(c.Module))
	fmt.Fprintf(b, "**用例路径**：`%s`\n", escapeMarkdown(c.CasePath))
	fmt.Fprintf(b, "**测试环境**：%s (%s)\n", escapeMarkdown(envName), escapeMarkdown(baseURL))
	fmt.Fprintf(b, "**代码基线**：%s\n", escapeMarkdown(payload.BaseRef))
	if phase := detectFailedPhase(c); phase != "" {
		fmt.Fprintf(b, "**失败阶段**：%s\n", phase)
	}
	b.WriteString("\n")
}

func writeFailureAnalysis(b *strings.Builder, c CaseResult) {
	if c.FailureAnalysis == "" {
		return
	}
	b.WriteString("### 失败分析\n\n")
	b.WriteString(escapeMarkdown(truncateUTF8(c.FailureAnalysis, maxAnalysisBytes)))
	b.WriteString("\n\n")
}

func writeErrorMsg(b *strings.Builder, c CaseResult) {
	msg := extractErrorMsg(c)
	if msg == "" {
		return
	}
	b.WriteString("### 错误信息\n\n```\n")
	b.WriteString(truncateUTF8(msg, maxErrorMsgBytes))
	b.WriteString("\n```\n\n")
}

func writeExpectations(b *strings.Builder, c CaseResult) {
	if len(c.Expectations) == 0 {
		return
	}
	b.WriteString("### 业务意图（case.yaml expectations）\n\n")
	b.WriteString("| 步骤 | 预期结果 |\n")
	b.WriteString("|------|--------|\n")
	for _, e := range c.Expectations {
		fmt.Fprintf(b, "| %s | %s |\n", escapeTableCell(e.Step), escapeTableCell(e.Expect))
	}
	b.WriteString("\n")
}

func writeFailedScripts(b *strings.Builder, c CaseResult) {
	scripts := collectFailedScripts(c)
	if len(scripts) == 0 {
		return
	}
	b.WriteString("### 失败脚本\n\n")
	for _, s := range scripts {
		fmt.Fprintf(b, "- `%s` — 退出码 %d\n", escapeMarkdown(s.Name), s.ExitCode)
	}
}

func writeAnchor(b *strings.Builder, taskID, casePath string) {
	b.WriteString("\n---\n")
	fmt.Fprintf(b, "<!-- dtworkflow:e2e:%s:%s -->\n", taskID, casePath)
	b.WriteString("此 Issue 由 DTWorkflow E2E 测试引擎自动创建。\n")
}

func detectFailedPhase(c CaseResult) string {
	if c.SetupResult != nil && c.SetupResult.Status == "failed" {
		return "setup"
	}
	if c.TestResult != nil && c.TestResult.Status == "failed" {
		return "test"
	}
	if c.TeardownResult != nil && c.TeardownResult.Status == "failed" {
		return "teardown"
	}
	return ""
}

func extractErrorMsg(c CaseResult) string {
	for _, pr := range []*PhaseResult{c.TestResult, c.SetupResult, c.TeardownResult} {
		if pr == nil {
			continue
		}
		for _, s := range pr.Scripts {
			if s.ErrorMsg != "" {
				return s.ErrorMsg
			}
		}
	}
	return ""
}

func collectFailedScripts(c CaseResult) []ScriptResult {
	var results []ScriptResult
	for _, pr := range []*PhaseResult{c.SetupResult, c.TestResult, c.TeardownResult} {
		if pr == nil {
			continue
		}
		for _, s := range pr.Scripts {
			if s.ExitCode != 0 {
				results = append(results, s)
			}
		}
	}
	return results
}

func escapeMarkdown(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`[`, `\[`, `]`, `\]`,
		`(`, `\(`, `)`, `\)`,
		`!`, `\!`, `<`, `\<`, `>`, `\>`,
		`*`, `\*`, `_`, `\_`,
		"`", "\\`", `#`, `\#`, `~`, `\~`,
	)
	return r.Replace(s)
}

func escapeTableCell(s string) string {
	s = escapeMarkdown(s)
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// sanitizeGiteaText 过滤非 BMP 字符（U+10000 以上），确保 Gitea MySQL utf8 兼容。
func sanitizeGiteaText(s string) string {
	return strings.Map(func(r rune) rune {
		if r > 0xFFFF {
			return -1
		}
		return r
	}, s)
}

func enforceBodyLimit(s string) string {
	s = sanitizeGiteaText(s)
	if len(s) > maxIssueBodyBytes {
		return truncateUTF8(s, maxIssueBodyBytes)
	}
	return s
}
