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

	"otws19.zicp.vip/kelin/dtworkflow/internal/code"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/validation"
)

var (
	codeFromDocOwner   string
	codeFromDocRepo    string
	codeFromDocDocPath string
	codeFromDocBranch  string
	codeFromDocRef     string
)

var codeFromDocCmd = &cobra.Command{
	Use:   "code-from-doc",
	Short: "手动触发文档驱动编码任务（本地入队）",
	Long: `手动触发文档驱动自动编码任务。

任务通过 SQLite 落盘 + asynq/Redis 入队，由 dtworkflow serve 对应的 Worker
消费执行。本命令依赖 gitea.url/token、redis.addr、database.path 等统一配置。`,
	RunE: runCodeFromDoc,
}

func init() {
	codeFromDocCmd.Flags().StringVar(&codeFromDocOwner, "owner", "", "仓库所有者（必填）")
	codeFromDocCmd.Flags().StringVar(&codeFromDocRepo, "repo", "", "仓库名（必填）")
	codeFromDocCmd.Flags().StringVar(&codeFromDocDocPath, "doc", "", "设计文档路径（必填）")
	codeFromDocCmd.Flags().StringVar(&codeFromDocBranch, "branch", "", "目标分支（省略则从 base ref 派生 auto-code/{slug}）")
	codeFromDocCmd.Flags().StringVar(&codeFromDocRef, "ref", "", "基础 ref（可选，留空用仓库默认分支）")

	_ = codeFromDocCmd.MarkFlagRequired("owner")
	_ = codeFromDocCmd.MarkFlagRequired("repo")
	_ = codeFromDocCmd.MarkFlagRequired("doc")

	rootCmd.AddCommand(codeFromDocCmd)
}

func runCodeFromDoc(cmd *cobra.Command, _ []string) error {
	owner := strings.TrimSpace(codeFromDocOwner)
	repo := strings.TrimSpace(codeFromDocRepo)
	docPath := strings.TrimSpace(codeFromDocDocPath)

	if owner == "" || repo == "" {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("--owner 与 --repo 必填")}
	}
	if err := validation.ValidateDocPath(docPath); err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("--doc %w", err)}
	}
	branch := strings.TrimSpace(codeFromDocBranch)
	if err := validation.ValidateBranchRef(branch); err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("--branch %w", err)}
	}

	docPath = validation.NormalizeDocPath(docPath)
	docSlug := code.DocSlug(docPath)

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
	// code_from_doc 使用 fix 账号（写权限）
	giteaToken := strings.TrimSpace(cfg.Gitea.FixToken())
	if giteaURL == "" || giteaToken == "" {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("gitea.url / gitea.token 未配置")}
	}

	s, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("打开 SQLite 失败: %w", err)}
	}
	defer func() { _ = s.Close() }()

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

	giteaOpts := []gitea.ClientOption{gitea.WithToken(giteaToken)}
	if cfg.Gitea.InsecureSkipVerify {
		giteaOpts = append(giteaOpts, gitea.WithHTTPClient(&http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12}, //nolint:gosec
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

	baseRef := strings.TrimSpace(codeFromDocRef)
	if err := validation.ValidateBaseRef(baseRef); err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("--ref %w", err)}
	}
	if baseRef == "" {
		baseRef = repoInfo.DefaultBranch
	}

	handler := queue.NewEnqueueHandler(qClient, qClient, s, slog.Default())
	triggeredBy := buildCLITriggeredBy()

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeCodeFromDoc,
		RepoOwner:    owner,
		RepoName:     repo,
		RepoFullName: repoInfo.FullName,
		CloneURL:     repoInfo.CloneURL,
		DocPath:      docPath,
		DocSlug:      docSlug,
		BaseRef:      baseRef,
		HeadRef:      branch,
	}

	taskID, err := handler.EnqueueCodeFromDoc(ctx, payload, triggeredBy)
	if err != nil {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("入队失败: %w", err)}
	}

	type codeFromDocResult struct {
		TaskID  string `json:"task_id"`
		Repo    string `json:"repo"`
		DocPath string `json:"doc_path"`
		Ref     string `json:"ref"`
		Branch  string `json:"branch,omitempty"`
		Status  string `json:"status"`
	}
	data := codeFromDocResult{
		TaskID:  taskID,
		Repo:    repoInfo.FullName,
		DocPath: docPath,
		Ref:     baseRef,
		Branch:  branch,
		Status:  "queued",
	}

	PrintResult(data, func(v any) string {
		r := v.(codeFromDocResult)
		var sb strings.Builder
		sb.WriteString("code_from_doc 任务已入队\n")
		fmt.Fprintf(&sb, "  task_id  = %s\n", r.TaskID)
		fmt.Fprintf(&sb, "  repo     = %s\n", r.Repo)
		fmt.Fprintf(&sb, "  doc_path = %s\n", r.DocPath)
		fmt.Fprintf(&sb, "  ref      = %s\n", r.Ref)
		if r.Branch != "" {
			fmt.Fprintf(&sb, "  branch   = %s\n", r.Branch)
		}
		return sb.String()
	})
	return nil
}
