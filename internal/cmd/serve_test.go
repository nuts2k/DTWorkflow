package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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
	defer func() {
		serveHost = oldHost
		servePort = oldPort
	}()
	serveHost = "127.0.0.1"
	servePort = port

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

	// 验证状态码
	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200, got %d", resp.StatusCode)
	}

	// 验证 JSON 响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("JSON 解析失败: %v, body: %s", err, body)
	}

	if result["status"] != "ok" {
		t.Errorf("status 应为 ok, got %q", result["status"])
	}
	if _, exists := result["version"]; !exists {
		t.Error("响应应包含 version 字段")
	}

	// 发送 SIGINT 触发优雅关闭
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
	defer func() {
		serveHost = oldHost
		servePort = oldPort
	}()
	serveHost = "127.0.0.1"
	servePort = port

	// runServe 应立即返回端口冲突错误
	err = runServe(nil, nil)
	if err == nil {
		t.Error("端口被占用时应返回错误")
	}
}
