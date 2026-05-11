package e2e

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

type mockConfigProvider struct {
	override     config.E2EOverride
	environments map[string]config.E2EEnvironment
	model        string
	effort       string
}

func (m *mockConfigProvider) ResolveE2EConfig(_ string) config.E2EOverride {
	return m.override
}
func (m *mockConfigProvider) GetE2EEnvironments() map[string]config.E2EEnvironment {
	return m.environments
}
func (m *mockConfigProvider) GetClaudeModel() string  { return m.model }
func (m *mockConfigProvider) GetClaudeEffort() string { return m.effort }

func TestResolveEnvironment_Priority(t *testing.T) {
	svc := &Service{
		cfgProv: &mockConfigProvider{
			override: config.E2EOverride{DefaultEnv: "staging"},
			environments: map[string]config.E2EEnvironment{
				"staging": {BaseURL: "https://staging.example.com"},
				"dev":     {BaseURL: "https://dev.example.com"},
			},
		},
	}

	// payload 指定 > override default
	payload := model.TaskPayload{Environment: "dev"}
	env, err := svc.resolveEnvironment(payload, config.E2EOverride{DefaultEnv: "staging"})
	require.NoError(t, err)
	assert.Equal(t, "https://dev.example.com", env.BaseURL)

	// payload 为空，回退到 override
	payload2 := model.TaskPayload{}
	env2, err := svc.resolveEnvironment(payload2, config.E2EOverride{DefaultEnv: "staging"})
	require.NoError(t, err)
	assert.Equal(t, "https://staging.example.com", env2.BaseURL)

	// 都为空 → error
	_, err = svc.resolveEnvironment(model.TaskPayload{}, config.E2EOverride{})
	assert.ErrorIs(t, err, ErrEnvironmentNotFound)
}

func TestBuildE2EEnvVars(t *testing.T) {
	env := config.E2EEnvironment{
		BaseURL: "https://staging.example.com",
		DB: &config.E2EDBConfig{
			Host: "db.internal", Port: 3306, User: "root", Password: "pass", Database: "testdb",
		},
		Accounts: map[string]config.E2EAccountConfig{
			"admin": {Username: "admin", Password: "admin123"},
		},
	}
	vars := buildE2EEnvVars(env, "https://staging.example.com")
	assert.Contains(t, vars, "E2E_BASE_URL=https://staging.example.com")
	assert.Contains(t, vars, "E2E_DB_HOST=db.internal")
	assert.Contains(t, vars, "E2E_DB_PORT=3306")
	assert.Contains(t, vars, "E2E_ACCOUNT_ADMIN_USERNAME=admin")
	assert.Contains(t, vars, "E2E_ACCOUNT_ADMIN_PASSWORD=admin123")
}

func TestParseE2EResult_DirectJSON(t *testing.T) {
	raw := `{"success":true,"total_cases":1,"passed_cases":1,"failed_cases":0,"error_cases":0,"skipped_cases":0,"cases":[{"name":"test1","module":"order","case_path":"e2e/order/cases/test1","status":"passed","duration_ms":1000}]}`
	output, err := parseE2EResult(raw)
	require.NoError(t, err)
	assert.True(t, output.Success)
	assert.Equal(t, 1, output.TotalCases)
}

func TestParseE2EResult_CLIEnvelope(t *testing.T) {
	inner := `{"success":true,"total_cases":1,"passed_cases":1,"failed_cases":0,"error_cases":0,"skipped_cases":0,"cases":[{"name":"test1","module":"order","case_path":"e2e/order/cases/test1","status":"passed","duration_ms":500}]}`
	raw := `{"type":"result","result":` + inner + `}`
	output, err := parseE2EResult(raw)
	require.NoError(t, err)
	assert.True(t, output.Success)
}

func TestParseE2EResult_StreamMonitorSuccessEnvelope(t *testing.T) {
	inner := `{"success":true,"total_cases":1,"passed_cases":1,"failed_cases":0,"error_cases":0,"skipped_cases":0,"cases":[{"name":"test1","module":"order","case_path":"e2e/order/cases/test1","status":"passed","duration_ms":500}]}`
	raw := `{"type":"success","is_error":false,"result":` + strconv.Quote(inner) + `}`
	output, err := parseE2EResult(raw)
	require.NoError(t, err)
	assert.True(t, output.Success)
	assert.Equal(t, 1, output.PassedCases)
}

func TestParseE2EResult_CLIErrorEnvelope(t *testing.T) {
	raw := `{"type":"error_during_execution","is_error":true,"result":""}`
	output, err := parseE2EResult(raw)
	require.Error(t, err)
	assert.Nil(t, output)
	assert.Contains(t, err.Error(), "Claude CLI 报告错误")
}

func TestValidateE2EOutput_SuccessWithFailed(t *testing.T) {
	o := &E2EOutput{Success: true, TotalCases: 1, PassedCases: 0, FailedCases: 1}
	err := validateE2EOutput(o)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "success=true")
}

func TestValidateE2EOutput_TotalMismatch(t *testing.T) {
	o := &E2EOutput{Success: true, TotalCases: 5, PassedCases: 3}
	err := validateE2EOutput(o)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "total_cases")
}

func TestSanitizeE2EOutput_Truncation(t *testing.T) {
	longStr := make([]rune, 3000)
	for i := range longStr {
		longStr[i] = 'a'
	}
	o := &E2EOutput{
		Warnings: []string{string(longStr)},
		Cases: []CaseResult{
			{FailureAnalysis: string(longStr)},
		},
	}
	sanitizeE2EOutput(o)
	assert.Len(t, []rune(o.Warnings[0]), maxOutputLen)
	assert.Len(t, []rune(o.Cases[0].FailureAnalysis), maxOutputLen)
}

func TestBuildClaudeCmd(t *testing.T) {
	cmd := buildClaudeCmd("claude-sonnet-4-6", "high")
	assert.Contains(t, cmd, "--model")
	assert.Contains(t, cmd, "claude-sonnet-4-6")
	assert.Contains(t, cmd, "--effort")
	assert.Contains(t, cmd, "high")
	assert.Equal(t, "-", cmd[len(cmd)-1])
}

// --- M5.2: processFailures 测试 mock ---

type mockPool struct{}

func (m *mockPool) RunWithCommandAndStdin(_ context.Context, _ model.TaskPayload,
	_ []string, _ []byte) (*worker.ExecutionResult, error) {
	return &worker.ExecutionResult{}, nil
}

type mockIssueClient struct {
	createdIssues []gitea.CreateIssueOption
	labels        []gitea.Label
	createErr     error
	labelsErr     error
}

func (m *mockIssueClient) CreateIssue(_ context.Context, _, _ string,
	opts gitea.CreateIssueOption) (*gitea.Issue, *gitea.Response, error) {
	if m.createErr != nil {
		return nil, nil, m.createErr
	}
	m.createdIssues = append(m.createdIssues, opts)
	return &gitea.Issue{Number: int64(len(m.createdIssues) + 40)}, nil, nil
}

func (m *mockIssueClient) ListRepoLabels(_ context.Context, _, _ string) ([]gitea.Label, *gitea.Response, error) {
	return m.labels, nil, m.labelsErr
}

func (m *mockIssueClient) CreateIssueAttachment(_ context.Context, _, _ string,
	_ int64, filename string, _ io.Reader) (*gitea.Attachment, *gitea.Response, error) {
	return &gitea.Attachment{ID: 1, Name: filename}, nil, nil
}

type mockE2EStore struct {
	saved   *store.E2EResultRecord
	updated map[string]int64
}

func (m *mockE2EStore) SaveE2EResult(_ context.Context, record *store.E2EResultRecord) error {
	record.ID = "test-record-id"
	m.saved = record
	return nil
}

func (m *mockE2EStore) GetE2EResultByTaskID(_ context.Context, taskID string) (*store.E2EResultRecord, error) {
	if m.saved != nil && m.saved.TaskID == taskID {
		return m.saved, nil
	}
	return nil, nil
}

func (m *mockE2EStore) UpdateE2ECreatedIssues(_ context.Context, _ string, issues map[string]int64) error {
	m.updated = issues
	return nil
}

// --- M5.2: processFailures 测试 ---

func TestProcessFailures_BugCreatesIssue(t *testing.T) {
	ic := &mockIssueClient{
		labels: []gitea.Label{{ID: 5, Name: "fix-to-pr"}},
	}
	svc := NewService(&mockPool{}, &mockConfigProvider{},
		WithIssueClient(ic),
		WithServiceLogger(slog.Default()),
	)

	result := &E2EResult{
		Output: &E2EOutput{
			Cases: []CaseResult{
				{Name: "create-order", Module: "order", CasePath: "e2e/order/cases/create-order",
					Status: "failed", FailureCategory: "bug", FailureAnalysis: "按钮失效"},
			},
		},
		CreatedIssues: make(map[string]int64),
	}

	payload := model.TaskPayload{TaskID: "t1", RepoFullName: "owner/repo", BaseRef: "main"}
	err := svc.processFailures(context.Background(), payload, result, map[string]int64{}, "https://app.example.com")
	if err != nil {
		t.Fatalf("processFailures: %v", err)
	}
	if len(ic.createdIssues) != 1 {
		t.Fatalf("期望创建 1 个 Issue，实际=%d", len(ic.createdIssues))
	}
	if !strings.Contains(ic.createdIssues[0].Title, "bug") {
		t.Errorf("标题应包含 bug: %s", ic.createdIssues[0].Title)
	}
	if len(ic.createdIssues[0].Labels) != 0 {
		t.Errorf("bug 类不应携带标签，实际=%v", ic.createdIssues[0].Labels)
	}
}

func TestProcessFailures_ScriptOutdatedWithLabel(t *testing.T) {
	ic := &mockIssueClient{
		labels: []gitea.Label{{ID: 5, Name: "fix-to-pr"}},
	}
	svc := NewService(&mockPool{}, &mockConfigProvider{},
		WithIssueClient(ic),
		WithServiceLogger(slog.Default()),
	)

	result := &E2EResult{
		Output: &E2EOutput{
			Cases: []CaseResult{
				{Name: "login", Module: "auth", CasePath: "e2e/auth/cases/login",
					Status: "failed", FailureCategory: "script_outdated"},
			},
		},
		CreatedIssues: make(map[string]int64),
	}

	payload := model.TaskPayload{TaskID: "t2", RepoFullName: "owner/repo"}
	_ = svc.processFailures(context.Background(), payload, result, map[string]int64{}, "https://app.example.com")

	if len(ic.createdIssues) != 1 {
		t.Fatalf("期望创建 1 个 Issue，实际=%d", len(ic.createdIssues))
	}
	if len(ic.createdIssues[0].Labels) != 1 || ic.createdIssues[0].Labels[0] != 5 {
		t.Errorf("script_outdated 应携带 fix-to-pr 标签 ID=5，实际=%v", ic.createdIssues[0].Labels)
	}
}

func TestProcessFailures_EnvironmentSkipped(t *testing.T) {
	ic := &mockIssueClient{}
	svc := NewService(&mockPool{}, &mockConfigProvider{},
		WithIssueClient(ic),
		WithServiceLogger(slog.Default()),
	)

	result := &E2EResult{
		Output: &E2EOutput{
			Cases: []CaseResult{
				{Name: "checkout", Module: "payment", CasePath: "e2e/payment/cases/checkout",
					Status: "failed", FailureCategory: "environment"},
			},
		},
		CreatedIssues: make(map[string]int64),
	}

	payload := model.TaskPayload{TaskID: "t3", RepoFullName: "owner/repo"}
	_ = svc.processFailures(context.Background(), payload, result, map[string]int64{}, "https://app.example.com")

	if len(ic.createdIssues) != 0 {
		t.Errorf("environment 失败不应创建 Issue，实际创建=%d", len(ic.createdIssues))
	}
}

func TestProcessFailures_IdempotentGuard(t *testing.T) {
	ic := &mockIssueClient{}
	svc := NewService(&mockPool{}, &mockConfigProvider{},
		WithIssueClient(ic),
		WithServiceLogger(slog.Default()),
	)

	result := &E2EResult{
		Output: &E2EOutput{
			Cases: []CaseResult{
				{Name: "create-order", Module: "order", CasePath: "e2e/order/cases/create-order",
					Status: "failed", FailureCategory: "bug"},
			},
		},
		CreatedIssues: make(map[string]int64),
	}

	// 模拟已有 Issue（幂等）
	saved := map[string]int64{"e2e/order/cases/create-order": 42}
	payload := model.TaskPayload{TaskID: "t4", RepoFullName: "owner/repo"}
	_ = svc.processFailures(context.Background(), payload, result, saved, "https://app.example.com")

	if len(ic.createdIssues) != 0 {
		t.Errorf("幂等 guard 应跳过已有 Issue，实际创建=%d", len(ic.createdIssues))
	}
}

func TestProcessFailures_CreateIssueError_Degraded(t *testing.T) {
	ic := &mockIssueClient{
		createErr: fmt.Errorf("Gitea API 500"),
	}
	svc := NewService(&mockPool{}, &mockConfigProvider{},
		WithIssueClient(ic),
		WithServiceLogger(slog.Default()),
	)

	result := &E2EResult{
		Output: &E2EOutput{
			Cases: []CaseResult{
				{Name: "a", Module: "m", CasePath: "e2e/m/cases/a",
					Status: "failed", FailureCategory: "bug"},
				{Name: "b", Module: "m", CasePath: "e2e/m/cases/b",
					Status: "failed", FailureCategory: "bug"},
			},
		},
		CreatedIssues: make(map[string]int64),
	}

	payload := model.TaskPayload{TaskID: "t5", RepoFullName: "owner/repo"}
	err := svc.processFailures(context.Background(), payload, result, map[string]int64{}, "https://app.example.com")
	// Issue 创建失败不阻断主流程
	if err != nil {
		t.Errorf("processFailures 不应返回错误: %v", err)
	}
	if len(result.CreatedIssues) != 0 {
		t.Errorf("创建失败时 CreatedIssues 应为空，实际=%d", len(result.CreatedIssues))
	}
}

func TestConvertScreenshotPath_Valid(t *testing.T) {
	svc := &Service{artifactDir: "/data", logger: slog.Default()}
	got := svc.convertScreenshotPath("/workspace/artifacts/test-results/login/screenshot.png", "task-1")
	expected := filepath.Join("/data", "e2e-artifacts", "task-1", "test-results", "login", "screenshot.png")
	if got != expected {
		t.Errorf("路径转换: 期望=%s 实际=%s", expected, got)
	}
}

func TestConvertScreenshotPath_TraversalBlocked(t *testing.T) {
	svc := &Service{artifactDir: "/data", logger: slog.Default()}
	got := svc.convertScreenshotPath("/workspace/artifacts/../../../etc/passwd", "task-1")
	if got != "" {
		t.Errorf("路径遍历应被拦截，实际=%s", got)
	}
}

func TestConvertScreenshotPath_NoPrefix(t *testing.T) {
	svc := &Service{artifactDir: "/data", logger: slog.Default()}
	got := svc.convertScreenshotPath("/tmp/random/file.png", "task-1")
	if got != "" {
		t.Errorf("非 artifact 路径应返回空，实际=%s", got)
	}
}
