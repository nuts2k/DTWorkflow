package worker

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
)

// ContainerExitError 容器退出错误，包含容器 ID、退出码和错误信息
type ContainerExitError struct {
	ContainerID string
	ExitCode    int64
	Message     string
}

func (e *ContainerExitError) Error() string {
	return fmt.Sprintf("容器 %s 退出错误 (code=%d): %s", e.ContainerID, e.ExitCode, e.Message)
}

// DockerClient Docker SDK 操作接口（便于 mock 测试）
type DockerClient interface {
	// ImageExists 检查镜像是否存在于本地
	ImageExists(ctx context.Context, imageRef string) (bool, error)
	// EnsureNetwork 确保 Docker 网络存在，不存在则创建
	EnsureNetwork(ctx context.Context, networkName string) error
	// CreateContainer 创建容器并返回容器 ID
	CreateContainer(ctx context.Context, config *ContainerConfig) (string, error)
	// StartContainer 启动容器
	StartContainer(ctx context.Context, containerID string) error
	// WaitContainer 等待容器退出，返回退出码
	WaitContainer(ctx context.Context, containerID string) (int64, error)
	// RemoveContainer 强制删除容器（包括运行中的）
	RemoveContainer(ctx context.Context, containerID string) error
	// GetContainerLogs 获取容器标准输出和标准错误日志
	GetContainerLogs(ctx context.Context, containerID string) (string, error)
	// ListContainers 列举符合过滤条件的容器（用于 GC 扫描）
	ListContainers(ctx context.Context, f filters.Args) ([]container.Summary, error)
	// Close 关闭客户端连接
	Close() error
}

// ContainerConfig 容器创建配置
type ContainerConfig struct {
	Image       string
	Name        string
	Env         []string          // 环境变量（KEY=VALUE 格式）
	Cmd         []string          // 执行命令
	Labels      map[string]string // 容器标签（用于 GC 识别）
	CPULimit    string            // CPU 限制，如 "2.0"
	MemoryLimit string            // 内存限制，如 "4g"
	NetworkName string            // Docker 网络名称
	WorkDir     string            // 容器内工作目录
}

// dockerClient DockerClient 的真实实现
type dockerClient struct {
	cli *client.Client
}

// NewDockerClient 创建 Docker 客户端，使用环境变量或默认 socket 连接
func NewDockerClient() (DockerClient, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("创建 Docker 客户端失败: %w", err)
	}
	return &dockerClient{cli: cli}, nil
}

// ImageExists 检查镜像是否存在于本地缓存中
func (d *dockerClient) ImageExists(ctx context.Context, imageRef string) (bool, error) {
	_, _, err := d.cli.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("检查镜像 %s 失败: %w", imageRef, err)
	}
	return true, nil
}

// EnsureNetwork 确保指定名称的 bridge 网络存在，不存在则创建
func (d *dockerClient) EnsureNetwork(ctx context.Context, networkName string) error {
	f := filters.NewArgs()
	f.Add("name", networkName)
	networks, err := d.cli.NetworkList(ctx, network.ListOptions{Filters: f})
	if err != nil {
		return fmt.Errorf("查询 Docker 网络失败: %w", err)
	}
	// 精确匹配网络名称
	for _, n := range networks {
		if n.Name == networkName {
			return nil
		}
	}
	// 网络不存在，创建 bridge 网络
	_, err = d.cli.NetworkCreate(ctx, networkName, network.CreateOptions{
		Driver:     "bridge",
		Attachable: true,
		Labels: map[string]string{
			"managed-by": "dtworkflow",
		},
	})
	if err != nil {
		if errdefs.IsConflict(err) {
			return nil // 网络已存在，正常情况
		}
		return fmt.Errorf("创建 Docker 网络 %s 失败: %w", networkName, err)
	}
	return nil
}

// CreateContainer 根据配置创建容器并返回容器 ID
func (d *dockerClient) CreateContainer(ctx context.Context, cfg *ContainerConfig) (string, error) {
	// 解析资源限制
	var nanoCPUs int64
	var memBytes int64
	var parseErr error

	if cfg.CPULimit != "" {
		nanoCPUs, parseErr = parseCPULimit(cfg.CPULimit)
		if parseErr != nil {
			return "", fmt.Errorf("解析 CPU 限制失败: %w", parseErr)
		}
	}
	if cfg.MemoryLimit != "" {
		memBytes, parseErr = parseMemoryLimit(cfg.MemoryLimit)
		if parseErr != nil {
			return "", fmt.Errorf("解析内存限制失败: %w", parseErr)
		}
	}

	// 构建容器配置
	containerCfg := &container.Config{
		Image:      cfg.Image,
		Env:        cfg.Env,
		Cmd:        cfg.Cmd,
		Labels:     cfg.Labels,
		WorkingDir: cfg.WorkDir,
	}

	// 构建主机资源配置
	hostCfg := &container.HostConfig{
		SecurityOpt: []string{"no-new-privileges"},
		CapDrop:     []string{"ALL"},
		Resources: container.Resources{
			NanoCPUs: nanoCPUs,
			Memory:   memBytes,
			PidsLimit: int64Ptr(512),
		},
	}

	// 构建网络配置
	networkCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			cfg.NetworkName: {},
		},
	}

	resp, err := d.cli.ContainerCreate(ctx, containerCfg, hostCfg, networkCfg, nil, cfg.Name)
	if err != nil {
		return "", fmt.Errorf("创建容器 %s 失败: %w", cfg.Name, err)
	}
	return resp.ID, nil
}

// StartContainer 启动指定容器
func (d *dockerClient) StartContainer(ctx context.Context, containerID string) error {
	err := d.cli.ContainerStart(ctx, containerID, container.StartOptions{})
	if err != nil {
		return fmt.Errorf("启动容器 %s 失败: %w", containerID, err)
	}
	return nil
}

// WaitContainer 等待容器退出并返回退出码
func (d *dockerClient) WaitContainer(ctx context.Context, containerID string) (int64, error) {
	statusCh, errCh := d.cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return -1, fmt.Errorf("等待容器 %s 失败: %w", containerID, err)
		}
		// errCh 收到 nil 时，继续等待 statusCh 获取退出码（带超时保护）
		select {
		case body := <-statusCh:
			return body.StatusCode, nil
		case <-ctx.Done():
			return -1, fmt.Errorf("等待容器 %s 状态超时: %w", containerID, ctx.Err())
		}
	case status := <-statusCh:
		if status.Error != nil {
			return status.StatusCode, &ContainerExitError{
				ContainerID: containerID,
				ExitCode:    status.StatusCode,
				Message:     status.Error.Message,
			}
		}
		return status.StatusCode, nil
	case <-ctx.Done():
		return -1, fmt.Errorf("等待容器 %s 超时: %w", containerID, ctx.Err())
	}
}

// RemoveContainer 强制删除容器（包括正在运行的）
func (d *dockerClient) RemoveContainer(ctx context.Context, containerID string) error {
	err := d.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	})
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil // 容器不存在，视为已清理
		}
		return fmt.Errorf("删除容器 %s 失败: %w", containerID, err)
	}
	return nil
}

// GetContainerLogs 获取容器的标准输出和标准错误日志
func (d *dockerClient) GetContainerLogs(ctx context.Context, containerID string) (string, error) {
	reader, err := d.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     false,
		Tail:       "5000", // 限制最后 5000 行，防止 OOM
	})
	if err != nil {
		return "", fmt.Errorf("获取容器 %s 日志失败: %w", containerID, err)
	}
	defer reader.Close()

	// 限制日志总读取量为 10MB，防止极端长行导致 OOM
	limitedReader := io.LimitReader(reader, 10*1024*1024)

	// 使用 stdcopy.StdCopy 正确解复用 Docker 日志流（stdout + stderr）
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, limitedReader); err != nil {
		return "", fmt.Errorf("读取容器 %s 日志失败: %w", containerID, err)
	}
	return stdout.String() + stderr.String(), nil
}

// ListContainers 列举符合过滤条件的容器（用于 GC 扫描）
func (d *dockerClient) ListContainers(ctx context.Context, f filters.Args) ([]container.Summary, error) {
	return d.cli.ContainerList(ctx, container.ListOptions{
		All:     true, // 包含已停止的容器
		Filters: f,
	})
}

// Close 关闭 Docker 客户端连接
func (d *dockerClient) Close() error {
	return d.cli.Close()
}

// int64Ptr 返回 int64 值的指针，用于 Docker API 中需要 *int64 的字段
func int64Ptr(v int64) *int64 {
	return &v
}
