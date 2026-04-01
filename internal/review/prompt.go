package review

import (
	"fmt"
	"path/filepath"
	"strings"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// TechStack 技术栈位掩码
type TechStack int

const (
	TechJava TechStack = 1 << iota // .java 文件
	TechVue                        // .vue 及关联前端文件
)

// ReviewConfig 评审编排层的内部配置（从 config.ReviewOverride 转换而来）
type ReviewConfig struct {
	Instructions       string   // 全局评审指令
	RepoInstructions   string   // 仓库级追加指令
	Dimensions         []string // 启用的评审维度
	LargePRThreshold   int      // 大 PR 警告阈值（变更行数）
	TechStack          []string // 技术栈显式指定
	CodeStandardsPaths []string // 自定义规范文件路径
	Severity           string   // M2.5: severity 过滤阈值
	IgnorePatterns     []string // M2.5: 文件忽略 glob 模式列表
}

// jsonSchemaInstruction 输出格式约束（硬编码，不可配置）
const jsonSchemaInstruction = `

## Output Format

You MUST output your review as a single JSON object with the following schema.
Do NOT wrap it in markdown code fences. Output ONLY the raw JSON.

{
  "summary": "Overall review summary in 2-3 paragraphs",
  "verdict": "approve | request_changes | comment",
  "issues": [
    {
      "file": "relative/path/to/file.java",
      "line": 42,
      "end_line": 45,
      "severity": "CRITICAL | ERROR | WARNING | INFO",
      "category": "security | logic | style | architecture",
      "message": "Description of the issue",
      "suggestion": "How to fix it (optional)"
    }
  ]
}

Verdict rules:
- If any issue has severity CRITICAL or ERROR -> "request_changes"
- If issues exist but all are WARNING or INFO -> "comment"
- If no issues found -> "approve"
`

// largePRGuidance 大 PR 警示（条件性追加）
const largePRGuidance = `

## Large PR Notice

This is a large PR. Focus your review on:
1. Security-critical changes first
2. Core logic changes (new features, bug fixes)
3. Skip trivial formatting or auto-generated files
Do not try to comment on every file. Prioritize high-impact issues.
`

// reviewPreamble 评审指令前言：标题 + 评审原则 + 严重程度定义（始终包含）
const reviewPreamble = `
## Review Instructions

你是一位资深代码评审专家，正在对一个团队项目的 Pull Request 进行评审。

### 评审原则
- 先理解 PR 的目的和作者意图，再评判实现是否合理
- 使用 git diff 查看变更，同时主动探索相关文件、追溯调用链、检查项目结构
- 聚焦真正重要的问题，不要吹毛求疵
- 安全问题和逻辑错误零容忍，必须明确指出
- 风格问题仅在严重影响可读性或违反项目既有惯例时提出
- 评估变更对现有功能的回归影响

### 严重程度定义
- CRITICAL: 安全漏洞（注入、越权、信息泄露）、数据损坏风险、生产事故隐患。必须修复。
- ERROR: 逻辑错误、边界条件遗漏、异常处理缺失、资源泄漏、并发安全问题。强烈建议修复。
- WARNING: 代码异味、潜在性能问题、错误传播不当、过度耦合、缺少必要校验。建议改进。
- INFO: 命名优化、注释补充、可读性提升、更优的设计模式建议。仅供参考。
`

// securityInstructions 安全维度评审指令
const securityInstructions = `
### 安全 (security)
- SQL 注入、XSS、CSRF、路径遍历、不安全的反序列化
- 硬编码密钥/凭证、敏感信息写入日志
- 认证/授权绕过、权限校验遗漏
`

// logicInstructions 逻辑维度评审指令
const logicInstructions = `
### 逻辑 (logic)
- 空指针/nil 引用、数组越界、类型转换失败
- 边界条件（空集合、零值、最大值、并发场景）
- 错误处理：吞没错误、错误类型不匹配、缺少回滚
- 资源泄漏（数据库连接、文件句柄、goroutine）
`

// architectureInstructions 架构维度评审指令
const architectureInstructions = `
### 架构 (architecture)
- 职责划分是否清晰，是否违反分层约定
- API 设计的一致性和向后兼容性
- 变更是否影响其他模块（评估回归半径）
`

// styleInstructions 风格维度评审指令
const styleInstructions = `
### 风格 (style)
- 命名是否清晰表达意图
- 函数/方法是否过长（超过 80 行需关注）
- 与项目既有风格的一致性
`

// dimensionInstructions 维度名称到指令段的映射
var dimensionInstructions = map[string]string{
	"security":     securityInstructions,
	"logic":        logicInstructions,
	"architecture": architectureInstructions,
	"style":        styleInstructions,
}

// defaultReviewInstructions 默认评审指令（中文），保留用于向后兼容
const defaultReviewInstructions = reviewPreamble + securityInstructions + logicInstructions + architectureInstructions + styleInstructions

// javaReviewInstructions Java 专项评审 prompt 段
const javaReviewInstructions = `

### Java 专项评审（Spring Boot + MyBatis）

在通用评审基础上，额外关注以下 Java 生态特有问题：

#### 事务与数据一致性
- @Transactional 使用是否正确：是否加在 public 方法上、传播级别是否合理、是否存在自调用导致事务失效
- 跨表操作是否遗漏事务注解
- 异常类型与 rollbackFor 是否匹配（默认仅回滚 RuntimeException）

#### MyBatis SQL 安全与质量
- ${} 与 #{} 使用是否正确：动态拼接 SQL 必须用 #{}，仅表名/列名等无法参数化的场景才允许 ${}
- 动态 SQL（<if>、<foreach>）是否存在注入风险
- 批量操作是否考虑数据量上限（避免 SQL 过长）
- 分页查询是否使用 LIMIT，避免全表扫描

#### Spring Boot 常见陷阱
- @Autowired 循环依赖：是否通过构造器注入避免
- Controller 层是否混入业务逻辑（应委托给 Service）
- 接口是否遵循 RESTful 规范：HTTP 方法语义、状态码使用、URI 命名
- 配置项是否硬编码（应使用 @Value 或 @ConfigurationProperties）

#### 并发与性能
- 共享可变状态是否有同步保护（Spring Bean 默认单例）
- 数据库连接是否及时释放（try-with-resources 或框架管理）
- N+1 查询问题：循环中是否存在逐条查询数据库
- 大数据量查询是否使用流式或分页处理
`

// vueReviewInstructions Vue 专项评审 prompt 段
const vueReviewInstructions = `

### Vue 专项评审（Vue 3 + Composition API）

在通用评审基础上，额外关注以下 Vue 生态特有问题：

#### 响应式使用
- ref 与 reactive 是否正确选择：原始类型用 ref，对象用 reactive
- 解构 reactive 对象是否使用 toRefs 保持响应式
- watchEffect / watch 是否存在不必要的依赖触发或遗漏清理
- computed 是否用于派生状态（避免 watch + 手动赋值的反模式）

#### 组件设计
- 组件是否遵循单一职责（超过 300 行需关注拆分）
- Props 是否定义类型和默认值（defineProps 使用是否规范）
- 事件是否通过 defineEmits 声明，避免隐式事件
- v-model 使用是否正确（组件双向绑定的 modelValue + update:modelValue 约定）

#### 安全
- v-html 使用是否存在 XSS 风险（是否对用户输入做过滤）
- 动态属性绑定（:href、:src）是否校验来源，防止 javascript: 协议注入
- 敏感信息是否泄露到前端代码或 localStorage

#### 性能
- v-for 是否提供唯一稳定的 key（避免使用 index）
- 大列表是否考虑虚拟滚动
- 组件是否合理使用 defineAsyncComponent 按需加载
- 不必要的深层 watch（deep: true）是否可用更精确的监听替代

#### 状态管理（Pinia）
- Store 是否职责清晰，避免单个 Store 过于庞大
- 异步操作是否在 actions 中处理（避免组件内直接修改 state）
- 是否存在跨 Store 循环依赖
`

// codeStandardsInstruction 默认编码规范引导（无自定义路径时使用）
const codeStandardsInstruction = `

### 项目编码规范

在评审前，请检查仓库中是否存在以下编码规范文件（按优先级顺序），如果存在请先阅读，并将其作为评审依据：

1. CLAUDE.md 中的"编码规范"或"开发规范"章节
2. .code-standards.md 或 .code-standards/
3. CONTRIBUTING.md
4. .editorconfig
5. docs/coding-standards.md 或 docs/code-style.md
6. README.md 中的"编码规范"或"开发规范"章节

如果找到规范文件，评审时应检查变更代码是否符合项目自身的编码规范。
违反项目规范的问题使用 WARNING 级别，category 标记为 style。
`

// truncate 按 rune 截断字符串，避免截断 UTF-8 多字节字符
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "..."
	}
	return s
}

// countChanges 单次遍历计算变更文件的新增行数、删除行数和总变更行数
func countChanges(files []*gitea.ChangedFile) (additions, deletions int) {
	for _, f := range files {
		additions += f.Additions
		deletions += f.Deletions
	}
	return additions, deletions
}

// formatFilesSummary 将变更文件列表格式化为 prompt 摘要。
// 超过 maxFiles 个文件时只列前 maxFiles 个并注明总数。
// 注意：此列表可能不完整（单页 API 获取），prompt 中会提示 Claude 用 git diff 查看完整变更。
func formatFilesSummary(files []*gitea.ChangedFile) string {
	const maxFiles = 50
	var b strings.Builder
	adds, dels := countChanges(files)
	b.WriteString(fmt.Sprintf("\nChanged files (%d files, +%d/-%d lines):\n",
		len(files), adds, dels))
	limit := len(files)
	if limit > maxFiles {
		limit = maxFiles
	}
	for _, f := range files[:limit] {
		b.WriteString(fmt.Sprintf("  - %s (+%d/-%d) [%s]\n",
			f.Filename, f.Additions, f.Deletions, f.Status))
	}
	if len(files) > maxFiles {
		b.WriteString(fmt.Sprintf("  ... and %d more files (use 'git diff' to see all)\n",
			len(files)-maxFiles))
	}
	b.WriteString("\nNote: this file list may be partial. Use 'git diff' to see the complete changeset.\n")
	return b.String()
}

// safeOutput 安全提取 ExecutionResult 的 Output，execResult 为 nil 时返回空字符串
func safeOutput(r *worker.ExecutionResult) string {
	if r == nil {
		return ""
	}
	return r.Output
}

// extractJSON 从 Claude 回答文本中提取 JSON 内容。
// 处理可能的 markdown code fence 包装（```json ... ```），
// 包括 code fence 前有前导文本的情况（如 "Here is my review:\n```json\n..."）。
func extractJSON(text string) string {
	text = strings.TrimSpace(text)
	// 查找 code fence 起始位置（可能不在开头）
	fenceStart := strings.Index(text, "```")
	if fenceStart >= 0 {
		// 从 fence 开始处理
		fenced := text[fenceStart:]
		// 跳过 ``` 后的语言标识行（如 ```json）
		lines := strings.SplitN(fenced, "\n", 2)
		if len(lines) == 2 {
			fenced = lines[1]
		}
		// 查找结束 fence
		if idx := strings.LastIndex(fenced, "```"); idx >= 0 {
			fenced = fenced[:idx]
		}
		return strings.TrimSpace(fenced)
	}
	// 无 code fence：定位第一个 '{' 和最后一个 '}'，
	// 处理 Claude 在 JSON 前后输出自然语言文本的情况。
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

// vueMarkerDirs Vue 项目特征目录（两段路径）
var vueMarkerDirs = []string{"src/views", "src/components", "src/composables", "src/stores"}

// detectTechStack 从 PR 变更文件列表中检测技术栈
func detectTechStack(files []*gitea.ChangedFile) TechStack {
	var stack TechStack
	vueSignal := hasVueSignal(files) // 预计算，避免对每个 .ts/.js 文件重复遍历整个列表
	for _, f := range files {
		ext := filepath.Ext(f.Filename)
		switch ext {
		case ".java":
			stack |= TechJava
		case ".vue":
			stack |= TechVue
		case ".ts", ".tsx", ".js", ".jsx":
			if vueSignal {
				stack |= TechVue
			}
		}
	}
	return stack
}

// hasVueSignal 检查文件列表中是否有 Vue 项目的特征信号
func hasVueSignal(files []*gitea.ChangedFile) bool {
	for _, f := range files {
		if filepath.Ext(f.Filename) == ".vue" {
			return true
		}
		// 按路径段精确匹配 Vue 特征目录，避免子串误判
		// 例：匹配 "src/components/Foo.ts" 但不匹配 "src/components-legacy/Bar.ts"
		normalized := filepath.ToSlash(f.Filename)
		for _, marker := range vueMarkerDirs {
			if strings.HasPrefix(normalized, marker+"/") || strings.Contains(normalized, "/"+marker+"/") {
				return true
			}
		}
	}
	return false
}

// resolveTechStack 解析最终技术栈：配置优先于自动检测。
// 返回值 unknown 包含配置中无法识别的技术栈名称，调用方应记录警告。
func resolveTechStack(files []*gitea.ChangedFile, cfg ReviewConfig) (stack TechStack, unknown []string) {
	if len(cfg.TechStack) > 0 {
		for _, t := range cfg.TechStack {
			switch strings.ToLower(t) {
			case "java":
				stack |= TechJava
			case "vue":
				stack |= TechVue
			default:
				unknown = append(unknown, t)
			}
		}
		return stack, unknown
	}
	return detectTechStack(files), nil
}

// buildDynamicInstructions 根据启用的维度动态组装评审指令。
// 始终包含 reviewPreamble，只拼接启用维度的指令段。
func buildDynamicInstructions(dimensions []string) string {
	var b strings.Builder
	b.WriteString(reviewPreamble)
	for _, dim := range dimensions {
		if instr, ok := dimensionInstructions[normalizeDimensionName(dim)]; ok {
			b.WriteString(instr)
		}
	}
	return b.String()
}

// normalizeDimensionName 规范化维度名称，与配置校验保持一致。
func normalizeDimensionName(dim string) string {
	return strings.ToLower(strings.TrimSpace(dim))
}

// buildCodeStandardsSection 构造编码规范 prompt 段
func buildCodeStandardsSection(paths []string) string {
	if len(paths) == 0 {
		return codeStandardsInstruction
	}
	var b strings.Builder
	b.WriteString("\n\n### 项目编码规范\n\n在评审前，请先阅读以下项目编码规范文件，并将其作为评审依据：\n\n")
	for i, p := range paths {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, p))
	}
	b.WriteString("\n如果找到规范文件，评审时应检查变更代码是否符合项目自身的编码规范。\n")
	b.WriteString("违反项目规范的问题使用 WARNING 级别，category 标记为 style。\n")
	return b.String()
}

// formatIgnoredFilesSection 构造 ignore_patterns 提示段，明确告知模型忽略范围。
func formatIgnoredFilesSection(patterns []string, ignoredFiles []*gitea.ChangedFile) string {
	if len(patterns) == 0 || len(ignoredFiles) == 0 {
		return ""
	}

	const maxFiles = 20

	var b strings.Builder
	b.WriteString("\nIgnored paths (configured via ignore_patterns):\n")
	for _, pattern := range patterns {
		b.WriteString(fmt.Sprintf("  - %s\n", pattern))
	}
	b.WriteString("These files are out of scope for this review. Even if they appear in `git diff`, do not review them and do not mention them in summary, verdict reasoning, or issues.\n")
	b.WriteString(fmt.Sprintf("Ignored files in this PR (%d files):\n", len(ignoredFiles)))

	limit := len(ignoredFiles)
	if limit > maxFiles {
		limit = maxFiles
	}
	for _, f := range ignoredFiles[:limit] {
		b.WriteString(fmt.Sprintf("  - %s (+%d/-%d) [%s]\n",
			f.Filename, f.Additions, f.Deletions, f.Status))
	}
	if len(ignoredFiles) > maxFiles {
		b.WriteString(fmt.Sprintf("  ... and %d more ignored files\n", len(ignoredFiles)-maxFiles))
	}

	return b.String()
}

// buildPrompt 按四段式构造评审 prompt：
// 1. 任务上下文（PR 元数据）
// 2. 评审指令（通用 + 仓库级 + 专项 + 规范）
// 3. 输出格式约束（硬编码）
// 4. 大 PR 警示（条件性）
func (s *Service) buildPrompt(pr *gitea.PullRequest, files []*gitea.ChangedFile, cfg ReviewConfig, techStack TechStack) string {
	var b strings.Builder

	// M2.5: 文件过滤 — 在构造 prompt 前剔除被忽略的文件
	var filteredFiles []*gitea.ChangedFile
	var ignoredFiles []*gitea.ChangedFile
	if len(cfg.IgnorePatterns) > 0 {
		for _, f := range files {
			if MatchesIgnorePattern(f.Filename, cfg.IgnorePatterns) {
				ignoredFiles = append(ignoredFiles, f)
			} else {
				filteredFiles = append(filteredFiles, f)
			}
		}
	} else {
		filteredFiles = files
	}

	// 0. 只读模式约束（最高优先级，最先出现）
	b.WriteString("IMPORTANT: You are in READ-ONLY code analysis mode.\n")
	b.WriteString("- Do NOT call any external APIs, HTTP endpoints, or network services\n")
	b.WriteString("- Do NOT attempt to submit reviews, comments, or status updates to Gitea or any other platform\n")
	b.WriteString("- Do NOT run curl, wget, python requests, or any other network commands\n")
	b.WriteString("- Your ONLY task is to analyze the code and output the JSON result to stdout\n\n")

	// 1. 任务上下文
	b.WriteString(fmt.Sprintf("You are reviewing PR #%d in repository %s.\n", pr.Number, pr.Base.Repo.FullName))
	b.WriteString(fmt.Sprintf("Author: %s\n", pr.User.Login))
	b.WriteString(fmt.Sprintf("Base branch: %s\n", pr.Base.Ref))
	b.WriteString(fmt.Sprintf("PR title: %s\n", pr.Title))
	if pr.Body != "" {
		b.WriteString(fmt.Sprintf("PR description:\n%s\n", truncate(pr.Body, 2000)))
	}
	b.WriteString(formatFilesSummary(filteredFiles))
	if len(ignoredFiles) > 0 {
		b.WriteString(formatIgnoredFilesSection(cfg.IgnorePatterns, ignoredFiles))
	}

	// 2a. 通用评审指令（全局）
	b.WriteString("\n")
	if cfg.Instructions == defaultReviewInstructions || cfg.Instructions == "" {
		// 使用动态组装（按维度裁剪）
		b.WriteString(buildDynamicInstructions(cfg.Dimensions))
	} else {
		// 用户自定义 instructions，不做维度裁剪
		b.WriteString(cfg.Instructions)
	}

	// 2b. 仓库级追加指令
	if cfg.RepoInstructions != "" {
		b.WriteString("\n\nAdditional repository-specific instructions:\n")
		b.WriteString(cfg.RepoInstructions)
	}

	// 2c. 专项评审指令（条件性）
	if techStack&TechJava != 0 {
		b.WriteString(javaReviewInstructions)
	}
	if techStack&TechVue != 0 {
		b.WriteString(vueReviewInstructions)
	}

	// 2d. 项目编码规范
	b.WriteString(buildCodeStandardsSection(cfg.CodeStandardsPaths))

	// 3. 输出格式（硬编码，不可配置）
	b.WriteString(jsonSchemaInstruction)

	// 4. 大 PR 警示（条件性，基于过滤后的文件列表）
	a, d := countChanges(filteredFiles)
	totalChanges := a + d
	if totalChanges > cfg.LargePRThreshold || len(filteredFiles) > 30 {
		b.WriteString(largePRGuidance)
	}

	return b.String()
}

// buildCommand 构造容器执行命令（stdin 模式，prompt 通过 stdin 传入）
// 评审场景为只读模式：通过 --disallowedTools 禁止所有文件写工具，
// 防止 Claude Code 误修改代码或执行 git 写操作。
func (s *Service) buildCommand() []string {
	return []string{
		"claude", "-p", "-",
		"--output-format", "json",
		"--disallowedTools", "Edit,Write,NotebookEdit",
	}
}
