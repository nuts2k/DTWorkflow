package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestGetStatus(t *testing.T) {
	r := gin.New()
	startTime := time.Now().Add(-10 * time.Second)
	deps := Dependencies{
		Store:     newMockStore(),
		Tokens:    testTokens(),
		Version:   "v0.1.0",
		StartTime: startTime,
		Logger:    slog.Default(),
	}
	RegisterRoutes(r, deps)

	w := httptest.NewRecorder()
	req := authedRequest("GET", "/api/v1/status", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}

	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应中缺少 data 字段")
	}

	if data["version"] != "v0.1.0" {
		t.Errorf("期望 version = \"v0.1.0\"，实际 %v", data["version"])
	}

	uptimeRaw, ok := data["uptime_seconds"].(float64)
	if !ok {
		t.Fatalf("uptime_seconds 类型错误")
	}
	if uptimeRaw < 9 {
		t.Errorf("期望 uptime >= 9 秒，实际 %v", uptimeRaw)
	}

	// QueueClient 和 Pool 为 nil 时应返回默认值
	if data["redis_connected"] != false {
		t.Errorf("期望 redis_connected = false，实际 %v", data["redis_connected"])
	}
	activeRaw, ok := data["active_workers"].(float64)
	if !ok {
		t.Fatalf("active_workers 类型错误")
	}
	if int(activeRaw) != 0 {
		t.Errorf("期望 active_workers = 0，实际 %v", activeRaw)
	}
}
