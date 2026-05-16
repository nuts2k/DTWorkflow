package notify

import "context"

// EventType 通知事件类型
type EventType string

const (
	EventPRReviewDone        EventType = "pr.review.done"
	EventPRRejected          EventType = "pr.rejected"
	EventIssueAnalysisDone   EventType = "issue.analysis.done"
	EventIssueNeedInfo       EventType = "issue.need_info"
	EventFixIssueDone        EventType = "fix.issue.done"
	EventFixPRCreated        EventType = "fix.pr.created"
	EventE2ETestFailed       EventType = "e2e.test.failed"
	EventPRReviewStarted     EventType = "pr.review.started"
	EventIssueFixStarted     EventType = "issue.fix.started"
	EventIssueAnalyzeStarted EventType = "issue.analyze.started" // M3.4
	EventIssueAnalyzeDone    EventType = "issue.analyze.done"    // M3.4
	EventSystemError         EventType = "system.error"
	// M4.2：测试生成任务的通知事件。
	// Failed 事件的 Severity 由调用方根据 metadata.failure_category 在 Processor 侧决定：
	//   - infrastructure   → Warning
	//   - test_quality     → Info
	//   - info_insufficient → Info
	EventGenTestsStarted EventType = "gen_tests.started"
	EventGenTestsDone    EventType = "gen_tests.done"
	EventGenTestsFailed  EventType = "gen_tests.failed"
	// M5.1：E2E 测试任务通知事件。
	EventE2EStarted EventType = "e2e.started"
	EventE2EDone    EventType = "e2e.done"
	EventE2EFailed  EventType = "e2e.failed"
	// M5.4：E2E 回归分析（triage）通知事件。
	EventE2ETriageStarted EventType = "e2e.triage.started"
	EventE2ETriageDone    EventType = "e2e.triage.done"
	EventE2ETriageFailed  EventType = "e2e.triage.failed"
	// M6.1：迭代式评审修复通知事件。
	EventIterationProgress  EventType = "iteration.progress"
	EventIterationPassed    EventType = "iteration.passed"
	EventIterationExhausted EventType = "iteration.exhausted"
	EventIterationError     EventType = "iteration.error"
	// M6.2：文档驱动编码通知事件。
	EventCodeFromDocStarted EventType = "code_from_doc.started"
	EventCodeFromDocDone    EventType = "code_from_doc.done"
	EventCodeFromDocFailed  EventType = "code_from_doc.failed"
)

// Metadata key 常量，用于 Message.Metadata 的键名，确保生产端和消费端类型安全。
const (
	MetaKeyPRURL         = "pr_url"
	MetaKeyPRTitle       = "pr_title"
	MetaKeyIssueURL      = "issue_url"
	MetaKeyVerdict       = "verdict"
	MetaKeyIssueSummary  = "issue_summary"
	MetaKeyRetryCount    = "retry_count"
	MetaKeyMaxRetry      = "max_retry"
	MetaKeyTaskStatus    = "task_status"
	MetaKeyNotifyTime    = "notify_time"    // 通知发送时间
	MetaKeyDuration      = "duration"       // 任务耗时（仅 succeeded）
	MetaKeyPRNumber      = "pr_number"      // M3.5: 修复任务创建的 PR 编号
	MetaKeyModifiedFiles = "modified_files" // M3.5: 修复任务改动文件数

	// M4.2 gen_tests 事件专用 metadata key。
	MetaKeyModule          = "module"           // 模块路径（整仓模式为 "all"）
	MetaKeyFramework       = "framework"        // 测试框架（junit5 / vitest）
	MetaKeyGeneratedCount  = "generated_count"  // 本次生成的测试文件数
	MetaKeyCommittedCount  = "committed_count"  // 实际提交到分支的文件数
	MetaKeySkippedCount    = "skipped_count"    // 跳过的目标数
	MetaKeyFailureCategory = "failure_category" // gen_tests 失败分类

	// M5.1 E2E 事件专用 metadata key。
	MetaKeyE2EEnv         = "e2e_env"
	MetaKeyE2ETotalCases  = "e2e_total_cases"
	MetaKeyE2EPassedCases = "e2e_passed_cases"
	MetaKeyE2EFailedCases = "e2e_failed_cases"
	MetaKeyE2EErrorCases  = "e2e_error_cases"
	// M5.2 E2E 失败分析报告新增 metadata key。
	MetaKeyE2ESkippedCases  = "e2e_skipped_cases"
	MetaKeyE2EModule        = "e2e_module"
	MetaKeyE2EBaseRef       = "e2e_base_ref"
	MetaKeyE2EFailedList    = "e2e_failed_list"    // JSON: [{"name":"...","category":"...","analysis":"..."}]
	MetaKeyE2ECreatedIssues = "e2e_created_issues" // "42,43"

	// M5.4 E2E 回归分析（triage）专用 metadata key。
	MetaKeyTriageModules        = "triage_modules"         // JSON: [{"name":"...","reason":"..."}]
	MetaKeyTriageSkippedModules = "triage_skipped_modules" // JSON: [{"name":"...","reason":"..."}]
	MetaKeyTriageAnalysis       = "triage_analysis"

	// M6.1 迭代修复专用 metadata key。
	MetaKeyIterationRound       = "iteration_round"
	MetaKeyIterationMaxRounds   = "iteration_max_rounds"
	MetaKeyIterationIssuesFound = "iteration_issues_found"
	MetaKeyIterationIssuesFixed = "iteration_issues_fixed"
	MetaKeyIterationSessionID   = "iteration_session_id"

	// M6.2 code_from_doc 事件专用 metadata key。
	MetaKeyDocPath        = "doc_path"
	MetaKeyBranchName     = "branch_name"
	MetaKeyFilesCreated   = "files_created"
	MetaKeyFilesModified  = "files_modified"
	MetaKeyTestPassed     = "test_passed"
	MetaKeyTestFailed     = "test_failed"
	MetaKeyImplementation = "implementation"
	MetaKeyFailureReason  = "failure_reason"
	MetaKeyMissingInfo    = "missing_info"
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
