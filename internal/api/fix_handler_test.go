package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

func TestTriggerFix_MissingBody(t *testing.T) {
	r, _ := setupTriggerRouter(t)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/fix-issue", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", w.Code)
	}
}

func TestTriggerFix_InvalidIssueNumber(t *testing.T) {
	r, _ := setupTriggerRouter(t)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/fix-issue", `{"issue_number": 0}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", w.Code)
	}
}

func TestTriggerFix_IssueNotFound(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{issueNotFound: true})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/fix-issue", `{"issue_number": 999}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 404，实际 %d", w.Code)
	}
}

func TestTriggerFix_IssueClosed(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{issueState: "closed"})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/fix-issue", `{"issue_number": 1}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("期望 409，实际 %d", w.Code)
	}
}

func TestTriggerFix_Success(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{issueState: "open"})
	defer giteaSrv.Close()

	r, deps := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/fix-issue", `{"issue_number": 1}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("期望 202，实际 %d, body: %s", w.Code, w.Body.String())
	}

	data := parseData(t, w)
	if data["task_id"] == nil || data["task_id"] == "" {
		t.Errorf("期望 task_id 非空")
	}
	store := deps.Store.(*mockStore)
	var found bool
	for _, task := range store.tasks {
		if task.TaskType == model.TaskTypeAnalyzeIssue {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("默认请求应创建 analyze_issue 任务")
	}
}

func TestTriggerFix_WithCustomRef(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{issueState: "open"})
	defer giteaSrv.Close()

	r, _ := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/fix-issue", `{"issue_number": 1, "ref": "develop"}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("期望 202，实际 %d, body: %s", w.Code, w.Body.String())
	}
}

func TestTriggerFix_WithFixTaskType(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{issueState: "open"})
	defer giteaSrv.Close()

	r, deps := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/fix-issue", `{"issue_number": 1, "task_type": "fix_issue"}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("期望 202，实际 %d, body: %s", w.Code, w.Body.String())
	}

	store := deps.Store.(*mockStore)
	var found bool
	for _, task := range store.tasks {
		if task.TaskType == model.TaskTypeFixIssue {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("显式 task_type=fix_issue 应创建 fix_issue 任务")
	}
}
