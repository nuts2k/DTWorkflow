package gitea

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestListRepoPullRequests(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		writeJSON(w, loadFixture(t, "pull_requests.json"))
	})

	prs, _, err := client.ListRepoPullRequests(context.Background(), "owner", "repo", ListPullRequestsOptions{})
	if err != nil {
		t.Fatalf("ListRepoPullRequests 返回错误: %v", err)
	}
	if len(prs) != 2 {
		t.Errorf("返回 %d 个 PR，期望 2 个", len(prs))
	}
	if prs[0].Number != 42 {
		t.Errorf("PR[0].Number = %d, 期望 42", prs[0].Number)
	}
	if prs[1].Number != 43 {
		t.Errorf("PR[1].Number = %d, 期望 43", prs[1].Number)
	}
}

func TestListRepoPullRequests_WithOptions(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		if got := r.URL.Query().Get("state"); got != "closed" {
			t.Errorf("查询参数 state = %q, 期望 %q", got, "closed")
		}
		if got := r.URL.Query().Get("sort"); got != "oldest" {
			t.Errorf("查询参数 sort = %q, 期望 %q", got, "oldest")
		}
		writeJSON(w, []byte(`[]`))
	})

	opts := ListPullRequestsOptions{
		State: "closed",
		Sort:  "oldest",
	}
	_, _, err := client.ListRepoPullRequests(context.Background(), "owner", "repo", opts)
	if err != nil {
		t.Fatalf("ListRepoPullRequests 返回错误: %v", err)
	}
}

func TestGetPullRequest(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		writeJSON(w, loadFixture(t, "pull_request.json"))
	})

	pr, _, err := client.GetPullRequest(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("GetPullRequest 返回错误: %v", err)
	}
	if pr.Number != 42 {
		t.Errorf("Number = %d, 期望 42", pr.Number)
	}
	if pr.Title != "测试PR" {
		t.Errorf("Title = %q, 期望 %q", pr.Title, "测试PR")
	}
}

func TestGetPullRequest_NotFound(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/pulls/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, []byte(`{"message":"not found"}`))
	})

	_, _, err := client.GetPullRequest(context.Background(), "owner", "repo", 999)
	if err == nil {
		t.Fatal("期望返回错误，但没有")
	}
	if !IsNotFound(err) {
		t.Errorf("期望 IsNotFound 为 true，实际错误: %v", err)
	}
}

func TestGetPullRequestDiff(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/pulls/42.diff", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		if got := r.Header.Get("Accept"); got != "text/plain" {
			t.Errorf("Accept = %q, 期望 %q", got, "text/plain")
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("diff --git a/file.go b/file.go\n")) //nolint:errcheck
	})

	diff, _, err := client.GetPullRequestDiff(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("GetPullRequestDiff 返回错误: %v", err)
	}
	if diff != "diff --git a/file.go b/file.go\n" {
		t.Errorf("diff = %q, 期望 diff 文本", diff)
	}
}

func TestListPullRequestFiles(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/pulls/42/files", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		writeJSON(w, loadFixture(t, "changed_files.json"))
	})

	files, _, err := client.ListPullRequestFiles(context.Background(), "owner", "repo", 42, ListOptions{})
	if err != nil {
		t.Fatalf("ListPullRequestFiles 返回错误: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("返回 %d 个文件，期望 2 个", len(files))
	}
	if files[0].Filename != "main.go" {
		t.Errorf("files[0].Filename = %q, 期望 %q", files[0].Filename, "main.go")
	}
}

func TestCreatePullRequest(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "POST")
		testHeader(t, r, "Authorization", "token test-token")
		testHeader(t, r, "Content-Type", "application/json")

		var body CreatePullRequestOption
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析请求体失败: %v", err)
		}
		if body.Title != "新功能PR" {
			t.Errorf("body.Title = %q, 期望 %q", body.Title, "新功能PR")
		}
		if body.Head != "feature" {
			t.Errorf("body.Head = %q, 期望 %q", body.Head, "feature")
		}

		w.WriteHeader(http.StatusCreated)
		writeJSON(w, loadFixture(t, "pull_request.json"))
	})

	opts := CreatePullRequestOption{
		Title: "新功能PR",
		Body:  "新功能说明",
		Head:  "feature",
		Base:  "main",
	}
	pr, _, err := client.CreatePullRequest(context.Background(), "owner", "repo", opts)
	if err != nil {
		t.Fatalf("CreatePullRequest 返回错误: %v", err)
	}
	if pr.Number != 42 {
		t.Errorf("Number = %d, 期望 42", pr.Number)
	}
}

func TestCreatePullReview_WithInlineComments(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "POST")
		testHeader(t, r, "Authorization", "token test-token")
		testHeader(t, r, "Content-Type", "application/json")

		var body CreatePullReviewOptions
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析请求体失败: %v", err)
		}
		if body.State != ReviewStateComment {
			t.Errorf("body.State = %q, 期望 %q", body.State, ReviewStateComment)
		}
		if len(body.Comments) != 1 {
			t.Errorf("body.Comments 数量 = %d, 期望 1", len(body.Comments))
		}
		if body.Comments[0].Path != "main.go" {
			t.Errorf("body.Comments[0].Path = %q, 期望 %q", body.Comments[0].Path, "main.go")
		}

		w.WriteHeader(http.StatusOK)
		writeJSON(w, loadFixture(t, "pull_review.json"))
	})

	opts := CreatePullReviewOptions{
		State: ReviewStateComment,
		Body:  "总体 LGTM",
		Comments: []ReviewComment{
			{
				Path:       "main.go",
				Body:       "此处逻辑需要注意",
				NewLineNum: 10,
			},
		},
	}
	review, _, err := client.CreatePullReview(context.Background(), "owner", "repo", 42, opts)
	if err != nil {
		t.Fatalf("CreatePullReview 返回错误: %v", err)
	}
	if review.ID != 1 {
		t.Errorf("review.ID = %d, 期望 1", review.ID)
	}
}

func TestListPullReviewComments(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/pulls/42/reviews/1/comments", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		writeJSON(w, loadFixture(t, "comments.json"))
	})

	comments, _, err := client.ListPullReviewComments(context.Background(), "owner", "repo", 42, 1, ListOptions{})
	if err != nil {
		t.Fatalf("ListPullReviewComments 返回错误: %v", err)
	}
	if len(comments) != 2 {
		t.Errorf("返回 %d 个评论，期望 2 个", len(comments))
	}
	if comments[0].Body != "测试评论 1" {
		t.Errorf("comments[0].Body = %q, 期望 %q", comments[0].Body, "测试评论 1")
	}
}

func TestListPullRequestCommits(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/pulls/42/commits", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		w.Write([]byte(`[{"id":"abc123","url":"https://gitea.example.com/owner/repo/commit/abc123"},{"id":"def456","url":"https://gitea.example.com/owner/repo/commit/def456"}]`)) //nolint:errcheck
	})

	commits, _, err := client.ListPullRequestCommits(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("ListPullRequestCommits 返回错误: %v", err)
	}
	if len(commits) != 2 {
		t.Errorf("返回 %d 个提交，期望 2 个", len(commits))
	}
	if commits[0].ID != "abc123" {
		t.Errorf("commits[0].ID = %q, 期望 %q", commits[0].ID, "abc123")
	}
}
