package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

type fixRequest struct {
	IssueNumber int64  `json:"issue_number" binding:"required,min=1"`
	Ref         string `json:"ref,omitempty"`      // 可选：指定修复的基准分支
	TaskType    string `json:"task_type,omitempty"` // M3.4: analyze_issue（默认）或 fix_issue
}

func (h *handlers) triggerFix(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")
	if owner == "" || repo == "" {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "owner 和 repo 不能为空")
		return
	}

	var req fixRequest
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

	// 查询 Issue 信息并验证状态
	issue, _, err := h.deps.GiteaClient.GetIssue(ctx, owner, repo, req.IssueNumber)
	if err != nil {
		if gitea.IsNotFound(err) {
			Error(c, http.StatusNotFound, ErrCodeNotFound,
				fmt.Sprintf("Issue #%d 不存在", req.IssueNumber))
			return
		}
		h.deps.Logger.Error("查询 Gitea Issue 失败", "owner", owner, "repo", repo, "issue", req.IssueNumber, "error", err)
		Error(c, http.StatusBadGateway, ErrCodeBadGateway, "查询 Gitea Issue 失败")
		return
	}
	if issue.State != "open" {
		Error(c, http.StatusConflict, ErrCodeConflict,
			fmt.Sprintf("Issue #%d 状态为 %s，仅支持修复 open 状态的 Issue", req.IssueNumber, issue.State))
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

	issueRef := req.Ref
	if issueRef == "" {
		issueRef = repoInfo.DefaultBranch
	}

	// M3.4: 解析 task_type，默认 analyze_issue
	taskType := model.TaskTypeAnalyzeIssue
	if req.TaskType == string(model.TaskTypeFixIssue) {
		taskType = model.TaskTypeFixIssue
	}

	payload := model.TaskPayload{
		TaskType:     taskType,
		RepoOwner:    owner,
		RepoName:     repo,
		RepoFullName: repoInfo.FullName,
		CloneURL:     repoInfo.CloneURL,
		IssueNumber:  issue.Number,
		IssueTitle:   issue.Title,
		IssueRef:     issueRef,
	}

	taskID, err := h.deps.EnqueueHandler.EnqueueManualFix(ctx, payload, triggeredBy)
	if err != nil {
		h.deps.Logger.Error("入队 Issue 修复失败", "owner", owner, "repo", repo, "issue", req.IssueNumber, "error", err)
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "入队 Issue 修复失败")
		return
	}

	taskLabel := "分析"
	if taskType == model.TaskTypeFixIssue {
		taskLabel = "修复"
	}
	Success(c, http.StatusAccepted, gin.H{
		"task_id": taskID,
		"message": fmt.Sprintf("Issue #%d %s任务已入队", req.IssueNumber, taskLabel),
	})
}
