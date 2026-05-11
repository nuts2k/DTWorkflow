package gitea

import (
	"context"
	"fmt"
	"io"
	"net/url"
)

// CreateIssueAttachment 上传 Issue 附件。
func (c *Client) CreateIssueAttachment(ctx context.Context, owner, repo string,
	issueIndex int64, filename string, reader io.Reader) (*Attachment, *Response, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/assets",
		url.PathEscape(owner), url.PathEscape(repo), issueIndex)

	req, err := c.newMultipartRequest(ctx, path, "attachment", filename, reader)
	if err != nil {
		return nil, nil, err
	}

	var att Attachment
	resp, err := c.doRequest(req, &att)
	if err != nil {
		return nil, resp, err
	}
	return &att, resp, nil
}
