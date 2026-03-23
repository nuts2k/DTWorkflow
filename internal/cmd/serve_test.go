package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
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

// skipIfNoRedis 如果本地 Redis 不可用则跳过测试
func skipIfNoRedis(t *testing.T, addr string) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer func() { _ = rdb.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("跳过测试：本地 Redis (%s) 不可用: %v", addr, err)
	}
}

// newTestConfig 构造测试用的 serveConfig，使用独立的随机端口
func newTestConfig(t *testing.T) serveConfig {
	t.Helper()
	return serveConfig{
		Host:          "127.0.0.1",
		Port:          getFreePort(t),
		RedisAddr:     "localhost:6379",
		DBPath:        t.TempDir() + "/test.db",
		WebhookSecret: "secret",
		ClaudeAPIKey:  "test-api-key",
		MaxWorkers:    1,
		WorkerImage:   "dtworkflow-worker:1.0",
	}
}

// TestServe_Healthz 测试 /healthz（liveness）端点始终返回 200
func TestBuildNotifier_NilClient(t *testing.T) {
	router, err := buildNotifier(nil)
	if err != nil {
		t.Fatalf("buildNotifier(nil) error: %v", err)
	}
	if router != nil {
		t.Fatal("buildNotifier(nil) should return nil router")
	}
}

func TestBuildNotifier_WithClient(t *testing.T) {
	client, err := gitea.NewClient("https://gitea.example.com", gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	router, err := buildNotifier(client)
	if err != nil {
		t.Fatalf("buildNotifier(client) error: %v", err)
	}
	if router == nil {
		t.Fatal("buildNotifier(client) should return non-nil router")
	}
}

func TestComputeReadyStatus_DegradedWhenWorkerImageMissing(t *testing.T) {
	payload, httpStatus := computeReadyStatus(readinessSnapshot{
		RedisOK:            true,
		SQLiteOK:           true,
		GiteaConfigured:    true,
		NotifierEnabled:    true,
		WorkerImagePresent: false,
		ActiveWorkers:      0,
	})
	if httpStatus != http.StatusServiceUnavailable {
		t.Fatalf("http status = %d, want %d", httpStatus, http.StatusServiceUnavailable)
	}
	if payload["worker_image_present"] != false {
		t.Fatalf("worker_image_present = %v, want false", payload["worker_image_present"])
	}
}

func TestBuildServiceDeps_WithoutGiteaConfig_NotifierIsNil(t *testing.T) {
	cfg := newTestConfig(t)
	skipIfNoRedis(t, cfg.RedisAddr)
	deps, cleanup, err := BuildServiceDeps(cfg)
	if err != nil {
		t.Fatalf("BuildServiceDeps error: %v", err)
	}
	defer cleanup()
	if deps.Notifier != nil {
		t.Fatal("deps.Notifier should be nil when Gitea config is absent")
	}
	if deps.GiteaConfigured {
		t.Fatal("deps.GiteaConfigured should be false when Gitea config is absent")
	}
	if deps.NotifierEnabled {
		t.Fatal("deps.NotifierEnabled should be false when notifier is absent")
	}
}

func TestBuildServiceDeps_WithGiteaConfig_BuildsNotifier(t *testing.T) {
	cfg := newTestConfig(t)
	skipIfNoRedis(t, cfg.RedisAddr)
	cfg.GiteaURL = "https://gitea.example.com"
	cfg.GiteaToken = "test-token"
	deps, cleanup, err := BuildServiceDeps(cfg)
	if err != nil {
		t.Fatalf("BuildServiceDeps error: %v", err)
	}
	defer cleanup()
	if deps.Notifier == nil {
		t.Fatal("deps.Notifier should be non-nil when Gitea config is present")
	}
	if !deps.GiteaConfigured {
		t.Fatal("deps.GiteaConfigured should be true when Gitea config is present")
	}
	if !deps.NotifierEnabled {
		t.Fatal("deps.NotifierEnabled should be true when notifier is present")
	}
}

func TestComputeReadyStatus_DegradedWhenGiteaMissing(t *testing.T) {
	payload, httpStatus := computeReadyStatus(readinessSnapshot{
		RedisOK:            true,
		SQLiteOK:           true,
		GiteaConfigured:    false,
		NotifierEnabled:    false,
		WorkerImagePresent: true,
		ActiveWorkers:      0,
	})
	if httpStatus != http.StatusServiceUnavailable {
		t.Fatalf("http status = %d, want %d", httpStatus, http.StatusServiceUnavailable)
	}
	if payload["status"] != "degraded" {
		t.Fatalf("status = %v, want degraded", payload["status"])
	}
	if payload["gitea_configured"] != false {
		t.Fatalf("gitea_configured = %v, want false", payload["gitea_configured"])
	}
	if payload["notifier_enabled"] != false {
		t.Fatalf("notifier_enabled = %v, want false", payload["notifier_enabled"])
	}
}

func TestComputeReadyStatus_OkWhenAllCriticalDepsPresent(t *testing.T) {
	payload, httpStatus := computeReadyStatus(readinessSnapshot{
		RedisOK:            true,
		SQLiteOK:           true,
		GiteaConfigured:    true,
		NotifierEnabled:    true,
		WorkerImagePresent: true,
		ActiveWorkers:      1,
	})
	if httpStatus != http.StatusOK {
		t.Fatalf("http status = %d, want %d", httpStatus, http.StatusOK)
	}
	if payload["status"] != "ok" {
		t.Fatalf("status = %v, want ok", payload["status"])
	}
	if payload["worker_image_present"] != true {
		t.Fatalf("worker_image_present = %v, want true", payload["worker_image_present"])
	}
	if payload["active_workers"] != 1 {
		t.Fatalf("active_workers = %v, want 1", payload["active_workers"])
	}
}

func TestServe_Healthz(t *testing.T) {
	cfg := newTestConfig(t)
	skipIfNoRedis(t, cfg.RedisAddr)

	stopCh := make(chan struct{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- runServeWithConfig(cfg, stopCh)
	}()

	// 等待服务就绪
	addr := fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Port)
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

	// /healthz 是 liveness 探针，始终返回 200
	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200, got %d", resp.StatusCode)
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

	// liveness 探针返回 "alive" 状态
	if result["status"] != "alive" {
		t.Errorf("status 应为 alive, got %v", result["status"])
	}
	if _, exists := result["version"]; !exists {
		t.Error("响应应包含 version 字段")
	}

	// 通过关闭 stopCh 触发优雅关闭，无需 syscall.Kill
	close(stopCh)

	// 等待 serve 退出
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runServeWithConfig 应返回 nil, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServeWithConfig 未在 10 秒内退出")
	}
}

// TestServe_Readyz 测试 /readyz（readiness）端点返回子系统详细状态
func TestServe_Readyz(t *testing.T) {
	cfg := newTestConfig(t)
	skipIfNoRedis(t, cfg.RedisAddr)

	stopCh := make(chan struct{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- runServeWithConfig(cfg, stopCh)
	}()

	// 等待服务就绪
	addr := fmt.Sprintf("http://127.0.0.1:%d/readyz", cfg.Port)
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
		t.Fatalf("无法连接到 readyz 端点: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 缺少 Gitea 配置时，即使 Redis/SQLite 可用，readyz 也应返回 503
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("缺少 Gitea 配置时状态码应为 503, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("JSON 解析失败: %v, body: %s", err, body)
	}

	if result["status"] != "degraded" {
		t.Errorf("缺少关键依赖时 status 应为 degraded, got %v", result["status"])
	}
	if _, exists := result["version"]; !exists {
		t.Error("响应应包含 version 字段")
	}
	if result["redis"] != true {
		t.Errorf("redis 应为 true, got %v", result["redis"])
	}
	if result["sqlite"] != true {
		t.Errorf("sqlite 应为 true, got %v", result["sqlite"])
	}
	if result["gitea_configured"] != false {
		t.Errorf("gitea_configured 应为 false, got %v", result["gitea_configured"])
	}
	if result["notifier_enabled"] != false {
		t.Errorf("notifier_enabled 应为 false, got %v", result["notifier_enabled"])
	}
	if _, exists := result["worker_image_present"]; !exists {
		t.Error("响应应包含 worker_image_present 字段")
	}
	if _, exists := result["active_workers"]; !exists {
		t.Error("响应应包含 active_workers 字段")
	}

	close(stopCh)
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runServeWithConfig 应返回 nil, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServeWithConfig 未在 10 秒内退出")
	}
}

// TestServe_PortConflict 测试端口被占用时返回错误
func TestServe_PortConflict(t *testing.T) {
	cfg := newTestConfig(t)
	skipIfNoRedis(t, cfg.RedisAddr)

	// 先占用一个端口
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	defer func() { _ = l.Close() }()

	cfg.Port = port

	stopCh := make(chan struct{})
	defer close(stopCh)

	// runServeWithConfig 应立即返回端口冲突错误
	err = runServeWithConfig(cfg, stopCh)
	if err == nil {
		t.Error("端口被占用时应返回错误")
	}
}

// TestServe_RedisUnavailable 测试 Redis 不可用时 BuildServiceDeps 快速失败
func TestServe_RedisUnavailable(t *testing.T) {
	cfg := newTestConfig(t)
	// 使用一个不存在的 Redis 地址
	cfg.RedisAddr = "localhost:16379"

	stopCh := make(chan struct{})
	defer close(stopCh)

	err := runServeWithConfig(cfg, stopCh)
	if err == nil {
		t.Fatal("Redis 不可用时应返回错误")
	}
	if !strings.Contains(err.Error(), "Redis 连接失败") {
		t.Errorf("错误信息应包含 'Redis 连接失败', got: %v", err)
	}
}

func TestServe_RequiresWebhookSecret(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.WebhookSecret = ""

	stopCh := make(chan struct{})
	defer close(stopCh)

	err := runServeWithConfig(cfg, stopCh)
	if err == nil {
		t.Fatal("runServeWithConfig should fail when webhook secret is empty")
	}
	if !strings.Contains(err.Error(), "webhook-secret") {
		t.Fatalf("error = %v, want message containing webhook-secret", err)
	}
}

func TestServe_WebhookRouteReturnsUnauthorizedWithoutSignature(t *testing.T) {
	cfg := newTestConfig(t)
	skipIfNoRedis(t, cfg.RedisAddr)

	stopCh := make(chan struct{})

	errCh := make(chan error, 1)
	go func() { errCh <- runServeWithConfig(cfg, stopCh) }()

	addr := fmt.Sprintf("http://127.0.0.1:%d/webhooks/gitea", cfg.Port)
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

	// 通过关闭 stopCh 触发优雅关闭，无需 syscall.Kill
	close(stopCh)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runServeWithConfig 应返回 nil, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServeWithConfig 未在 10 秒内退出")
	}
}
