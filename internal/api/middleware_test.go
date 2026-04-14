package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
)

func TestTokenAuth_NoHeader(t *testing.T) {
	r := gin.New()
	r.Use(TokenAuth(testTokens()))
	r.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d", w.Code)
	}
	assertErrorCode(t, w, ErrCodeUnauthorized)
}

func TestTokenAuth_WrongPrefix(t *testing.T) {
	r := gin.New()
	r.Use(TokenAuth(testTokens()))
	r.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Basic sometoken")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d", w.Code)
	}
}

func TestTokenAuth_WrongToken(t *testing.T) {
	r := gin.New()
	r.Use(TokenAuth(testTokens()))
	r.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d", w.Code)
	}
}

func TestTokenAuth_ValidToken(t *testing.T) {
	var gotIdentity string
	r := gin.New()
	r.Use(TokenAuth(testTokens()))
	r.GET("/test", func(c *gin.Context) {
		v, _ := c.Get(ContextKeyIdentity)
		gotIdentity, _ = v.(string)
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}
	if gotIdentity != testIdentity {
		t.Errorf("期望 identity = %q，实际 %q", testIdentity, gotIdentity)
	}
}

func TestTokenAuth_MultipleTokens(t *testing.T) {
	tokens := []config.TokenConfig{
		{Token: "token-a", Identity: "user-a"},
		{Token: "token-b", Identity: "user-b"},
	}

	var gotIdentity string
	r := gin.New()
	r.Use(TokenAuth(tokens))
	r.GET("/test", func(c *gin.Context) {
		v, _ := c.Get(ContextKeyIdentity)
		gotIdentity, _ = v.(string)
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer token-b")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}
	if gotIdentity != "user-b" {
		t.Errorf("期望 identity = \"user-b\"，实际 %q", gotIdentity)
	}
}

func assertErrorCode(t *testing.T, w *httptest.ResponseRecorder, expectedCode string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("响应中缺少 error 字段")
	}
	if errObj["code"] != expectedCode {
		t.Errorf("期望 error.code = %q，实际 %v", expectedCode, errObj["code"])
	}
}
