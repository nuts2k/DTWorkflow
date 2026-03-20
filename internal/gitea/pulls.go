package gitea

import (
	"context"
	"fmt"
	"net/url"
)

// ListRepoPullRequests 列出仓库的 PR
// GET /api/v1/repos/{owner}/{repo}/pulls
func (c *Client) ListRepoPullRequests(ctx context.Context, owner, repo string, opts ListPullRequestsOptions) ([]*PullRequest, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls", owner, repo)
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
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d", owner, repo, index)
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
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d.diff", owner, repo, index)
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
func (c *Client) ListPullRequestFiles(ctx context.Context, owner, repo string, index int64) ([]*ChangedFile, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/files", owner, repo, index)
	req, err := c.newRequest(ctx, "GET", path, nil)
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
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls", owner, repo)
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

// CreatePullReview 创建 PR 评审
// POST /api/v1/repos/{owner}/{repo}/pulls/{index}/reviews
func (c *Client) CreatePullReview(ctx context.Context, owner, repo string, index int64, opts CreatePullReviewOptions) (*PullReview, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", owner, repo, index)
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
func (c *Client) ListPullReviewComments(ctx context.Context, owner, repo string, index int64, reviewID int64) ([]*Comment, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews/%d/comments", owner, repo, index, reviewID)
	req, err := c.newRequest(ctx, "GET", path, nil)
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
	path := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/commits", owner, repo, index)
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
