package review

// CLIResponse 表示 Claude Code CLI --output-format json 的完整输出。
// 字段基于 Claude CLI v2.x 的已知格式，实施前需在容器内验证实际输出并更新。
type CLIResponse struct {
	Type          string  `json:"type"`           // "result"
	Subtype       string  `json:"subtype"`        // "success" / "error"
	CostUSD       float64 `json:"cost_usd"`
	DurationMs    int64   `json:"duration_ms"`
	DurationAPIMs int64   `json:"duration_api_ms"`
	IsError       bool    `json:"is_error"`
	NumTurns      int     `json:"num_turns"`
	Result        string  `json:"result"` // Claude 的文本回答（字符串）
	SessionID     string  `json:"session_id"`
}

// CLIMeta 从 CLIResponse 提取的执行元数据
type CLIMeta struct {
	CostUSD    float64
	DurationMs int64
	IsError    bool
	NumTurns   int
	SessionID  string
}

// ReviewResult 是 Service.Execute 的返回值
type ReviewResult struct {
	RawOutput  string        // Claude CLI 原始 stdout
	CLIMeta    *CLIMeta      // 外层 JSON 信封摘要
	Review     *ReviewOutput // 解析成功的内层评审结果（可能为 nil）
	ParseError error         // JSON 解析失败时非 nil（外层或内层）
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
