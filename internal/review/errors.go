package review

import "errors"

// ErrPRNotOpen 表示 PR 不处于 open 状态，为确定性失败不应重试
var ErrPRNotOpen = errors.New("PR 不处于 open 状态")
