package review

import (
	"fmt"
	"strings"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// ReviewConfig 评审编排层的内部配置（从 config.ReviewOverride 转换而来）
type ReviewConfig struct {
	Instructions     string   // 全局评审指令
	RepoInstructions string   // 仓库级追加指令
	Dimensions       []string // 启用的评审维度
	LargePRThreshold int      // 大 PR 警告阈值（变更行数）
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

// defaultReviewInstructions 默认评审指令（中文）
const defaultReviewInstructions = `
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

### 安全 (security)
- SQL 注入、XSS、CSRF、路径遍历、不安全的反序列化
- 硬编码密钥/凭证、敏感信息写入日志
- 认证/授权绕过、权限校验遗漏

### 逻辑 (logic)
- 空指针/nil 引用、数组越界、类型转换失败
- 边界条件（空集合、零值、最大值、并发场景）
- 错误处理：吞没错误、错误类型不匹配、缺少回滚
- 资源泄漏（数据库连接、文件句柄、goroutine）

### 架构 (architecture)
- 职责划分是否清晰，是否违反分层约定
- API 设计的一致性和向后兼容性
- 变更是否影响其他模块（评估回归半径）

### 风格 (style)
- 命名是否清晰表达意图
- 函数/方法是否过长（超过 80 行需关注）
- 与项目既有风格的一致性
`

// truncate 按 rune 截断字符串，避免截断 UTF-8 多字节字符
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "..."
	}
	return s
}

// totalAdditions 计算所有变更文件的总新增行数
func totalAdditions(files []*gitea.ChangedFile) int {
	total := 0
	for _, f := range files {
		total += f.Additions
	}
	return total
}

// totalDeletions 计算所有变更文件的总删除行数
func totalDeletions(files []*gitea.ChangedFile) int {
	total := 0
	for _, f := range files {
		total += f.Deletions
	}
	return total
}

// sumChanges 计算变更文件的总增删行数
func sumChanges(files []*gitea.ChangedFile) int {
	total := 0
	for _, f := range files {
		total += f.Additions + f.Deletions
	}
	return total
}

// formatFilesSummary 将变更文件列表格式化为 prompt 摘要。
// 超过 maxFiles 个文件时只列前 maxFiles 个并注明总数。
// 注意：此列表可能不完整（单页 API 获取），prompt 中会提示 Claude 用 git diff 查看完整变更。
func formatFilesSummary(files []*gitea.ChangedFile) string {
	const maxFiles = 50
	var b strings.Builder
	b.WriteString(fmt.Sprintf("\nChanged files (%d files, +%d/-%d lines):\n",
		len(files), totalAdditions(files), totalDeletions(files)))
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
	return text
}

// buildPrompt 按四段式构造评审 prompt：
// 1. 任务上下文（PR 元数据）
// 2. 评审指令（可配置）
// 3. 输出格式约束（硬编码）
// 4. 大 PR 警示（条件性）
func (s *Service) buildPrompt(pr *gitea.PullRequest, files []*gitea.ChangedFile, cfg ReviewConfig) string {
	var b strings.Builder

	// 1. 任务上下文
	b.WriteString(fmt.Sprintf("You are reviewing PR #%d in repository %s.\n", pr.Number, pr.Base.Repo.FullName))
	b.WriteString(fmt.Sprintf("Author: %s\n", pr.User.Login))
	b.WriteString(fmt.Sprintf("Base branch: %s\n", pr.Base.Ref))
	b.WriteString(fmt.Sprintf("PR title: %s\n", pr.Title))
	if pr.Body != "" {
		b.WriteString(fmt.Sprintf("PR description:\n%s\n", truncate(pr.Body, 2000)))
	}
	b.WriteString(formatFilesSummary(files))

	// 2. 评审指令（全局 + 仓库级追加）
	b.WriteString("\n")
	b.WriteString(cfg.Instructions)
	if cfg.RepoInstructions != "" {
		b.WriteString("\n\nAdditional repository-specific instructions:\n")
		b.WriteString(cfg.RepoInstructions)
	}

	// 3. 输出格式（硬编码，不可配置）
	b.WriteString(jsonSchemaInstruction)

	// 4. 大 PR 警示（条件性）
	totalChanges := sumChanges(files)
	if totalChanges > cfg.LargePRThreshold || len(files) > 30 {
		b.WriteString(largePRGuidance)
	}

	return b.String()
}

// buildCommand 构造容器执行命令
func (s *Service) buildCommand(prompt string) []string {
	return []string{"claude", "-p", prompt, "--output-format", "json"}
}
