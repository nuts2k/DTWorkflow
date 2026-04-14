package api

import "github.com/gin-gonic/gin"

// 错误码常量
const (
	ErrCodeUnauthorized  = "unauthorized"
	ErrCodeNotFound      = "not_found"
	ErrCodeBadRequest    = "bad_request"
	ErrCodeConflict      = "conflict"
	ErrCodeBadGateway    = "bad_gateway"
	ErrCodeInternalError = "internal_error"
)

// Success 统一成功响应
func Success(c *gin.Context, httpStatus int, data any) {
	c.JSON(httpStatus, gin.H{"data": data})
}

// Error 统一错误响应
func Error(c *gin.Context, httpStatus int, code, message string) {
	c.JSON(httpStatus, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}
