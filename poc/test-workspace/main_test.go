package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHelloHandler 测试根路径处理器
func TestHelloHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	helloHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("期望状态码 %d，实际 %d", http.StatusOK, w.Code)
	}
	if !strings.Contains(w.Body.String(), "DTWorkflow") {
		t.Errorf("响应体中未找到 'DTWorkflow'，实际：%s", w.Body.String())
	}
}

// TestHealthHandler 测试健康检查处理器
func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	healthHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("期望状态码 %d，实际 %d", http.StatusOK, w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("期望 Content-Type 包含 application/json，实际：%s", contentType)
	}

	var resp healthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("JSON 解析失败：%v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("期望 status 为 'ok'，实际：%s", resp.Status)
	}
	if resp.Timestamp == "" {
		t.Error("timestamp 不应为空")
	}
	if resp.Version == "" {
		t.Error("version 不应为空")
	}
}
