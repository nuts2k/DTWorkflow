package test

import (
	"fmt"
	"strings"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

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
