package fix

import (
	"errors"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// ErrIssueNotOpen Issue 不处于 open 状态时返回此错误，
// Processor 层据此跳过重试（同 review.ErrPRNotOpen 模式）。
var ErrIssueNotOpen = errors.New("Issue 不处于 open 状态")

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

// FixResult 是 Service.Execute 的返回值
type FixResult struct {
	IssueContext   *IssueContext   // 采集到的 Issue 上下文
	RawOutput      string          // Claude CLI 原始 stdout
	ExitCode       int             // worker 原始退出码；非零时表示执行失败，通常不会再进入 JSON 解析
	CLIMeta        *model.CLIMeta  // CLI 执行元数据
	Analysis       *AnalysisOutput // 解析成功的分析结果（可能为 nil）
	ParseError     error           // JSON 解析失败时非 nil
	WritebackError error           // 回写失败时非 nil
}
