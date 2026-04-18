package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// ---------------------------------------------------------------
// 合法请求路径
// ---------------------------------------------------------------

func TestTriggerGenTests_Success_FullBody(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{})
	defer giteaSrv.Close()

	r, deps := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	body := `{"module": "backend/service", "ref": "develop", "framework": "junit5"}`
	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests", body)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("期望 202，实际 %d, body: %s", w.Code, w.Body.String())
	}
	data := parseData(t, w)
	if data["task_id"] == nil || data["task_id"] == "" {
		t.Errorf("期望 task_id 非空，实际 %v", data["task_id"])
	}
	if msg, _ := data["message"].(string); msg == "" {
		t.Errorf("期望 message 非空")
	}
	// 校验 Store 中确已创建 gen_tests 类型的 record，且 BaseRef/Module 注入正确
	store := deps.Store.(*mockStore)
	var found *model.TaskRecord
	for _, task := range store.tasks {
		if task.TaskType == model.TaskTypeGenTests {
			found = task
			break
		}
	}
	if found == nil {
		t.Fatal("应创建 gen_tests 任务 record")
	}
	if found.Payload.Module != "backend/service" {
		t.Errorf("Payload.Module = %q, 期望 backend/service", found.Payload.Module)
	}
	if found.Payload.Framework != "junit5" {
		t.Errorf("Payload.Framework = %q, 期望 junit5", found.Payload.Framework)
	}
	if found.Payload.BaseRef != "develop" {
		t.Errorf("Payload.BaseRef = %q, 期望 develop", found.Payload.BaseRef)
	}
}

func TestTriggerGenTests_Success_ChunkedBody(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{})
	defer giteaSrv.Close()

	r, deps := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/repos/owner/repo/gen-tests",
		strings.NewReader(`{"module":"backend/service","framework":"vitest"}`))
	req.ContentLength = -1 // 模拟 chunked 请求，无显式 Content-Length
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("期望 202，实际 %d, body: %s", w.Code, w.Body.String())
	}

	store := deps.Store.(*mockStore)
	var found *model.TaskRecord
	for _, task := range store.tasks {
		if task.TaskType == model.TaskTypeGenTests {
			found = task
			break
		}
	}
	if found == nil {
		t.Fatal("应创建 gen_tests 任务 record")
	}
	if found.Payload.Module != "backend/service" {
		t.Errorf("Payload.Module = %q, 期望 backend/service", found.Payload.Module)
	}
	if found.Payload.Framework != "vitest" {
		t.Errorf("Payload.Framework = %q, 期望 vitest", found.Payload.Framework)
	}
}

func TestTriggerGenTests_Success_EmptyBody(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{})
	defer giteaSrv.Close()

	r, deps := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	// 完全空 body → 走全部默认值（module=""、ref 回退到 default_branch="main"）
	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("期望 202，实际 %d, body: %s", w.Code, w.Body.String())
	}

	store := deps.Store.(*mockStore)
	var found *model.TaskRecord
	for _, task := range store.tasks {
		if task.TaskType == model.TaskTypeGenTests {
			found = task
			break
		}
	}
	if found == nil {
		t.Fatal("应创建 gen_tests 任务 record")
	}
	if found.Payload.Module != "" {
		t.Errorf("Payload.Module = %q, 期望空串（整仓模式）", found.Payload.Module)
	}
	// fakeGitea.default_branch = "main"
	if found.Payload.BaseRef != "main" {
		t.Errorf("Payload.BaseRef = %q, 期望 main（仓库默认分支）", found.Payload.BaseRef)
	}
}

// ---------------------------------------------------------------
// 认证 / 路径参数校验
// ---------------------------------------------------------------

func TestTriggerGenTests_MissingAuth(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	// 不带 Authorization header，应由 TokenAuth 中间件在 handler 之前拦截
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/repos/owner/repo/gen-tests",
		strings.NewReader(`{"module": "srv"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d, body: %s", w.Code, w.Body.String())
	}
}

// Gin 路由层面 owner/repo 为空会导致 404（路由不匹配）。这里通过绕过路由、
// 直接驱动 handler 的方式覆盖 "owner/repo 为空 → 400" 的分支逻辑。
func TestTriggerGenTests_EmptyOwnerRepo(t *testing.T) {
	// 采用自定义路由 :owner/:repo 都允许空的变体来触发 handler 内部校验
	r := gin.New()
	deps := Dependencies{
		Tokens:    testTokens(),
		Version:   "test",
		StartTime: time.Now(),
		Logger:    slog.Default(),
	}
	h := &handlers{deps: deps}
	v1 := r.Group("/api/v1", TokenAuth(deps.Tokens))
	// 故意用 * 通配以让 owner/repo 可为空字符串传入 handler
	v1.POST("/manual-empty/gen-tests", func(c *gin.Context) {
		c.Params = append(c.Params, gin.Param{Key: "owner", Value: ""},
			gin.Param{Key: "repo", Value: ""})
		h.triggerGenTests(c)
	})

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/manual-empty/gen-tests", `{}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d, body: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------
// 请求体校验
// ---------------------------------------------------------------

func TestTriggerGenTests_ModuleContainsDotDot(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests",
		`{"module": "../etc/passwd"}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", w.Code)
	}
	errMsg := readErrorMessage(t, w)
	if !strings.Contains(errMsg, "..") {
		t.Errorf("期望错误信息包含 \"..\"，实际: %s", errMsg)
	}
}

func TestTriggerGenTests_ModuleAbsolutePath(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests",
		`{"module": "/abs/path"}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", w.Code)
	}
	errMsg := readErrorMessage(t, w)
	if !strings.Contains(errMsg, "绝对路径") {
		t.Errorf("期望错误信息包含 \"绝对路径\"，实际: %s", errMsg)
	}
}

func TestTriggerGenTests_BodyTooLarge(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	// 构造 > 16 KiB 的 body：用一个超长 module 字段填充
	big := strings.Repeat("a", genTestsMaxBodyBytes+1)
	body := `{"module": "` + big + `"}`
	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests", body)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("期望 413，实际 %d, body: %s", w.Code, w.Body.String())
	}
	errMsg := readErrorMessage(t, w)
	if !strings.Contains(errMsg, "请求体过大") {
		t.Errorf("期望错误信息含 \"请求体过大\"，实际: %s", errMsg)
	}
}

func TestTriggerGenTests_FrameworkInvalid(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests",
		`{"framework": "go"}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", w.Code)
	}
	errMsg := readErrorMessage(t, w)
	if !strings.Contains(errMsg, `"junit5" / "vitest"`) {
		t.Errorf("期望错误信息含框架白名单提示，实际: %s", errMsg)
	}
}

// ---------------------------------------------------------------
// Gitea / Enqueue 依赖错误
// ---------------------------------------------------------------

func TestTriggerGenTests_RepoNotFound(t *testing.T) {
	giteaSrv := newFakeGiteaRepoError(http.StatusNotFound)
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests", `{}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 404，实际 %d, body: %s", w.Code, w.Body.String())
	}
}

func TestTriggerGenTests_RepoOtherError(t *testing.T) {
	giteaSrv := newFakeGiteaRepoError(http.StatusInternalServerError)
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests", `{}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("期望 502，实际 %d, body: %s", w.Code, w.Body.String())
	}
}

// EnqueueHandler 未配置（nil）时，handler 应返回 500 —— 覆盖 "入队服务未配置" 分支。
// 底层 asynq 入队失败不会返回错误（EnqueueHandler 降级为 pending，由 RecoveryLoop 兜底），
// 因此用 "依赖未注入" 的路径来验证 500 响应语义。
func TestTriggerGenTests_EnqueueHandlerNotConfigured(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{})
	defer giteaSrv.Close()

	gc, err := gitea.NewClient(giteaSrv.URL, gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("创建 Gitea 客户端失败: %v", err)
	}
	r := gin.New()
	deps := Dependencies{
		Store:       newMockStore(),
		GiteaClient: gc,
		// EnqueueHandler 故意为 nil
		Tokens:    testTokens(),
		Version:   "test",
		StartTime: time.Now(),
		Logger:    slog.Default(),
	}
	RegisterRoutes(r, deps)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests", `{"module": "srv"}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("期望 500，实际 %d, body: %s", w.Code, w.Body.String())
	}
}

// handler 缺少 GiteaClient 时返回 502 —— 覆盖 "Gitea 客户端未配置" 分支。
func TestTriggerGenTests_NoGiteaClient(t *testing.T) {
	r := gin.New()
	deps := Dependencies{
		Store:     newMockStore(),
		Tokens:    testTokens(),
		Version:   "test",
		StartTime: time.Now(),
		Logger:    slog.Default(),
		// GiteaClient 为 nil
	}
	RegisterRoutes(r, deps)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests", `{}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("期望 502，实际 %d", w.Code)
	}
}

// ---------------------------------------------------------------
// Cancel-and-Replace 入队层幂等：两次相同请求都应 202
// ---------------------------------------------------------------

func TestTriggerGenTests_CancelAndReplace_BothAccepted(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	body := `{"module": "srv"}`

	// 第一次入队
	w1 := httptest.NewRecorder()
	req1 := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests", body)
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("第一次期望 202，实际 %d, body: %s", w1.Code, w1.Body.String())
	}

	// 第二次立即触发同样 body：API 层仍应 202（Cancel-and-Replace 由入队层处理）
	w2 := httptest.NewRecorder()
	req2 := authedRequest("POST", "/api/v1/repos/owner/repo/gen-tests", body)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusAccepted {
		t.Fatalf("第二次期望 202，实际 %d, body: %s", w2.Code, w2.Body.String())
	}

	// 两次应得到不同的 task_id
	d1 := parseData(t, w1)
	d2 := parseData(t, w2)
	if d1["task_id"] == nil || d1["task_id"] == d2["task_id"] {
		t.Errorf("两次 task_id 不应相同：%v vs %v", d1["task_id"], d2["task_id"])
	}
}

// ---------------------------------------------------------------
// 测试辅助函数
// ---------------------------------------------------------------

// readErrorMessage 从错误响应中提取 error.message 字段
func readErrorMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v; body: %s", err, w.Body.String())
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("响应中缺少 error 对象: %s", w.Body.String())
	}
	msg, _ := errObj["message"].(string)
	return msg
}

// newFakeGiteaRepoError 返回一个 Gitea mock：/api/v1/repos/{o}/{r} 直接返回指定 HTTP 状态码。
// 用于覆盖 GetRepo 错误分支（404 / 5xx）。
func newFakeGiteaRepoError(statusCode int) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "simulated"})
	})
	return httptest.NewServer(mux)
}
