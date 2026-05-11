package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

type E2EPoolRunner interface {
	RunWithCommandAndStdin(ctx context.Context, payload model.TaskPayload,
		cmd []string, stdinData []byte) (*worker.ExecutionResult, error)
}

type E2EConfigProvider interface {
	ResolveE2EConfig(repoFullName string) config.E2EOverride
	GetE2EEnvironments() map[string]config.E2EEnvironment
	GetClaudeModel() string
	GetClaudeEffort() string
}

// IssueClient Issue 创建 + 标签查询 + 附件上传（M5.2）。
type IssueClient interface {
	CreateIssue(ctx context.Context, owner, repo string,
		opts gitea.CreateIssueOption) (*gitea.Issue, *gitea.Response, error)
	ListRepoLabels(ctx context.Context, owner, repo string) ([]gitea.Label, *gitea.Response, error)
	CreateIssueAttachment(ctx context.Context, owner, repo string,
		issueIndex int64, filename string, reader io.Reader) (*gitea.Attachment, *gitea.Response, error)
}

// E2EStore E2E 结果持久化接口（M5.2）。
type E2EStore interface {
	SaveE2EResult(ctx context.Context, result *store.E2EResultRecord) error
	GetE2EResultByTaskID(ctx context.Context, taskID string) (*store.E2EResultRecord, error)
	UpdateE2ECreatedIssues(ctx context.Context, id string, issues map[string]int64) error
}

type Service struct {
	pool        E2EPoolRunner
	cfgProv     E2EConfigProvider
	issueClient IssueClient // M5.2：nil 时跳过 Issue 创建
	store       E2EStore    // M5.2：nil 时降级为仅日志
	artifactDir string      // M5.2：宿主机 artifact 根目录
	logger      *slog.Logger
}

type ServiceOption func(*Service)

func WithServiceLogger(logger *slog.Logger) ServiceOption {
	return func(s *Service) { s.logger = logger }
}

func WithIssueClient(c IssueClient) ServiceOption {
	return func(s *Service) { s.issueClient = c }
}

func WithStore(st E2EStore) ServiceOption {
	return func(s *Service) { s.store = st }
}

func WithArtifactDir(dir string) ServiceOption {
	return func(s *Service) { s.artifactDir = dir }
}

func NewService(pool E2EPoolRunner, cfgProv E2EConfigProvider, opts ...ServiceOption) *Service {
	if pool == nil {
		panic("e2e.NewService: pool 不能为 nil")
	}
	if cfgProv == nil {
		panic("e2e.NewService: cfgProv 不能为 nil")
	}
	s := &Service{pool: pool, cfgProv: cfgProv, logger: slog.Default()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) Execute(ctx context.Context, payload model.TaskPayload) (*E2EResult, error) {
	start := time.Now()

	override := s.cfgProv.ResolveE2EConfig(payload.RepoFullName)
	if override.Enabled != nil && !*override.Enabled {
		return nil, ErrE2EDisabled
	}

	env, err := s.resolveEnvironment(payload, override)
	if err != nil {
		return nil, err
	}

	baseURL := env.BaseURL
	if payload.BaseURLOverride != "" {
		baseURL = payload.BaseURLOverride
	}

	pctx := promptContext{
		RepoFullName: payload.RepoFullName,
		BaseRef:      payload.BaseRef,
		BaseURL:      baseURL,
		DB:           env.DB,
		Accounts:     env.Accounts,
		Module:       payload.Module,
		CaseName:     payload.CaseName,
		Model:        s.cfgProv.GetClaudeModel(),
		Effort:       s.cfgProv.GetClaudeEffort(),
	}
	prompt := buildE2EPrompt(pctx)

	payload.ExtraEnvs = buildE2EEnvVars(env, baseURL)

	cmd := buildClaudeCmd(pctx.Model, pctx.Effort)
	execResult, err := s.pool.RunWithCommandAndStdin(ctx, payload, cmd, []byte(prompt))
	if err != nil {
		return &E2EResult{RawOutput: "", DurationMs: time.Since(start).Milliseconds()}, err
	}

	output, parseErr := parseE2EResult(execResult.Output)
	result := &E2EResult{
		Output:     output,
		RawOutput:  execResult.Output,
		DurationMs: time.Since(start).Milliseconds(),
	}

	// --- M5.2: 赋值 Environment ---
	envName := payload.Environment
	if envName == "" {
		envName = override.DefaultEnv
	}
	result.Environment = envName

	if parseErr != nil {
		s.logger.ErrorContext(ctx, "E2E 输出解析失败",
			"task_id", payload.TaskID,
			"error", parseErr,
		)
		return result, fmt.Errorf("%w: %v", ErrE2EParseFailure, parseErr)
	}

	// --- M5.2: 阶段 1 持久化 ---
	var savedRecordID string
	var savedIssueNumbers map[string]int64
	if s.store != nil && output != nil {
		// 先读取旧记录，避免阶段 1 UPSERT 把阶段 2 写入的 created_issues 清空。
		if existing, err := s.store.GetE2EResultByTaskID(ctx, payload.TaskID); err == nil && existing != nil {
			savedRecordID = existing.ID
			savedIssueNumbers = cloneCreatedIssues(existing.CreatedIssues)
		} else if err != nil {
			s.logger.WarnContext(ctx, "查询已有 E2E 结果失败", "error", err)
		}

		rec := &store.E2EResultRecord{
			ID:            savedRecordID,
			TaskID:        payload.TaskID,
			Repo:          payload.RepoFullName,
			Environment:   envName,
			Module:        payload.Module,
			TotalCases:    output.TotalCases,
			PassedCases:   output.PassedCases,
			FailedCases:   output.FailedCases,
			ErrorCases:    output.ErrorCases,
			SkippedCases:  output.SkippedCases,
			Success:       output.Success,
			DurationMs:    result.DurationMs,
			CreatedIssues: cloneCreatedIssues(savedIssueNumbers),
		}
		if err := s.store.SaveE2EResult(ctx, rec); err != nil {
			s.logger.WarnContext(ctx, "E2E 结果持久化阶段 1 失败", "error", err)
		} else {
			savedRecordID = rec.ID
		}
	}
	if savedIssueNumbers == nil {
		savedIssueNumbers = make(map[string]int64)
	}

	// --- M5.2: processFailures（baseURL 由 Execute 顶部已解析，直接传入） ---
	result.CreatedIssues = make(map[string]int64)
	for k, v := range savedIssueNumbers {
		result.CreatedIssues[k] = v
	}
	if err := s.processFailures(ctx, payload, result, savedIssueNumbers, baseURL); err != nil {
		s.logger.WarnContext(ctx, "processFailures 失败", "error", err)
	}

	// --- M5.2: 阶段 2 持久化（写入 issue_number 映射） ---
	if s.store != nil && savedRecordID != "" && len(result.CreatedIssues) > 0 {
		if err := s.store.UpdateE2ECreatedIssues(ctx, savedRecordID, result.CreatedIssues); err != nil {
			s.logger.WarnContext(ctx, "E2E 结果持久化阶段 2 失败", "error", err)
		}
	}

	return result, nil
}

func (s *Service) resolveEnvironment(payload model.TaskPayload, override config.E2EOverride) (config.E2EEnvironment, error) {
	envName := payload.Environment
	if envName == "" {
		envName = override.DefaultEnv
	}
	if envName == "" {
		return config.E2EEnvironment{}, ErrEnvironmentNotFound
	}
	envs := s.cfgProv.GetE2EEnvironments()
	env, ok := envs[envName]
	if !ok {
		return config.E2EEnvironment{}, fmt.Errorf("%w: %q", ErrEnvironmentNotFound, envName)
	}
	return env, nil
}

func buildE2EEnvVars(env config.E2EEnvironment, baseURL string) []string {
	vars := []string{
		fmt.Sprintf("E2E_BASE_URL=%s", baseURL),
	}
	if env.DB != nil {
		vars = append(vars,
			fmt.Sprintf("E2E_DB_HOST=%s", env.DB.Host),
			fmt.Sprintf("E2E_DB_PORT=%d", env.DB.Port),
			fmt.Sprintf("E2E_DB_USER=%s", env.DB.User),
			fmt.Sprintf("E2E_DB_PASSWORD=%s", env.DB.Password),
			fmt.Sprintf("E2E_DB_DATABASE=%s", env.DB.Database),
		)
	}
	for name, acc := range env.Accounts {
		upper := strings.ToUpper(name)
		vars = append(vars,
			fmt.Sprintf("E2E_ACCOUNT_%s_USERNAME=%s", upper, acc.Username),
			fmt.Sprintf("E2E_ACCOUNT_%s_PASSWORD=%s", upper, acc.Password),
		)
	}
	return vars
}

func buildClaudeCmd(model, effort string) []string {
	cmd := []string{"claude", "-p", "--output-format", "json"}
	if model != "" {
		cmd = append(cmd, "--model", model)
	}
	if effort != "" {
		cmd = append(cmd, "--effort", effort)
	}
	cmd = append(cmd, "--disallowedTools", "Edit,Write,MultiEdit")
	cmd = append(cmd, "-")
	return cmd
}

func parseE2EResult(raw string) (*E2EOutput, error) {
	type cliEnvelope struct {
		Type   string          `json:"type"`
		IsErr  bool            `json:"is_error"`
		Result json.RawMessage `json:"result"`
	}
	raw = strings.TrimSpace(raw)
	var envelope cliEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err == nil && isE2ECLIEnvelope(envelope.Type, envelope.IsErr, envelope.Result) {
		if envelope.IsErr || !isE2ECLISuccessType(envelope.Type) {
			return nil, fmt.Errorf("Claude CLI 报告错误: type=%s is_error=%v", envelope.Type, envelope.IsErr)
		}
		raw = string(envelope.Result)
		var s string
		if json.Unmarshal([]byte(raw), &s) == nil {
			raw = s
		}
	}

	var output E2EOutput
	if err := json.Unmarshal([]byte(raw), &output); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}

	sanitizeE2EOutput(&output)

	if err := validateE2EOutput(&output); err != nil {
		return &output, fmt.Errorf("校验失败: %w", err)
	}

	return &output, nil
}

func isE2ECLIEnvelope(t string, isErr bool, result json.RawMessage) bool {
	if isErr || len(result) == 0 {
		return isErr
	}
	switch t {
	case "result", "success", "error", "error_during_execution":
		return true
	default:
		return false
	}
}

func isE2ECLISuccessType(t string) bool {
	switch t {
	case "", "result", "success":
		return true
	default:
		return false
	}
}

const maxScreenshotSize = 5 << 20 // 5MB

// processFailures 的 baseURL 参数由 Execute 传入（Execute 中已完成环境解析，无需重复）。
func (s *Service) processFailures(ctx context.Context, payload model.TaskPayload,
	result *E2EResult, savedIssueNumbers map[string]int64, baseURL string) error {
	if s.issueClient == nil || result.Output == nil {
		return nil
	}

	owner, repo, ok := splitRepo(payload.RepoFullName)
	if !ok {
		return fmt.Errorf("无效的仓库名: %s", payload.RepoFullName)
	}

	env := result.Environment

	var fixToPRLabelID int64 = -1 // -1 表示未查询

	for _, c := range result.Output.Cases {
		if (c.Status != "failed" && c.Status != "error") || c.FailureCategory == "environment" {
			continue
		}
		if _, exists := savedIssueNumbers[c.CasePath]; exists {
			continue
		}

		// 构建 Issue body
		var body string
		switch c.FailureCategory {
		case "bug":
			body = formatBugIssueBody(c, payload, env, baseURL)
		case "script_outdated":
			body = formatScriptOutdatedIssueBody(c, payload, env, baseURL)
		default:
			continue
		}

		title := formatIssueTitle(c.Module, c.Name, c.FailureCategory)

		// 查找 fix-to-pr 标签 ID（仅 script_outdated，且只查一次）
		var labelIDs []int64
		if c.FailureCategory == "script_outdated" {
			if fixToPRLabelID == -1 {
				fixToPRLabelID = s.lookupLabelID(ctx, owner, repo, "fix-to-pr")
			}
			if fixToPRLabelID > 0 {
				labelIDs = []int64{fixToPRLabelID}
			}
		}

		// 创建 Issue
		issue, _, err := s.issueClient.CreateIssue(ctx, owner, repo, gitea.CreateIssueOption{
			Title:  title,
			Body:   body,
			Labels: labelIDs,
		})
		if err != nil {
			s.logger.WarnContext(ctx, "创建 E2E Issue 失败",
				"case_path", c.CasePath, "error", err)
			continue
		}

		s.logger.InfoContext(ctx, "E2E Issue 已创建",
			"case_path", c.CasePath, "issue_number", issue.Number)

		// 上传截图
		s.uploadScreenshots(ctx, owner, repo, issue.Number, c, payload.TaskID)

		result.CreatedIssues[c.CasePath] = issue.Number
	}

	return nil
}

func (s *Service) lookupLabelID(ctx context.Context, owner, repo, labelName string) int64 {
	labels, _, err := s.issueClient.ListRepoLabels(ctx, owner, repo)
	if err != nil {
		s.logger.WarnContext(ctx, "查询仓库标签失败", "error", err)
		return 0
	}
	for _, l := range labels {
		if l.Name == labelName {
			return l.ID
		}
	}
	s.logger.WarnContext(ctx, "未找到标签", "label", labelName)
	return 0
}

func (s *Service) uploadScreenshots(ctx context.Context, owner, repo string,
	issueNumber int64, c CaseResult, taskID string) {
	for _, screenshotPath := range c.Screenshots {
		hostPath := s.convertScreenshotPath(screenshotPath, taskID)
		if hostPath == "" {
			continue
		}

		f, err := os.Open(hostPath)
		if err != nil {
			s.logger.WarnContext(ctx, "打开截图文件失败",
				"path", hostPath, "error", err)
			continue
		}

		// 检查文件大小
		info, err := f.Stat()
		if err != nil || info.Size() > maxScreenshotSize {
			f.Close()
			if err == nil {
				s.logger.WarnContext(ctx, "截图过大已跳过",
					"path", hostPath, "size", info.Size())
			}
			continue
		}

		filename := filepath.Base(hostPath)
		_, _, err = s.issueClient.CreateIssueAttachment(ctx, owner, repo, issueNumber, filename, f)
		f.Close()
		if err != nil {
			s.logger.WarnContext(ctx, "上传截图附件失败",
				"path", hostPath, "error", err)
		}
	}
}

// convertScreenshotPath 将容器内路径转为宿主机路径，并做路径遍历防护。
func (s *Service) convertScreenshotPath(containerPath, taskID string) string {
	if s.artifactDir == "" {
		return ""
	}
	const containerPrefix = "/workspace/artifacts/"
	if !strings.HasPrefix(containerPath, containerPrefix) {
		return ""
	}

	relPath := strings.TrimPrefix(containerPath, containerPrefix)
	hostPath := filepath.Join(s.artifactDir, "e2e-artifacts", taskID, relPath)
	hostPath = filepath.Clean(hostPath)

	expectedBase := filepath.Clean(filepath.Join(s.artifactDir, "e2e-artifacts", taskID))
	if !strings.HasPrefix(hostPath, expectedBase+string(filepath.Separator)) && hostPath != expectedBase {
		s.logger.Warn("截图路径遍历防护触发", "container_path", containerPath, "host_path", hostPath)
		return ""
	}

	return hostPath
}

func splitRepo(fullName string) (owner, repo string, ok bool) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func cloneCreatedIssues(src map[string]int64) map[string]int64 {
	dst := make(map[string]int64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
