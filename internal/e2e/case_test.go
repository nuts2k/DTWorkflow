package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestValidateCaseSpec_Valid(t *testing.T) {
	spec := &CaseSpec{
		Name: "创建工单后列表页正确展示",
		Test: []string{"create-order.spec.ts"},
	}
	assert.NoError(t, ValidateCaseSpec(spec))
}

func TestValidateCaseSpec_FullExample(t *testing.T) {
	spec := &CaseSpec{
		Name:        "创建工单后列表页正确展示",
		Description: "验证完整的工单创建和列表展示流程",
		Timeout:     Duration{5 * time.Minute},
		Tags:        []string{"order", "crud", "smoke"},
		Expectations: []Expectation{
			{Step: "以 admin 账号登录系统", Expect: "登录成功，跳转到首页"},
			{Step: "导航到工单创建页", Expect: "页面提示创建成功"},
		},
		Setup:    []string{"setup.sql", "seed-data.js"},
		Test:     []string{"create-order.spec.ts"},
		Teardown: []string{"teardown.sql"},
	}
	assert.NoError(t, ValidateCaseSpec(spec))
}

func TestValidateCaseSpec_Nil(t *testing.T) {
	assert.Error(t, ValidateCaseSpec(nil))
}

func TestValidateCaseSpec_EmptyName(t *testing.T) {
	spec := &CaseSpec{Name: "", Test: []string{"t.spec.ts"}}
	err := ValidateCaseSpec(spec)
	assert.ErrorContains(t, err, "name 必填")
}

func TestValidateCaseSpec_WhitespaceName(t *testing.T) {
	spec := &CaseSpec{Name: "   ", Test: []string{"t.spec.ts"}}
	assert.ErrorContains(t, ValidateCaseSpec(spec), "name 必填")
}

func TestValidateCaseSpec_EmptyTest(t *testing.T) {
	spec := &CaseSpec{Name: "test", Test: []string{}}
	assert.ErrorContains(t, ValidateCaseSpec(spec), "test 必填")
}

func TestValidateCaseSpec_TestNotSpecTS(t *testing.T) {
	spec := &CaseSpec{Name: "test", Test: []string{"run.ts"}}
	assert.ErrorContains(t, ValidateCaseSpec(spec), ".spec.ts")
}

func TestValidateCaseSpec_TimeoutRange(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		wantErr bool
	}{
		{"default zero", 0, false},
		{"30s min", 30 * time.Second, false},
		{"5m typical", 5 * time.Minute, false},
		{"30m max", 30 * time.Minute, false},
		{"below min", 10 * time.Second, true},
		{"above max", 31 * time.Minute, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &CaseSpec{
				Name:    "test",
				Timeout: Duration{tt.timeout},
				Test:    []string{"t.spec.ts"},
			}
			err := ValidateCaseSpec(spec)
			if tt.wantErr {
				assert.ErrorContains(t, err, "timeout")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateCaseSpec_ScriptValidation(t *testing.T) {
	tests := []struct {
		name    string
		scripts []string
		phase   string
		wantErr string
	}{
		{"valid sql", []string{"setup.sql"}, "setup", ""},
		{"valid js", []string{"seed.js"}, "setup", ""},
		{"valid ts", []string{"helper.ts"}, "setup", ""},
		{"absolute path", []string{"/etc/passwd"}, "setup", "绝对路径"},
		{"path traversal", []string{"../secret.sql"}, "setup", "路径遍历"},
		{"subdirectory", []string{"sub/script.sql"}, "setup", "路径分隔符"},
		{"empty name", []string{""}, "setup", "不能为空"},
		{"unsupported ext", []string{"data.json"}, "setup", "不支持的脚本扩展名"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &CaseSpec{
				Name: "test",
				Test: []string{"t.spec.ts"},
			}
			switch tt.phase {
			case "setup":
				spec.Setup = tt.scripts
			case "teardown":
				spec.Teardown = tt.scripts
			}
			err := ValidateCaseSpec(spec)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDuration_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"5 minutes", "timeout: 5m", 5 * time.Minute, false},
		{"30 seconds", "timeout: 30s", 30 * time.Second, false},
		{"empty default", "timeout: \"\"", DefaultCaseTimeout, false},
		{"invalid", "timeout: invalid", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out struct {
				Timeout Duration `yaml:"timeout"`
			}
			err := yaml.Unmarshal([]byte(tt.input), &out)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, out.Timeout.Duration)
			}
		})
	}
}

func TestCaseSpec_YAMLRoundTrip(t *testing.T) {
	input := `name: "创建工单后列表页正确展示"
description: "验证完整的工单创建和列表展示流程"
timeout: 5m
tags: [order, crud, smoke]
expectations:
  - step: "以 admin 账号登录系统"
    expect: "登录成功，跳转到首页"
  - step: "导航到工单创建页"
    expect: "页面提示创建成功"
setup: [setup.sql, seed-data.js]
test: [create-order.spec.ts]
teardown: [teardown.sql]
`
	var spec CaseSpec
	require.NoError(t, yaml.Unmarshal([]byte(input), &spec))
	assert.Equal(t, "创建工单后列表页正确展示", spec.Name)
	assert.Equal(t, "验证完整的工单创建和列表展示流程", spec.Description)
	assert.Equal(t, 5*time.Minute, spec.Timeout.Duration)
	assert.Equal(t, []string{"order", "crud", "smoke"}, spec.Tags)
	assert.Len(t, spec.Expectations, 2)
	assert.Equal(t, []string{"setup.sql", "seed-data.js"}, spec.Setup)
	assert.Equal(t, []string{"create-order.spec.ts"}, spec.Test)
	assert.Equal(t, []string{"teardown.sql"}, spec.Teardown)

	assert.NoError(t, ValidateCaseSpec(&spec))
}
