package queue

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/test"
	"otws19.zicp.vip/kelin/dtworkflow/internal/webhook"
)

// 编译时检查 *EnqueueHandler 实现 webhook.Handler 接口
var _ webhook.Handler = (*EnqueueHandler)(nil)

// EnqueueHandler 实现 webhook.Handler 接口,将 webhook 事件转换为任务并入队
type EnqueueHandler struct {
	client        Enqueuer
	canceller     TaskCanceller    // M2.4: 任务取消能力
	store         store.Store
	logger        *slog.Logger
	branchCleaner BranchCleaner    // M4.2: 可选,Cancel-and-Replace 时清理旧 auto-test 分支
	prClient      genTestsPRClient // M4.2.1: cleanupAllAutoTestBranches
	moduleScanner test.RepoFileChecker // M4.2.1: ScanRepoModules
}

// EnqueueOption EnqueueHandler 可选配置。
type EnqueueOption func(*EnqueueHandler)

// WithBranchCleaner 注入 BranchCleaner 用于 M4.2 gen_tests Cancel-and-Replace 阶段
// 清理旧 auto-test/{module} 远程分支。未注入时 EnqueueManualGenTests 会跳过清理。
func WithBranchCleaner(c BranchCleaner) EnqueueOption {
	return func(h *EnqueueHandler) {
		h.branchCleaner = c
	}
}

// EnqueuedTask 多模块入队结果条目。
type EnqueuedTask struct {
	TaskID    string
	Module    string
	Framework string
}

// genTestsPRClient 全量清理所需的 PR 操作窄接口。
type genTestsPRClient interface {
	ListRepoPullRequests(ctx context.Context, owner, repo string,
		opts gitea.ListPullRequestsOptions) ([]*gitea.PullRequest, *gitea.Response, error)
	ClosePullRequest(ctx context.Context, owner, repo string, index int64) error
}

// WithPRClient 注入 genTestsPRClient 用于 M4.2.1 全量清理 auto-test 分支。
func WithPRClient(c genTestsPRClient) EnqueueOption {
	return func(h *EnqueueHandler) { h.prClient = c }
}

// WithModuleScanner 注入 RepoFileChecker 用于 M4.2.1 ScanRepoModules。
func WithModuleScanner(c test.RepoFileChecker) EnqueueOption {
	return func(h *EnqueueHandler) { h.moduleScanner = c }
}

// NewEnqueueHandler 创建 EnqueueHandler 实例。
// 参数 client 和 store 为必要依赖,传入 nil 属于编程错误（programming error）,
// 因此使用 panic 而非返回 error,与 Go 标准库（如 http.NewServeMux）的惯例一致。
// canceller 为可选依赖,nil 时跳过取消逻辑。
func NewEnqueueHandler(client Enqueuer, canceller TaskCanceller, store store.Store, logger *slog.Logger, opts ...EnqueueOption) *EnqueueHandler {
	if client == nil {
		panic("NewEnqueueHandler: client 不能为 nil")
	}
	if store == nil {
		panic("NewEnqueueHandler: store 不能为 nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	h := &EnqueueHandler{
		client:    client,
		canceller: canceller,
		store:     store,
		logger:    logger,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// HandlePullRequest 处理 PR 事件,执行幂等检查后创建任务并入队。
// 注意：HandlePullRequest 与 HandleIssueLabel 有相似的流程结构（幂等检查 -> 构建 payload ->
// 构建 record -> enqueueTask -> 日志）,但因各事件的 payload 字段差异较大且日志消息不同,
// 提取通用模板方法反而会引入复杂的泛型或 interface 抽象,得不偿失。
// 核心逻辑已下沉到 enqueueTask,当前的重复仅在 payload/record 构建和日志层面。
func (h *EnqueueHandler) HandlePullRequest(ctx context.Context, event webhook.PullRequestEvent) error {
	// M4.2: 拦截来自 auto-test/{moduleKey} 分支的 PR webhook——这些 PR 由 gen_tests
	// 自己产出,上游已发过 gen_tests 完成通知,不应再为其触发自动评审。
	// 仅在命中活跃 gen_tests 任务的 module 时拦截；查询失败 fail-open,继续原流程
	// 保证评审能力不被 gen_tests 拖垮。
	if h.shouldSkipAutoTestReview(ctx, event) {
		return nil
	}

	// 幂等检查：相同 delivery_id + task_type 不重复创建
	existing, err := h.store.FindByDeliveryID(ctx, event.DeliveryID, model.TaskTypeReviewPR)
	if err != nil {
		return fmt.Errorf("幂等检查失败: %w", err)
	}
	if existing != nil {
		h.logger.InfoContext(ctx, "PR 评审任务已存在,跳过",
			"delivery_id", event.DeliveryID,
			"task_id", existing.ID,
			"status", existing.Status,
		)
		return nil
	}

	// M2.4: 先识别被替代的旧任务,等新任务持久化成功后再取消旧任务,
	// 避免 replacement 创建失败时把当前唯一可运行任务也一起取消掉。
	activeTasks, superseded := h.listActivePRTasks(ctx, event.Repository.FullName, event.PullRequest.Number)

	payload := model.TaskPayload{
		TaskType:        model.TaskTypeReviewPR,
		DeliveryID:      event.DeliveryID,
		RepoOwner:       event.Repository.Owner,
		RepoName:        event.Repository.Name,
		RepoFullName:    event.Repository.FullName,
		CloneURL:        event.Repository.CloneURL,
		PRNumber:        event.PullRequest.Number,
		PRTitle:         event.PullRequest.Title,
		BaseRef:         event.PullRequest.BaseRef,
		HeadRef:         event.PullRequest.HeadRef,
		HeadSHA:         event.PullRequest.HeadSHA,
		SupersededCount: superseded.Count,
		PreviousHeadSHA: superseded.LastHeadSHA,
	}

	if payload.RepoFullName == "" || payload.CloneURL == "" {
		return fmt.Errorf("webhook 数据不完整: RepoFullName 或 CloneURL 为空")
	}

	record := &model.TaskRecord{
		TaskType:     model.TaskTypeReviewPR,
		Priority:     model.PriorityHigh,
		RepoFullName: event.Repository.FullName,
		PRNumber:     event.PullRequest.Number,
		DeliveryID:   event.DeliveryID,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return err
	}

	h.cancelTasks(ctx, activeTasks)

	if record.Status == model.TaskStatusQueued {
		h.logger.InfoContext(ctx, "PR 评审任务已入队",
			"task_id", record.ID,
			"asynq_id", record.AsynqID,
			"repo", event.Repository.FullName,
			"pr", event.PullRequest.Number,
			"superseded", superseded.Count,
		)
	} else {
		h.logger.InfoContext(ctx, "PR 评审任务已创建（pending）,等待 RecoveryLoop 入队",
			"task_id", record.ID,
			"repo", event.Repository.FullName,
			"pr", event.PullRequest.Number,
		)
	}
	return nil
}

// HandleIssueLabel 处理 Issue 标签事件,按标签类型路由到不同任务。
// M3.4: fix-to-pr 标签 → TaskTypeFixIssue；auto-fix 标签 → TaskTypeAnalyzeIssue。
// fix-to-pr 优先级高于 auto-fix（同时存在时仅入队 fix_issue）。
// 流程结构与 HandlePullRequest 类似,参见其注释了解未提取模板方法的原因。
func (h *EnqueueHandler) HandleIssueLabel(ctx context.Context, event webhook.IssueLabelEvent) error {
	// M3.4: 双标签分流,fix-to-pr 优先于 auto-fix
	var taskType model.TaskType
	switch {
	case event.FixToPRAdded:
		taskType = model.TaskTypeFixIssue
	case event.AutoFixAdded:
		taskType = model.TaskTypeAnalyzeIssue
	default:
		return nil
	}

	// 幂等检查（按各自 TaskType 独立查询）
	existing, err := h.store.FindByDeliveryID(ctx, event.DeliveryID, taskType)
	if err != nil {
		return fmt.Errorf("幂等检查失败: %w", err)
	}
	if existing != nil {
		h.logger.InfoContext(ctx, "Issue 任务已存在,跳过",
			"delivery_id", event.DeliveryID,
			"task_id", existing.ID,
			"task_type", taskType,
			"status", existing.Status,
		)
		return nil
	}

	payload := model.TaskPayload{
		TaskType:     taskType,
		DeliveryID:   event.DeliveryID,
		RepoOwner:    event.Repository.Owner,
		RepoName:     event.Repository.Name,
		RepoFullName: event.Repository.FullName,
		CloneURL:     event.Repository.CloneURL,
		IssueNumber:  event.Issue.Number,
		IssueTitle:   event.Issue.Title,
		IssueRef:     event.Issue.Ref,
	}

	if payload.RepoFullName == "" || payload.CloneURL == "" {
		return fmt.Errorf("webhook 数据不完整: RepoFullName 或 CloneURL 为空")
	}

	record := &model.TaskRecord{
		TaskType:     taskType,
		Priority:     model.PriorityNormal,
		RepoFullName: event.Repository.FullName,
		DeliveryID:   event.DeliveryID,
	}

	activeTasks := h.listReplacementIssueTasks(ctx, event.Repository.FullName, event.Issue.Number, taskType)

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return err
	}
	h.cancelTasks(ctx, activeTasks)

	taskTypeName := "Issue 分析"
	if taskType == model.TaskTypeFixIssue {
		taskTypeName = "Issue 修复"
	}
	if record.Status == model.TaskStatusQueued {
		h.logger.InfoContext(ctx, taskTypeName+"任务已入队",
			"task_id", record.ID,
			"asynq_id", record.AsynqID,
			"task_type", taskType,
			"repo", event.Repository.FullName,
			"issue", event.Issue.Number,
		)
	} else {
		h.logger.InfoContext(ctx, taskTypeName+"任务已创建（pending）,等待 RecoveryLoop 入队",
			"task_id", record.ID,
			"task_type", taskType,
			"repo", event.Repository.FullName,
			"issue", event.Issue.Number,
		)
	}
	return nil
}

// buildAsynqTaskID 根据 deliveryID 和 taskType 构建确定性的 asynq TaskID。
// 当 deliveryID 非空时,使用 "deliveryID:taskType" 格式,保证 asynq 层面的幂等去重；
// 当 deliveryID 为空时返回空字符串,让 asynq 自动生成 TaskID。
//
// 此函数被 enqueueTask 和 RecoveryLoop.requeue 共享,确保同一任务无论首次入队
// 还是恢复重入队都使用相同的 TaskID,避免因 TaskID 不一致导致任务重复执行。
func buildAsynqTaskID(deliveryID string, taskType model.TaskType) string {
	if deliveryID != "" {
		return fmt.Sprintf("%s:%s", deliveryID, taskType)
	}
	return "" // 让 asynq 自动生成
}

// autoTestBranchPrefix 标识由 gen_tests 产出的稳定工作分支前缀；
// 与 test.BuildAutoTestBranchName 保持一致（auto-test/{moduleKey}）。
const autoTestBranchPrefix = "auto-test/"

// shouldSkipAutoTestReview 判断 PR webhook 是否来自 gen_tests 自己产出的
// auto-test/{moduleKey} 分支；若同仓库当前仍有该 module 的活跃 gen_tests 任务,
// 则拦截评审入队。其它情况（非 auto-test/* 前缀、查询失败等）均 fail-open。
func (h *EnqueueHandler) shouldSkipAutoTestReview(ctx context.Context, event webhook.PullRequestEvent) bool {
	head := event.PullRequest.HeadRef
	if !strings.HasPrefix(head, autoTestBranchPrefix) {
		return false
	}
	moduleKey := strings.TrimPrefix(head, autoTestBranchPrefix)
	if moduleKey == "" {
		return false
	}

	candidates, err := h.store.ListActiveGenTestsModules(ctx, event.Repository.FullName)
	if err != nil {
		// fail-open：查询失败时不阻断评审,只记日志。按 §4.5 要求,字段覆盖
		// repo / module_key / pr / head / delivery_id / error 全量,便于排障。
		h.logger.WarnContext(ctx, "查询活跃 gen_tests module 失败,auto-test PR 仍按评审流程放行",
			"repo", event.Repository.FullName,
			"module_key", moduleKey,
			"pr", event.PullRequest.Number,
			"head", head,
			"delivery_id", event.DeliveryID,
			"error", err,
		)
		return false
	}
	for _, candidate := range candidates {
		if test.ModuleKey(candidate) == moduleKey {
			h.logger.InfoContext(ctx, "拦截 auto-test PR 评审入队：存在活跃 gen_tests 任务",
				"repo", event.Repository.FullName,
				"module_key", moduleKey,
				"pr", event.PullRequest.Number,
				"head", head,
				"delivery_id", event.DeliveryID,
			)
			return true
		}
	}
	return false
}

// SupersededInfo 取消旧任务后的汇总信息
type SupersededInfo struct {
	Count       int    // 取消的旧任务数量
	LastHeadSHA string // 最近一个旧任务的 HeadSHA
}

// listActivePRTasks 查找同一 PR 的活跃旧评审任务,并汇总 superseded 信息。
func (h *EnqueueHandler) listActivePRTasks(ctx context.Context, repoFullName string, prNumber int64) ([]*model.TaskRecord, SupersededInfo) {
	tasks, err := h.store.FindActivePRTasks(ctx, repoFullName, prNumber, model.TaskTypeReviewPR)
	if err != nil {
		h.logger.WarnContext(ctx, "查找活跃旧任务失败,跳过取消",
			"repo", repoFullName, "pr", prNumber, "error", err)
		return nil, SupersededInfo{}
	}
	if len(tasks) == 0 {
		return nil, SupersededInfo{}
	}

	info := SupersededInfo{Count: len(tasks)}
	for _, task := range tasks {
		if task.Payload.HeadSHA != "" {
			info.LastHeadSHA = task.Payload.HeadSHA
		}
	}
	return tasks, info
}

// listActiveIssueTasks 查找同一 Issue 的活跃任务。
func (h *EnqueueHandler) listActiveIssueTasks(ctx context.Context, repoFullName string, issueNumber int64, taskType model.TaskType) []*model.TaskRecord {
	tasks, err := h.store.FindActiveIssueTasks(ctx, repoFullName, issueNumber, taskType)
	if err != nil {
		h.logger.WarnContext(ctx, "查找活跃 Issue 任务失败,跳过取消",
			"repo", repoFullName, "issue", issueNumber, "error", err)
		return nil
	}
	return tasks
}

// listReplacementIssueTasks 查找新任务创建后需要取消的旧任务。
// analyze_issue 仅替换旧 analyze_issue；fix_issue 会同时替换旧 fix_issue 和 analyze_issue,
// 确保 fix-to-pr 升级为修复时不会与前序分析任务并发运行。
func (h *EnqueueHandler) listReplacementIssueTasks(ctx context.Context, repoFullName string, issueNumber int64, taskType model.TaskType) []*model.TaskRecord {
	tasks := h.listActiveIssueTasks(ctx, repoFullName, issueNumber, taskType)
	if taskType != model.TaskTypeFixIssue {
		return tasks
	}

	analyzeTasks := h.listActiveIssueTasks(ctx, repoFullName, issueNumber, model.TaskTypeAnalyzeIssue)
	if len(analyzeTasks) == 0 {
		return tasks
	}

	merged := make([]*model.TaskRecord, 0, len(tasks)+len(analyzeTasks))
	seen := make(map[string]struct{}, len(tasks)+len(analyzeTasks))
	for _, task := range append(tasks, analyzeTasks...) {
		if task == nil {
			continue
		}
		if _, ok := seen[task.ID]; ok {
			continue
		}
		seen[task.ID] = struct{}{}
		merged = append(merged, task)
	}
	return merged
}

// cancelTasks 在 replacement 已持久化后,逐个取消旧任务（best-effort）。
func (h *EnqueueHandler) cancelTasks(ctx context.Context, tasks []*model.TaskRecord) {
	var failCount int
	for _, task := range tasks {
		if !h.cancelTask(ctx, task) {
			failCount++
		}
	}
	if failCount > 0 {
		h.logger.WarnContext(ctx, "部分旧任务取消失败,可能存在并行评审",
			"total", len(tasks), "failed", failCount)
	}
}

// cancelTask 取消单个旧评审任务（best-effort）
func (h *EnqueueHandler) cancelTask(ctx context.Context, task *model.TaskRecord) bool {
	prevStatus := task.Status

	if task.AsynqID != "" {
		if h.canceller == nil {
			h.logger.WarnContext(ctx, "缺少任务取消器,保留旧任务为可运行状态",
				"task_id", task.ID, "asynq_id", task.AsynqID, "status", task.Status)
			return false
		}
		switch task.Status {
		case model.TaskStatusPending, model.TaskStatusQueued:
			queueName := PriorityToQueue(task.Priority)
			if err := h.canceller.Delete(ctx, queueName, task.AsynqID); err != nil {
				h.logger.WarnContext(ctx, "从 asynq 删除任务失败",
					"task_id", task.ID, "asynq_id", task.AsynqID, "error", err)
				return false
			}
		case model.TaskStatusRunning:
			if err := h.canceller.CancelProcessing(ctx, task.AsynqID); err != nil {
				h.logger.WarnContext(ctx, "取消运行中任务失败",
					"task_id", task.ID, "asynq_id", task.AsynqID, "error", err)
				return false
			}
		}
	}

	now := time.Now()
	task.Status = model.TaskStatusCancelled
	task.Error = "被同一 PR 的新评审任务取代"
	task.UpdatedAt = now
	task.CompletedAt = &now
	if err := h.store.UpdateTask(ctx, task); err != nil {
		h.logger.WarnContext(ctx, "更新旧任务状态为 cancelled 失败（asynq 已操作但 SQLite 更新失败）",
			"task_id", task.ID, "error", err)
		return false
	}
	h.logger.InfoContext(ctx, "已取消旧评审任务",
		"task_id", task.ID, "prev_status", prevStatus, "pr", task.PRNumber)
	return true
}

// enqueueTask 持久化任务记录并将其入队,record 字段 TaskType/Priority/RepoFullName/DeliveryID 需预先填充
func (h *EnqueueHandler) enqueueTask(ctx context.Context, payload model.TaskPayload, record *model.TaskRecord) error {
	now := time.Now()
	record.ID = uuid.New().String()
	record.Status = model.TaskStatusPending
	record.MaxRetry = TaskMaxRetry()
	record.CreatedAt = now
	record.UpdatedAt = now

	// M2.4: 设置 payload.CreatedAt 用于 staleness check
	payload.CreatedAt = record.CreatedAt
	record.Payload = payload

	// 1. 先持久化到 SQLite（status=pending）
	if err := h.store.CreateTask(ctx, record); err != nil {
		return fmt.Errorf("创建任务记录失败: %w", err)
	}

	// 2. 入队到 Redis
	// 设计决策："先持久化再入队" 的 eventually consistent 模式。
	// Step 1 成功但 Step 2 失败时,任务保持 pending 状态,不向调用方返回错误。
	// RecoveryLoop 会定期扫描长时间处于 pending 的孤儿任务并重新入队,
	// 从而保证最终一致性。这避免了分布式事务的复杂性,代价是入队可能有延迟。
	// 使用共享的 buildAsynqTaskID 生成确定性 TaskID,确保与 RecoveryLoop 一致
	taskID := buildAsynqTaskID(record.DeliveryID, record.TaskType)
	asynqID, err := h.client.Enqueue(ctx, payload, EnqueueOptions{
		Priority: record.Priority,
		TaskID:   taskID,
	})
	if err != nil {
		// 设计决策：入队失败不返回错误给调用方。
		// 设计文档（docs/M1.5-task-queue-design.md）中描述入队失败应返回 error,
		// 但实际实现采用了 "先持久化再入队" 的 eventually consistent 模式：
		// 任务已成功持久化到 SQLite（status=pending）,RecoveryLoop 会定期扫描
		// 长时间处于 pending 的孤儿任务并重新入队,最终保证一致性。
		// 若此处返回 error,webhook handler 会向 Gitea 返回 500,触发 Gitea 重发
		// 同一 webhook,而任务实际已被持久化,这会造成不必要的重试噪音。
		// 因此选择静默降级：记录警告日志,依赖 RecoveryLoop 补偿入队,
		// 代价是入队可能有最多 interval（默认 60s）的延迟。
		h.logger.WarnContext(ctx, "任务入队失败,将由 RecoveryLoop 重试",
			"task_id", record.ID,
			"task_type", record.TaskType,
			"error", err,
		)
		return nil
	}

	// 3. 更新状态为 queued
	record.AsynqID = asynqID
	record.Status = model.TaskStatusQueued
	record.UpdatedAt = time.Now()
	if err := h.store.UpdateTask(ctx, record); err != nil {
		h.logger.ErrorContext(ctx, "更新任务状态为 queued 失败",
			"task_id", record.ID,
			"error", err,
		)
		// 不返回错误,任务已成功入队
	}

	return nil
}

// EnqueueManualReview 手动触发 PR 评审入队。
// payload 由 API handler 组装（含完整 PR 信息）,triggeredBy 格式为 "manual:{identity}"。
func (h *EnqueueHandler) EnqueueManualReview(ctx context.Context, payload model.TaskPayload, triggeredBy string) (string, error) {
	payload.TaskType = model.TaskTypeReviewPR
	payload.DeliveryID = generateManualDeliveryID()

	// Cancel-and-Replace：查找同一 PR 的活跃旧任务
	activeTasks, superseded := h.listActivePRTasks(ctx, payload.RepoFullName, payload.PRNumber)
	payload.SupersededCount = superseded.Count
	payload.PreviousHeadSHA = superseded.LastHeadSHA

	record := &model.TaskRecord{
		TaskType:     model.TaskTypeReviewPR,
		Priority:     model.PriorityHigh,
		RepoFullName: payload.RepoFullName,
		PRNumber:     payload.PRNumber,
		DeliveryID:   payload.DeliveryID,
		TriggeredBy:  triggeredBy,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return "", err
	}
	h.cancelTasks(ctx, activeTasks)

	h.logger.InfoContext(ctx, "手动 PR 评审任务已入队",
		"task_id", record.ID, "repo", payload.RepoFullName,
		"pr", payload.PRNumber, "triggered_by", triggeredBy)
	return record.ID, nil
}

// EnqueueManualFix 手动触发 Issue 分析或修复入队。
// M3.4: 支持调用方通过 payload.TaskType 指定任务类型（analyze_issue 或 fix_issue）,
// 未指定时默认为 analyze_issue。
func (h *EnqueueHandler) EnqueueManualFix(ctx context.Context, payload model.TaskPayload, triggeredBy string) (string, error) {
	// M3.4: 支持调用方指定 TaskType（analyze_issue 或 fix_issue）
	if payload.TaskType == "" {
		payload.TaskType = model.TaskTypeAnalyzeIssue // 默认分析模式
	}
	// 校验仅允许 Issue 相关类型
	if payload.TaskType != model.TaskTypeAnalyzeIssue && payload.TaskType != model.TaskTypeFixIssue {
		return "", fmt.Errorf("EnqueueManualFix 不支持任务类型: %s", payload.TaskType)
	}
	payload.DeliveryID = generateManualDeliveryID()

	activeTasks := h.listReplacementIssueTasks(ctx, payload.RepoFullName, payload.IssueNumber, payload.TaskType)

	record := &model.TaskRecord{
		TaskType:     payload.TaskType,
		Priority:     model.PriorityNormal,
		RepoFullName: payload.RepoFullName,
		DeliveryID:   payload.DeliveryID,
		TriggeredBy:  triggeredBy,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return "", err
	}
	h.cancelTasks(ctx, activeTasks)

	taskTypeName := "Issue 分析"
	if payload.TaskType == model.TaskTypeFixIssue {
		taskTypeName = "Issue 修复"
	}
	h.logger.InfoContext(ctx, "手动"+taskTypeName+"任务已入队",
		"task_id", record.ID, "task_type", payload.TaskType,
		"repo", payload.RepoFullName,
		"issue", payload.IssueNumber, "triggered_by", triggeredBy)
	return record.ID, nil
}

// enqueueSingleGenTests 封装单个 gen_tests 任务的入队逻辑,供 EnqueueManualGenTests
// 及未来的多模块批量入队复用。调用方需确保 payload.RepoFullName / CloneURL 已填充。
func (h *EnqueueHandler) enqueueSingleGenTests(ctx context.Context, payload model.TaskPayload, triggeredBy string) (EnqueuedTask, error) {
	payload.TaskType = model.TaskTypeGenTests
	payload.DeliveryID = generateManualDeliveryID()

	activeTasks := h.listActiveGenTestsTasksByBranchKey(ctx, payload.RepoFullName, payload.Module, payload.Framework)

	if len(activeTasks) > 0 && h.branchCleaner != nil {
		branchName := test.BuildAutoTestBranchName(payload.Module, payload.Framework)
		if err := h.branchCleaner.CleanupAutoTestBranch(ctx, payload.RepoOwner, payload.RepoName, branchName); err != nil {
			h.logger.WarnContext(ctx, "清理旧 auto-test 分支失败,继续入队新任务",
				"repo", payload.RepoFullName, "module", payload.Module,
				"branch", branchName, "error", err)
		}
	}

	h.cancelTasks(ctx, activeTasks)

	record := &model.TaskRecord{
		TaskType:     model.TaskTypeGenTests,
		Priority:     model.PriorityNormal,
		RepoFullName: payload.RepoFullName,
		DeliveryID:   payload.DeliveryID,
		TriggeredBy:  triggeredBy,
	}

	if err := h.enqueueTask(ctx, payload, record); err != nil {
		return EnqueuedTask{}, err
	}

	h.logger.InfoContext(ctx, "gen_tests 任务已入队",
		"task_id", record.ID, "repo", payload.RepoFullName,
		"module", payload.Module, "framework", payload.Framework,
		"triggered_by", triggeredBy)
	return EnqueuedTask{TaskID: record.ID, Module: payload.Module, Framework: payload.Framework}, nil
}

// EnqueueManualGenTests 手动触发 gen_tests 任务入队。
// payload 由 API handler / CLI handler 组装；triggeredBy 格式为 "manual:{identity}"。
// Cancel-and-Replace 按 stable branch key 粒度：同仓库同 branch key 的活跃任务在本次入队后被取消。
func (h *EnqueueHandler) EnqueueManualGenTests(ctx context.Context, payload model.TaskPayload, triggeredBy string) (string, error) {
	if payload.RepoFullName == "" || payload.CloneURL == "" {
		return "", fmt.Errorf("payload 数据不完整: RepoFullName 或 CloneURL 为空")
	}
	result, err := h.enqueueSingleGenTests(ctx, payload, triggeredBy)
	if err != nil {
		return "", err
	}
	return result.TaskID, nil
}

// listActiveGenTestsTasks EnqueueHandler 私有 helper,封装错误日志,
// 与 listActivePRTasks / listActiveIssueTasks 风格一致。
func (h *EnqueueHandler) listActiveGenTestsTasks(ctx context.Context, repoFullName, module string) []*model.TaskRecord {
	tasks, err := h.store.FindActiveGenTestsTasks(ctx, repoFullName, module)
	if err != nil {
		h.logger.WarnContext(ctx, "查找活跃 gen_tests 任务失败,跳过取消",
			"repo", repoFullName, "module", module, "error", err)
		return nil
	}
	return tasks
}

// listActiveGenTestsTasksByBranchKey 查找会落到同一 stable branch 的活跃 gen_tests 任务。
//
// 设计背景：stable branch 名由 test.ModuleKey(module) 派生,原始 module 字符串不同但
// branch key 相同（例如 "svc/api" 与 "svc-api"）时,本质上会写同一 remote branch。
// 若仍按原始 module 精确匹配 Cancel-and-Replace,会放行两条任务并发写同一分支。
//
// M4.2.1：framework 非空时,targetKey 追加 "-{framework}" 后缀,使同 module 不同 framework
// 的任务（例如 junit5 与 vitest）不互相取消；同时在内层循环增加二次过滤,排除
// Payload.Framework 不匹配的历史任务。
//
// 实现上先取活跃 module 列表,再按 ModuleKey 过滤并回查具体任务。若列表查询失败则回退
// 到旧的"按原始 module 精确匹配"逻辑,保持 fail-open 行为与既有兼容性。
func (h *EnqueueHandler) listActiveGenTestsTasksByBranchKey(ctx context.Context,
	repoFullName, module, framework string) []*model.TaskRecord {
	targetKey := test.ModuleKey(module)
	if framework != "" {
		targetKey = targetKey + "-" + framework
	}

	modules, err := h.store.ListActiveGenTestsModules(ctx, repoFullName)
	if err != nil {
		h.logger.WarnContext(ctx, "查询活跃 gen_tests module 列表失败,回退到原始 module 精确匹配",
			"repo", repoFullName, "module", module, "module_key", targetKey, "error", err)
		return h.listActiveGenTestsTasks(ctx, repoFullName, module)
	}

	seenModule := make(map[string]struct{}, len(modules))
	seenTask := make(map[string]struct{})
	var merged []*model.TaskRecord
	for _, candidate := range modules {
		if test.ModuleKey(candidate) != test.ModuleKey(module) {
			continue
		}
		if _, ok := seenModule[candidate]; ok {
			continue
		}
		seenModule[candidate] = struct{}{}

		tasks, err := h.store.FindActiveGenTestsTasks(ctx, repoFullName, candidate)
		if err != nil {
			h.logger.WarnContext(ctx, "按 stable branch key 回查活跃 gen_tests 任务失败,跳过该 module",
				"repo", repoFullName,
				"module", module,
				"candidate_module", candidate,
				"module_key", targetKey,
				"error", err,
			)
			continue
		}
		for _, task := range tasks {
			if task == nil {
				continue
			}
			if _, ok := seenTask[task.ID]; ok {
				continue
			}
			if framework != "" && task.Payload.Framework != framework {
				continue
			}
			seenTask[task.ID] = struct{}{}
			merged = append(merged, task)
		}
	}
	return merged
}

// generateManualDeliveryID 生成手动触发的合成 delivery ID
func generateManualDeliveryID() string {
	return fmt.Sprintf("manual-%d-%s", time.Now().UnixMilli(), uuid.New().String()[:8])
}
