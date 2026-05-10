package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
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
