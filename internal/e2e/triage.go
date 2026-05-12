package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// TriageE2EOutput triage 分析输出结构。
type TriageE2EOutput struct {
	Modules        []TriageModule `json:"modules"`
	SkippedModules []TriageModule `json:"skipped_modules"`
	Analysis       string         `json:"analysis"`
}

// TriageModule triage 输出中的模块条目。
type TriageModule struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

const maxTriageReasonLen = 2048
const maxTriageAnalysisLen = 2048
const maxTriageChangedFiles = 50

// TriagePromptContext 描述一次回归分析需要的稳定 Git 上下文。
type TriagePromptContext struct {
	Repo           string
	BaseRef        string
	BaseSHA        string
	HeadSHA        string
	MergeCommitSHA string
	ChangedFiles   []string
}

// ParseTriageResult 双层 JSON 解析（CLI 信封 → TriageE2EOutput）。
func ParseTriageResult(raw string) (*TriageE2EOutput, error) {
	jsonStr := extractTriageJSON(raw)
	if jsonStr == "" {
		return nil, fmt.Errorf("%w: 未找到 JSON", ErrE2ETriageParseFailure)
	}

	var out TriageE2EOutput
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		// 尝试 CLI 信封剥离
		var envelope struct {
			Result string `json:"result"`
		}
		if envErr := json.Unmarshal([]byte(jsonStr), &envelope); envErr == nil && envelope.Result != "" {
			innerJSON := extractTriageJSON(envelope.Result)
			if innerJSON != "" {
				if err2 := json.Unmarshal([]byte(innerJSON), &out); err2 != nil {
					return nil, fmt.Errorf("%w: 内层解析失败: %v", ErrE2ETriageParseFailure, err2)
				}
				sanitizeTriageOutput(&out)
				return &out, validateTriageOutput(&out)
			}
		}
		return nil, fmt.Errorf("%w: %v", ErrE2ETriageParseFailure, err)
	}

	// 检查是否是 CLI 信封（有 result 字段包含 JSON 字符串）
	var envelope struct {
		Result string `json:"result"`
	}
	if json.Unmarshal([]byte(jsonStr), &envelope) == nil && envelope.Result != "" {
		innerJSON := extractTriageJSON(envelope.Result)
		if innerJSON != "" {
			var innerOut TriageE2EOutput
			if err := json.Unmarshal([]byte(innerJSON), &innerOut); err == nil {
				if innerOut.Modules != nil {
					out = innerOut
				}
			}
		}
	}

	sanitizeTriageOutput(&out)
	return &out, validateTriageOutput(&out)
}

func validateTriageOutput(out *TriageE2EOutput) error {
	if out.Modules == nil {
		return fmt.Errorf("%w: modules 字段缺失", ErrE2ETriageParseFailure)
	}
	for i, m := range out.Modules {
		if strings.TrimSpace(m.Name) == "" {
			return fmt.Errorf("%w: modules[%d].name 为空", ErrE2ETriageParseFailure, i)
		}
	}
	return nil
}

func sanitizeTriageOutput(out *TriageE2EOutput) {
	out.Analysis = sanitizeTriageText(out.Analysis, maxTriageAnalysisLen)
	for i := range out.Modules {
		out.Modules[i].Reason = sanitizeTriageText(out.Modules[i].Reason, maxTriageReasonLen)
		out.Modules[i].Name = strings.TrimSpace(out.Modules[i].Name)
	}
	for i := range out.SkippedModules {
		out.SkippedModules[i].Reason = sanitizeTriageText(out.SkippedModules[i].Reason, maxTriageReasonLen)
		out.SkippedModules[i].Name = strings.TrimSpace(out.SkippedModules[i].Name)
	}
}

// sanitizeTriageText 过滤控制字符（保留换行和 tab）并截断到 maxLen。
func sanitizeTriageText(s string, maxLen int) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			continue
		}
		b.WriteRune(r)
	}
	result := b.String()
	if len(result) > maxLen {
		result = result[:maxLen] + "... (truncated)"
	}
	return result
}

// extractTriageJSON 从 Claude 输出中提取 JSON 内容。
// 支持三种布局：纯 JSON、代码块包裹、自然语言前后文中的 JSON。
func extractTriageJSON(s string) string {
	s = strings.TrimSpace(s)
	// 优先处理代码块包裹
	fenceStart := strings.Index(s, "```")
	if fenceStart >= 0 {
		fenced := s[fenceStart:]
		lines := strings.SplitN(fenced, "\n", 2)
		if len(lines) == 2 {
			fenced = lines[1]
		}
		if idx := strings.LastIndex(fenced, "```"); idx >= 0 {
			fenced = fenced[:idx]
		}
		return strings.TrimSpace(fenced)
	}
	// 查找第一个 { 和最后一个 }
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	end := strings.LastIndex(s, "}")
	if end < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// BuildTriagePrompt 构建 triage 分析 prompt。
func BuildTriagePrompt(repo, baseRef string, changedFiles []string) string {
	return BuildTriagePromptWithContext(TriagePromptContext{
		Repo:         repo,
		BaseRef:      baseRef,
		ChangedFiles: changedFiles,
	})
}

// BuildTriagePromptWithContext 构建带精确提交信息的 triage 分析 prompt。
func BuildTriagePromptWithContext(ctx TriagePromptContext) string {
	var b strings.Builder

	// 上下文
	b.WriteString("## Context\n\n")
	fmt.Fprintf(&b, "Repository: %s\n", ctx.Repo)
	fmt.Fprintf(&b, "Base branch: %s\n", ctx.BaseRef)
	if ctx.BaseSHA != "" {
		fmt.Fprintf(&b, "Base SHA before merge: %s\n", ctx.BaseSHA)
	}
	if ctx.HeadSHA != "" {
		fmt.Fprintf(&b, "PR head SHA: %s\n", ctx.HeadSHA)
	}
	if ctx.MergeCommitSHA != "" {
		fmt.Fprintf(&b, "Merged commit SHA: %s\n", ctx.MergeCommitSHA)
	}
	fmt.Fprintf(&b, "Changed files count: %d\n\n", len(ctx.ChangedFiles))

	// 变更文件列表
	files := ctx.ChangedFiles
	truncated := false
	if len(files) > maxTriageChangedFiles {
		files = files[:maxTriageChangedFiles]
		truncated = true
	}
	b.WriteString("### Changed files\n\n")
	for _, f := range files {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	if truncated {
		fmt.Fprintf(&b, "\n... and %d more files (truncated)\n", len(ctx.ChangedFiles)-maxTriageChangedFiles)
	}

	// 分析指令
	b.WriteString("\n## Instructions\n\n")
	b.WriteString("Analyze the changed files above and determine which E2E test modules need to be run for regression testing.\n\n")
	b.WriteString("1. Read the `e2e/` directory to discover all available E2E test modules (each subdirectory under `e2e/` is a module)\n")
	b.WriteString("2. For each module, read `e2e/{module}/cases/*/case.yaml` to understand what the tests cover\n")
	b.WriteString("3. Analyze the exact merged change. Prefer these commands in order:\n")
	if ctx.BaseSHA != "" && ctx.HeadSHA != "" {
		fmt.Fprintf(&b, "   - `git diff %s...%s`\n", ctx.BaseSHA, ctx.HeadSHA)
	}
	if ctx.MergeCommitSHA != "" {
		fmt.Fprintf(&b, "   - `git diff %s^1 %s`\n", ctx.MergeCommitSHA, ctx.MergeCommitSHA)
	}
	b.WriteString("   - If exact commits are unavailable, rely on the changed file list above and explain the limitation\n")
	b.WriteString("4. Determine which E2E modules are affected by the changes based on:\n")
	b.WriteString("   - Direct code path overlap (changed files are used by test scenarios)\n")
	b.WriteString("   - Shared dependencies (changed code is imported by modules under test)\n")
	b.WriteString("   - API/interface changes (endpoint or data format changes affecting E2E flows)\n\n")

	// 约束
	b.WriteString("## Constraints\n\n")
	b.WriteString("- READ-ONLY: Do NOT modify any files\n")
	b.WriteString("- If no E2E modules are affected, return an empty modules array\n")
	b.WriteString("- Be conservative: when in doubt, include the module\n")
	b.WriteString("- Only skip modules when you are confident the changes cannot affect them\n\n")

	// 输出格式
	b.WriteString("## Output format\n\n")
	b.WriteString("Respond with a single JSON object:\n\n")
	b.WriteString("```json\n")
	b.WriteString("{\n")
	b.WriteString("  \"modules\": [\n")
	b.WriteString("    {\"name\": \"module_name\", \"reason\": \"brief explanation\"}\n")
	b.WriteString("  ],\n")
	b.WriteString("  \"skipped_modules\": [\n")
	b.WriteString("    {\"name\": \"module_name\", \"reason\": \"why it was skipped\"}\n")
	b.WriteString("  ],\n")
	b.WriteString("  \"analysis\": \"Overall analysis summary\"\n")
	b.WriteString("}\n")
	b.WriteString("```\n")

	return b.String()
}
