package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
)

func TestTriggerReview_MissingBody(t *testing.T) {
	r, deps := setupTriggerRouter(t)
	_ = deps

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/review-pr", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", w.Code)
	}
}

func TestTriggerReview_InvalidPRNumber(t *testing.T) {
	r, _ := setupTriggerRouter(t)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/review-pr", `{"pr_number": 0}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", w.Code)
	}
}

func TestTriggerReview_PRNotFound(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{prNotFound: true})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/review-pr", `{"pr_number": 999}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 404，实际 %d", w.Code)
	}
}

func TestTriggerReview_PRClosed(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{prState: "closed"})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/review-pr", `{"pr_number": 1}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("期望 409，实际 %d", w.Code)
	}
}

func TestTriggerReview_Success(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{prState: "open"})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/review-pr", `{"pr_number": 1}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("期望 202，实际 %d, body: %s", w.Code, w.Body.String())
	}

	data := parseData(t, w)
	if data["task_id"] == nil || data["task_id"] == "" {
		t.Errorf("期望 task_id 非空")
	}
}

func TestTriggerReview_NoGiteaClient(t *testing.T) {
	s := newMockStore()
	r := gin.New()
	deps := Dependencies{
		Store:     s,
		Tokens:    testTokens(),
		Version:   "test",
		StartTime: time.Now(),
		Logger:    slog.Default(),
		// GiteaClient 为 nil
	}
	RegisterRoutes(r, deps)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/review-pr", `{"pr_number": 1}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("期望 502，实际 %d", w.Code)
	}
}

// --- 辅助函数 ---

type fakeGiteaOpts struct {
	prState      string // PR state，默认 "open"
	prNotFound   bool
	issueState   string // Issue state，默认 "open"
	issueNotFound bool
}

func newFakeGitea(opts fakeGiteaOpts) *httptest.Server {
	mux := http.NewServeMux()

	// GET /api/v1/repos/{owner}/{repo}/pulls/{index}
	mux.HandleFunc("/api/v1/repos/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// 简单路由匹配
		path := r.URL.Path

		// PR 端点
		if matchPullRequest(path) {
			if opts.prNotFound {
				w.WriteHeader(404)
				json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
				return
			}
			state := opts.prState
			if state == "" {
				state = "open"
			}
			json.NewEncoder(w).Encode(map[string]any{
				"number": 1,
				"title":  "Test PR",
				"state":  state,
				"base":   map[string]any{"ref": "main", "sha": "abc123"},
				"head":   map[string]any{"ref": "feature", "sha": "def456"},
			})
			return
		}

		// Issue 端点
		if matchIssue(path) {
			if opts.issueNotFound {
				w.WriteHeader(404)
				json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
				return
			}
			state := opts.issueState
			if state == "" {
				state = "open"
			}
			json.NewEncoder(w).Encode(map[string]any{
				"number": 1,
				"title":  "Test Issue",
				"state":  state,
				"ref":    "main",
			})
			return
		}

		// Repo 端点 — 默认返回仓库信息
		json.NewEncoder(w).Encode(map[string]any{
			"full_name":      "owner/repo",
			"clone_url":      "https://gitea.example.com/owner/repo.git",
			"default_branch": "main",
			"owner":          map[string]any{"login": "owner"},
		})
	})

	return httptest.NewServer(mux)
}

func matchPullRequest(path string) bool {
	// 简单匹配 /api/v1/repos/{owner}/{repo}/pulls/{index}
	// 至少需要 7 段: "", "api", "v1", "repos", owner, repo, "pulls", index
	parts := splitPath(path)
	return len(parts) >= 8 && parts[6] == "pulls"
}

func matchIssue(path string) bool {
	parts := splitPath(path)
	return len(parts) >= 8 && parts[6] == "issues"
}

func splitPath(path string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			parts = append(parts, path[start:i])
			start = i + 1
		}
	}
	return parts
}

func setupTriggerRouter(t *testing.T) (*gin.Engine, Dependencies) {
	t.Helper()
	s := newMockStore()
	r := gin.New()
	deps := Dependencies{
		Store:     s,
		Tokens:    testTokens(),
		Version:   "test",
		StartTime: time.Now(),
		Logger:    slog.Default(),
	}
	RegisterRoutes(r, deps)
	return r, deps
}

func setupTriggerRouterWithGitea(t *testing.T, giteaURL string) (*gin.Engine, Dependencies) {
	t.Helper()
	s := newMockStore()
	gc, err := gitea.NewClient(giteaURL, gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("创建 Gitea 客户端失败: %v", err)
	}

	enqHandler := newMockEnqueueHandler(s)

	r := gin.New()
	deps := Dependencies{
		Store:          s,
		GiteaClient:    gc,
		EnqueueHandler: enqHandler,
		Tokens:         testTokens(),
		Version:        "test",
		StartTime:      time.Now(),
		Logger:         slog.Default(),
	}
	RegisterRoutes(r, deps)
	return r, deps
}
