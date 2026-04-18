package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

// gen-tests 子命令的 flags（包级）
var (
	genTestsOwner     string
	genTestsRepo      string
	genTestsModule    string
	genTestsRef       string
	genTestsFramework string
)

var genTestsCmd = &cobra.Command{
	Use:   "gen-tests",
	Short: "手动触发 gen_tests 任务（本地入队）",
	Long: `手动触发仓库测试生成任务。

任务通过 SQLite 落盘 + asynq/Redis 入队，由 dtworkflow serve 对应的 Worker
消费执行。本命令依赖 gitea.url/token、redis.addr、database.path 等统一配置。`,
	RunE: runGenTests,
}

func init() {
	genTestsCmd.Flags().StringVar(&genTestsOwner, "owner", "", "仓库所有者（必填）")
	genTestsCmd.Flags().StringVar(&genTestsRepo, "repo", "", "仓库名（必填）")
	genTestsCmd.Flags().StringVar(&genTestsModule, "module", "", "目标模块路径（可选，留空为整仓生成）")
	genTestsCmd.Flags().StringVar(&genTestsRef, "ref", "", "基准分支（可选，留空用仓库默认分支）")
	genTestsCmd.Flags().StringVar(&genTestsFramework, "framework", "", `强制使用测试框架（可选，"junit5" / "vitest"）`)

	_ = genTestsCmd.MarkFlagRequired("owner")
	_ = genTestsCmd.MarkFlagRequired("repo")

	rootCmd.AddCommand(genTestsCmd)
}

// runGenTests 本地入队 gen_tests 任务。
//
// 依赖：SQLite + Redis + Gitea API（获取 CloneURL / 默认分支）。
// 装配思路复用 task 子命令：从 cfgManager 读取 database.path / redis.addr / gitea.url/token，
// 构造 store + queue.Client + EnqueueHandler，然后调用 EnqueueManualGenTests。
//
// 注意：与 task 子命令不同，本命令不在 PersistentPreRunE 中预初始化依赖，
// 而是在 RunE 内按需创建并在返回前清理，避免 `gen-tests --help` 等场景不必要地打开资源。
func runGenTests(cmd *cobra.Command, _ []string) error {
	// flag 校验（MarkFlagRequired 已处理缺失，但保留空白字符串判断）
	owner := strings.TrimSpace(genTestsOwner)
	repo := strings.TrimSpace(genTestsRepo)
	if owner == "" || repo == "" {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("--owner 与 --repo 必填")}
	}

	if err := validateGenTestsFrameworkFlag(genTestsFramework); err != nil {
		return &ExitCodeError{Code: 1, Err: err}
	}
	if err := validateGenTestsModuleFlag(genTestsModule); err != nil {
		return &ExitCodeError{Code: 1, Err: err}
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// 读取统一配置
	if cfgManager == nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("配置管理器未初始化")}
	}
	cfg := cfgManager.Get()
	if cfg == nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("读取统一配置失败")}
	}

	dbPath := strings.TrimSpace(cfg.Database.Path)
	if dbPath == "" {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("database.path 未配置")}
	}
	redisAddr := strings.TrimSpace(cfg.Redis.Addr)
	if redisAddr == "" {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("redis.addr 未配置")}
	}
	giteaURL := strings.TrimSpace(cfg.Gitea.URL)
	giteaToken := strings.TrimSpace(cfg.Gitea.Token)
	if giteaURL == "" || giteaToken == "" {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("gitea.url / gitea.token 未配置")}
	}

	// 1. 连接 SQLite
	s, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("打开 SQLite 失败: %w", err)}
	}
	defer func() { _ = s.Close() }()

	// 2. 构造 queue.Client（asynq + Redis）
	qClient, err := queue.NewClient(asynq.RedisClientOpt{
		Addr:     redisAddr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("创建 queue.Client 失败: %w", err)}
	}
	qClient.SetTimeouts(buildQueueTimeoutConfigFromAppConfig(cfg))
	defer func() { _ = qClient.Close() }()

	// 3. 构造 gitea.Client（查仓库默认分支与 CloneURL）
	//    gitea 包暴露的是 Functional Options：必须 WithToken，可选 WithHTTPClient 注入 InsecureSkipVerify。
	giteaOpts := []gitea.ClientOption{gitea.WithToken(giteaToken)}
	if cfg.Gitea.InsecureSkipVerify {
		giteaOpts = append(giteaOpts, gitea.WithHTTPClient(&http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12}, //nolint:gosec // 与 serve_deps.go 保持一致
			},
		}))
	}
	gc, err := gitea.NewClient(giteaURL, giteaOpts...)
	if err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("创建 Gitea Client 失败: %w", err)}
	}

	repoInfo, resp, err := gc.GetRepo(ctx, owner, repo)
	// gitea.Client.GetRepo 不会在 err != nil 时返回 *Response 的可读 Body，
	// 但按 client.go doRequest 的约定调用方无需关心 Body 的清理，此处忽略 resp。
	_ = resp
	if err != nil {
		if gitea.IsNotFound(err) {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("仓库 %s/%s 不存在", owner, repo)}
		}
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("查询仓库失败: %w", err)}
	}
	if repoInfo == nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("Gitea 返回仓库信息为空")}
	}
	if strings.TrimSpace(repoInfo.CloneURL) == "" {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("Gitea 返回 CloneURL 为空")}
	}

	baseRef := strings.TrimSpace(genTestsRef)
	if baseRef == "" {
		baseRef = repoInfo.DefaultBranch
	}

	// 4. 构造 EnqueueHandler
	//    qClient 同时实现 Enqueuer 与 TaskCanceller 接口（cancellation 能力可用）。
	handler := queue.NewEnqueueHandler(qClient, qClient, s, slog.Default())

	// 5. 生成 triggeredBy（CLI 本地触发）
	triggeredBy := buildGenTestsTriggeredBy()

	// 6. 构造 payload 并入队。
	//    注意：EnqueueManualGenTests 内部会强制设置 TaskType=gen_tests 并合成 DeliveryID。
	//    --framework 目前仅在人类可读输出中展示，Service 层按 test_gen.test_framework 解析，
	//    CLI 请求级框架覆盖为 M4.2 扩展项。
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		RepoOwner:    owner,
		RepoName:     repo,
		RepoFullName: repoInfo.FullName,
		CloneURL:     repoInfo.CloneURL,
		Module:       genTestsModule,
		BaseRef:      baseRef,
	}

	taskID, err := handler.EnqueueManualGenTests(ctx, payload, triggeredBy)
	if err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("入队失败: %w", err)}
	}

	printGenTestsResult(taskID, payload, baseRef, genTestsFramework)
	return nil
}

// printGenTestsResult 统一输出入队结果，复用 PrintResult 以自动处理 --json 模式。
func printGenTestsResult(taskID string, payload model.TaskPayload, baseRef, framework string) {
	data := map[string]any{
		"task_id":   taskID,
		"repo":      payload.RepoFullName,
		"module":    payload.Module,
		"ref":       baseRef,
		"framework": framework,
		"status":    "queued",
	}
	PrintResult(data, func(_ any) string {
		var sb strings.Builder
		sb.WriteString("gen_tests 任务已入队\n")
		fmt.Fprintf(&sb, "  task_id = %s\n", taskID)
		fmt.Fprintf(&sb, "  repo = %s\n", payload.RepoFullName)
		fmt.Fprintf(&sb, "  module = %q\n", payload.Module)
		fmt.Fprintf(&sb, "  ref = %s\n", baseRef)
		if framework != "" {
			fmt.Fprintf(&sb, "  framework = %s（显式指定，若与 test_gen.test_framework 冲突以请求级为准）\n", framework)
		}
		return sb.String()
	})
}

// buildGenTestsTriggeredBy 构造 CLI 触发者标识。
// 与 API handler 的 "manual:{identity}" 惯例对应，本地 CLI 使用 "cli:{hostname}"，
// 便于在 TaskRecord.TriggeredBy 中区分 webhook / REST API / 本地 CLI 三种来源。
func buildGenTestsTriggeredBy() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "local"
	}
	return fmt.Sprintf("cli:%s", hostname)
}

// validateGenTestsFrameworkFlag 校验 --framework 取值。
func validateGenTestsFrameworkFlag(framework string) error {
	switch framework {
	case "", "junit5", "vitest":
		return nil
	default:
		return fmt.Errorf(`--framework 合法值为 "junit5" / "vitest"，当前值: %q`, framework)
	}
}

// validateGenTestsModuleFlag 校验 --module 取值：
//   - 空字符串合法（全仓生成）；
//   - 禁止绝对路径与任何 `..` 段（防止越出 repo 根目录）。
func validateGenTestsModuleFlag(module string) error {
	if module == "" {
		return nil
	}
	if strings.HasPrefix(module, "/") {
		return fmt.Errorf("--module 不能为绝对路径: %q", module)
	}
	// 将反斜杠视为分隔符处理一下，避免 Windows 风格路径绕过校验。
	normalized := strings.ReplaceAll(module, `\`, "/")
	if normalized == ".." || strings.HasPrefix(normalized, "../") ||
		strings.HasSuffix(normalized, "/..") || strings.Contains(normalized, "/../") {
		return fmt.Errorf("--module 不能包含 ..: %q", module)
	}
	return nil
}
