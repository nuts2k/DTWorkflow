package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func resetCodeFromDocFlags(t *testing.T) {
	t.Helper()
	prev := struct {
		repo, docPath, branch, ref string
		noWait                     bool
		timeout                    time.Duration
	}{
		repo: codeFromDocRepo, docPath: codeFromDocDocPath, branch: codeFromDocBranch, ref: codeFromDocRef,
		noWait: codeFromDocNoWait, timeout: codeFromDocTimeout,
	}
	codeFromDocRepo = ""
	codeFromDocDocPath = ""
	codeFromDocBranch = ""
	codeFromDocRef = ""
	codeFromDocNoWait = false
	codeFromDocTimeout = 0
	t.Cleanup(func() {
		codeFromDocRepo = prev.repo
		codeFromDocDocPath = prev.docPath
		codeFromDocBranch = prev.branch
		codeFromDocRef = prev.ref
		codeFromDocNoWait = prev.noWait
		codeFromDocTimeout = prev.timeout
	})
}

func TestCodeFromDocCmd_DefaultWaitTimeout(t *testing.T) {
	if defaultCodeFromDocWaitTimeout != 60*time.Minute {
		t.Fatalf("defaultCodeFromDocWaitTimeout = %s, want 60m", defaultCodeFromDocWaitTimeout)
	}
}

func TestCodeFromDocCmd_InvalidRefFlag(t *testing.T) {
	resetCodeFromDocFlags(t)

	codeFromDocRepo = "owner/repo"
	codeFromDocDocPath = "docs/spec.md"
	codeFromDocRef = "../main"
	codeFromDocNoWait = true

	err := codeFromDocCmd.RunE(codeFromDocCmd, nil)
	if err == nil {
		t.Fatal("期望 --ref 校验失败，实际 nil")
	}
	if !strings.Contains(err.Error(), "--ref") {
		t.Fatalf("错误应提及 --ref，实际: %v", err)
	}
}

func TestCodeFromDocCmd_PostsExpectedPathAndBody(t *testing.T) {
	resetCodeFromDocFlags(t)

	var capturedPath string
	var capturedMethod string
	var capturedBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		capturedBody = map[string]string{}
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("解析请求体失败: %v", err)
		}
		writeDataEnvelope(t, w, http.StatusAccepted, map[string]string{"task_id": "code-123"})
	}))
	defer srv.Close()

	restore := setupTestClient(t, srv.URL)
	defer restore()

	codeFromDocRepo = "alice/widgets"
	codeFromDocDocPath = `docs\payment-spec.md`
	codeFromDocBranch = "feature/payment"
	codeFromDocRef = "develop"
	codeFromDocNoWait = true

	codeFromDocCmd.SetContext(context.Background())
	if err := codeFromDocCmd.RunE(codeFromDocCmd, nil); err != nil {
		t.Fatalf("RunE 失败: %v", err)
	}

	if capturedMethod != http.MethodPost {
		t.Errorf("HTTP 方法 = %q，期望 POST", capturedMethod)
	}
	if capturedPath != "/api/v1/repos/alice/widgets/code-from-doc" {
		t.Errorf("HTTP 路径 = %q，期望 /api/v1/repos/alice/widgets/code-from-doc", capturedPath)
	}
	if got := capturedBody["doc_path"]; got != "docs/payment-spec.md" {
		t.Errorf("body.doc_path = %q，期望 docs/payment-spec.md", got)
	}
	if got := capturedBody["branch"]; got != "feature/payment" {
		t.Errorf("body.branch = %q，期望 feature/payment", got)
	}
	if got := capturedBody["ref"]; got != "develop" {
		t.Errorf("body.ref = %q，期望 develop", got)
	}
}
