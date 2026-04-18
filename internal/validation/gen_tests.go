// Package validation 集中承载跨层（API handler / 服务端 CLI / dtw 瘦客户端）共用的
// 轻量参数校验函数，避免同样的黑名单、白名单规则在三处被复制。
//
// 设计原则：
//   - 只做"最小安全校验"（快速拒绝明显非法输入），不替代下游 Service 层的完整校验。
//   - 返回的 error 消息不含字段名前缀；调用方按上下文自行拼接（例如 "module " + err.Error()
//     或 fmt.Errorf("--module %w", err)），保持日志与 CLI / API 错误风格一致。
package validation

import (
	"fmt"
	"strings"
)

// GenTestsModule 校验 gen_tests 任务的 module 字段。
//
// 规则：
//   - 空字符串合法（表示整仓生成）
//   - 不允许以 "/" 开头（绝对路径）
//   - 不允许包含 ".." 段（防止越出仓库根；兼容 Windows 风格反斜杠作为分隔符）
//
// 注意：这是"最小安全校验"。test.Service.validateModule 还会基于 ModuleScope 白名单
// 与 path.Clean 归一化做完整校验；这里的目的是让 API / CLI 入口层以最低成本挡住
// 构造攻击，并给用户一个更快的反馈。
func GenTestsModule(module string) error {
	if module == "" {
		return nil
	}
	if strings.HasPrefix(module, "/") {
		return fmt.Errorf("不能为绝对路径: %q", module)
	}
	// 兼容 Windows 风格反斜杠：先归一化再看是否包含 `..` 段
	normalized := strings.ReplaceAll(module, `\`, "/")
	if normalized == ".." ||
		strings.HasPrefix(normalized, "../") ||
		strings.HasSuffix(normalized, "/..") ||
		strings.Contains(normalized, "/../") {
		return fmt.Errorf("不能包含 ..: %q", module)
	}
	return nil
}

// GenTestsFramework 校验 gen_tests 任务的 framework 字段。
//
// 合法取值：空字符串（留给服务端 resolveFramework 自动检测）、"junit5"、"vitest"。
// 对大小写敏感——任何变种（JUnit5、Vitest、vitEst）均视为非法，避免下游 mvn/npm 命令
// 因配置不一致而失败。
func GenTestsFramework(framework string) error {
	switch framework {
	case "", "junit5", "vitest":
		return nil
	default:
		return fmt.Errorf(`合法值为 "junit5" / "vitest"，当前值: %q`, framework)
	}
}
