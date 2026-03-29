package worker

import (
	"encoding/json"
	"testing"
)

func TestTryExtractResultCLIJSON(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantOK  bool
		wantKey string // 验证输出中某个 key 的值
		wantVal any
	}{
		{
			name:    "result event",
			line:    `{"type":"result","subtype":"success","cost_usd":0.05}`,
			wantOK:  true,
			wantKey: "type",
			wantVal: "success",
		},
		{
			name:   "assistant event",
			line:   `{"type":"assistant","message":{}}`,
			wantOK: false,
		},
		{
			name:   "system event",
			line:   `{"type":"system","subtype":"init"}`,
			wantOK: false,
		},
		{
			name:   "empty line",
			line:   "",
			wantOK: false,
		},
		{
			name:   "invalid json",
			line:   "not json",
			wantOK: false,
		},
		{
			name:   "empty object",
			line:   "{}",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tryExtractResultCLIJSON(tt.line)
			if ok != tt.wantOK {
				t.Errorf("tryExtractResultCLIJSON(%q) ok = %v, want %v", tt.line, ok, tt.wantOK)
			}
			if ok && tt.wantKey != "" {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(got), &parsed); err != nil {
					t.Fatalf("输出不是合法 JSON: %v", err)
				}
				if parsed[tt.wantKey] != tt.wantVal {
					t.Errorf("%s = %v, want %v", tt.wantKey, parsed[tt.wantKey], tt.wantVal)
				}
				// subtype 应被移除
				if _, has := parsed["subtype"]; has {
					t.Error("subtype 字段应被移除")
				}
			}
		})
	}
}

func TestTryExtractResultCLIJSON_PreservesUnknownFields(t *testing.T) {
	rawJSON := `{"type":"result","subtype":"success","cost_usd":0.05,"future_field":"hello","nested":{"a":1}}`
	out, ok := tryExtractResultCLIJSON(rawJSON)
	if !ok {
		t.Fatal("应成功提取 result 事件")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("输出不是合法 JSON: %v", err)
	}
	if parsed["future_field"] != "hello" {
		t.Errorf("未知字段 future_field 应被保留，实际: %v", parsed["future_field"])
	}
	if parsed["nested"] == nil {
		t.Error("未知字段 nested 应被保留")
	}
}

func TestResultEventToCLIJSON(t *testing.T) {
	rawJSON := `{"type":"result","subtype":"success","cost_usd":0.05,"duration_ms":12345,"is_error":false,"num_turns":8,"result":"{\"summary\":\"good\",\"verdict\":\"approve\",\"issues\":[]}","session_id":"sess-123"}`
	out, err := resultEventToCLIJSON(rawJSON)
	if err != nil {
		t.Fatalf("返回错误: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("输出不是合法 JSON: %v", err)
	}
	if parsed["type"] != "success" {
		t.Errorf("type = %v, want success", parsed["type"])
	}
	if parsed["is_error"] != false {
		t.Errorf("is_error = %v, want false", parsed["is_error"])
	}
	// subtype 应被移除
	if _, ok := parsed["subtype"]; ok {
		t.Error("subtype 字段应被移除")
	}
}

func TestInjectStreamJsonFlags_NoExisting(t *testing.T) {
	cmd := []string{"claude", "-p", "-", "--disallowedTools", "Edit,Write"}
	got := injectStreamJsonFlags(cmd)
	assertContains(t, got, "--output-format")
	assertContains(t, got, "stream-json")
	assertContains(t, got, "--verbose")
	assertContains(t, got, "--include-partial-messages")
	assertContains(t, got, "--disallowedTools")
}

func TestInjectStreamJsonFlags_ReplaceExisting(t *testing.T) {
	cmd := []string{"claude", "-p", "-", "--output-format", "json", "--disallowedTools", "Edit"}
	got := injectStreamJsonFlags(cmd)
	for _, arg := range got {
		if arg == "json" {
			t.Error("旧的 --output-format json 应被替换")
		}
	}
	assertContains(t, got, "stream-json")
	assertContains(t, got, "--verbose")
}

func TestInjectStreamJsonFlags_ReplaceEqualsForm(t *testing.T) {
	cmd := []string{"claude", "-p", "-", "--output-format=json", "--disallowedTools", "Edit"}
	got := injectStreamJsonFlags(cmd)
	for _, arg := range got {
		if arg == "--output-format=json" {
			t.Error("旧的 --output-format=json（等号形式）应被替换")
		}
	}
	assertContains(t, got, "stream-json")
	assertContains(t, got, "--verbose")
	assertContains(t, got, "--disallowedTools")
}

func TestInjectStreamJsonFlags_EmptyCmd(t *testing.T) {
	got := injectStreamJsonFlags(nil)
	assertContains(t, got, "stream-json")
}

func assertContains(t *testing.T, slice []string, item string) {
	t.Helper()
	for _, s := range slice {
		if s == item {
			return
		}
	}
	t.Errorf("切片 %v 中未找到 %q", slice, item)
}
