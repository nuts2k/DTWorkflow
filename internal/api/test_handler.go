package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/validation"
)

// genTestsRequest POST /api/v1/repos/:owner/:repo/gen-tests 的请求体。
// 所有字段可选：留空则走默认值（module=整仓、ref=仓库默认分支、framework 由服务端检测）。
type genTestsRequest struct {
	Module    string `json:"module,omitempty"`
	Ref       string `json:"ref,omitempty"`       // 可选：指定基准分支，留空时回退到仓库 default_branch
	Framework string `json:"framework,omitempty"` // 可选：显式声明测试框架（"junit5" / "vitest"）
}

// genTestsMaxBodyBytes 限制 gen_tests POST 请求体大小。三字段 JSON 最多百字节数量级，
// 16 KiB 足以容纳任何合法输入并抵御客户端无意或恶意地推大 body 导致的内存压力。
// 对齐 webhook/receiver.go 的同名 MaxBytesReader 兜底风格（不过 webhook 体积更大故用 1MiB）。
const genTestsMaxBodyBytes = 1 << 14 // 16 KiB

// triggerGenTests 手动触发 gen_tests 任务入队。
// 对齐 triggerReview / triggerFix 风格：
//   - 认证由 TokenAuth 中间件统一处理
//   - 仓库存在性通过 GiteaClient.GetRepo 校验
//   - Cancel-and-Replace 由 EnqueueHandler.EnqueueManualGenTests 负责
func (h *handlers) triggerGenTests(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")
	if owner == "" || repo == "" {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "owner 和 repo 不能为空")
		return
	}

	var req genTestsRequest
	// gen_tests 允许空 body（使用全部默认值）。
	// 不能仅依赖 Content-Length：chunked 请求常为 -1，若据此跳过解析会把合法 body
	// 静默当成空请求。通过 http.MaxBytesReader 在读取阶段就截断超限 body，
	// 避免读取整个请求体至内存后再决定拒绝。
	if c.Request.Body != nil {
		limited := http.MaxBytesReader(c.Writer, c.Request.Body, genTestsMaxBodyBytes)
		body, err := io.ReadAll(limited)
		if err != nil {
			// MaxBytesReader 超限返回的 error.Error() 含 "http: request body too large"；
			// 以 413 响应更贴近语义，其他读取错误维持 400。
			if strings.Contains(err.Error(), "request body too large") {
				Error(c, http.StatusRequestEntityTooLarge, ErrCodeBadRequest,
					fmt.Sprintf("请求体过大（上限 %d 字节）", genTestsMaxBodyBytes))
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

	// module 最小安全校验（防 ../ 与绝对路径），防止在 API 层就能构造越界路径。
	// ModuleScope 白名单 + path.Clean 归一化等完整校验交由 test.Service.validateModule
	// 在任务执行时处理（避免 API 层重复访问 config provider）。
	if err := validation.GenTestsModule(req.Module); err != nil {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "module "+err.Error())
		return
	}
	if err := validation.GenTestsFramework(req.Framework); err != nil {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, "framework "+err.Error())
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

	// 查询仓库信息（CloneURL + DefaultBranch）；gen_tests 不要求任何 PR/Issue 实体存在。
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
		TaskType:     model.TaskTypeGenTests,
		RepoOwner:    owner,
		RepoName:     repo,
		RepoFullName: repoInfo.FullName,
		CloneURL:     repoInfo.CloneURL,
		Module:       req.Module,
		Framework:    req.Framework,
		BaseRef:      baseRef,
	}

	taskID, err := h.deps.EnqueueHandler.EnqueueManualGenTests(ctx, payload, triggeredBy)
	if err != nil {
		h.deps.Logger.Error("入队 gen_tests 失败",
			"owner", owner, "repo", repo, "module", req.Module, "error", err)
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "入队 gen_tests 失败")
		return
	}

	Success(c, http.StatusAccepted, gin.H{
		"task_id": taskID,
		"message": fmt.Sprintf("gen_tests 任务已入队（module=%q, ref=%q）", req.Module, baseRef),
	})
}

