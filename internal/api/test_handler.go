package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// genTestsRequest POST /api/v1/repos/:owner/:repo/gen-tests 的请求体。
// 所有字段可选：留空则走默认值（module=整仓、ref=仓库默认分支、framework 由服务端检测）。
type genTestsRequest struct {
	Module    string `json:"module,omitempty"`
	Ref       string `json:"ref,omitempty"`       // 可选：指定基准分支，留空时回退到仓库 default_branch
	Framework string `json:"framework,omitempty"` // 可选：显式声明测试框架（"junit5" / "vitest"）
}

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
	// Gin 的 ShouldBindJSON 在 body 为空时会返回 "EOF" 错误，因此仅在 Content-Length > 0
	// 时绑定请求体；Content-Length <= 0 直接走全默认路径。
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			Error(c, http.StatusBadRequest, ErrCodeBadRequest, "无效的请求体: "+err.Error())
			return
		}
	}

	// module 最小安全校验（防 ../ 与绝对路径），防止在 API 层就能构造越界路径。
	// ModuleScope 白名单 + path.Clean 归一化等完整校验交由 test.Service.validateModule
	// 在任务执行时处理（避免 API 层重复访问 config provider）。
	if err := validateGenTestsModule(req.Module); err != nil {
		Error(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	if err := validateGenTestsFramework(req.Framework); err != nil {
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

	// framework 不注入 payload：TaskPayload 目前无 Framework 字段，framework 偏好
	// 由 config.TestGen.TestFramework 和 test.Service.resolveFramework 解析。API 层
	// 仅做入口校验，保留字段以便 M4.2 扩展为请求级覆盖（届时需在 TaskPayload 增字段）。
	payload := model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		RepoOwner:    owner,
		RepoName:     repo,
		RepoFullName: repoInfo.FullName,
		CloneURL:     repoInfo.CloneURL,
		Module:       req.Module,
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

// validateGenTestsModule API 层的最小 module 校验，与 test.validateModule 的"绝对路径 / .."
// 规则对齐，但不做 ModuleScope 白名单与 path.Clean 归一化（由 test.Service 负责）。
// module 为空表示整仓模式，此层直接放行。
func validateGenTestsModule(module string) error {
	if module == "" {
		return nil
	}
	if strings.HasPrefix(module, "/") {
		return fmt.Errorf("module 不能为绝对路径: %q", module)
	}
	for _, seg := range strings.Split(module, "/") {
		if seg == ".." {
			return fmt.Errorf("module 不能包含 ..: %q", module)
		}
	}
	return nil
}

// validateGenTestsFramework framework 入口校验：仅允许空串 / "junit5" / "vitest"。
// 空串由 test.Service.resolveFramework 根据仓库结构与配置推断。
func validateGenTestsFramework(framework string) error {
	switch framework {
	case "", "junit5", "vitest":
		return nil
	default:
		return fmt.Errorf("framework 合法值为 \"junit5\" / \"vitest\"，当前值: %q", framework)
	}
}
