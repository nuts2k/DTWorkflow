package gitea

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestListRepoIssues(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		writeJSON(w, loadFixture(t, "issues.json"))
	})

	issues, _, err := client.ListRepoIssues(context.Background(), "owner", "repo", ListIssueOptions{})
	if err != nil {
		t.Fatalf("ListRepoIssues 失败: %v", err)
	}
	if len(issues) != 2 {
		t.Errorf("期望 2 个 Issue, 得到 %d", len(issues))
	}
	if issues[0].Number != 1 {
		t.Errorf("Issue[0].Number = %d, 期望 1", issues[0].Number)
	}
	if issues[1].Number != 2 {
		t.Errorf("Issue[1].Number = %d, 期望 2", issues[1].Number)
	}
}

func TestListRepoIssues_WithOptions(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		q := r.URL.Query()
		if got := q.Get("state"); got != "closed" {
			t.Errorf("查询参数 state = %q, 期望 %q", got, "closed")
		}
		if got := q.Get("labels"); got != "bug" {
			t.Errorf("查询参数 labels = %q, 期望 %q", got, "bug")
		}
		if got := q.Get("type"); got != "issues" {
			t.Errorf("查询参数 type = %q, 期望 %q", got, "issues")
		}
		writeJSON(w, loadFixture(t, "issues.json"))
	})

	opts := ListIssueOptions{
		State:  "closed",
		Labels: "bug",
		Type:   "issues",
	}
	_, _, err := client.ListRepoIssues(context.Background(), "owner", "repo", opts)
	if err != nil {
		t.Fatalf("ListRepoIssues 失败: %v", err)
	}
}

func TestGetIssue(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		writeJSON(w, loadFixture(t, "issue.json"))
	})

	issue, resp, err := client.GetIssue(context.Background(), "owner", "repo", 1)
	if err != nil {
		t.Fatalf("GetIssue 失败: %v", err)
	}
	if issue.Number != 1 {
		t.Errorf("Issue.Number = %d, 期望 1", issue.Number)
	}
	if issue.Title != "测试Issue" {
		t.Errorf("Issue.Title = %q, 期望 %q", issue.Title, "测试Issue")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, 期望 200", resp.StatusCode)
	}
}

func TestGetIssue_NotFound(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/issues/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, []byte(`{"message":"issue not found"}`))
	})

	_, _, err := client.GetIssue(context.Background(), "owner", "repo", 999)
	if !IsNotFound(err) {
		t.Errorf("期望 NotFound 错误, 得到: %v", err)
	}
}

func TestListIssueComments(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		writeJSON(w, loadFixture(t, "comments.json"))
	})

	comments, _, err := client.ListIssueComments(context.Background(), "owner", "repo", 1, ListOptions{})
	if err != nil {
		t.Fatalf("ListIssueComments 失败: %v", err)
	}
	if len(comments) != 2 {
		t.Errorf("期望 2 个评论, 得到 %d", len(comments))
	}
	if comments[0].ID != 1 {
		t.Errorf("Comment[0].ID = %d, 期望 1", comments[0].ID)
	}
}

func TestListIssueComments_WithPagination(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		q := r.URL.Query()
		if got := q.Get("page"); got != "2" {
			t.Errorf("查询参数 page = %q, 期望 %q", got, "2")
		}
		if got := q.Get("limit"); got != "10" {
			t.Errorf("查询参数 limit = %q, 期望 %q", got, "10")
		}
		writeJSON(w, loadFixture(t, "comments.json"))
	})

	comments, _, err := client.ListIssueComments(context.Background(), "owner", "repo", 1, ListOptions{Page: 2, PageSize: 10})
	if err != nil {
		t.Fatalf("ListIssueComments 失败: %v", err)
	}
	if len(comments) != 2 {
		t.Errorf("期望 2 个评论, 得到 %d", len(comments))
	}
}

func TestCreateIssueComment(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "POST")
		testHeader(t, r, "Content-Type", "application/json")

		var opts CreateIssueCommentOption
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
			t.Errorf("解析请求体失败: %v", err)
		}
		if opts.Body != "新评论内容" {
			t.Errorf("请求体 body = %q, 期望 %q", opts.Body, "新评论内容")
		}

		w.WriteHeader(http.StatusCreated)
		writeJSON(w, loadFixture(t, "comment.json"))
	})

	comment, _, err := client.CreateIssueComment(context.Background(), "owner", "repo", 1, CreateIssueCommentOption{
		Body: "新评论内容",
	})
	if err != nil {
		t.Fatalf("CreateIssueComment 失败: %v", err)
	}
	if comment.ID != 1 {
		t.Errorf("Comment.ID = %d, 期望 1", comment.ID)
	}
}

func TestGetIssueLabels(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1/labels", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		writeJSON(w, loadFixture(t, "labels.json"))
	})

	labels, _, err := client.GetIssueLabels(context.Background(), "owner", "repo", 1)
	if err != nil {
		t.Fatalf("GetIssueLabels 失败: %v", err)
	}
	if len(labels) != 2 {
		t.Errorf("期望 2 个标签, 得到 %d", len(labels))
	}
	if labels[0].Name != "bug" {
		t.Errorf("Label[0].Name = %q, 期望 %q", labels[0].Name, "bug")
	}
}

func TestAddIssueLabels(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1/labels", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "POST")
		testHeader(t, r, "Content-Type", "application/json")

		var body struct {
			Labels []int64 `json:"labels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析请求体失败: %v", err)
		}
		if len(body.Labels) != 2 {
			t.Errorf("请求体 labels 长度 = %d, 期望 2", len(body.Labels))
		}
		if body.Labels[0] != 1 || body.Labels[1] != 2 {
			t.Errorf("请求体 labels = %v, 期望 [1 2]", body.Labels)
		}

		writeJSON(w, loadFixture(t, "labels.json"))
	})

	labels, _, err := client.AddIssueLabels(context.Background(), "owner", "repo", 1, []int64{1, 2})
	if err != nil {
		t.Fatalf("AddIssueLabels 失败: %v", err)
	}
	if len(labels) != 2 {
		t.Errorf("期望 2 个标签, 得到 %d", len(labels))
	}
}

func TestRemoveIssueLabel(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1/labels/1", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "DELETE")
		w.WriteHeader(http.StatusNoContent)
	})

	resp, err := client.RemoveIssueLabel(context.Background(), "owner", "repo", 1, 1)
	if err != nil {
		t.Fatalf("RemoveIssueLabel 失败: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("StatusCode = %d, 期望 204", resp.StatusCode)
	}
}

func TestRemoveIssueLabel_NotFound(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/issues/1/labels/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, []byte(`{"message":"label not found"}`))
	})

	_, err := client.RemoveIssueLabel(context.Background(), "owner", "repo", 1, 999)
	if !IsNotFound(err) {
		t.Errorf("期望 NotFound 错误, 得到: %v", err)
	}
}
