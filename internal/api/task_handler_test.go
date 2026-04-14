package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

func seedTask(s *mockStore, id string, taskType model.TaskType, status model.TaskStatus, repo string) {
	_ = s.CreateTask(context.Background(), &model.TaskRecord{
		ID:           id,
		TaskType:     taskType,
		Status:       status,
		RepoFullName: repo,
		TriggeredBy:  "webhook",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	})
}

func TestListTasks_Empty(t *testing.T) {
	s := newMockStore()
	r, w := setupTestRouter(t, s)

	req := authedRequest("GET", "/api/v1/tasks", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}

	data := parseData(t, w)
	items := data["items"].([]any)
	if len(items) != 0 {
		t.Errorf("期望空列表，实际 %d 条", len(items))
	}
}

func TestListTasks_WithFilters(t *testing.T) {
	s := newMockStore()
	seedTask(s, "t1", model.TaskTypeReviewPR, model.TaskStatusRunning, "org/repo-a")
	seedTask(s, "t2", model.TaskTypeFixIssue, model.TaskStatusFailed, "org/repo-b")
	seedTask(s, "t3", model.TaskTypeReviewPR, model.TaskStatusFailed, "org/repo-a")

	r, w := setupTestRouter(t, s)

	// 按 status 过滤
	req := authedRequest("GET", "/api/v1/tasks?status=failed", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}

	data := parseData(t, w)
	items := data["items"].([]any)
	if len(items) != 2 {
		t.Errorf("期望 2 条 failed 任务，实际 %d", len(items))
	}
}

func TestListTasks_InvalidStatus(t *testing.T) {
	s := newMockStore()
	r, w := setupTestRouter(t, s)

	req := authedRequest("GET", "/api/v1/tasks?status=invalid", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", w.Code)
	}
}

func TestListTasks_InvalidType(t *testing.T) {
	s := newMockStore()
	r, w := setupTestRouter(t, s)

	req := authedRequest("GET", "/api/v1/tasks?type=invalid", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", w.Code)
	}
}

func TestGetTask_Found(t *testing.T) {
	s := newMockStore()
	seedTask(s, "task-123", model.TaskTypeReviewPR, model.TaskStatusRunning, "org/repo")

	r, w := setupTestRouter(t, s)
	req := authedRequest("GET", "/api/v1/tasks/task-123", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}

	data := parseData(t, w)
	if data["id"] != "task-123" {
		t.Errorf("期望 id = \"task-123\"，实际 %v", data["id"])
	}
	if data["task_type"] != string(model.TaskTypeReviewPR) {
		t.Errorf("期望 task_type = %q，实际 %v", model.TaskTypeReviewPR, data["task_type"])
	}
}

func TestGetTask_NotFound(t *testing.T) {
	s := newMockStore()
	r, w := setupTestRouter(t, s)

	req := authedRequest("GET", "/api/v1/tasks/nonexist", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 404，实际 %d", w.Code)
	}
}

func TestRetryTask_Success(t *testing.T) {
	s := newMockStore()
	seedTask(s, "task-fail", model.TaskTypeReviewPR, model.TaskStatusFailed, "org/repo")

	r, w := setupTestRouter(t, s)
	req := authedRequest("POST", "/api/v1/tasks/task-fail/retry", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}

	// 验证任务已重置为 pending
	record, _ := s.GetTask(context.Background(), "task-fail")
	if record.Status != model.TaskStatusPending {
		t.Errorf("期望状态 pending，实际 %s", record.Status)
	}
	if record.Error != "" {
		t.Errorf("期望 error 被清空，实际 %q", record.Error)
	}
	if record.RetryCount != 0 {
		t.Errorf("期望 retry_count = 0，实际 %d", record.RetryCount)
	}
}

func TestRetryTask_Cancelled(t *testing.T) {
	s := newMockStore()
	seedTask(s, "task-cancel", model.TaskTypeFixIssue, model.TaskStatusCancelled, "org/repo")

	r, w := setupTestRouter(t, s)
	req := authedRequest("POST", "/api/v1/tasks/task-cancel/retry", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}
}

func TestRetryTask_NotFound(t *testing.T) {
	s := newMockStore()
	r, w := setupTestRouter(t, s)

	req := authedRequest("POST", "/api/v1/tasks/nonexist/retry", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 404，实际 %d", w.Code)
	}
}

func TestRetryTask_Conflict_RunningStatus(t *testing.T) {
	s := newMockStore()
	seedTask(s, "task-run", model.TaskTypeReviewPR, model.TaskStatusRunning, "org/repo")

	r, w := setupTestRouter(t, s)
	req := authedRequest("POST", "/api/v1/tasks/task-run/retry", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("期望 409，实际 %d", w.Code)
	}
}

func TestRetryTask_Conflict_SucceededStatus(t *testing.T) {
	s := newMockStore()
	seedTask(s, "task-ok", model.TaskTypeReviewPR, model.TaskStatusSucceeded, "org/repo")

	r, w := setupTestRouter(t, s)
	req := authedRequest("POST", "/api/v1/tasks/task-ok/retry", "")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("期望 409，实际 %d", w.Code)
	}
}

// parseData 从成功响应中提取 data 字段
func parseData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应中缺少 data 字段")
	}
	return data
}
