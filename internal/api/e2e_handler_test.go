package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

func TestTriggerE2E_SingleCaseResponseIncludesCase(t *testing.T) {
	giteaSrv := newFakeGitea(fakeGiteaOpts{})
	defer giteaSrv.Close()

	r, deps := setupTriggerRouterWithGitea(t, giteaSrv.URL)

	w := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/repos/owner/repo/e2e",
		`{"module": "order", "case": "create-order", "env": "staging"}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("期望 202，实际 %d, body: %s", w.Code, w.Body.String())
	}

	data := parseData(t, w)
	if data["case"] != "create-order" {
		t.Fatalf("case = %v, want create-order", data["case"])
	}
	if data["module"] != "order" {
		t.Fatalf("module = %v, want order", data["module"])
	}

	store := deps.Store.(*mockStore)
	var found *model.TaskRecord
	for _, task := range store.tasks {
		if task.TaskType == model.TaskTypeRunE2E {
			found = task
			break
		}
	}
	if found == nil {
		t.Fatal("应创建 run_e2e 任务 record")
	}
	if found.Payload.CaseName != "create-order" {
		t.Fatalf("Payload.CaseName = %q, want create-order", found.Payload.CaseName)
	}
}
