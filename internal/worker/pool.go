package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// Pool 管理 Docker 容器生命周期的 Worker 池
// 不做并发控制（由 asynq 控制并发度），只负责容器创建和清理
type Pool struct {
	config PoolConfig
	docker DockerClient
	logger *slog.Logger

	networkMu      sync.Mutex // 保护网络创建，支持失败后重试
	networkCreated bool

	active atomic.Int32 // 当前活跃容器数
	total  atomic.Int64 // 累计完成任务数
	wg     sync.WaitGroup
}

// NewPool 创建 Worker 池
func NewPool(config PoolConfig, dockerClient DockerClient) *Pool {
	if config.NetworkName == "" {
		config.NetworkName = "dtworkflow-net"
	}
	return &Pool{
		config: config,
		docker: dockerClient,
		logger: slog.Default(),
	}
}

// ensureNetwork 确保 Docker 网络存在，支持失败后重试
func (p *Pool) ensureNetwork(ctx context.Context) error {
	p.networkMu.Lock()
	defer p.networkMu.Unlock()
	if p.networkCreated {
		return nil
	}
	if err := p.docker.EnsureNetwork(ctx, p.config.NetworkName); err != nil {
		return err
	}
	p.networkCreated = true
	return nil
}

// Run 在独立 Docker 容器中执行任务，容器用完即销毁
// 流程：EnsureNetwork → CreateContainer → StartContainer → WaitContainer → GetLogs → RemoveContainer
func (p *Pool) Run(ctx context.Context, payload model.TaskPayload) (*ExecutionResult, error) {
	p.wg.Add(1)
	defer p.wg.Done()
	p.active.Add(1)
	defer p.active.Add(-1)

	start := time.Now()

	// 构建容器名称（使用 DeliveryID 或任务类型+时间戳确保唯一性）
	containerName := buildContainerName(payload)

	// 构建容器标签，用于 GC 识别
	labels := map[string]string{
		"managed-by": "dtworkflow",
		"task-type":  string(payload.TaskType),
		"task-id":    payload.DeliveryID,
	}

	// 构建容器配置
	containerCfg := &ContainerConfig{
		Image:       p.config.Image,
		Name:        containerName,
		Env:         buildContainerEnv(p.config, payload),
		Cmd:         buildContainerCmd(payload),
		Labels:      labels,
		CPULimit:    p.config.CPULimit,
		MemoryLimit: p.config.MemoryLimit,
		NetworkName: p.config.NetworkName,
		WorkDir:     p.config.WorkDir,
	}

	// 确保 Docker 网络存在（支持失败后重试）
	if err := p.ensureNetwork(ctx); err != nil {
		return nil, fmt.Errorf("确保 Docker 网络失败: %w", err)
	}

	// 检查镜像是否存在
	exists, err := p.docker.ImageExists(ctx, p.config.Image)
	if err != nil {
		return nil, fmt.Errorf("检查镜像 %s 失败: %w", p.config.Image, err)
	}
	if !exists {
		return nil, fmt.Errorf("镜像 %s 不存在，请先构建或拉取", p.config.Image)
	}

	// 创建容器
	containerID, err := p.docker.CreateContainer(ctx, containerCfg)
	if err != nil {
		return nil, fmt.Errorf("创建容器失败: %w", err)
	}

	p.logger.InfoContext(ctx, "容器已创建",
		slog.String("container_id", containerID),
		slog.String("container_name", containerName),
		slog.String("task_type", string(payload.TaskType)),
		slog.String("repo", payload.RepoFullName),
	)

	// 确保容器在函数返回时被清理
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if removeErr := p.docker.RemoveContainer(cleanCtx, containerID); removeErr != nil {
			p.logger.WarnContext(ctx, "清理容器失败",
				slog.String("container_id", containerID),
				slog.String("error", removeErr.Error()),
			)
		} else {
			p.logger.InfoContext(ctx, "容器已清理", slog.String("container_id", containerID))
		}
	}()

	// 启动容器
	if err := p.docker.StartContainer(ctx, containerID); err != nil {
		return nil, fmt.Errorf("启动容器失败: %w", err)
	}

	// 等待容器退出（结合 ctx 取消）
	exitCode, waitErr := p.docker.WaitContainer(ctx, containerID)

	// 无论成功与否，都尝试获取日志（使用独立 context，避免原 ctx 已取消导致无法获取日志）
	logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer logCancel()
	logs, logErr := p.docker.GetContainerLogs(logCtx, containerID)
	if logErr != nil {
		p.logger.WarnContext(ctx, "获取容器日志失败",
			slog.String("container_id", containerID),
			slog.String("error", logErr.Error()),
		)
	}

	duration := time.Since(start).Milliseconds()
	p.total.Add(1)

	result := &ExecutionResult{
		ExitCode:    int(exitCode),
		Output:      logs,
		Duration:    duration,
		ContainerID: containerID,
	}

	if waitErr != nil {
		result.Error = waitErr.Error()
		p.logger.ErrorContext(ctx, "容器执行失败",
			slog.String("container_id", containerID),
			slog.Int64("exit_code", exitCode),
			slog.String("error", waitErr.Error()),
			slog.Int64("duration_ms", duration),
		)
		return result, waitErr
	}

	p.logger.InfoContext(ctx, "容器执行完成",
		slog.String("container_id", containerID),
		slog.Int64("exit_code", exitCode),
		slog.Int64("duration_ms", duration),
	)

	return result, nil
}

// Shutdown 优雅关闭 Worker 池，等待所有正在运行的容器完成
func (p *Pool) Shutdown(ctx context.Context) error {
	// 无论正常退出还是超时，都确保关闭 Docker 客户端连接
	defer func() {
		if err := p.docker.Close(); err != nil {
			p.logger.Warn("关闭 Docker 客户端失败", slog.String("error", err.Error()))
		}
	}()

	p.logger.Info("Worker 池正在关闭，等待活跃容器完成",
		slog.Int("active", int(p.active.Load())),
	)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info("Worker 池已关闭")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("Worker 池关闭超时: %w", ctx.Err())
	}
}

// Stats 返回当前池的统计信息
func (p *Pool) Stats() PoolStats {
	return PoolStats{
		Active:    int(p.active.Load()),
		Completed: p.total.Load(),
	}
}

// buildContainerName 根据任务 payload 构建唯一的容器名称
func buildContainerName(payload model.TaskPayload) string {
	id := payload.DeliveryID
	if id == "" {
		id = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	// 仅保留字母数字和连字符，截断到合理长度
	safe := sanitizeName(id)
	if len(safe) > 20 {
		safe = safe[:20]
	}
	return fmt.Sprintf("dtw-%s-%s", payload.TaskType, safe)
}

// sanitizeName 将字符串中非字母数字字符替换为连字符
func sanitizeName(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			result = append(result, c)
		} else {
			result = append(result, '-')
		}
	}
	return string(result)
}
