package model

import "time"

// TaskType 任务类型枚举
type TaskType string

const (
	TaskTypeReviewPR       TaskType = "review_pr"
	TaskTypeAnalyzeIssue   TaskType = "analyze_issue" // M3.4: 只读分析（从 fix_issue 拆分）
	TaskTypeFixIssue       TaskType = "fix_issue"     // M3.4: 语义修正为真正的修复
	TaskTypeGenTests       TaskType = "gen_tests"
	TaskTypeGenDailyReport TaskType = "gen_daily_report"
	TaskTypeRunE2E         TaskType = "run_e2e"
	TaskTypeTriageE2E      TaskType = "triage_e2e"
	TaskTypeFixReview      TaskType = "fix_review"
)

// TaskPriority 任务优先级（asynq 使用整数，越小越高）
type TaskPriority int

const (
	PriorityCritical TaskPriority = 1 // 安全问题等紧急任务
	PriorityHigh     TaskPriority = 3 // PR 评审（用户在等）
	PriorityNormal   TaskPriority = 5 // Issue 修复
	PriorityLow      TaskPriority = 7 // 测试生成
)

// TaskStatus 任务状态
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"   // 已创建（SQLite），尚未入队（Redis）
	TaskStatusQueued    TaskStatus = "queued"    // 已入 asynq 队列
	TaskStatusRunning   TaskStatus = "running"   // Worker 正在执行
	TaskStatusSucceeded TaskStatus = "succeeded" // 执行成功
	TaskStatusFailed    TaskStatus = "failed"    // 执行失败（重试耗尽）
	TaskStatusRetrying  TaskStatus = "retrying"  // 等待重试
	TaskStatusCancelled TaskStatus = "cancelled" // 手动取消
)

// IsValid 检查任务类型是否为已知值
func (t TaskType) IsValid() bool {
	switch t {
	case TaskTypeReviewPR, TaskTypeAnalyzeIssue, TaskTypeFixIssue, TaskTypeGenTests, TaskTypeGenDailyReport, TaskTypeRunE2E, TaskTypeTriageE2E, TaskTypeFixReview:
		return true
	}
	return false
}

// IsValid 检查任务状态是否为已知值
func (s TaskStatus) IsValid() bool {
	switch s {
	case TaskStatusPending, TaskStatusQueued, TaskStatusRunning,
		TaskStatusSucceeded, TaskStatusFailed, TaskStatusRetrying, TaskStatusCancelled:
		return true
	}
	return false
}

// IsValid 检查任务优先级是否为已知值
func (p TaskPriority) IsValid() bool {
	switch p {
	case PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow:
		return true
	}
	return false
}

// TaskPayload 任务定位符（非完整快照）
// Processor 执行时通过 Gitea API 获取最新数据
type TaskPayload struct {
	// TaskType 与 TaskRecord.TaskType 存在设计上的冗余：
	// TaskRecord.TaskType 用于 SQLite 列查询和索引过滤；
	// TaskPayload.TaskType 随 JSON 序列化传递给 asynq Worker，使 Processor 无需反查数据库即可路由任务。
	TaskType   TaskType `json:"task_type"`
	TaskID     string   `json:"-"`                     // 运行时由 Processor 从 TaskRecord.ID 注入，不序列化
	DeliveryID string   `json:"delivery_id,omitempty"` // Webhook delivery ID，用于幂等

	// 仓库定位（所有任务类型共享）
	RepoOwner    string `json:"repo_owner"`
	RepoName     string `json:"repo_name"`
	RepoFullName string `json:"repo_full_name"`
	CloneURL     string `json:"clone_url"`

	// PR 评审定位
	PRNumber int64  `json:"pr_number,omitempty"`
	PRTitle  string `json:"pr_title,omitempty"`
	BaseRef  string `json:"base_ref,omitempty"`
	HeadRef  string `json:"head_ref,omitempty"`
	BaseSHA  string `json:"base_sha,omitempty"`
	HeadSHA  string `json:"head_sha,omitempty"`
	// MergeCommitSHA 固定 PR merged 事件对应的目标提交，避免队列延迟后误分析更新的 base 分支。
	MergeCommitSHA string `json:"merge_commit_sha,omitempty"`

	// Issue 修复定位
	IssueNumber int64  `json:"issue_number,omitempty"`
	IssueTitle  string `json:"issue_title,omitempty"`
	IssueRef    string `json:"issue_ref,omitempty"`

	// 测试生成定位
	Module       string   `json:"module,omitempty"`
	Framework    string   `json:"framework,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"` // 变更驱动：触发变更的源码文件列表

	// E2E 测试定位
	Environment     string `json:"environment,omitempty"`       // 命名环境（staging / dev 等）
	CaseName        string `json:"case_name,omitempty"`         // 单用例指定（需配合 Module）
	BaseURLOverride string `json:"base_url_override,omitempty"` // 临时覆盖 base_url

	// 额外环境变量（key=value 格式），由特定 Service 在入队前注入
	ExtraEnvs []string `json:"extra_envs,omitempty"`

	// M2.4 重新评审
	CreatedAt       time.Time `json:"created_at,omitempty"`        // 任务创建时间（staleness check 基准）
	SupersededCount int       `json:"superseded_count,omitempty"`  // 替代的旧任务数量
	PreviousHeadSHA string    `json:"previous_head_sha,omitempty"` // 上一次评审的 head SHA

	// M6.1 迭代修复
	SessionID     int64  `json:"session_id,omitempty"`
	RoundNumber   int    `json:"round_number,omitempty"`
	ReviewIssues  string `json:"review_issues,omitempty"`  // JSON: []review.ReviewIssue
	PreviousFixes string `json:"previous_fixes,omitempty"` // JSON: []iterate.FixSummary
}

// TaskRecord 持久化到 SQLite 的任务记录
type TaskRecord struct {
	ID           string       `json:"id"`
	AsynqID      string       `json:"asynq_id"`
	TaskType     TaskType     `json:"task_type"`
	Status       TaskStatus   `json:"status"`
	Priority     TaskPriority `json:"priority"`
	Payload      TaskPayload  `json:"payload"`
	RepoFullName string       `json:"repo_full_name"`      // 冗余列，便于过滤查询
	PRNumber     int64        `json:"pr_number,omitempty"` // 冗余列，便于按 PR 查询
	Result       string       `json:"result,omitempty"`
	Error        string       `json:"error,omitempty"`
	RetryCount   int          `json:"retry_count"`
	MaxRetry     int          `json:"max_retry"`
	WorkerID     string       `json:"worker_id,omitempty"`
	DeliveryID   string       `json:"delivery_id,omitempty"`
	TriggeredBy  string       `json:"triggered_by,omitempty"` // "webhook" / "manual:{identity}"
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	StartedAt    *time.Time   `json:"started_at,omitempty"`
	CompletedAt  *time.Time   `json:"completed_at,omitempty"`
}
