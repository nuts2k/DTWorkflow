package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	e2esvc "otws19.zicp.vip/kelin/dtworkflow/internal/e2e"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/validation"
)

// e2eRequest POST /api/v1/repos/:owner/:repo/e2e 的请求体。
type e2eRequest struct {
	Module  string `json:"module,omitempty"`
	Case    string `json:"case,omitempty"`
	Env     string `json:"env,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	Ref     string `json:"ref,omitempty"`
}

const e2eMaxBodyBytes = 1 << 14

// triggerE2E 手动触发 run_e2e 任务入队。
func (h *handlers) triggerE2E(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")
	if owner == "" || repo == "" {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "owner 和 repo 不能为空")
		return
	}

	var req e2eRequest
	if c.Request.Body != nil {
		limited := http.MaxBytesReader(c.Writer, c.Request.Body, e2eMaxBodyBytes)
		body, err := io.ReadAll(limited)
		if err != nil {
			if strings.Contains(err.Error(), "request body too large") {
				Error(c, http.StatusRequestEntityTooLarge, ErrCodeBadRequest,
					fmt.Sprintf("请求体过大（上限 %d 字节）", e2eMaxBodyBytes))
				return
			}
			Error(c, http.StatusBadRequest, ErrCodeBadRequest, "读取请求体失败: "+err.Error())
			return
		}
		if len(bytes.TrimSpace(body)) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				Error(c, http.StatusBadRequest, ErrCodeBadRequest, "无效的请求体: "+err.Error())
				return
			}
		}
	}

	if req.Case != "" && req.Module == "" {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "指定 case 时必须同时指定 module")
		return
	}

	if err := validation.E2EModule(req.Module); err != nil {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "module "+err.Error())
		return
	}
	if err := validation.E2ECaseName(req.Case); err != nil {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "case "+err.Error())
		return
	}
	if err := validation.E2EBaseURL(req.BaseURL); err != nil {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "base_url "+err.Error())
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
	repoInfo, _, err := h.deps.GiteaClient.GetRepo(ctx, owner, repo)
	if err != nil {
		if gitea.IsNotFound(err) {
			Error(c, http.StatusNotFound, ErrCodeNotFound,
				fmt.Sprintf("仓库 %s/%s 不存在", owner, repo))
			return
		}
		h.deps.Logger.Error("查询 Gitea 仓库失败", "owner", owner, "repo", repo, "error", err)
		Error(c, http.StatusBadGateway, ErrCodeBadGateway, "查询 Gitea 仓库信息失败")
		return
	}

	identity, _ := c.Get(ContextKeyIdentity)
	triggeredBy := fmt.Sprintf("manual:%v", identity)

	baseRef := req.Ref
	if baseRef == "" {
		baseRef = repoInfo.DefaultBranch
	}

	payload := model.TaskPayload{
		TaskType:        model.TaskTypeRunE2E,
		RepoOwner:       owner,
		RepoName:        repo,
		RepoFullName:    repoInfo.FullName,
		CloneURL:        repoInfo.CloneURL,
		BaseRef:         baseRef,
		Module:          req.Module,
		CaseName:        req.Case,
		Environment:     req.Env,
		BaseURLOverride: req.BaseURL,
	}

	results, err := h.deps.EnqueueHandler.EnqueueManualE2E(ctx, payload, triggeredBy)
	if err != nil {
		if errors.Is(err, e2esvc.ErrNoE2EModulesFound) {
			Error(c, http.StatusUnprocessableEntity, ErrCodeBadRequest,
				"仓库中未发现 E2E 测试模块（需 e2e/{module}/cases/ 目录结构）")
			return
		}
		h.deps.Logger.Error("入队 e2e 失败",
			"owner", owner, "repo", repo, "error", err)
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "入队 e2e 失败")
		return
	}

	if len(results) == 1 {
		Success(c, http.StatusAccepted, gin.H{
			"task_id": results[0].TaskID,
			"repo":    repoInfo.FullName,
			"module":  results[0].Module,
			"env":     req.Env,
			"status":  "pending",
		})
		return
	}

	type taskInfo struct {
		TaskID string `json:"task_id"`
		Module string `json:"module"`
	}
	tasks := make([]taskInfo, 0, len(results))
	for _, r := range results {
		tasks = append(tasks, taskInfo{TaskID: r.TaskID, Module: r.Module})
	}
	Success(c, http.StatusAccepted, gin.H{
		"split": true,
		"tasks": tasks,
	})
}
