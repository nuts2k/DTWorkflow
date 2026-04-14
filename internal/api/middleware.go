package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
)

// ContextKeyIdentity 存储已认证身份的 gin context key
const ContextKeyIdentity = "api_identity"

// TokenAuth Bearer Token 认证中间件，使用常量时间比较防止时序攻击
func TokenAuth(tokens []config.TokenConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			Error(c, http.StatusUnauthorized, ErrCodeUnauthorized, "缺少有效的认证凭证")
			c.Abort()
			return
		}
		provided := strings.TrimPrefix(header, "Bearer ")

		for _, tc := range tokens {
			if subtle.ConstantTimeCompare([]byte(provided), []byte(tc.Token)) == 1 {
				c.Set(ContextKeyIdentity, tc.Identity)
				c.Next()
				return
			}
		}

		Error(c, http.StatusUnauthorized, ErrCodeUnauthorized, "缺少有效的认证凭证")
		c.Abort()
	}
}
