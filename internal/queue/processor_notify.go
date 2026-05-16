package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/code"
	"otws19.zicp.vip/kelin/dtworkflow/internal/e2e"
	"otws19.zicp.vip/kelin/dtworkflow/internal/fix"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/test"
)

// buildPRURL 基于 Gitea 配置构造 PR 页面链接。
// 调用方（WithGiteaBaseURL）已保证 giteaBaseURL 无尾斜杠。
func buildPRURL(giteaBaseURL string, payload model.TaskPayload) string {
	return fmt.Sprintf("%s/%s/%s/pulls/%d",
		giteaBaseURL,
		payload.RepoOwner,
		payload.RepoName,
		payload.PRNumber,
	)
}

// buildPRMetadata 构造 PR 相关通知的公共 metadata 字段
func (p *Processor) buildPRMetadata(payload model.TaskPayload) map[string]string {
	metadata := map[string]string{}
	if p.giteaBaseURL != "" && payload.PRNumber > 0 {
		metadata[notify.MetaKeyPRURL] = buildPRURL(p.giteaBaseURL, payload)
	}
	if payload.PRTitle != "" {
		metadata[notify.MetaKeyPRTitle] = payload.PRTitle
	}
	return metadata
}

// buildPRTarget 构造 PR 相关通知的 Target
func buildPRTarget(payload model.TaskPayload) notify.Target {
	return notify.Target{
		Owner:  payload.RepoOwner,
		Repo:   payload.RepoName,
		Number: payload.PRNumber,
		IsPR:   payload.PRNumber > 0,
	}
}

// formatIssueSummary 从 ReviewIssue 列表生成 issue 统计摘要
func formatIssueSummary(issues []review.ReviewIssue) string {
	if len(issues) == 0 {
		return ""
	}
	counts := make(map[string]int)
	for _, issue := range issues {
		severity := strings.ToUpper(issue.Severity)
		if severity == "" {
			severity = "UNKNOWN"
		}
		counts[severity]++
	}
	var parts []string
	for _, sev := range []string{"CRITICAL", "ERROR", "WARNING", "INFO"} {
		if c, ok := counts[sev]; ok {
			parts = append(parts, fmt.Sprintf("%d %s", c, sev))
		}
	}
	return strings.Join(parts, ", ")
}

func (p *Processor) sendStartNotification(ctx context.Context, payload model.TaskPayload) {
	if p.notifier == nil {
		return
	}
	msg, ok := p.buildStartMessage(payload)
	if !ok {
		return
	}
	if err := p.notifier.Send(ctx, msg); err != nil {
		p.logger.ErrorContext(ctx, "发送任务开始通知失败",
			"task_type", payload.TaskType,
			"error", err,
		)
	}
}

func (p *Processor) buildStartMessage(payload model.TaskPayload) (notify.Message, bool) {
	if payload.RepoOwner == "" || payload.RepoName == "" {
		return notify.Message{}, false
	}

	var msg notify.Message
	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		if payload.PRNumber <= 0 {
			return notify.Message{}, false
		}
		msg = notify.Message{
			EventType: notify.EventPRReviewStarted,
			Severity:  notify.SeverityInfo,
			Target:    buildPRTarget(payload),
			Title:     "PR 自动评审开始",
			Body:      fmt.Sprintf("正在评审 PR #%d\n\n仓库：%s", payload.PRNumber, payload.RepoFullName),
			Metadata:  p.buildPRMetadata(payload),
		}
	case model.TaskTypeAnalyzeIssue:
		if payload.IssueNumber <= 0 {
			return notify.Message{}, false
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata[notify.MetaKeyIssueURL] = fmt.Sprintf("%s/%s/%s/issues/%d",
				p.giteaBaseURL, payload.RepoOwner, payload.RepoName, payload.IssueNumber)
		}
		msg = notify.Message{
			EventType: notify.EventIssueAnalyzeStarted,
			Severity:  notify.SeverityInfo,
			Target: notify.Target{
				Owner:  payload.RepoOwner,
				Repo:   payload.RepoName,
				Number: payload.IssueNumber,
				IsPR:   false,
			},
			Title:    "Issue 自动分析开始",
			Body:     fmt.Sprintf("正在分析 Issue #%d\n\n仓库：%s", payload.IssueNumber, payload.RepoFullName),
			Metadata: metadata,
		}
	case model.TaskTypeFixIssue:
		if payload.IssueNumber <= 0 {
			return notify.Message{}, false
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata[notify.MetaKeyIssueURL] = fmt.Sprintf("%s/%s/%s/issues/%d",
				p.giteaBaseURL, payload.RepoOwner, payload.RepoName, payload.IssueNumber)
		}
		msg = notify.Message{
			EventType: notify.EventIssueFixStarted,
			Severity:  notify.SeverityInfo,
			Target: notify.Target{
				Owner:  payload.RepoOwner,
				Repo:   payload.RepoName,
				Number: payload.IssueNumber,
				IsPR:   false,
			},
			Title:    "Issue 自动修复开始",
			Body:     fmt.Sprintf("正在修复 Issue #%d\n\n仓库：%s", payload.IssueNumber, payload.RepoFullName),
			Metadata: metadata,
		}
	case model.TaskTypeGenTests:
		// gen_tests 的 fail-open 判据为 RepoFullName 非空（非 Number>0：整仓
		// 生成模式下 module 可空，不应因此屏蔽通知）。
		if payload.RepoFullName == "" {
			return notify.Message{}, false
		}
		metadata := map[string]string{
			notify.MetaKeyModule:    genTestsModuleLabel(payload.Module),
			notify.MetaKeyFramework: payload.Framework,
		}
		msg = notify.Message{
			EventType: notify.EventGenTestsStarted,
			Severity:  notify.SeverityInfo,
			Target: notify.Target{
				Owner: payload.RepoOwner,
				Repo:  payload.RepoName,
				IsPR:  false,
			},
			Title:    "自动测试生成开始",
			Body:     fmt.Sprintf("正在生成测试\n\n仓库：%s\nmodule：%s", payload.RepoFullName, genTestsModuleLabel(payload.Module)),
			Metadata: metadata,
		}
	case model.TaskTypeRunE2E:
		if payload.RepoFullName == "" {
			return notify.Message{}, false
		}
		metadata := map[string]string{}
		if payload.Environment != "" {
			metadata[notify.MetaKeyE2EEnv] = payload.Environment
		}
		if payload.Module != "" {
			metadata[notify.MetaKeyE2EModule] = payload.Module
		}
		if payload.BaseRef != "" {
			metadata[notify.MetaKeyE2EBaseRef] = payload.BaseRef
		}
		msg = notify.Message{
			EventType: notify.EventE2EStarted,
			Severity:  notify.SeverityInfo,
			Target: notify.Target{
				Owner: payload.RepoOwner,
				Repo:  payload.RepoName,
				IsPR:  false,
			},
			Title:    "E2E 测试开始",
			Body:     fmt.Sprintf("正在执行 E2E 测试\n\n仓库：%s", payload.RepoFullName),
			Metadata: metadata,
		}
	case model.TaskTypeTriageE2E:
		if payload.RepoFullName == "" {
			return notify.Message{}, false
		}
		metadata := p.buildPRMetadata(payload)
		msg = notify.Message{
			EventType: notify.EventE2ETriageStarted,
			Severity:  notify.SeverityInfo,
			Target:    buildPRTarget(payload),
			Title:     "E2E 回归分析开始",
			Body:      fmt.Sprintf("正在分析变更影响的 E2E 模块\n\n仓库：%s", payload.RepoFullName),
			Metadata:  metadata,
		}
	case model.TaskTypeFixReview:
		// 迭代修复的开始通知由迭代层管理，Processor 跳过
		return notify.Message{}, false
	case model.TaskTypeCodeFromDoc:
		if payload.RepoFullName == "" || payload.DocPath == "" {
			return notify.Message{}, false
		}
		branch := payload.HeadRef
		if branch == "" {
			branch = "auto-code/" + payload.DocSlug
		}
		metadata := map[string]string{
			notify.MetaKeyDocPath:    payload.DocPath,
			notify.MetaKeyBranchName: branch,
		}
		msg = notify.Message{
			EventType: notify.EventCodeFromDocStarted,
			Severity:  notify.SeverityInfo,
			Target: notify.Target{
				Owner: payload.RepoOwner,
				Repo:  payload.RepoName,
				IsPR:  false,
			},
			Title:    "文档驱动编码开始",
			Body:     fmt.Sprintf("正在根据文档实现代码\n\n仓库：%s\n文档：%s", payload.RepoFullName, payload.DocPath),
			Metadata: metadata,
		}
	default:
		return notify.Message{}, false
	}

	// 公共路径：统一注入通知时间
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata[notify.MetaKeyNotifyTime] = formatNotifyTime()
	return msg, true
}

// sendCompletionNotification 在任务达到最终状态且状态已持久化后发送完成通知。
// 内部创建独立后台 context，与 asynq 任务 ctx 的生命周期解耦，
// 确保即使 asynq ctx 已过期（如任务超时）仍能发出通知。
func (p *Processor) sendCompletionNotification(ctx context.Context, record *model.TaskRecord, reviewResult *review.ReviewResult, fixResult *fix.FixResult, testResult *test.TestGenResult, e2eResult *e2e.E2EResult, triageResult *e2e.TriageE2EOutput, codeResult *code.CodeFromDocResult) {
	if p.notifier == nil || record == nil {
		return
	}
	msg, ok := p.buildNotificationMessage(record, reviewResult, fixResult, testResult, e2eResult, triageResult, codeResult)
	if !ok {
		// 主消息未构建成功（例如 gen_tests 缺 RepoFullName）仍尝试 Warnings 追加——
		// 但无主消息时 Warnings 也无处附着，直接返回。
		p.sendTestGenWarnings(ctx, record, testResult)
		return
	}
	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	if err := p.notifier.Send(notifyCtx, msg); err != nil {
		p.logger.ErrorContext(ctx, "发送任务完成通知失败",
			"task_id", record.ID,
			"status", record.Status,
			"error", err,
		)
	}
	// 主消息发送后，如果 gen_tests 产出含 Warnings（例如分支强制对齐失败），
	// 追加一条独立的 Warning 消息，保持主消息干净 + 告警信息可达。
	p.sendTestGenWarnings(ctx, record, testResult)
	p.syncGenTestsPRComment(ctx, record, testResult)
}

// buildNotificationMessage 构建任务完成通知消息。
// M4.2：新增 testResult 形参，用于 gen_tests 任务（EventGenTestsDone / EventGenTestsFailed）。
// M5.1：新增 e2eResult 形参，用于 run_e2e 任务（EventE2EDone / EventE2EFailed）。
// M5.4：新增 triageResult 形参，用于 triage_e2e 任务（EventE2ETriageDone / EventE2ETriageFailed）。
// 其它类型调用时传 nil。
func (p *Processor) buildNotificationMessage(record *model.TaskRecord, reviewResult *review.ReviewResult, fixResult *fix.FixResult, testResult *test.TestGenResult, e2eResult *e2e.E2EResult, triageResult *e2e.TriageE2EOutput, codeResult *code.CodeFromDocResult) (notify.Message, bool) {
	if record == nil {
		return notify.Message{}, false
	}
	switch record.Status {
	case model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusRetrying:
		// 这三种状态都需要发送通知
	default:
		return notify.Message{}, false
	}

	payload := record.Payload
	if payload.RepoOwner == "" || payload.RepoName == "" {
		return notify.Message{}, false
	}

	// 构建通知正文
	var body string
	if record.Status == model.TaskStatusRetrying {
		body = fmt.Sprintf("任务执行失败，即将重试\n\n仓库：%s\n任务类型：%s", payload.RepoFullName, payload.TaskType)
	} else {
		body = fmt.Sprintf("任务 %s 执行完成\n\n仓库：%s\n任务类型：%s\n状态：%s", record.ID, payload.RepoFullName, payload.TaskType, record.Status)
	}
	if record.Error != "" {
		body += fmt.Sprintf("\n错误：%s", record.Error)
	}

	var msg notify.Message
	switch payload.TaskType {
	case model.TaskTypeReviewPR:
		if payload.PRNumber <= 0 {
			return notify.Message{}, false
		}
		metadata := p.buildPRMetadata(payload)
		if reviewResult != nil && reviewResult.Review != nil {
			metadata[notify.MetaKeyVerdict] = string(reviewResult.Review.Verdict)
			metadata[notify.MetaKeyIssueSummary] = formatIssueSummary(reviewResult.Review.Issues)
		}
		if record.Status == model.TaskStatusRetrying {
			metadata[notify.MetaKeyRetryCount] = fmt.Sprintf("%d", record.RetryCount+1)
			metadata[notify.MetaKeyMaxRetry] = fmt.Sprintf("%d", record.MaxRetry)
			metadata[notify.MetaKeyTaskStatus] = string(record.Status)
		}
		target := buildPRTarget(payload)
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventPRReviewDone,
				Severity:  notify.SeverityInfo,
				Target:    target,
				Title:     "PR 自动评审任务完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "PR 自动评审重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default: // failed
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "PR 自动评审任务失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeAnalyzeIssue:
		if payload.IssueNumber <= 0 {
			return notify.Message{}, false
		}
		issueTarget := notify.Target{
			Owner:  payload.RepoOwner,
			Repo:   payload.RepoName,
			Number: payload.IssueNumber,
			IsPR:   false,
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata[notify.MetaKeyIssueURL] = fmt.Sprintf("%s/%s/%s/issues/%d",
				p.giteaBaseURL, payload.RepoOwner, payload.RepoName, payload.IssueNumber)
		}
		if record.Status == model.TaskStatusRetrying {
			metadata[notify.MetaKeyRetryCount] = fmt.Sprintf("%d", record.RetryCount+1)
			metadata[notify.MetaKeyMaxRetry] = fmt.Sprintf("%d", record.MaxRetry)
			metadata[notify.MetaKeyTaskStatus] = string(record.Status)
		}
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventIssueAnalyzeDone,
				Severity:  notify.SeverityInfo,
				Target:    issueTarget,
				Title:     "Issue 自动分析完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    issueTarget,
				Title:     "Issue 自动分析重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default: // failed
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    issueTarget,
				Title:     "Issue 自动分析失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeFixIssue:
		if payload.IssueNumber <= 0 {
			return notify.Message{}, false
		}
		issueTarget := notify.Target{
			Owner:  payload.RepoOwner,
			Repo:   payload.RepoName,
			Number: payload.IssueNumber,
			IsPR:   false,
		}
		metadata := map[string]string{}
		if p.giteaBaseURL != "" {
			metadata[notify.MetaKeyIssueURL] = fmt.Sprintf("%s/%s/%s/issues/%d",
				p.giteaBaseURL, payload.RepoOwner, payload.RepoName, payload.IssueNumber)
		}
		if record.Status == model.TaskStatusRetrying {
			metadata[notify.MetaKeyRetryCount] = fmt.Sprintf("%d", record.RetryCount+1)
			metadata[notify.MetaKeyMaxRetry] = fmt.Sprintf("%d", record.MaxRetry)
			metadata[notify.MetaKeyTaskStatus] = string(record.Status)
		}
		if record.Status == model.TaskStatusSucceeded && fixResult != nil && fixResult.Fix != nil && fixResult.PRNumber > 0 {
			metadata[notify.MetaKeyPRURL] = fixResult.PRURL
			metadata[notify.MetaKeyPRNumber] = fmt.Sprintf("%d", fixResult.PRNumber)
			metadata[notify.MetaKeyModifiedFiles] = fmt.Sprintf("%d", len(fixResult.Fix.ModifiedFiles))
		}
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventFixIssueDone,
				Severity:  notify.SeverityInfo,
				Target:    issueTarget,
				Title:     "Issue 自动修复任务完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    issueTarget,
				Title:     "Issue 自动修复重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default: // failed
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    issueTarget,
				Title:     "Issue 自动修复任务失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeGenTests:
		// gen_tests 不绑定具体 PR/Issue 编号，Target 以整仓为单位。module 与 framework
		// 通过 metadata 透传；Severity 依 FailureCategory 区分。
		genTarget := notify.Target{
			Owner: payload.RepoOwner,
			Repo:  payload.RepoName,
			IsPR:  false,
		}
		metadata := p.buildGenTestsMetadata(payload, testResult)
		if record.Status == model.TaskStatusRetrying {
			metadata[notify.MetaKeyRetryCount] = fmt.Sprintf("%d", record.RetryCount+1)
			metadata[notify.MetaKeyMaxRetry] = fmt.Sprintf("%d", record.MaxRetry)
			metadata[notify.MetaKeyTaskStatus] = string(record.Status)
		}
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventGenTestsDone,
				Severity:  notify.SeverityInfo,
				Target:    genTarget,
				Title:     "自动测试生成完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			// 对齐 review/fix：重试中统一走 EventSystemError + Warning severity
			msg = notify.Message{
				EventType: notify.EventSystemError,
				Severity:  notify.SeverityWarning,
				Target:    genTarget,
				Title:     "自动测试生成重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default: // failed
			severity, title := genTestsFailedSeverity(testResult)
			msg = notify.Message{
				EventType: notify.EventGenTestsFailed,
				Severity:  severity,
				Target:    genTarget,
				Title:     title,
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeRunE2E:
		if payload.RepoFullName == "" {
			return notify.Message{}, false
		}
		metadata := map[string]string{}
		if payload.Environment != "" {
			metadata[notify.MetaKeyE2EEnv] = payload.Environment
		}
		if e2eResult != nil && e2eResult.Output != nil {
			metadata[notify.MetaKeyE2ETotalCases] = fmt.Sprintf("%d", e2eResult.Output.TotalCases)
			metadata[notify.MetaKeyE2EPassedCases] = fmt.Sprintf("%d", e2eResult.Output.PassedCases)
			metadata[notify.MetaKeyE2EFailedCases] = fmt.Sprintf("%d", e2eResult.Output.FailedCases)
			metadata[notify.MetaKeyE2EErrorCases] = fmt.Sprintf("%d", e2eResult.Output.ErrorCases)
			metadata[notify.MetaKeyE2ESkippedCases] = fmt.Sprintf("%d", e2eResult.Output.SkippedCases)

			// 构建失败用例列表 JSON
			if e2eResult.Output.FailedCases > 0 || e2eResult.Output.ErrorCases > 0 {
				type failedItem struct {
					Name     string `json:"name"`
					Category string `json:"category"`
					Analysis string `json:"analysis"`
				}
				var items []failedItem
				for _, c := range e2eResult.Output.Cases {
					if c.Status != "failed" && c.Status != "error" {
						continue
					}
					analysis := c.FailureAnalysis
					if len([]rune(analysis)) > 80 {
						analysis = string([]rune(analysis)[:80]) + "..."
					}
					items = append(items, failedItem{
						Name:     c.Module + "/" + c.Name,
						Category: c.FailureCategory,
						Analysis: analysis,
					})
				}
				if data, err := json.Marshal(items); err == nil {
					metadata[notify.MetaKeyE2EFailedList] = string(data)
				}
			}
		}
		// M5.2: 已创建的 Issue 号
		if e2eResult != nil && len(e2eResult.CreatedIssues) > 0 {
			var nums []string
			for _, n := range e2eResult.CreatedIssues {
				nums = append(nums, fmt.Sprintf("%d", n))
			}
			sort.Strings(nums)
			metadata[notify.MetaKeyE2ECreatedIssues] = strings.Join(nums, ",")
		}
		target := notify.Target{
			Owner: payload.RepoOwner,
			Repo:  payload.RepoName,
			IsPR:  false,
		}
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventE2EDone,
				Severity:  notify.SeverityInfo,
				Target:    target,
				Title:     "E2E 测试完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventE2EFailed,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "E2E 测试重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default:
			msg = notify.Message{
				EventType: notify.EventE2EFailed,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "E2E 测试失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeTriageE2E:
		if payload.RepoFullName == "" {
			return notify.Message{}, false
		}
		metadata := p.buildPRMetadata(payload)
		if triageResult != nil {
			if data, err := json.Marshal(triageResult.Modules); err == nil {
				metadata[notify.MetaKeyTriageModules] = string(data)
			}
			if data, err := json.Marshal(triageResult.SkippedModules); err == nil {
				metadata[notify.MetaKeyTriageSkippedModules] = string(data)
			}
			if triageResult.Analysis != "" {
				metadata[notify.MetaKeyTriageAnalysis] = triageResult.Analysis
			}
		}
		target := buildPRTarget(payload)
		switch record.Status {
		case model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventE2ETriageDone,
				Severity:  notify.SeverityInfo,
				Target:    target,
				Title:     "E2E 回归分析完成",
				Body:      body,
				Metadata:  metadata,
			}
		case model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventE2ETriageFailed,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "E2E 回归分析重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default:
			msg = notify.Message{
				EventType: notify.EventE2ETriageFailed,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "E2E 回归分析失败",
				Body:      body,
				Metadata:  metadata,
			}
		}
	case model.TaskTypeCodeFromDoc:
		if payload.RepoFullName == "" || payload.DocPath == "" {
			return notify.Message{}, false
		}
		target := buildCodeFromDocTarget(payload, codeResult)
		metadata := buildCodeFromDocMetadata(payload, codeResult)
		if record.Status == model.TaskStatusRetrying {
			metadata[notify.MetaKeyRetryCount] = fmt.Sprintf("%d", record.RetryCount+1)
			metadata[notify.MetaKeyMaxRetry] = fmt.Sprintf("%d", record.MaxRetry)
			metadata[notify.MetaKeyTaskStatus] = string(record.Status)
		}
		isCodeFromDocTestFailure := codeResult != nil &&
			codeResult.Output != nil &&
			codeResult.Output.FailureCategory == code.FailureCategoryTestFailure
		switch {
		case record.Status == model.TaskStatusSucceeded && isCodeFromDocTestFailure:
			msg = notify.Message{
				EventType: notify.EventCodeFromDocFailed,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "文档驱动编码测试失败",
				Body:      body,
				Metadata:  metadata,
			}
		case record.Status == model.TaskStatusSucceeded:
			msg = notify.Message{
				EventType: notify.EventCodeFromDocDone,
				Severity:  notify.SeverityInfo,
				Target:    target,
				Title:     "文档驱动编码完成",
				Body:      body,
				Metadata:  metadata,
			}
		case record.Status == model.TaskStatusRetrying:
			msg = notify.Message{
				EventType: notify.EventCodeFromDocFailed,
				Severity:  notify.SeverityWarning,
				Target:    target,
				Title:     "文档驱动编码重试中",
				Body:      body,
				Metadata:  metadata,
			}
		default:
			severity, title := codeFromDocFailedSeverity(codeResult)
			msg = notify.Message{
				EventType: notify.EventCodeFromDocFailed,
				Severity:  severity,
				Target:    target,
				Title:     title,
				Body:      body,
				Metadata:  metadata,
			}
		}
	default:
		return notify.Message{}, false
	}

	// 公共路径：统一注入通知时间和耗时
	if msg.Metadata == nil {
		msg.Metadata = map[string]string{}
	}
	msg.Metadata[notify.MetaKeyNotifyTime] = formatNotifyTime()
	if record.Status == model.TaskStatusSucceeded &&
		record.StartedAt != nil && record.CompletedAt != nil {
		msg.Metadata[notify.MetaKeyDuration] = formatDuration(
			record.CompletedAt.Sub(*record.StartedAt))
	}
	return msg, true
}

// genTestsModuleLabel gen_tests 通知里 module 字段的显示值：空串回落 "all"
// 以呼应 test.ModuleKey 的语义，避免飞书卡片出现空值。
func genTestsModuleLabel(module string) string {
	if strings.TrimSpace(module) == "" {
		return "all"
	}
	return module
}

// buildGenTestsMetadata 为 gen_tests 通知消息构造 metadata。
// testResult 为 nil 时仅回填 payload 维度字段（module / framework）；有输出时
// 追加 PR 元数据 / 文件计数 / failure_category（若有）。
func (p *Processor) buildGenTestsMetadata(payload model.TaskPayload, testResult *test.TestGenResult) map[string]string {
	metadata := map[string]string{
		notify.MetaKeyModule: genTestsModuleLabel(payload.Module),
	}
	framework := payload.Framework
	if testResult != nil && testResult.Framework != "" {
		framework = string(testResult.Framework)
	}
	if framework != "" {
		metadata[notify.MetaKeyFramework] = framework
	}
	if testResult == nil {
		return metadata
	}
	if testResult.PRURL != "" {
		metadata[notify.MetaKeyPRURL] = testResult.PRURL
	}
	if testResult.PRNumber > 0 {
		metadata[notify.MetaKeyPRNumber] = fmt.Sprintf("%d", testResult.PRNumber)
	}
	out := testResult.Output
	if out == nil {
		return metadata
	}
	metadata[notify.MetaKeyGeneratedCount] = fmt.Sprintf("%d", len(out.GeneratedFiles))
	metadata[notify.MetaKeyCommittedCount] = fmt.Sprintf("%d", len(out.CommittedFiles))
	metadata[notify.MetaKeySkippedCount] = fmt.Sprintf("%d", len(out.SkippedTargets))
	if out.FailureCategory != "" && out.FailureCategory != test.FailureCategoryNone {
		metadata[notify.MetaKeyFailureCategory] = string(out.FailureCategory)
	}
	return metadata
}

func buildCodeFromDocMetadata(payload model.TaskPayload, codeResult *code.CodeFromDocResult) map[string]string {
	branch := payload.HeadRef
	if codeResult != nil && codeResult.Output != nil && codeResult.Output.BranchName != "" {
		branch = codeResult.Output.BranchName
	}
	if branch == "" {
		branch = "auto-code/" + payload.DocSlug
	}
	metadata := map[string]string{
		notify.MetaKeyDocPath:    payload.DocPath,
		notify.MetaKeyBranchName: branch,
	}
	if codeResult == nil {
		return metadata
	}
	if codeResult.PRURL != "" {
		metadata[notify.MetaKeyPRURL] = codeResult.PRURL
	}
	if codeResult.PRNumber > 0 {
		metadata[notify.MetaKeyPRNumber] = fmt.Sprintf("%d", codeResult.PRNumber)
	}
	out := codeResult.Output
	if out == nil {
		return metadata
	}
	created, modified := countCodeFromDocFileActions(out.ModifiedFiles)
	metadata[notify.MetaKeyFilesCreated] = fmt.Sprintf("%d", created)
	metadata[notify.MetaKeyFilesModified] = fmt.Sprintf("%d", modified)
	metadata[notify.MetaKeyTestPassed] = fmt.Sprintf("%d", out.TestResults.Passed)
	metadata[notify.MetaKeyTestFailed] = fmt.Sprintf("%d", out.TestResults.Failed)
	if out.Implementation != "" {
		metadata[notify.MetaKeyImplementation] = out.Implementation
	}
	if out.FailureCategory != "" && out.FailureCategory != code.FailureCategoryNone {
		metadata[notify.MetaKeyFailureCategory] = string(out.FailureCategory)
	}
	if out.FailureReason != "" {
		metadata[notify.MetaKeyFailureReason] = out.FailureReason
	}
	if len(out.MissingInfo) > 0 {
		metadata[notify.MetaKeyMissingInfo] = strings.Join(out.MissingInfo, "\n")
	}
	return metadata
}

func buildCodeFromDocTarget(payload model.TaskPayload, codeResult *code.CodeFromDocResult) notify.Target {
	target := notify.Target{
		Owner: payload.RepoOwner,
		Repo:  payload.RepoName,
		IsPR:  false,
	}
	if codeResult != nil && codeResult.PRNumber > 0 {
		target.Number = codeResult.PRNumber
		target.IsPR = true
	}
	return target
}

func countCodeFromDocFileActions(files []code.ModifiedFile) (created, modified int) {
	for _, f := range files {
		switch f.Action {
		case "created":
			created++
		case "modified":
			modified++
		}
	}
	return created, modified
}

func codeFromDocFailedSeverity(codeResult *code.CodeFromDocResult) (notify.Severity, string) {
	if codeResult == nil || codeResult.Output == nil {
		return notify.SeverityWarning, "文档驱动编码失败"
	}
	switch codeResult.Output.FailureCategory {
	case code.FailureCategoryInfoInsufficient:
		return notify.SeverityInfo, "文档驱动编码信息不足"
	case code.FailureCategoryTestFailure:
		return notify.SeverityWarning, "文档驱动编码测试失败"
	case code.FailureCategoryInfrastructure:
		return notify.SeverityWarning, "文档驱动编码基础设施故障"
	default:
		return notify.SeverityWarning, "文档驱动编码失败"
	}
}

// genTestsFailedSeverity 按 FailureCategory 映射失败通知的 severity 与标题。
// 映射来源 §4.9.4：
//   - infrastructure   → Warning，"基础设施故障"
//   - test_quality     → Info，"测试质量未达标"
//   - info_insufficient → Info，"生成信息不足"
//   - 未知枚举值 / nil → Info，"自动测试生成失败"（默认 Info 避免误触发运维告警）
//   - testResult 为 nil（极端情况，parseResult 未产出）→ Warning，保留可见性
func genTestsFailedSeverity(testResult *test.TestGenResult) (notify.Severity, string) {
	if testResult == nil || testResult.Output == nil {
		return notify.SeverityWarning, "自动测试生成失败"
	}
	switch testResult.Output.FailureCategory {
	case test.FailureCategoryInfrastructure:
		return notify.SeverityWarning, "基础设施故障"
	case test.FailureCategoryTestQuality:
		return notify.SeverityInfo, "测试质量未达标"
	case test.FailureCategoryInfoInsufficient:
		return notify.SeverityInfo, "生成信息不足"
	default:
		// M7：未知 FailureCategory（例如未来 Claude 新增分类但后端未升级）默认走 Info，
		// 避免误触发运维分页。若真的是基础设施问题会由 infrastructure 枚举命中。
		return notify.SeverityInfo, "自动测试生成失败"
	}
}

// sendTestGenWarnings 若 gen_tests 产出含 Warnings（例如 entrypoint 写的
// AUTO_TEST_BRANCH_RESET_REMOTE_FAILED 提示），追加一条独立的 Warning 消息。
// 与主消息拆分的原因：主消息在 Succeeded 时走 Info 语义，Warnings 作为附加
// 告警需单独 Severity，便于飞书卡片着色 / 运维专人处理。
func (p *Processor) sendTestGenWarnings(ctx context.Context, record *model.TaskRecord, testResult *test.TestGenResult) {
	if p.notifier == nil || record == nil || testResult == nil || testResult.Output == nil {
		return
	}
	// 仅在最终态追加 Warnings 消息：retrying 态下 Warnings 可能随重试反复出现，
	// 每次都发会让运维误以为出了事；主消息已走 EventSystemError 通道。
	if record.Status != model.TaskStatusSucceeded && record.Status != model.TaskStatusFailed {
		return
	}
	warnings := testResult.Output.Warnings
	if len(warnings) == 0 {
		return
	}
	payload := record.Payload
	if payload.RepoOwner == "" || payload.RepoName == "" {
		return
	}
	metadata := p.buildGenTestsMetadata(payload, testResult)
	metadata[notify.MetaKeyNotifyTime] = formatNotifyTime()
	body := "gen_tests 产出附加告警：\n- " + strings.Join(warnings, "\n- ")
	msg := notify.Message{
		EventType: notify.EventGenTestsFailed,
		Severity:  notify.SeverityWarning,
		Target: notify.Target{
			Owner: payload.RepoOwner,
			Repo:  payload.RepoName,
			IsPR:  false,
		},
		Title:    "自动测试生成存在告警",
		Body:     body,
		Metadata: metadata,
	}
	if err := p.notifier.Send(ctx, msg); err != nil {
		p.logger.ErrorContext(ctx, "发送 gen_tests 附加告警消息失败",
			"task_id", record.ID,
			"error", err,
		)
	}
}

// syncGenTestsPRComment 在 gen_tests 最终态时，把最新结果 upsert 到目标 PR 评论中。
//
// 与通用 Router.Send 分离的原因：
//   - gen_tests started/done/failed 属于 repo 级通知，目标可能没有 PR/Issue 编号
//   - gen_tests PR 评论是显式的 Gitea 写回能力，应直接走专用接口，而非经路由配置
func (p *Processor) syncGenTestsPRComment(ctx context.Context, record *model.TaskRecord, testResult *test.TestGenResult) {
	if p.notifier == nil || record == nil || testResult == nil || testResult.Output == nil {
		return
	}
	if record.Payload.TaskType != model.TaskTypeGenTests {
		return
	}
	if record.Status != model.TaskStatusSucceeded && record.Status != model.TaskStatusFailed {
		return
	}
	if testResult.PRNumber <= 0 {
		return
	}
	payload := record.Payload
	if payload.RepoOwner == "" || payload.RepoName == "" {
		return
	}
	commenter, ok := p.notifier.(GenTestsPRCommenter)
	if !ok {
		return
	}

	body := test.FormatTestGenPRBody(testResult.Output, payload, string(testResult.Framework))
	if err := commenter.CommentOnGenTestsPR(ctx, payload.RepoOwner, payload.RepoName, testResult.PRNumber, body); err != nil {
		p.logger.ErrorContext(ctx, "同步 gen_tests PR 评论失败",
			"task_id", record.ID,
			"repo", payload.RepoFullName,
			"pr_number", testResult.PRNumber,
			"error", err,
		)
	}
}
