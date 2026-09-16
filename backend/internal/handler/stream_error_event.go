// 旧 HTTP 入口只提取元数据并委托协议错误输出。
package handler

import (
	"strings"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
)

func writeResponsesFailedSSE(c *gin.Context, errType, code, message string) bool {
	return gatewayhttp.WriteResponsesFailedSSE(c, errType, code, message, failedResponseRequestID(c), requestModel(c))
}
func failedResponseRequestID(c *gin.Context) string {
	if c != nil && c.Request != nil {
		value, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
		return value
	}
	return ""
}
func synthesizeResponseID(c *gin.Context) string {
	return gatewayhttp.SynthesizeResponseID(failedResponseRequestID(c))
}
func inboundIsResponses(c *gin.Context) bool { return gatewayhttp.InboundIsResponses(c) }
func mapResponsesErrorCode(kind string, code ...string) string {
	return gatewayhttp.MapResponsesErrorCode(kind, code...)
}

// requestModel 取当前请求的 inbound model（由 setOpsRequestContext 写入）。
// 缺失时返回 ""；caller 据此决定是否忽略该字段。
func requestModel(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if v, ok := c.Get(opsModelKey); ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
