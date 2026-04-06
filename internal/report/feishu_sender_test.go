package report

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewReportFeishuSender_EmptyURL(t *testing.T) {
	_, err := NewReportFeishuSender("", "")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestNewReportFeishuSender_Valid(t *testing.T) {
	s, err := NewReportFeishuSender("https://example.com/webhook", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil sender")
	}
}

func TestMarshalWithSign_NoSecret(t *testing.T) {
	s, _ := NewReportFeishuSender("https://example.com/webhook", "")
	card := map[string]any{"msg_type": "interactive"}

	data, err := s.marshalWithSign(card)
	if err != nil {
		t.Fatalf("marshalWithSign: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 无 secret 时不应有 timestamp/sign
	if _, ok := result["timestamp"]; ok {
		t.Error("should not have timestamp without secret")
	}
	if _, ok := result["sign"]; ok {
		t.Error("should not have sign without secret")
	}

	// 原 map 不应被修改
	if _, ok := card["timestamp"]; ok {
		t.Error("original map should not be modified")
	}
}

func TestMarshalWithSign_WithSecret(t *testing.T) {
	s, _ := NewReportFeishuSender("https://example.com/webhook", "test-secret")
	card := map[string]any{"msg_type": "interactive", "card": "data"}

	data, err := s.marshalWithSign(card)
	if err != nil {
		t.Fatalf("marshalWithSign: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 有 secret 时应有 timestamp 和 sign
	if _, ok := result["timestamp"]; !ok {
		t.Error("should have timestamp with secret")
	}
	if _, ok := result["sign"]; !ok {
		t.Error("should have sign with secret")
	}

	// 原 map 不应被修改（验证浅拷贝）
	if _, ok := card["timestamp"]; ok {
		t.Error("original map should not be modified (shallow copy broken)")
	}
	if _, ok := card["sign"]; ok {
		t.Error("original map should not be modified (shallow copy broken)")
	}
}

func TestSendCard_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"code":0,"msg":"success"}`)
	}))
	defer server.Close()

	s, _ := NewReportFeishuSender(server.URL, "")
	card := map[string]any{"msg_type": "interactive"}

	err := s.SendCard(context.Background(), card)
	if err != nil {
		t.Fatalf("SendCard: %v", err)
	}
}

func TestSendCard_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal server error")
	}))
	defer server.Close()

	s, _ := NewReportFeishuSender(server.URL, "")
	err := s.SendCard(context.Background(), map[string]any{"msg_type": "interactive"})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestSendCard_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		code := 19001
		resp := struct {
			Code *int   `json:"code"`
			Msg  string `json:"msg"`
		}{Code: &code, Msg: "token invalid"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	s, _ := NewReportFeishuSender(server.URL, "")
	err := s.SendCard(context.Background(), map[string]any{"msg_type": "interactive"})
	if err == nil {
		t.Fatal("expected error for API error code")
	}
}

func TestSendCard_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not reach server with cancelled context")
	}))
	defer server.Close()

	s, _ := NewReportFeishuSender(server.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	err := s.SendCard(ctx, map[string]any{"msg_type": "interactive"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
