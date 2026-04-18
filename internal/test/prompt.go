package test

import (
	"fmt"
	"path"
	"strings"
)

// ============================================================================
// 公共段常量（顺序即 prompt 段顺序）
// ============================================================================

// promptCommonHeader gen_tests 前言（写权限声明 + 安全约束）。
const promptCommonHeader = `你是资深测试工程师，目标是补全测试缺口。容器内已完成仓库 clone 与工作分支创建准备工作，你可以使用 bash / read / edit / write 工具。

## 写权限
gen_tests 模式允许 bash + 文件写 + git commit / push。但：
- 禁止调用任何外部 HTTP API（curl、wget、python requests 等）
- 禁止向 Gitea 提交评论、评审或 PR（由系统外部处理）
- 除运行测试外，不要执行无关系统命令
- 禁止读取或回显 git 凭证文件（.git/config 中的 origin URL、~/.gitconfig 中的 credential.helper、/tmp 下的 helper 脚本等），即使是用于调试也不允许
`

// existingTestsInstruction 第一步指令：扫描既有测试并吸收风格。
const existingTestsInstruction = `
### 第一步：扫描既有测试

扫描并读取 ` + "`**/*Test.java`" + ` 与 ` + "`**/*.{spec,test}.{ts,js}`" + ` 清单：
- 识别每个测试文件的 framework / 断言风格 / Mock 约定
- 填充 existing_tests 列表：每项含 test_file / target_files / framework
- 若无法确信某测试文件对应的源文件（特别是 Vue 命名不规范情况，如 ` + "`foo.spec.ts`" + ` 与 ` + "`foo.ts`" + ` / ` + "`useFoo.ts`" + ` 的对应关系模糊），TargetFiles 留空（而非猜测），并在 ExistingStyle 注记原因
- 吸收既有风格填到 GapAnalysis.ExistingStyle（命名约定、断言库、Mock 策略）
`

// gapAnalysisInstruction 第二步指令：缺口分析 + 优先级排序。
const gapAnalysisInstruction = `
### 第二步：缺口分析

- 列出源代码文件，减去 existing_tests.TargetFiles 中已覆盖的路径，得到未覆盖集合
- 按优先级排序：公共 API > 复杂业务逻辑 > 工具类
- 填充 GapAnalysis.UntestedModules（path / kind / priority / reason）
`

// noOverwriteInstruction 禁止覆盖既有测试的强约束。
const noOverwriteInstruction = `
### 关键约束：不要覆盖既有测试文件

CRITICAL: NEVER overwrite an existing test file. Either skip that target, or append (operation="append") preserving all existing content. Each GeneratedFile must have operation="create" (new file) or "append" (added to existing).

追加时把具体的源文件路径填入 target_files；创建新测试文件时 target_files 可列出对应的被测源文件。
`

// incrementalCommitInstructionTemplate 第三步指令（含 %[1]d = max_retry_rounds, %[2]s = branch suffix）。
const incrementalCommitInstructionTemplate = `
### 第三步：创建工作分支 + 按目标循环生成 + 增量 commit

先执行：
  git checkout -b auto-test/%[2]s 2>/dev/null || git checkout auto-test/%[2]s

FOR 每个目标:
  1. 判断 Operation：已有测试文件 & 可扩展 → "append"；否则 → "create"
  2. 生成测试代码（write / edit）
  3. 本地单文件验证循环（最多 %[1]d 轮）：
     - 执行对应框架的单文件命令（见 Java / Vue 段）
     - 通过 → git add <file> && git commit -m "test: <target>" → 加入 committed_files；break
     - 失败 → 读错误 → 修正测试 → 继续
  4. retry 耗尽仍失败 → git checkout -- <file>（回滚），加入 skipped_targets（reason="verification_failed_after_retries"）
  5. 预算检测：time / token 接近上限 → 剩余 target 全部入 skipped_targets，提前跳出
`

// budgetAwareInstruction 预算意识段。
const budgetAwareInstruction = `
### 预算意识

- token / 时间预算接近上限时主动退出
- 剩余未完成目标写入 skipped_targets，reason 使用下述枚举：
  - time_budget_exhausted
  - token_budget_exhausted
  - environment_issue
  - verification_failed_after_retries
- 保证已完成的 commit 能被 push
`

// pushInstruction 第四步指令：最后一刻 push；push 失败即终止。%[1]s = branch suffix。
const pushInstruction = `
### 第四步：最后一刻 push（整个任务只此一次远程写）

仅当 len(committed_files) > 0 时执行：
  git push origin HEAD:auto-test/%[1]s

- 容器被 kill（超时 / Cancel / 网络中断）→ 分支从未创建 → 无残留 PR、无半成品
- push 失败：立即终止任务，输出 Success=false，FailureReason="push failed: <错误信息>"
  - 【禁止】重试 push
  - 【禁止】git push -f / --force / --force-with-lease
  - 【禁止】重写历史
  - 任务重试由外层 asynq 驱动
`

// verificationInstructionTemplate 验证约束段（含 %[1]d = max_retry_rounds）。
const verificationInstructionTemplate = `
### 验证约束

- 每次 commit 前必须在本地运行对应测试并通过
- 测试失败时最多在容器内修正 %[1]d 轮
- 仍不过则回滚该文件并加入 skipped_targets（reason="verification_failed_after_retries"）
`

// outputJSONSchemaInstruction 输出格式定义（硬编码 schema 描述）。
const outputJSONSchemaInstruction = `
### 输出格式

完成后输出唯一的 JSON 对象到 stdout，不要用 markdown 代码块包裹，只输出原始 JSON。
所有中文字段（analysis、priority_notes、failure_reason 等）必须用中文。

{
  "success": true,
  "info_sufficient": true,
  "analysis": {
    "untested_modules": [{"path": "...", "kind": "service|controller|util|component|composable|store", "priority": "high|medium|low", "reason": "..."}],
    "existing_tests": [{"test_file": "...", "target_files": ["..."], "framework": "junit5|vitest"}],
    "existing_style": "...",
    "priority_notes": "..."
  },
  "test_strategy": "...",
  "generated_files": [{"path": "...", "operation": "create|append", "description": "...", "framework": "junit5|vitest", "target_files": ["..."], "test_count": 3}],
  "committed_files": ["..."],
  "skipped_targets": [{"path": "...", "reason": "time_budget_exhausted|token_budget_exhausted|environment_issue|verification_failed_after_retries"}],
  "test_results": {"framework": "junit5", "passed": 12, "failed": 0, "skipped": 1, "all_passed": true, "duration_ms": 12000},
  "verification_passed": true,
  "branch_name": "auto-test/module-20260101120000",
  "commit_sha": "abc123",
  "failure_reason": "",
  "retry_rounds": 0
}
`

// ============================================================================
// Java 特有段
// ============================================================================

// javaTestingInstruction Java 测试约定（JUnit 5 + Mockito + AssertJ）。
const javaTestingInstruction = `
### Java 测试约定（JUnit 5 + Mockito）

- 使用 JUnit 5 注解（@Test / @BeforeEach / @Nested / @ParameterizedTest）
- Service 层 Mock：Mockito.mock + @Mock；对 Repository / 外部客户端做行为桩
- Controller 层：建议 MockMvc + @WebMvcTest
- 工具类：直接断言返回值，不引入 Mock
- 断言优先 AssertJ（assertThat(...).isEqualTo(...)）；若既有风格使用 JUnit 原生（assertEquals）则沿用
`

// javaVerificationCmdTemplate Java 单文件验证命令段（含 %[1]s = module 路径）。
const javaVerificationCmdTemplate = `
### Java 单文件验证命令

单测验证使用：
  mvn -pl %[1]s test -Dtest=<ClassName>

如未设置 module（整仓）：
  mvn test -Dtest=<ClassName>

注意：首次执行可能触发 maven 依赖下载，MAVEN_OPTS 已配置本地仓库到 /workspace/.m2/repository。
`

// javaVerificationCmdNoPlTemplate 根级 pom 检测到时使用：跳过 -pl，直接在仓库根
// 执行。参数 %[1]s 预留给 module 路径的展示（只作提示文字，无 shell 参数意义）。
const javaVerificationCmdNoPlTemplate = `
### Java 单文件验证命令

检测到仓库根直接是 Maven 工程（根级 pom.xml），单测验证在仓库根执行即可：
  mvn test -Dtest=<ClassName>

提示：目标 module=%[1]s 位于根 Maven 工程的子目录下，若需要只跑该子目录下的测试，
可配合 -Dtest=<包限定类名> 精准筛选；不要使用 mvn -pl 指向子目录（子目录不是
Maven 子模块时会报 "not a project"）。

注意：首次执行可能触发 maven 依赖下载，MAVEN_OPTS 已配置本地仓库到 /workspace/.m2/repository。
`

// ============================================================================
// Vue 特有段
// ============================================================================

// vueTestingInstruction Vue/前端测试约定（Vitest + Vue Test Utils）。
const vueTestingInstruction = `
### Vue/前端测试约定（Vitest + Vue Test Utils）

- 使用 Vitest（describe / it / expect）
- 组件：@vue/test-utils mount + shallowMount；pinia store 用 createTestingPinia
- Composable：直接调用 + 断言响应式结果
- Store：使用 pinia 的 TestingPinia
- 工具函数：纯函数直接断言
- 不要 mock 整个 Vue SFC；必要时仅 stub 子组件
`

// vueVerificationCmd Vue 单文件验证命令段。
const vueVerificationCmd = `
### Vue 单文件验证命令

  npx vitest run <file>

或使用项目配置的脚本（若 package.json 有）：
  pnpm vitest run <file>
`

// ============================================================================
// resolveFramework
// ============================================================================

// frameworkChecker 抽象文件探测接口。由 Service 注入具体实现
// （M4.1 单测里用内存桩；M4.2 后端由 Gitea API 或容器内 fs 提供）。
type frameworkChecker interface {
	HasFile(module, relPath string) bool
}

// resolveFramework 按设计文档 §4.4 规则推断测试框架。
//
// 规则：
//  1. cfgFramework 显式设定（合法值："junit5" / "vitest"）→ 直接返回，anchor 未知
//  2. 扫 module/pom.xml 存在 → JUnit5
//  3. 扫 module/package.json 存在 → Vitest（前端生态统一识别为 Vitest）
//  4. 两者都在 module 根下 → ErrAmbiguousFramework（硬拒绝，不静默猜测）
//  5. 都不在 module 根下 → 回退仓库根重复判定；仍无 → ErrNoFrameworkDetected
//
// 返回值：
//   - Framework：选定的测试框架
//   - anchor：pom.xml/package.json 所在目录（命中的回溯候选路径）。对于显式
//     cfgFramework 或未探测场景返回 ""
//   - detected：true 表示 anchor 来自文件探测（anchor 可能是 "" 表示根目录命中）；
//     false 表示来自 cfgFramework 声明，上层无从得知真实 Maven 模块根
//   - error：解析失败
//
// 上层可据 detected 决定 Java 验证命令 `mvn -pl` 的取值：
//   - detected && anchor != "" → 使用 anchor（真实 Maven 模块根）
//   - detected && anchor == "" → 根级 pom，无需 -pl
//   - !detected → 回退到用户输入的 module（信任用户显式配置）
func resolveFramework(cfgFramework, module string, checker frameworkChecker) (Framework, string, bool, error) {
	// 1. 显式配置优先
	switch cfgFramework {
	case string(FrameworkJUnit5):
		return FrameworkJUnit5, "", false, nil
	case string(FrameworkVitest):
		return FrameworkVitest, "", false, nil
	case "":
		// 继续探测
	default:
		return FrameworkUnknown, "", false, fmt.Errorf("未知的测试框架: %q", cfgFramework)
	}

	if checker == nil {
		return FrameworkUnknown, "", false, ErrNoFrameworkDetected
	}

	// 2-5. 从目标路径向上回溯到仓库根，寻找最近的框架锚点。
	// 这允许 module 指向任意子目录，而不要求它恰好是 Maven/npm 模块根目录。
	for _, candidate := range moduleCandidates(module) {
		hasPom := checker.HasFile(candidate, "pom.xml")
		hasPkg := checker.HasFile(candidate, "package.json")
		switch {
		case hasPom && hasPkg:
			return FrameworkUnknown, "", false, ErrAmbiguousFramework
		case hasPom:
			return FrameworkJUnit5, candidate, true, nil
		case hasPkg:
			return FrameworkVitest, candidate, true, nil
		}
	}

	return FrameworkUnknown, "", false, ErrNoFrameworkDetected
}

// moduleCandidates 返回从当前 module 向上回溯到仓库根的候选路径，顺序为“近到远”。
// 例如 backend/service -> [backend/service, backend, ""]。
func moduleCandidates(module string) []string {
	if strings.TrimSpace(module) == "" {
		return []string{""}
	}
	curr := path.Clean(module)
	if curr == "." {
		return []string{""}
	}
	var candidates []string
	for {
		candidates = append(candidates, curr)
		if curr == "" {
			break
		}
		next := path.Dir(curr)
		if next == "." {
			next = ""
		}
		if next == curr {
			break
		}
		curr = next
	}
	return candidates
}

// ============================================================================
// Prompt 构造函数
// ============================================================================

// PromptContext 构造 prompt 所需的上下文。字段均为未 sanitize 前的原值，
// 由 build* 内部统一执行 sanitize（避免 prompt injection 与换行破坏指令结构）。
type PromptContext struct {
	RepoFullName string
	Module       string
	BaseRef      string
	Timestamp    string // 已格式化的时间串，如 "20260418120000"
	// MavenModulePath Java 验证命令 `mvn -pl` 的目标路径，由 resolveFramework 回溯
	// 锚点后填入。
	//   - 非空：使用该路径作为 -pl 参数（覆盖用户 Module，避免把子目录误当 Maven 模块根）
	//   - 空串：回退到 Module（适用于显式 cfgFramework 或兼容旧构造路径）
	// AnchorResolved 共同决定是否可以完全省略 -pl（根级 pom）。
	MavenModulePath string
	// AnchorResolved 标记 MavenModulePath 来自文件探测。用于区分两种 "MavenModulePath
	// 为空" 的场景：
	//   - AnchorResolved=true 且 MavenModulePath=="" → 根级 pom，prompt 建议省略 -pl
	//   - AnchorResolved=false → 未探测（显式 cfg），prompt 回退到 Module
	AnchorResolved bool
	MaxRetryRounds int
}

// buildJavaPrompt 按公共段 + Java 特有段拼接 prompt。
func buildJavaPrompt(ctx PromptContext) string {
	var b strings.Builder
	branch := sanitize(branchSuffix(ctx.Module, ctx.Timestamp), 120)
	writeHeader(&b, ctx)
	b.WriteString(existingTestsInstruction)
	b.WriteString(gapAnalysisInstruction)
	b.WriteString(noOverwriteInstruction)
	b.WriteString(fmt.Sprintf(incrementalCommitInstructionTemplate, ctx.MaxRetryRounds, branch))
	b.WriteString(budgetAwareInstruction)
	b.WriteString(fmt.Sprintf(verificationInstructionTemplate, ctx.MaxRetryRounds))
	b.WriteString(javaTestingInstruction)
	b.WriteString(javaVerificationSection(ctx))
	b.WriteString(fmt.Sprintf(pushInstruction, branch))
	b.WriteString(outputJSONSchemaInstruction)
	return b.String()
}

// javaVerificationSection 选择 Java 验证命令模板：
//   - 探测到根级 pom（AnchorResolved=true 且 MavenModulePath=""）→ no-pl 模板
//   - 其它场景 → 标准 -pl 模板，参数取 mavenModuleTarget(ctx)
func javaVerificationSection(ctx PromptContext) string {
	if ctx.AnchorResolved && strings.TrimSpace(ctx.MavenModulePath) == "" {
		displayModule := ctx.Module
		if strings.TrimSpace(displayModule) == "" {
			displayModule = "<整仓>"
		}
		return fmt.Sprintf(javaVerificationCmdNoPlTemplate, sanitize(displayModule, 500))
	}
	return fmt.Sprintf(javaVerificationCmdTemplate, javaVerificationTarget(mavenModuleTarget(ctx)))
}

// mavenModuleTarget 选择 Java 验证命令里 `mvn -pl` 的目标路径：
//   - MavenModulePath 非空（resolveFramework 回溯命中的锚点）→ 使用 anchor
//   - MavenModulePath 为空 → 回退到用户输入的 Module
func mavenModuleTarget(ctx PromptContext) string {
	if strings.TrimSpace(ctx.MavenModulePath) != "" {
		return ctx.MavenModulePath
	}
	return ctx.Module
}

// buildVuePrompt 按公共段 + Vue 特有段拼接 prompt。
func buildVuePrompt(ctx PromptContext) string {
	var b strings.Builder
	branch := sanitize(branchSuffix(ctx.Module, ctx.Timestamp), 120)
	writeHeader(&b, ctx)
	b.WriteString(existingTestsInstruction)
	b.WriteString(gapAnalysisInstruction)
	b.WriteString(noOverwriteInstruction)
	b.WriteString(fmt.Sprintf(incrementalCommitInstructionTemplate, ctx.MaxRetryRounds, branch))
	b.WriteString(budgetAwareInstruction)
	b.WriteString(fmt.Sprintf(verificationInstructionTemplate, ctx.MaxRetryRounds))
	b.WriteString(vueTestingInstruction)
	b.WriteString(vueVerificationCmd)
	b.WriteString(fmt.Sprintf(pushInstruction, branch))
	b.WriteString(outputJSONSchemaInstruction)
	return b.String()
}

// writeHeader 写入公共前言 + 任务上下文段（Java / Vue prompt 共用）。
func writeHeader(b *strings.Builder, ctx PromptContext) {
	b.WriteString(promptCommonHeader)
	b.WriteString(fmt.Sprintf("\n## 任务上下文\n\n仓库：%s\n", sanitize(ctx.RepoFullName, 200)))
	if ctx.Module != "" {
		b.WriteString(fmt.Sprintf("目标 module：%s\n", sanitize(ctx.Module, 500)))
	}
	if ctx.BaseRef != "" {
		b.WriteString(fmt.Sprintf("基准 ref：%s\n", sanitize(ctx.BaseRef, 200)))
	}
	b.WriteString(fmt.Sprintf("拟创建分支：auto-test/%s\n", sanitize(branchSuffix(ctx.Module, ctx.Timestamp), 120)))
}

// branchSuffix 以 module + timestamp 构造 auto-test 分支尾部。
// module 空时用 "all"；通过 sanitizeBranchRef 过滤 git ref 非法字符。
func branchSuffix(module, ts string) string {
	key := module
	if key == "" {
		key = "all"
	}
	key = sanitizeBranchRef(key)
	if key == "" {
		// 所有字符都被过滤掉（例如 module 只含空格 / NUL 等），回落 "all"
		key = "all"
	}
	return key + "-" + ts
}

// sanitizeBranchRef 按 git ref 命名约束清洗 module 名：
//   - 仅保留 [A-Za-z0-9._-]；其他字符（空格、/、:、~、^、?、*、[、]、\、@、{、}、
//     控制字符、UTF-8 字符等）统一替换为 -
//   - 连续多个 - 合并为单个
//   - 修剪首尾的 .、-（git 拒绝以 . 开头的 ref；以 - 开头易与命令行参数混淆）
//
// 约束依据：`git help check-ref-format` 列明 ref 禁止包含空格、~、^、:、?、*、[、\\、
// `..`、`@{`、控制字符，且不能以 . 开头或以 .lock 结尾。module 经本函数处理后再拼入
// `auto-test/<module>-<timestamp>`，可规避绝大多数 ref 非法情形。
func sanitizeBranchRef(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z'),
			(r >= 'A' && r <= 'Z'),
			(r >= '0' && r <= '9'),
			r == '.', r == '_':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := b.String()
	// 迭代归一化：消除 git ref 禁止的 ".." 序列以及替换过程中产生的
	// "-."/".-" 混合（经常出现在 "a/../b" → "a-..-b" 这类输入里），
	// 直到字符串稳定为止；再修剪首尾 . 与 -。
	for {
		prev := out
		out = strings.ReplaceAll(out, "..", ".")
		out = strings.ReplaceAll(out, "-.", "-")
		out = strings.ReplaceAll(out, ".-", "-")
		out = strings.ReplaceAll(out, "--", "-")
		if out == prev {
			break
		}
	}
	out = strings.Trim(out, ".-")
	return out
}

// sanitize 截断 + 清理换行/NUL 字符，防止 prompt injection 与格式破坏。
// 与 worker/container.go 的 sanitizePromptInput 等价独立实现，避免跨包耦合。
func sanitize(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}

// javaVerificationTarget 返回可安全嵌入 shell 命令的 module 参数。
// 未指定 module 时使用占位文本，避免生成缺少有效 module 参数的误导性命令。
func javaVerificationTarget(module string) string {
	module = sanitize(module, 500)
	if strings.TrimSpace(module) == "" {
		return "<module>"
	}
	return shellQuote(module)
}

// shellQuote 用 POSIX 单引号形式转义任意字符串，避免把 module 当成 shell 片段执行。
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// buildCommand 构造 Claude CLI 容器执行命令（与 fix.buildFixCommand 风格一致）。
// gen_tests 需要 Edit / Write / Bash 写权限，不追加 --disallowedTools。
func buildCommand(cfgProv TestConfigProvider) []string {
	cmd := []string{
		"claude", "-p", "-",
		"--output-format", "json",
	}
	if cfgProv != nil {
		if m := cfgProv.GetClaudeModel(); m != "" {
			cmd = append(cmd, "--model", m)
		}
		if effort := cfgProv.GetClaudeEffort(); effort != "" {
			cmd = append(cmd, "--effort", strings.ToLower(strings.TrimSpace(effort)))
		}
	}
	return cmd
}
