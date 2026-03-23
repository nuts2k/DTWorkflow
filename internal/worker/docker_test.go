package worker

import (
	"context"
	"testing"
)

// mockDockerClient DockerClient 的 mock 实现，用于单元测试
type mockDockerClient struct {
	imageExistsFunc      func(ctx context.Context, imageRef string) (bool, error)
	ensureNetworkFunc    func(ctx context.Context, networkName string) error
	createContainerFunc  func(ctx context.Context, config *ContainerConfig) (string, error)
	startContainerFunc   func(ctx context.Context, containerID string) error
	waitContainerFunc    func(ctx context.Context, containerID string) (int64, error)
	removeContainerFunc  func(ctx context.Context, containerID string) error
	getContainerLogsFunc func(ctx context.Context, containerID string) (string, error)
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

func (m *mockDockerClient) GetContainerLogs(ctx context.Context, containerID string) (string, error) {
	if m.getContainerLogsFunc != nil {
		return m.getContainerLogsFunc(ctx, containerID)
	}
	return "mock logs", nil
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
