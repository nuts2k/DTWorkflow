package review

import "errors"

// ErrPRNotOpen 表示 PR 不处于 open 状态，为确定性失败不应重试
var ErrPRNotOpen = errors.New("PR 不处于 open 状态")

// ErrParseFailure 表示 Claude 返回内容无法解析为结构化评审结果。
// 属于暂时性失败（幻觉/格式偶发），应重试；重试耗尽后由 processor 触发降级回写。
var ErrParseFailure = errors.New("评审结果解析失败")
