package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/iterate"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/test"
)

func (p *Processor) prepareFixReviewPayload(ctx context.Context, payload model.TaskPayload) (model.TaskPayload, error) {
	if payload.SessionID == 0 || payload.RoundNumber <= 1 {
		return payload, nil
	}
	payload.PreviousFixes = ""

	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	completedRounds, err := p.store.GetCompletedRoundsForSession(persistCtx, payload.SessionID)
	if err != nil {
		p.logger.WarnContext(ctx, "查询已完成迭代轮次失败，继续使用空 PreviousFixes",
			"session_id", payload.SessionID,
			"round", payload.RoundNumber,
			"error", err)
		return payload, nil
	}
	var fixes []iterate.FixSummary
	for _, r := range completedRounds {
		if r.RoundNumber >= payload.RoundNumber {
			continue
		}
		fixes = append(fixes, iterate.FixSummary{
			Round:       r.RoundNumber,
			IssuesFixed: r.IssuesFixed,
			Summary:     iterate.SanitizeFixReviewError(r.FixSummary),
		})
	}
	if len(fixes) == 0 {
		return payload, nil
	}
	data, err := json.Marshal(fixes)
	if err != nil {
		return payload, fmt.Errorf("%w: 序列化 PreviousFixes 失败: %v", iterate.ErrFixReviewDeterministicFailure, err)
	}
	payload.PreviousFixes = string(data)
	return payload, nil
}

func (p *Processor) repairSucceededFixReviewIterationState(ctx context.Context, record *model.TaskRecord, payload model.TaskPayload) error {
	p.logger.InfoContext(ctx, "fix_review 任务已成功，跳过容器重跑并修复迭代状态",
		"task_id", record.ID,
		"session_id", payload.SessionID,
		"round", payload.RoundNumber)
	iterateResult := &iterate.FixReviewResult{RawOutput: record.Result}
	if output, ok := decodeFixReviewOutput(record.Result); ok {
		iterateResult.Output = output
	} else {
		return fmt.Errorf("fix_review 已成功但无法解析历史结果用于状态修复")
	}
	if err := p.persistIterationFixResult(ctx, payload, record, iterateResult); err != nil {
		return fmt.Errorf("fix_review 迭代状态修复失败: %w", err)
	}
	return nil
}

func (p *Processor) persistIterationFixResult(ctx context.Context, payload model.TaskPayload, record *model.TaskRecord, result *iterate.FixReviewResult) error {
	if payload.SessionID == 0 || payload.RoundNumber == 0 || record == nil || result == nil || result.Output == nil {
		return fmt.Errorf("fix_review 迭代落库上下文不完整")
	}

	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	round, err := p.store.GetIterationRound(persistCtx, payload.SessionID, payload.RoundNumber)
	if err != nil {
		p.logger.ErrorContext(ctx, "查询迭代轮次失败",
			"session_id", payload.SessionID, "round", payload.RoundNumber, "error", err)
		return err
	}
	if round == nil {
		p.logger.WarnContext(ctx, "迭代轮次不存在，跳过修复结果落库",
			"session_id", payload.SessionID, "round", payload.RoundNumber)
		return fmt.Errorf("迭代轮次不存在: session_id=%d round=%d", payload.SessionID, payload.RoundNumber)
	}
	if round.FixTaskID != "" && round.FixTaskID != record.ID {
		p.logger.WarnContext(ctx, "迭代轮次 fix_task_id 不匹配，跳过修复结果落库",
			"session_id", payload.SessionID,
			"round", payload.RoundNumber,
			"round_fix_task_id", round.FixTaskID,
			"task_id", record.ID)
		return fmt.Errorf("迭代轮次 fix_task_id 不匹配: round_fix_task_id=%s task_id=%s", round.FixTaskID, record.ID)
	}

	alreadyCompleted := round.CompletedAt != nil
	issuesFixed := iterate.CountFixedIssues(result.Output)
	if !alreadyCompleted {
		now := time.Now()
		round.FixTaskID = record.ID
		round.IssuesFixed = issuesFixed
		round.FixSummary = iterate.SanitizeFixReviewError(result.Output.Summary)
		if strings.TrimSpace(round.FixReportPath) == "" {
			round.FixReportPath = payload.FixReportPath
		}
		if strings.TrimSpace(round.FixReportPath) == "" {
			round.FixReportPath = iterate.BuildReportPath("docs/review_history", payload.PRNumber, payload.RoundNumber)
		}
		round.CompletedAt = &now
		if err := p.store.UpdateIterationRound(persistCtx, round); err != nil {
			p.logger.ErrorContext(ctx, "更新迭代轮次修复结果失败",
				"session_id", payload.SessionID, "round", payload.RoundNumber, "error", err)
			return err
		}
	}

	session, err := p.store.FindActiveIterationSession(persistCtx, payload.RepoFullName, payload.PRNumber)
	if err != nil {
		p.logger.ErrorContext(ctx, "查询迭代会话失败",
			"repo", payload.RepoFullName, "pr", payload.PRNumber, "error", err)
		return err
	}
	if session == nil || session.ID != payload.SessionID {
		return fmt.Errorf("活跃迭代会话不存在或不匹配: session_id=%d", payload.SessionID)
	}
	totalIssuesFixed, err := recomputeIterationTotalIssuesFixed(persistCtx, p.store, payload.SessionID, round)
	if err != nil {
		p.logger.ErrorContext(ctx, "重算迭代会话修复统计失败",
			"session_id", payload.SessionID, "error", err)
		return err
	}
	session.TotalIssuesFixed = totalIssuesFixed
	if session.Status == "fixing" {
		session.Status = "reviewing"
	}
	session.LastError = ""
	if err := p.store.UpdateIterationSession(persistCtx, session); err != nil {
		p.logger.ErrorContext(ctx, "更新迭代会话修复统计失败",
			"session_id", session.ID, "error", err)
		return err
	}
	return nil
}

func (p *Processor) persistIterationFixFailure(ctx context.Context, payload model.TaskPayload, errMsg string) {
	if payload.SessionID == 0 {
		return
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.completeIterationRoundOnFailure(ctx, persistCtx, payload, errMsg)

	session, err := p.store.FindActiveIterationSession(persistCtx, payload.RepoFullName, payload.PRNumber)
	if err != nil {
		p.logger.ErrorContext(ctx, "查询迭代会话失败",
			"repo", payload.RepoFullName, "pr", payload.PRNumber, "error", err)
		return
	}
	if session == nil || session.ID != payload.SessionID {
		return
	}
	session.Status = "idle"
	session.LastError = iterate.SanitizeFixReviewError(errMsg)
	if err := p.store.UpdateIterationSession(persistCtx, session); err != nil {
		p.logger.ErrorContext(ctx, "更新迭代会话失败状态失败",
			"session_id", session.ID, "error", err)
	}
}

func (p *Processor) completeIterationRoundOnFailure(ctx context.Context, persistCtx context.Context, payload model.TaskPayload, summary string) {
	if payload.SessionID == 0 || payload.RoundNumber == 0 {
		return
	}
	round, err := p.store.GetIterationRound(persistCtx, payload.SessionID, payload.RoundNumber)
	if err != nil {
		p.logger.ErrorContext(ctx, "查询失败迭代轮次失败",
			"session_id", payload.SessionID, "round", payload.RoundNumber, "error", err)
		return
	}
	if round == nil || round.CompletedAt != nil {
		return
	}
	now := time.Now()
	round.IssuesFixed = 0
	round.FixSummary = iterate.SanitizeFixReviewError(summary)
	if strings.TrimSpace(round.FixReportPath) == "" {
		round.FixReportPath = payload.FixReportPath
	}
	if strings.TrimSpace(round.FixReportPath) == "" {
		round.FixReportPath = iterate.BuildReportPath("docs/review_history", payload.PRNumber, payload.RoundNumber)
	}
	round.CompletedAt = &now
	if err := p.store.UpdateIterationRound(persistCtx, round); err != nil {
		p.logger.ErrorContext(ctx, "标记失败迭代轮次完成失败",
			"session_id", payload.SessionID, "round", payload.RoundNumber, "error", err)
	}
}

func (p *Processor) persistIterationZeroFixResult(ctx context.Context, payload model.TaskPayload, record *model.TaskRecord, summary string) {
	if payload.SessionID == 0 || payload.RoundNumber == 0 || record == nil {
		return
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	round, err := p.store.GetIterationRound(persistCtx, payload.SessionID, payload.RoundNumber)
	if err != nil {
		p.logger.ErrorContext(ctx, "查询迭代轮次失败",
			"session_id", payload.SessionID, "round", payload.RoundNumber, "error", err)
		return
	}
	if round == nil {
		return
	}
	if round.FixTaskID != "" && round.FixTaskID != record.ID {
		p.logger.WarnContext(ctx, "迭代轮次 fix_task_id 不匹配，跳过零修复结果落库",
			"session_id", payload.SessionID,
			"round", payload.RoundNumber,
			"round_fix_task_id", round.FixTaskID,
			"task_id", record.ID)
		return
	}

	if round.CompletedAt == nil {
		now := time.Now()
		round.FixTaskID = record.ID
		round.IssuesFixed = 0
		round.FixSummary = iterate.SanitizeFixReviewError(summary)
		if strings.TrimSpace(round.FixReportPath) == "" {
			round.FixReportPath = payload.FixReportPath
		}
		if strings.TrimSpace(round.FixReportPath) == "" {
			round.FixReportPath = iterate.BuildReportPath("docs/review_history", payload.PRNumber, payload.RoundNumber)
		}
		round.CompletedAt = &now
		if err := p.store.UpdateIterationRound(persistCtx, round); err != nil {
			p.logger.ErrorContext(ctx, "更新迭代轮次零修复结果失败",
				"session_id", payload.SessionID, "round", payload.RoundNumber, "error", err)
			return
		}
	}

	session, err := p.store.FindActiveIterationSession(persistCtx, payload.RepoFullName, payload.PRNumber)
	if err != nil {
		p.logger.ErrorContext(ctx, "查询迭代会话失败",
			"repo", payload.RepoFullName, "pr", payload.PRNumber, "error", err)
		return
	}
	if session == nil || session.ID != payload.SessionID {
		return
	}
	if session.Status == "fixing" {
		session.Status = "reviewing"
	}
	session.LastError = iterate.SanitizeFixReviewError(summary)
	if err := p.store.UpdateIterationSession(persistCtx, session); err != nil {
		p.logger.ErrorContext(ctx, "更新迭代会话零修复状态失败",
			"session_id", session.ID, "error", err)
	}
}

func (p *Processor) sendIterationErrorNotification(ctx context.Context, record *model.TaskRecord) {
	if p.notifier == nil || record == nil {
		return
	}
	payload := record.Payload
	if payload.RepoOwner == "" || payload.RepoName == "" || payload.PRNumber <= 0 {
		return
	}

	body := fmt.Sprintf("PR #%d 迭代修复异常\n\n仓库：%s\n任务：%s\n状态：%s",
		payload.PRNumber, payload.RepoFullName, record.ID, record.Status)
	if record.Error != "" {
		body += fmt.Sprintf("\n错误：%s", record.Error)
	}
	metadata := map[string]string{
		notify.MetaKeyIterationRound:     fmt.Sprintf("%d", payload.RoundNumber),
		notify.MetaKeyIterationMaxRounds: fmt.Sprintf("%d", payload.IterationMaxRounds),
		notify.MetaKeyIterationSessionID: fmt.Sprintf("%d", payload.SessionID),
		notify.MetaKeyTaskStatus:         string(record.Status),
		notify.MetaKeyNotifyTime:         formatNotifyTime(),
	}
	if payload.PRTitle != "" {
		metadata[notify.MetaKeyPRTitle] = payload.PRTitle
	}
	msg := notify.Message{
		EventType: notify.EventIterationError,
		Severity:  notify.SeverityWarning,
		Target:    buildPRTarget(payload),
		Title:     fmt.Sprintf("PR #%d 迭代修复异常", payload.PRNumber),
		Body:      body,
		Metadata:  metadata,
	}
	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	if err := p.notifier.Send(notifyCtx, msg); err != nil {
		p.logger.ErrorContext(ctx, "发送迭代异常通知失败",
			"task_id", record.ID,
			"error", err)
	}
}

func (p *Processor) handleIterationQualityFailure(ctx context.Context, record *model.TaskRecord, runErr error, iterateResult *iterate.FixReviewResult, logMsg string) error {
	record.Status = model.TaskStatusFailed
	record.Error = test.SanitizeErrorMessage(runErr.Error())
	if iterateResult != nil {
		record.Result = iterateResult.RawOutput
	}
	completedAt := time.Now()
	record.CompletedAt = &completedAt
	p.logger.WarnContext(ctx, logMsg,
		"task_id", record.ID,
		"error", runErr,
	)

	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()
	if err := p.store.UpdateTask(persistCtx, record); err != nil {
		p.logger.ErrorContext(ctx, "更新任务最终状态失败",
			"task_id", record.ID,
			"status", record.Status,
			"error", err,
		)
	} else {
		p.persistIterationZeroFixResult(ctx, record.Payload, record, record.Error)
		p.sendIterationErrorNotification(ctx, record)
	}
	return fmt.Errorf("%s: %w", logMsg, asynq.SkipRetry)
}
