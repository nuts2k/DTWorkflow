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

// TestPoolRunner 容器执行接口（与 review / fix 同类接口独立）。
type TestPoolRunner interface {
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

// WithPRClient 注入 PR 创建客户端（M4.2 必须；M4.1 createTestPR 占位不调用）。
func WithPRClient(c PRClient) ServiceOption {
	return func(s *Service) { s.prClient = c }
}

// WithFileChecker 注入仓库文件探测器。
// M4.1 单测必选（用于 resolveFramework）；M4.2 由生产实现注入。
func WithFileChecker(c RepoFileChecker) ServiceOption {
	return func(s *Service) { s.fileChecker = c }
}

// Service 测试生成编排服务，负责 gen_tests 任务的完整流程。
type Service struct {
	gitea       RepoClient
	prClient    PRClient
	pool        TestPoolRunner
	cfgProv     TestConfigProvider
	fileChecker RepoFileChecker
	logger      *slog.Logger
}

// NewService 创建 Service 实例。
// gitea / pool / cfgProv 为必要依赖，传入 nil 属于编程错误（panic）。
func NewService(gitea RepoClient, pool TestPoolRunner, cfgProv TestConfigProvider,
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

// Execute 执行 gen_tests 任务的完整流程（§4.2）。
//
// 顺序：
//  1. 解析并校验 test_gen 配置（Enabled 语义）
//  2. validateModule（路径合法性 + ModuleScope 白名单）
//  3. resolveBaseRef（空则回退仓库默认分支）
//  4. resolveFramework（请求级 framework > cfg.test_framework > 仓库探测）
//  5. 构造 prompt（Java / Vue 独立模板）
//  6. pool.RunWithCommandAndStdin
//  7. 解析结果（外层 CLI 信封 → 内层 TestGenOutput → 不变量校验）
//  8. 业务失败判定（InfoSufficient / Success）
//  9. createTestPR（M4.1 占位；M4.2 实装）
func (s *Service) Execute(ctx context.Context, payload model.TaskPayload) (*TestGenResult, error) {
	tgCfg := s.cfgProv.ResolveTestGenConfig(payload.RepoFullName)
	// Enabled 指针语义：nil 视为默认启用；*false 才算显式禁用
	if tgCfg.Enabled != nil && !*tgCfg.Enabled {
		return nil, fmt.Errorf("gen_tests 在仓库 %s 下被显式禁用", payload.RepoFullName)
	}

	// 1. module 合法性 + ModuleScope 白名单（纯字符串规则，无需访问仓库）
	if err := validateModule(payload.Module, tgCfg.ModuleScope); err != nil {
		return nil, err
	}

	// 2. resolveBaseRef
	baseRef, err := s.resolveBaseRef(ctx, payload)
	if err != nil {
		return nil, err
	}

	// 3. module 存在性校验（需 baseRef 后的真实访问；fileChecker 未注入则跳过）
	if err := s.validateModuleExists(ctx, payload, baseRef); err != nil {
		return nil, err
	}

	// 4. resolveFramework
	chk := &repoFileCheckerAdapter{
		inner: s.fileChecker,
		ctx:   ctx,
		owner: payload.RepoOwner,
		repo:  payload.RepoName,
		ref:   baseRef,
	}
	requestFramework := strings.TrimSpace(payload.Framework)
	if requestFramework == "" {
		requestFramework = tgCfg.TestFramework
	}
	framework, err := resolveFramework(requestFramework, payload.Module, chk)
	if err != nil {
		return nil, err
	}

	// 4. 构造 prompt
	maxRetry := tgCfg.MaxRetryRounds
	if maxRetry <= 0 {
		maxRetry = 3 // 防御性默认，与 config.WithDefaults 保持一致
	}
	promptCtx := PromptContext{
		RepoFullName:   payload.RepoFullName,
		Module:         payload.Module,
		BaseRef:        baseRef,
		Timestamp:      time.Now().UTC().Format("20060102150405"),
		MaxRetryRounds: maxRetry,
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

	// 5. 容器执行
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
	// RawOutput 已由 safeOutput(execResult) 设置；此处无需重复赋值。
	if execResult.ExitCode != 0 {
		result.ExitCode = execResult.ExitCode
		s.logger.ErrorContext(ctx, "gen_tests worker 非零退出",
			"repo", payload.RepoFullName, "module", payload.Module,
			"exit_code", execResult.ExitCode)
		return result, nil
	}

	// 6. 解析 + 不变量校验
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
	// 7. 业务失败判定
	if !out.InfoSufficient {
		return result, fmt.Errorf("%s: %w", payload.RepoFullName, ErrInfoInsufficient)
	}
	if !out.Success {
		return result, fmt.Errorf("%s: %w", payload.RepoFullName, ErrTestGenFailed)
	}

	// 8. createTestPR（M4.1 占位；M4.2 实装）
	prNum, prURL, prErr := s.createTestPR(ctx, payload, result)
	if prErr != nil {
		return result, fmt.Errorf("创建测试 PR 失败: %w", prErr)
	}
	result.PRNumber = prNum
	result.PRURL = prURL
	return result, nil
}

// parseResult 双层 JSON 解析：外层 CLI 信封 → 内层 TestGenOutput → 不变量校验。
// 失败将信息写入 result.ParseError（不返回 error，由调用方统一决策）。
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
//   - 包含 .. 元素（".."、"../x"、"a/../b"）→ ErrInvalidModule
//   - module 空 + scope 非空 → ErrModuleOutOfScope（scope 要求必须指定）
//   - scope 非空 + cleaned 既不等于 scope 也不以 "scope/" 开头 → ErrModuleOutOfScope
//   - module 空 + scope 空 → 放行（整仓模式）
func validateModule(module, scope string) error {
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
	//   - 原字符串含 "../" 或 ".." 尾：按语义拒绝（path.Clean 可能扁平化为合法路径，
	//     如 "services/../etc" → "etc"，但用户意图可疑，一律视为非法）
	//   - cleaned 含 "../" 或等于 ".."：防止扁平化后依然能走出仓库根
	if containsDotDotSegment(module) || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") || strings.Contains("/"+cleaned+"/", "/../") {
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

// createTestPR 在 Claude 完成 push 后创建 PR。
//
// M4.1：占位实现，直接返回零值；签名冻结（M4.2 不得修改）。
// 签名冻结的原因：M4.1 单测以此签名验证调用链；M4.2 实装时若改签名会破坏既有测试。
//
// M4.2 实装计划：
//   - 若 PRClient 未注入 → 返回明确错误
//   - ListRepoPullRequests 做幂等检查，已存在同 head 分支的 open PR 则复用
//   - 构造 title="test: gen_tests <module|all>"，body 含 GapAnalysis / TestResults
//   - CreatePullRequest(head=result.Output.BranchName, base=result.BaseRef)
func (s *Service) createTestPR(_ context.Context, _ model.TaskPayload,
	_ *TestGenResult) (prNumber int64, prURL string, err error) {
	return 0, "", nil
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

// repoFileCheckerAdapter 把 ctx + owner/repo/ref 三元组适配到 frameworkChecker。
//
// resolveFramework 需要一个同步 HasFile(module, relPath) 接口；业务层 RepoFileChecker
// 携带 ctx 与仓库元信息。此 adapter 在 Execute 中构造一次，消耗一个 ctx 生命周期。
type repoFileCheckerAdapter struct {
	inner RepoFileChecker
	ctx   context.Context
	owner string
	repo  string
	ref   string
}

// HasFile 委托给 inner.HasFile；inner 为 nil 或查询出错时返回 false
// （让 resolveFramework 走到错误分支而非静默判错）。
func (a *repoFileCheckerAdapter) HasFile(module, relPath string) bool {
	if a == nil || a.inner == nil {
		return false
	}
	ok, err := a.inner.HasFile(a.ctx, a.owner, a.repo, a.ref, module, relPath)
	if err != nil {
		return false
	}
	return ok
}
