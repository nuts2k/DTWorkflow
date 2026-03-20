package gitea

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxErrorBodySize 错误响应体最大读取大小（1MB）
const maxErrorBodySize = 1 << 20

// ErrorResponse Gitea API 返回的错误
type ErrorResponse struct {
	StatusCode int    `json:"-"`
	Method     string `json:"-"`
	Path       string `json:"-"`
	Message    string `json:"message"`
	URL        string `json:"url,omitempty"`
}

// Error 实现 error 接口
func (e *ErrorResponse) Error() string {
	return fmt.Sprintf("%s %s: %d %s",
		e.Method,
		e.Path,
		e.StatusCode,
		e.Message,
	)
}

// checkResponse 检查 HTTP 状态码，非 2xx 时解析错误体。
// 注意：非 2xx 时会消费 r.Body，调用方不应在 checkResponse 返回非 nil 错误后再读取 r.Body。
func checkResponse(r *http.Response) error {
	if r.StatusCode >= 200 && r.StatusCode < 300 {
		return nil
	}
	errResp := &ErrorResponse{
		StatusCode: r.StatusCode,
		Method:     r.Request.Method,
		Path:       r.Request.URL.Path,
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxErrorBodySize))
	if err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, errResp)
	}
	if errResp.Message == "" {
		errResp.Message = http.StatusText(r.StatusCode)
	}
	return errResp
}

// IsNotFound 判断是否为 404 错误
func IsNotFound(err error) bool {
	return hasStatusCode(err, http.StatusNotFound)
}

// IsUnauthorized 判断是否为 401 错误
func IsUnauthorized(err error) bool {
	return hasStatusCode(err, http.StatusUnauthorized)
}

// IsForbidden 判断是否为 403 错误
func IsForbidden(err error) bool {
	return hasStatusCode(err, http.StatusForbidden)
}

// IsConflict 判断是否为 409 错误（如分支已存在）
func IsConflict(err error) bool {
	return hasStatusCode(err, http.StatusConflict)
}

// hasStatusCode 检查错误是否为指定 HTTP 状态码的 ErrorResponse
func hasStatusCode(err error, code int) bool {
	var errResp *ErrorResponse
	if errors.As(err, &errResp) {
		return errResp.StatusCode == code
	}
	return false
}
