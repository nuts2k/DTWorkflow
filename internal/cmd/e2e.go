package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/validation"
)

// run-e2e 子命令的 flags（包级）
var (
	e2eOwner    string
	e2eRepo     string
	e2eModule   string
	e2eCase     string
	e2eEnv      string
	e2eBaseURL  string
	e2eRef      string
)

var runE2ECmd = &cobra.Command{
	Use:   "run-e2e",
	Short: "手动触发 run_e2e 任务（本地入队）",
	Long: `手动触发仓库 E2E 测试任务。

任务通过 SQLite 落盘 + asynq/Redis 入队，由 dtworkflow serve 对应的 Worker
消费执行。本命令依赖 gitea.url/token、redis.addr、database.path 等统一配置。`,
	RunE: runE2E,
}

func init() {
	runE2ECmd.Flags().StringVar(&e2eOwner, "owner", "", "仓库所有者（必填）")
	runE2ECmd.Flags().StringVar(&e2eRepo, "repo", "", "仓库名（必填）")
	runE2ECmd.Flags().StringVar(&e2eModule, "module", "", "指定测试模块（可选，留空为整仓）")
	runE2ECmd.Flags().StringVar(&e2eCase, "case", "", "单用例名称（可选）")
	runE2ECmd.Flags().StringVar(&e2eEnv, "env", "", "命名环境（如 staging / dev）")
	runE2ECmd.Flags().StringVar(&e2eBaseURL, "base-url", "", "临时覆盖 base_url（http/https）")
	runE2ECmd.Flags().StringVar(&e2eRef, "ref", "", "基准分支（可选，留空用仓库默认分支）")

	_ = runE2ECmd.MarkFlagRequired("owner")
	_ = runE2ECmd.MarkFlagRequired("repo")

	rootCmd.AddCommand(runE2ECmd)
}

func runE2E(cmd *cobra.Command, _ []string) error {
	owner := strings.TrimSpace(e2eOwner)
	repo := strings.TrimSpace(e2eRepo)
	if owner == "" || repo == "" {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("--owner 与 --repo 必填")}
	}

	if e2eCase != "" && e2eModule == "" {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("指定 --case 时必须同时指定 --module")}
	}

	if err := validation.E2EModule(e2eModule); err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("--module %w", err)}
	}
	if err := validation.E2ECaseName(e2eCase); err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("--case %w", err)}
	}
	if err := validation.E2EBaseURL(e2eBaseURL); err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("--base-url %w", err)}
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

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

	// 2. 构造 queue.Client
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

	// 3. 构造 gitea.Client（查询仓库 CloneURL + DefaultBranch）
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

	repoInfo, _, err := gc.GetRepo(ctx, owner, repo)
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

	baseRef := strings.TrimSpace(e2eRef)
	if baseRef == "" {
		baseRef = repoInfo.DefaultBranch
	}

	// 4. 构造 EnqueueHandler
	handler := queue.NewEnqueueHandler(qClient, qClient, s, slog.Default())

	// 5. 构造 payload 并入队
	payload := model.TaskPayload{
		TaskType:        model.TaskTypeRunE2E,
		RepoOwner:       owner,
		RepoName:        repo,
		RepoFullName:    repoInfo.FullName,
		CloneURL:        repoInfo.CloneURL,
		BaseRef:         baseRef,
		Module:          e2eModule,
		CaseName:        e2eCase,
		Environment:     strings.TrimSpace(e2eEnv),
		BaseURLOverride: e2eBaseURL,
	}

	taskID, err := handler.EnqueueManualE2E(ctx, payload, buildCLITriggeredBy())
	if err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("入队失败: %w", err)}
	}

	printE2EResult(taskID, payload)
	return nil
}

type e2eResult struct {
	TaskID      string `json:"task_id"`
	Repo        string `json:"repo"`
	Environment string `json:"environment,omitempty"`
	Module      string `json:"module,omitempty"`
	CaseName    string `json:"case,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Status      string `json:"status"`
}

func printE2EResult(taskID string, payload model.TaskPayload) {
	data := e2eResult{
		TaskID:      taskID,
		Repo:        payload.RepoFullName,
		Environment: payload.Environment,
		Module:      payload.Module,
		CaseName:    payload.CaseName,
		Ref:         payload.BaseRef,
		Status:      "queued",
	}
	PrintResult(data, func(v any) string {
		r := v.(e2eResult)
		var sb strings.Builder
		sb.WriteString("run_e2e 任务已入队\n")
		fmt.Fprintf(&sb, "  task_id = %s\n", r.TaskID)
		fmt.Fprintf(&sb, "  repo = %s\n", r.Repo)
		if r.Environment != "" {
			fmt.Fprintf(&sb, "  env = %s\n", r.Environment)
		}
		if r.Module != "" {
			fmt.Fprintf(&sb, "  module = %s\n", r.Module)
		}
		if r.CaseName != "" {
			fmt.Fprintf(&sb, "  case = %s\n", r.CaseName)
		}
		fmt.Fprintf(&sb, "  ref = %s\n", r.Ref)
		return sb.String()
	})
}
