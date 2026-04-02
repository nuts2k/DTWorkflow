package fix

import (
	"errors"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// ErrIssueNotOpen Issue 不处于 open 状态时返回此错误，
// Processor 层据此跳过重试（同 review.ErrPRNotOpen 模式）。
var ErrIssueNotOpen = errors.New("Issue 不处于 open 状态")

// FixResult 是 Service.Execute 的返回值
type FixResult struct {
	IssueContext *IssueContext  // 采集到的 Issue 上下文
	RawOutput    string         // M3.2 补充：Claude CLI 原始 stdout
	CLIMeta      *model.CLIMeta // M3.2 补充：CLI 执行元数据（M3.1 始终为 nil）
}
