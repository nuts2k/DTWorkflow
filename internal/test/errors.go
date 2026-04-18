// Package test 实现 gen_tests 任务的测试生成 Service 骨架。
//
// M4.1：仅定义 Service/prompt/result/errors 骨架与完整单测；
//   createTestPR 签名冻结，WriteDegraded M4.1 no-op，Processor 暂不注入。
// M4.2：激活容器执行 + PR 创建 + Processor 路由。
package test

import "errors"

// Sentinel 错误分层：
//   - 参数层：用户可修正 → 上层 Processor 据此 SkipRetry
//   - 业务层：Claude 判定或结果失败 → SkipRetry
//   - 解析层：JSON 解析或不变量校验失败 → 允许重试，耗尽后降级

// 参数层 sentinel
var (
	// ErrInvalidModule module 路径非法（绝对路径、含 .. 等）
	ErrInvalidModule = errors.New("module 路径不合法")
	// ErrModuleOutOfScope module 路径不在 test_gen.module_scope 白名单前缀下
	ErrModuleOutOfScope = errors.New("module 路径不在 test_gen.module_scope 白名单前缀下")
	// ErrModuleNotFound module 子路径在仓库中不存在
	ErrModuleNotFound = errors.New("module 子路径不存在于仓库")
	// ErrInvalidRef --ref 指定但 Gitea 无法解析
	ErrInvalidRef = errors.New("ref 不存在")
	// ErrAmbiguousFramework module 下同时存在 pom.xml 与 package.json，无法判定单一框架
	ErrAmbiguousFramework = errors.New("module 下同时存在 pom.xml 与 package.json，请缩窄 --module 或指定 --framework")
	// ErrNoFrameworkDetected 未检测到 JUnit / Vitest 配置
	ErrNoFrameworkDetected = errors.New("未检测到 JUnit / Vitest 配置，请设置 test_gen.test_framework")
)

// 业务层 sentinel
var (
	// ErrInfoInsufficient Claude 判定信息不足，无法生成测试
	ErrInfoInsufficient = errors.New("Claude 判定信息不足，无法生成测试")
	// ErrTestGenFailed 测试生成执行失败（Success=false）
	ErrTestGenFailed = errors.New("测试生成执行失败")
)

// 解析层 sentinel
var (
	// ErrTestGenParseFailure TestGenOutput 解析失败或不变量校验失败
	ErrTestGenParseFailure = errors.New("TestGenOutput 解析失败")
)

// RefKind 标识 ref 类型。与 fix.RefKind 同义但独立，避免跨包 import 消歧混乱。
type RefKind int

// RefKind 取值。
const (
	RefKindUnknown RefKind = iota
	RefKindBranch
	RefKindTag
)

// Framework 测试框架枚举。独立命名空间，避免与 fix 包冲突。
type Framework string

// Framework 取值。
const (
	FrameworkUnknown Framework = ""
	FrameworkJUnit5  Framework = "junit5"
	FrameworkVitest  Framework = "vitest"
)
