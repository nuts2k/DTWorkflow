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

func TestTriggerCodeFromDoc_RejectsEmptyCloneURL(t *testing.T) {
	giteaSrv := newFakeCodeFromDocRepo(map[string]any{
		"full_name":      "owner/repo",
		"clone_url":      "",
		"default_branch": "main",
		"owner":          map[string]any{"login": "owner"},
	})
	defer giteaSrv.Close()

	r, _ := setupCodeFromDocRouterWithGitea(t, giteaSrv.URL)
	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/code-from-doc", `{"doc_path":"docs/spec.md"}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("期望 502，实际 %d, body: %s", w.Code, w.Body.String())
	}
}

func TestTriggerCodeFromDoc_RejectsNullRepoInfo(t *testing.T) {
	giteaSrv := newFakeCodeFromDocRepo(nil)
	defer giteaSrv.Close()

	r, _ := setupCodeFromDocRouterWithGitea(t, giteaSrv.URL)
	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/code-from-doc", `{"doc_path":"docs/spec.md"}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("期望 502，实际 %d, body: %s", w.Code, w.Body.String())
	}
}

func newFakeCodeFromDocRepo(repo map[string]any) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if repo == nil {
			_, _ = w.Write([]byte("null"))
			return
		}
		_ = json.NewEncoder(w).Encode(repo)
	})
	return httptest.NewServer(mux)
}

func setupCodeFromDocRouterWithGitea(t *testing.T, giteaURL string) (*gin.Engine, Dependencies) {
	t.Helper()
	s := newMockStore()
	gc, err := gitea.NewClient(giteaURL, gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("创建 Gitea 客户端失败: %v", err)
	}

	r := gin.New()
	deps := Dependencies{
		Store:          s,
		GiteaClient:    gc,
		EnqueueHandler: newMockEnqueueHandler(s),
		Tokens:         testTokens(),
		Version:        "test",
		StartTime:      time.Now(),
		Logger:         slog.Default(),
	}
	RegisterRoutes(r, deps)
	return r, deps
}
