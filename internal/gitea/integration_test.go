//go:build integration

package gitea

import (
	"context"
	"os"
	"strings"
	"testing"
)

func newIntegrationClient(t *testing.T) *Client {
	t.Helper()
	giteaURL := os.Getenv("GITEA_URL")
	giteaToken := os.Getenv("GITEA_TOKEN")
	if giteaURL == "" || giteaToken == "" {
		t.Skip("跳过集成测试：需设置 GITEA_URL 和 GITEA_TOKEN")
	}
	client, err := NewClient(giteaURL, WithToken(giteaToken))
	if err != nil {
		t.Fatalf("创建客户端: %v", err)
	}
	return client
}

func splitRepo(t *testing.T, fullName string) (string, string) {
	t.Helper()
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		t.Fatalf("无效的仓库名格式: %s（需要 owner/repo）", fullName)
	}
	return parts[0], parts[1]
}

func TestIntegration_GetRepo(t *testing.T) {
	client := newIntegrationClient(t)
	repoName := os.Getenv("GITEA_REPO")
	if repoName == "" {
		t.Skip("跳过集成测试：需设置 GITEA_REPO")
	}
	owner, repo := splitRepo(t, repoName)

	repository, _, err := client.GetRepo(context.Background(), owner, repo)
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if repository.FullName == "" {
		t.Error("期望 FullName 非空")
	}
	t.Logf("仓库: %s (默认分支: %s)", repository.FullName, repository.DefaultBranch)
}

func TestIntegration_ListRepoPullRequests(t *testing.T) {
	client := newIntegrationClient(t)
	repoName := os.Getenv("GITEA_REPO")
	if repoName == "" {
		t.Skip("跳过集成测试：需设置 GITEA_REPO")
	}
	owner, repo := splitRepo(t, repoName)

	prs, resp, err := client.ListRepoPullRequests(context.Background(), owner, repo, ListPullRequestsOptions{
		State: "open",
	})
	if err != nil {
		t.Fatalf("ListRepoPullRequests: %v", err)
	}
	t.Logf("PR 数量: %d (X-Total-Count: %d)", len(prs), resp.TotalCount)
}
