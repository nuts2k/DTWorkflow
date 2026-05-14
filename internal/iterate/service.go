package iterate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/test"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// PoolRunner 容器执行接口（复用 queue 包的同名接口签名）。
type PoolRunner interface {
	RunWithCommandAndStdin(ctx context.Context, payload model.TaskPayload,
		cmd []string, stdinData []byte) (*worker.ExecutionResult, error)
}

// FixReviewResult 容器执行的完整结果。
type FixReviewResult struct {
	RawOutput string
	CLIMeta   *model.CLIMeta
	Output    *FixReviewOutput
	ParseErr  error
	ExitCode  int
}

// Service 迭代修复核心服务。
type Service struct {
	pool   PoolRunner
	logger *slog.Logger
}

// NewService 创建迭代修复服务。
func NewService(pool PoolRunner, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, logger: logger}
}

// Execute 在容器中执行修复任务。
func (s *Service) Execute(ctx context.Context, payload model.TaskPayload) (*FixReviewResult, error) {
	result := &FixReviewResult{}

	// 反序列化评审问题
	var issues []review.ReviewIssue
	if payload.ReviewIssues != "" {
		if err := json.Unmarshal([]byte(payload.ReviewIssues), &issues); err != nil {
			return result, fmt.Errorf("反序列化 ReviewIssues 失败: %w", err)
		}
	}
	if len(issues) == 0 {
		return result, fmt.Errorf("%w: 无评审问题", ErrNoChanges)
	}

	// 反序列化前几轮修复上下文
	var prevFixes []FixSummary
	if payload.PreviousFixes != "" {
		if err := json.Unmarshal([]byte(payload.PreviousFixes), &prevFixes); err != nil {
			s.logger.WarnContext(ctx, "反序列化 PreviousFixes 失败，忽略", "error", err)
		}
	}

	// 构造 prompt
	reportPath := strings.TrimSpace(payload.FixReportPath)
	if reportPath == "" {
		reportPath = BuildReportPath("docs/review_history", payload.PRNumber, payload.RoundNumber)
	}
	maxRounds := payload.IterationMaxRounds
	if maxRounds <= 0 {
		maxRounds = 3
	}
	prompt := BuildFixPrompt(FixPromptContext{
		Repo:          payload.RepoFullName,
		PRNumber:      payload.PRNumber,
		HeadRef:       payload.HeadRef,
		BaseRef:       payload.BaseRef,
		Issues:        issues,
		PreviousFixes: prevFixes,
		ReportPath:    reportPath,
		RoundNumber:   payload.RoundNumber,
		MaxRounds:     maxRounds,
	})

	// 容器执行
	cmd := []string{"claude", "-p", "--output-format", "json", "--dangerously-skip-permissions", "-"}
	execResult, err := s.pool.RunWithCommandAndStdin(ctx, payload, cmd, []byte(prompt))
	if err != nil {
		return result, err
	}
	if execResult != nil {
		result.RawOutput = execResult.Output
		result.ExitCode = execResult.ExitCode
	}
	if execResult != nil && execResult.ExitCode != 0 {
		if execResult.ExitCode == 2 {
			return result, fmt.Errorf("%w: 容器执行失败，退出码 %d", ErrFixReviewDeterministicFailure, execResult.ExitCode)
		}
		return result, fmt.Errorf("容器执行失败，退出码 %d", execResult.ExitCode)
	}

	// 解析 JSON 结果
	output, cliResp, parseErr := parseFixReviewOutput(result.RawOutput)
	if cliResp != nil {
		result.CLIMeta = &model.CLIMeta{
			CostUSD:    cliResp.EffectiveCostUSD(),
			DurationMs: cliResp.DurationMs,
			IsError:    cliResp.IsExecutionError(),
			NumTurns:   cliResp.NumTurns,
			SessionID:  cliResp.SessionID,
		}
	}
	if parseErr != nil {
		result.ParseErr = parseErr
		return result, fmt.Errorf("%w: %v", ErrFixReviewParseFailure, parseErr)
	}
	result.Output = output
	if CountFixedIssues(output) == 0 {
		return result, fmt.Errorf("%w: 结构化结果未包含 modified 或 alternative_chosen", ErrNoChanges)
	}

	return result, nil
}

// parseFixReviewOutput 从容器输出中提取结构化 JSON。
func parseFixReviewOutput(rawOutput string) (*FixReviewOutput, *review.CLIResponse, error) {
	cliResp, resultText, err := extractResultFromCLIOutput(rawOutput)
	if err != nil {
		return nil, cliResp, err
	}

	jsonStr := extractJSON(resultText)
	if jsonStr == "" {
		return nil, cliResp, fmt.Errorf("未找到 JSON 输出")
	}

	var output FixReviewOutput
	if err := json.Unmarshal([]byte(jsonStr), &output); err != nil {
		return nil, cliResp, fmt.Errorf("JSON 解析失败: %w", err)
	}
	return &output, cliResp, nil
}

// extractResultFromCLIOutput 从 Claude CLI JSON 输出中提取 result 字符串。
func extractResultFromCLIOutput(raw string) (*review.CLIResponse, string, error) {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var resp review.CLIResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if isCLIErrorResponse(resp) {
			return &resp, "", fmt.Errorf("Claude CLI 报告错误: type=%s, subtype=%s", resp.Type, resp.Subtype)
		}
		if resp.Type == "result" || resp.Type == "success" {
			return &resp, resp.Result, nil
		}
	}
	return nil, "", fmt.Errorf("未找到 type=result 的 CLI 输出行")
}

func isCLIErrorResponse(resp review.CLIResponse) bool {
	if resp.IsError {
		return true
	}
	respType := strings.ToLower(strings.TrimSpace(resp.Type))
	subtype := strings.ToLower(strings.TrimSpace(resp.Subtype))
	return subtype == "error" || strings.Contains(respType, "error")
}

// extractJSON 从文本中提取最外层 JSON 对象，使用大括号深度匹配避免嵌套截断。
func extractJSON(text string) string {
	start := strings.Index(text, "{")
	if start == -1 {
		return ""
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inString {
			escape = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				candidate := text[start : i+1]
				if json.Valid([]byte(candidate)) {
					return candidate
				}
			}
		}
	}
	return ""
}

// CountFixedIssues 统计实际修复的问题数（action=modified 或 alternative_chosen）。
func CountFixedIssues(output *FixReviewOutput) int {
	if output == nil {
		return 0
	}
	count := 0
	for _, fix := range output.Fixes {
		if fix.Action == "modified" || fix.Action == "alternative_chosen" {
			count++
		}
	}
	return count
}

// SanitizeFixReviewError 脱敏 fix_review 错误信息。
func SanitizeFixReviewError(errMsg string) string {
	return test.SanitizeErrorMessage(errMsg)
}
