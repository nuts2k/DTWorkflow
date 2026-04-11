package gitea

import (
	"context"
	"net/http"
	"testing"
)

func TestGetRepo(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		writeJSON(w, loadFixture(t, "repo.json"))
	})

	repo, _, err := client.GetRepo(context.Background(), "owner", "repo")
	if err != nil {
		t.Fatalf("GetRepo 返回错误: %v", err)
	}
	if repo.FullName != "owner/repo" {
		t.Errorf("FullName = %q, 期望 %q", repo.FullName, "owner/repo")
	}
	if repo.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, 期望 %q", repo.DefaultBranch, "main")
	}
}

func TestGetRepo_NotFound(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/notexist", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, []byte(`{"message":"not found"}`))
	})

	_, _, err := client.GetRepo(context.Background(), "owner", "notexist")
	if err == nil {
		t.Fatal("期望返回错误，但没有")
	}
	if !IsNotFound(err) {
		t.Errorf("期望 IsNotFound 为 true，实际错误: %v", err)
	}
}

func TestGetBranch(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/branches/main", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		writeJSON(w, loadFixture(t, "branch.json"))
	})

	branch, _, err := client.GetBranch(context.Background(), "owner", "repo", "main")
	if err != nil {
		t.Fatalf("GetBranch 返回错误: %v", err)
	}
	if branch.Name != "main" {
		t.Errorf("Name = %q, 期望 %q", branch.Name, "main")
	}
	if branch.Commit.ID != "abc123" {
		t.Errorf("Commit.ID = %q, 期望 %q", branch.Commit.ID, "abc123")
	}
}

func TestGetBranch_NotFound(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/branches/notexist", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, []byte(`{"message":"not found"}`))
	})

	_, _, err := client.GetBranch(context.Background(), "owner", "repo", "notexist")
	if err == nil {
		t.Fatal("期望返回错误，但没有")
	}
	if !IsNotFound(err) {
		t.Errorf("期望 IsNotFound 为 true，实际错误: %v", err)
	}
}

func TestCreateBranch(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/branches", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "POST")
		testHeader(t, r, "Authorization", "token test-token")
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, []byte(`{"name":"feature","commit":{"id":"def456","url":"https://gitea.example.com/owner/repo/commit/def456"}}`))
	})

	opts := CreateBranchOption{
		BranchName:    "feature",
		OldBranchName: "main",
	}
	branch, _, err := client.CreateBranch(context.Background(), "owner", "repo", opts)
	if err != nil {
		t.Fatalf("CreateBranch 返回错误: %v", err)
	}
	if branch.Name != "feature" {
		t.Errorf("Name = %q, 期望 %q", branch.Name, "feature")
	}
}

func TestCreateBranch_Conflict(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/branches", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, []byte(`{"message":"branch already exists"}`))
	})

	opts := CreateBranchOption{BranchName: "main"}
	_, _, err := client.CreateBranch(context.Background(), "owner", "repo", opts)
	if err == nil {
		t.Fatal("期望返回错误，但没有")
	}
	if !IsConflict(err) {
		t.Errorf("期望 IsConflict 为 true，实际错误: %v", err)
	}
}

func TestGetContents(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/contents/README.md", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		if got := r.URL.Query().Get("ref"); got != "main" {
			t.Errorf("查询参数 ref = %q, 期望 %q", got, "main")
		}
		writeJSON(w, loadFixture(t, "contents.json"))
	})

	contents, _, err := client.GetContents(context.Background(), "owner", "repo", "README.md", "main")
	if err != nil {
		t.Fatalf("GetContents 返回错误: %v", err)
	}
	if contents.Name != "README.md" {
		t.Errorf("Name = %q, 期望 %q", contents.Name, "README.md")
	}
	if contents.SHA != "abc123def456" {
		t.Errorf("SHA = %q, 期望 %q", contents.SHA, "abc123def456")
	}
}

func TestGetContents_NotFound(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/contents/notexist.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, []byte(`{"message":"not found"}`))
	})

	_, _, err := client.GetContents(context.Background(), "owner", "repo", "notexist.md", "")
	if err == nil {
		t.Fatal("期望返回错误，但没有")
	}
	if !IsNotFound(err) {
		t.Errorf("期望 IsNotFound 为 true，实际错误: %v", err)
	}
}

func TestGetFileContent(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/raw/README.md", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		if got := r.URL.Query().Get("ref"); got != "main" {
			t.Errorf("查询参数 ref = %q, 期望 %q", got, "main")
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Hello World"))
	})

	data, _, err := client.GetFileContent(context.Background(), "owner", "repo", "README.md", "main")
	if err != nil {
		t.Fatalf("GetFileContent 返回错误: %v", err)
	}
	if string(data) != "Hello World" {
		t.Errorf("内容 = %q, 期望 %q", string(data), "Hello World")
	}
}

func TestGetFileContent_NotFound(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/raw/notexist.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, []byte(`{"message":"not found"}`))
	})

	_, _, err := client.GetFileContent(context.Background(), "owner", "repo", "notexist.md", "")
	if err == nil {
		t.Fatal("期望返回错误，但没有")
	}
	if !IsNotFound(err) {
		t.Errorf("期望 IsNotFound 为 true，实际错误: %v", err)
	}
}

func TestGetTag(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/tags/v1.0.0", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		testHeader(t, r, "Authorization", "token test-token")
		writeJSON(w, loadFixture(t, "tag.json"))
	})

	tag, _, err := client.GetTag(context.Background(), "owner", "repo", "v1.0.0")
	if err != nil {
		t.Fatalf("GetTag 返回错误: %v", err)
	}
	if tag.Name != "v1.0.0" {
		t.Errorf("Name = %q, 期望 %q", tag.Name, "v1.0.0")
	}
}

func TestGetTag_NotFound(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/tags/notexist", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, []byte(`{"message":"not found"}`))
	})

	_, _, err := client.GetTag(context.Background(), "owner", "repo", "notexist")
	if err == nil {
		t.Fatal("期望返回错误，但没有")
	}
	if !IsNotFound(err) {
		t.Errorf("期望 IsNotFound 为 true，实际错误: %v", err)
	}
}
