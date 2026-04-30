package gitea

import (
	"context"
	"fmt"
	"net/url"
)

// GetRepo 获取仓库信息
// GET /api/v1/repos/{owner}/{repo}
func (c *Client) GetRepo(ctx context.Context, owner, repo string) (*Repository, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	req, err := c.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, nil, err
	}

	var result Repository
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return &result, resp, nil
}

// GetBranch 获取分支信息
// GET /api/v1/repos/{owner}/{repo}/branches/{branch}
func (c *Client) GetBranch(ctx context.Context, owner, repo, branch string) (*Branch, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/branches/%s",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(branch))
	req, err := c.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, nil, err
	}

	var result Branch
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return &result, resp, nil
}

// GetTag 获取 tag 信息
// GET /api/v1/repos/{owner}/{repo}/tags/{tag}
func (c *Client) GetTag(ctx context.Context, owner, repo, tag string) (*Tag, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/tags/%s",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(tag))
	req, err := c.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, nil, err
	}

	var result Tag
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return &result, resp, nil
}

// CreateBranch 创建分支
// POST /api/v1/repos/{owner}/{repo}/branches
func (c *Client) CreateBranch(ctx context.Context, owner, repo string, opts CreateBranchOption) (*Branch, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/branches", url.PathEscape(owner), url.PathEscape(repo))
	req, err := c.newRequest(ctx, "POST", path, opts)
	if err != nil {
		return nil, nil, err
	}

	var result Branch
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return &result, resp, nil
}

// GetContents 获取文件元数据和内容
// GET /api/v1/repos/{owner}/{repo}/contents/{filepath}?ref={ref}
// 注意：filepath 不做 PathEscape，Gitea 的 contents 端点使用通配符路径，期望原始斜杠
func (c *Client) GetContents(ctx context.Context, owner, repo, filepath, ref string) (*ContentsResponse, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s",
		url.PathEscape(owner), url.PathEscape(repo), filepath)
	params := url.Values{}
	if ref != "" {
		params.Set("ref", ref)
	}
	req, err := c.newRequestWithQuery(ctx, "GET", path, params, nil)
	if err != nil {
		return nil, nil, err
	}

	var result ContentsResponse
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return &result, resp, nil
}

// GetFileContent 获取文件原始内容
// GET /api/v1/repos/{owner}/{repo}/raw/{filepath}?ref={ref}
// 注意：filepath 不做 PathEscape，Gitea 的 raw 端点使用通配符路径，期望原始斜杠
func (c *Client) GetFileContent(ctx context.Context, owner, repo, filepath, ref string) ([]byte, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/raw/%s",
		url.PathEscape(owner), url.PathEscape(repo), filepath)
	params := url.Values{}
	if ref != "" {
		params.Set("ref", ref)
	}
	req, err := c.newRequestWithQuery(ctx, "GET", path, params, nil)
	if err != nil {
		return nil, nil, err
	}

	data, resp, err := c.doRequestRaw(req)
	if err != nil {
		return nil, resp, err
	}
	return data, resp, nil
}

// ListDirContents 列出仓库指定目录下的条目（文件和子目录）。
// dir 为空字符串时列出根目录。
// GET /api/v1/repos/{owner}/{repo}/contents/{dir}?ref={ref}
func (c *Client) ListDirContents(ctx context.Context, owner, repo, dir, ref string) ([]ContentsResponse, *Response, error) {
	p := fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s",
		url.PathEscape(owner), url.PathEscape(repo), dir)
	params := url.Values{}
	if ref != "" {
		params.Set("ref", ref)
	}
	req, err := c.newRequestWithQuery(ctx, "GET", p, params, nil)
	if err != nil {
		return nil, nil, err
	}

	var result []ContentsResponse
	resp, err := c.doRequest(req, &result)
	if err != nil {
		return nil, resp, err
	}
	return result, resp, nil
}
