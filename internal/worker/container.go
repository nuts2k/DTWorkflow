package worker

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// sanitizeInput 清理环境变量值，去除换行和 NUL 字符。
// 注意：Docker SDK 的 Env 参数直接传递给容器进程，不经 shell 解析，
// 因此无需转义 shell 元字符。此函数主要防止日志注入和 NUL 截断。
func sanitizeInput(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\x00", "")
	return s
}

// sanitizeEnvValue 清理环境变量值，移除可能导致注入的字符
func sanitizeEnvValue(s string) string {
	return sanitizeInput(s)
}

// sanitizePromptInput 清理用于 CLI prompt 的用户输入。
// 基于 sanitizeInput 去除换行和 NUL 字符后，按 []rune 截断到 maxLen，
// 避免截断 UTF-8 多字节字符。用于构建传给 Claude CLI 的 prompt 参数。
func sanitizePromptInput(s string, maxLen int) string {
	s = sanitizeInput(s)
	runes := []rune(s)
	if len(runes) > maxLen {
		s = string(runes[:maxLen])
	}
	return s
}

// buildContainerEnv 构建容器环境变量列表
// 包含 Gitea、Claude API 凭证和任务相关信息
func buildContainerEnv(config PoolConfig, payload model.TaskPayload) []string {
	env := []string{
		fmt.Sprintf("GITEA_URL=%s", sanitizeEnvValue(config.GiteaURL)),
		fmt.Sprintf("GITEA_TOKEN=%s", sanitizeEnvValue(string(config.GiteaToken))),
		fmt.Sprintf("ANTHROPIC_API_KEY=%s", sanitizeEnvValue(string(config.ClaudeAPIKey))),
		fmt.Sprintf("REPO_CLONE_URL=%s", sanitizeEnvValue(payload.CloneURL)),
		fmt.Sprintf("REPO_OWNER=%s", sanitizeEnvValue(payload.RepoOwner)),
		fmt.Sprintf("REPO_NAME=%s", sanitizeEnvValue(payload.RepoName)),
		fmt.Sprintf("REPO_FULL_NAME=%s", sanitizeEnvValue(payload.RepoFullName)),
		fmt.Sprintf("TASK_TYPE=%s", sanitizeEnvValue(string(payload.TaskType))),
	}

	// 按任务类型追加额外环境变量
	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		env = append(env,
			fmt.Sprintf("PR_NUMBER=%d", payload.PRNumber),
			fmt.Sprintf("HEAD_REF=%s", sanitizeEnvValue(payload.HeadRef)),
			fmt.Sprintf("BASE_REF=%s", sanitizeEnvValue(payload.BaseRef)),
			fmt.Sprintf("HEAD_SHA=%s", sanitizeEnvValue(payload.HeadSHA)),
		)
	case model.TaskTypeFixIssue:
		env = append(env,
			fmt.Sprintf("ISSUE_NUMBER=%d", payload.IssueNumber),
			fmt.Sprintf("ISSUE_TITLE=%s", sanitizeEnvValue(payload.IssueTitle)),
		)
	case model.TaskTypeGenTests:
		if payload.Module != "" {
			env = append(env, fmt.Sprintf("MODULE=%s", sanitizeEnvValue(payload.Module)))
		}
	}

	return env
}

// 注意：prompt 使用英文以获得更好的 AI 理解效果
// buildContainerCmd 根据任务类型构建容器执行命令
// 返回占位命令，实际 prompt 由容器内脚本动态生成
func buildContainerCmd(payload model.TaskPayload) []string {
	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		return []string{
			"claude", "-p",
			fmt.Sprintf("Review the PR #%d in repository %s. Analyze the code changes, check for bugs, style issues, and security concerns. Provide constructive feedback.", payload.PRNumber, sanitizePromptInput(payload.RepoFullName, 200)),
		}
	case model.TaskTypeFixIssue:
		return []string{
			"claude", "-p",
			fmt.Sprintf("Fix the issue #%d (%s) in repository %s. Clone the repository, understand the issue, implement a fix, and create a pull request.", payload.IssueNumber, sanitizePromptInput(payload.IssueTitle, 500), sanitizePromptInput(payload.RepoFullName, 200)),
		}
	case model.TaskTypeGenTests:
		module := payload.Module
		if module == "" {
			module = "."
		}
		return []string{
			"claude", "-p",
			fmt.Sprintf("Generate tests for module %s in repository %s. Analyze existing code, identify untested paths, and write comprehensive unit tests.", sanitizePromptInput(module, 200), sanitizePromptInput(payload.RepoFullName, 200)),
		}
	default:
		return []string{"claude", "-p", fmt.Sprintf("Process task of type %s for repository %s.", sanitizePromptInput(string(payload.TaskType), 50), sanitizePromptInput(payload.RepoFullName, 200))}
	}
}

// parseCPULimit 将 CPU 限制字符串（如 "2.0"）转换为 NanoCPUs（整数）
// Docker API 使用 NanoCPUs：1 CPU = 1e9 NanoCPUs
func parseCPULimit(limit string) (int64, error) {
	limit = strings.TrimSpace(limit)
	if limit == "" {
		return 0, nil
	}
	val, err := strconv.ParseFloat(limit, 64)
	if err != nil {
		return 0, fmt.Errorf("无效的 CPU 限制值 %q: %w", limit, err)
	}
	if val <= 0 {
		return 0, fmt.Errorf("CPU 限制必须大于 0，当前值: %g", val)
	}
	return int64(math.Round(val * 1e9)), nil
}

// parseMemoryLimit 将内存限制字符串（如 "4g", "512m", "1024k"）转换为字节数
// 无后缀时单位为字节（如 "1024" 表示 1024 字节）
func parseMemoryLimit(limit string) (int64, error) {
	limit = strings.TrimSpace(limit)
	if limit == "" {
		return 0, nil
	}
	lower := strings.ToLower(limit)

	var multiplier int64 = 1
	numStr := lower

	switch {
	case strings.HasSuffix(lower, "g") || strings.HasSuffix(lower, "gb"):
		multiplier = 1024 * 1024 * 1024
		numStr = strings.TrimSuffix(strings.TrimSuffix(lower, "gb"), "g")
	case strings.HasSuffix(lower, "m") || strings.HasSuffix(lower, "mb"):
		multiplier = 1024 * 1024
		numStr = strings.TrimSuffix(strings.TrimSuffix(lower, "mb"), "m")
	case strings.HasSuffix(lower, "k") || strings.HasSuffix(lower, "kb"):
		multiplier = 1024
		numStr = strings.TrimSuffix(strings.TrimSuffix(lower, "kb"), "k")
	case strings.HasSuffix(lower, "b"):
		numStr = strings.TrimSuffix(lower, "b")
	}

	// 优先尝试整数解析，避免浮点精度损失；失败再回退浮点解析
	if intVal, intErr := strconv.ParseInt(numStr, 10, 64); intErr == nil {
		if intVal <= 0 {
			return 0, fmt.Errorf("内存限制必须大于 0，当前值: %d", intVal)
		}
		return intVal * multiplier, nil
	}

	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("无效的内存限制值 %q: %w", limit, err)
	}
	if val <= 0 {
		return 0, fmt.Errorf("内存限制必须大于 0，当前值: %g", val)
	}
	return int64(val * float64(multiplier)), nil
}
