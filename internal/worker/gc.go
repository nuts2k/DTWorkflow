package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
)

// GCOption GarbageCollector 选项函数
type GCOption func(*GarbageCollector)

// GarbageCollector 扫描并清理超龄的 dtworkflow 容器
type GarbageCollector struct {
	docker   DockerClient
	interval time.Duration // 扫描间隔，默认 60s
	maxAge   time.Duration // 容器最大存活时间，默认 35min
	logger   *slog.Logger
}

// WithGCInterval 设置扫描间隔
func WithGCInterval(d time.Duration) GCOption {
	return func(gc *GarbageCollector) {
		gc.interval = d
	}
}

// WithGCMaxAge 设置容器最大存活时间
func WithGCMaxAge(d time.Duration) GCOption {
	return func(gc *GarbageCollector) {
		gc.maxAge = d
	}
}

// WithGCLogger 设置日志记录器
func WithGCLogger(logger *slog.Logger) GCOption {
	return func(gc *GarbageCollector) {
		gc.logger = logger
	}
}

// NewGarbageCollector 创建容器 GC，支持选项函数定制行为
func NewGarbageCollector(docker DockerClient, opts ...GCOption) *GarbageCollector {
	gc := &GarbageCollector{
		docker:   docker,
		interval: 60 * time.Second,
		maxAge:   35 * time.Minute, // 30min 最长任务 + 5min 缓冲
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(gc)
	}
	return gc
}

// Run 启动 GC 循环，直到 ctx 取消为止
func (gc *GarbageCollector) Run(ctx context.Context) {
	gc.logger.Info("容器 GC 已启动",
		slog.Duration("interval", gc.interval),
		slog.Duration("max_age", gc.maxAge),
	)
	ticker := time.NewTicker(gc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			gc.logger.Info("容器 GC 已停止")
			return
		case <-ticker.C:
			gc.runOnce(ctx)
		}
	}
}

// runOnce 执行一次 GC 扫描，清理超龄容器
func (gc *GarbageCollector) runOnce(ctx context.Context) {
	// 仅扫描带有 managed-by=dtworkflow 标签且已退出的容器
	f := filters.NewArgs()
	f.Add("label", "managed-by=dtworkflow")
	f.Add("status", "exited")
	f.Add("status", "dead")

	containers, err := gc.listContainers(ctx, f)
	if err != nil {
		gc.logger.Error("GC 扫描容器列表失败", slog.String("error", err.Error()))
		return
	}

	now := time.Now()
	cleaned := 0
	for _, c := range containers {
		// 容器创建时间（Unix 时间戳，秒）
		createdAt := time.Unix(c.Created, 0)
		age := now.Sub(createdAt)
		if age > gc.maxAge {
			shortID := c.ID
			if len(shortID) > 12 {
				shortID = shortID[:12]
			}
			gc.logger.Warn("发现超龄容器，准备清理",
				slog.String("container_id", shortID),
				slog.String("name", containerDisplayName(c.Names)),
				slog.Duration("age", age),
				slog.Duration("max_age", gc.maxAge),
			)
			// 使用 context.Background() 而非传入的 ctx，确保即使 GC 循环被取消也能完成容器清理
			cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
			removeErr := gc.docker.RemoveContainer(cleanCtx, c.ID)
			cleanCancel()
			if removeErr != nil {
				gc.logger.Error("GC 清理容器失败",
					slog.String("container_id", shortID),
					slog.String("error", removeErr.Error()),
				)
			} else {
				gc.logger.Info("GC 已清理容器",
					slog.String("container_id", shortID),
					slog.String("name", containerDisplayName(c.Names)),
				)
				cleaned++
			}
		}
	}

	if cleaned > 0 || len(containers) > 0 {
		gc.logger.Info("GC 扫描完成",
			slog.Int("scanned", len(containers)),
			slog.Int("cleaned", cleaned),
		)
	}
}

// listContainers 获取符合过滤条件的容器列表
func (gc *GarbageCollector) listContainers(ctx context.Context, f filters.Args) ([]container.Summary, error) {
	return gc.docker.ListContainers(ctx, f)
}

// containerDisplayName 返回容器名称（Docker 名称带前缀 /，去掉前缀后返回）
func containerDisplayName(names []string) string {
	if len(names) == 0 {
		return "<unknown>"
	}
	name := names[0]
	if len(name) > 0 && name[0] == '/' {
		return name[1:]
	}
	return name
}
