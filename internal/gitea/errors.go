package gitea

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrorResponse Gitea API 返回的错误
type ErrorResponse struct {
	Response *http.Response `json:"-"`
	Message  string         `json:"message"`
	URL      string         `json:"url,omitempty"`
}

// Error 实现 error 接口
func (e *ErrorResponse) Error() string {
	return fmt.Sprintf("%s %s: %d %s",
		e.Response.Request.Method,
		e.Response.Request.URL.Path,
		e.Response.StatusCode,
		e.Message,
	)
}

// checkResponse 检查 HTTP 状态码，非 2xx 时解析错误体
func checkResponse(r *http.Response) error {
	if r.StatusCode >= 200 && r.StatusCode < 300 {
		return nil
	}
	errResp := &ErrorResponse{Response: r}
	data, err := io.ReadAll(r.Body)
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
		return errResp.Response.StatusCode == code
	}
	return false
}
