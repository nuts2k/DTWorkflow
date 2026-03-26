package review

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// WritebackClient 回写所需的 Gitea API 窄接口，*gitea.Client 已满足此接口。
type WritebackClient interface {
	CreatePullReview(ctx context.Context, owner, repo string, index int64,
		opts gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error)
	GetPullRequestDiff(ctx context.Context, owner, repo string, index int64) (string, *gitea.Response, error)
}

// ReviewStore 评审结果持久化接口
type ReviewStore interface {
	SaveReviewResult(ctx context.Context, result *model.ReviewRecord) error
}

// Writer 负责将评审结果回写到 Gitea PR 评审
type Writer struct {
	gitea  WritebackClient
	store  ReviewStore  // 可选，nil 时跳过持久化
	logger *slog.Logger
}

// NewWriter 创建 Writer 实例。store 为可选参数，nil 时跳过持久化。
func NewWriter(giteaClient WritebackClient, store ReviewStore, logger *slog.Logger) *Writer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Writer{
		gitea:  giteaClient,
		store:  store,
		logger: logger,
	}
}

// WritebackInput 回写操作的输入参数
type WritebackInput struct {
	TaskID   string
	Owner    string
	Repo     string
	PRNumber int64
	HeadSHA  string
	Result   *ReviewResult
}

// MapResult 单个 issue 的行号映射结果
type MapResult struct {
	Issue    ReviewIssue
	Position int  // 映射到 diff 中的 position（语义 A = 新文件行号）
	Mapped   bool // 是否成功映射到 diff 行
}

// Write 将评审结果回写到 Gitea PR 评审，并持久化到 store。
// 返回 Gitea 评审 ID 和错误。store 持久化失败不影响 Gitea 回写结果。
func (w *Writer) Write(ctx context.Context, input WritebackInput) (giteaReviewID int64, err error) {
	result := input.Result
	if result == nil {
		return 0, fmt.Errorf("writeback: result 不能为 nil")
	}

	parseFailed := result.Review == nil || result.ParseError != nil

	// 8a: 获取 PR diff（失败则降级：所有 issues 归入 body）
	var diffText string
	diffErr := error(nil)
	if !parseFailed {
		diffText, _, diffErr = w.gitea.GetPullRequestDiff(ctx, input.Owner, input.Repo, input.PRNumber)
		if diffErr != nil {
			w.logger.WarnContext(ctx, "获取 PR diff 失败，降级为全量 body 模式",
				"pr", input.PRNumber, "error", diffErr)
		}
	}

	// 8b: 解析 diff
	var diffMap *DiffMap
	if diffErr == nil && diffText != "" {
		diffMap = ParseDiff(diffText)
	}

	// 8c: 将 issues 映射到 diff 行
	var mappedComments []gitea.ReviewComment
	var unmapped []ReviewIssue

	if !parseFailed && result.Review != nil {
		mapResults := mapIssuesToComments(diffMap, result.Review.Issues)
		for _, mr := range mapResults {
			if mr.Mapped {
				mappedComments = append(mappedComments, gitea.ReviewComment{
					Path:       mr.Issue.File,
					Body:       formatCommentBody(mr.Issue),
					NewLineNum: int64(mr.Position),
				})
			} else {
				unmapped = append(unmapped, mr.Issue)
			}
		}
	}

	// 8d: 生成评审正文
	var (
		durationSec float64
		costUSD     float64
		parseErr    error
		rawOutput   string
	)
	rawOutput = result.RawOutput
	parseErr = result.ParseError
	if result.CLIMeta != nil {
		durationSec = float64(result.CLIMeta.DurationMs) / 1000.0
		costUSD = result.CLIMeta.CostUSD
	}

	var reviewOutput *ReviewOutput
	if result.Review != nil {
		reviewOutput = result.Review
	}

	body := formatReviewBody(reviewOutput, unmapped, parseFailed, parseErr, rawOutput, durationSec, costUSD)

	// 8e: 映射 verdict
	var issues []ReviewIssue
	if result.Review != nil {
		issues = result.Review.Issues
	}
	state := mapVerdict(verdictFromResult(result), issues, parseFailed)

	// 8f: 一次原子调用 CreatePullReview
	var commitID string
	if input.HeadSHA != "" {
		commitID = input.HeadSHA
	}

	reviewOpts := gitea.CreatePullReviewOptions{
		State:    state,
		Body:     body,
		CommitID: commitID,
		Comments: mappedComments,
	}

	review, _, apiErr := w.gitea.CreatePullReview(ctx, input.Owner, input.Repo, input.PRNumber, reviewOpts)
	if apiErr != nil {
		return 0, fmt.Errorf("writeback: CreatePullReview 失败: %w", apiErr)
	}
	if review != nil {
		giteaReviewID = review.ID
	}

	// 8g: 持久化评审结果（失败仅记录日志，不影响回写结果）
	if w.store != nil {
		record := buildReviewRecord(input, result, giteaReviewID, parseFailed, "")
		if storeErr := w.store.SaveReviewResult(ctx, record); storeErr != nil {
			w.logger.ErrorContext(ctx, "持久化评审结果失败",
				"task_id", input.TaskID, "pr", input.PRNumber, "error", storeErr)
		}
	}

	return giteaReviewID, nil
}

// mapIssuesToComments 使用 DiffMap 将 issues 映射到 diff 行。
// diffMap 为 nil 时（diff 获取失败降级），所有 issues 标记为未映射。
func mapIssuesToComments(diffMap *DiffMap, issues []ReviewIssue) []MapResult {
	results := make([]MapResult, 0, len(issues))
	for _, issue := range issues {
		mr := MapResult{Issue: issue}
		if diffMap != nil && issue.File != "" && issue.Line > 0 {
			pos, ok := diffMap.MapLine(issue.File, issue.Line)
			if ok {
				mr.Mapped = true
				mr.Position = pos
			}
		}
		results = append(results, mr)
	}
	return results
}

// mapVerdict 将评审 verdict 转换为 Gitea ReviewStateType。
// 安全网：存在 CRITICAL 或 ERROR 级别 issue 时强制返回 REQUEST_CHANGES。
func mapVerdict(verdict VerdictType, issues []ReviewIssue, hasParseError bool) gitea.ReviewStateType {
	// 安全网：解析失败时使用 COMMENT（信息不足，不应 approve 或 request_changes）
	if hasParseError {
		return gitea.ReviewStateComment
	}

	// 安全网：存在高危 issue 时强制 REQUEST_CHANGES
	for _, issue := range issues {
		sev := strings.ToUpper(issue.Severity)
		if sev == "CRITICAL" || sev == "ERROR" {
			return gitea.ReviewStateRequestChanges
		}
	}

	switch verdict {
	case VerdictApprove:
		return gitea.ReviewStateApproved
	case VerdictRequestChanges:
		return gitea.ReviewStateRequestChanges
	case VerdictComment:
		return gitea.ReviewStateComment
	default:
		return gitea.ReviewStateComment
	}
}

// verdictFromResult 从 ReviewResult 中提取 VerdictType，nil 时返回 VerdictComment。
func verdictFromResult(result *ReviewResult) VerdictType {
	if result == nil || result.Review == nil {
		return VerdictComment
	}
	return result.Review.Verdict
}

// buildReviewRecord 构建持久化记录
func buildReviewRecord(input WritebackInput, result *ReviewResult, giteaReviewID int64, parseFailed bool, writebackError string) *model.ReviewRecord {
	record := &model.ReviewRecord{
		TaskID:        input.TaskID,
		RepoFullName:  input.Owner + "/" + input.Repo,
		PRNumber:      input.PRNumber,
		HeadSHA:       input.HeadSHA,
		GiteaReviewID: giteaReviewID,
		ParseFailed:   parseFailed,
		WritebackError: writebackError,
	}

	if result.CLIMeta != nil {
		record.CostUSD = result.CLIMeta.CostUSD
		record.DurationMs = result.CLIMeta.DurationMs
	}

	if result.Review != nil {
		record.Verdict = string(result.Review.Verdict)
		record.Summary = result.Review.Summary
		record.IssueCount = len(result.Review.Issues)

		// 统计各级别数量
		for _, issue := range result.Review.Issues {
			switch strings.ToUpper(issue.Severity) {
			case "CRITICAL":
				record.CriticalCount++
			case "ERROR":
				record.ErrorCount++
			case "WARNING":
				record.WarningCount++
			case "INFO":
				record.InfoCount++
			}
		}

		// 序列化 issues 为 JSON
		if data, err := json.Marshal(result.Review.Issues); err == nil {
			record.IssuesJSON = string(data)
		}
	}

	return record
}
