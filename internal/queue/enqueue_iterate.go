package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/iterate"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

// AfterReviewCompleted 在 review_pr 成功且 verdict=request_changes 时调用。
// 返回 true 表示已触发迭代（调用方应抑制 review 通知）。
func (h *EnqueueHandler) AfterReviewCompleted(ctx context.Context, task *model.TaskRecord, payload model.TaskPayload, labels []string, issues []review.ReviewIssue) bool {
	if h.iterateStore == nil || h.iterateCfg == nil {
		return false
	}

	cfg := h.iterateCfg.ResolveIterateConfig(payload.RepoFullName)
	if !cfg.Enabled {
		return false
	}
	currentLabels, ok := h.currentIterationLabels(ctx, payload, labels)
	if !ok || !iterate.ContainsLabel(currentLabels, cfg.Label) {
		return false
	}

	// 按严重等级过滤需要修复的问题
	filteredIssues := filterIssuesBySeverity(issues, cfg.FixSeverityThreshold)
	if len(filteredIssues) == 0 {
		return false
	}

	// 查找或创建迭代会话
	session, err := h.iterateStore.FindOrCreateIterationSession(ctx,
		payload.RepoFullName, payload.PRNumber, payload.HeadRef, cfg.MaxRounds)
	if err != nil {
		h.logger.ErrorContext(ctx, "创建迭代会话失败",
			"repo", payload.RepoFullName, "pr", payload.PRNumber, "error", err)
		return false
	}

	// 获取最新轮次，检查是否为恢复场景。
	latestRound, ok := h.ensureLatestFixReviewRoundCompleted(ctx, payload, session, "跳过本次迭代链式入队")
	if !ok {
		return false
	}

	// 上一轮修复完成，发 PR 评论摘要
	if latestRound != nil && latestRound.FixTaskID != "" && latestRound.IssuesFixed > 0 {
		h.postFixRoundComment(ctx, payload, latestRound.RoundNumber, latestRound.IssuesFixed, latestRound.FixReportPath)
	}

	// 检查是否为恢复场景：上一轮的 fix_review 失败后用户 push 了新提交
	isRecovery := false
	if latestRound != nil && latestRound.FixTaskID != "" {
		fixTask, _ := h.store.GetTask(ctx, latestRound.FixTaskID)
		if fixTask != nil && fixTask.Status == model.TaskStatusFailed {
			isRecovery = true
		}
	}

	// 检查是否超限（仅统计非 recovery 轮次）
	nonRecoveryRounds, err := h.iterateStore.CountNonRecoveryRounds(ctx, session.ID)
	if err != nil {
		h.logger.ErrorContext(ctx, "统计迭代轮次失败", "session_id", session.ID, "error", err)
		return false
	}
	if !isRecovery && nonRecoveryRounds >= session.MaxRounds {
		session.Status = "exhausted"
		_ = h.iterateStore.UpdateIterationSession(ctx, session)
		h.sendIterationTerminalNotification(ctx, payload, session, notify.EventIterationExhausted)
		h.updateIterationLabels(ctx, payload, "exhausted")
		return false
	}

	// 连续两轮零修复检查
	if !isRecovery && nonRecoveryRounds >= 2 {
		recentFixed, rfErr := h.iterateStore.GetRecentRoundsIssuesFixed(ctx, session.ID, 2)
		if rfErr == nil && len(recentFixed) >= 2 && recentFixed[0] == 0 && recentFixed[1] == 0 {
			session.Status = "exhausted"
			session.LastError = "连续两轮零修复"
			_ = h.iterateStore.UpdateIterationSession(ctx, session)
			h.sendIterationTerminalNotification(ctx, payload, session, notify.EventIterationExhausted)
			h.updateIterationLabels(ctx, payload, "exhausted")
			return false
		}
	}

	// 计算轮次号
	var roundNumber int
	if latestRound != nil {
		roundNumber = latestRound.RoundNumber + 1
	} else {
		roundNumber = 1
	}
	reportDir := strings.TrimSpace(cfg.ReportPath)
	if reportDir == "" {
		reportDir = "docs/review_history"
	}
	fixReportPath := iterate.BuildReportPath(reportDir, payload.PRNumber, roundNumber)

	// 更新会话状态：→ fixing
	session.Status = "fixing"
	session.CurrentRound = roundNumber
	session.TotalIssuesFound += len(filteredIssues)
	if err := h.iterateStore.UpdateIterationSession(ctx, session); err != nil {
		h.logger.ErrorContext(ctx, "更新迭代会话状态为 fixing 失败",
			"session_id", session.ID, "error", err)
		return false
	}

	// 创建轮次记录
	round := &store.IterationRoundRecord{
		SessionID:     session.ID,
		RoundNumber:   roundNumber,
		ReviewTaskID:  task.ID,
		IssuesFound:   len(filteredIssues),
		FixReportPath: fixReportPath,
		IsRecovery:    isRecovery,
	}
	if err := h.iterateStore.CreateIterationRound(ctx, round); err != nil {
		h.logger.ErrorContext(ctx, "创建迭代轮次失败",
			"session_id", session.ID, "round", roundNumber, "error", err)
		return false
	}

	// 首次迭代（round 1 且非 recovery）添加 iterating 标签
	if roundNumber == 1 && !isRecovery {
		h.updateIterationLabels(ctx, payload, "start")
	}

	// 序列化问题列表
	issuesJSON, _ := json.Marshal(filteredIssues)

	// 构建前几轮修复上下文
	var previousFixesJSON string
	if roundNumber > 1 {
		completedRounds, crErr := h.iterateStore.GetCompletedRoundsForSession(ctx, session.ID)
		if crErr != nil {
			h.logger.WarnContext(ctx, "查询已完成轮次失败，PreviousFixes 为空",
				"session_id", session.ID, "error", crErr)
		} else if len(completedRounds) > 0 {
			var fixes []iterate.FixSummary
			for _, r := range completedRounds {
				if r.RoundNumber >= roundNumber {
					continue
				}
				fixes = append(fixes, iterate.FixSummary{
					Round:       r.RoundNumber,
					IssuesFixed: r.IssuesFixed,
					Summary:     iterate.SanitizeFixReviewError(r.FixSummary),
				})
			}
			if data, err := json.Marshal(fixes); err == nil {
				previousFixesJSON = string(data)
			}
		}
	}

	deliveryID := fmt.Sprintf("iterate-%d:fix_review:%d", session.ID, roundNumber)
	fixPayload := model.TaskPayload{
		TaskType:           model.TaskTypeFixReview,
		DeliveryID:         deliveryID,
		RepoOwner:          payload.RepoOwner,
		RepoName:           payload.RepoName,
		RepoFullName:       payload.RepoFullName,
		CloneURL:           payload.CloneURL,
		PRNumber:           payload.PRNumber,
		PRTitle:            payload.PRTitle,
		BaseRef:            payload.BaseRef,
		HeadRef:            payload.HeadRef,
		HeadSHA:            payload.HeadSHA,
		SessionID:          session.ID,
		RoundNumber:        roundNumber,
		ReviewIssues:       string(issuesJSON),
		PreviousFixes:      previousFixesJSON,
		FixReportPath:      fixReportPath,
		IterationMaxRounds: session.MaxRounds,
	}

	fixRecord := &model.TaskRecord{
		TaskType:     model.TaskTypeFixReview,
		Priority:     model.PriorityHigh,
		RepoFullName: payload.RepoFullName,
		PRNumber:     payload.PRNumber,
		DeliveryID:   deliveryID,
	}

	if err := h.enqueueTask(ctx, fixPayload, fixRecord); err != nil {
		h.logger.ErrorContext(ctx, "入队 fix_review 失败",
			"session_id", session.ID, "round", roundNumber, "error", err)
		return false
	}

	// 更新轮次的 fix_task_id
	round.FixTaskID = fixRecord.ID
	if err := h.iterateStore.UpdateIterationRound(ctx, round); err != nil {
		h.logger.WarnContext(ctx, "更新迭代轮次 fix_task_id 失败",
			"session_id", session.ID,
			"round", roundNumber,
			"fix_task_id", fixRecord.ID,
			"error", err)
	}

	h.logger.InfoContext(ctx, "fix_review 已入队",
		"task_id", fixRecord.ID, "session_id", session.ID,
		"round", roundNumber, "issues", len(filteredIssues))

	// I-4: notification_mode=progress 时发送迭代进度通知
	if h.iterateCfg != nil {
		cfg2 := h.iterateCfg.ResolveIterateConfig(payload.RepoFullName)
		if cfg2.NotificationMode == "progress" {
			h.sendIterationProgressNotification(ctx, payload, session, roundNumber, len(filteredIssues))
		}
	}

	return true
}

func (h *EnqueueHandler) currentIterationLabels(ctx context.Context, payload model.TaskPayload, fallback []string) ([]string, bool) {
	if h.iterateLabels == nil {
		return fallback, true
	}
	labels, err := h.iterateLabels.ListLabels(ctx, payload.RepoOwner, payload.RepoName, payload.PRNumber)
	if err != nil {
		h.logger.WarnContext(ctx, "刷新 PR 标签失败，跳过迭代修复以避免使用过期标签",
			"repo", payload.RepoFullName,
			"pr", payload.PRNumber,
			"error", err)
		return nil, false
	}
	return labels, true
}

func (h *EnqueueHandler) latestFixReviewTaskTerminal(ctx context.Context, latestRound *store.IterationRoundRecord) bool {
	if latestRound == nil || latestRound.FixTaskID == "" {
		return false
	}
	fixTask, err := h.store.GetTask(ctx, latestRound.FixTaskID)
	if err != nil || fixTask == nil {
		return false
	}
	switch fixTask.Status {
	case model.TaskStatusFailed, model.TaskStatusCancelled:
		return true
	default:
		return false
	}
}

func (h *EnqueueHandler) tryCompleteSucceededFixReviewRound(ctx context.Context, payload model.TaskPayload, session *store.IterationSessionRecord, latestRound *store.IterationRoundRecord) (*store.IterationRoundRecord, error) {
	if latestRound == nil || latestRound.FixTaskID == "" || latestRound.CompletedAt != nil {
		return nil, nil
	}
	fixTask, err := h.store.GetTask(ctx, latestRound.FixTaskID)
	if err != nil || fixTask == nil {
		return nil, nil
	}
	if fixTask.Status != model.TaskStatusSucceeded {
		return nil, nil
	}
	issuesFixed := countFixedIssuesFromTaskResult(fixTask.Result)
	now := time.Now()
	latestRound.CompletedAt = &now
	latestRound.IssuesFixed = issuesFixed
	if strings.TrimSpace(latestRound.FixSummary) == "" {
		latestRound.FixSummary = extractFixReviewSummary(fixTask.Result)
		if strings.TrimSpace(latestRound.FixSummary) == "" {
			latestRound.FixSummary = "上一轮 fix_review 已成功，但迭代轮次状态由后续 review 自愈补齐"
		}
	}
	latestRound.FixSummary = iterate.SanitizeFixReviewError(latestRound.FixSummary)
	if strings.TrimSpace(latestRound.FixReportPath) == "" {
		latestRound.FixReportPath = fixTask.Payload.FixReportPath
	}
	if strings.TrimSpace(latestRound.FixReportPath) == "" {
		latestRound.FixReportPath = iterate.BuildReportPath("docs/review_history", payload.PRNumber, latestRound.RoundNumber)
	}
	if err := h.iterateStore.UpdateIterationRound(ctx, latestRound); err != nil {
		return nil, err
	}
	if session != nil {
		totalIssuesFixed, err := recomputeIterationTotalIssuesFixed(ctx, h.iterateStore, session.ID, latestRound)
		if err != nil {
			return nil, err
		}
		session.TotalIssuesFixed = totalIssuesFixed
		if session.Status == "fixing" {
			session.Status = "reviewing"
		}
		session.LastError = ""
		if err := h.iterateStore.UpdateIterationSession(ctx, session); err != nil {
			return nil, err
		}
	}
	return latestRound, nil
}

func recomputeIterationTotalIssuesFixed(ctx context.Context, lister completedIterationRoundsLister, sessionID int64, fallback *store.IterationRoundRecord) (int, error) {
	rounds, err := lister.GetCompletedRoundsForSession(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	total := 0
	seen := make(map[string]struct{}, len(rounds)+1)
	for _, round := range rounds {
		if round == nil || round.CompletedAt == nil {
			continue
		}
		key := iterationRoundIdentity(round)
		seen[key] = struct{}{}
		total += round.IssuesFixed
	}
	if fallback != nil && fallback.CompletedAt != nil {
		key := iterationRoundIdentity(fallback)
		if _, ok := seen[key]; !ok {
			total += fallback.IssuesFixed
		}
	}
	return total, nil
}

func iterationRoundIdentity(round *store.IterationRoundRecord) string {
	if round.ID != 0 {
		return fmt.Sprintf("id:%d", round.ID)
	}
	return fmt.Sprintf("session:%d:round:%d", round.SessionID, round.RoundNumber)
}

func countFixedIssuesFromTaskResult(raw string) int {
	output, ok := decodeFixReviewOutput(raw)
	if !ok {
		return 0
	}
	return iterate.CountFixedIssues(output)
}

func extractFixReviewSummary(raw string) string {
	output, ok := decodeFixReviewOutput(raw)
	if !ok || output == nil {
		return ""
	}
	return iterate.SanitizeFixReviewError(output.Summary)
}

func decodeFixReviewOutput(raw string) (*iterate.FixReviewOutput, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	for _, text := range candidateFixReviewResultTexts(raw) {
		jsonText := extractFirstJSONObject(text)
		if jsonText == "" {
			continue
		}
		var output iterate.FixReviewOutput
		if err := json.Unmarshal([]byte(jsonText), &output); err == nil {
			return &output, true
		}
	}
	return nil, false
}

func candidateFixReviewResultTexts(raw string) []string {
	var candidates []string
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var resp review.CLIResponse
		if err := json.Unmarshal([]byte(line), &resp); err == nil && (resp.Type == "result" || resp.Type == "success" || resp.Type == "") {
			candidates = append(candidates, resp.Result)
		}
	}
	candidates = append(candidates, raw)
	return candidates
}

func extractFirstJSONObject(text string) string {
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

func (h *EnqueueHandler) waitForIterationRoundCompleted(ctx context.Context, sessionID int64, roundNumber int) (*store.IterationRoundRecord, error) {
	timeout := h.roundWaitTimeout
	if timeout <= 0 {
		timeout = defaultIterationRoundWaitTimeout
	}
	interval := h.roundPollInterval
	if interval <= 0 {
		interval = defaultIterationRoundPollInterval
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		round, err := h.iterateStore.GetIterationRound(waitCtx, sessionID, roundNumber)
		if err != nil {
			return nil, err
		}
		if round != nil && round.CompletedAt != nil {
			return round, nil
		}

		select {
		case <-waitCtx.Done():
			return round, waitCtx.Err()
		case <-ticker.C:
		}
	}
}

func (h *EnqueueHandler) ensureLatestFixReviewRoundCompleted(ctx context.Context, payload model.TaskPayload, session *store.IterationSessionRecord, skipAction string) (*store.IterationRoundRecord, bool) {
	latestRound, err := h.iterateStore.GetLatestRound(ctx, session.ID)
	if err != nil {
		h.logger.WarnContext(ctx, "查询最新迭代轮次失败，"+skipAction,
			"session_id", session.ID,
			"error", err)
		return nil, false
	}

	if latestRound != nil && latestRound.FixTaskID != "" && latestRound.CompletedAt == nil {
		if completed, recoverErr := h.tryCompleteSucceededFixReviewRound(ctx, payload, session, latestRound); recoverErr != nil {
			h.logger.WarnContext(ctx, "自愈已成功 fix_review 轮次失败，"+skipAction,
				"session_id", session.ID,
				"latest_round", latestRound.RoundNumber,
				"fix_task_id", latestRound.FixTaskID,
				"error", recoverErr)
			return nil, false
		} else if completed != nil {
			latestRound = completed
		} else if h.latestFixReviewTaskTerminal(ctx, latestRound) {
			now := time.Now()
			latestRound.CompletedAt = &now
			latestRound.IssuesFixed = 0
			if strings.TrimSpace(latestRound.FixSummary) == "" {
				latestRound.FixSummary = "上一轮 fix_review 已终止，等待用户新提交恢复"
			}
			if err := h.iterateStore.UpdateIterationRound(ctx, latestRound); err != nil {
				h.logger.WarnContext(ctx, "标记已终止 fix_review 轮次完成失败，"+skipAction,
					"session_id", session.ID,
					"latest_round", latestRound.RoundNumber,
					"fix_task_id", latestRound.FixTaskID,
					"error", err)
				return nil, false
			}
		}
	}

	if latestRound != nil && latestRound.FixTaskID != "" && latestRound.CompletedAt == nil {
		waitedRound, waitErr := h.waitForIterationRoundCompleted(ctx, session.ID, latestRound.RoundNumber)
		if waitErr != nil {
			h.logger.WarnContext(ctx, "上一轮 fix_review 尚未完成落库，"+skipAction,
				"session_id", session.ID,
				"latest_round", latestRound.RoundNumber,
				"fix_task_id", latestRound.FixTaskID,
				"error", waitErr)
			return nil, false
		}
		latestRound = waitedRound
	}

	if latestRound != nil && latestRound.FixTaskID != "" && latestRound.CompletedAt == nil {
		h.logger.WarnContext(ctx, "上一轮 fix_review 仍未完成落库，"+skipAction,
			"session_id", session.ID,
			"latest_round", latestRound.RoundNumber,
			"fix_task_id", latestRound.FixTaskID)
		return nil, false
	}

	return latestRound, true
}

// AfterIterationApproved 在迭代中的 review_pr verdict=approve 时调用。
// 将会话标记为 completed，更新标签，发送终态通知。
func (h *EnqueueHandler) AfterIterationApproved(ctx context.Context, payload model.TaskPayload) {
	if h.iterateStore == nil {
		return
	}
	session, err := h.iterateStore.FindActiveIterationSession(ctx, payload.RepoFullName, payload.PRNumber)
	if err != nil || session == nil {
		return
	}
	latestRound, ok := h.ensureLatestFixReviewRoundCompleted(ctx, payload, session, "跳过迭代完成")
	if !ok {
		return
	}
	if latestRound != nil {
		totalIssuesFixed, err := recomputeIterationTotalIssuesFixed(ctx, h.iterateStore, session.ID, latestRound)
		if err != nil {
			h.logger.WarnContext(ctx, "重算迭代会话修复统计失败，跳过迭代完成",
				"session_id", session.ID,
				"error", err)
			return
		}
		session.TotalIssuesFixed = totalIssuesFixed
	}
	session.Status = "completed"
	if err := h.iterateStore.UpdateIterationSession(ctx, session); err != nil {
		h.logger.WarnContext(ctx, "更新迭代会话完成状态失败",
			"session_id", session.ID,
			"error", err)
		return
	}
	h.sendIterationTerminalNotification(ctx, payload, session, notify.EventIterationPassed)
	h.updateIterationLabels(ctx, payload, "passed")
}

// updateIterationLabels 根据迭代阶段更新 PR 标签。
func (h *EnqueueHandler) updateIterationLabels(ctx context.Context, payload model.TaskPayload, phase string) {
	if h.iterateLabels == nil {
		return
	}
	owner, repo := payload.RepoOwner, payload.RepoName
	pr := payload.PRNumber

	switch phase {
	case "start":
		_ = h.iterateLabels.AddLabel(ctx, owner, repo, pr, "iterating")
	case "passed":
		_ = h.iterateLabels.RemoveLabel(ctx, owner, repo, pr, "iterating")
		_ = h.iterateLabels.AddLabel(ctx, owner, repo, pr, "iterate-passed")
	case "exhausted":
		_ = h.iterateLabels.RemoveLabel(ctx, owner, repo, pr, "iterating")
		_ = h.iterateLabels.AddLabel(ctx, owner, repo, pr, "iterate-exhausted")
	}
}

// postFixRoundComment 在 fix_review 完成后发 PR 精简评论。
func (h *EnqueueHandler) postFixRoundComment(ctx context.Context, payload model.TaskPayload, round int, issuesFixed int, reportPath string) {
	if h.iterateCommenter == nil {
		return
	}
	body := fmt.Sprintf(
		"**迭代修复 Round %d 完成**\n\n"+
			"- 修复问题数: %d\n"+
			"- 详细报告: `%s`\n\n"+
			"已推送新提交，等待重新评审。",
		round, issuesFixed, reportPath)
	if err := h.iterateCommenter.CreateComment(ctx, payload.RepoOwner, payload.RepoName, payload.PRNumber, body); err != nil {
		h.logger.WarnContext(ctx, "发送迭代修复 PR 评论失败",
			"pr", payload.PRNumber, "round", round, "error", err)
	}
}

// sendIterationTerminalNotification 发送迭代终态通知。
func (h *EnqueueHandler) sendIterationTerminalNotification(ctx context.Context, payload model.TaskPayload, session *store.IterationSessionRecord, eventType notify.EventType) {
	if h.iterateNotifier == nil {
		return
	}
	var title, body string
	switch eventType {
	case notify.EventIterationPassed:
		title = fmt.Sprintf("PR #%d 迭代完成", payload.PRNumber)
		body = fmt.Sprintf("评审通过，共 %d 轮修复 %d 个问题",
			session.CurrentRound, session.TotalIssuesFixed)
	case notify.EventIterationExhausted:
		title = fmt.Sprintf("PR #%d 迭代修复达到上限", payload.PRNumber)
		body = fmt.Sprintf("%d 轮后仍有问题，需人工介入", session.CurrentRound)
	}
	severity := notify.SeverityInfo
	if eventType == notify.EventIterationExhausted {
		severity = notify.SeverityWarning
	}
	metadata := map[string]string{
		notify.MetaKeyIterationRound:     fmt.Sprintf("%d", session.CurrentRound),
		notify.MetaKeyIterationMaxRounds: fmt.Sprintf("%d", session.MaxRounds),
		notify.MetaKeyIterationSessionID: fmt.Sprintf("%d", session.ID),
		notify.MetaKeyNotifyTime:         formatNotifyTime(),
	}
	if payload.PRTitle != "" {
		metadata[notify.MetaKeyPRTitle] = payload.PRTitle
	}
	msg := notify.Message{
		EventType: eventType,
		Severity:  severity,
		Target: notify.Target{
			Owner:  payload.RepoOwner,
			Repo:   payload.RepoName,
			Number: payload.PRNumber,
			IsPR:   true,
		},
		Title:    title,
		Body:     body,
		Metadata: metadata,
	}
	if err := h.iterateNotifier.Send(ctx, msg); err != nil {
		h.logger.WarnContext(ctx, "发送迭代终态通知失败",
			"event", eventType, "error", err)
	}
}

// sendIterationProgressNotification 在 fix_review 入队后发送迭代进度通知。
func (h *EnqueueHandler) sendIterationProgressNotification(ctx context.Context, payload model.TaskPayload, session *store.IterationSessionRecord, roundNumber int, issueCount int) {
	if h.iterateNotifier == nil {
		return
	}
	title := fmt.Sprintf("PR #%d 迭代修复 Round %d 启动", payload.PRNumber, roundNumber)
	body := fmt.Sprintf("发现 %d 个问题，已入队 fix_review 自动修复（%d/%d 轮）",
		issueCount, roundNumber, session.MaxRounds)
	metadata := map[string]string{
		notify.MetaKeyIterationRound:     fmt.Sprintf("%d", roundNumber),
		notify.MetaKeyIterationMaxRounds: fmt.Sprintf("%d", session.MaxRounds),
		notify.MetaKeyIterationSessionID: fmt.Sprintf("%d", session.ID),
		notify.MetaKeyNotifyTime:         formatNotifyTime(),
	}
	if payload.PRTitle != "" {
		metadata[notify.MetaKeyPRTitle] = payload.PRTitle
	}
	msg := notify.Message{
		EventType: notify.EventIterationProgress,
		Severity:  notify.SeverityInfo,
		Target: notify.Target{
			Owner:  payload.RepoOwner,
			Repo:   payload.RepoName,
			Number: payload.PRNumber,
			IsPR:   true,
		},
		Title:    title,
		Body:     body,
		Metadata: metadata,
	}
	if err := h.iterateNotifier.Send(ctx, msg); err != nil {
		h.logger.WarnContext(ctx, "发送迭代进度通知失败",
			"round", roundNumber, "error", err)
	}
}

// filterIssuesBySeverity 直接过滤 []review.ReviewIssue。
func filterIssuesBySeverity(issues []review.ReviewIssue, threshold string) []review.ReviewIssue {
	minRank := iterate.SeverityRank(strings.ToUpper(threshold))
	var result []review.ReviewIssue
	for _, issue := range issues {
		if iterate.SeverityRank(strings.ToUpper(issue.Severity)) >= minRank {
			result = append(result, issue)
		}
	}
	return result
}
