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

	"otws19.zicp.vip/kelin/dtworkflow/internal/api"
	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/fix"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/report"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	testgen "otws19.zicp.vip/kelin/dtworkflow/internal/test"
	"otws19.zicp.vip/kelin/dtworkflow/internal/webhook"
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
	serveClaudeBaseURL string
	serveGiteaURL      string
	serveGiteaToken    string
)

// serveConfig 封装 serve 命令的所有配置，避免测试直接修改包级全局变量
type serveConfig struct {
	Host          string
	Port          int
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	DBPath        string
	WebhookSecret string
	ClaudeAPIKey  string
	ClaudeBaseURL string
	GiteaURL      string
	GiteaToken    string // 基础 token（必填）：review/fix 专属 token 留空时使用
	// GiteaTokenReview 评审账号 token（review.Service / review.Writer / 只读 API / Gitea 通知）；
	// 空字符串表示回退到 GiteaToken。
	GiteaTokenReview string
	// GiteaTokenFix 修复账号 token（fix.Service 的 PRClient/IssueClient + 容器内 git push）；
	// 空字符串表示回退到 GiteaToken。
	GiteaTokenFix string
	// GiteaTokenGenTests 测试生成账号 token（gen_tests 容器内 git push 到 auto-test/* + host 侧创建 PR）；
	// 空字符串表示回退到 GiteaToken。
	GiteaTokenGenTests string
	MaxWorkers    int
	WorkerImage   string

	// 资源限制与运行网络（运行时快照）。
	CPULimit    string
	MemoryLimit string
	NetworkName string

	// AppCfg 用于承载统一配置入口的配置对象，供装配层按配置驱动方式构建组件。
	AppCfg *config.Config
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
		getEnvDefault("DTWORKFLOW_DATABASE_PATH", "data/dtworkflow.db"), "SQLite 数据库路径（也可通过 DTWORKFLOW_DATABASE_PATH 环境变量设置）")
	serveCmd.Flags().IntVar(&serveMaxWorkers, "max-workers", 3, "最大并发 Worker 数")
	serveCmd.Flags().StringVar(&serveWorkerImage, "worker-image", "dtworkflow-worker:1.0", "Worker Docker 镜像")
	serveCmd.Flags().StringVar(&serveClaudeAPIKey, "claude-api-key",
		"", "Claude API Key（也可通过 DTWORKFLOW_CLAUDE_API_KEY 环境变量设置）")
	serveCmd.Flags().StringVar(&serveClaudeBaseURL, "claude-base-url",
		"", "Claude API 代理地址（也可通过 DTWORKFLOW_CLAUDE_BASE_URL 环境变量设置）")
	serveCmd.Flags().StringVar(&serveGiteaURL, "gitea-url",
		"", "Gitea 实例地址（也可通过 DTWORKFLOW_GITEA_URL 环境变量设置）")
	serveCmd.Flags().StringVar(&serveGiteaToken, "gitea-token",
		"", "Gitea API Token（也可通过 DTWORKFLOW_GITEA_TOKEN 环境变量设置）")
	rootCmd.AddCommand(serveCmd)
}

// runServe 是 Cobra 命令入口：从统一配置入口构造运行时快照后，委托给 runServeWithConfig。
func runServe(cmd *cobra.Command, args []string) error {
	var cfg serveConfig
	var err error
	if cfgManager != nil {
		cfg, err = buildServeConfigFromManager(cfgManager)
		if err != nil {
			return err
		}
	} else {
		// 兜底：理论上 serve 命令总会在 root 的 PersistentPreRunE 中初始化 cfgManager。
		cfg = serveConfig{
			Host:          serveHost,
			Port:          servePort,
			RedisAddr:     serveRedisAddr,
			DBPath:        serveDBPath,
			WebhookSecret: serveWebhookSecret,
			ClaudeAPIKey:  serveClaudeAPIKey,
			ClaudeBaseURL: serveClaudeBaseURL,
			GiteaURL:      serveGiteaURL,
			GiteaToken:    serveGiteaToken,
			MaxWorkers:    serveMaxWorkers,
			WorkerImage:   serveWorkerImage,
		}
	}

	// 使用 OS 信号作为 stopCh
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return runServeWithConfig(cfg, ctx.Done())
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

	// 说明：敏感配置的 env 回填已由统一配置入口（Viper AutomaticEnv）处理。
	// serve 运行时不再手工读取 os.Getenv，避免形成“真实主来源”与“统一配置入口”并存的双入口。

	if cfg.WebhookSecret == "" {
		return fmt.Errorf("webhook-secret 不能为空")
	}
	if cfg.ClaudeAPIKey == "" {
		return fmt.Errorf("claude-api-key 不能为空（通过 --claude-api-key 或 DTWORKFLOW_CLAUDE_API_KEY 环境变量设置）")
	}

	// 构建所有依赖
	deps, cleanup, err := BuildServiceDeps(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, err any) {
		slog.Error("HTTP handler panic recovered", slog.Any("error", err), slog.String("path", c.Request.URL.Path))
		c.AbortWithStatus(http.StatusInternalServerError)
	}))

	// Liveness 探针：只要进程存活就返回 200，用于 Docker HEALTHCHECK
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "alive",
			"version": version,
		})
	})

	// Readiness 探针：检查执行任务所需的关键前提，缺失时返回 503
	router.GET("/readyz", func(c *gin.Context) {
		ctx := c.Request.Context()
		payload, httpStatus := computeReadyStatus(readinessSnapshot{
			RedisOK:            deps.QueueClient.Ping(ctx) == nil,
			SQLiteOK:           deps.Store.Ping(ctx) == nil,
			GiteaConfigured:    deps.GiteaConfigured,
			NotifierEnabled:    deps.NotifierEnabled,
			WorkerImagePresent: deps.WorkerImagePresent,
			ActiveWorkers:      deps.Pool.Stats().Active,
		})
		c.JSON(httpStatus, payload)
	})

	// 注册 Webhook 路由，注入 EnqueueHandler
	webhook.RegisterRoutes(router, webhook.Config{
		Secret:  cfg.WebhookSecret,
		Handler: deps.Handler,
	})

	// 注册 REST API 路由（Bearer Token 认证）
	var apiTokens []config.TokenConfig
	if cfg.AppCfg != nil {
		apiTokens = cfg.AppCfg.API.Tokens
	}
	api.RegisterRoutes(router, api.Dependencies{
		Store:          deps.Store,
		QueueClient:    deps.QueueClient,
		Enqueuer:       deps.QueueClient,
		Pool:           deps.Pool,
		EnqueueHandler: deps.EnqueueHandler,
		GiteaClient:    deps.GiteaClient,
		Tokens:         apiTokens,
		Version:        version,
		StartTime:      time.Now(),
		Logger:         slog.Default(),
	})

	// 启动 asynq Processor（消费端）
	var processorOpts []queue.ProcessorOption
	processorOpts = append(processorOpts, queue.WithGiteaBaseURL(cfg.GiteaURL))
	if deps.GiteaClient != nil && cfgManager != nil {
		cfgAdapter := &configAdapter{mgr: cfgManager}
		writer := review.NewWriter(deps.GiteaClient, deps.Store, deps.Store, slog.Default())
		reviewSvc := review.NewService(
			deps.GiteaClient,
			deps.Pool,
			cfgAdapter,
			review.WithServiceLogger(slog.Default()),
			review.WithWriter(writer),
		)
		processorOpts = append(processorOpts, queue.WithReviewService(reviewSvc))
		processorOpts = append(processorOpts, queue.WithReviewEnabledChecker(cfgAdapter))

		// M3.2: 装配 fix.Service
		// 使用修复账号（GiteaFixClient）：Issue 评论 + 创建 auto-fix PR + 读 ref；
		// 拆账号后，fix 创建的 PR 可以被 review 账号评审，规避 Gitea 自评审限制。
		fixSvc := fix.NewService(
			deps.GiteaFixClient,
			deps.Pool,
			fix.WithServiceLogger(slog.Default()),
			fix.WithConfigProvider(cfgAdapter),
			fix.WithRefClient(deps.GiteaFixClient),
			fix.WithPRClient(deps.GiteaFixClient),
			fix.WithFixStaleChecker(deps.Store), // M3.5: 前序分析"信息不足"检查
		)
		processorOpts = append(processorOpts, queue.WithFixService(fixSvc))

		// M4.2：test.Service 新增 ReviewEnqueuer + TestGenResultStore 两个可选依赖。
		// - ReviewEnqueuer：gen_tests Success（或 ReviewOnFailure=true 且失败）且 PRNumber>0 时
		//   主动 enqueue 一次 review。queue.EnqueueHandler.EnqueueManualReview 天然满足该接口，
		//   依赖方向 queue → test（接口定义在 internal/test，避免反向 import）。
		// - TestGenResultStore：两阶段 UPSERT 写 test_gen_results 表。*store.SQLiteStore 天然满足。
		testSvc := testgen.NewService(
			deps.GiteaClient,
			deps.Pool,
			cfgAdapter,
			testgen.WithServiceLogger(slog.Default()),
			testgen.WithPRClient(deps.GiteaClient),
			testgen.WithFileChecker(&giteaRepoFileChecker{client: deps.GiteaClient}),
			testgen.WithReviewEnqueuer(deps.EnqueueHandler),
			testgen.WithStore(deps.Store),
		)
		processorOpts = append(processorOpts, queue.WithTestService(testSvc))
	}
	processor := queue.NewProcessor(deps.Pool, deps.Store, deps.Notifier, slog.Default(), processorOpts...)
	mux := asynq.NewServeMux()
	mux.Handle(queue.AsynqTypeReviewPR, processor)
	mux.Handle(queue.AsynqTypeAnalyzeIssue, processor) // M3.4
	mux.Handle(queue.AsynqTypeFixIssue, processor)
	mux.Handle(queue.AsynqTypeGenTests, processor)

	// M2.7: 每日报告 Handler 装配
	var dailyReportHandler *report.DailyReportHandler
	if cfg.AppCfg != nil && cfg.AppCfg.DailyReport.Enabled {
		drCfg := cfg.AppCfg.DailyReport
		reportSender, reportErr := report.NewReportFeishuSender(drCfg.FeishuWebhook, drCfg.FeishuSecret)
		if reportErr != nil {
			return fmt.Errorf("初始化每日报告飞书发送器失败: %w", reportErr)
		}
		reportCollector := report.NewReviewStatCollector(deps.Store)
		reportGen := report.NewReportGenerator(reportCollector, reportSender, drCfg.Timezone, drCfg.SkipEmpty)
		dailyReportHandler = report.NewDailyReportHandler(deps.Store, reportGen)
		mux.Handle(queue.AsynqTypeGenDailyReport, dailyReportHandler)
	}

	if err := deps.AsynqServer.Start(mux); err != nil {
		return fmt.Errorf("启动 asynq Server 失败: %w", err)
	}

	// M2.7: 启动每日报告 Scheduler
	var reportScheduler *asynq.Scheduler
	if dailyReportHandler != nil {
		drCfg := cfg.AppCfg.DailyReport
		loc, _ := time.LoadLocation(drCfg.Timezone) // 已在 Validate 校验
		redisOpt := asynq.RedisClientOpt{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB}
		reportScheduler = asynq.NewScheduler(redisOpt, &asynq.SchedulerOpts{
			Location: loc,
		})
		entryID, schedErr := reportScheduler.Register(drCfg.Cron, newDailyReportTask())
		if schedErr != nil {
			return fmt.Errorf("注册每日报告定时任务失败: %w", schedErr)
		}
		slog.Info("每日报告定时任务已注册", "cron", drCfg.Cron, "timezone", drCfg.Timezone, "entry_id", entryID)

		go func() {
			if runErr := reportScheduler.Run(); runErr != nil {
				slog.Error("每日报告 Scheduler 异常退出", "error", runErr)
			}
		}()
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

	// M2.7: 关闭 Scheduler（在 gracefulShutdown 之前，确保不再入队新任务）
	if reportScheduler != nil {
		reportScheduler.Shutdown()
		slog.Info("每日报告 Scheduler 已关闭")
	}

	slog.Info("收到关闭信号，开始分层关闭...")
	return gracefulShutdown(server, deps, recoveryCancel, gcCancel)
}

// getEnvDefault 读取环境变量，若为空则返回默认值。
// 旧环境变量兼容统一由 internal/config.WithEnvPrefix 处理。
func getEnvDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// gracefulShutdown 分层关闭所有组件。
// 注意：Store、QueueClient、Pool 由 defer cleanup() 统一关闭，
// 此处只负责关闭 HTTP Server 和 asynq Server，避免与 cleanup 双重关闭。
// 总超时 60 秒，防止关闭流程无限阻塞。
func gracefulShutdown(server *http.Server, deps *ServiceDeps, cancelRecovery, cancelGC context.CancelFunc) error {
	// 设置总超时保护，确保关闭流程不会无限阻塞
	totalCtx, totalCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer totalCancel()

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- doGracefulShutdown(server, deps, cancelRecovery, cancelGC)
	}()

	select {
	case err := <-doneCh:
		return err
	case <-totalCtx.Done():
		// 总超时到达，强制取消 Recovery 和 GC 协程
		cancelRecovery()
		cancelGC()
		return fmt.Errorf("优雅关闭超时（60s），强制退出")
	}
}

// doGracefulShutdown 执行实际的分层关闭逻辑
func doGracefulShutdown(server *http.Server, deps *ServiceDeps, cancelRecovery, cancelGC context.CancelFunc) error {
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

	// 停止配置管理器的热加载监听
	if cfgManager != nil {
		cfgManager.Stop()
	}

	if firstErr != nil {
		return fmt.Errorf("关闭过程中出现错误: %w", firstErr)
	}
	slog.Info("HTTP Server 和 asynq Processor 已优雅关闭，其余资源将由 cleanup 释放")
	return nil
}
