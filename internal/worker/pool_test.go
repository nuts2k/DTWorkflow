package worker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

func defaultPoolConfig() PoolConfig {
	return PoolConfig{
		Image:        "dtworkflow-worker:test",
		CPULimit:     "1.0",
		MemoryLimit:  "512m",
		GiteaURL:     "http://gitea.example.com",
		GiteaToken:   "token",
		ClaudeAPIKey: "key",
		NetworkName:  "test-net",
	}
}

func defaultPayload() model.TaskPayload {
	return model.TaskPayload{
		TaskType:     model.TaskTypeReviewPR,
		DeliveryID:   "delivery-001",
		RepoOwner:    "owner",
		RepoName:     "repo",
		RepoFullName: "owner/repo",
		CloneURL:     "http://gitea.example.com/owner/repo.git",
		PRNumber:     1,
		HeadRef:      "feature",
		BaseRef:      "main",
	}
}

// mustNewPool 创建 Pool，失败则终止测试
func mustNewPool(t *testing.T, config PoolConfig, docker DockerClient) *Pool {
	t.Helper()
	pool, err := NewPool(config, docker)
	if err != nil {
		t.Fatalf("NewPool 返回非预期错误: %v", err)
	}
	return pool
}

// TestPool_NewPoolValidation 验证 NewPool 配置校验
func TestPool_NewPoolValidation(t *testing.T) {
	mock := &mockDockerClient{}

	// Image 为空应返回错误
	_, err := NewPool(PoolConfig{}, mock)
	if err == nil {
		t.Error("Image 为空时 NewPool 应返回错误")
	}

	// Image 非空应成功
	_, err = NewPool(defaultPoolConfig(), mock)
	if err != nil {
		t.Errorf("NewPool 返回非预期错误: %v", err)
	}
}

// TestPool_RunSuccess 测试容器正常执行并返回成功结果
func TestPool_RunSuccess(t *testing.T) {
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			// 验证标签包含 managed-by
			if config.Labels["managed-by"] != "dtworkflow" {
				t.Errorf("容器缺少 managed-by 标签")
			}
			return "container-abc123", nil
		},
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			return 0, nil
		},
		getContainerLogsFunc: func(ctx context.Context, containerID string) (ContainerLogs, error) {
			return ContainerLogs{Stdout: "PR review complete", Stderr: "entrypoint info"}, nil
		},
	}

	pool := mustNewPool(t, defaultPoolConfig(), mock)
	result, err := pool.Run(context.Background(), defaultPayload())
	if err != nil {
		t.Fatalf("Run 返回非预期错误: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, 期望 0", result.ExitCode)
	}
	if result.Output != "PR review complete" {
		t.Errorf("Output = %q, 期望 PR review complete", result.Output)
	}
	if result.ContainerID != "container-abc123" {
		t.Errorf("ContainerID = %q, 期望 container-abc123", result.ContainerID)
	}
	if result.Duration < 0 {
		t.Errorf("Duration 不应为负数: %d", result.Duration)
	}
}

// TestPool_RunFailureIncludesStderr 验证失败场景会保留 stderr 诊断日志。
func TestPool_RunFailureIncludesStderr(t *testing.T) {
	mock := &mockDockerClient{
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			return 1, &ContainerExitError{ContainerID: containerID, ExitCode: 1, Message: "clone failed"}
		},
		getContainerLogsFunc: func(ctx context.Context, containerID string) (ContainerLogs, error) {
			return ContainerLogs{Stdout: "", Stderr: "fatal: Authentication failed"}, nil
		},
	}

	pool := mustNewPool(t, defaultPoolConfig(), mock)
	result, err := pool.Run(context.Background(), defaultPayload())
	if err == nil {
		t.Fatal("失败场景应返回错误")
	}
	if result == nil {
		t.Fatal("失败场景 result 不应为 nil")
	}
	if result.Output != "fatal: Authentication failed" {
		t.Errorf("Output = %q, 期望保留 stderr 日志", result.Output)
	}
}

// TestPool_RunContainerRemoved 验证容器无论成功/失败都会被清理
func TestPool_RunContainerRemoved(t *testing.T) {
	removed := false
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			return "container-xyz", nil
		},
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			return 1, errors.New("容器内部错误")
		},
		removeContainerFunc: func(ctx context.Context, containerID string) error {
			removed = true
			return nil
		},
	}

	pool := mustNewPool(t, defaultPoolConfig(), mock)
	_, _ = pool.Run(context.Background(), defaultPayload())

	if !removed {
		t.Error("容器执行失败后应被自动清理")
	}
}

// TestPool_RunContextCancelled 验证 ctx 取消时容器被清理
func TestPool_RunContextCancelled(t *testing.T) {
	removed := false
	ctx, cancel := context.WithCancel(context.Background())

	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			return "container-cancel", nil
		},
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			// 取消 context 并返回错误
			cancel()
			return -1, ctx.Err()
		},
		removeContainerFunc: func(ctx context.Context, containerID string) error {
			removed = true
			return nil
		},
	}

	pool := mustNewPool(t, defaultPoolConfig(), mock)
	_, err := pool.Run(ctx, defaultPayload())
	if err == nil {
		t.Error("ctx 取消后 Run 应返回错误")
	}
	if !removed {
		t.Error("ctx 取消后容器应被清理")
	}
}

// TestPool_Stats 验证统计数据更新
func TestPool_Stats(t *testing.T) {
	mock := &mockDockerClient{}
	pool := mustNewPool(t, defaultPoolConfig(), mock)

	stats := pool.Stats()
	if stats.Active != 0 {
		t.Errorf("初始 Active = %d, 期望 0", stats.Active)
	}
	if stats.Completed != 0 {
		t.Errorf("初始 Completed = %d, 期望 0", stats.Completed)
	}

	// 执行一次任务
	pool.Run(context.Background(), defaultPayload())

	stats = pool.Stats()
	if stats.Completed != 1 {
		t.Errorf("执行后 Completed = %d, 期望 1", stats.Completed)
	}
}

// TestPool_Shutdown 验证 Shutdown 等待活跃任务完成
func TestPool_Shutdown(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})

	mock := &mockDockerClient{
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			close(started) // 通知测试容器已启动
			<-unblock      // 等待测试解除阻塞
			return 0, nil
		},
	}

	pool := mustNewPool(t, defaultPoolConfig(), mock)

	// 在 goroutine 中运行任务
	go func() {
		pool.Run(context.Background(), defaultPayload())
	}()

	// 等待容器启动
	<-started

	// Shutdown 应等待任务完成
	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		shutdownDone <- pool.Shutdown(ctx)
	}()

	// 解除容器阻塞
	close(unblock)

	// 验证 Shutdown 完成且无错误
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Errorf("Shutdown 返回非预期错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Shutdown 超时")
	}
}

// TestPool_DefaultNetworkName 验证默认网络名称
func TestPool_DefaultNetworkName(t *testing.T) {
	config := defaultPoolConfig()
	config.NetworkName = "" // 清空网络名，应使用默认值

	var capturedNetworkName string
	mock := &mockDockerClient{
		ensureNetworkFunc: func(ctx context.Context, networkName string) error {
			capturedNetworkName = networkName
			return nil
		},
	}

	pool := mustNewPool(t, config, mock)
	pool.Run(context.Background(), defaultPayload())

	if capturedNetworkName != "dtworkflow-net" {
		t.Errorf("默认网络名 = %q, 期望 dtworkflow-net", capturedNetworkName)
	}
}

// TestBuildContainerName 验证容器名称格式
func TestBuildContainerName(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:   model.TaskTypeReviewPR,
		DeliveryID: "test-delivery-123",
	}
	name := buildContainerName(payload)
	if name == "" {
		t.Error("容器名称不应为空")
	}
	// 名称应包含任务类型前缀
	if len(name) < 4 {
		t.Errorf("容器名称太短: %s", name)
	}
}

// TestPool_RunImageNotExists 验证镜像不存在时返回错误
func TestPool_RunImageNotExists(t *testing.T) {
	mock := &mockDockerClient{
		imageExistsFunc: func(ctx context.Context, imageRef string) (bool, error) {
			return false, nil
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	_, err := pool.Run(context.Background(), defaultPayload())
	if err == nil {
		t.Error("镜像不存在时 Run 应返回错误")
	}
}

// TestPool_RunImageCheckError 验证镜像检查失败时返回错误
func TestPool_RunImageCheckError(t *testing.T) {
	mock := &mockDockerClient{
		imageExistsFunc: func(ctx context.Context, imageRef string) (bool, error) {
			return false, errors.New("docker daemon 不可用")
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	_, err := pool.Run(context.Background(), defaultPayload())
	if err == nil {
		t.Error("镜像检查失败时 Run 应返回错误")
	}
}

// TestPool_RunNetworkError 验证网络创建失败时返回错误
func TestPool_RunNetworkError(t *testing.T) {
	mock := &mockDockerClient{
		ensureNetworkFunc: func(ctx context.Context, networkName string) error {
			return errors.New("网络创建失败")
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	_, err := pool.Run(context.Background(), defaultPayload())
	if err == nil {
		t.Error("网络创建失败时 Run 应返回错误")
	}
}

// TestPool_RunCreateContainerError 验证容器创建失败时返回错误
func TestPool_RunCreateContainerError(t *testing.T) {
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			return "", errors.New("容器创建失败")
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	_, err := pool.Run(context.Background(), defaultPayload())
	if err == nil {
		t.Error("容器创建失败时 Run 应返回错误")
	}
}

// TestPool_RunStartContainerError 验证容器启动失败时返回 result 和 error
func TestPool_RunStartContainerError(t *testing.T) {
	removed := false
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			return "container-start-fail", nil
		},
		startContainerFunc: func(ctx context.Context, containerID string) error {
			return errors.New("端口冲突")
		},
		removeContainerFunc: func(ctx context.Context, containerID string) error {
			removed = true
			return nil
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	result, err := pool.Run(context.Background(), defaultPayload())
	if err == nil {
		t.Error("启动失败时 Run 应返回错误")
	}
	// 启动失败时应返回部分填充的 result
	if result == nil {
		t.Fatal("启动失败时应返回 result")
	}
	if result.ExitCode != -1 {
		t.Errorf("ExitCode = %d, 期望 -1", result.ExitCode)
	}
	if result.ContainerID != "container-start-fail" {
		t.Errorf("ContainerID = %q, 期望 container-start-fail", result.ContainerID)
	}
	if !removed {
		t.Error("启动失败后容器应被清理")
	}
}

// TestPool_RunInvalidTaskType 验证无效任务类型快速失败
func TestPool_RunInvalidTaskType(t *testing.T) {
	mock := &mockDockerClient{}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	payload := defaultPayload()
	payload.TaskType = "invalid_type"
	_, err := pool.Run(context.Background(), payload)
	if err == nil {
		t.Error("无效任务类型应返回错误")
	}
}

// TestPool_RunWithCommand 验证自定义命令执行
func TestPool_RunWithCommand(t *testing.T) {
	var capturedCmd []string
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			capturedCmd = config.Cmd
			return "container-custom", nil
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	customCmd := []string{"claude", "-p", "custom prompt"}
	result, err := pool.RunWithCommand(context.Background(), defaultPayload(), customCmd)
	if err != nil {
		t.Fatalf("RunWithCommand 返回错误: %v", err)
	}
	if result.ContainerID != "container-custom" {
		t.Errorf("ContainerID = %q, 期望 container-custom", result.ContainerID)
	}
	if len(capturedCmd) != 3 || capturedCmd[2] != "custom prompt" {
		t.Errorf("容器命令 = %v, 期望自定义命令", capturedCmd)
	}
}

// TestPool_RunWithCommandAndStdin_EmptyStdin 空 stdin 应退化为 RunWithCommand
func TestPool_RunWithCommandAndStdin_EmptyStdin(t *testing.T) {
	var capturedOpenStdin bool
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			capturedOpenStdin = config.OpenStdin
			return "container-no-stdin", nil
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	result, err := pool.RunWithCommandAndStdin(context.Background(), defaultPayload(), []string{"claude"}, nil)
	if err != nil {
		t.Fatalf("RunWithCommandAndStdin(nil) 返回错误: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
	if capturedOpenStdin {
		t.Error("空 stdin 时 OpenStdin 应为 false")
	}
}

// TestPool_RunWithCommandAndStdin_WithData 测试 stdin 数据传入
func TestPool_RunWithCommandAndStdin_WithData(t *testing.T) {
	var capturedOpenStdin bool
	attachCalled := false
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			capturedOpenStdin = config.OpenStdin
			return "container-stdin", nil
		},
		attachContainerFunc: func(ctx context.Context, containerID string) (io.WriteCloser, error) {
			attachCalled = true
			// 使用 drainWriteCloser 模拟容器 stdin：接受写入并丢弃
			return &drainWriteCloser{}, nil
		},
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			return 0, nil
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	result, err := pool.RunWithCommandAndStdin(
		context.Background(), defaultPayload(),
		[]string{"claude", "--stdin"}, []byte("stdin data"),
	)
	if err != nil {
		t.Fatalf("RunWithCommandAndStdin 返回错误: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
	if !capturedOpenStdin {
		t.Error("有 stdin 数据时 OpenStdin 应为 true")
	}
	if !attachCalled {
		t.Error("有 stdin 数据时应调用 AttachContainer")
	}
}

// drainWriteCloser 接受写入数据并丢弃，模拟容器 stdin
type drainWriteCloser struct {
	closed bool
}

func (d *drainWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (d *drainWriteCloser) Close() error                { d.closed = true; return nil }

// TestPool_RunWithStdin_AttachError 测试 attach 失败时返回错误
func TestPool_RunWithStdin_AttachError(t *testing.T) {
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			return "container-attach-fail", nil
		},
		attachContainerFunc: func(ctx context.Context, containerID string) (io.WriteCloser, error) {
			return nil, errors.New("attach 失败")
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	_, err := pool.RunWithCommandAndStdin(
		context.Background(), defaultPayload(),
		[]string{"claude"}, []byte("data"),
	)
	if err == nil {
		t.Error("attach 失败时应返回错误")
	}
}

// TestPool_RunWithStdin_StartError_ClosesStdin 启动失败时应关闭 stdin writer
func TestPool_RunWithStdin_StartError_ClosesStdin(t *testing.T) {
	stdinClosed := false
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			return "container-stdin-close", nil
		},
		attachContainerFunc: func(ctx context.Context, containerID string) (io.WriteCloser, error) {
			return &trackingWriteCloser{
				onClose: func() { stdinClosed = true },
			}, nil
		},
		startContainerFunc: func(ctx context.Context, containerID string) error {
			return errors.New("启动失败")
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	_, _ = pool.RunWithCommandAndStdin(
		context.Background(), defaultPayload(),
		[]string{"claude"}, []byte("data"),
	)
	if !stdinClosed {
		t.Error("启动失败时应关闭 stdin writer")
	}
}

// TestPool_RunWithStdin_WriteErrorAndWaitError 两者都失败时优先返回容器错误
func TestPool_RunWithStdin_WriteErrorAndWaitError(t *testing.T) {
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			return "container-both-fail", nil
		},
		attachContainerFunc: func(ctx context.Context, containerID string) (io.WriteCloser, error) {
			return &failWriteCloser{}, nil
		},
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			return 1, errors.New("容器异常退出")
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	result, err := pool.RunWithCommandAndStdin(
		context.Background(), defaultPayload(),
		[]string{"claude"}, []byte("data"),
	)
	// waitErr 不为 nil 时应返回 waitErr
	if err == nil {
		t.Error("两者都失败时应返回错误")
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, 期望 1", result.ExitCode)
	}
}

// TestPool_RunWithStdin_WriteErrorOnly stdin 写入失败但容器执行成功
func TestPool_RunWithStdin_WriteErrorOnly(t *testing.T) {
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			return "container-stdin-err", nil
		},
		attachContainerFunc: func(ctx context.Context, containerID string) (io.WriteCloser, error) {
			return &failWriteCloser{}, nil
		},
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			return 0, nil // 容器正常退出
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	result, err := pool.RunWithCommandAndStdin(
		context.Background(), defaultPayload(),
		[]string{"claude"}, []byte("data"),
	)
	// waitErr 为 nil 但 stdin 写入失败
	if err == nil {
		t.Error("stdin 写入失败时应返回错误")
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
}

// failWriteCloser 写入时总是返回错误
type failWriteCloser struct{}

func (f *failWriteCloser) Write(p []byte) (int, error) { return 0, errors.New("write failed") }
func (f *failWriteCloser) Close() error                { return nil }

// TestPool_EnsureNetworkCalledOnce 验证网络只创建一次
func TestPool_EnsureNetworkCalledOnce(t *testing.T) {
	callCount := 0
	mock := &mockDockerClient{
		ensureNetworkFunc: func(ctx context.Context, networkName string) error {
			callCount++
			return nil
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	// 运行两次
	pool.Run(context.Background(), defaultPayload())
	pool.Run(context.Background(), defaultPayload())

	if callCount != 1 {
		t.Errorf("EnsureNetwork 应只调用 1 次，实际 %d 次", callCount)
	}
}

// TestPool_BuildContainerName_EmptyDeliveryID 验证空 DeliveryID 时生成唯一名称
func TestPool_BuildContainerName_EmptyDeliveryID(t *testing.T) {
	payload := model.TaskPayload{
		TaskType:   model.TaskTypeReviewPR,
		DeliveryID: "",
	}
	name1 := buildContainerName(payload)
	name2 := buildContainerName(payload)
	if name1 == "" || name2 == "" {
		t.Error("容器名称不应为空")
	}
	if name1 == name2 {
		t.Error("两次生成的名称应不同")
	}
}

// TestPool_SanitizeName 验证名称清理逻辑
func TestPool_SanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"abc123", "abc123"},
		{"a.b.c", "a-b-c"},
		{"a--b", "a-b"},  // 连续连字符压缩
		{"a!!!b", "a-b"}, // 多个特殊字符压缩
		{"hello world", "hello-world"},
	}
	for _, tc := range tests {
		got := sanitizeName(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeName(%q) = %q, 期望 %q", tc.input, got, tc.expected)
		}
	}
}

// TestPool_ShutdownTimeout 验证 Shutdown 超时路径
func TestPool_ShutdownTimeout(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	mock := &mockDockerClient{
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			close(started)
			// 阻塞直到 unblock 或 ctx 取消
			select {
			case <-unblock:
				return 0, nil
			case <-ctx.Done():
				return -1, ctx.Err()
			}
		},
	}

	pool := mustNewPool(t, defaultPoolConfig(), mock)

	// 在 goroutine 中运行任务
	go func() {
		pool.Run(context.Background(), defaultPayload())
	}()
	<-started

	// 用极短超时触发 Shutdown 超时路径
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- pool.Shutdown(ctx)
	}()

	// 等 Shutdown 超时后解除容器阻塞，让 defer cleanup 完成
	time.Sleep(100 * time.Millisecond)
	close(unblock)

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Errorf("Shutdown 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Shutdown 超时未完成")
	}
}

// TestPool_ShutdownIdempotent 验证多次调用 Shutdown 是幂等的
func TestPool_ShutdownIdempotent(t *testing.T) {
	closeCount := 0
	mock := &mockDockerClient{
		closeFunc: func() error {
			closeCount++
			return nil
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)

	_ = pool.Shutdown(context.Background())
	_ = pool.Shutdown(context.Background())

	// 第二次 Shutdown 应直接返回 nil，不调用 docker.Close()
	if closeCount != 1 {
		t.Errorf("docker.Close 被调用 %d 次, 期望 1 次", closeCount)
	}
}

// TestPool_RunLogError 验证获取日志失败时不影响整体执行
func TestPool_RunLogError(t *testing.T) {
	mock := &mockDockerClient{
		getContainerLogsFunc: func(ctx context.Context, containerID string) (ContainerLogs, error) {
			return ContainerLogs{}, errors.New("日志获取失败")
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	result, err := pool.Run(context.Background(), defaultPayload())
	if err != nil {
		t.Fatalf("日志失败不应导致 Run 失败: %v", err)
	}
	if result.Output != "" {
		t.Errorf("日志获取失败时 Output 应为空，实际: %q", result.Output)
	}
}

// TestPool_RunRemoveContainerError 验证清理容器失败时不阻塞
func TestPool_RunRemoveContainerError(t *testing.T) {
	mock := &mockDockerClient{
		removeContainerFunc: func(ctx context.Context, containerID string) error {
			return errors.New("清理失败")
		},
	}
	pool := mustNewPool(t, defaultPoolConfig(), mock)
	// 不应 panic 或阻塞
	result, err := pool.Run(context.Background(), defaultPayload())
	if err != nil {
		t.Fatalf("Run 不应因清理失败而报错: %v", err)
	}
	if result == nil {
		t.Fatal("result 不应为 nil")
	}
}

// trackingWriteCloser 用于追踪 Close 调用的 WriteCloser
type trackingWriteCloser struct {
	onClose func()
}

func (t *trackingWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (t *trackingWriteCloser) Close() error {
	if t.onClose != nil {
		t.onClose()
	}
	return nil
}

// TestPool_RunAfterShutdown 验证 Shutdown 后并发调用 Run 返回错误且不会泄漏 goroutine
func TestPool_RunAfterShutdown(t *testing.T) {
	mock := &mockDockerClient{}
	pool := mustNewPool(t, defaultPoolConfig(), mock)

	// 先关闭池
	ctx := context.Background()
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown 返回非预期错误: %v", err)
	}

	// 并发发起多个 Run 调用，全部应返回错误
	const concurrency = 10
	var wg sync.WaitGroup
	var errCount atomic.Int32

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := pool.Run(ctx, defaultPayload())
			if err != nil {
				errCount.Add(1)
			}
		}()
	}

	wg.Wait()

	if int(errCount.Load()) != concurrency {
		t.Errorf("期望 %d 个 Run 调用返回错误，实际 %d 个", concurrency, errCount.Load())
	}
}

// --- 流式心跳监控测试 ---

// stdcopyFrame 构造带 Docker stdcopy 8 字节 header 的数据帧。
// streamType: 1=stdout, 2=stderr
func stdcopyFrame(streamType byte, data string) []byte {
	payload := []byte(data)
	header := make([]byte, 8)
	header[0] = streamType
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	return append(header, payload...)
}

// TestPool_RunWithStreamMonitor_Success 模拟完整 stream-json 事件流（含 result），验证 Output 正确提取
func TestPool_RunWithStreamMonitor_Success(t *testing.T) {
	var capturedCmd []string
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			capturedCmd = config.Cmd
			return "container-stream-ok", nil
		},
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			// 短暂延迟，确保 streamMonitorLoop 有时间处理流中的 result 事件
			time.Sleep(200 * time.Millisecond)
			return 0, nil
		},
		followLogsFunc: func(ctx context.Context, containerID string) (io.ReadCloser, error) {
			var buf bytes.Buffer
			buf.Write(stdcopyFrame(1, `{"type":"system","subtype":"init"}`+"\n"))
			buf.Write(stdcopyFrame(1, `{"type":"assistant","message":{"role":"assistant"}}`+"\n"))
			buf.Write(stdcopyFrame(1, `{"type":"result","subtype":"success","cost_usd":0.05,"duration_ms":1000,"is_error":false,"num_turns":3,"result":"{\"summary\":\"good\",\"verdict\":\"approve\",\"issues\":[]}","session_id":"sess-1"}`+"\n"))
			return io.NopCloser(&buf), nil
		},
		getContainerLogsFunc: func(ctx context.Context, containerID string) (ContainerLogs, error) {
			return ContainerLogs{Stdout: "fallback logs"}, nil
		},
	}

	config := defaultPoolConfig()
	config.StreamMonitor = StreamMonitorConfig{
		Enabled:         true,
		ActivityTimeout: 5 * time.Second,
	}
	pool := mustNewPool(t, config, mock)

	result, err := pool.RunWithCommand(context.Background(), defaultPayload(), []string{"claude", "-p", "review"})
	if err != nil {
		t.Fatalf("Run 返回非预期错误: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, 期望 0", result.ExitCode)
	}
	// 验证 Output 来自流式 result 事件（而非 fallback 日志）
	// result 字段内的 JSON 是转义后的字符串，所以 verdict 出现为 \"verdict\"
	if !strings.Contains(result.Output, `verdict`) {
		t.Errorf("Output 应包含 result 事件内容，实际: %s", result.Output)
	}
	if !strings.Contains(result.Output, `"type":"success"`) {
		t.Errorf("Output 应包含 CLI JSON 信封的 type=success，实际: %s", result.Output)
	}
	if result.Output == "fallback logs" {
		t.Error("Output 不应为 fallback 日志")
	}
	// 验证命令被注入了 stream-json 标志
	found := false
	for _, arg := range capturedCmd {
		if arg == "stream-json" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("开启流式监控时命令应包含 stream-json，实际: %v", capturedCmd)
	}
}

// TestPool_RunWithStreamMonitor_Disabled 开关关闭走旧路径，验证从 GetContainerLogs 获取输出
func TestPool_RunWithStreamMonitor_Disabled(t *testing.T) {
	followLogsCalled := false
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			return "container-no-stream", nil
		},
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			return 0, nil
		},
		getContainerLogsFunc: func(ctx context.Context, containerID string) (ContainerLogs, error) {
			return ContainerLogs{Stdout: "legacy log output"}, nil
		},
		followLogsFunc: func(ctx context.Context, containerID string) (io.ReadCloser, error) {
			followLogsCalled = true
			return io.NopCloser(strings.NewReader("")), nil
		},
	}

	config := defaultPoolConfig()
	config.StreamMonitor = StreamMonitorConfig{
		Enabled: false,
	}
	pool := mustNewPool(t, config, mock)

	result, err := pool.RunWithCommand(context.Background(), defaultPayload(), []string{"claude", "-p", "review"})
	if err != nil {
		t.Fatalf("Run 返回非预期错误: %v", err)
	}
	if result.Output != "legacy log output" {
		t.Errorf("Output = %q, 期望 legacy log output", result.Output)
	}
	if followLogsCalled {
		t.Error("开关关闭时不应调用 FollowLogs")
	}
}

// blockingReadCloser 先返回 buf 中的数据，读完后阻塞直到 ctx 取消。
// 模拟 Docker 日志流：容器还在运行，但长时间无新输出。
type blockingReadCloser struct {
	buf    *bytes.Buffer
	ctx    context.Context
	closed chan struct{}
}

func newBlockingReadCloser(ctx context.Context, data []byte) *blockingReadCloser {
	return &blockingReadCloser{
		buf:    bytes.NewBuffer(data),
		ctx:    ctx,
		closed: make(chan struct{}),
	}
}

func (b *blockingReadCloser) Read(p []byte) (int, error) {
	// 先返回缓冲区中的数据
	if b.buf.Len() > 0 {
		return b.buf.Read(p)
	}
	// 缓冲区空了之后阻塞，模拟流挂起
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.closed:
		return 0, io.EOF
	}
}

func (b *blockingReadCloser) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

// TestPool_RunWithStreamMonitor_ActivityTimeout 模拟流中断（只输出一行后停止），验证活跃度超时后返回错误
func TestPool_RunWithStreamMonitor_ActivityTimeout(t *testing.T) {
	mock := &mockDockerClient{
		createContainerFunc: func(ctx context.Context, config *ContainerConfig) (string, error) {
			return "container-timeout", nil
		},
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			// 等待 ctx 被超时取消
			<-ctx.Done()
			return -1, ctx.Err()
		},
		followLogsFunc: func(ctx context.Context, containerID string) (io.ReadCloser, error) {
			// 只发一行后阻塞，模拟流中断（容器还在运行但无输出）
			var buf bytes.Buffer
			buf.Write(stdcopyFrame(1, `{"type":"system","subtype":"init"}`+"\n"))
			return newBlockingReadCloser(ctx, buf.Bytes()), nil
		},
		getContainerLogsFunc: func(ctx context.Context, containerID string) (ContainerLogs, error) {
			return ContainerLogs{Stdout: "timeout fallback"}, nil
		},
	}

	config := defaultPoolConfig()
	config.StreamMonitor = StreamMonitorConfig{
		Enabled:         true,
		ActivityTimeout: 100 * time.Millisecond, // 短阈值加速测试
	}
	pool := mustNewPool(t, config, mock)

	result, err := pool.RunWithCommand(context.Background(), defaultPayload(), []string{"claude", "-p", "review"})
	// 活跃度超时应导致返回错误
	if err == nil {
		t.Fatal("活跃度超时后应返回错误")
	}
	if !strings.Contains(err.Error(), "活跃度超时") && !strings.Contains(err.Error(), "context") {
		t.Errorf("错误应包含超时相关信息，实际: %v", err)
	}
	if result == nil {
		t.Fatal("超时时 result 不应为 nil")
	}
}
