package worker

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// TestSecretString_String 验证 String() 返回 [REDACTED]
func TestSecretString_String(t *testing.T) {
	s := SecretString("my-secret-key")
	if s.String() != "[REDACTED]" {
		t.Errorf("String() = %q, 期望 [REDACTED]", s.String())
	}
}

// TestSecretString_GoString 验证 GoString() 返回 [REDACTED]
func TestSecretString_GoString(t *testing.T) {
	s := SecretString("my-secret-key")
	if s.GoString() != "[REDACTED]" {
		t.Errorf("GoString() = %q, 期望 [REDACTED]", s.GoString())
	}
	// 验证 %#v 格式化不泄漏
	out := fmt.Sprintf("%#v", s)
	if out != "[REDACTED]" {
		t.Errorf("fmt %%#v = %q, 期望 [REDACTED]", out)
	}
}

// TestSecretString_MarshalJSON 验证 JSON 序列化不泄漏
func TestSecretString_MarshalJSON(t *testing.T) {
	s := SecretString("my-secret-key")
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("MarshalJSON 失败: %v", err)
	}
	if string(data) != `"[REDACTED]"` {
		t.Errorf("MarshalJSON = %s, 期望 \"[REDACTED]\"", string(data))
	}
}

// TestSecretString_MarshalText 验证 Text 序列化不泄漏
func TestSecretString_MarshalText(t *testing.T) {
	s := SecretString("my-secret-key")
	data, err := s.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText 失败: %v", err)
	}
	if string(data) != "[REDACTED]" {
		t.Errorf("MarshalText = %q, 期望 [REDACTED]", string(data))
	}
}

// TestSecretString_LogValue 验证 slog 日志不泄漏
func TestSecretString_LogValue(t *testing.T) {
	s := SecretString("my-secret-key")
	v := s.LogValue()
	if v.String() != "[REDACTED]" {
		t.Errorf("LogValue = %q, 期望 [REDACTED]", v.String())
	}
	if v.Kind() != slog.KindString {
		t.Errorf("LogValue Kind = %v, 期望 KindString", v.Kind())
	}
}

// TestSecretString_FmtPrint 验证 fmt.Sprint 不泄漏
func TestSecretString_FmtPrint(t *testing.T) {
	s := SecretString("super-secret")
	out := fmt.Sprint(s)
	if out != "[REDACTED]" {
		t.Errorf("fmt.Sprint = %q, 期望 [REDACTED]", out)
	}
	out = fmt.Sprintf("%s", s)
	if out != "[REDACTED]" {
		t.Errorf("fmt %%s = %q, 期望 [REDACTED]", out)
	}
}

// TestSecretString_InStruct 验证嵌入结构体时 JSON 序列化不泄漏
func TestSecretString_InStruct(t *testing.T) {
	type Config struct {
		Token SecretString `json:"token"`
	}
	c := Config{Token: "real-token"}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	if string(data) != `{"token":"[REDACTED]"}` {
		t.Errorf("JSON = %s, 期望 token 被遮蔽", string(data))
	}
}

// TestPoolConfig_Validate 验证 PoolConfig 各字段校验
func TestPoolConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  PoolConfig
		wantErr bool
	}{
		{
			name:    "全部为空",
			config:  PoolConfig{},
			wantErr: true,
		},
		{
			name:    "仅 Image",
			config:  PoolConfig{Image: "img"},
			wantErr: true,
		},
		{
			name:    "缺少 GiteaToken",
			config:  PoolConfig{Image: "img", GiteaURL: "http://g"},
			wantErr: true,
		},
		{
			name:    "缺少 ClaudeAPIKey",
			config:  PoolConfig{Image: "img", GiteaURL: "http://g", GiteaToken: "t"},
			wantErr: true,
		},
		{
			name: "全部填写",
			config: PoolConfig{
				Image:        "img",
				GiteaURL:     "http://g",
				GiteaToken:   "t",
				ClaudeAPIKey: "k",
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if tc.wantErr && err == nil {
				t.Error("期望错误，但返回 nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("非预期错误: %v", err)
			}
		})
	}
}

// TestExecutionResult_Fields 验证 ExecutionResult 字段赋值
func TestExecutionResult_Fields(t *testing.T) {
	r := ExecutionResult{
		ExitCode:    0,
		Output:      "output",
		Error:       "",
		Duration:    1234,
		ContainerID: "cid",
	}
	if r.ExitCode != 0 || r.Output != "output" || r.Duration != 1234 || r.ContainerID != "cid" {
		t.Error("字段赋值不匹配")
	}
}

// TestTaskTimeoutsConfig_Lookup 验证 Lookup 按任务类型返回正确超时值
func TestTaskTimeoutsConfig_Lookup(t *testing.T) {
	tests := []struct {
		name     string
		config   TaskTimeoutsConfig
		taskType model.TaskType
		want     time.Duration
	}{
		{
			name:     "ReviewPR 使用配置值",
			config:   TaskTimeoutsConfig{ReviewPR: 5 * time.Minute},
			taskType: model.TaskTypeReviewPR,
			want:     5 * time.Minute,
		},
		{
			name:     "ReviewPR 零值回退到默认 10m",
			config:   TaskTimeoutsConfig{},
			taskType: model.TaskTypeReviewPR,
			want:     10 * time.Minute,
		},
		{
			name:     "FixIssue 使用配置值",
			config:   TaskTimeoutsConfig{FixIssue: 15 * time.Minute},
			taskType: model.TaskTypeFixIssue,
			want:     15 * time.Minute,
		},
		{
			name:     "FixIssue 零值回退到默认 30m",
			config:   TaskTimeoutsConfig{},
			taskType: model.TaskTypeFixIssue,
			want:     30 * time.Minute,
		},
		{
			name:     "GenTests 使用配置值",
			config:   TaskTimeoutsConfig{GenTests: 8 * time.Minute},
			taskType: model.TaskTypeGenTests,
			want:     8 * time.Minute,
		},
		{
			name:     "GenTests 零值回退到默认 20m",
			config:   TaskTimeoutsConfig{},
			taskType: model.TaskTypeGenTests,
			want:     20 * time.Minute,
		},
		{
			name:     "未知任务类型回退到默认 10m",
			config:   TaskTimeoutsConfig{},
			taskType: model.TaskType("unknown"),
			want:     10 * time.Minute,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.config.Lookup(tc.taskType)
			if got != tc.want {
				t.Errorf("Lookup(%q) = %v, 期望 %v", tc.taskType, got, tc.want)
			}
		})
	}
}
