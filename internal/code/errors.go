package code

import "errors"

var (
	// ErrInfoInsufficient 设计文档信息不足以完成实现（确定性失败，SkipRetry）。
	ErrInfoInsufficient = errors.New("code_from_doc: 设计文档信息不足")

	// ErrTestFailure 编码完成但测试未通过（仍创建 PR 保留成果）。
	ErrTestFailure = errors.New("code_from_doc: 测试未通过")

	// ErrCodeFromDocParseFailure 容器输出 JSON 解析失败（允许 asynq 重试）。
	ErrCodeFromDocParseFailure = errors.New("code_from_doc: 输出解析失败")

	// ErrCodeFromDocDisabled 仓库已禁用 code_from_doc（确定性失败，SkipRetry）。
	ErrCodeFromDocDisabled = errors.New("code_from_doc: 仓库已禁用")
)
