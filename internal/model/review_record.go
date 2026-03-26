package model

import "time"

// ReviewRecord 评审结果持久化记录，与 review_results 表对应。
// 放在 model 包（与 TaskRecord 并列），避免 store->review 循环依赖。
type ReviewRecord struct {
	ID             string    `json:"id"`
	TaskID         string    `json:"task_id"`
	RepoFullName   string    `json:"repo_full_name"`
	PRNumber       int64     `json:"pr_number"`
	HeadSHA        string    `json:"head_sha"`
	Verdict        string    `json:"verdict"`
	Summary        string    `json:"summary"`
	IssuesJSON     string    `json:"issues_json"`
	IssueCount     int       `json:"issue_count"`
	CriticalCount  int       `json:"critical_count"`
	ErrorCount     int       `json:"error_count"`
	WarningCount   int       `json:"warning_count"`
	InfoCount      int       `json:"info_count"`
	CostUSD        float64   `json:"cost_usd"`
	DurationMs     int64     `json:"duration_ms"`
	GiteaReviewID  int64     `json:"gitea_review_id"`
	ParseFailed    bool      `json:"parse_failed"`
	WritebackError string    `json:"writeback_error"`
	CreatedAt      time.Time `json:"created_at"`
}
