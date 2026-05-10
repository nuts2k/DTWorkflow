package e2e

import "errors"

var (
	ErrE2EDisabled         = errors.New("e2e: 功能已禁用")
	ErrEnvironmentNotFound = errors.New("e2e: 命名环境不存在")
	ErrInvalidRef          = errors.New("e2e: 指定 ref 不存在")
	ErrNoCasesFound        = errors.New("e2e: 指定范围内无用例")
	ErrE2EParseFailure     = errors.New("e2e: 输出解析失败")
)
