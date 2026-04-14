package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRegisterRoutes_NoTokens_SkipsRegistration(t *testing.T) {
	r := gin.New()
	deps := Dependencies{
		Tokens: nil,
		Logger: slog.Default(),
	}
	RegisterRoutes(r, deps)

	// 所有路由应返回 404（未注册）
	paths := []string{
		"/api/v1/status",
		"/api/v1/tasks",
	}
	for _, path := range paths {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer any")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("路径 %s: 期望 404（未注册），实际 %d", path, w.Code)
		}
	}
}

func TestRegisterRoutes_WithTokens_RegistersAllRoutes(t *testing.T) {
	r := gin.New()
	s := newMockStore()
	deps := Dependencies{
		Store:     s,
		Tokens:    testTokens(),
		Version:   "test",
		StartTime: time.Now(),
		Logger:    slog.Default(),
	}
	RegisterRoutes(r, deps)

	tests := []struct {
		method string
		path   string
		expect int // 不是 404 即可（说明路由已注册）
	}{
		{"GET", "/api/v1/status", http.StatusOK},
		{"GET", "/api/v1/tasks", http.StatusOK},
		{"GET", "/api/v1/tasks/nonexist", http.StatusNotFound},
		{"POST", "/api/v1/tasks/nonexist/retry", http.StatusNotFound},
		{"POST", "/api/v1/repos/owner/repo/review-pr", http.StatusBadRequest},
		{"POST", "/api/v1/repos/owner/repo/fix-issue", http.StatusBadRequest},
	}

	for _, tc := range tests {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		r.ServeHTTP(w, req)
		if w.Code != tc.expect {
			t.Errorf("%s %s: 期望 %d，实际 %d", tc.method, tc.path, tc.expect, w.Code)
		}
	}
}

func TestRegisterRoutes_UnauthedRequest_Returns401(t *testing.T) {
	r := gin.New()
	s := newMockStore()
	deps := Dependencies{
		Store:     s,
		Tokens:    testTokens(),
		Version:   "test",
		StartTime: time.Now(),
		Logger:    slog.Default(),
	}
	RegisterRoutes(r, deps)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("期望 401，实际 %d", w.Code)
	}
}
