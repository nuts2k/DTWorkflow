package notify

import "context"

// EventType 通知事件类型
type EventType string

const (
	EventPRReviewDone      EventType = "pr.review.done"
	EventPRRejected        EventType = "pr.rejected"
	EventIssueAnalysisDone EventType = "issue.analysis.done"
	EventIssueNeedInfo     EventType = "issue.need_info"
	EventFixPRCreated      EventType = "fix.pr.created"
	EventE2ETestFailed     EventType = "e2e.test.failed"
	EventSystemError       EventType = "system.error"
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
