package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/gin-gonic/gin"
)

// SetClaudeCodeClientContext 在 HTTP 边界固化识别结果，供后续请求处理读取。
func SetClaudeCodeClientContext(c *gin.Context, body []byte, parsed *requeststate.ParsedRequest) {
	if c == nil || c.Request == nil {
		return
	}
	probe, _ := requeststate.IsMaxTokensOneHaikuRequestFromContext(c.Request.Context())
	result := DetectClaudeCodeRequest(c, body, parsed, probe)
	ctx := requeststate.SetClaudeCodeClient(c.Request.Context(), result.ClaudeCode)
	if result.ClaudeCode && result.Version != "" {
		ctx = requeststate.SetClaudeCodeVersion(ctx, result.Version)
	}
	c.Request = c.Request.WithContext(ctx)
}
