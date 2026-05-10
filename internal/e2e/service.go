package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
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

type Service struct {
	pool    E2EPoolRunner
	cfgProv E2EConfigProvider
	logger  *slog.Logger
}

type ServiceOption func(*Service)

func WithServiceLogger(logger *slog.Logger) ServiceOption {
	return func(s *Service) { s.logger = logger }
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

	if parseErr != nil {
		s.logger.ErrorContext(ctx, "E2E 输出解析失败",
			"task_id", payload.TaskID,
			"error", parseErr,
		)
		return result, fmt.Errorf("%w: %v", ErrE2EParseFailure, parseErr)
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
