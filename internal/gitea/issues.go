package gitea

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ListRepoIssues 列出仓库的 Issue
func (c *Client) ListRepoIssues(ctx context.Context, owner, repo string, opts ListIssueOptions) ([]*Issue, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues",
		url.PathEscape(owner), url.PathEscape(repo))

	params := url.Values{}
	if opts.Page > 0 {
		params.Set("page", fmt.Sprintf("%d", opts.Page))
	}
	if opts.PageSize > 0 {
		params.Set("limit", fmt.Sprintf("%d", opts.PageSize))
	}
	if opts.State != "" {
		params.Set("state", opts.State)
	}
	if opts.Labels != "" {
		params.Set("labels", opts.Labels)
	}
	if opts.Type != "" {
		params.Set("type", opts.Type)
	}

	req, err := c.newRequestWithQuery(ctx, http.MethodGet, path, params, nil)
	if err != nil {
		return nil, nil, err
	}

	var issues []*Issue
	resp, err := c.doRequest(req, &issues)
	if err != nil {
		return nil, resp, err
	}
	return issues, resp, nil
}

// GetIssue 获取 Issue 详情
func (c *Client) GetIssue(ctx context.Context, owner, repo string, index int64) (*Issue, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d",
		url.PathEscape(owner), url.PathEscape(repo), index)
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}

	var issue Issue
	resp, err := c.doRequest(req, &issue)
	if err != nil {
		return nil, resp, err
	}
	return &issue, resp, nil
}

// ListIssueComments 列出 Issue 的评论
func (c *Client) ListIssueComments(ctx context.Context, owner, repo string, index int64, opts ListOptions) ([]*Comment, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments",
		url.PathEscape(owner), url.PathEscape(repo), index)

	params := url.Values{}
	if opts.Page > 0 {
		params.Set("page", fmt.Sprintf("%d", opts.Page))
	}
	if opts.PageSize > 0 {
		params.Set("limit", fmt.Sprintf("%d", opts.PageSize))
	}

	req, err := c.newRequestWithQuery(ctx, http.MethodGet, path, params, nil)
	if err != nil {
		return nil, nil, err
	}

	var comments []*Comment
	resp, err := c.doRequest(req, &comments)
	if err != nil {
		return nil, resp, err
	}
	return comments, resp, nil
}

// CreateIssueComment 创建 Issue 评论
func (c *Client) CreateIssueComment(ctx context.Context, owner, repo string, index int64, opts CreateIssueCommentOption) (*Comment, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments",
		url.PathEscape(owner), url.PathEscape(repo), index)
	req, err := c.newRequest(ctx, http.MethodPost, path, opts)
	if err != nil {
		return nil, nil, err
	}

	var comment Comment
	resp, err := c.doRequest(req, &comment)
	if err != nil {
		return nil, resp, err
	}
	return &comment, resp, nil
}

// EditIssueCommentOption 编辑 Issue 评论的选项。
//
// 与 CreateIssueCommentOption 同 shape，为语义清晰独立定义。Gitea API
// PATCH /repos/{owner}/{repo}/issues/comments/{id} 只接受 body 字段。
type EditIssueCommentOption struct {
	Body string `json:"body"`
}

// EditIssueComment 编辑 Issue/PR 评论内容（按评论 ID 定位，不依赖 Issue index）。
//
// 用于 gen_tests Done 事件 PR 评论的幂等 upsert 场景：首次 Create，
// 再次调用时发现含锚点的旧评论 → 走 Edit 覆盖，避免重复评论堆积。
func (c *Client) EditIssueComment(ctx context.Context, owner, repo string, commentID int64, opts EditIssueCommentOption) (*Comment, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/comments/%d",
		url.PathEscape(owner), url.PathEscape(repo), commentID)
	req, err := c.newRequest(ctx, http.MethodPatch, path, opts)
	if err != nil {
		return nil, nil, err
	}

	var comment Comment
	resp, err := c.doRequest(req, &comment)
	if err != nil {
		return nil, resp, err
	}
	return &comment, resp, nil
}

// GetIssueLabels 获取 Issue 的标签列表
func (c *Client) GetIssueLabels(ctx context.Context, owner, repo string, index int64) ([]*Label, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/labels",
		url.PathEscape(owner), url.PathEscape(repo), index)
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}

	var labels []*Label
	resp, err := c.doRequest(req, &labels)
	if err != nil {
		return nil, resp, err
	}
	return labels, resp, nil
}

// AddIssueLabels 为 Issue 添加标签
func (c *Client) AddIssueLabels(ctx context.Context, owner, repo string, index int64, labelIDs []int64) ([]*Label, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/labels",
		url.PathEscape(owner), url.PathEscape(repo), index)

	body := struct {
		Labels []int64 `json:"labels"`
	}{Labels: labelIDs}

	req, err := c.newRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, nil, err
	}

	var labels []*Label
	resp, err := c.doRequest(req, &labels)
	if err != nil {
		return nil, resp, err
	}
	return labels, resp, nil
}

// RemoveIssueLabel 删除 Issue 的标签
func (c *Client) RemoveIssueLabel(ctx context.Context, owner, repo string, index int64, labelID int64) (*Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/labels/%d",
		url.PathEscape(owner), url.PathEscape(repo), index, labelID)
	req, err := c.newRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequest(req, nil)
	if err != nil {
		return resp, err
	}
	return resp, nil
}
