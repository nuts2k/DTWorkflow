package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestPrintResult_JSON(t *testing.T) {
	// 保存并恢复全局状态
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = true

	// 捕获 stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	data := map[string]string{"key": "value"}
	PrintResult(data, func(any) string { return "不应到达此处" })

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	var parsed map[string]string
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("JSON 解析失败: %v, 输出: %s", err, output)
	}
	if parsed["key"] != "value" {
		t.Errorf("JSON 输出内容不匹配: got %q, want %q", parsed["key"], "value")
	}
}

func TestPrintResult_Human(t *testing.T) {
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	// 捕获 stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintResult("data", func(v any) string {
		return fmt.Sprintf("人类可读: %s", v)
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if output != "人类可读: data" {
		t.Errorf("人类可读输出不匹配: got %q", output)
	}
}

func TestPrintResult_NoHTMLEscape(t *testing.T) {
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = true

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	data := map[string]string{"msg": "<仓库名>"}
	PrintResult(data, nil)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	// SetEscapeHTML(false) 应保留原始 < > 字符
	if strings.Contains(output, `\u003c`) {
		t.Errorf("JSON 不应转义 HTML 字符: %s", output)
	}
	if !strings.Contains(output, `<仓库名>`) {
		t.Errorf("JSON 应包含原始 <仓库名>: %s", output)
	}
}

func TestPrintError_JSON(t *testing.T) {
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = true

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintError(fmt.Errorf("测试错误"))

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("JSON 解析失败: %v, 输出: %s", err, output)
	}
	if parsed["error"] != "测试错误" {
		t.Errorf("错误消息不匹配: got %v", parsed["error"])
	}
	if parsed["exitCode"] != float64(1) {
		t.Errorf("退出码不匹配: got %v, want 1", parsed["exitCode"])
	}
}

func TestPrintError_JSON_ExitCodeError(t *testing.T) {
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = true

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintError(&ExitCodeError{Code: 2, Err: fmt.Errorf("部分成功")})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if parsed["exitCode"] != float64(2) {
		t.Errorf("退出码不匹配: got %v, want 2", parsed["exitCode"])
	}
}

func TestPrintError_Human(t *testing.T) {
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	PrintError(fmt.Errorf("人类错误"))

	_ = w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := strings.TrimSpace(buf.String())

	if output != "人类错误" {
		t.Errorf("stderr 输出不匹配: got %q, want %q", output, "人类错误")
	}
}

func TestPrintResult_JSON_EncodeFail(t *testing.T) {
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = true

	// 捕获 stderr（编码失败时输出到 stderr）
	oldErr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// chan 类型无法 JSON 序列化，触发编码失败分支
	PrintResult(make(chan int), nil)

	_ = w.Close()
	os.Stderr = oldErr

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "JSON 输出失败") {
		t.Errorf("编码失败时应输出到 stderr, got %q", output)
	}
}
