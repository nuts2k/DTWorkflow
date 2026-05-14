package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/e2e"
	"otws19.zicp.vip/kelin/dtworkflow/internal/fix"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/test"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// adaptFixResult 将 fix.FixResult 适配为 worker.ExecutionResult。
// M3.3: ParseError 仅保留信息到 Error 字段供调试，不再导致 ExitCode=1。
// fix 解析失败的降级评论由 processor 在重试耗尽后统一触发。
// CLIMeta.IsError 仍保留 ExitCode=1（与 review 包对齐）。
func adaptFixResult(r *fix.FixResult) *worker.ExecutionResult {
	if r == nil {
		return &worker.ExecutionResult{ExitCode: 0}
	}
	res := &worker.ExecutionResult{
		Output:   r.RawOutput,
		ExitCode: 0,
	}
	if r.ExitCode != 0 {
		res.ExitCode = r.ExitCode
		res.Error = fmt.Sprintf("fix worker 退出码非零: %d", r.ExitCode)
	}
	if r.CLIMeta != nil {
		res.Duration = r.CLIMeta.DurationMs
		if r.CLIMeta.IsError {
			res.ExitCode = 1
			res.Error = "Claude CLI 报告错误"
		}
	}
	// ParseError 信息保留到 Error 字段供调试，但不影响退出码
	if r.ParseError != nil {
		if res.Error == "" {
			res.Error = r.ParseError.Error()
		} else if !strings.Contains(res.Error, r.ParseError.Error()) {
			res.Error = res.Error + "; " + r.ParseError.Error()
		}
	}
	// fix 流程的用户可见结果只有 Issue 评论；回写失败必须失败并允许重试。
	if r.WritebackError != nil {
		msg := fmt.Sprintf("回写失败: %v", r.WritebackError)
		if res.ExitCode == 0 {
			res.ExitCode = 1
		}
		if res.Error == "" {
			res.Error = msg
		} else if !strings.Contains(res.Error, msg) {
			res.Error = res.Error + "; " + msg
		}
	}
	return res
}

// adaptReviewResult 将 review.ReviewResult 适配为 worker.ExecutionResult
func adaptReviewResult(r *review.ReviewResult) *worker.ExecutionResult {
	if r == nil {
		return nil
	}
	result := &worker.ExecutionResult{
		ExitCode: 0,
		Output:   r.RawOutput,
	}
	if r.CLIMeta != nil {
		result.Duration = r.CLIMeta.DurationMs
		if r.CLIMeta.IsError {
			result.ExitCode = 1
			result.Error = "Claude CLI 报告错误"
		}
	}
	// 保留 ParseError 信息到任务记录，便于调试（优雅降级场景）
	if r.ParseError != nil && result.Error == "" {
		result.Error = r.ParseError.Error()
	}
	// WritebackError 不影响任务退出码，但需要保留到任务记录供调试。
	if r.WritebackError != nil {
		msg := fmt.Sprintf("回写失败: %v", r.WritebackError)
		if result.Error == "" {
			result.Error = msg
		} else if !strings.Contains(result.Error, msg) {
			result.Error = result.Error + "; " + msg
		}
	}
	return result
}

func adaptE2EResult(r *e2e.E2EResult) *worker.ExecutionResult {
	if r == nil {
		return nil
	}
	exitCode := 0
	errMsg := ""
	if r.Output != nil && !r.Output.Success {
		exitCode = 1
		if isDeterministicE2EFailure(r.Output) {
			exitCode = 2
		}
		errMsg = fmt.Sprintf("E2E 测试未通过: total=%d passed=%d failed=%d error=%d skipped=%d",
			r.Output.TotalCases,
			r.Output.PassedCases,
			r.Output.FailedCases,
			r.Output.ErrorCases,
			r.Output.SkippedCases,
		)
	}
	return &worker.ExecutionResult{
		ExitCode: exitCode,
		Output:   r.RawOutput,
		Duration: r.DurationMs,
		Error:    errMsg,
	}
}

func isDeterministicE2EFailure(o *e2e.E2EOutput) bool {
	if o == nil {
		return false
	}
	hasFailure := false
	for _, c := range o.Cases {
		switch c.FailureCategory {
		case "environment":
			return false
		case "bug", "script_outdated":
			hasFailure = true
		}
	}
	return hasFailure && o.ErrorCases == 0
}

// adaptTestResult 将 test.TestGenResult 适配为 worker.ExecutionResult。
// ParseError 仅保留到 Error 字段，不直接改写退出码；真正的重试策略由 Processor
// 依据 ErrTestGenParseFailure 判断。
func adaptTestResult(r *test.TestGenResult) *worker.ExecutionResult {
	if r == nil {
		return &worker.ExecutionResult{ExitCode: 0}
	}
	res := &worker.ExecutionResult{
		Output:   r.RawOutput,
		ExitCode: 0,
	}
	if r.ExitCode != 0 {
		res.ExitCode = r.ExitCode
		res.Error = fmt.Sprintf("gen_tests worker 退出码非零: %d", r.ExitCode)
	}
	if r.CLIMeta != nil {
		res.Duration = r.CLIMeta.DurationMs
		if r.CLIMeta.IsError {
			res.ExitCode = 1
			res.Error = "Claude CLI 报告错误"
		}
	}
	if r.ParseError != nil {
		if res.Error == "" {
			res.Error = r.ParseError.Error()
		} else if !strings.Contains(res.Error, r.ParseError.Error()) {
			res.Error = res.Error + "; " + r.ParseError.Error()
		}
	}
	if res.Output == "" && r.Output != nil {
		if data, err := json.Marshal(r.Output); err == nil {
			res.Output = string(data)
		}
	}
	return res
}

// handleSkipRetryFailure 处理确定性失败（如 PR/Issue 不处于 open 状态），
// 标记任务 failed、尽可能保留结构化结果、持久化、发送通知，并返回 SkipRetry 错误。
func (p *Processor) handleSkipRetryFailure(ctx context.Context, record *model.TaskRecord, runErr error, reviewResult *review.ReviewResult, fixResult *fix.FixResult, testResult *test.TestGenResult, logMsg string) error {
	record.Status = model.TaskStatusFailed
	if record.Payload.TaskType == model.TaskTypeGenTests || record.Payload.TaskType == model.TaskTypeRunE2E || record.Payload.TaskType == model.TaskTypeTriageE2E || record.Payload.TaskType == model.TaskTypeFixReview {
		record.Error = test.SanitizeErrorMessage(runErr.Error())
	} else {
		record.Error = runErr.Error()
	}
	switch {
	case reviewResult != nil:
		if result := adaptReviewResult(reviewResult); result != nil {
			record.Result = result.Output
		}
	case fixResult != nil:
		if result := adaptFixResult(fixResult); result != nil {
			record.Result = result.Output
		}
	case testResult != nil:
		if result := adaptTestResult(testResult); result != nil {
			record.Result = result.Output
		}
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
	} else if record.Payload.TaskType == model.TaskTypeFixReview {
		p.persistIterationFixFailure(ctx, record.Payload, record.Error)
		p.sendIterationErrorNotification(ctx, record)
	} else {
		p.sendCompletionNotification(ctx, record, reviewResult, fixResult, testResult, nil, nil)
	}
	return fmt.Errorf("%s: %w", logMsg, asynq.SkipRetry)
}

func (p *Processor) markTaskCancelled(ctx context.Context, record *model.TaskRecord, reason string) error {
	record.Status = model.TaskStatusCancelled
	record.Error = reason
	completedAt := time.Now()
	record.CompletedAt = &completedAt
	record.UpdatedAt = completedAt

	p.logger.InfoContext(ctx, reason,
		"task_id", record.ID,
	)

	// 原始 ctx 可能已取消；使用后台 context 落库，确保最终状态尽量持久化。
	bgCtx, bgCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer bgCancel()
	if err := p.store.UpdateTask(bgCtx, record); err != nil {
		p.logger.ErrorContext(ctx, "更新取消任务状态失败",
			"task_id", record.ID, "error", err,
		)
	} else if record.Payload.TaskType == model.TaskTypeFixReview {
		p.persistIterationFixFailure(ctx, record.Payload, reason)
		p.sendIterationErrorNotification(ctx, record)
	}

	return fmt.Errorf("%s: %w", reason, asynq.SkipRetry)
}

type triageDispatchResult struct {
	Requested int
	Enqueued  int
	Failed    int
}

var errTriageDispatchPartialFailure = errors.New("triage_e2e 部分 run_e2e 子任务入队失败")

// handleTriageE2EResult 处理 triage_e2e 成功后的链式入队。
// 遍历 triageResult.Modules，逐模块调用 EnqueueManualE2E 入队 run_e2e。
// 入队前基于仓库实际 e2e/ 模块做白名单校验，避免 LLM 输出直接驱动任意任务。
func (p *Processor) handleTriageE2EResult(ctx context.Context, record *model.TaskRecord, output *e2e.TriageE2EOutput) (triageDispatchResult, error) {
	var dispatch triageDispatchResult
	if len(output.Modules) == 0 {
		p.logger.InfoContext(ctx, "triage_e2e: 无需回归，modules 为空",
			"task_id", record.ID)
		return dispatch, nil
	}
	dispatch.Requested = len(output.Modules)
	if p.enqueueHandler == nil {
		p.logger.ErrorContext(ctx, "triage_e2e: enqueueHandler 未注入，无法链式入队",
			"task_id", record.ID)
		return dispatch, fmt.Errorf("enqueueHandler 未注入，无法链式入队 run_e2e")
	}

	available, err := p.loadAvailableE2EModules(ctx, record)
	if err != nil {
		return dispatch, err
	}
	for _, mod := range output.Modules {
		if _, ok := available[mod.Name]; !ok {
			return dispatch, fmt.Errorf("triage 输出模块 %q 不存在于仓库 e2e/ 可用模块", mod.Name)
		}
	}

	triggeredBy := fmt.Sprintf("triage_e2e:%s", record.ID)
	for _, mod := range output.Modules {
		payload := model.TaskPayload{
			TaskType:       model.TaskTypeRunE2E,
			RepoOwner:      record.Payload.RepoOwner,
			RepoName:       record.Payload.RepoName,
			RepoFullName:   record.Payload.RepoFullName,
			CloneURL:       record.Payload.CloneURL,
			Module:         mod.Name,
			BaseRef:        record.Payload.BaseRef,
			HeadSHA:        record.Payload.HeadSHA,
			MergeCommitSHA: record.Payload.MergeCommitSHA,
			Environment:    record.Payload.Environment,
		}
		tasks, err := p.enqueueHandler.EnqueueManualE2E(ctx, payload, triggeredBy)
		if err != nil {
			dispatch.Failed++
			p.logger.WarnContext(ctx, "triage_e2e: 链式入队失败",
				"task_id", record.ID,
				"module", mod.Name, "error", err)
			continue
		}
		if len(tasks) == 0 {
			dispatch.Failed++
			p.logger.WarnContext(ctx, "triage_e2e: 链式入队未返回任务",
				"task_id", record.ID,
				"module", mod.Name)
			continue
		}
		dispatch.Enqueued += len(tasks)
	}
	if dispatch.Enqueued == 0 {
		return dispatch, fmt.Errorf("所有 run_e2e 子任务入队均失败，modules=%d", dispatch.Requested)
	}
	if dispatch.Failed > 0 {
		p.logger.WarnContext(ctx, "triage_e2e: 部分 run_e2e 子任务入队失败",
			"task_id", record.ID,
			"requested", dispatch.Requested,
			"enqueued", dispatch.Enqueued,
			"failed", dispatch.Failed)
		return dispatch, fmt.Errorf("%w: requested=%d enqueued=%d failed=%d",
			errTriageDispatchPartialFailure, dispatch.Requested, dispatch.Enqueued, dispatch.Failed)
	}
	return dispatch, nil
}

func (p *Processor) loadAvailableE2EModules(ctx context.Context, record *model.TaskRecord) (map[string]struct{}, error) {
	if p.enqueueHandler.e2eModuleScanner == nil {
		return nil, fmt.Errorf("E2E 模块扫描器未注入，无法校验 triage 输出模块")
	}
	payload := record.Payload
	ref := triageModuleScanRef(payload)
	if ref == "" {
		return nil, fmt.Errorf("缺少可用于扫描 E2E 模块的 ref")
	}
	modules, err := e2e.ScanE2EModules(ctx, p.enqueueHandler.e2eModuleScanner,
		payload.RepoOwner, payload.RepoName, ref)
	if err != nil {
		return nil, fmt.Errorf("扫描 E2E 模块失败: %w", err)
	}
	available := make(map[string]struct{}, len(modules))
	for _, mod := range modules {
		available[mod] = struct{}{}
	}
	return available, nil
}

func triageModuleScanRef(payload model.TaskPayload) string {
	switch {
	case payload.MergeCommitSHA != "":
		return payload.MergeCommitSHA
	case payload.HeadSHA != "":
		return payload.HeadSHA
	default:
		return payload.BaseRef
	}
}
