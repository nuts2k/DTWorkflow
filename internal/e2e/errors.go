package e2e

import "errors"

var (
	ErrE2EDisabled         = errors.New("e2e: 功能已禁用")
	ErrEnvironmentNotFound = errors.New("e2e: 命名环境不存在")
	ErrInvalidRef          = errors.New("e2e: 指定 ref 不存在")
	ErrNoCasesFound        = errors.New("e2e: 指定范围内无用例")
	ErrE2EParseFailure     = errors.New("e2e: 输出解析失败")
	ErrNoE2EModulesFound      = errors.New("e2e: 未发现包含 cases/ 的模块")
	ErrDirNotFound            = errors.New("e2e: 目录不存在")
	ErrE2ETriageParseFailure  = errors.New("e2e: triage 输出解析失败")
)
