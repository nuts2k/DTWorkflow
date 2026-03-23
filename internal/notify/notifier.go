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
	Owner  string
	Repo   string
	Number int64
	IsPR   bool
}

// Message 通知消息体
type Message struct {
	EventType EventType
	Severity  Severity
	Target    Target
	Title     string
	Body      string
	Metadata  map[string]string
}

// Notifier 通知渠道接口
type Notifier interface {
	Name() string
	Send(ctx context.Context, msg Message) error
}
