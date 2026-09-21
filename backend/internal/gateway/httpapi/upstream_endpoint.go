package httpapi

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// SetActualOpenAIUpstreamEndpoint 记录当前转发尝试选择的端点，供无法取得
// OpenAIForwardResult 的错误路径记录用量和运维日志。
func SetActualOpenAIUpstreamEndpoint(c *gin.Context, endpoint string) {
	if c == nil {
		return
	}
	if endpoint = strings.TrimSpace(endpoint); endpoint != "" {
		c.Set(openAIUpstreamEndpointContextKey, endpoint)
	}
}

// ClearActualOpenAIUpstreamEndpoint 清理当前转发尝试记录的端点。
// Handler 会在账号 failover 尝试间复用同一个 Gin context，因此每次尝试
// 都必须从无残留状态开始。
func ClearActualOpenAIUpstreamEndpoint(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(openAIUpstreamEndpointContextKey, "")
}

// GetActualOpenAIUpstreamEndpoint 返回该请求最近一次转发尝试记录的端点。
func GetActualOpenAIUpstreamEndpoint(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, exists := c.Get(openAIUpstreamEndpointContextKey)
	if !exists {
		return ""
	}
	endpoint, _ := value.(string)
	return strings.TrimSpace(endpoint)
}

const openAIUpstreamEndpointContextKey = "openai_actual_upstream_endpoint"
