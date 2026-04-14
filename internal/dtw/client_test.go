package dtw

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Do_Success(t *testing.T) {
	type respData struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q，期望 Bearer test-token", r.Header.Get("Authorization"))
		}
		if r.Method != http.MethodGet {
			t.Errorf("Method = %q，期望 GET", r.Method)
		}
		if r.URL.Path != "/api/v1/hello" {
			t.Errorf("Path = %q，期望 /api/v1/hello", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": respData{Name: "test", Age: 42},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test-token")
	var result respData
	err := client.Do(context.Background(), "GET", "/api/v1/hello", nil, &result)
	if err != nil {
		t.Fatalf("Do 失败: %v", err)
	}
	if result.Name != "test" || result.Age != 42 {
		t.Errorf("result = %+v，期望 {test 42}", result)
	}
}

func TestClient_Do_WithBody(t *testing.T) {
	type reqBody struct {
		Msg string `json:"msg"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q，期望 application/json", r.Header.Get("Content-Type"))
		}
		var body reqBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("解析请求体失败: %v", err)
		}
		if body.Msg != "hello" {
			t.Errorf("body.Msg = %q，期望 hello", body.Msg)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"status": "ok"}})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	var result map[string]string
	err := client.Do(context.Background(), "POST", "/api/v1/action", reqBody{Msg: "hello"}, &result)
	if err != nil {
		t.Fatalf("Do 失败: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %q，期望 ok", result["status"])
	}
}

func TestClient_Do_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": APIError{Code: "INVALID_INPUT", Message: "缺少必填字段"},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	err := client.Do(context.Background(), "GET", "/api/v1/bad", nil, nil)
	if err == nil {
		t.Fatal("期望返回错误，但得到 nil")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 *APIError，但得到 %T: %v", err, err)
	}
	if apiErr.Code != "INVALID_INPUT" {
		t.Errorf("Code = %q，期望 INVALID_INPUT", apiErr.Code)
	}
}

func TestClient_Do_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	err := client.Do(context.Background(), "GET", "/api/v1/fail", nil, nil)
	if err == nil {
		t.Fatal("期望返回错误，但得到 nil")
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Fatal("不应解析为 APIError")
	}
}

func TestClient_Do_NilResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	err := client.Do(context.Background(), "DELETE", "/api/v1/item/1", nil, nil)
	if err != nil {
		t.Fatalf("Do 失败: %v", err)
	}
}
