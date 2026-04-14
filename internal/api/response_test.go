package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	Success(c, http.StatusOK, gin.H{"key": "value"})

	if w.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际 %d", w.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}

	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应中缺少 data 字段")
	}
	if data["key"] != "value" {
		t.Errorf("期望 data.key = \"value\"，实际 %v", data["key"])
	}
}

func TestSuccess_CreatedStatus(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	Success(c, http.StatusCreated, gin.H{"id": "123"})

	if w.Code != http.StatusCreated {
		t.Fatalf("期望状态码 201，实际 %d", w.Code)
	}
}

func TestError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	Error(c, http.StatusBadRequest, ErrCodeBadRequest, "参数缺失")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望状态码 400，实际 %d", w.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}

	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("响应中缺少 error 字段")
	}
	if errObj["code"] != ErrCodeBadRequest {
		t.Errorf("期望 error.code = %q，实际 %v", ErrCodeBadRequest, errObj["code"])
	}
	if errObj["message"] != "参数缺失" {
		t.Errorf("期望 error.message = \"参数缺失\"，实际 %v", errObj["message"])
	}

	// 确保没有 data 字段
	if _, exists := body["data"]; exists {
		t.Errorf("错误响应不应包含 data 字段")
	}
}
