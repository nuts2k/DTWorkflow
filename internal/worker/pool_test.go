package worker

import (
	"context"
	"errors"
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
		getContainerLogsFunc: func(ctx context.Context, containerID string) (string, error) {
			return "PR review complete", nil
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
