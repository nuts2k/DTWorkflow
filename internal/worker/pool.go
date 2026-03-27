package worker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// stdinWriteTimeout stdin 数据写入超时，防止容器未读 stdin 导致 goroutine 永久阻塞
const stdinWriteTimeout = 30 * time.Second

// consecutiveHyphens 匹配连续的连字符，用于容器名称清理
var consecutiveHyphens = regexp.MustCompile(`-{2,}`)

// Pool 管理 Docker 容器生命周期的 Worker 池
// 不做并发控制（由 asynq 控制并发度），只负责容器创建和清理
type Pool struct {
	config PoolConfig
	docker DockerClient
	logger *slog.Logger

	networkMu      sync.Mutex // 保护网络创建，支持失败后重试
	networkCreated bool

	shutdownMu sync.RWMutex // 保护 closed 检查与 wg.Add 之间的原子性，防止 TOCTOU 竞态
	closed     atomic.Bool  // 标记池是否已关闭
	active     atomic.Int32 // 当前活跃容器数
	total      atomic.Int64 // 累计完成任务数
	wg         sync.WaitGroup
}

// NewPool 创建 Worker 池。必填字段由 PoolConfig.Validate() 校验。
func NewPool(config PoolConfig, dockerClient DockerClient) (*Pool, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if config.NetworkName == "" {
		config.NetworkName = "dtworkflow-net"
	}
	return &Pool{
		config: config,
		docker: dockerClient,
		logger: slog.Default(),
	}, nil
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

// containerRunOpts 容器执行可选参数（nil 表示标准模式）
type containerRunOpts struct {
	stdinData []byte // 通过 stdin 传入的数据（非空时启用 stdin 模式）
}

// Run 在独立 Docker 容器中执行任务，容器用完即销毁
// 流程：EnsureNetwork → CreateContainer → StartContainer → WaitContainer → GetLogs → RemoveContainer
// 注意：当容器执行失败时，同时返回 result（包含日志和退出码）和 error，
// 调用方应同时检查两者以获取完整的调试信息。
func (p *Pool) Run(ctx context.Context, payload model.TaskPayload) (*ExecutionResult, error) {
	return p.runContainer(ctx, payload, buildContainerCmd(payload), nil)
}

// RunWithCommand 与 Run 相同的容器生命周期管理，但使用调用方提供的命令替代 buildContainerCmd。
// 用于 review.Service 等需要自定义 prompt 的场景。
func (p *Pool) RunWithCommand(ctx context.Context, payload model.TaskPayload, cmd []string) (*ExecutionResult, error) {
	return p.runContainer(ctx, payload, cmd, nil)
}

// RunWithCommandAndStdin 与 RunWithCommand 相同的容器生命周期管理，
// 但额外通过 stdin 传入数据。用于评审场景：prompt 通过 stdin 传入，
// 避免命令行参数暴露。stdinData 为空时行为与 RunWithCommand 一致。
func (p *Pool) RunWithCommandAndStdin(
	ctx context.Context,
	payload model.TaskPayload,
	cmd []string,
	stdinData []byte,
) (*ExecutionResult, error) {
	if len(stdinData) == 0 {
		return p.runContainer(ctx, payload, cmd, nil)
	}
	return p.runContainer(ctx, payload, cmd, &containerRunOpts{stdinData: stdinData})
}

// runContainer 统一的容器执行骨架，支持标准模式和 stdin 模式。
// opts 为 nil 时使用标准模式；opts.stdinData 非空时启用 stdin 模式：
// 容器创建后 attach stdin → 启动容器 → goroutine 写入数据 → 等待完成。
func (p *Pool) runContainer(ctx context.Context, payload model.TaskPayload, cmd []string, opts *containerRunOpts) (*ExecutionResult, error) {
	p.shutdownMu.RLock()
	if p.closed.Load() {
		p.shutdownMu.RUnlock()
		return nil, fmt.Errorf("Worker 池已关闭")
	}
	p.wg.Add(1)
	p.shutdownMu.RUnlock()
	defer p.wg.Done()
	p.active.Add(1)
	defer p.active.Add(-1)

	// 校验任务类型，对未知类型快速失败，避免进入容器创建流程
	if !payload.TaskType.IsValid() {
		return nil, fmt.Errorf("未知的任务类型: %q", payload.TaskType)
	}

	start := time.Now()

	// 构建容器名称（使用 DeliveryID 或任务类型+时间戳确保唯一性）
	containerName := buildContainerName(payload)

	// 构建容器标签，用于 GC 识别
	labels := map[string]string{
		"managed-by": "dtworkflow",
		"task-type":  string(payload.TaskType),
		"task-id":    payload.DeliveryID,
	}

	useStdin := opts != nil && len(opts.stdinData) > 0

	// 构建容器配置
	containerCfg := &ContainerConfig{
		Image:       p.config.Image,
		Name:        containerName,
		Env:         buildContainerEnv(p.config, payload),
		Cmd:         cmd,
		Labels:      labels,
		CPULimit:    p.config.CPULimit,
		MemoryLimit: p.config.MemoryLimit,
		NetworkName: p.config.NetworkName,
		WorkDir:     p.config.WorkDir,
		OpenStdin:   useStdin,
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
		slog.Bool("stdin_mode", useStdin),
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

	// 可选：容器创建后、启动前 attach stdin，确保数据不丢失
	var stdinWriter io.WriteCloser
	if useStdin {
		stdinWriter, err = p.docker.AttachContainer(ctx, containerID)
		if err != nil {
			return nil, fmt.Errorf("attach 容器 stdin 失败: %w", err)
		}
	}

	// 启动容器
	if err := p.docker.StartContainer(ctx, containerID); err != nil {
		if stdinWriter != nil {
			stdinWriter.Close()
		}
		// 启动失败时返回部分填充的 ExecutionResult（包含 ContainerID），
		// 便于调用方记录和排查问题（参考 waitErr 时的处理模式）
		return &ExecutionResult{
			ExitCode:    -1,
			ContainerID: containerID,
			Error:       err.Error(),
		}, fmt.Errorf("启动容器失败: %w", err)
	}

	// 为容器等待设置独立超时，防止 Docker daemon 无响应导致 goroutine 永远阻塞
	waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Minute)
	defer waitCancel()

	// 可选：后台 goroutine 写入 stdin 数据，通过 channel 报告写入结果
	var stdinErrCh chan error
	if stdinWriter != nil {
		stdinErrCh = make(chan error, 1)
		go func() {
			defer stdinWriter.Close()
			if tc, ok := stdinWriter.(interface{ SetWriteDeadline(t time.Time) error }); ok {
				_ = tc.SetWriteDeadline(time.Now().Add(stdinWriteTimeout))
			}
			_, werr := stdinWriter.Write(opts.stdinData)
			stdinErrCh <- werr
			if werr != nil {
				waitCancel() // stdin 写入失败时提前终止容器等待，避免长时间挂起
			}
		}()
	}
	exitCode, waitErr := p.docker.WaitContainer(waitCtx, containerID)

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

	output := logs.Stdout
	if waitErr != nil || exitCode != 0 {
		output = mergeLogStreams(logs.Stdout, logs.Stderr)
	}

	result := &ExecutionResult{
		ExitCode:    int(exitCode),
		Output:      output,
		Duration:    duration,
		ContainerID: containerID,
	}

	// 检查 stdin 写入错误：写入失败意味着容器收到的 prompt 为空或截断，结果不可信
	if stdinErrCh != nil {
		if stdinErr := <-stdinErrCh; stdinErr != nil {
			p.logger.ErrorContext(ctx, "写入容器 stdin 失败",
				slog.String("container_id", containerID),
				slog.String("error", stdinErr.Error()),
			)
			// 容器等待也失败时，优先返回容器错误（根因更可能在容器侧）
			if waitErr == nil {
				result.Error = stdinErr.Error()
				return result, fmt.Errorf("stdin 写入失败，结果不可信: %w", stdinErr)
			}
		}
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

func mergeLogStreams(stdout, stderr string) string {
	switch {
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	case strings.HasSuffix(stdout, "\n") || strings.HasPrefix(stderr, "\n"):
		return stdout + stderr
	default:
		return stdout + "\n" + stderr
	}
}

// Shutdown 优雅关闭 Worker 池，等待所有正在运行的容器完成
func (p *Pool) Shutdown(ctx context.Context) error {
	p.shutdownMu.Lock()
	if !p.closed.CompareAndSwap(false, true) {
		p.shutdownMu.Unlock()
		return nil // 已经在关闭中
	}
	p.shutdownMu.Unlock()

	p.logger.Info("Worker 池正在关闭...",
		slog.Int("active", int(p.active.Load())),
	)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info("所有 Worker 已完成")
	case <-ctx.Done():
		p.logger.Error("Worker 池关闭超时，等待容器清理完成...")
		// 不立即关闭 docker client，给 defer RemoveContainer 留出时间
		// 额外等待一段时间让 defer 中的清理逻辑执行
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		select {
		case <-done:
			p.logger.Info("容器清理完成")
		case <-cleanupCtx.Done():
			p.logger.Error("容器清理超时，部分容器可能需要手动清理")
		}
	}

	return p.docker.Close()
}

// Stats 返回当前池的统计信息
func (p *Pool) Stats() PoolStats {
	return PoolStats{
		Active:    int(p.active.Load()),
		Completed: p.total.Load(),
	}
}

// containerSeq 包级别原子计数器，用于避免容器名称碰撞
var containerSeq atomic.Int64

// buildContainerName 根据任务 payload 构建唯一的容器名称
func buildContainerName(payload model.TaskPayload) string {
	id := payload.DeliveryID
	if id == "" {
		id = fmt.Sprintf("%d-%d", time.Now().UnixNano(), containerSeq.Add(1))
	}
	// 仅保留字母数字和连字符，截断到合理长度
	safe := sanitizeName(id)
	if len(safe) > 20 {
		safe = safe[:20]
	}
	return fmt.Sprintf("dtw-%s-%s", payload.TaskType, safe)
}

// sanitizeName 将字符串中非字母数字字符替换为连字符，并压缩连续连字符
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
	// 压缩连续连字符为单个
	result = consecutiveHyphens.ReplaceAllLiteral(result, []byte("-"))
	return string(result)
}
