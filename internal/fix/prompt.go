package fix

import (
	"fmt"
	"strings"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// readOnlyConstraint 只读模式约束（最高优先级，同 review 包）
const readOnlyConstraint = `IMPORTANT: You are in READ-ONLY code analysis mode.
- Do NOT call any external APIs, HTTP endpoints, or network services
- Do NOT attempt to submit reviews, comments, or status updates to Gitea or any other platform
- Do NOT run curl, wget, python requests, or any other network commands
- Your ONLY task is to analyze the code and output the JSON result to stdout

`

// analysisPreamble 分析指令前言
const analysisPreamble = `
## Issue 分析指令

你是一位资深软件工程师，正在对一个项目中报告的 Issue 进行根因分析。
你的目标是：定位问题根因，给出修复建议。
`

// infoSufficiencyInstruction 信息充分性判断
const infoSufficiencyInstruction = `
### 第一步：信息充分性判断

在开始分析前，先评估 Issue 提供的信息是否足够进行根因定位：
- 充分信息：包含错误信息/异常堆栈/复现步骤/影响范围中至少两项
- 不充分信息：仅有模糊描述（如"页面出错了"）、缺少错误日志和复现步骤

如果信息不充分，设置 info_sufficient=false，在 missing_info 中列出具体需要补充的信息项，
然后在 analysis 中简要说明现有信息能推断出什么。不必进行深入代码分析。
`

// rootCauseInstruction 根因分析指令
const rootCauseInstruction = `
### 第二步：根因分析

如果信息充分，按以下策略进行分析：

1. **关键词定位**：从 Issue 中提取类名、方法名、错误信息等关键词，在代码库中搜索
2. **调用链追踪**：找到可疑代码后，追踪其调用者和被调用者，理解完整执行路径
3. **关联文件检查**：检查配置文件、测试文件、数据模型等关联资源
4. **变更历史**：如有帮助，查看相关文件的 git log 了解近期变更
5. **项目结构理解**：先浏览项目结构，理解模块划分和依赖关系

目标是精确定位到具体文件、方法和行号。优先定位最根本的原因，而非表象。
`

// fixSuggestionInstruction 修复建议指令
const fixSuggestionInstruction = `
### 第三步：修复建议

基于根因分析给出修复方向：
- 需要修改哪些文件、方法
- 推荐的修复策略
- 需要注意的副作用和回归风险
`

// analysisJSONSchemaInstruction 输出格式定义（硬编码）
const analysisJSONSchemaInstruction = `
## 输出格式

你必须将分析结果输出为如下结构的单个 JSON 对象。
不要用 markdown 代码块包裹，只输出原始 JSON。
**所有文本字段必须使用中文书写。**

{
  "info_sufficient": true,
  "missing_info": [],
  "root_cause": {
    "file": "relative/path/to/file.java",
    "function": "methodName",
    "start_line": 42,
    "end_line": 55,
    "description": "根因描述（中文）"
  },
  "analysis": "详细分析说明（中文）",
  "fix_suggestion": "修复建议（中文）",
  "confidence": "high | medium | low",
  "related_files": ["path/to/related1.java", "path/to/related2.java"]
}

信息不足时的输出示例：

{
  "info_sufficient": false,
  "missing_info": ["缺少错误堆栈信息", "缺少复现步骤"],
  "analysis": "根据现有描述的初步判断...",
  "confidence": "low",
  "related_files": []
}
`

// ==================== M3.5: fix_issue 修复模式 prompt ====================

// fixPreamble 修复模式前言（替代 analyze 的 readOnlyConstraint + analysisPreamble）。
// fix 模式允许运行 Bash（需执行 mvn test / npm test），但仍禁止网络访问。
const fixPreamble = `你是一位资深软件工程师，正在修复项目中报告的 Issue。
你拥有完整的文件读写和 Bash 执行能力，可以：
- 修改源代码文件
- 运行 mvn test / npm test / npx vitest 等测试命令
- 使用 git 创建分支、commit、push

安全约束（非常重要）：
- 禁止调用任何外部 HTTP API / 网络服务（curl、wget 等）
- 禁止向 Gitea 提交评审、评论或状态更新（由系统外部处理）
- 除运行测试外，不要执行任何与修复无关的系统命令

你的目标：分析 Issue → 修改代码 → 补充测试 → 运行测试 → push 代码，并输出 JSON 结果。
`

// fixInstructions 修复流程指令（六步）
const fixInstructions = `
## 修复流程

### 第一步：理解问题
- 阅读 Issue 描述和评论，判断信息是否充分
- **如果 Issue 评论中存在之前的分析报告**，优先参考其根因定位和修复建议
- 信息不充分时，输出 info_sufficient=false + missing_info 后终止，不要启动修复

### 第二步：创建修复分支
执行（分支已存在时用 force-with-lease 覆盖，确保重试幂等）：
  git checkout -b auto-fix/issue-<id> 2>/dev/null || git checkout auto-fix/issue-<id>

### 第三步：修改代码
- 最小必要范围原则：只修改与 Issue 直接相关的文件
- 不修改无关代码格式、不删除现有测试
- 参考前序分析（如有）定位的文件和方法

### 第四步：补充测试
- 为修复点新增或调整单元测试，验证修复有效
- Java 模块用 JUnit 5 + Mockito；Vue/前端模块用 Vitest
- 测试风格与既有测试保持一致（查看现有测试文件的命名、断言库选择）

### 第五步：运行测试
- 自动检测：pom.xml 存在 → 运行 mvn test；package.json 存在 → 运行 npm test 或 npx vitest
- 全部通过才能继续。测试失败时根据错误信息修正代码或测试，重试最多 3 轮
- 3 轮后仍失败，输出 success=false + failure_reason 后终止

### 第六步：Commit + Push
- commit message 格式：fix: #<issue_id> <简要描述>
- push：git push --force-with-lease origin auto-fix/issue-<id>
- 若 push 被拒（例如他人已推送同分支），输出 success=false + failure_reason
`

// fixConstraints 修复约束段
const fixConstraints = `
## 修复约束

- 【禁止】修改与 Issue 无关的文件
- 【禁止】删除或禁用现有测试用例
- 【禁止】跳过测试（--skip-tests / -DskipTests）
- 【禁止】force push 到 main/master/主分支
- 【必须】commit message 使用 "fix: #<issue_id> <描述>" 格式
- 【必须】分支名严格为 auto-fix/issue-<issue_id>
- 【必须】对所有修改的代码路径覆盖测试

Git identity 已由容器 entrypoint 设置（DTWorkflow Bot），直接 git commit 即可。
`

// fixJSONSchemaInstruction 修复输出格式定义
const fixJSONSchemaInstruction = `
## 输出格式

你必须将修复结果输出为如下结构的单个 JSON 对象。
不要用 markdown 代码块包裹，只输出原始 JSON。
**analysis / fix_approach / failure_reason 字段必须使用中文书写。**

成功场景示例：
{
  "success": true,
  "info_sufficient": true,
  "branch_name": "auto-fix/issue-15",
  "commit_sha": "abc123def456",
  "modified_files": ["src/main/java/com/example/LoginController.java", "src/test/java/com/example/LoginControllerTest.java"],
  "test_results": {
    "passed": 12,
    "failed": 0,
    "skipped": 1,
    "all_passed": true
  },
  "analysis": "根因分析（中文）",
  "fix_approach": "修复方案描述（中文）"
}

信息不足场景：
{
  "success": false,
  "info_sufficient": false,
  "missing_info": ["缺少错误堆栈信息", "缺少复现步骤"],
  "analysis": "根据现有描述的初步判断..."
}

修复失败场景（测试未通过、分支 push 失败等）：
{
  "success": false,
  "info_sufficient": true,
  "branch_name": "auto-fix/issue-15",
  "test_results": {"passed": 5, "failed": 3, "skipped": 0, "all_passed": false},
  "analysis": "根因分析（中文）",
  "fix_approach": "尝试的修复方案（中文）",
  "failure_reason": "3 个单元测试未通过：... "
}
`

// CLIResponse Claude CLI JSON 信封（独立于 review 包）
type CLIResponse struct {
	Type          string  `json:"type"`
	Subtype       string  `json:"subtype"`
	CostUSD       float64 `json:"cost_usd"`
	TotalCostUSD  float64 `json:"total_cost_usd"` // CLI 新版本使用此字段
	DurationMs    int64   `json:"duration_ms"`
	DurationAPIMs int64   `json:"duration_api_ms"`
	IsError       bool    `json:"is_error"`
	NumTurns      int     `json:"num_turns"`
	Result        string  `json:"result"`
	SessionID     string  `json:"session_id"`
}

// EffectiveCostUSD 返回有效的费用值，兼容新旧 CLI 字段。
func (r CLIResponse) EffectiveCostUSD() float64 {
	if r.TotalCostUSD > 0 {
		return r.TotalCostUSD
	}
	return r.CostUSD
}

// IsExecutionError 判断 CLI 响应是否表示执行错误。
// 注意：开启 stream_monitor 时，tryExtractResultCLIJSON 会将流事件
// {"type":"result","subtype":"success"} 转换为 {"type":"success"}，
// 因此 "success" 也必须视为正常类型。
func (r CLIResponse) IsExecutionError() bool {
	if r.IsError {
		return true
	}
	switch r.Type {
	case "", "result", "success":
		return false
	default:
		return true
	}
}

// buildPrompt 按四段式构造分析 prompt
func (s *Service) buildPrompt(issueCtx *IssueContext) string {
	var b strings.Builder

	// 1. 只读模式约束
	b.WriteString(readOnlyConstraint)

	// 2. 任务上下文
	b.WriteString(fmt.Sprintf("你正在分析仓库中的 Issue #%d。\n", issueCtx.Issue.Number))
	if issueCtx.Ref != "" {
		b.WriteString(fmt.Sprintf("当前代码基于 ref：%s\n", issueCtx.Ref))
	}
	b.WriteString(fmt.Sprintf("Issue 标题：%s\n", issueCtx.Issue.Title))
	if issueCtx.Issue.Body != "" {
		b.WriteString(fmt.Sprintf("Issue 描述：\n%s\n", truncate(issueCtx.Issue.Body, 5000)))
	}
	if len(issueCtx.Comments) > 0 {
		b.WriteString(fmt.Sprintf("\n评论（共 %d 条）：\n", len(issueCtx.Comments)))
		const maxCommentTotalRunes = 20000
		commentRunes := 0
		for i, c := range issueCtx.Comments {
			author := commentAuthor(c)
			body := ""
			if c != nil {
				body = truncate(c.Body, 2000)
			}
			if commentRunes+len([]rune(body)) > maxCommentTotalRunes {
				b.WriteString(fmt.Sprintf("（剩余 %d 条评论因长度限制被省略）\n", len(issueCtx.Comments)-i))
				break
			}
			commentRunes += len([]rune(body))
			b.WriteString(fmt.Sprintf("--- 评论 #%d（%s）---\n%s\n",
				i+1, author, body))
		}
	}

	// 3. 分析指令
	b.WriteString(analysisPreamble)
	b.WriteString(infoSufficiencyInstruction)
	b.WriteString(rootCauseInstruction)
	b.WriteString(fixSuggestionInstruction)

	// 4. 输出格式
	b.WriteString(analysisJSONSchemaInstruction)

	return b.String()
}

// buildFixPrompt M3.5: 按四段式构造修复 prompt。
// 与 buildPrompt（分析模式）结构相似但内容不同：
// - 没有 READ-ONLY 约束（修复需要 Bash + 文件写）
// - 包含修复流程、分支命名、测试运行、push 指令
// - JSON schema 使用 FixOutput
func (s *Service) buildFixPrompt(issueCtx *IssueContext) string {
	var b strings.Builder

	// 1. 修复前言（含安全约束）
	b.WriteString(fixPreamble)

	// 2. 任务上下文
	b.WriteString(fmt.Sprintf("\n## 任务上下文\n\n你正在修复仓库中的 Issue #%d。\n", issueCtx.Issue.Number))
	if issueCtx.Ref != "" {
		b.WriteString(fmt.Sprintf("当前代码基于 ref：%s\n", issueCtx.Ref))
	}
	b.WriteString(fmt.Sprintf("Issue 标题：%s\n", issueCtx.Issue.Title))
	if issueCtx.Issue.Body != "" {
		b.WriteString(fmt.Sprintf("Issue 描述：\n%s\n", truncate(issueCtx.Issue.Body, 5000)))
	}
	if len(issueCtx.Comments) > 0 {
		b.WriteString(fmt.Sprintf("\n评论（共 %d 条，若含前序分析报告请优先参考其根因定位）：\n", len(issueCtx.Comments)))
		const maxCommentTotalRunes = 20000
		commentRunes := 0
		for i, c := range issueCtx.Comments {
			author := commentAuthor(c)
			body := ""
			if c != nil {
				body = truncate(c.Body, 2000)
			}
			if commentRunes+len([]rune(body)) > maxCommentTotalRunes {
				b.WriteString(fmt.Sprintf("（剩余 %d 条评论因长度限制被省略）\n", len(issueCtx.Comments)-i))
				break
			}
			commentRunes += len([]rune(body))
			b.WriteString(fmt.Sprintf("--- 评论 #%d（%s）---\n%s\n",
				i+1, author, body))
		}
	}

	// 3. 修复指令 + 约束
	b.WriteString(fixInstructions)
	b.WriteString(fixConstraints)

	// 4. 输出格式
	b.WriteString(fixJSONSchemaInstruction)

	return b.String()
}

func commentAuthor(c *gitea.Comment) string {
	if c == nil || c.User == nil || strings.TrimSpace(c.User.Login) == "" {
		return "未知作者"
	}
	return c.User.Login
}

// buildCommand 构造容器执行命令
func (s *Service) buildCommand() []string {
	cmd := []string{
		"claude", "-p", "-",
		"--output-format", "json",
		"--disallowedTools", "Edit,Write,NotebookEdit",
	}
	if s.cfgProv != nil {
		if model := s.cfgProv.GetClaudeModel(); model != "" {
			cmd = append(cmd, "--model", model)
		}
		if effort := s.cfgProv.GetClaudeEffort(); effort != "" {
			cmd = append(cmd, "--effort", strings.ToLower(strings.TrimSpace(effort)))
		}
	}
	return cmd
}

// extractJSON 从 Claude 回答文本中提取 JSON 内容（同 review 包逻辑，独立实现）
func extractJSON(text string) string {
	text = strings.TrimSpace(text)
	fenceStart := strings.Index(text, "```")
	if fenceStart >= 0 {
		fenced := text[fenceStart:]
		lines := strings.SplitN(fenced, "\n", 2)
		if len(lines) == 2 {
			fenced = lines[1]
		}
		if idx := strings.LastIndex(fenced, "```"); idx >= 0 {
			fenced = fenced[:idx]
		}
		return strings.TrimSpace(fenced)
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

// safeOutput 安全提取 ExecutionResult 的 Output
func safeOutput(r *worker.ExecutionResult) string {
	if r == nil {
		return ""
	}
	return r.Output
}

// truncate 按 rune 截断字符串，避免截断 UTF-8 多字节字符
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "..."
	}
	return s
}
