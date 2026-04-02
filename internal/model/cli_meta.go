package model

// CLIMeta 从 Claude CLI JSON 信封提取的执行元数据。
// fix 和 review 包共享此类型，避免重复定义。
type CLIMeta struct {
	CostUSD    float64
	DurationMs int64
	IsError    bool
	NumTurns   int
	SessionID  string
}
