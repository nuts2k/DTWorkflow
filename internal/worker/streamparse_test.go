package worker

import (
	"encoding/json"
	"testing"
)

func TestIsResultEvent(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"result event", `{"type":"result","subtype":"success"}`, true},
		{"assistant event", `{"type":"assistant","message":{}}`, false},
		{"system event", `{"type":"system","subtype":"init"}`, false},
		{"empty line", "", false},
		{"invalid json", "not json", false},
		{"empty object", "{}", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isResultEvent(tt.line); got != tt.want {
				t.Errorf("isResultEvent(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseResultEvent(t *testing.T) {
	line := `{"type":"result","subtype":"success","cost_usd":0.05,"duration_ms":12345,"is_error":false,"num_turns":8,"result":"review output","session_id":"sess-123"}`
	e, err := parseResultEvent(line)
	if err != nil {
		t.Fatalf("parseResultEvent 返回错误: %v", err)
	}
	if e.Type != "result" {
		t.Errorf("Type = %q, want result", e.Type)
	}
	if e.Subtype != "success" {
		t.Errorf("Subtype = %q, want success", e.Subtype)
	}
	if e.CostUSD != 0.05 {
		t.Errorf("CostUSD = %f, want 0.05", e.CostUSD)
	}
	if e.NumTurns != 8 {
		t.Errorf("NumTurns = %d, want 8", e.NumTurns)
	}
	if e.Result != "review output" {
		t.Errorf("Result = %q", e.Result)
	}
}

func TestParseResultEvent_NotResult(t *testing.T) {
	_, err := parseResultEvent(`{"type":"assistant"}`)
	if err == nil {
		t.Error("非 result 事件应返回错误")
	}
}

func TestParseResultEvent_InvalidJSON(t *testing.T) {
	_, err := parseResultEvent("not json")
	if err == nil {
		t.Error("无效 JSON 应返回错误")
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

func TestResultEventToCLIJSON_PreservesUnknownFields(t *testing.T) {
	rawJSON := `{"type":"result","subtype":"success","cost_usd":0.05,"future_field":"hello","nested":{"a":1}}`
	out, err := resultEventToCLIJSON(rawJSON)
	if err != nil {
		t.Fatalf("返回错误: %v", err)
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
