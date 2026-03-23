package worker

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
)

// mockDockerClientWithList 扩展 mockDockerClient，支持容器列表查询（用于 GC 测试）
type mockDockerClientWithList struct {
	mockDockerClient
	containers        []container.Summary // exited/dead 容器
	runningContainers []container.Summary // running 容器
	removedIDs        []string
}

// ListContainers 根据 filters 中的 status 参数返回不同的容器列表
func (m *mockDockerClientWithList) ListContainers(_ context.Context, f filters.Args) ([]container.Summary, error) {
	if f.Contains("status") && f.ExactMatch("status", "running") {
		return m.runningContainers, nil
	}
	return m.containers, nil
}

func (m *mockDockerClientWithList) RemoveContainer(_ context.Context, containerID string) error {
	m.removedIDs = append(m.removedIDs, containerID)
	return nil
}

// TestGarbageCollector_DefaultValues 验证默认配置值
func TestGarbageCollector_DefaultValues(t *testing.T) {
	mock := &mockDockerClient{}
	gc := NewGarbageCollector(mock)
	if gc.interval != 60*time.Second {
		t.Errorf("默认 interval = %v, 期望 60s", gc.interval)
	}
	if gc.maxAge != 35*time.Minute {
		t.Errorf("默认 maxAge = %v, 期望 35min", gc.maxAge)
	}
}

// TestGarbageCollector_WithOptions 验证选项函数生效
func TestGarbageCollector_WithOptions(t *testing.T) {
	mock := &mockDockerClient{}
	gc := NewGarbageCollector(mock,
		WithGCInterval(30*time.Second),
		WithGCMaxAge(10*time.Minute),
	)
	if gc.interval != 30*time.Second {
		t.Errorf("interval = %v, 期望 30s", gc.interval)
	}
	if gc.maxAge != 10*time.Minute {
		t.Errorf("maxAge = %v, 期望 10min", gc.maxAge)
	}
}

// TestGarbageCollector_RunOnce_CleansOldContainers 验证超龄容器被清理
func TestGarbageCollector_RunOnce_CleansOldContainers(t *testing.T) {
	now := time.Now()
	// 创建两个容器：一个超龄，一个未超龄
	containers := []container.Summary{
		{
			ID:      "old-container-id",
			Names:   []string{"/dtw-old"},
			Created: now.Add(-40 * time.Minute).Unix(), // 40分钟前创建，超出 35min 阈值
		},
		{
			ID:      "new-container-id",
			Names:   []string{"/dtw-new"},
			Created: now.Add(-10 * time.Minute).Unix(), // 10分钟前创建，未超龄
		},
	}

	mock := &mockDockerClientWithList{
		containers: containers,
	}

	gc := NewGarbageCollector(mock,
		WithGCInterval(time.Hour), // 设置很长的间隔，避免自动触发
		WithGCMaxAge(35*time.Minute),
	)

	gc.runOnce(context.Background())

	// 验证只有超龄容器被清理
	if len(mock.removedIDs) != 1 {
		t.Fatalf("应清理 1 个容器，实际清理 %d 个: %v", len(mock.removedIDs), mock.removedIDs)
	}
	if mock.removedIDs[0] != "old-container-id" {
		t.Errorf("清理的容器 ID = %q, 期望 old-container-id", mock.removedIDs[0])
	}
}

// TestGarbageCollector_RunOnce_NoContainers 验证无容器时不出错
func TestGarbageCollector_RunOnce_NoContainers(t *testing.T) {
	mock := &mockDockerClientWithList{
		containers: []container.Summary{},
	}

	gc := NewGarbageCollector(mock)
	// 不应 panic 或报错
	gc.runOnce(context.Background())

	if len(mock.removedIDs) != 0 {
		t.Errorf("无容器时不应清理任何容器，实际清理 %d 个", len(mock.removedIDs))
	}
}

// TestGarbageCollector_Run_StopsOnContextCancel 验证 ctx 取消后 Run 退出
func TestGarbageCollector_Run_StopsOnContextCancel(t *testing.T) {
	mock := &mockDockerClientWithList{}
	gc := NewGarbageCollector(mock, WithGCInterval(100*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		gc.Run(ctx)
		close(done)
	}()

	// 取消 context
	cancel()

	select {
	case <-done:
		// 正常退出
	case <-time.After(2 * time.Second):
		t.Error("GC Run 在 ctx 取消后未及时退出")
	}
}

// TestContainerDisplayName 验证容器名称格式化
func TestContainerDisplayName(t *testing.T) {
	tests := []struct {
		names    []string
		expected string
	}{
		{[]string{"/dtw-container"}, "dtw-container"},
		{[]string{"no-slash"}, "no-slash"},
		{[]string{}, "<unknown>"},
		{[]string{"/first", "/second"}, "first"},
	}

	for _, tc := range tests {
		got := containerDisplayName(tc.names)
		if got != tc.expected {
			t.Errorf("containerDisplayName(%v) = %q, 期望 %q", tc.names, got, tc.expected)
		}
	}
}
