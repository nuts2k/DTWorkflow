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

// sanitizeEnvValue 清理环境变量值，移除可能导致注入的字符。
// 当前实现等同于 sanitizeInput，预留为独立函数以便未来差异化处理，
// 例如：对环境变量中的 '=' 号进行转义或过滤、对特定 shell 元字符做额外清理等。
func sanitizeEnvValue(s string) string {
	return sanitizeInput(s)
}

// selectGiteaToken 根据任务类型选择注入到容器的 Gitea Token。
//
// fix_issue 任务需要在容器内 git push 到 auto-fix/* 分支并随后由 host 侧创建 PR，
// 为规避 Gitea"同一账号不能评审自己创建的 PR"的限制，单独走 fix 账号（GiteaTokenFix）；
// 其他任务（review_pr / analyze_issue / gen_tests 等）使用默认 GiteaToken。
// GiteaTokenFix 为空时回退到 GiteaToken，保持向后兼容。
func selectGiteaToken(config PoolConfig, taskType model.TaskType) string {
	if taskType == model.TaskTypeFixIssue && config.GiteaTokenFix != "" {
		return string(config.GiteaTokenFix)
	}
	return string(config.GiteaToken)
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
// 包含 Gitea、Claude API 凭证、Git 配置和任务相关信息
func buildContainerEnv(config PoolConfig, payload model.TaskPayload) []string {
	env := []string{
		fmt.Sprintf("GITEA_URL=%s", sanitizeEnvValue(config.GiteaURL)),
		fmt.Sprintf("GITEA_TOKEN=%s", sanitizeEnvValue(selectGiteaToken(config, payload.TaskType))),
		fmt.Sprintf("ANTHROPIC_API_KEY=%s", sanitizeEnvValue(string(config.ClaudeAPIKey))),
	}
	if config.ClaudeBaseURL != "" {
		env = append(env, fmt.Sprintf("ANTHROPIC_BASE_URL=%s", sanitizeEnvValue(config.ClaudeBaseURL)))
	}
	// 自签名证书场景：通知 entrypoint.sh 和 git 跳过 SSL 验证
	if config.GiteaInsecureSkipVerify {
		env = append(env, "GIT_SSL_NO_VERIFY=true")
	}
	// 注意：GITEA_URL 和 REPO_CLONE_URL 仅供 entrypoint.sh 的 clone 阶段使用。
	// entrypoint.sh 在 clone 完成后会 unset 这两个变量，确保 Claude Code 进程看不到它们。
	// 这是有意为之的安全设计：凭证信息不暴露给 AI 执行阶段。
	env = append(env,
		fmt.Sprintf("REPO_CLONE_URL=%s", sanitizeEnvValue(payload.CloneURL)),
		fmt.Sprintf("REPO_OWNER=%s", sanitizeEnvValue(payload.RepoOwner)),
		fmt.Sprintf("REPO_NAME=%s", sanitizeEnvValue(payload.RepoName)),
		fmt.Sprintf("REPO_FULL_NAME=%s", sanitizeEnvValue(payload.RepoFullName)),
		fmt.Sprintf("TASK_TYPE=%s", sanitizeEnvValue(string(payload.TaskType))),
	)

	// 按任务类型追加额外环境变量
	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		env = append(env,
			fmt.Sprintf("PR_NUMBER=%d", payload.PRNumber),
			fmt.Sprintf("HEAD_REF=%s", sanitizeEnvValue(payload.HeadRef)),
			fmt.Sprintf("BASE_REF=%s", sanitizeEnvValue(payload.BaseRef)),
			fmt.Sprintf("HEAD_SHA=%s", sanitizeEnvValue(payload.HeadSHA)),
		)
	case model.TaskTypeAnalyzeIssue, model.TaskTypeFixIssue:
		env = append(env,
			fmt.Sprintf("ISSUE_NUMBER=%d", payload.IssueNumber),
			fmt.Sprintf("ISSUE_TITLE=%s", sanitizeEnvValue(payload.IssueTitle)),
		)
		if payload.IssueRef != "" {
			env = append(env, fmt.Sprintf("ISSUE_REF=%s", sanitizeEnvValue(payload.IssueRef)))
		}
	case model.TaskTypeGenTests:
		if payload.Module != "" {
			env = append(env, fmt.Sprintf("MODULE=%s", sanitizeEnvValue(payload.Module)))
		}
		// M4.2：注入 BASE_REF 给 entrypoint 做前置 checkout
		// 空值允许：entrypoint 会回落到仓库默认分支
		env = append(env, fmt.Sprintf("BASE_REF=%s", sanitizeEnvValue(payload.BaseRef)))
		// M4.2：注入清洗后的 module key（entrypoint 用作分支名），空 module → "all"
		// 与 internal/test.ModuleKey 语义一致；由于 test → worker 已存在反向依赖，
		// 不能在此 import test，改用本地等价实现 moduleKeyForContainer。
		env = append(env, fmt.Sprintf("MODULE_SANITIZED=%s", moduleKeyForContainer(payload.Module)))
	}

	return env
}

// moduleKeyForContainer 将 module 路径映射为 auto-test 分支的稳定 key。
// 语义与 internal/test.ModuleKey + sanitizeBranchRef 保持一致：
//   - 空串（或清洗后全部被过滤）→ 回落 "all"
//   - 其它 → 仅保留 [A-Za-z0-9._-]，其他字符统一替换为 -，连续 - 合并为单个，
//     归一化 ".." / "-." / ".-" 序列，修剪首尾的 . 与 -
//
// 注意：worker 包不能 import internal/test（存在 test → worker 反向依赖），
// 因此在此维护一份等价实现。任何修改必须同步到 internal/test.ModuleKey。
func moduleKeyForContainer(module string) string {
	key := module
	if key == "" {
		key = "all"
	}
	key = sanitizeModuleBranchRef(key)
	if key == "" {
		key = "all"
	}
	return key
}

// sanitizeModuleBranchRef 是 internal/test.sanitizeBranchRef 的本地等价实现。
func sanitizeModuleBranchRef(s string) string {
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
	// 迭代归一化：消除 ".."、"-."、".-"、"--" 序列直到字符串稳定
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

// 注意：prompt 使用英文以获得更好的 AI 理解效果
// buildContainerCmd 根据任务类型构建容器执行命令
// 容器入口脚本（entrypoint.sh）已完成仓库 clone 和分支 checkout，
// 此处生成的命令在已就绪的仓库目录中执行。
//
// SECURITY: prompt injection risk
// 以下字段来源于外部用户输入，攻击者可控：
//   - payload.IssueTitle（Issue 标题）
//   - payload.RepoFullName（仓库全名，通过 fork 可控）
//   - payload.Module（模块路径）
//   - 以及其他通过 Webhook 传入的字段
//
// sanitizePromptInput 仅做长度截断和换行/NUL 清理，不足以防御 prompt injection。
//
// 当前缓解措施：
//   1. 容器隔离：ReadonlyRootfs、CapDrop ALL、no-new-privileges
//   2. 资源限制：CPU、内存、PID 数量上限
//   3. 网络隔离：独立 bridge 网络
//   4. 输入截断：sanitizePromptInput 限制各字段最大长度
//
// 后续改进计划：
//   - 考虑通过文件传入用户输入（而非命令行参数），降低注入面
//   - 将 system prompt 与 user prompt 分离，利用 Claude 的 system/user 角色区分
//   - 对高风险字段（如 IssueTitle）做更严格的字符白名单过滤
func buildContainerCmd(payload model.TaskPayload) []string {
	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		baseRef := payload.BaseRef
		if baseRef == "" {
			baseRef = "main"
		}
		return []string{
			"claude", "-p",
			fmt.Sprintf(
				"You are reviewing PR #%d in repository %s. "+
					"The repository has been cloned and the PR branch is checked out. "+
					"The base branch is '%s'. "+
					"Use 'git diff origin/%s...HEAD' to see the changes. "+
					"Explore the codebase to understand context — trace call chains, read related files, check project structure. "+
					"Provide a thorough code review covering: code quality, potential bugs, security concerns, and architecture impact.",
				payload.PRNumber,
				sanitizePromptInput(payload.RepoFullName, 200),
				sanitizePromptInput(baseRef, 100),
				sanitizePromptInput(baseRef, 100),
			),
		}
	case model.TaskTypeAnalyzeIssue:
		repoInfo := "The repository has been cloned to the current directory."
		if payload.IssueRef != "" {
			repoInfo = fmt.Sprintf(
				"The repository has been cloned and ref '%s' is checked out.",
				sanitizePromptInput(payload.IssueRef, 200))
		}
		return []string{
			"claude", "-p",
			fmt.Sprintf(
				"Analyze issue #%d (%s) in repository %s. "+
					"%s "+
					"Read the issue description and comments. "+
					"Explore the codebase to identify the root cause. "+
					"Provide a detailed analysis report with: root cause, affected files, suggested approach, and whether the information is sufficient for an automated fix.",
				payload.IssueNumber,
				sanitizePromptInput(payload.IssueTitle, 500),
				sanitizePromptInput(payload.RepoFullName, 200),
				repoInfo,
			),
		}
	case model.TaskTypeFixIssue:
		repoInfo := "The repository has been cloned to the current directory."
		if payload.IssueRef != "" {
			repoInfo = fmt.Sprintf(
				"The repository has been cloned and ref '%s' is checked out.",
				sanitizePromptInput(payload.IssueRef, 200))
		}
		return []string{
			"claude", "-p",
			fmt.Sprintf(
				"Fix issue #%d (%s) in repository %s. "+
					"%s "+
					"Analyze the issue, explore the codebase, implement a fix, and commit the changes.",
				payload.IssueNumber,
				sanitizePromptInput(payload.IssueTitle, 500),
				sanitizePromptInput(payload.RepoFullName, 200),
				repoInfo,
			),
		}
	case model.TaskTypeGenTests:
		module := payload.Module
		if module == "" {
			module = "."
		}
		return []string{
			"claude", "-p",
			fmt.Sprintf(
				"Generate tests for module %s in repository %s. "+
					"The repository has been cloned to the current directory. "+
					"Analyze existing code, identify untested paths, and write comprehensive unit tests.",
				sanitizePromptInput(module, 200),
				sanitizePromptInput(payload.RepoFullName, 200),
			),
		}
	default:
		return []string{
			"claude", "-p",
			fmt.Sprintf(
				"Process task of type %s for repository %s. "+
					"The repository has been cloned to the current directory.",
				sanitizePromptInput(string(payload.TaskType), 50),
				sanitizePromptInput(payload.RepoFullName, 200),
			),
		}
	}
}

// maxCPUCores CPU 限制上限，超过此值视为配置错误
const maxCPUCores = 32

// maxMemoryBytes 内存限制上限（64GB），超过此值视为配置错误
const maxMemoryBytes = 64 * 1024 * 1024 * 1024 // 64GB

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
	if val < 0 {
		return 0, fmt.Errorf("CPU 限制不可为负数，当前值: %g", val)
	}
	if val == 0 {
		return 0, nil
	}
	if val > maxCPUCores {
		return 0, fmt.Errorf("CPU 限制超出上限，当前值: %g，最大允许: %d 核", val, maxCPUCores)
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
		if intVal < 0 {
			return 0, fmt.Errorf("内存限制不可为负数，当前值: %d", intVal)
		}
		if intVal == 0 {
			return 0, nil
		}
		// 乘法前检查溢出：确保 intVal * multiplier 不会超出 int64 范围
		if multiplier > 0 && intVal > maxMemoryBytes/multiplier {
			return 0, fmt.Errorf("内存限制超出上限，当前值: %s，最大允许: 64GB", limit)
		}
		result := intVal * multiplier
		if result > maxMemoryBytes {
			return 0, fmt.Errorf("内存限制超出上限，当前值: %s，最大允许: 64GB", limit)
		}
		return result, nil
	}

	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("无效的内存限制值 %q: %w", limit, err)
	}
	if val < 0 {
		return 0, fmt.Errorf("内存限制不可为负数，当前值: %g", val)
	}
	if val == 0 {
		return 0, nil
	}
	result := int64(val * float64(multiplier))
	if result > maxMemoryBytes {
		return 0, fmt.Errorf("内存限制超出上限，当前值: %s，最大允许: 64GB", limit)
	}
	return result, nil
}
