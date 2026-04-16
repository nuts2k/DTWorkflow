package fix

import (
	"errors"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// ErrIssueNotOpen Issue 不处于 open 状态时返回此错误，
// Processor 层据此跳过重试（同 review.ErrPRNotOpen 模式）。
var ErrIssueNotOpen = errors.New("Issue 不处于 open 状态")

// ErrMissingIssueRef Issue 未设置关联分支或 tag 时返回此错误，
// Processor 层据此跳过重试（同 ErrIssueNotOpen 模式）。
var ErrMissingIssueRef = errors.New("Issue 未设置关联分支或 tag")

// ErrInvalidIssueRef Issue 关联的分支或 tag 不存在时返回此错误，
// Processor 层据此跳过重试（同 ErrIssueNotOpen 模式）。
var ErrInvalidIssueRef = errors.New("Issue 关联的分支或 tag 不存在")

// ErrInfoInsufficient M3.5: 前序分析报告"信息不足"，阻断 fix_issue。
// Processor 层据此跳过重试（用户补充信息后重新添加标签再触发）。
var ErrInfoInsufficient = errors.New("前序分析报告信息不足，无法修复")

// ErrFixFailed M3.5: Claude 返回 success=false（测试失败、分支未创建等），
// 确定性失败，跳过重试。
var ErrFixFailed = errors.New("修复执行失败")

// ErrFixParseFailure M3.5: FixOutput JSON 解析失败或不变量校验失败。
// Processor 层根据剩余重试次数决定重试或降级评论。
var ErrFixParseFailure = errors.New("修复结果解析失败")

// RefKind 标识 Issue 关联 ref 的类型，PR 创建时需据此决定 Base 字段。
type RefKind int

const (
	RefKindUnknown RefKind = iota
	RefKindBranch          // 分支名，PR Base 直接使用
	RefKindTag             // tag 名，PR Base 需改用仓库默认分支
)

// AnalysisOutput Issue 分析输出 JSON schema
type AnalysisOutput struct {
	InfoSufficient bool       `json:"info_sufficient"`
	MissingInfo    []string   `json:"missing_info,omitempty"`
	RootCause      *RootCause `json:"root_cause,omitempty"`
	Analysis       string     `json:"analysis"`
	FixSuggestion  string     `json:"fix_suggestion,omitempty"`
	Confidence     string     `json:"confidence"`
	RelatedFiles   []string   `json:"related_files,omitempty"`
}

// RootCause 根因定位
type RootCause struct {
	File        string `json:"file"`
	Function    string `json:"function,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
	Description string `json:"description"`
}

// FixOutput M3.5: Issue 自动修复输出 JSON schema
type FixOutput struct {
	Success        bool         `json:"success"`
	InfoSufficient bool         `json:"info_sufficient"`
	MissingInfo    []string     `json:"missing_info,omitempty"`
	BranchName     string       `json:"branch_name,omitempty"`
	CommitSHA      string       `json:"commit_sha,omitempty"`
	ModifiedFiles  []string     `json:"modified_files,omitempty"`
	TestResults    *TestResults `json:"test_results,omitempty"`
	Analysis       string       `json:"analysis,omitempty"`
	FixApproach    string       `json:"fix_approach,omitempty"`
	FailureReason  string       `json:"failure_reason,omitempty"`
}

// TestResults M3.5: 容器内测试运行结果
type TestResults struct {
	Passed    int  `json:"passed"`
	Failed    int  `json:"failed"`
	Skipped   int  `json:"skipped"`
	AllPassed bool `json:"all_passed"`
}

// FixResult 是 Service.Execute 的返回值
type FixResult struct {
	IssueContext   *IssueContext   // 采集到的 Issue 上下文
	RawOutput      string          // Claude CLI 原始 stdout
	ExitCode       int             // worker 原始退出码；非零时表示执行失败，通常不会再进入 JSON 解析
	CLIMeta        *model.CLIMeta  // CLI 执行元数据
	Analysis       *AnalysisOutput // 解析成功的分析结果（可能为 nil）
	Fix            *FixOutput      // M3.5: 修复模式结果（fix_issue）
	ParseError     error           // JSON 解析失败时非 nil
	WritebackError error           // 回写失败时非 nil
	RefKind        RefKind         // M3.5: ref 类型，PR 创建时决定 Base 字段
	PRNumber       int64           // M3.5: 创建的 PR 编号（0 = 未创建）
	PRURL          string          // M3.5: PR 页面链接
}
