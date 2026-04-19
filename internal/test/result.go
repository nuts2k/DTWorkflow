package test

import (
	"fmt"
	"strings"
	"unicode"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// ============================================================================
// Claude 自由文本脱敏 —— 防 prompt-injection 内容泄露到飞书 / PR 评论 / Gitea
// ============================================================================
//
// 背景：TestGenOutput 有大量字段由 Claude 输出原样填充（failure_reason /
// missing_info / analysis.priority_notes / skipped_targets.reason / warnings
// 等）。攻击者可通过 Issue 标题 / PR 标题 / 仓库名等入口走 prompt injection
// 让 Claude 往这些字段塞钓鱼链接、控制字符或超长文本，最终被 Processor 注入
// 飞书卡片 / Gitea 评论 / PR body。
//
// 统一在 parseResult 成功路径调用 sanitizeTestGenOutput 做一次性过滤，避免
// 每个渲染器重复防御、漏防。
//
// 设计决策：
//   - Warnings：走白名单前缀匹配（只保留 entrypoint 已知写入的 KEY=1 格式），
//     单条 ≤ maxWarningLen，总数 ≤ maxWarningCount。
//   - 其他自由文本：统一经 sanitizeClaudeText —— 剥离 http(s)://ftp:// 链接、
//     过滤控制字符、按 UTF-8 rune 硬截断到 maxClaudeFreeTextLen。
const (
	maxWarningCount       = 10
	maxWarningLen         = 200
	maxClaudeFreeTextLen  = 500
	linkRedactedMarker    = "[link-redacted]"
	maxClaudeListItems    = 20
	// maxClaudeShortTextLen 用于较短字段（如 Path / Kind / Priority / Operation 等
	// 名字化字段）截断长度，避免 Claude 往短字段里塞长文。
	maxClaudeShortTextLen = 200
)

// allowedWarningPrefixes 是 warnings 字段允许的 KEY= 前缀白名单。
// 与 build/docker/entrypoint.sh 在 gen_tests 分支写入 /tmp/.gen_tests_warnings
// 的 KEY 同步；新增告警 KEY 必须同时更新此处。
var allowedWarningPrefixes = []string{
	"AUTO_TEST_BRANCH_RESET_PUSHED=",
	"AUTO_TEST_BRANCH_RESET_REMOTE_FAILED=",
	"ENTRYPOINT_BASE_SHA=",
}

// sanitizeClaudeText 过滤 Claude 自由文本：
//  1. 去除控制字符（保留换行符不会被写入输出 —— 统一替换为空格，防止日志/卡片分行破坏）
//  2. 掩蔽 http(s):// / ftp:// 链接（以 http/https/ftp:// 开头的 token 被替换为 marker）
//  3. 按 UTF-8 rune 硬截断到 maxLen
//  4. 去首尾空白
func sanitizeClaudeText(s string, maxLen int) string {
	if s == "" || maxLen <= 0 {
		return ""
	}
	// 1. 控制字符过滤
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\x00':
			// NUL 直接删除
		case r == '\r' || r == '\n' || r == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	cleaned := b.String()

	// 2. 链接掩蔽（不借助正则，保持纯文本扫描）
	cleaned = redactURLScheme(cleaned, "http://")
	cleaned = redactURLScheme(cleaned, "https://")
	cleaned = redactURLScheme(cleaned, "ftp://")
	cleaned = redactURLScheme(cleaned, "HTTP://")
	cleaned = redactURLScheme(cleaned, "HTTPS://")
	cleaned = redactURLScheme(cleaned, "FTP://")

	// 3. UTF-8 安全截断
	runes := []rune(cleaned)
	if len(runes) > maxLen {
		cleaned = string(runes[:maxLen]) + "…"
	}

	return strings.TrimSpace(cleaned)
}

// redactURLScheme 将 s 中以 scheme 开头的连续 non-whitespace token 替换为 marker。
func redactURLScheme(s, scheme string) string {
	if !strings.Contains(s, scheme) {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	for {
		idx := strings.Index(s, scheme)
		if idx < 0 {
			out.WriteString(s)
			break
		}
		out.WriteString(s[:idx])
		// 跳过 URL token 直到下一个空白 / 结束
		rest := s[idx+len(scheme):]
		endIdx := len(rest)
		for i, r := range rest {
			if unicode.IsSpace(r) {
				endIdx = i
				break
			}
		}
		out.WriteString(linkRedactedMarker)
		s = rest[endIdx:]
	}
	return out.String()
}

// sanitizeWarnings 按白名单 + 长度 + 条数三重约束清洗 warnings，
// 防止 Claude 被 prompt injection 后往 warnings 里塞恶意文本/链接。
// 不匹配白名单前缀的告警一律丢弃（entrypoint 写入的 KEY=1 格式天然满足）。
func sanitizeWarnings(ws []string) []string {
	if len(ws) == 0 {
		return ws
	}
	out := make([]string, 0, len(ws))
	for _, raw := range ws {
		if len(out) >= maxWarningCount {
			break
		}
		// 先去控制字符
		trimmed := strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7F {
				return -1
			}
			return r
		}, raw)
		trimmed = strings.TrimSpace(trimmed)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > maxWarningLen {
			trimmed = trimmed[:maxWarningLen]
		}
		matched := false
		for _, p := range allowedWarningPrefixes {
			if strings.HasPrefix(trimmed, p) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// sanitizeTestGenOutput 对 TestGenOutput 里所有 Claude 可控的自由文本字段
// 做一次性脱敏。由 parseResult 在成功解析后调用，确保下游所有渲染器
// （pr_body / feishu_card / gitea_notifier / 日志）不再各自防御。
//
// 修改字段：
//   - Warnings（白名单过滤）
//   - FailureReason / TestStrategy
//   - MissingInfo[i]
//   - Analysis.ExistingStyle / PriorityNotes
//   - Analysis.UntestedModules[i].Reason
//   - Analysis.ExistingTests[i].Framework / Source / TestFile / TargetFiles[j]
//   - GeneratedFiles[i].Description / Framework / Path / Operation / TargetFiles[j]
//   - SkippedTargets[i].Reason / Path
//   - CommittedFiles[i]（路径，不应含 URL）
//   - BranchName / CommitSHA（git ref，短字段）
//   - FailureCategory（枚举，但防御性截断）
//   - TestResults.Framework
//
// 注意：sanitizeClaudeText 可能替换掉合法的链接 —— Claude 正常输出不应包含
// 链接（prompt 明确要求中文描述），所以是可接受的 false-positive 代价。
func sanitizeTestGenOutput(out *TestGenOutput) {
	if out == nil {
		return
	}

	// 保护 len(Warnings) > maxWarningCount 的情况，走白名单
	out.Warnings = sanitizeWarnings(out.Warnings)

	out.FailureReason = sanitizeClaudeText(out.FailureReason, maxClaudeFreeTextLen)
	out.TestStrategy = sanitizeClaudeText(out.TestStrategy, maxClaudeFreeTextLen)
	out.BranchName = sanitizeClaudeText(out.BranchName, maxClaudeShortTextLen)
	out.CommitSHA = sanitizeClaudeText(out.CommitSHA, maxClaudeShortTextLen)
	out.FailureCategory = FailureCategory(sanitizeClaudeText(string(out.FailureCategory), maxClaudeShortTextLen))

	if len(out.MissingInfo) > maxClaudeListItems {
		out.MissingInfo = out.MissingInfo[:maxClaudeListItems]
	}
	for i, m := range out.MissingInfo {
		out.MissingInfo[i] = sanitizeClaudeText(m, maxClaudeFreeTextLen)
	}

	if out.Analysis != nil {
		out.Analysis.ExistingStyle = sanitizeClaudeText(out.Analysis.ExistingStyle, maxClaudeFreeTextLen)
		out.Analysis.PriorityNotes = sanitizeClaudeText(out.Analysis.PriorityNotes, maxClaudeFreeTextLen)
		if len(out.Analysis.UntestedModules) > maxClaudeListItems {
			out.Analysis.UntestedModules = out.Analysis.UntestedModules[:maxClaudeListItems]
		}
		for i := range out.Analysis.UntestedModules {
			u := &out.Analysis.UntestedModules[i]
			u.Path = sanitizeClaudeText(u.Path, maxClaudeShortTextLen)
			u.Kind = sanitizeClaudeText(u.Kind, maxClaudeShortTextLen)
			u.Priority = sanitizeClaudeText(u.Priority, maxClaudeShortTextLen)
			u.Reason = sanitizeClaudeText(u.Reason, maxClaudeFreeTextLen)
		}
		if len(out.Analysis.ExistingTests) > maxClaudeListItems {
			out.Analysis.ExistingTests = out.Analysis.ExistingTests[:maxClaudeListItems]
		}
		for i := range out.Analysis.ExistingTests {
			e := &out.Analysis.ExistingTests[i]
			e.TestFile = sanitizeClaudeText(e.TestFile, maxClaudeShortTextLen)
			e.Framework = sanitizeClaudeText(e.Framework, maxClaudeShortTextLen)
			e.Source = sanitizeClaudeText(e.Source, maxClaudeShortTextLen)
			if len(e.TargetFiles) > maxClaudeListItems {
				e.TargetFiles = e.TargetFiles[:maxClaudeListItems]
			}
			for j, t := range e.TargetFiles {
				e.TargetFiles[j] = sanitizeClaudeText(t, maxClaudeShortTextLen)
			}
		}
	}

	if len(out.GeneratedFiles) > maxClaudeListItems {
		out.GeneratedFiles = out.GeneratedFiles[:maxClaudeListItems]
	}
	for i := range out.GeneratedFiles {
		g := &out.GeneratedFiles[i]
		g.Path = sanitizeClaudeText(g.Path, maxClaudeShortTextLen)
		g.Operation = sanitizeClaudeText(g.Operation, maxClaudeShortTextLen)
		g.Description = sanitizeClaudeText(g.Description, maxClaudeFreeTextLen)
		g.Framework = sanitizeClaudeText(g.Framework, maxClaudeShortTextLen)
		if len(g.TargetFiles) > maxClaudeListItems {
			g.TargetFiles = g.TargetFiles[:maxClaudeListItems]
		}
		for j, t := range g.TargetFiles {
			g.TargetFiles[j] = sanitizeClaudeText(t, maxClaudeShortTextLen)
		}
	}

	if len(out.CommittedFiles) > maxClaudeListItems {
		out.CommittedFiles = out.CommittedFiles[:maxClaudeListItems]
	}
	for i, f := range out.CommittedFiles {
		out.CommittedFiles[i] = sanitizeClaudeText(f, maxClaudeShortTextLen)
	}

	if len(out.SkippedTargets) > maxClaudeListItems {
		out.SkippedTargets = out.SkippedTargets[:maxClaudeListItems]
	}
	for i := range out.SkippedTargets {
		s := &out.SkippedTargets[i]
		s.Path = sanitizeClaudeText(s.Path, maxClaudeShortTextLen)
		s.Reason = sanitizeClaudeText(s.Reason, maxClaudeFreeTextLen)
	}

	if out.TestResults != nil {
		out.TestResults.Framework = sanitizeClaudeText(out.TestResults.Framework, maxClaudeShortTextLen)
	}
}

// SanitizeErrorMessage 对外暴露的 error 字符串脱敏工具，用于 Processor
// 把 runErr.Error() 写入 record.Error 前兜底过滤，避免 ParseError 透传原始输出
// 之外的场景仍然泄露 Claude 文本。
func SanitizeErrorMessage(s string) string {
	return sanitizeClaudeText(s, maxClaudeFreeTextLen)
}

// FailureCategory 标识 gen_tests 失败分类。
//
// Success=true 时必须为 FailureCategoryNone；Success=false 时必须为 infrastructure
// / test_quality / info_insufficient 三者之一（空值与未枚举值都非法）。
// 通知层据此决定 severity：
//   - infrastructure   → Warning（需运维介入）
//   - test_quality     → Info（业务正常，测试质量问题）
//   - info_insufficient → Info（附 MissingInfo）
type FailureCategory string

// FailureCategory 取值。
const (
	FailureCategoryNone             FailureCategory = "none"
	FailureCategoryInfrastructure   FailureCategory = "infrastructure"
	FailureCategoryTestQuality      FailureCategory = "test_quality"
	FailureCategoryInfoInsufficient FailureCategory = "info_insufficient"
)

// TestGenOutput 是 Claude 容器内层输出的核心 JSON schema（M4.1 §4.3 + M4.2 §4.1 增量）。
//
// 与 fix.FixOutput 的语义对称：Success=true 时必须满足完整不变量
// （见 validateSuccessfulTestGenOutput）；Success=false 可携带部分交付
// （committed_files + skipped_targets）与失败原因 + 失败分类。
type TestGenOutput struct {
	Success            bool            `json:"success"`
	InfoSufficient     bool            `json:"info_sufficient"`
	MissingInfo        []string        `json:"missing_info,omitempty"`
	Analysis           *GapAnalysis    `json:"analysis,omitempty"`
	TestStrategy       string          `json:"test_strategy,omitempty"`
	GeneratedFiles     []GeneratedFile `json:"generated_files,omitempty"`
	CommittedFiles     []string        `json:"committed_files,omitempty"`
	SkippedTargets     []SkippedTarget `json:"skipped_targets,omitempty"`
	TestResults        *TestRunResults `json:"test_results,omitempty"`
	VerificationPassed bool            `json:"verification_passed"`
	BranchName         string          `json:"branch_name,omitempty"`
	CommitSHA          string          `json:"commit_sha,omitempty"`
	// M4.2 新增：失败分类，由 Claude 在 prompt 指令下分类填写。
	FailureCategory FailureCategory `json:"failure_category,omitempty"`
	FailureReason   string          `json:"failure_reason,omitempty"`
	// M4.2 新增：entrypoint 写入的 /tmp/.gen_tests_warnings 内容，Claude 原样透传。
	Warnings    []string `json:"warnings,omitempty"`
	RetryRounds int      `json:"retry_rounds,omitempty"`
}

// GapAnalysis 测试缺口分析结果。
type GapAnalysis struct {
	UntestedModules []UntestedModule      `json:"untested_modules"`
	ExistingTests   []ExistingTestSummary `json:"existing_tests,omitempty"`
	ExistingStyle   string                `json:"existing_style"`
	PriorityNotes   string                `json:"priority_notes"`
}

// UntestedModule 未覆盖的模块条目。
type UntestedModule struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`     // service / controller / util / component / composable / store
	Priority string `json:"priority"` // high / medium / low
	Reason   string `json:"reason"`
}

// ExistingTestSummary 扫描到的既有测试文件摘要。
//
// M4.2 新增 Source 字段：
//   - project_existing    用户在项目内编写的既有测试（风格模板来源）
//   - branch_continuation 本服务前一次任务产出、沿 auto-test 稳定分支延续（不作为风格模板）
type ExistingTestSummary struct {
	TestFile    string   `json:"test_file"`
	TargetFiles []string `json:"target_files"`
	Framework   string   `json:"framework"`
	Source      string   `json:"source,omitempty"`
}

// GeneratedFile 本次生成的测试文件声明。
type GeneratedFile struct {
	Path        string   `json:"path"`
	Operation   string   `json:"operation"` // "create" | "append"
	Description string   `json:"description"`
	Framework   string   `json:"framework"`
	TargetFiles []string `json:"target_files"`
	TestCount   int      `json:"test_count"`
}

// SkippedTarget 因预算或验证失败跳过的目标。
type SkippedTarget struct {
	Path   string `json:"path"`
	Reason string `json:"reason"` // time_budget_exhausted / token_budget_exhausted / environment_issue / verification_failed_after_retries
}

// TestRunResults 容器内测试运行结果。
//
// 与 fix.TestResults 语义类似但命名刻意区别（Run 前缀），避免跨包 import
// 时 fix.TestResults / test.TestResults 同名消歧混乱；多出的 Framework
// 字段用于混合仓区分 Java / Vue 产物。
type TestRunResults struct {
	Framework  string `json:"framework,omitempty"`
	Passed     int    `json:"passed"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped"`
	AllPassed  bool   `json:"all_passed"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// TestGenResult Service.Execute 的返回值。
type TestGenResult struct {
	Framework  Framework      // 最终采用的测试框架
	BaseRef    string         // 解析后的 base ref（传入 payload 或仓库默认分支）
	RawOutput  string         // Claude CLI 原始 stdout
	ExitCode   int            // 容器退出码（非 0 表示执行失败）
	CLIMeta    *model.CLIMeta // CLI 执行元数据
	Output     *TestGenOutput // 解析成功的内层输出（可能为 nil）
	ParseError error          // JSON 解析或不变量校验失败时非 nil
	PRNumber   int64          // M4.2 填充；M4.1 createTestPR 占位返回 0
	PRURL      string         // M4.2 填充；M4.1 createTestPR 占位返回 ""
}

// CLIResponse 外层 Claude CLI JSON 信封（与 fix.CLIResponse 同义独立实现）。
type CLIResponse struct {
	Type          string  `json:"type"`
	Subtype       string  `json:"subtype"`
	CostUSD       float64 `json:"cost_usd"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	DurationMs    int64   `json:"duration_ms"`
	DurationAPIMs int64   `json:"duration_api_ms"`
	IsError       bool    `json:"is_error"`
	NumTurns      int     `json:"num_turns"`
	Result        string  `json:"result"`
	SessionID     string  `json:"session_id"`
}

// EffectiveCostUSD 返回有效的费用值，兼容新旧 CLI 字段。
func (r CLIResponse) EffectiveCostUSD() float64 {
	if r.TotalCostUSD > 0 {
		return r.TotalCostUSD
	}
	return r.CostUSD
}

// IsExecutionError 判断 CLI 响应是否表示执行错误。
//
// stream_monitor 开启时，tryExtractResultCLIJSON 会把 {"type":"result","subtype":"success"}
// 转为 {"type":"success"}，因此 "success" 与 "result" 均视为正常类型。
func (r CLIResponse) IsExecutionError() bool {
	if r.IsError {
		return true
	}
	switch r.Type {
	case "", "result", "success":
		return false
	default:
		return true
	}
}

// validateSuccessfulTestGenOutput 校验 TestGenOutput 的不变量（§6.2 完整清单 + §4.1 M4.2 增量）。
//
// 约束覆盖两层：
//  1. 跨 Success 状态的强约束：operation 必须为 create/append；append 必须声明 target_files。
//     即使 Success=false，非法 operation 也应阻断（防止下游误用）。
//  2. Success=true 的附加不变量：info_sufficient、verification_passed、test_results、
//     branch_name、commit_sha、committed_files 非空且为 generated_files 的子集；
//     failure_category 必须为 "none"（为兼容 M4.1 既有产出，空值视同 none）。
//
// 返回 nil 表示通过；非 nil 表示不变量违反。
//
// Success=false 分支应改走 validateFailureTestGenOutput（§4.1 新增），以覆盖
// FailureCategory 枚举 + InfoSufficient 一致性，不强制 CommitSHA / TestResults 非空。
func validateSuccessfulTestGenOutput(out *TestGenOutput) error {
	if out == nil {
		return fmt.Errorf("TestGenOutput 为空")
	}

	// 跨 Success 状态的强约束：operation 枚举与 append target_files
	generatedPaths := make(map[string]struct{}, len(out.GeneratedFiles))
	for i, gf := range out.GeneratedFiles {
		switch gf.Operation {
		case "create", "append":
			// ok
		default:
			return fmt.Errorf("GeneratedFile[%d].Operation 非法: %q（必须为 create 或 append）", i, gf.Operation)
		}
		if gf.Operation == "append" && len(gf.TargetFiles) == 0 {
			return fmt.Errorf("GeneratedFile[%d] append 操作必须声明 target_files", i)
		}
		generatedPaths[gf.Path] = struct{}{}
	}

	if !out.Success {
		return nil
	}

	if !out.InfoSufficient {
		return fmt.Errorf("Success=true 但 InfoSufficient=false")
	}
	if !out.VerificationPassed {
		return fmt.Errorf("Success=true 但 VerificationPassed=false")
	}
	if out.TestResults == nil {
		return fmt.Errorf("Success=true 但 test_results 为空")
	}
	if !out.TestResults.AllPassed || out.TestResults.Failed > 0 {
		return fmt.Errorf("测试未全部通过 all_passed=%v failed=%d",
			out.TestResults.AllPassed, out.TestResults.Failed)
	}
	if out.BranchName == "" {
		return fmt.Errorf("branch_name 为空")
	}
	if out.CommitSHA == "" {
		return fmt.Errorf("commit_sha 为空")
	}
	if len(out.CommittedFiles) == 0 {
		return fmt.Errorf("committed_files 为空")
	}
	for _, p := range out.CommittedFiles {
		if _, ok := generatedPaths[p]; !ok {
			return fmt.Errorf("committed_files 包含未在 generated_files 声明的路径 %q", p)
		}
	}
	// M4.2：Success=true ⇒ FailureCategory == "none"（空值视同 none，为兼容 M4.1
	// 既有容器产出；Claude 新 prompt 被要求显式填 "none"）。
	if out.FailureCategory != "" && out.FailureCategory != FailureCategoryNone {
		return fmt.Errorf("Success=true 但 FailureCategory=%q（必须为 %q）",
			out.FailureCategory, FailureCategoryNone)
	}
	return nil
}

// validateFailureTestGenOutput 校验 Success=false 路径的弱不变量（§4.1 新增）。
//
// 由 parseResult 的 Success=false 分支调用，覆盖：
//   - FailureCategory 必须在 {infrastructure, test_quality, info_insufficient}（空值与
//     "none" / 未枚举值都非法）
//   - InfoSufficient=false ⇔ FailureCategory == info_insufficient（双向一致）
//   - 不强制 BranchName / CommittedFiles / CommitSHA / TestResults 非空（Success=false
//     允许这些字段为空，半成品交付路径仍可建 PR 给用户接管）
//
// 注意：本函数不处理跨 Success 状态的 operation 校验 —— 调用方应先跑
// validateSuccessfulTestGenOutput 得到 operation 级别的保护。
func validateFailureTestGenOutput(out *TestGenOutput) error {
	if out == nil {
		return fmt.Errorf("TestGenOutput 为空")
	}
	if out.Success {
		return fmt.Errorf("validateFailureTestGenOutput 仅适用于 Success=false 路径")
	}
	switch out.FailureCategory {
	case FailureCategoryInfrastructure,
		FailureCategoryTestQuality,
		FailureCategoryInfoInsufficient:
		// ok
	case FailureCategoryNone, "":
		return fmt.Errorf("Success=false 但 failure_category 未填（必须为 infrastructure / test_quality / info_insufficient）")
	default:
		return fmt.Errorf("failure_category=%q 不在枚举内（必须为 infrastructure / test_quality / info_insufficient）",
			out.FailureCategory)
	}
	// 双向一致：InfoSufficient=false ⇔ FailureCategory == info_insufficient
	if !out.InfoSufficient && out.FailureCategory != FailureCategoryInfoInsufficient {
		return fmt.Errorf("InfoSufficient=false 必须配 failure_category=%q，实际 %q",
			FailureCategoryInfoInsufficient, out.FailureCategory)
	}
	if out.InfoSufficient && out.FailureCategory == FailureCategoryInfoInsufficient {
		return fmt.Errorf("failure_category=%q 必须配 InfoSufficient=false",
			FailureCategoryInfoInsufficient)
	}
	return nil
}

// extractJSON 从 Claude 回答文本中提取 JSON 内容（与 fix.extractJSON 同算法独立实现）。
//
// 支持三种布局：
//   - 纯 JSON：` { ... } `
//   - 代码块包裹：```json\n{...}\n```
//   - 自然语言前后文中的 JSON：`解释... { ... } 结尾`
func extractJSON(text string) string {
	text = strings.TrimSpace(text)
	fenceStart := strings.Index(text, "```")
	if fenceStart >= 0 {
		fenced := text[fenceStart:]
		lines := strings.SplitN(fenced, "\n", 2)
		if len(lines) == 2 {
			fenced = lines[1]
		}
		if idx := strings.LastIndex(fenced, "```"); idx >= 0 {
			fenced = fenced[:idx]
		}
		return strings.TrimSpace(fenced)
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

// safeOutput 安全提取 ExecutionResult.Output，nil 时返回空串。
func safeOutput(r *worker.ExecutionResult) string {
	if r == nil {
		return ""
	}
	return r.Output
}
