package gitea

import (
	"context"
	"fmt"
	"net/url"
)

// DeleteBranch 删除仓库上的指定分支，用于 Cancel-and-Replace 清理阶段回收遗留分支。
// Gitea API: DELETE /repos/{owner}/{repo}/branches/{branch}
//
// 幂等语义：
//   - 404（分支不存在）→ 返回 nil
//   - 403 / 5xx / 网络错误 → 返回非 nil error
//
// 注意：branch 名可能包含 `/`（如 `auto-test/srv-account`），必须进行路径段 URL-encode，
// 否则 Gitea 会把斜杠当成下一级路径段从而返回 404。
func (c *Client) DeleteBranch(ctx context.Context, owner, repo, branch string) error {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/branches/%s",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(branch))
	req, err := c.newRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}
	if _, err := c.doRequest(req, nil); err != nil {
		if IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}
