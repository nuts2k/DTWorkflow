package gitea

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestDeleteBranch(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/branches/feature", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "DELETE")
		testHeader(t, r, "Authorization", "token test-token")
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.DeleteBranch(context.Background(), "owner", "repo", "feature"); err != nil {
		t.Fatalf("DeleteBranch 返回错误: %v", err)
	}
}

func TestDeleteBranch_NotFound(t *testing.T) {
	// 分支已不存在时应返回 nil（幂等语义）
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/branches/deleted", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "DELETE")
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, []byte(`{"message":"branch not found"}`))
	})

	if err := client.DeleteBranch(context.Background(), "owner", "repo", "deleted"); err != nil {
		t.Fatalf("期望 nil，实际错误: %v", err)
	}
}

func TestDeleteBranch_Forbidden(t *testing.T) {
	// 权限不足（如受保护分支）应返回非 nil error
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/branches/main", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "DELETE")
		w.WriteHeader(http.StatusForbidden)
		writeJSON(w, []byte(`{"message":"cannot delete protected branch"}`))
	})

	err := client.DeleteBranch(context.Background(), "owner", "repo", "main")
	if err == nil {
		t.Fatal("期望返回错误，但没有")
	}
	if !IsForbidden(err) {
		t.Errorf("期望 IsForbidden 为 true，实际错误: %v", err)
	}
}

func TestDeleteBranch_EncodesSlashInName(t *testing.T) {
	// 分支名含 `/` 时需转义为 %2F，否则会被 Gitea 解析为嵌套路径段导致 404
	mux, client := setup(t)
	var gotEscapedPath string

	// Go 1.22+ ServeMux 将 %2F 视为路径段字面量，不会匹配含字面 `/` 的 exact 模式，
	// 因此用前缀模式兜底，再在 handler 内通过 r.URL.EscapedPath() 验证客户端发出的原始编码串。
	mux.HandleFunc("/api/v1/repos/owner/repo/branches/", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "DELETE")
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.DeleteBranch(context.Background(), "owner", "repo", "auto-test/srv-account"); err != nil {
		t.Fatalf("DeleteBranch 返回错误: %v", err)
	}

	wantSuffix := "/branches/auto-test%2Fsrv-account"
	if !strings.HasSuffix(gotEscapedPath, wantSuffix) {
		t.Errorf("escaped path = %q, 期望以 %q 结尾（分支名中的 / 必须被编码为 %%2F）", gotEscapedPath, wantSuffix)
	}
}
