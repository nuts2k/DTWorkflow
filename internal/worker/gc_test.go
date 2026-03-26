package worker

import (
	"context"
	"errors"
	"log/slog"
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

// TestGarbageCollector_WithGCLogger 验证 Logger 选项
func TestGarbageCollector_WithGCLogger(t *testing.T) {
	mock := &mockDockerClient{}
	logger := slog.Default()
	gc := NewGarbageCollector(mock, WithGCLogger(logger))
	if gc.logger != logger {
		t.Error("WithGCLogger 未生效")
	}
}

// TestGarbageCollector_WithGCForceKillAge 验证 ForceKillAge 选项
func TestGarbageCollector_WithGCForceKillAge(t *testing.T) {
	mock := &mockDockerClient{}
	gc := NewGarbageCollector(mock, WithGCForceKillAge(2*time.Hour))
	if gc.forceKillAge != 2*time.Hour {
		t.Errorf("forceKillAge = %v, 期望 2h", gc.forceKillAge)
	}
}

// TestGarbageCollector_DefaultForceKillAge 验证 forceKillAge 默认为 maxAge*3
func TestGarbageCollector_DefaultForceKillAge(t *testing.T) {
	mock := &mockDockerClient{}
	gc := NewGarbageCollector(mock, WithGCMaxAge(10*time.Minute))
	if gc.forceKillAge != 30*time.Minute {
		t.Errorf("forceKillAge = %v, 期望 30min (maxAge*3)", gc.forceKillAge)
	}
}

// TestGarbageCollector_RunOnce_ForceKillStuckRunningContainer 验证强制清理卡死容器
func TestGarbageCollector_RunOnce_ForceKillStuckRunningContainer(t *testing.T) {
	now := time.Now()
	mock := &mockDockerClientWithList{
		containers: []container.Summary{}, // 无已退出容器
		runningContainers: []container.Summary{
			{
				ID:      "stuck-container",
				Names:   []string{"/dtw-stuck"},
				Created: now.Add(-4 * time.Hour).Unix(), // 4 小时前创建
			},
		},
	}

	gc := NewGarbageCollector(mock,
		WithGCMaxAge(35*time.Minute),
		WithGCForceKillAge(1*time.Hour), // 1 小时即强制清理
	)
	gc.runOnce(context.Background())

	if len(mock.removedIDs) != 1 || mock.removedIDs[0] != "stuck-container" {
		t.Errorf("应强制清理卡死容器, 实际清理: %v", mock.removedIDs)
	}
}

// TestGarbageCollector_RunOnce_WarnStuckButNotForceKill 验证 stuck 但未到 forceKillAge 只记日志
func TestGarbageCollector_RunOnce_WarnStuckButNotForceKill(t *testing.T) {
	now := time.Now()
	mock := &mockDockerClientWithList{
		containers: []container.Summary{},
		runningContainers: []container.Summary{
			{
				ID:      "slow-container",
				Names:   []string{"/dtw-slow"},
				Created: now.Add(-80 * time.Minute).Unix(), // 80 分钟前（超 maxAge*2=70min，但未超 forceKillAge=105min）
			},
		},
	}

	gc := NewGarbageCollector(mock, WithGCMaxAge(35*time.Minute))
	gc.runOnce(context.Background())

	// 未到 forceKillAge，不应删除
	if len(mock.removedIDs) != 0 {
		t.Errorf("未超 forceKillAge 时不应清理容器, 实际清理: %v", mock.removedIDs)
	}
}

// TestGarbageCollector_RunOnce_ContextCancelled 验证 ctx 取消时跳过第二次扫描
func TestGarbageCollector_RunOnce_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	mock := &mockDockerClientWithList{}
	gc := NewGarbageCollector(mock)
	// 不应 panic
	gc.runOnce(ctx)
}

// TestGarbageCollector_RunOnce_ListError 验证列表查询失败时不 panic
func TestGarbageCollector_RunOnce_ListError(t *testing.T) {
	mock := &mockDockerClient{
		listContainersFunc: func(ctx context.Context, f filters.Args) ([]container.Summary, error) {
			return nil, errors.New("docker 不可用")
		},
	}
	gc := NewGarbageCollector(mock)
	// 不应 panic
	gc.runOnce(context.Background())
}

// TestGarbageCollector_RunOnce_RemoveError 验证删除失败时继续处理其他容器
func TestGarbageCollector_RunOnce_RemoveError(t *testing.T) {
	now := time.Now()
	removeAttempts := 0
	mock := &mockDockerClient{
		listContainersFunc: func(ctx context.Context, f filters.Args) ([]container.Summary, error) {
			if f.ExactMatch("status", "running") {
				return nil, nil
			}
			return []container.Summary{
				{
					ID:      "fail-container",
					Names:   []string{"/dtw-fail"},
					Created: now.Add(-40 * time.Minute).Unix(),
				},
				{
					ID:      "ok-container",
					Names:   []string{"/dtw-ok"},
					Created: now.Add(-40 * time.Minute).Unix(),
				},
			}, nil
		},
		removeContainerFunc: func(ctx context.Context, containerID string) error {
			removeAttempts++
			if containerID == "fail-container" {
				return errors.New("删除失败")
			}
			return nil
		},
	}
	gc := NewGarbageCollector(mock, WithGCMaxAge(35*time.Minute))
	gc.runOnce(context.Background())

	if removeAttempts != 2 {
		t.Errorf("应尝试删除 2 个容器，实际尝试 %d 次", removeAttempts)
	}
}

// TestGarbageCollector_RunOnce_RunningListError 验证 running 容器列表查询失败不影响 exited 处理
func TestGarbageCollector_RunOnce_RunningListError(t *testing.T) {
	now := time.Now()
	callCount := 0
	mock := &mockDockerClient{
		listContainersFunc: func(ctx context.Context, f filters.Args) ([]container.Summary, error) {
			callCount++
			if f.ExactMatch("status", "running") {
				return nil, errors.New("查询 running 失败")
			}
			return []container.Summary{
				{
					ID:      "old-exited",
					Names:   []string{"/dtw-old"},
					Created: now.Add(-40 * time.Minute).Unix(),
				},
			}, nil
		},
		removeContainerFunc: func(ctx context.Context, containerID string) error {
			return nil
		},
	}
	gc := NewGarbageCollector(mock, WithGCMaxAge(35*time.Minute))
	gc.runOnce(context.Background())

	// 应进行两次 ListContainers 调用（exited + running）
	if callCount != 2 {
		t.Errorf("ListContainers 调用次数 = %d, 期望 2", callCount)
	}
}

// TestGarbageCollector_RunOnce_ForceKillRemoveError 验证强制清理失败时不 panic
func TestGarbageCollector_RunOnce_ForceKillRemoveError(t *testing.T) {
	now := time.Now()
	mock := &mockDockerClient{
		listContainersFunc: func(ctx context.Context, f filters.Args) ([]container.Summary, error) {
			if f.ExactMatch("status", "running") {
				return []container.Summary{
					{
						ID:      "stuck-rm-fail",
						Names:   []string{"/dtw-stuck"},
						Created: now.Add(-4 * time.Hour).Unix(),
					},
				}, nil
			}
			return nil, nil
		},
		removeContainerFunc: func(ctx context.Context, containerID string) error {
			return errors.New("强制删除失败")
		},
	}
	gc := NewGarbageCollector(mock,
		WithGCMaxAge(35*time.Minute),
		WithGCForceKillAge(1*time.Hour),
	)
	// 不应 panic
	gc.runOnce(context.Background())
}

// TestGarbageCollector_RunOnce_ShortContainerID 验证短 ID（<=12 位）不被截断
func TestGarbageCollector_RunOnce_ShortContainerID(t *testing.T) {
	now := time.Now()
	mock := &mockDockerClientWithList{
		containers: []container.Summary{
			{
				ID:      "short-id", // 少于 12 字符
				Names:   []string{"/dtw-short"},
				Created: now.Add(-40 * time.Minute).Unix(),
			},
		},
	}
	gc := NewGarbageCollector(mock, WithGCMaxAge(35*time.Minute))
	gc.runOnce(context.Background())

	if len(mock.removedIDs) != 1 {
		t.Errorf("应清理 1 个容器, 实际: %d", len(mock.removedIDs))
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
