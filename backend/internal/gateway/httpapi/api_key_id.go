package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/gin-gonic/gin"
)

// APIKeyIDFromContext 读取原认证上下文中的 Key ID；缺失或类型不匹配返回零，
// 不将仅加载的诊断投影提升为已认证主体。
func APIKeyIDFromContext(c *gin.Context) int64 {
	if c == nil {
		return 0
	}
	v, exists := c.Get("api_key")
	if !exists {
		return 0
	}
	apiKey, ok := v.(*apikey.APIKey)
	if !ok || apiKey == nil {
		return 0
	}
	return apiKey.ID
}
