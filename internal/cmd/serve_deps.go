package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/webhook"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// ServiceDeps 封装 serve 命令运行时的所有依赖
type ServiceDeps struct {
	Store              store.Store
	GiteaClient        *gitea.Client
	QueueClient        *queue.Client
	AsynqServer        *asynq.Server
	Pool               *worker.Pool
	Recovery           *queue.RecoveryLoop
	GC                 *worker.GarbageCollector
	Handler            webhook.Handler
	EnqueueHandler     *queue.EnqueueHandler // 具体类型，供 API trigger handlers 使用
	Notifier           queue.TaskNotifier
	GiteaConfigured    bool
	NotifierEnabled    bool
	WorkerImagePresent bool
}

type readinessSnapshot struct {
	RedisOK            bool
	SQLiteOK           bool
	GiteaConfigured    bool
	NotifierEnabled    bool
	WorkerImagePresent bool
	ActiveWorkers      int
}

func buildWorkerPoolConfigFromServeConfig(cfg serveConfig) worker.PoolConfig {
	pcfg := worker.PoolConfig{
		Image:         cfg.WorkerImage,
		CPULimit:      cfg.CPULimit,
		MemoryLimit:   cfg.MemoryLimit,
		GiteaURL:      cfg.GiteaURL,
		GiteaToken:    worker.SecretString(cfg.GiteaToken),
		ClaudeAPIKey:  worker.SecretString(cfg.ClaudeAPIKey),
		ClaudeBaseURL: cfg.ClaudeBaseURL,
		NetworkName:   cfg.NetworkName,
		Timeouts:      buildWorkerTimeoutConfigFromAppConfig(cfg.AppCfg),
		StreamMonitor: buildWorkerStreamMonitorConfigFromAppConfig(cfg.AppCfg),
	}
	if cfg.AppCfg != nil {
		pcfg.GiteaInsecureSkipVerify = cfg.AppCfg.Gitea.InsecureSkipVerify
		pcfg.ImageFull = cfg.AppCfg.Worker.ImageFull // M3.4
	}
	return pcfg
}

func computeReadyStatus(s readinessSnapshot) (map[string]any, int) {
	status := "ok"
	httpStatus := http.StatusOK
	if !s.RedisOK || !s.SQLiteOK || !s.GiteaConfigured || !s.NotifierEnabled || !s.WorkerImagePresent {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}
	return map[string]any{
		"status":               status,
		"version":              version,
		"redis":                s.RedisOK,
		"sqlite":               s.SQLiteOK,
		"gitea_configured":     s.GiteaConfigured,
		"notifier_enabled":     s.NotifierEnabled,
		"worker_image_present": s.WorkerImagePresent,
		"active_workers":       s.ActiveWorkers,
	}, httpStatus
}

// BuildServiceDeps 从 serveConfig 构建所有依赖，返回 ServiceDeps 和清理函数。
func BuildServiceDeps(cfg serveConfig) (*ServiceDeps, func(), error) {
	var cleanups []func()
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	// 说明：当前版本 Worker 执行依赖 Gitea 配置（容器内需要 GITEA_URL/GITEA_TOKEN）。
	// 为避免出现"日志提示可降级、实际又在更深层失败"的矛盾，这里做前置硬失败。
	if cfg.GiteaURL == "" || cfg.GiteaToken == "" {
		return nil, nil, fmt.Errorf("gitea-url 与 gitea-token 不能为空（当前版本 Worker 执行依赖 Gitea 配置）")
	}

	// 1. 初始化 SQLite Store
	s, err := store.NewSQLiteStore(cfg.DBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("初始化 SQLite 失败: %w", err)
	}
	cleanups = append(cleanups, func() { _ = s.Close() })

	// 2. 初始化 Gitea Client
	giteaConfigured := cfg.GiteaURL != "" && cfg.GiteaToken != ""
	var giteaClient *gitea.Client
	if giteaConfigured {
		giteaOpts := []gitea.ClientOption{gitea.WithToken(cfg.GiteaToken)}
		if cfg.AppCfg != nil && cfg.AppCfg.Gitea.InsecureSkipVerify {
			giteaOpts = append(giteaOpts, gitea.WithHTTPClient(&http.Client{
				Timeout: 30 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12}, //nolint:gosec
				},
			}))
		}
		giteaClient, err = gitea.NewClient(cfg.GiteaURL, giteaOpts...)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("初始化 Gitea Client 失败: %w", err)
		}
	}

	// 2.1 初始化可选通知路由
	notifier, err := buildNotifier(cfg.AppCfg, giteaClient)
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	// 3. 初始化 asynq Client（用于入队）
	redisOpt := asynq.RedisClientOpt{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB}
	queueClient, err := queue.NewClient(redisOpt)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("初始化 asynq Client 失败: %w", err)
	}
	queueClient.SetTimeouts(buildQueueTimeoutConfigFromAppConfig(cfg.AppCfg))
	cleanups = append(cleanups, func() { _ = queueClient.Close() })
	// 验证 Redis 连通性：Redis 是任务队列的核心依赖，不可用时必须快速失败
	if err := queueClient.Ping(context.Background()); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("Redis 连接失败，服务无法启动: %w", err)
	}

	// 4. 初始化 Docker Client 和 Worker Pool
	dockerClient, err := worker.NewDockerClient()
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("初始化 Docker Client 失败: %w", err)
	}
	cleanups = append(cleanups, func() { _ = dockerClient.Close() })

	workerImagePresent, err := dockerClient.ImageExists(context.Background(), cfg.WorkerImage)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("检查 Worker 镜像失败: %w", err)
	}

	poolCfg := buildWorkerPoolConfigFromServeConfig(cfg)
	pool, err := worker.NewPool(poolCfg, dockerClient)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("初始化 Worker Pool 失败: %w", err)
	}
	cleanups = append(cleanups, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = pool.Shutdown(ctx)
	})

	// 5. 初始化 asynq Server（并发由此控制）
	// 注意：MaxWorkers 的并发控制由 asynq Server 的 Concurrency 参数管理，
	// 而非 Worker Pool 自身。Pool 仅负责容器生命周期管理，不限制并发数量。
	// 这是有意的架构决策：asynq 作为任务调度层统一管控并发，Pool 作为执行层专注于容器操作。
	asynqServer := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: cfg.MaxWorkers,
		Queues: map[string]int{
			queue.QueueCritical: 6,
			queue.QueueDefault:  3,
			queue.QueueLow:      1,
		},
		RetryDelayFunc: func(n int, err error, task *asynq.Task) time.Duration {
			return queue.TaskRetryDelay(n)
		},
	})

	// 6. 构建 EnqueueHandler（M2.4: 注入 TaskCanceller）
	handler := queue.NewEnqueueHandler(queueClient, queueClient, s, slog.Default())

	// 7. 构建 RecoveryLoop
	recovery := queue.NewRecoveryLoop(s, queueClient, slog.Default(), 60*time.Second, 120*time.Second)

	// 8. 构建容器 GC
	gc := worker.NewGarbageCollector(dockerClient)

	return &ServiceDeps{
		Store:              s,
		GiteaClient:        giteaClient,
		QueueClient:        queueClient,
		AsynqServer:        asynqServer,
		Pool:               pool,
		Recovery:           recovery,
		GC:                 gc,
		Handler:            handler,
		EnqueueHandler:     handler,
		Notifier:           notifier,
		GiteaConfigured:    giteaConfigured,
		NotifierEnabled:    notifier != nil,
		WorkerImagePresent: workerImagePresent,
	}, cleanup, nil
}

func buildServeConfigFromManager(mgr *config.Manager) (serveConfig, error) {
	if mgr == nil {
		return serveConfig{}, fmt.Errorf("配置管理器不能为空")
	}
	cfg := mgr.Get()
	if cfg == nil {
		return serveConfig{}, fmt.Errorf("配置未加载")
	}
	return serveConfig{
		Host:          cfg.Server.Host,
		Port:          cfg.Server.Port,
		RedisAddr:     cfg.Redis.Addr,
		RedisPassword: cfg.Redis.Password,
		RedisDB:       cfg.Redis.DB,
		DBPath:        cfg.Database.Path,
		WebhookSecret: cfg.Webhook.Secret,
		ClaudeAPIKey:  cfg.Claude.APIKey,
		ClaudeBaseURL: cfg.Claude.BaseURL,
		GiteaURL:      cfg.Gitea.URL,
		GiteaToken:    cfg.Gitea.Token,
		MaxWorkers:    cfg.Worker.Concurrency,
		WorkerImage:   cfg.Worker.Image,
		CPULimit:      cfg.Worker.CPULimit,
		MemoryLimit:   cfg.Worker.MemoryLimit,
		NetworkName:   cfg.Worker.NetworkName,
		AppCfg:        cfg,
	}, nil
}
