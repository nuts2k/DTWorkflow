package iterate

import "errors"

var (
	// ErrIterateDisabled 迭代功能未启用。
	ErrIterateDisabled = errors.New("iterate: 迭代功能未启用")

	// ErrSessionExhausted 迭代达到上限。
	ErrSessionExhausted = errors.New("iterate: 达到迭代上限")

	// ErrFixReviewParseFailure 修复结果 JSON 解析失败。
	ErrFixReviewParseFailure = errors.New("iterate: 修复结果解析失败")

	// ErrNoChanges 修复未产生实际变更。
	ErrNoChanges = errors.New("iterate: 修复未产生实际变更")

	// ErrConsecutiveZeroFixes 连续两轮零修复，提前终止。
	ErrConsecutiveZeroFixes = errors.New("iterate: 连续两轮零修复")
)
