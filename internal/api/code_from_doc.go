package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/code"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/validation"
)

// codeFromDocRequest POST /api/v1/repos/:owner/:repo/code-from-doc 的请求体。
type codeFromDocRequest struct {
	DocPath string `json:"doc_path"`
	Branch  string `json:"branch,omitempty"`
	Ref     string `json:"ref,omitempty"`
}

const codeFromDocMaxBodyBytes = 1 << 14 // 16 KiB

// triggerCodeFromDoc 手动触发 code_from_doc 任务入队。
func (h *handlers) triggerCodeFromDoc(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")
	if owner == "" || repo == "" {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "owner 和 repo 不能为空")
		return
	}

	var req codeFromDocRequest
	if c.Request.Body != nil {
		limited := http.MaxBytesReader(c.Writer, c.Request.Body, codeFromDocMaxBodyBytes)
		body, err := io.ReadAll(limited)
		if err != nil {
			if strings.Contains(err.Error(), "request body too large") {
				Error(c, http.StatusRequestEntityTooLarge, ErrCodeBadRequest,
					fmt.Sprintf("请求体过大（上限 %d 字节）", codeFromDocMaxBodyBytes))
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

	if err := validation.ValidateDocPath(req.DocPath); err != nil {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	branch := strings.TrimSpace(req.Branch)
	if err := validation.ValidateBranchRef(branch); err != nil {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
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

	docPath := validation.NormalizeDocPath(req.DocPath)
	docSlug := code.DocSlug(docPath)

	baseRef := req.Ref
	if baseRef == "" {
		baseRef = repoInfo.DefaultBranch
	}

	identity, _ := c.Get(ContextKeyIdentity)
	triggeredBy := fmt.Sprintf("manual:%v", identity)

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

	taskID, err := h.deps.EnqueueHandler.EnqueueCodeFromDoc(ctx, payload, triggeredBy)
	if err != nil {
		h.deps.Logger.Error("入队 code_from_doc 失败",
			"owner", owner, "repo", repo, "doc_path", docPath, "error", err)
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "入队 code_from_doc 失败")
		return
	}

	Success(c, http.StatusAccepted, gin.H{
		"task_id": taskID,
		"message": fmt.Sprintf("code_from_doc 任务已入队（doc_path=%q, ref=%q）", docPath, baseRef),
	})
}
