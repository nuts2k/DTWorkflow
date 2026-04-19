package gitea

import (
	"context"
	"fmt"
	"net/url"
)

// ListRepoPullRequests 列出仓库的 PR
// GET /api/v1/repos/{owner}/{repo}/pulls
func (c *Client) ListRepoPullRequests(ctx context.Context, owner, repo string, opts ListPullRequestsOptions) ([]*PullRequest, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls",
		url.PathEscape(owner), url.PathEscape(repo))
	params := url.Values{}
	if opts.State != "" {
		params.Set("state", opts.State)
	}
	if opts.Sort != "" {
		params.Set("sort", opts.Sort)
	}
	if opts.Page > 0 {
		params.Set("page", fmt.Sprintf("%d", opts.Page))
	}
	if opts.PageSize > 0 {
		params.Set("limit", fmt.Sprintf("%d", opts.PageSize))
	}

	req, err := c.newRequestWithQuery(ctx, "GET", path, params, nil)
	if err != nil {
		return nil, nil, err
	}

	var result []*PullRequest
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return result, resp, nil
}

// GetPullRequest 获取单个 PR
// GET /api/v1/repos/{owner}/{repo}/pulls/{index}
func (c *Client) GetPullRequest(ctx context.Context, owner, repo string, index int64) (*PullRequest, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d",
		url.PathEscape(owner), url.PathEscape(repo), index)
	req, err := c.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, nil, err
	}

	var result PullRequest
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return &result, resp, nil
}

// GetPullRequestDiff 获取 PR 的 diff 文本
// GET /api/v1/repos/{owner}/{repo}/pulls/{index}.diff
func (c *Client) GetPullRequestDiff(ctx context.Context, owner, repo string, index int64) (string, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d.diff",
		url.PathEscape(owner), url.PathEscape(repo), index)
	req, err := c.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "text/plain")

	data, resp, err := c.doRequestRaw(req)
	if err != nil {
		return "", resp, err
	}
	return string(data), resp, nil
}

// ListPullRequestFiles 列出 PR 变更的文件
// GET /api/v1/repos/{owner}/{repo}/pulls/{index}/files
func (c *Client) ListPullRequestFiles(ctx context.Context, owner, repo string, index int64, opts ListOptions) ([]*ChangedFile, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/files",
		url.PathEscape(owner), url.PathEscape(repo), index)

	params := url.Values{}
	if opts.Page > 0 {
		params.Set("page", fmt.Sprintf("%d", opts.Page))
	}
	if opts.PageSize > 0 {
		params.Set("limit", fmt.Sprintf("%d", opts.PageSize))
	}

	req, err := c.newRequestWithQuery(ctx, "GET", path, params, nil)
	if err != nil {
		return nil, nil, err
	}

	var result []*ChangedFile
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return result, resp, nil
}

// CreatePullRequest 创建 PR
// POST /api/v1/repos/{owner}/{repo}/pulls
func (c *Client) CreatePullRequest(ctx context.Context, owner, repo string, opts CreatePullRequestOption) (*PullRequest, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls",
		url.PathEscape(owner), url.PathEscape(repo))
	req, err := c.newRequest(ctx, "POST", path, opts)
	if err != nil {
		return nil, nil, err
	}

	var result PullRequest
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return &result, resp, nil
}

// ClosePullRequest 关闭指定 PR（state=closed），用于 Cancel-and-Replace 清理。
// Gitea API: PATCH /repos/{owner}/{repo}/pulls/{index} body {"state":"closed"}
//
// 幂等语义：
//   - 404（PR 不存在或分支已删）→ 返回 nil
//   - 200（包括原本已是 closed 状态的 PR）→ 返回 nil
//   - 403 / 5xx / 网络错误 → 返回非 nil error
func (c *Client) ClosePullRequest(ctx context.Context, owner, repo string, index int64) error {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d",
		url.PathEscape(owner), url.PathEscape(repo), index)
	body := map[string]string{"state": "closed"}
	req, err := c.newRequest(ctx, "PATCH", path, body)
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

// CreatePullReview 创建 PR 评审
// POST /api/v1/repos/{owner}/{repo}/pulls/{index}/reviews
func (c *Client) CreatePullReview(ctx context.Context, owner, repo string, index int64, opts CreatePullReviewOptions) (*PullReview, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews",
		url.PathEscape(owner), url.PathEscape(repo), index)
	req, err := c.newRequest(ctx, "POST", path, opts)
	if err != nil {
		return nil, nil, err
	}

	var result PullReview
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return &result, resp, nil
}

// ListPullReviewComments 列出 PR 评审的行级评论
// GET /api/v1/repos/{owner}/{repo}/pulls/{index}/reviews/{reviewID}/comments
func (c *Client) ListPullReviewComments(ctx context.Context, owner, repo string, index int64, reviewID int64, opts ListOptions) ([]*Comment, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews/%d/comments",
		url.PathEscape(owner), url.PathEscape(repo), index, reviewID)

	params := url.Values{}
	if opts.Page > 0 {
		params.Set("page", fmt.Sprintf("%d", opts.Page))
	}
	if opts.PageSize > 0 {
		params.Set("limit", fmt.Sprintf("%d", opts.PageSize))
	}

	req, err := c.newRequestWithQuery(ctx, "GET", path, params, nil)
	if err != nil {
		return nil, nil, err
	}

	var result []*Comment
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return result, resp, nil
}

// ListPullRequestCommits 列出 PR 的提交记录
// GET /api/v1/repos/{owner}/{repo}/pulls/{index}/commits
func (c *Client) ListPullRequestCommits(ctx context.Context, owner, repo string, index int64) ([]*Commit, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/commits",
		url.PathEscape(owner), url.PathEscape(repo), index)
	req, err := c.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, nil, err
	}

	var result []*Commit
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return result, resp, nil
}
