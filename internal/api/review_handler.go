package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

type reviewRequest struct {
	PRNumber int64 `json:"pr_number" binding:"required,min=1"`
}

func (h *handlers) triggerReview(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")
	if owner == "" || repo == "" {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "owner 和 repo 不能为空")
		return
	}

	var req reviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "无效的请求体: "+err.Error())
		return
	}

	if h.deps.GiteaClient == nil {
		Error(c, http.StatusBadGateway, ErrCodeBadGateway, "Gitea 客户端未配置")
		return
	}
	if h.deps.EnqueueHandler == nil {
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "入队服务未配置")
		return
	}

	ctx := c.Request.Context()

	// 查询 PR 信息并验证状态
	pr, _, err := h.deps.GiteaClient.GetPullRequest(ctx, owner, repo, req.PRNumber)
	if err != nil {
		if gitea.IsNotFound(err) {
			Error(c, http.StatusNotFound, ErrCodeNotFound,
				fmt.Sprintf("PR #%d 不存在", req.PRNumber))
			return
		}
		h.deps.Logger.Error("查询 Gitea PR 失败", "owner", owner, "repo", repo, "pr", req.PRNumber, "error", err)
		Error(c, http.StatusBadGateway, ErrCodeBadGateway, "查询 Gitea PR 失败")
		return
	}
	if pr.State != "open" {
		Error(c, http.StatusConflict, ErrCodeConflict,
			fmt.Sprintf("PR #%d 状态为 %s，仅支持评审 open 状态的 PR", req.PRNumber, pr.State))
		return
	}

	// 获取仓库信息（CloneURL）
	repoInfo, _, err := h.deps.GiteaClient.GetRepo(ctx, owner, repo)
	if err != nil {
		h.deps.Logger.Error("查询 Gitea 仓库失败", "owner", owner, "repo", repo, "error", err)
		Error(c, http.StatusBadGateway, ErrCodeBadGateway, "查询 Gitea 仓库信息失败")
		return
	}

	identity, _ := c.Get(ContextKeyIdentity)
	triggeredBy := fmt.Sprintf("manual:%v", identity)

	payload := model.TaskPayload{
		RepoOwner:    owner,
		RepoName:     repo,
		RepoFullName: repoInfo.FullName,
		CloneURL:     repoInfo.CloneURL,
		PRNumber:     pr.Number,
		PRTitle:      pr.Title,
		BaseRef:      pr.Base.Ref,
		HeadRef:      pr.Head.Ref,
		HeadSHA:      pr.Head.SHA,
	}

	taskID, err := h.deps.EnqueueHandler.EnqueueManualReview(ctx, payload, triggeredBy)
	if err != nil {
		h.deps.Logger.Error("入队 PR 评审失败", "owner", owner, "repo", repo, "pr", req.PRNumber, "error", err)
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "入队 PR 评审失败")
		return
	}

	Success(c, http.StatusAccepted, gin.H{
		"task_id": taskID,
		"message": fmt.Sprintf("PR #%d 评审任务已入队", req.PRNumber),
	})
}
