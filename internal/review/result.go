package review

import "otws19.zicp.vip/kelin/dtworkflow/internal/model"

// CLIResponse 表示 Claude Code CLI --output-format json 的完整输出。
// 字段基于 Claude CLI v2.x 的已知格式，实施前需在容器内验证实际输出并更新。
type CLIResponse struct {
	Type          string  `json:"type"`           // "result" 为正常，其他值（如 "error_during_execution"）视为错误
	Subtype       string  `json:"subtype"`        // "success" / "error"
	CostUSD       float64 `json:"cost_usd"`
	TotalCostUSD  float64 `json:"total_cost_usd"` // CLI 新版本使用此字段
	DurationMs    int64   `json:"duration_ms"`
	DurationAPIMs int64   `json:"duration_api_ms"`
	IsError       bool    `json:"is_error"`
	NumTurns      int     `json:"num_turns"`
	Result        string  `json:"result"` // Claude 的文本回答（字符串）
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
// 同时检查 is_error 标志和 type 字段（type != "result" 视为错误）。
func (r CLIResponse) IsExecutionError() bool {
	return r.IsError || (r.Type != "" && r.Type != "result")
}

// ReviewResult 是 Service.Execute 的返回值
type ReviewResult struct {
	RawOutput      string         // Claude CLI 原始 stdout
	CLIMeta        *model.CLIMeta // 外层 JSON 信封摘要
	Review         *ReviewOutput // 解析成功的内层评审结果（可能为 nil）
	ParseError     error         // JSON 解析失败时非 nil（外层或内层）
	WritebackError error         // 回写 Gitea 失败时非 nil（不影响任务整体状态）
	GiteaReviewID  int64         // 回写成功时的 Gitea Review ID
}

// ReviewOutput 评审输出 JSON schema（M2.3 的解析合约）
type ReviewOutput struct {
	Summary string        `json:"summary"`
	Verdict VerdictType   `json:"verdict"`
	Issues  []ReviewIssue `json:"issues,omitempty"`
}

// VerdictType 评审判定类型
type VerdictType string

const (
	VerdictApprove        VerdictType = "approve"
	VerdictRequestChanges VerdictType = "request_changes"
	VerdictComment        VerdictType = "comment"
)

// ReviewIssue 评审发现的问题
type ReviewIssue struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	EndLine    int    `json:"end_line,omitempty"`
	Severity   string `json:"severity"`             // CRITICAL / ERROR / WARNING / INFO
	Category   string `json:"category"`             // security / logic / style / architecture
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}
