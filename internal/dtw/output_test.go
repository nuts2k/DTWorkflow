package dtw

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPrinter_JSON(t *testing.T) {
	var buf bytes.Buffer
	p := &Printer{Mode: OutputJSON, Writer: &buf}

	data := map[string]string{"status": "ok", "id": "123"}
	if err := p.PrintJSON(data); err != nil {
		t.Fatalf("PrintJSON 失败: %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %q，期望 ok", result["status"])
	}
}

func TestPrinter_Human(t *testing.T) {
	var buf bytes.Buffer
	p := &Printer{Mode: OutputHuman, Writer: &buf}

	p.PrintHuman("任务 %s 已完成", "t1")
	got := buf.String()
	expected := "任务 t1 已完成\n"
	if got != expected {
		t.Errorf("输出 = %q，期望 %q", got, expected)
	}
}

func TestPrinter_Print_JSONMode(t *testing.T) {
	var buf bytes.Buffer
	p := &Printer{Mode: OutputJSON, Writer: &buf}

	data := map[string]int{"count": 5}
	if err := p.Print("有 5 个结果", data); err != nil {
		t.Fatalf("Print 失败: %v", err)
	}

	var result map[string]int
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if result["count"] != 5 {
		t.Errorf("count = %d，期望 5", result["count"])
	}
}

func TestPrinter_Print_HumanMode(t *testing.T) {
	var buf bytes.Buffer
	p := &Printer{Mode: OutputHuman, Writer: &buf}

	data := map[string]int{"count": 5}
	if err := p.Print("有 5 个结果", data); err != nil {
		t.Fatalf("Print 失败: %v", err)
	}

	got := buf.String()
	expected := "有 5 个结果\n"
	if got != expected {
		t.Errorf("输出 = %q，期望 %q", got, expected)
	}
}

func TestNewPrinter(t *testing.T) {
	p := NewPrinter(true)
	if p.Mode != OutputJSON {
		t.Errorf("Mode = %d，期望 OutputJSON", p.Mode)
	}

	p = NewPrinter(false)
	if p.Mode != OutputHuman {
		t.Errorf("Mode = %d，期望 OutputHuman", p.Mode)
	}
}
