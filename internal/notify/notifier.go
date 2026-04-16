package notify

import "context"

// EventType 通知事件类型
type EventType string

const (
	EventPRReviewDone      EventType = "pr.review.done"
	EventPRRejected        EventType = "pr.rejected"
	EventIssueAnalysisDone EventType = "issue.analysis.done"
	EventIssueNeedInfo     EventType = "issue.need_info"
	EventFixIssueDone      EventType = "fix.issue.done"
	EventFixPRCreated      EventType = "fix.pr.created"
	EventE2ETestFailed     EventType = "e2e.test.failed"
	EventPRReviewStarted EventType = "pr.review.started"
	EventIssueFixStarted    EventType = "issue.fix.started"
	EventIssueAnalyzeStarted EventType = "issue.analyze.started"  // M3.4
	EventIssueAnalyzeDone    EventType = "issue.analyze.done"     // M3.4
	EventSystemError         EventType = "system.error"
)

// Metadata key 常量，用于 Message.Metadata 的键名，确保生产端和消费端类型安全。
const (
	MetaKeyPRURL        = "pr_url"
	MetaKeyPRTitle      = "pr_title"
	MetaKeyIssueURL     = "issue_url"
	MetaKeyVerdict      = "verdict"
	MetaKeyIssueSummary = "issue_summary"
	MetaKeyRetryCount   = "retry_count"
	MetaKeyMaxRetry     = "max_retry"
	MetaKeyTaskStatus   = "task_status"
	MetaKeyNotifyTime   = "notify_time" // 通知发送时间
	MetaKeyDuration     = "duration"    // 任务耗时（仅 succeeded）
)

// Severity 通知紧急程度
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Target 通知目标（Issue 或 PR）
type Target struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int64  `json:"number"`
	IsPR   bool   `json:"is_pr"` // true=PR, false=Issue；当前 GiteaNotifier 不使用此字段（Gitea Issue/PR 共用评论 API），保留供未来扩展
}

// Message 表示一条通知消息。
// 基础验证（EventType 非空、Target.Owner/Repo 非空）由 Router.Send 在入口处检查，
// 渠道特定验证（如 Gitea 的 Number > 0）由各 Notifier 实现负责。
type Message struct {
	EventType EventType
	Severity  Severity
	Target    Target
	Title     string
	Body      string
	// Metadata 扩展元数据，供后续通知渠道使用（如企微/钉钉的额外参数）。
	// 当前 GiteaNotifier 不使用此字段。
	Metadata map[string]string
}

// Notifier 通知渠道接口
type Notifier interface {
	Name() string
	Send(ctx context.Context, msg Message) error
}
