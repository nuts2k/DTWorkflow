package api

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// Dependencies API 层所需的全部依赖
type Dependencies struct {
	Store          store.Store
	QueueClient    *queue.Client
	Enqueuer       queue.Enqueuer
	Pool           *worker.Pool
	EnqueueHandler *queue.EnqueueHandler
	GiteaClient    *gitea.Client
	Tokens         []config.TokenConfig
	Version        string
	StartTime      time.Time
	Logger         *slog.Logger
}

// RegisterRoutes 注册所有 REST API 路由。tokens 为空时跳过注册。
func RegisterRoutes(r *gin.Engine, deps Dependencies) {
	if len(deps.Tokens) == 0 {
		if deps.Logger != nil {
			deps.Logger.Info("api.tokens 未配置，跳过 REST API 注册")
		}
		return
	}

	v1 := r.Group("/api/v1", TokenAuth(deps.Tokens))

	h := &handlers{deps: deps}
	v1.GET("/status", h.getStatus)
	v1.GET("/tasks", h.listTasks)
	v1.GET("/tasks/:id", h.getTask)
	v1.POST("/tasks/:id/retry", h.retryTask)
	v1.POST("/repos/:owner/:repo/review-pr", h.triggerReview)
	v1.POST("/repos/:owner/:repo/fix-issue", h.triggerFix)
	v1.POST("/repos/:owner/:repo/gen-tests", h.triggerGenTests)
	v1.POST("/repos/:owner/:repo/e2e", h.triggerE2E)
	v1.POST("/repos/:owner/:repo/code-from-doc", h.triggerCodeFromDoc)
}

type handlers struct {
	deps Dependencies
}
