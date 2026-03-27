package worker

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
)

// mockDockerClient DockerClient 的 mock 实现，用于单元测试
type mockDockerClient struct {
	imageExistsFunc      func(ctx context.Context, imageRef string) (bool, error)
	ensureNetworkFunc    func(ctx context.Context, networkName string) error
	createContainerFunc  func(ctx context.Context, config *ContainerConfig) (string, error)
	startContainerFunc   func(ctx context.Context, containerID string) error
	waitContainerFunc    func(ctx context.Context, containerID string) (int64, error)
	removeContainerFunc  func(ctx context.Context, containerID string) error
	getContainerLogsFunc func(ctx context.Context, containerID string) (ContainerLogs, error)
	listContainersFunc   func(ctx context.Context, f filters.Args) ([]container.Summary, error)
	attachContainerFunc  func(ctx context.Context, containerID string) (io.WriteCloser, error)
	closeFunc            func() error
}

func (m *mockDockerClient) ImageExists(ctx context.Context, imageRef string) (bool, error) {
	if m.imageExistsFunc != nil {
		return m.imageExistsFunc(ctx, imageRef)
	}
	return true, nil
}

func (m *mockDockerClient) EnsureNetwork(ctx context.Context, networkName string) error {
	if m.ensureNetworkFunc != nil {
		return m.ensureNetworkFunc(ctx, networkName)
	}
	return nil
}

func (m *mockDockerClient) CreateContainer(ctx context.Context, config *ContainerConfig) (string, error) {
	if m.createContainerFunc != nil {
		return m.createContainerFunc(ctx, config)
	}
	return "mock-container-id", nil
}

func (m *mockDockerClient) StartContainer(ctx context.Context, containerID string) error {
	if m.startContainerFunc != nil {
		return m.startContainerFunc(ctx, containerID)
	}
	return nil
}

func (m *mockDockerClient) WaitContainer(ctx context.Context, containerID string) (int64, error) {
	if m.waitContainerFunc != nil {
		return m.waitContainerFunc(ctx, containerID)
	}
	return 0, nil
}

func (m *mockDockerClient) RemoveContainer(ctx context.Context, containerID string) error {
	if m.removeContainerFunc != nil {
		return m.removeContainerFunc(ctx, containerID)
	}
	return nil
}

func (m *mockDockerClient) GetContainerLogs(ctx context.Context, containerID string) (ContainerLogs, error) {
	if m.getContainerLogsFunc != nil {
		return m.getContainerLogsFunc(ctx, containerID)
	}
	return ContainerLogs{Stdout: "mock logs"}, nil
}

func (m *mockDockerClient) ListContainers(ctx context.Context, f filters.Args) ([]container.Summary, error) {
	if m.listContainersFunc != nil {
		return m.listContainersFunc(ctx, f)
	}
	return nil, nil
}

func (m *mockDockerClient) AttachContainer(ctx context.Context, containerID string) (io.WriteCloser, error) {
	if m.attachContainerFunc != nil {
		return m.attachContainerFunc(ctx, containerID)
	}
	// 默认返回一个内存 pipe WriteCloser
	_, server := net.Pipe()
	return server, nil
}

func (m *mockDockerClient) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// TestMockDockerClientImplementsInterface 确保 mockDockerClient 实现了 DockerClient 接口
func TestMockDockerClientImplementsInterface(t *testing.T) {
	var _ DockerClient = &mockDockerClient{}
}

// TestContainerConfigFields 测试 ContainerConfig 结构体字段
func TestContainerConfigFields(t *testing.T) {
	cfg := &ContainerConfig{
		Image:       "test-image:latest",
		Name:        "test-container",
		Env:         []string{"KEY=VALUE"},
		Cmd:         []string{"echo", "hello"},
		Labels:      map[string]string{"managed-by": "dtworkflow"},
		CPULimit:    "2.0",
		MemoryLimit: "4g",
		NetworkName: "test-net",
	}
	if cfg.Image != "test-image:latest" {
		t.Errorf("Image 字段不匹配: got %s", cfg.Image)
	}
	if cfg.NetworkName != "test-net" {
		t.Errorf("NetworkName 字段不匹配: got %s", cfg.NetworkName)
	}
	if cfg.Labels["managed-by"] != "dtworkflow" {
		t.Errorf("Labels 字段不匹配: got %v", cfg.Labels)
	}
}

// TestContainerExitError 测试 ContainerExitError 的错误信息格式
func TestContainerExitError(t *testing.T) {
	err := &ContainerExitError{
		ContainerID: "abc123",
		ExitCode:    1,
		Message:     "process failed",
	}
	msg := err.Error()
	if !strings.Contains(msg, "abc123") {
		t.Errorf("错误信息应包含容器 ID，实际: %s", msg)
	}
	if !strings.Contains(msg, "code=1") {
		t.Errorf("错误信息应包含退出码，实际: %s", msg)
	}
	if !strings.Contains(msg, "process failed") {
		t.Errorf("错误信息应包含消息内容，实际: %s", msg)
	}

	// 验证实现了 error 接口
	var _ error = err
}

// TestInt64Ptr 测试 int64Ptr 辅助函数
func TestInt64Ptr(t *testing.T) {
	ptr := int64Ptr(512)
	if ptr == nil {
		t.Fatal("int64Ptr 返回 nil")
	}
	if *ptr != 512 {
		t.Errorf("*int64Ptr(512) = %d, 期望 512", *ptr)
	}

	zeroPtr := int64Ptr(0)
	if *zeroPtr != 0 {
		t.Errorf("*int64Ptr(0) = %d, 期望 0", *zeroPtr)
	}
}

// TestStdinWriteCloser 测试 stdinWriteCloser 的 Write/Close/SetWriteDeadline 方法
func TestStdinWriteCloser(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	wc := &stdinWriteCloser{conn: server}

	// 测试 Write
	go func() {
		buf := make([]byte, 5)
		_, _ = client.Read(buf)
	}()
	n, err := wc.Write([]byte("hello"))
	if err != nil {
		t.Errorf("Write 返回错误: %v", err)
	}
	if n != 5 {
		t.Errorf("Write 返回 %d 字节, 期望 5", n)
	}

	// 测试 SetWriteDeadline
	err = wc.SetWriteDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Errorf("SetWriteDeadline 返回错误: %v", err)
	}

	// 测试 Close
	err = wc.Close()
	if err != nil {
		t.Errorf("Close 返回错误: %v", err)
	}
}

// TestStdinWriteCloser_CloseWithHalfClose 测试 Close 对支持 CloseWrite 的连接正确执行半关闭
func TestStdinWriteCloser_CloseWithHalfClose(t *testing.T) {
	// net.Pipe 连接支持 CloseWrite（通过 net.TCPConn 等），
	// 但 net.Pipe 本身不支持 CloseWrite，所以这里用 mock 验证
	closed := false
	closeWriteCalled := false

	mock := &mockConn{
		closeFunc: func() error {
			closed = true
			return nil
		},
		closeWriteFunc: func() error {
			closeWriteCalled = true
			return nil
		},
	}

	wc := &stdinWriteCloser{conn: mock}
	_ = wc.Close()

	if !closeWriteCalled {
		t.Error("Close 应先调用 CloseWrite")
	}
	if !closed {
		t.Error("Close 应调用 conn.Close")
	}
}

// mockConn 实现 net.Conn 接口，用于测试 stdinWriteCloser
type mockConn struct {
	net.Conn       // 嵌入 net.Conn 提供默认实现
	closeFunc      func() error
	closeWriteFunc func() error
}

func (m *mockConn) Write(p []byte) (int, error) { return len(p), nil }
func (m *mockConn) Read(p []byte) (int, error)  { return 0, io.EOF }
func (m *mockConn) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}
func (m *mockConn) CloseWrite() error {
	if m.closeWriteFunc != nil {
		return m.closeWriteFunc()
	}
	return nil
}
func (m *mockConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (m *mockConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }
