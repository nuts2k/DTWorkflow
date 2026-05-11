package gitea

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListRepoLabels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("期望 GET，实际 %s", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/owner/repo/labels" {
			t.Fatalf("路径错误: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode([]Label{
			{ID: 1, Name: "bug", Color: "#d73a4a"},
			{ID: 2, Name: "fix-to-pr", Color: "#0e8a16"},
		})
	}))
	defer ts.Close()

	c, _ := NewClient(ts.URL, WithToken("test-token"))
	labels, _, err := c.ListRepoLabels(context.Background(), "owner", "repo")
	if err != nil {
		t.Fatalf("ListRepoLabels 失败: %v", err)
	}
	if len(labels) != 2 {
		t.Fatalf("期望 2 个标签，实际=%d", len(labels))
	}
	if labels[1].Name != "fix-to-pr" {
		t.Errorf("期望标签名 fix-to-pr，实际=%s", labels[1].Name)
	}
}
