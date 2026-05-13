package iterate

// FixReviewOutput 容器内 Claude Code 修复结果的结构化 JSON。
type FixReviewOutput struct {
	Fixes   []FixItem `json:"fixes"`
	Summary string    `json:"summary"`
}

// FixItem 单个问题的修复结果。
type FixItem struct {
	File         string        `json:"file"`
	Line         int           `json:"line"`
	IssueRef     string        `json:"issue_ref"`
	Severity     string        `json:"severity"`
	Action       string        `json:"action"` // modified / skipped / alternative_chosen
	What         string        `json:"what"`
	Why          string        `json:"why"`
	Alternatives []Alternative `json:"alternatives,omitempty"`
}

// Alternative 备选方案。
type Alternative struct {
	Description string `json:"description"`
	Pros        string `json:"pros"`
	Cons        string `json:"cons"`
	WhyNot      string `json:"why_not"`
}

// FixSummary 前几轮修复的精简上下文，传递给后续轮次的 prompt。
type FixSummary struct {
	Round       int    `json:"round"`
	IssuesFixed int    `json:"issues_fixed"`
	Summary     string `json:"summary"`
}
