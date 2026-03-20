package gitea

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// GetRepo 获取仓库信息
// GET /api/v1/repos/{owner}/{repo}
func (c *Client) GetRepo(ctx context.Context, owner, repo string) (*Repository, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s", owner, repo)
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
	path := fmt.Sprintf("/api/v1/repos/%s/%s/branches/%s", owner, repo, branch)
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

// CreateBranch 创建分支
// POST /api/v1/repos/{owner}/{repo}/branches
func (c *Client) CreateBranch(ctx context.Context, owner, repo string, opts CreateBranchOption) (*Branch, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/branches", owner, repo)
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

// newRequestWithQuery 构造带查询参数的 HTTP 请求
func (c *Client) newRequestWithQuery(ctx context.Context, method, path string, params url.Values, body any) (*http.Request, error) {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	return req, nil
}

// GetContents 获取文件元数据和内容
// GET /api/v1/repos/{owner}/{repo}/contents/{filepath}?ref={ref}
func (c *Client) GetContents(ctx context.Context, owner, repo, filepath, ref string) (*ContentsResponse, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s", owner, repo, filepath)
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
func (c *Client) GetFileContent(ctx context.Context, owner, repo, filepath, ref string) ([]byte, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/raw/%s", owner, repo, filepath)
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
