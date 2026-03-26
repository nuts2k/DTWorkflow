package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
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
	GiteaToken    string
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
		Image:        cfg.WorkerImage,
		CPULimit:     cfg.CPULimit,
		MemoryLimit:  cfg.MemoryLimit,
		GiteaURL:     cfg.GiteaURL,
		GiteaToken:   worker.SecretString(cfg.GiteaToken),
		ClaudeAPIKey:  worker.SecretString(cfg.ClaudeAPIKey),
		ClaudeBaseURL: cfg.ClaudeBaseURL,
		NetworkName:   cfg.NetworkName,
	}
	if cfg.AppCfg != nil {
		pcfg.GiteaInsecureSkipVerify = cfg.AppCfg.Gitea.InsecureSkipVerify
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

// buildNotifyRules / buildNotifier 属于 serve 命令的装配层逻辑，用于将统一配置入口中的 notify 配置
// 映射为运行时可用的 Router/Notifier。
//
// 注意：本任务仅完成“通知路由配置驱动”，不会扩展为完整 serve 配置迁移（Task 6）。
// buildNotifyRules 将配置中的路由规则映射为 notify.Router 可识别的规则结构。
//
// repoFullName 用于触发仓库级覆盖（ResolveNotifyRoutes）。
// 返回值 fallback 直接来自 cfg.Notify.DefaultChannel。
func buildNotifyRules(cfg *config.Config, repoFullName string) ([]notify.RoutingRule, string) {
	if cfg == nil {
		return nil, ""
	}

	routes := cfg.ResolveNotifyRoutes(repoFullName)
	rules := make([]notify.RoutingRule, 0, len(routes))
	for _, r := range routes {
		rule := notify.RoutingRule{
			RepoPattern: r.Repo,
			Channels:    append([]string(nil), r.Channels...),
		}
		if len(r.Events) > 0 {
			rule.EventTypes = make([]notify.EventType, 0, len(r.Events))
			for _, e := range r.Events {
				rule.EventTypes = append(rule.EventTypes, notify.EventType(e))
			}
		}
		rules = append(rules, rule)
	}

	return rules, cfg.Notify.DefaultChannel
}

type configDrivenNotifier struct {
	cfg           *config.Config
	giteaNotifier notify.Notifier
	logger        *slog.Logger

	// 对未声明仓库级 notify 覆盖的仓库，复用同一个全局 Router，避免按 repoFullName 无上限缓存。
	// 对显式声明了 repo.notify.routes 的仓库，才按仓库缓存 Router。
	// 说明：当前实现不支持配置热更新“即时生效”。即使 cfgManager 热加载更新了 cfg，
	// 已缓存的 router 也不会自动刷新；后续如需支持，需要引入显式的更新机制。
	mu           sync.Mutex
	globalRouter *notify.Router
	routers      map[string]*notify.Router
}

func (n *configDrivenNotifier) Send(ctx context.Context, msg notify.Message) error {
	if n == nil {
		return nil
	}
	repoFullName := msg.Target.Owner + "/" + msg.Target.Repo
	router, err := n.getRouter(repoFullName)
	if err != nil {
		return err
	}
	return router.Send(ctx, msg)
}

func (n *configDrivenNotifier) getRouter(repoFullName string) (*notify.Router, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.hasRepoNotifyOverride(repoFullName) {
		if n.globalRouter != nil {
			return n.globalRouter, nil
		}
		router, err := n.newRouter(repoFullName)
		if err != nil {
			return nil, err
		}
		n.globalRouter = router
		return router, nil
	}

	if n.routers == nil {
		n.routers = make(map[string]*notify.Router)
	}
	if r, ok := n.routers[repoFullName]; ok {
		return r, nil
	}

	router, err := n.newRouter(repoFullName)
	if err != nil {
		return nil, err
	}

	n.routers[repoFullName] = router
	return router, nil
}

func (n *configDrivenNotifier) hasRepoNotifyOverride(repoFullName string) bool {
	if n == nil || n.cfg == nil {
		return false
	}
	for _, repo := range n.cfg.Repos {
		if repo.Name == repoFullName && repo.Notify != nil && repo.Notify.Routes != nil {
			return true
		}
	}
	return false
}

func (n *configDrivenNotifier) newRouter(repoFullName string) (*notify.Router, error) {
	rules, fallback := buildNotifyRules(n.cfg, repoFullName)

	router, err := notify.NewRouter(
		notify.WithNotifier(n.giteaNotifier),
		notify.WithRules(rules),
		notify.WithFallback(fallback),
		notify.WithRouterLogger(n.logger),
	)
	if err != nil {
		return nil, fmt.Errorf("构造通知路由失败: %w", err)
	}
	return router, nil
}

// configAdapter 将 config.Manager 适配为 review.ConfigProvider 接口
type configAdapter struct {
	mgr *config.Manager
}

func (a *configAdapter) ResolveReviewConfig(repoFullName string) config.ReviewOverride {
	cfg := a.mgr.Get()
	if cfg == nil {
		return config.ReviewOverride{}
	}
	return cfg.ResolveReviewConfig(repoFullName)
}

func buildNotifier(cfg *config.Config, giteaClient *gitea.Client) (queue.TaskNotifier, error) {
	if cfg == nil || giteaClient == nil {
		return nil, nil
	}

	// 当前最小实现边界：仅支持 gitea 渠道。
	// 该约束已在 config.Validate 中统一校验；此处仅根据 gitea 渠道是否启用决定是否构造 Notifier。
	ch, ok := cfg.Notify.Channels["gitea"]
	if !ok || !ch.Enabled {
		return nil, nil
	}

	giteaNotifier, err := notify.NewGiteaNotifier(&giteaCommentAdapter{client: giteaClient}, notify.WithLogger(slog.Default()))
	if err != nil {
		return nil, fmt.Errorf("构造 GiteaNotifier 失败: %w", err)
	}

	return &configDrivenNotifier{
		cfg:           cfg,
		giteaNotifier: giteaNotifier,
		logger:        slog.Default(),
	}, nil
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
	// 为避免出现“日志提示可降级、实际又在更深层失败”的矛盾，这里做前置硬失败。
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
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
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

	// 6. 构建 EnqueueHandler
	handler := queue.NewEnqueueHandler(queueClient, s, slog.Default())

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

	// 启动 asynq Processor（消费端）
	var reviewOpts []queue.ProcessorOption
	if deps.GiteaClient != nil && cfgManager != nil {
		writer := review.NewWriter(deps.GiteaClient, deps.Store, slog.Default())
		reviewSvc := review.NewService(
			deps.GiteaClient,
			deps.Pool,
			&configAdapter{mgr: cfgManager},
			review.WithServiceLogger(slog.Default()),
			review.WithWriter(writer),
		)
		reviewOpts = append(reviewOpts, queue.WithReviewService(reviewSvc))
	}
	processor := queue.NewProcessor(deps.Pool, deps.Store, deps.Notifier, slog.Default(), reviewOpts...)
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
