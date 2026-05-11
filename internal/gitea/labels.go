package gitea

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ListRepoLabels 列出仓库所有标签。
func (c *Client) ListRepoLabels(ctx context.Context, owner, repo string) ([]Label, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/labels",
		url.PathEscape(owner), url.PathEscape(repo))
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}

	var labels []Label
	resp, err := c.doRequest(req, &labels)
	if err != nil {
		return nil, resp, err
	}
	return labels, resp, nil
}
