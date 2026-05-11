package gitea

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateIssue(t *testing.T) {
	var gotBody CreateIssueOption
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("期望 POST，实际 %s", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/owner/repo/issues" {
			t.Fatalf("路径错误: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("解析请求体: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(Issue{Number: 42, Title: gotBody.Title})
	}))
	defer ts.Close()

	c, _ := NewClient(ts.URL, WithToken("test-token"))
	issue, _, err := c.CreateIssue(context.Background(), "owner", "repo", CreateIssueOption{
		Title:  "[E2E] order/create-order - bug",
		Body:   "test body",
		Labels: []int64{1, 2},
	})
	if err != nil {
		t.Fatalf("CreateIssue 失败: %v", err)
	}
	if issue.Number != 42 {
		t.Errorf("期望 issue number=42，实际=%d", issue.Number)
	}
	if gotBody.Title != "[E2E] order/create-order - bug" {
		t.Errorf("请求 title 不匹配: %s", gotBody.Title)
	}
	if len(gotBody.Labels) != 2 {
		t.Errorf("期望 2 个标签，实际=%d", len(gotBody.Labels))
	}
}
