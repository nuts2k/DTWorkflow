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

// serve 命令的 CLI flags
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

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "启动 HTTP 服务（API + Webhook 接收器 + 任务执行引擎）",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().StringVar(&serveHost, "host", "0.0.0.0", "监听地址")
	serveCmd.Flags().IntVar(&servePort, "port", 8080, "监听端口")
	serveCmd.Flags().StringVar(&serveWebhookSecret, "webhook-secret",
		os.Getenv("DTWORKFLOW_WEBHOOK_SECRET"), "Webhook Secret（也可通过 DTWORKFLOW_WEBHOOK_SECRET 环境变量设置）")
	serveCmd.Flags().StringVar(&serveRedisAddr, "redis-addr", "localhost:6379", "Redis 地址")
	serveCmd.Flags().StringVar(&serveDBPath, "db-path", "data/dtworkflow.db", "SQLite 数据库路径")
	serveCmd.Flags().IntVar(&serveMaxWorkers, "max-workers", 3, "最大并发 Worker 数")
	serveCmd.Flags().StringVar(&serveWorkerImage, "worker-image", "dtworkflow-worker:1.0", "Worker Docker 镜像")
	serveCmd.Flags().StringVar(&serveClaudeAPIKey, "claude-api-key",
		os.Getenv("DTWORKFLOW_CLAUDE_API_KEY"), "Claude API Key（也可通过 DTWORKFLOW_CLAUDE_API_KEY 环境变量设置）")
	serveCmd.Flags().StringVar(&serveGiteaURL, "gitea-url", "", "Gitea 实例地址")
	serveCmd.Flags().StringVar(&serveGiteaToken, "gitea-token",
		os.Getenv("DTWORKFLOW_GITEA_TOKEN"), "Gitea API Token（也可通过 DTWORKFLOW_GITEA_TOKEN 环境变量设置）")
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

// BuildServiceDeps 从 CLI flags 构建所有依赖，返回 ServiceDeps 和清理函数
func BuildServiceDeps() (*ServiceDeps, func(), error) {
	var cleanups []func()
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	// 1. 初始化 SQLite Store
	s, err := store.NewSQLiteStore(serveDBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("初始化 SQLite 失败: %w", err)
	}
	cleanups = append(cleanups, func() { _ = s.Close() })

	// 2. 初始化 Gitea Client
	var giteaClient *gitea.Client
	if serveGiteaURL != "" && serveGiteaToken != "" {
		giteaClient, err = gitea.NewClient(serveGiteaURL, gitea.WithToken(serveGiteaToken))
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("初始化 Gitea Client 失败: %w", err)
		}
	}

	// 3. 初始化 asynq Client（用于入队）
	redisOpt := asynq.RedisClientOpt{Addr: serveRedisAddr}
	queueClient, err := queue.NewClient(redisOpt)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("初始化 asynq Client 失败: %w", err)
	}
	cleanups = append(cleanups, func() { _ = queueClient.Close() })

	// 4. 初始化 Docker Client 和 Worker Pool
	dockerClient, err := worker.NewDockerClient()
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("初始化 Docker Client 失败: %w", err)
	}
	cleanups = append(cleanups, func() { _ = dockerClient.Close() })

	// TODO: 后续通过 CLI flags 或 Viper 配置文件暴露以下配置
	pool := worker.NewPool(worker.PoolConfig{
		Image:        serveWorkerImage,
		CPULimit:     "2.0",
		MemoryLimit:  "4g",
		GiteaURL:     serveGiteaURL,
		GiteaToken:   serveGiteaToken,
		ClaudeAPIKey: serveClaudeAPIKey,
		NetworkName:  "dtworkflow-net",
	}, dockerClient)
	cleanups = append(cleanups, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = pool.Shutdown(ctx)
	})

	// 5. 初始化 asynq Server（并发由此控制）
	asynqServer := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: serveMaxWorkers,
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

// runServe 启动 HTTP 服务，注册路由，并支持优雅关闭
func runServe(cmd *cobra.Command, args []string) error {
	if serveWebhookSecret == "" {
		return fmt.Errorf("webhook-secret 不能为空")
	}
	if serveClaudeAPIKey == "" {
		return fmt.Errorf("claude-api-key 不能为空（通过 --claude-api-key 或 DTWORKFLOW_CLAUDE_API_KEY 环境变量设置）")
	}
	if serveGiteaURL == "" || serveGiteaToken == "" {
		slog.Warn("Gitea 配置不完整，通知功能将不可用",
			"gitea-url", serveGiteaURL != "",
			"gitea-token", serveGiteaToken != "")
	}

	// 构建所有依赖
	deps, cleanup, err := BuildServiceDeps()
	if err != nil {
		return err
	}
	defer cleanup()

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	// storePinger 是一个可选接口，Store 实现了此接口时可用于健康检查
	type storePinger interface {
		Ping(ctx context.Context) error
	}

	// 健康检查（包含 Redis 和 SQLite 状态）
	router.GET("/healthz", func(c *gin.Context) {
		ctx := c.Request.Context()
		redisOK := deps.QueueClient.Ping(ctx) == nil
		poolStats := deps.Pool.Stats()

		sqliteOK := true
		if pinger, ok := deps.Store.(storePinger); ok {
			sqliteOK = pinger.Ping(ctx) == nil
		}

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
		Secret:  serveWebhookSecret,
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
	defer recoveryCancel()
	go deps.Recovery.Run(recoveryCtx)

	// 启动容器 GC Goroutine
	gcCtx, gcCancel := context.WithCancel(context.Background())
	defer gcCancel()
	go deps.GC.Run(gcCtx)

	// 启动 HTTP 服务
	addr := fmt.Sprintf("%s:%d", serveHost, servePort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听地址 %s 失败: %w", addr, err)
	}

	server := &http.Server{Handler: router, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP 服务异常退出", "error", err)
		}
	}()

	slog.Info("服务启动",
		"host", serveHost,
		"port", servePort,
		"max_workers", serveMaxWorkers,
		"worker_image", serveWorkerImage,
		"redis", serveRedisAddr,
		"db", serveDBPath,
	)

	// 等待信号 -> 分层关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	signal.Stop(quit)

	slog.Info("收到关闭信号，开始分层关闭...")
	return gracefulShutdown(server, deps, recoveryCancel, gcCancel)
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
