package test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// ============================================================================
// 窄接口（仅暴露 Service 所需能力，便于单测 mock）
// ============================================================================

// RepoClient 仓库 / ref 信息查询接口。
type RepoClient interface {
	GetRepo(ctx context.Context, owner, repo string) (*gitea.Repository, *gitea.Response, error)
	GetBranch(ctx context.Context, owner, repo, branch string) (*gitea.Branch, *gitea.Response, error)
}

// PRClient M3.5 复用的 PR 创建与幂等查询能力。*gitea.Client 已满足此接口。
type PRClient interface {
	CreatePullRequest(ctx context.Context, owner, repo string,
		opts gitea.CreatePullRequestOption) (*gitea.PullRequest, *gitea.Response, error)
	ListRepoPullRequests(ctx context.Context, owner, repo string,
		opts gitea.ListPullRequestsOptions) ([]*gitea.PullRequest, *gitea.Response, error)
}

// GenTestsPoolRunner 容器执行接口（与 review / fix 同类接口独立）。
// 命名对应 gen_tests 领域，区别于 review / fix 的 pool 接口。
type GenTestsPoolRunner interface {
	RunWithCommandAndStdin(ctx context.Context, payload model.TaskPayload,
		cmd []string, stdinData []byte) (*worker.ExecutionResult, error)
}

// TestConfigProvider 配置访问接口。
//
// ResolveTestGenConfig 返回合并后的配置；其中 Enabled 的 *bool 语义：
//   - nil = 未覆盖（调用方按默认启用处理）
//   - *false = 显式禁用
//   - *true = 显式启用
type TestConfigProvider interface {
	ResolveTestGenConfig(repoFullName string) config.TestGenOverride
	GetClaudeModel() string
	GetClaudeEffort() string
}

// RepoFileChecker 检查仓库中指定路径是否存在。
// relPath 为空时表示检查 module 本身；非空时表示检查 module/relPath。
// M4.1 单测里用内存桩；M4.2 由上层适配（Gitea API 或容器内 fs）。
type RepoFileChecker interface {
	HasFile(ctx context.Context, owner, repo, ref, module, relPath string) (bool, error)
}

// ReviewEnqueuer gen_tests 任务完成后触发 review 入队的窄接口。
//
// 由 queue.EnqueueHandler.EnqueueManualReview 天然满足；接口定义在 internal/test
// 只是为了维持 queue → test 的既有依赖方向（禁止 internal/test 反向 import
// internal/queue），wiring 在 cmd/dtworkflow 主程序完成。
type ReviewEnqueuer interface {
	EnqueueManualReview(ctx context.Context, payload model.TaskPayload, triggeredBy string) (string, error)
}

// TestGenResultStore Service 持久化测试生成结果所需的窄接口。
//
// *store.SQLiteStore 通过 SaveTestGenResult / UpdateTestGenResultReviewEnqueued /
// GetTestGenResultByTaskID 天然满足此接口；保持窄接口便于单测 mock，避免在测试中
// 实现完整 store.Store 的 20+ 方法。两阶段 UPSERT 的阶段 2 走
// UpdateTestGenResultReviewEnqueued 的 partial UPDATE 语义，避免覆盖阶段 1
// 之后可能由其它异步组件写入的字段；GetTestGenResultByTaskID 用于 Execute 重试
// 时检查 review 入队幂等（I10）。
type TestGenResultStore interface {
	SaveTestGenResult(ctx context.Context, record *store.TestGenResultRecord) error
	UpdateTestGenResultReviewEnqueued(ctx context.Context, taskID string) error
	GetTestGenResultByTaskID(ctx context.Context, taskID string) (*store.TestGenResultRecord, error)
}

// ============================================================================
// Service
// ============================================================================

// ServiceOption Service 可选配置。
type ServiceOption func(*Service)

// WithServiceLogger 注入自定义日志记录器。
func WithServiceLogger(logger *slog.Logger) ServiceOption {
	return func(s *Service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// WithPRClient 注入 PR 创建客户端。
// M4.2：createTestPR 若 prClient 未注入将返回明确错误。
func WithPRClient(c PRClient) ServiceOption {
	return func(s *Service) { s.prClient = c }
}

// WithFileChecker 注入仓库文件探测器。
// M4.1 单测必选（用于 resolveFramework）；M4.2 由生产实现注入。
func WithFileChecker(c RepoFileChecker) ServiceOption {
	return func(s *Service) { s.fileChecker = c }
}

// WithReviewEnqueuer 注入 review 入队器（M4.2 新增）。
// 未注入时 Execute 跳过"完成后自动 enqueue review"步骤（单测兼容）。
func WithReviewEnqueuer(e ReviewEnqueuer) ServiceOption {
	return func(s *Service) { s.reviewEnqueuer = e }
}

// WithStore 注入测试生成结果持久化 store（M4.2 新增）。
// 未注入时 persistTestGenResult 静默跳过；适合单测关注主流程而无需 SQLite。
func WithStore(st TestGenResultStore) ServiceOption {
	return func(s *Service) { s.store = st }
}

// Service 测试生成编排服务，负责 gen_tests 任务的完整流程。
type Service struct {
	gitea          RepoClient
	prClient       PRClient
	pool           GenTestsPoolRunner
	cfgProv        TestConfigProvider
	fileChecker    RepoFileChecker
	reviewEnqueuer ReviewEnqueuer     // M4.2：Execute 完成后触发 review 入队
	store          TestGenResultStore // M4.2：test_gen_results 表 UPSERT
	logger         *slog.Logger
}

// NewService 创建 Service 实例。
// gitea / pool / cfgProv 为必要依赖，传入 nil 属于编程错误（panic）。
func NewService(gitea RepoClient, pool GenTestsPoolRunner, cfgProv TestConfigProvider,
	opts ...ServiceOption) *Service {
	if gitea == nil {
		panic("NewService: gitea 不能为 nil")
	}
	if pool == nil {
		panic("NewService: pool 不能为 nil")
	}
	if cfgProv == nil {
		panic("NewService: cfgProv 不能为 nil")
	}
	s := &Service{
		gitea:   gitea,
		pool:    pool,
		cfgProv: cfgProv,
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Execute 执行 gen_tests 任务的完整流程（设计文档 §4.2 八步流程）。
//
// 与 M4.1 相比的关键差异：
//
//  1. 失败路径（Success=false / InfoSufficient=false）即使仍返回 sentinel error，
//     也会在失败之前完成 createTestPR + persistTestGenResult，让 Processor
//     即便拿到 error 也能拿到 PR 链接与 result metadata。
//
//  2. Success=false 路径新增 validateFailureTestGenOutput，覆盖 FailureCategory
//     枚举 + InfoSufficient 一致性；任一违反视为解析层失败（数据不可信）。
//
//  3. 完成后由 test.Service 主动 enqueue review（Success=true 或
//     ReviewOnFailure=*true 且 PRNumber>0）；入队器与 store 都是可选依赖。
//
// 流程：
//  1. 前置校验：Enabled / validateModule / resolveBaseRef / validateModuleExists
//  2. resolveFramework / buildPrompt / 容器执行
//  3. parseResult（外层 CLI 信封 → 内层 TestGenOutput → Success 分支不变量校验）
//  4. len(CommittedFiles)>0 即使 Success=false 也 createTestPR
//  5. 阶段 1 UPSERT（review_enqueued=0 占位）
//  6. review 入队决策 + 成功时阶段 2 UPSERT（review_enqueued=1）
//  7. 业务失败判定（!InfoSufficient / !Success → sentinel error）
//  8. return result, nil
func (s *Service) Execute(ctx context.Context, payload model.TaskPayload) (*TestGenResult, error) {
	// Step 1：前置校验
	tgCfg := s.cfgProv.ResolveTestGenConfig(payload.RepoFullName)
	// Enabled 指针语义：nil 视为默认启用；*false 才算显式禁用
	if tgCfg.Enabled != nil && !*tgCfg.Enabled {
		return nil, fmt.Errorf("%s: %w", payload.RepoFullName, ErrTestGenDisabled)
	}
	if err := validateModule(payload.Module, tgCfg.ModuleScope); err != nil {
		return nil, err
	}
	baseRef, err := s.resolveBaseRef(ctx, payload)
	if err != nil {
		return nil, err
	}
	// M4.2 修订：resolveBaseRef 解析后**必须**把 baseRef 回写到 payload，
	// 让 worker/container.go 的 buildContainerEnv 把 BASE_REF 注入容器环境。
	// 否则整仓模式或新增触发入口（M4.3 Webhook PR merged）忘记提前填充 baseRef
	// 时，entrypoint 看到空 BASE_REF 会在 `git rev-parse "origin/"` 上 set -e 退出，
	// 失败信息极度误导。
	payload.BaseRef = baseRef
	if err := s.validateModuleExists(ctx, payload, baseRef); err != nil {
		return nil, err
	}

	// Step 2：resolveFramework / buildPrompt / 容器执行
	chk := newFrameworkChecker(s.fileChecker, ctx, payload.RepoOwner, payload.RepoName, baseRef)
	requestFramework := strings.TrimSpace(payload.Framework)
	if requestFramework == "" {
		requestFramework = tgCfg.TestFramework
	}
	framework, anchor, anchorResolved, err := resolveFramework(requestFramework, payload.Module, chk)
	if err != nil {
		return nil, err
	}

	maxRetry := tgCfg.MaxRetryRounds
	if maxRetry <= 0 {
		maxRetry = 3 // 防御性默认，与 config.WithDefaults 保持一致
	}
	promptCtx := PromptContext{
		RepoFullName:    payload.RepoFullName,
		Module:          payload.Module,
		BaseRef:         baseRef,
		Timestamp:       time.Now().UTC().Format("20060102150405"),
		MavenModulePath: anchor,
		AnchorResolved:  anchorResolved,
		MaxRetryRounds:  maxRetry,
	}
	var prompt string
	switch framework {
	case FrameworkJUnit5:
		prompt = buildJavaPrompt(promptCtx)
	case FrameworkVitest:
		prompt = buildVuePrompt(promptCtx)
	default:
		// resolveFramework 只返回上述两个或 error；此处是防御性兜底。
		return nil, fmt.Errorf("未预期的 framework: %q", framework)
	}

	cmd := buildCommand(s.cfgProv)
	execResult, execErr := s.pool.RunWithCommandAndStdin(ctx, payload, cmd, []byte(prompt))
	result := &TestGenResult{
		Framework: framework,
		BaseRef:   baseRef,
		RawOutput: safeOutput(execResult),
	}
	if execErr != nil {
		return result, fmt.Errorf("容器执行失败: %w", execErr)
	}
	if execResult == nil {
		return result, fmt.Errorf("容器执行结果为空")
	}
	if execResult.ExitCode != 0 {
		result.ExitCode = execResult.ExitCode
		s.logger.ErrorContext(ctx, "gen_tests worker 非零退出",
			"repo", payload.RepoFullName, "module", payload.Module,
			"exit_code", execResult.ExitCode)
		return result, nil
	}

	// Step 3：解析 + 不变量校验（含 Success=false 的 validateFailureTestGenOutput）
	s.parseResult(result)
	if result.ParseError != nil {
		// ParseError 详情仅走日志，不混入返回 error（防 prompt injection）
		s.logger.ErrorContext(ctx, "TestGenOutput 解析失败",
			"repo", payload.RepoFullName, "module", payload.Module,
			"parse_error", result.ParseError)
		return result, fmt.Errorf("%s/%s module=%s: %w",
			payload.RepoOwner, payload.RepoName, payload.Module, ErrTestGenParseFailure)
	}

	out := result.Output

	// I7：在进入"对外产生副作用"的节点前检查 ctx.Err()。Cancel-and-Replace
	// 会在新任务入队时 cancel 旧任务的 asynq ctx；被取消任务若继续 createTestPR
	// / persistTestGenResult / EnqueueManualReview 就会留下僵尸 PR 与重复 review 任务。
	// 此处主动返回 context.Canceled，让 Processor 的 errors.Is(runErr, context.Canceled)
	// 分支把任务状态标记为 cancelled（见 processor.go:markTaskCancelled）。
	if err := ctx.Err(); err != nil {
		s.logger.InfoContext(ctx, "gen_tests 任务在 PR 创建前检测到 ctx 取消，跳过后续副作用",
			"repo", payload.RepoFullName, "module", payload.Module, "error", err)
		return result, err
	}

	// Step 4：createTestPR（Output==nil / BranchName 空 / CommittedFiles 空 → 返回 0）
	// 关键 M4.2 行为：即使 Success=false 也建 PR（保留半成品供用户接管），
	// 具体门控由 createTestPR 内部判定。
	prNum, prURL, prErr := s.createTestPR(ctx, payload, result)
	if prErr != nil {
		// PR 创建失败：仍返回 result 让上层保留原始输出做审计；
		// 但不做 persist（避免写入 PRNumber=0 的误导性记录）。
		s.logger.ErrorContext(ctx, "创建测试 PR 失败",
			"repo", payload.RepoFullName, "module", payload.Module,
			"branch", out.BranchName, "error", prErr)
		return result, fmt.Errorf("创建测试 PR 失败: %w", prErr)
	}
	result.PRNumber = prNum
	result.PRURL = prURL

	// Step 5：阶段 1 UPSERT（review_enqueued=0 占位）
	// 失败仅 warn 不阻断主流程（persistTestGenResult 内部处理）。
	s.persistTestGenResult(ctx, payload, result, false)

	// Step 6：review 入队决策（Success=true 默认入队；Success=false 仅在 ReviewOnFailure=*true 时入队）
	reviewOnFailure := tgCfg.ReviewOnFailure != nil && *tgCfg.ReviewOnFailure
	shouldEnqueue := s.reviewEnqueuer != nil && result.PRNumber > 0 &&
		(out.Success || reviewOnFailure)
	// I10 修订：检查 test_gen_results.review_enqueued 已为 1 则跳过（重试幂等）。
	// gen_tests 任务被 asynq 重试时，同一 PRNumber 下的 review 已被前一轮入队，
	// 再次入队会让 review 侧 Cancel-and-Replace 取消掉正在执行的 review，造成无谓
	// 的容器抖动。PR 编号变更（例如旧 PR 被关闭 + 新 PR 创建）会对应新的 PRNumber
	// 主键，这里的 guard 仍允许入队。
	if shouldEnqueue && s.store != nil && payload.TaskID != "" {
		if prev, err := s.lookupPreviousEnqueueState(ctx, payload.TaskID, result.PRNumber); err == nil && prev {
			s.logger.InfoContext(ctx, "检测到本次任务已为同一 PR 入队过 review，跳过重复入队",
				"task_id", payload.TaskID, "repo", payload.RepoFullName,
				"pr_number", result.PRNumber)
			shouldEnqueue = false
		}
	}
	if shouldEnqueue {
		reviewPayload := model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			RepoOwner:    payload.RepoOwner,
			RepoName:     payload.RepoName,
			RepoFullName: payload.RepoFullName,
			CloneURL:     payload.CloneURL,
			PRNumber:     result.PRNumber,
			BaseRef:      result.BaseRef,
			HeadRef:      out.BranchName,
			HeadSHA:      out.CommitSHA,
		}
		triggeredBy := "gen_tests:" + payload.TaskID
		if _, enqErr := s.reviewEnqueuer.EnqueueManualReview(ctx, reviewPayload, triggeredBy); enqErr != nil {
			// 不阻断主流程：review 入队失败仅记日志；test_gen_results.review_enqueued=0 供审计检索
			s.logger.WarnContext(ctx, "完成后入队 review 失败",
				"task_id", payload.TaskID, "repo", payload.RepoFullName,
				"pr_number", result.PRNumber, "error", enqErr)
		} else {
			// 阶段 2：partial UPDATE 只翻转 review_enqueued 标志，不再走全字段 UPSERT
			// （避免把阶段 1 之后可能已被更新的其它字段复写回阶段 1 的值）。
			s.markReviewEnqueued(ctx, payload)
		}
	}

	// Step 7：业务失败判定
	if !out.InfoSufficient {
		return result, fmt.Errorf("%s: %w", payload.RepoFullName, ErrInfoInsufficient)
	}
	if !out.Success {
		return result, fmt.Errorf("%s: %w", payload.RepoFullName, ErrTestGenFailed)
	}

	// Step 8：Success=true happy path
	return result, nil
}

// parseResult 双层 JSON 解析：外层 CLI 信封 → 内层 TestGenOutput → 不变量校验。
// 失败将信息写入 result.ParseError（不返回 error，由调用方统一决策）。
//
// M4.2：Success=true 走 validateSuccessfulTestGenOutput（强约束）；
// Success=false 额外走 validateFailureTestGenOutput（覆盖 FailureCategory 枚举
// + InfoSufficient 一致性；违反视为数据不可信 → ParseError）。
func (s *Service) parseResult(r *TestGenResult) {
	var cliResp CLIResponse
	if err := json.Unmarshal([]byte(r.RawOutput), &cliResp); err != nil {
		r.ParseError = fmt.Errorf("CLI JSON 解析失败: %w", err)
		return
	}
	r.CLIMeta = &model.CLIMeta{
		CostUSD:    cliResp.EffectiveCostUSD(),
		DurationMs: cliResp.DurationMs,
		IsError:    cliResp.IsExecutionError(),
		NumTurns:   cliResp.NumTurns,
		SessionID:  cliResp.SessionID,
	}
	if cliResp.IsExecutionError() {
		r.ParseError = fmt.Errorf("Claude CLI 报告错误 type=%s subtype=%s", cliResp.Type, cliResp.Subtype)
		return
	}
	jsonText := extractJSON(cliResp.Result)
	var out TestGenOutput
	if err := json.Unmarshal([]byte(jsonText), &out); err != nil {
		r.ParseError = fmt.Errorf("TestGenOutput JSON 解析失败: %w", err)
		return
	}
	if err := validateSuccessfulTestGenOutput(&out); err != nil {
		r.ParseError = err
		return
	}
	// M4.2：Success=false 补充 failure_category 枚举 + InfoSufficient 一致性校验。
	// 违反视为数据不可信 → ParseError，同等严重度，不走业务失败路径。
	if !out.Success {
		if err := validateFailureTestGenOutput(&out); err != nil {
			r.ParseError = err
			return
		}
	}
	// M4.2 安全加固：统一脱敏所有 Claude 自由文本字段（含 warnings 白名单过滤），
	// 防止 prompt-injection 内容流入飞书卡片 / PR body / Gitea 评论。详见
	// sanitizeTestGenOutput 函数说明。
	sanitizeTestGenOutput(&out)
	r.Output = &out
}

// resolveBaseRef 返回任务基准 ref。payload.BaseRef 空时回退到仓库默认分支。
// payload.BaseRef 非空时校验其存在（不存在 → ErrInvalidRef）。
func (s *Service) resolveBaseRef(ctx context.Context, payload model.TaskPayload) (string, error) {
	if payload.BaseRef != "" {
		if _, _, err := s.gitea.GetBranch(ctx, payload.RepoOwner, payload.RepoName, payload.BaseRef); err != nil {
			if gitea.IsNotFound(err) {
				return "", fmt.Errorf("%s/%s ref=%q: %w",
					payload.RepoOwner, payload.RepoName, payload.BaseRef, ErrInvalidRef)
			}
			return "", fmt.Errorf("校验 ref %q 失败: %w", payload.BaseRef, err)
		}
		return payload.BaseRef, nil
	}
	repo, _, err := s.gitea.GetRepo(ctx, payload.RepoOwner, payload.RepoName)
	if err != nil {
		return "", fmt.Errorf("获取仓库信息失败: %w", err)
	}
	if repo == nil || repo.DefaultBranch == "" {
		return "", fmt.Errorf("仓库 %s/%s 默认分支为空", payload.RepoOwner, payload.RepoName)
	}
	return repo.DefaultBranch, nil
}

// validateModule 校验 module 合法性 + ModuleScope 白名单前缀。
//
// 约束：
//   - 绝对路径（以 / 开头）或 path.Clean 后以 / 开头 → ErrInvalidModule
//   - 包含 .. 元素（".."、"../x"、"a/../b"、"a\..\b" 等）→ ErrInvalidModule
//   - module 空 + scope 非空 → ErrModuleOutOfScope（scope 要求必须指定）
//   - scope 非空 + cleaned 既不等于 scope 也不以 "scope/" 开头 → ErrModuleOutOfScope
//   - module 空 + scope 空 → 放行（整仓模式）
//
// 深层校验必须 ⊇ 入口层校验（validation.GenTestsModule）。入口层会把 Windows 风格
// 的 `\` 归一化为 `/` 再拒绝，若本函数只按 `/` 分段检查 `..`，会让绕过入口层直接
// 入队的调用（例如后续新增的 CronJob 或直接 queue.Enqueue）把 `a\..\b` 当作合法
// 路径通过，造成深浅校验不一致。
func validateModule(module, scope string) error {
	// 归一化 Windows 风格分隔符：与 validation.GenTestsModule 对齐。
	// 归一化后的值既用于语义检查，也用于错误消息，便于用户看到校验器实际看到的输入。
	module = strings.ReplaceAll(module, "\\", "/")

	if module == "" {
		if scope != "" {
			return fmt.Errorf("%w: test_gen.module_scope=%q 要求 module 必须指定", ErrModuleOutOfScope, scope)
		}
		return nil
	}

	cleaned := path.Clean(module)
	if strings.HasPrefix(module, "/") || strings.HasPrefix(cleaned, "/") {
		return fmt.Errorf("%w: module=%q 不能为绝对路径", ErrInvalidModule, module)
	}
	// 同时检查原字符串与 path.Clean 归一化结果：
	//   - 原字符串含 ".." 段（如 "a/../b"）→ 按语义拒绝（path.Clean 会扁平化，
	//     但用户意图可疑，一律视为非法）
	//   - cleaned 含 "../" 或等于 ".."：防止扁平化后依然能走出仓库根
	//
	// 说明：path.Clean 后不会存在中间 "/../" 段，因此无需再额外检查
	// strings.Contains("/"+cleaned+"/", "/../") —— containsDotDotSegment 已覆盖原字符串。
	if containsDotDotSegment(module) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("%w: module=%q 不能包含 ..", ErrInvalidModule, module)
	}
	if scope != "" {
		if cleaned != scope && !strings.HasPrefix(cleaned, scope+"/") {
			return fmt.Errorf("%w: module=%q 不在白名单前缀 %q 下", ErrModuleOutOfScope, module, scope)
		}
	}
	return nil
}

// moduleMarkerFiles 判断 module 子路径存在性时尝试探测的文件清单。
//
// 选择原则：优先命中几乎必然存在的"构建 / 描述"文件，再兜底到通用文件。
//   - 构建描述（Java / Gradle / Node）：直接命中常见后端模块
//   - README：大多数文档化模块都有
//   - .gitkeep：仅有空目录占位的极端情况
//
// 只要任一命中即视为 module 存在；全部不存在才返回 ErrModuleNotFound。
// 这避免了 RepoFileChecker 接口扩展 HasDir 的成本，也可兼容 M4.2 的多种实现
// （Gitea API / 容器内 fs）。
var moduleMarkerFiles = []string{
	"pom.xml",
	"build.gradle",
	"build.gradle.kts",
	"package.json",
	"README.md",
	"README",
	".gitkeep",
}

// validateModuleExists 通过 RepoFileChecker 校验 module 路径本身是否存在。
//
// 行为：
//   - payload.Module 为空（整仓模式）→ 跳过
//   - fileChecker 未注入 → 跳过（与 resolveFramework 对齐，允许 M4.1 单测灵活构造 Service）
//   - module 路径本身存在 → 通过（允许指向任意子目录，而非仅限构建模块根）
//   - 若底层实现不支持目录存在性查询，则回退到 marker 文件探测以兼容旧桩实现
//   - 查询错误 → 保守放行并记 warn 日志，避免临时性 Gitea 故障误杀任务
//   - 所有 marker 都返回 false → 返回 ErrModuleNotFound 供 Processor SkipRetry
func (s *Service) validateModuleExists(ctx context.Context, payload model.TaskPayload, baseRef string) error {
	if payload.Module == "" || s.fileChecker == nil {
		return nil
	}
	ok, err := s.fileChecker.HasFile(ctx, payload.RepoOwner, payload.RepoName, baseRef, payload.Module, "")
	if err != nil {
		s.logger.WarnContext(ctx, "module 存在性检查失败，保守放行",
			"repo", payload.RepoFullName,
			"module", payload.Module,
			"error", err,
		)
		return nil
	}
	if ok {
		return nil
	}
	for _, marker := range moduleMarkerFiles {
		ok, err := s.fileChecker.HasFile(ctx, payload.RepoOwner, payload.RepoName, baseRef,
			payload.Module, marker)
		if err != nil {
			// 保守放行：避免网络抖动或权限短暂失效导致合法任务被拒绝。
			// 记录 warn 以便后续从日志中回溯不稳定情况。
			s.logger.WarnContext(ctx, "module 存在性检查失败，保守放行",
				"repo", payload.RepoFullName,
				"module", payload.Module,
				"marker", marker,
				"error", err,
			)
			return nil
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("%w: %s/%s@%s module=%q",
		ErrModuleNotFound, payload.RepoOwner, payload.RepoName, baseRef, payload.Module)
}

// containsDotDotSegment 判断 path 是否含完整的 ".." 段（以 / 分隔）。
// 仅当 ".." 作为独立段出现时返回 true（"a..b" 这种名字合法）。
func containsDotDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// createTestPR 在 Claude 完成 push 后创建 / 复用 PR。
//
// M4.2 实装（与 fix.createFixPR 幂等模板对称）：
//   - Output==nil / BranchName 空 / CommittedFiles 空 → 返回 (0,"",nil)，不建 PR
//     （兼容 Success=false 且无产出的降级路径，不视为错误）
//   - PRClient 未注入 → 返回明确错误（production 路径必须注入）
//   - 幂等保护：先 ListRepoPullRequests 查询同 head 分支 open PR，存在则复用
//   - title 固定为 `test: 补充 <module|整仓> 测试用例`，250 字符截断
//   - body 由 FormatTestGenPRBody 渲染
//
// 关键语义：即使 Success=false，只要 len(CommittedFiles)>0 也建 PR——
// 保留半成品供用户接管（D5 决策）。
func (s *Service) createTestPR(ctx context.Context, payload model.TaskPayload,
	result *TestGenResult) (prNumber int64, prURL string, err error) {
	if result == nil || result.Output == nil {
		return 0, "", nil
	}
	out := result.Output
	if out.BranchName == "" || len(out.CommittedFiles) == 0 {
		return 0, "", nil
	}

	if s.prClient == nil {
		return 0, "", fmt.Errorf("PRClient 未注入")
	}

	// 1. 幂等：查同 head 分支 open PR
	if num, url, ok := s.findExistingTestPR(ctx, payload.RepoOwner, payload.RepoName, out.BranchName); ok {
		s.logger.InfoContext(ctx, "复用已存在的测试 PR（幂等）",
			"repo", payload.RepoFullName, "module", payload.Module,
			"branch", out.BranchName, "pr_number", num)
		return num, url, nil
	}

	// 2. 构造 title
	moduleLabel := payload.Module
	if strings.TrimSpace(moduleLabel) == "" {
		moduleLabel = "整仓"
	}
	title := fmt.Sprintf("test: 补充 %s 测试用例", moduleLabel)
	if len(title) > 250 {
		title = title[:250]
	}

	// 3. 构造 body（交由 pr_body.go 渲染，包含 GapAnalysis / TestResults 等）
	body := FormatTestGenPRBody(out, payload, string(result.Framework))

	pr, _, err := s.prClient.CreatePullRequest(ctx, payload.RepoOwner, payload.RepoName,
		gitea.CreatePullRequestOption{
			Head:  out.BranchName,
			Base:  result.BaseRef,
			Title: title,
			Body:  body,
		})
	if err != nil {
		return 0, "", err
	}
	if pr == nil {
		return 0, "", fmt.Errorf("Gitea API 返回空 PR")
	}
	return pr.Number, pr.HTMLURL, nil
}

// findExistingTestPR 查询同 head 分支的 open PR；找到返回 (number, url, true)。
//
// 列表查询失败按 fail-open 处理（返回 false），让后续 CreatePullRequest 走原本路径，
// 失败再处理——避免列表 API 短暂故障阻断主流程。模板对齐 fix.findExistingFixPR。
func (s *Service) findExistingTestPR(ctx context.Context, owner, repo, headBranch string) (int64, string, bool) {
	if headBranch == "" {
		return 0, "", false
	}
	prs, _, err := s.prClient.ListRepoPullRequests(ctx, owner, repo,
		gitea.ListPullRequestsOptions{
			State:       "open",
			ListOptions: gitea.ListOptions{PageSize: 50},
		})
	if err != nil {
		s.logger.WarnContext(ctx, "查询既有 PR 失败，跳过幂等检查继续创建",
			"owner", owner, "repo", repo, "head", headBranch, "error", err)
		return 0, "", false
	}
	for _, pr := range prs {
		if pr == nil || pr.Head == nil {
			continue
		}
		if pr.Head.Ref == headBranch {
			return pr.Number, pr.HTMLURL, true
		}
	}
	return 0, "", false
}

// persistTestGenResult 向 test_gen_results 表写入一次全字段 UPSERT（阶段 1 专用）。
//
// 阶段 1：Execute Step 5，reviewEnqueued=false 占位写入主结果。
// 阶段 2：走 markReviewEnqueued 的 partial UPDATE（见该方法说明）。
//
// 失败仅 warn 不阻断主流程；store 未注入时静默跳过（便于单测聚焦主流程）。
// 仅在 result.Output 非空时 persist——ParseError 路径没有结构化数据可存。
func (s *Service) persistTestGenResult(ctx context.Context, payload model.TaskPayload,
	result *TestGenResult, reviewEnqueued bool) {
	if s.store == nil || result == nil || result.Output == nil {
		return
	}
	rec := buildTestGenResultRecord(payload, result, reviewEnqueued)
	if err := s.store.SaveTestGenResult(ctx, rec); err != nil {
		s.logger.WarnContext(ctx, "持久化 test_gen_results 失败，忽略",
			"task_id", payload.TaskID,
			"repo", payload.RepoFullName,
			"module", payload.Module,
			"review_enqueued", reviewEnqueued,
			"error", err)
	}
}

// lookupPreviousEnqueueState 查询本 task_id 是否已为同一 PRNumber 入队过 review。
// 返回 true 表示需要跳过重复入队；查询失败（含未找到）一律返回 false（fail-open），
// 确保重试时不会因为瞬时 DB 错误而永远不入队。
func (s *Service) lookupPreviousEnqueueState(ctx context.Context, taskID string, prNumber int64) (bool, error) {
	rec, err := s.store.GetTestGenResultByTaskID(ctx, taskID)
	if err != nil {
		return false, err
	}
	if rec == nil {
		return false, nil
	}
	// PR 编号变化（例如旧 PR 被关闭 + 新 PR 创建）时不跳过：新 PRNumber 对应一次新的
	// review 入队需求；只有完全相同的 PR 上 review 已入队才算重复。
	return rec.ReviewEnqueued && rec.PRNumber == prNumber, nil
}

// markReviewEnqueued 阶段 2 专用：只把 review_enqueued 翻转为 true、刷新 updated_at。
// 相比全字段 UPSERT 更安全：不会覆盖阶段 1 之后由其它异步组件写入的字段。
// store 未注入 / task_id 空 / DB 错误均仅 warn，不阻断主流程。
func (s *Service) markReviewEnqueued(ctx context.Context, payload model.TaskPayload) {
	if s.store == nil || payload.TaskID == "" {
		return
	}
	if err := s.store.UpdateTestGenResultReviewEnqueued(ctx, payload.TaskID); err != nil {
		s.logger.WarnContext(ctx, "刷新 review_enqueued 失败，忽略",
			"task_id", payload.TaskID,
			"repo", payload.RepoFullName,
			"module", payload.Module,
			"error", err)
	}
}

// buildTestGenResultRecord 把 payload + result 映射为 store.TestGenResultRecord。
//
// 约定：
//   - record.ID 留空交给 store 生成 UUID
//   - record.OutputJSON = json.Marshal(result.Output)；序列化失败回退 "{}"
//     并记 warn（不吞错）
//   - failure_category 默认 "none"（Success=true 或未填场景；DB 默认值需要显式写入）
//   - TestResults / CLIMeta 若为空则对应字段留 0
//   - truncation 由 store 层负责（TestGenResultRecord 契约里 ≤2 KB 截断）
func buildTestGenResultRecord(payload model.TaskPayload, result *TestGenResult,
	reviewEnqueued bool) *store.TestGenResultRecord {
	out := result.Output // 调用方确保非 nil
	rec := &store.TestGenResultRecord{
		TaskID:             payload.TaskID,
		RepoFullName:       payload.RepoFullName,
		Module:             payload.Module,
		Framework:          string(result.Framework),
		BaseRef:            result.BaseRef,
		BranchName:         out.BranchName,
		CommitSHA:          out.CommitSHA,
		PRNumber:           result.PRNumber,
		PRURL:              result.PRURL,
		Success:            out.Success,
		InfoSufficient:     out.InfoSufficient,
		VerificationPassed: out.VerificationPassed,
		FailureCategory:    string(out.FailureCategory),
		FailureReason:      out.FailureReason,
		GeneratedCount:     len(out.GeneratedFiles),
		CommittedCount:     len(out.CommittedFiles),
		SkippedCount:       len(out.SkippedTargets),
		ReviewEnqueued:     reviewEnqueued,
	}
	// FailureCategory 空值归一为 "none"（与 SQL 列默认值对齐，便于查询）。
	if rec.FailureCategory == "" {
		rec.FailureCategory = string(FailureCategoryNone)
	}
	if out.TestResults != nil {
		rec.TestPassed = out.TestResults.Passed
		rec.TestFailed = out.TestResults.Failed
		rec.TestDurationMs = out.TestResults.DurationMs
	}
	if result.CLIMeta != nil {
		rec.CostUSD = result.CLIMeta.CostUSD
		rec.DurationMs = result.CLIMeta.DurationMs
	}
	// OutputJSON 序列化失败回退空对象；真实错误通过调用方日志覆盖。
	if b, err := json.Marshal(out); err == nil {
		rec.OutputJSON = string(b)
	} else {
		rec.OutputJSON = "{}"
	}
	return rec
}

// WriteDegraded M4.1 no-op 实现（仅结构化日志）。
//
// Processor 在解析失败且重试耗尽后调用本方法，目的是至少保留原始输出片段与
// parse_error 迹象便于排障。为避免原始输出中夹带凭证或 helper 内容，M4.1 不记录
// 任何原文 preview，只保留长度和 parse_error 标记。
func (s *Service) WriteDegraded(ctx context.Context, payload model.TaskPayload,
	result *TestGenResult) error {
	if result == nil {
		return nil
	}
	s.logger.ErrorContext(ctx, "gen_tests 降级回写（M4.1 no-op，仅记录原始输出）",
		"repo", payload.RepoFullName,
		"module", payload.Module,
		"raw_output_len", len(result.RawOutput),
		"has_parse_error", result.ParseError != nil,
	)
	return nil
}

// ============================================================================
// 内部适配器
// ============================================================================

// frameworkCheckerFunc 是 frameworkChecker 的函数类型实现。
// 通过闭包把 ctx / owner / repo / ref 捕获到函数体内，避免将 context.Context
// 存入 struct（违反 Go 惯例："Context should not be stored inside a struct type"）。
type frameworkCheckerFunc func(module, relPath string) bool

func (f frameworkCheckerFunc) HasFile(module, relPath string) bool {
	return f(module, relPath)
}

// newFrameworkChecker 构造一个把 ctx + 仓库元信息闭包封装的 frameworkChecker。
// inner 为 nil 时直接返回 nil，resolveFramework 对 nil checker 会返回 ErrNoFrameworkDetected。
func newFrameworkChecker(inner RepoFileChecker, ctx context.Context,
	owner, repo, ref string) frameworkChecker {
	if inner == nil {
		return nil
	}
	return frameworkCheckerFunc(func(module, relPath string) bool {
		ok, err := inner.HasFile(ctx, owner, repo, ref, module, relPath)
		if err != nil {
			return false
		}
		return ok
	})
}
