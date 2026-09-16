package service

import (
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
)

// StartOpenAICompactSSEKeepalive 委托唯一 HTTP 心跳拥有者。
func StartOpenAICompactSSEKeepalive(c *gin.Context, interval time.Duration) func() {
	return gatewayhttp.StartOpenAICompactSSEKeepalive(c, interval)
}

// startOpenAISSEKeepalive 为已确认 SSE 的透传入口保留原签名。
func startOpenAISSEKeepalive(c *gin.Context, interval time.Duration) func() {
	return gatewayhttp.StartOpenAISSEKeepalive(c, interval)
}

// StopOpenAICompactSSEKeepaliveCommitted 在同一心跳状态上停止并接管输出。
func StopOpenAICompactSSEKeepaliveCommitted(c *gin.Context) bool {
	return gatewayhttp.StopOpenAICompactSSEKeepaliveCommitted(c)
}

// OpenAICompactKeepaliveAdjustedWrittenSize 保留排除心跳字节的兼容入口。
func OpenAICompactKeepaliveAdjustedWrittenSize(c *gin.Context) int {
	return gatewayhttp.OpenAICompactKeepaliveAdjustedWrittenSize(c)
}
