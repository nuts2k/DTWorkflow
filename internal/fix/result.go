package fix

import "errors"

// ErrIssueNotOpen Issue 不处于 open 状态时返回此错误，
// Processor 层据此跳过重试（同 review.ErrPRNotOpen 模式）。
var ErrIssueNotOpen = errors.New("Issue 不处于 open 状态")

// FixResult 是 Service.Execute 的返回值
type FixResult struct {
	IssueContext *IssueContext // 采集到的 Issue 上下文
	RawOutput string   // M3.2 补充：Claude CLI 原始 stdout
	CLIMeta   *CLIMeta // M3.2 补充：CLI 执行元数据（M3.1 始终为 nil）
}

// CLIMeta 执行元数据（独立于 review 包，避免 fix->review 的包依赖）
type CLIMeta struct {
	CostUSD    float64
	DurationMs int64
	IsError    bool
	NumTurns   int
	SessionID  string
}
