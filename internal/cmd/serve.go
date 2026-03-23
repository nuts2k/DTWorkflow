package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/webhook"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

var (
	serveHost          string
	servePort          int
	serveWebhookSecret string
	serveRedisAddr     string
	serveDBPath        string
	serveMaxWorkers    int
	serveWorkerImage   string
	serveClaudeAPIKey  string
	serveGiteaURL      string
	serveGiteaToken    string
)

// serveConfig 封装 serve 命令的所有配置，避免测试直接修改包级全局变量
type serveConfig struct {
	Host          string
	Port          int
	RedisAddr     string
	DBPath        string
	WebhookSecret string
	ClaudeAPIKey  string
	GiteaURL      string
	GiteaToken    string
	MaxWorkers    int
	WorkerImage   string
	MemoryLimit   string
	CPULimit      string
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "启动 HTTP 服务（API + Webhook 接收器 + 任务执行引擎）",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().StringVar(&serveHost, "host", "0.0.0.0", "监听地址")
	serveCmd.Flags().IntVar(&servePort, "port", 8080, "监听端口")
	serveCmd.Flags().StringVar(&serveWebhookSecret, "webhook-secret",
		"", "Webhook 签名密钥（也可通过 DTWORKFLOW_WEBHOOK_SECRET 环境变量设置）")
	serveCmd.Flags().StringVar(&serveRedisAddr, "redis-addr",
		getEnvDefault("DTWORKFLOW_REDIS_ADDR", "localhost:6379"), "Redis 地址（也可通过 DTWORKFLOW_REDIS_ADDR 环境变量设置）")
	serveCmd.Flags().StringVar(&serveDBPath, "db-path",
		getEnvDefault("DTWORKFLOW_DB_PATH", "data/dtworkflow.db"), "SQLite 数据库路径（也可通过 DTWORKFLOW_DB_PATH 环境变量设置）")
	serveCmd.Flags().IntVar(&serveMaxWorkers, "max-workers", 3, "最大并发 Worker 数")
	serveCmd.Flags().StringVar(&serveWorkerImage, "worker-image", "dtworkflow-worker:1.0", "Worker Docker 镜像")
	serveCmd.Flags().StringVar(&serveClaudeAPIKey, "claude-api-key",
		"", "Claude API Key（也可通过 DTWORKFLOW_CLAUDE_API_KEY 环境变量设置）")
	serveCmd.Flags().StringVar(&serveGiteaURL, "gitea-url",
		"", "Gitea 实例地址（也可通过 DTWORKFLOW_GITEA_URL 环境变量设置）")
	serveCmd.Flags().StringVar(&serveGiteaToken, "gitea-token",
		"", "Gitea API Token（也可通过 DTWORKFLOW_GITEA_TOKEN 环境变量设置）")
	rootCmd.AddCommand(serveCmd)
}

// ServiceDeps 封装 serve 命令运行时的所有依赖
type ServiceDeps struct {
	Store       store.Store
	GiteaClient *gitea.Client
	QueueClient *queue.Client
	AsynqServer *asynq.Server
	Pool        *worker.Pool
	Recovery    *queue.RecoveryLoop
	GC          *worker.GarbageCollector
	Handler     webhook.Handler
}

// BuildServiceDeps 从 serveConfig 构建所有依赖，返回 ServiceDeps 和清理函数
func BuildServiceDeps(cfg serveConfig) (*ServiceDeps, func(), error) {
	var cleanups []func()
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	// 1. 初始化 SQLite Store
	s, err := store.NewSQLiteStore(cfg.DBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("初始化 SQLite 失败: %w", err)
	}
	cleanups = append(cleanups, func() { _ = s.Close() })

	// 2. 初始化 Gitea Client
	var giteaClient *gitea.Client
	if cfg.GiteaURL != "" && cfg.GiteaToken != "" {
		giteaClient, err = gitea.NewClient(cfg.GiteaURL, gitea.WithToken(cfg.GiteaToken))
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("初始化 Gitea Client 失败: %w", err)
		}
	}

	// 3. 初始化 asynq Client（用于入队）
	redisOpt := asynq.RedisClientOpt{Addr: cfg.RedisAddr}
	queueClient, err := queue.NewClient(redisOpt)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("初始化 asynq Client 失败: %w", err)
	}
	cleanups = append(cleanups, func() { _ = queueClient.Close() })
	// 创建后验证 Redis 连通性（非致命，降级运行）
	if err := queueClient.Ping(context.Background()); err != nil {
		slog.Warn("Redis 连接检查失败，队列功能可能不可用", "error", err)
	}

	// 4. 初始化 Docker Client 和 Worker Pool
	dockerClient, err := worker.NewDockerClient()
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("初始化 Docker Client 失败: %w", err)
	}
	cleanups = append(cleanups, func() { _ = dockerClient.Close() })

	cpuLimit := cfg.CPULimit
	if cpuLimit == "" {
		cpuLimit = "2.0"
	}
	memoryLimit := cfg.MemoryLimit
	if memoryLimit == "" {
		memoryLimit = "4g"
	}

	pool, err := worker.NewPool(worker.PoolConfig{
		Image:        cfg.WorkerImage,
		CPULimit:     cpuLimit,
		MemoryLimit:  memoryLimit,
		GiteaURL:     cfg.GiteaURL,
		GiteaToken:   worker.SecretString(cfg.GiteaToken),
		ClaudeAPIKey: worker.SecretString(cfg.ClaudeAPIKey),
		NetworkName:  "dtworkflow-net",
	}, dockerClient)
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

	// 6. 构建 EnqueueHandler
	handler := queue.NewEnqueueHandler(queueClient, s, slog.Default())

	// 7. 构建 RecoveryLoop
	recovery := queue.NewRecoveryLoop(s, queueClient, slog.Default(), 60*time.Second, 120*time.Second)

	// 8. 构建容器 GC
	gc := worker.NewGarbageCollector(dockerClient)

	return &ServiceDeps{
		Store:       s,
		GiteaClient: giteaClient,
		QueueClient: queueClient,
		AsynqServer: asynqServer,
		Pool:        pool,
		Recovery:    recovery,
		GC:          gc,
		Handler:     handler,
	}, cleanup, nil
}

// runServe 是 Cobra 命令入口，将全局变量读入 serveConfig 后委托给 runServeWithConfig。
func runServe(cmd *cobra.Command, args []string) error {
	cfg := serveConfig{
		Host:          serveHost,
		Port:          servePort,
		RedisAddr:     serveRedisAddr,
		DBPath:        serveDBPath,
		WebhookSecret: serveWebhookSecret,
		ClaudeAPIKey:  serveClaudeAPIKey,
		GiteaURL:      serveGiteaURL,
		GiteaToken:    serveGiteaToken,
		MaxWorkers:    serveMaxWorkers,
		WorkerImage:   serveWorkerImage,
	}

	// 使用 OS 信号作为 stopCh
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	stopCh := make(chan struct{})
	go func() {
		<-quit
		signal.Stop(quit)
		close(stopCh)
	}()

	return runServeWithConfig(cfg, stopCh)
}

// runServeWithConfig 启动 HTTP 服务，注册路由，并支持优雅关闭。
// stopCh 关闭时触发分层关闭流程，便于测试通过 close(stopCh) 代替 syscall.Kill。
func runServeWithConfig(cfg serveConfig, stopCh <-chan struct{}) error {
	// 参数校验
	if cfg.MaxWorkers <= 0 {
		return fmt.Errorf("--max-workers 必须为正整数，当前值: %d", cfg.MaxWorkers)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("--port 必须在 1-65535 范围内，当前值: %d", cfg.Port)
	}

	// 敏感 flag 默认值为空，优先读取环境变量作为回退（避免 --help 泄露敏感信息）
	if cfg.ClaudeAPIKey == "" {
		cfg.ClaudeAPIKey = os.Getenv("DTWORKFLOW_CLAUDE_API_KEY")
	}
	if cfg.GiteaToken == "" {
		cfg.GiteaToken = os.Getenv("DTWORKFLOW_GITEA_TOKEN")
	}
	if cfg.WebhookSecret == "" {
		cfg.WebhookSecret = os.Getenv("DTWORKFLOW_WEBHOOK_SECRET")
	}
	if cfg.GiteaURL == "" {
		cfg.GiteaURL = os.Getenv("DTWORKFLOW_GITEA_URL")
	}

	if cfg.WebhookSecret == "" {
		return fmt.Errorf("webhook-secret 不能为空")
	}
	if cfg.ClaudeAPIKey == "" {
		return fmt.Errorf("claude-api-key 不能为空（通过 --claude-api-key 或 DTWORKFLOW_CLAUDE_API_KEY 环境变量设置）")
	}
	if cfg.GiteaURL == "" || cfg.GiteaToken == "" {
		slog.Warn("Gitea 配置不完整，通知功能将不可用",
			"gitea-url", cfg.GiteaURL != "",
			"gitea-token", cfg.GiteaToken != "")
	}

	// 构建所有依赖
	deps, cleanup, err := BuildServiceDeps(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, err any) {
		slog.Error("HTTP handler panic recovered", slog.Any("error", err), slog.String("path", c.Request.URL.Path))
		c.AbortWithStatus(http.StatusInternalServerError)
	}))

	// 健康检查（包含 Redis 和 SQLite 状态）
	router.GET("/healthz", func(c *gin.Context) {
		ctx := c.Request.Context()
		redisOK := deps.QueueClient.Ping(ctx) == nil
		poolStats := deps.Pool.Stats()
		sqliteOK := deps.Store.Ping(ctx) == nil

		status := "ok"
		httpStatus := http.StatusOK
		if !redisOK || !sqliteOK {
			status = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}
		c.JSON(httpStatus, gin.H{
			"status":         status,
			"version":        version,
			"redis":          redisOK,
			"sqlite":         sqliteOK,
			"active_workers": poolStats.Active,
		})
	})

	// 注册 Webhook 路由，注入 EnqueueHandler
	webhook.RegisterRoutes(router, webhook.Config{
		Secret:  cfg.WebhookSecret,
		Handler: deps.Handler,
	})

	// 启动 asynq Processor（消费端）
	processor := queue.NewProcessor(deps.Pool, deps.Store, slog.Default())
	mux := asynq.NewServeMux()
	mux.Handle(queue.AsynqTypeReviewPR, processor)
	mux.Handle(queue.AsynqTypeFixIssue, processor)
	mux.Handle(queue.AsynqTypeGenTests, processor)

	if err := deps.AsynqServer.Start(mux); err != nil {
		return fmt.Errorf("启动 asynq Server 失败: %w", err)
	}

	// 启动 Recovery Goroutine
	recoveryCtx, recoveryCancel := context.WithCancel(context.Background())
	go deps.Recovery.Run(recoveryCtx)

	// 启动容器 GC Goroutine
	gcCtx, gcCancel := context.WithCancel(context.Background())
	go deps.GC.Run(gcCtx)

	// 启动 HTTP 服务
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		recoveryCancel()
		gcCancel()
		return fmt.Errorf("监听地址 %s 失败: %w", addr, err)
	}

	server := &http.Server{
		Handler:           router,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP 服务异常退出", "error", err)
		}
	}()

	slog.Info("服务启动",
		"host", cfg.Host,
		"port", cfg.Port,
		"max_workers", cfg.MaxWorkers,
		"worker_image", cfg.WorkerImage,
		"redis", cfg.RedisAddr,
		"db", cfg.DBPath,
	)

	// 等待 stopCh 关闭 -> 分层关闭
	<-stopCh

	slog.Info("收到关闭信号，开始分层关闭...")
	return gracefulShutdown(server, deps, recoveryCancel, gcCancel)
}

// getEnvDefault 读取环境变量，若为空则返回默认值
func getEnvDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// gracefulShutdown 分层关闭所有组件。
// 注意：Store、QueueClient、Pool 由 defer cleanup() 统一关闭，
// 此处只负责关闭 HTTP Server 和 asynq Server，避免与 cleanup 双重关闭。
func gracefulShutdown(server *http.Server, deps *ServiceDeps, cancelRecovery, cancelGC context.CancelFunc) error {
	var firstErr error
	recordErr := func(layer string, err error) {
		slog.Error("关闭失败", "layer", layer, "error", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	// Layer 1: HTTP Server（5s）
	slog.Info("关闭 Layer 1: HTTP Server...")
	httpCtx, httpCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer httpCancel()
	if err := server.Shutdown(httpCtx); err != nil {
		recordErr("HTTP", err)
	}

	// Layer 2: asynq Processor（等待活跃 handler 完成）
	slog.Info("关闭 Layer 2: asynq Processor...")
	deps.AsynqServer.Shutdown()

	// Layer 3: Recovery goroutine + GC goroutine
	slog.Info("关闭 Layer 3: Recovery + GC goroutine...")
	cancelRecovery()
	cancelGC()

	// Layer 4-6（Pool、Store、QueueClient）由 runServe 中的 defer cleanup() 统一关闭。

	if firstErr != nil {
		return fmt.Errorf("关闭过程中出现错误: %w", firstErr)
	}
	slog.Info("HTTP Server 和 asynq Processor 已优雅关闭，其余资源将由 cleanup 释放")
	return nil
}
