package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Version 版本号，可通过 ldflags 在构建时注入
var Version = "dev"

// maxRawBodySize 原始响应体最大读取大小（100MB，用于 diff 等大文件）
const maxRawBodySize = 100 << 20

// Client Gitea API 客户端
type Client struct {
	baseURL    *url.URL     // Gitea 实例地址，如 https://gitea.example.com
	token      string       // API Token
	httpClient *http.Client // 可替换的 HTTP 客户端
	userAgent  string       // User-Agent 标识
}

// ClientOption 客户端配置选项（Functional Options 模式）
type ClientOption func(*Client) error

// NewClient 创建新的 Gitea API 客户端
func NewClient(baseURL string, opts ...ClientOption) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("解析 Gitea URL: %w", err)
	}

	c := &Client{
		baseURL:    u,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		userAgent:  "dtworkflow/" + Version,
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, fmt.Errorf("应用客户端选项: %w", err)
		}
	}

	if c.token == "" {
		return nil, errors.New("必须提供 API Token：使用 WithToken() 选项")
	}

	return c, nil
}

// WithToken 设置 API Token 认证
func WithToken(token string) ClientOption {
	return func(c *Client) error {
		if token == "" {
			return errors.New("token 不能为空")
		}
		c.token = token
		return nil
	}
}

// WithHTTPClient 替换底层 HTTP 客户端（用于自定义超时、代理、TLS 等）
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) error {
		if hc == nil {
			return errors.New("HTTP 客户端不能为 nil")
		}
		c.httpClient = hc
		return nil
	}
}

// WithUserAgent 自定义 User-Agent
func WithUserAgent(ua string) ClientOption {
	return func(c *Client) error {
		c.userAgent = ua
		return nil
	}
}

// newRequest 构造 HTTP 请求，注入认证头和 Content-Type
func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	u := c.baseURL.JoinPath(path)

	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求体: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return req, nil
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

// doRequest 执行请求，解析 JSON 响应，返回 *Response
func (c *Client) doRequest(req *http.Request, v any) (*Response, error) {
	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer httpResp.Body.Close()

	resp := newResponse(httpResp)

	if err := checkResponse(httpResp); err != nil {
		return resp, err
	}

	if v != nil {
		if err := json.NewDecoder(httpResp.Body).Decode(v); err != nil {
			return resp, fmt.Errorf("解析响应: %w", err)
		}
	}

	return resp, nil
}

// doRequestRaw 执行请求，返回原始字节（用于 diff/patch 等非 JSON 响应）
func (c *Client) doRequestRaw(req *http.Request) ([]byte, *Response, error) {
	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer httpResp.Body.Close()

	resp := newResponse(httpResp)

	if err := checkResponse(httpResp); err != nil {
		return nil, resp, err
	}

	data, err := io.ReadAll(io.LimitReader(httpResp.Body, maxRawBodySize))
	if err != nil {
		return nil, resp, fmt.Errorf("读取响应体: %w", err)
	}

	return data, resp, nil
}
