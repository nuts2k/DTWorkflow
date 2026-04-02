package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewFeishuNotifier_MissingWebhookURL(t *testing.T) {
	_, err := NewFeishuNotifier("")
	if err == nil {
		t.Fatal("空 webhookURL 应返回错误")
	}
}

func TestFeishuNotifier_Name(t *testing.T) {
	n, err := NewFeishuNotifier("https://example.com/hook")
	if err != nil {
		t.Fatalf("NewFeishuNotifier error: %v", err)
	}
	if n.Name() != "feishu" {
		t.Errorf("Name() = %q, want feishu", n.Name())
	}
}

func TestFeishuNotifier_Send_Success(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %s, want application/json", ct)
		}
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body error: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"StatusCode":0,"StatusMessage":"success"}`))
	}))
	defer srv.Close()

	n, err := NewFeishuNotifier(srv.URL)
	if err != nil {
		t.Fatalf("NewFeishuNotifier error: %v", err)
	}

	msg := Message{
		EventType: EventPRReviewStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 42, IsPR: true},
		Title:     "PR 评审开始",
		Body:      "正在评审 PR #42",
		Metadata:  map[string]string{"pr_url": "https://gitea.example.com/org/repo/pulls/42"},
	}

	if err := n.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send error: %v", err)
	}

	if len(receivedBody) == 0 {
		t.Fatal("服务端未收到请求体")
	}
	var body map[string]any
	if err := json.Unmarshal(receivedBody, &body); err != nil {
		t.Fatalf("JSON unmarshal error: %v", err)
	}
	if body["msg_type"] != "interactive" {
		t.Errorf("msg_type = %v, want interactive", body["msg_type"])
	}
}

func TestFeishuNotifier_Send_WithSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("JSON unmarshal error: %v", err)
		}
		if _, ok := payload["timestamp"]; !ok {
			t.Error("签名模式下应包含 timestamp")
		}
		if _, ok := payload["sign"]; !ok {
			t.Error("签名模式下应包含 sign")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"StatusCode":0,"StatusMessage":"success"}`))
	}))
	defer srv.Close()

	n, err := NewFeishuNotifier(srv.URL, WithFeishuSecret("test-secret"))
	if err != nil {
		t.Fatalf("NewFeishuNotifier error: %v", err)
	}

	msg := Message{
		EventType: EventPRReviewStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1, IsPR: true},
		Title:     "test",
		Body:      "test",
	}

	if err := n.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send error: %v", err)
	}
}

func TestFeishuNotifier_Send_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n, err := NewFeishuNotifier(srv.URL)
	if err != nil {
		t.Fatalf("NewFeishuNotifier error: %v", err)
	}

	msg := Message{
		EventType: EventPRReviewStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1, IsPR: true},
		Title:     "test",
		Body:      "test",
	}

	err = n.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("非 2xx 响应应返回错误")
	}
}

func TestFeishuNotifier_Send_429Warning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	n, err := NewFeishuNotifier(srv.URL)
	if err != nil {
		t.Fatalf("NewFeishuNotifier error: %v", err)
	}

	msg := Message{
		EventType: EventPRReviewStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1, IsPR: true},
		Title:     "test",
		Body:      "test",
	}

	err = n.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("429 应返回错误")
	}
	if !strings.Contains(err.Error(), "rate limit") && !strings.Contains(err.Error(), "429") {
		t.Errorf("429 错误应包含 rate limit 或 429 标识，得到: %v", err)
	}
}

func TestFeishuNotifier_Send_APIErrorIn200Response(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"StatusCode":19024,"StatusMessage":"invalid sign"}`))
	}))
	defer srv.Close()

	n, err := NewFeishuNotifier(srv.URL)
	if err != nil {
		t.Fatalf("NewFeishuNotifier error: %v", err)
	}

	msg := Message{
		EventType: EventPRReviewStarted,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1, IsPR: true},
		Title:     "test",
		Body:      "test",
	}

	err = n.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("200 响应中的飞书 API 错误应返回错误")
	}
	if !strings.Contains(err.Error(), "19024") || !strings.Contains(err.Error(), "invalid sign") {
		t.Fatalf("错误应包含飞书业务错误码与消息，得到: %v", err)
	}
}

func TestGenSign(t *testing.T) {
	sign, err := genSign("test-secret", 1617000000)
	if err != nil {
		t.Fatalf("genSign error: %v", err)
	}
	if sign == "" {
		t.Fatal("签名不应为空")
	}
	sign2, _ := genSign("test-secret", 1617000000)
	if sign != sign2 {
		t.Errorf("相同输入应产生相同签名: %q != %q", sign, sign2)
	}
	sign3, _ := genSign("other-secret", 1617000000)
	if sign == sign3 {
		t.Error("不同 secret 应产生不同签名")
	}
}
