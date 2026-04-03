package fix

import (
	"fmt"
	"strings"

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

// CLIResponse Claude CLI JSON 信封（独立于 review 包）
type CLIResponse struct {
	Type          string  `json:"type"`
	Subtype       string  `json:"subtype"`
	CostUSD       float64 `json:"cost_usd"`
	DurationMs    int64   `json:"duration_ms"`
	DurationAPIMs int64   `json:"duration_api_ms"`
	IsError       bool    `json:"is_error"`
	NumTurns      int     `json:"num_turns"`
	Result        string  `json:"result"`
	SessionID     string  `json:"session_id"`
}

// buildPrompt 按四段式构造分析 prompt
func (s *Service) buildPrompt(issueCtx *IssueContext) string {
	var b strings.Builder

	// 1. 只读模式约束
	b.WriteString(readOnlyConstraint)

	// 2. 任务上下文
	b.WriteString(fmt.Sprintf("你正在分析仓库中的 Issue #%d。\n", issueCtx.Issue.Number))
	b.WriteString(fmt.Sprintf("Issue 标题：%s\n", issueCtx.Issue.Title))
	if issueCtx.Issue.Body != "" {
		b.WriteString(fmt.Sprintf("Issue 描述：\n%s\n", truncate(issueCtx.Issue.Body, 5000)))
	}
	if len(issueCtx.Comments) > 0 {
		b.WriteString(fmt.Sprintf("\n评论（共 %d 条）：\n", len(issueCtx.Comments)))
		const maxCommentTotalRunes = 20000
		commentRunes := 0
		for i, c := range issueCtx.Comments {
			body := truncate(c.Body, 2000)
			if commentRunes+len([]rune(body)) > maxCommentTotalRunes {
				b.WriteString(fmt.Sprintf("（剩余 %d 条评论因长度限制被省略）\n", len(issueCtx.Comments)-i))
				break
			}
			commentRunes += len([]rune(body))
			b.WriteString(fmt.Sprintf("--- 评论 #%d（%s）---\n%s\n",
				i+1, c.User.Login, body))
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
