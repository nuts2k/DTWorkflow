package worker

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
)

// GCOption GarbageCollector 选项函数
type GCOption func(*GarbageCollector)

// GarbageCollector 扫描并清理超龄的 dtworkflow 容器
type GarbageCollector struct {
	docker       DockerClient
	interval     time.Duration // 扫描间隔，默认 60s
	maxAge       time.Duration // 容器最大存活时间，默认 35min
	forceKillAge time.Duration // 强制清理卡死 running 容器的阈值，默认 maxAge*3
	logger       *slog.Logger
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

// WithGCForceKillAge 设置强制清理卡死 running 容器的年龄阈值
func WithGCForceKillAge(d time.Duration) GCOption {
	return func(gc *GarbageCollector) {
		gc.forceKillAge = d
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
	// forceKillAge 默认为 maxAge*3，若未通过选项函数覆盖则在此设置
	if gc.forceKillAge == 0 {
		gc.forceKillAge = gc.maxAge * 3
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
	if ctx.Err() != nil {
		return
	}

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
	removed := 0
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

	// 检查 ctx 是否已取消，避免不必要的第二次扫描
	if ctx.Err() != nil {
		return
	}

	// 额外扫描 running 状态的容器：
	// - 超过 maxAge*2 记录 WARN 日志供运维关注
	// - 超过 forceKillAge 强制删除并记录 ERROR 日志
	rf := filters.NewArgs()
	rf.Add("label", "managed-by=dtworkflow")
	rf.Add("status", "running")
	runningContainers, runErr := gc.listContainers(ctx, rf)
	if runErr != nil {
		gc.logger.Error("GC 扫描运行中容器列表失败", slog.String("error", runErr.Error()))
	} else {
		stuckThreshold := gc.maxAge * 2
		for _, c := range runningContainers {
			createdAt := time.Unix(c.Created, 0)
			age := now.Sub(createdAt)
			shortID := c.ID
			if len(shortID) > 12 {
				shortID = shortID[:12]
			}
			if age > gc.forceKillAge {
				gc.logger.Error("强制清理卡死容器",
					slog.String("container", containerDisplayName(c.Names)),
					slog.Duration("age", age),
					slog.Duration("force_kill_age", gc.forceKillAge),
				)
				cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
				removeErr := gc.docker.RemoveContainer(cleanCtx, c.ID)
				cleanCancel()
				if removeErr != nil {
					gc.logger.Error("强制清理容器失败",
						slog.String("container_id", shortID),
						slog.String("name", containerDisplayName(c.Names)),
						slog.String("error", removeErr.Error()),
					)
				} else {
					removed++
				}
			} else if age > stuckThreshold {
				gc.logger.Warn("发现疑似卡死的运行中容器，请运维关注",
					slog.String("container_id", shortID),
					slog.String("name", containerDisplayName(c.Names)),
					slog.Duration("age", age),
					slog.Duration("stuck_threshold", stuckThreshold),
				)
			}
		}
	}

	if cleaned > 0 || removed > 0 || len(containers) > 0 || len(runningContainers) > 0 {
		gc.logger.Info("GC 扫描完成",
			slog.Int("scanned_exited", len(containers)),
			slog.Int("scanned_running", len(runningContainers)),
			slog.Int("cleaned", cleaned),
			slog.Int("force_killed", removed),
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
	return strings.TrimLeft(name, "/")
}
