package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

// getFreePort 获取一个可用的随机端口
func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("获取空闲端口失败: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// TestServe_Healthz 测试 /healthz 端点返回正确的 JSON 响应
func TestServe_Healthz(t *testing.T) {
	port := getFreePort(t)

	// 保存并恢复全局状态
	oldHost, oldPort := serveHost, servePort
	oldSecret := serveWebhookSecret
	oldAPIKey := serveClaudeAPIKey
	defer func() {
		serveHost = oldHost
		servePort = oldPort
		serveWebhookSecret = oldSecret
		serveClaudeAPIKey = oldAPIKey
	}()
	serveHost = "127.0.0.1"
	servePort = port
	serveWebhookSecret = "secret"
	serveClaudeAPIKey = "test-api-key"

	// 在 goroutine 中启动 serve，它会阻塞等待信号
	errCh := make(chan error, 1)
	go func() {
		errCh <- runServe(nil, nil)
	}()

	// 等待服务就绪
	addr := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	var resp *http.Response
	var err error
	for i := 0; i < 20; i++ {
		resp, err = http.Get(addr) //nolint:gosec // 测试用固定地址
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("无法连接到 healthz 端点: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 验证状态码：测试环境无 Redis，健康检查返回 degraded/503 是预期行为
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("状态码应为 200 或 503, got %d", resp.StatusCode)
	}

	// 验证 JSON 响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("JSON 解析失败: %v, body: %s", err, body)
	}

	// 测试环境无 Redis，status 可能是 "ok"（Redis 可用）或 "degraded"（Redis 不可用）
	if result["status"] != "ok" && result["status"] != "degraded" {
		t.Errorf("status 应为 ok 或 degraded, got %v", result["status"])
	}
	if _, exists := result["version"]; !exists {
		t.Error("响应应包含 version 字段")
	}
	if _, exists := result["redis"]; !exists {
		t.Error("响应应包含 redis 字段")
	}
	if _, exists := result["sqlite"]; !exists {
		t.Error("响应应包含 sqlite 字段")
	}

	// 注意：向当前进程发送 SIGINT 在 CI 环境中可能影响其他 goroutine。
	// 后续考虑重构为 channel-based 关闭触发。
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("发送 SIGINT 失败: %v", err)
	}

	// 等待 serve 退出
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runServe 应返回 nil, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServe 未在 10 秒内退出")
	}
}

// TestServe_PortConflict 测试端口被占用时返回错误
func TestServe_PortConflict(t *testing.T) {
	// 先占用一个端口
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	defer func() { _ = l.Close() }()

	oldHost, oldPort := serveHost, servePort
	oldSecret := serveWebhookSecret
	oldAPIKey := serveClaudeAPIKey
	defer func() {
		serveHost = oldHost
		servePort = oldPort
		serveWebhookSecret = oldSecret
		serveClaudeAPIKey = oldAPIKey
	}()
	serveHost = "127.0.0.1"
	servePort = port
	serveWebhookSecret = "secret"
	serveClaudeAPIKey = "test-api-key"

	// runServe 应立即返回端口冲突错误
	err = runServe(nil, nil)
	if err == nil {
		t.Error("端口被占用时应返回错误")
	}
}

func TestServe_RequiresWebhookSecret(t *testing.T) {
	oldHost, oldPort := serveHost, servePort
	oldSecret := serveWebhookSecret
	oldAPIKey := serveClaudeAPIKey
	defer func() {
		serveHost, servePort = oldHost, oldPort
		serveWebhookSecret = oldSecret
		serveClaudeAPIKey = oldAPIKey
	}()

	serveHost = "127.0.0.1"
	servePort = getFreePort(t)
	serveWebhookSecret = ""
	serveClaudeAPIKey = "test-api-key"

	err := runServe(nil, nil)
	if err == nil {
		t.Fatal("runServe should fail when webhook secret is empty")
	}
	if !strings.Contains(err.Error(), "webhook-secret") {
		t.Fatalf("error = %v, want message containing webhook-secret", err)
	}
}

func TestServe_WebhookRouteReturnsUnauthorizedWithoutSignature(t *testing.T) {
	port := getFreePort(t)
	oldHost, oldPort := serveHost, servePort
	oldSecret := serveWebhookSecret
	oldAPIKey := serveClaudeAPIKey
	defer func() {
		serveHost, servePort = oldHost, oldPort
		serveWebhookSecret = oldSecret
		serveClaudeAPIKey = oldAPIKey
	}()
	serveHost = "127.0.0.1"
	servePort = port
	serveWebhookSecret = "secret"
	serveClaudeAPIKey = "test-api-key"

	errCh := make(chan error, 1)
	go func() { errCh <- runServe(nil, nil) }()

	addr := fmt.Sprintf("http://127.0.0.1:%d/webhooks/gitea", port)
	var resp *http.Response
	var err error
	for i := 0; i < 20; i++ {
		resp, err = http.Post(addr, "application/json", strings.NewReader(`{}`)) //nolint:gosec // 测试用固定地址
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("POST webhook failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	// 注意：向当前进程发送 SIGINT 在 CI 环境中可能影响其他 goroutine。
	// 后续考虑重构为 channel-based 关闭触发。
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("发送 SIGINT 失败: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runServe 应返回 nil, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServe 未在 10 秒内退出")
	}
}
