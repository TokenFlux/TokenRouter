package service

import (
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"

	"github.com/gin-gonic/gin"
)

// clientSessionIDHeaders 在 OpenAI 兼容粘性会话头的基础上加入原生协议标识；
// 这些标识可以安全持久化，但不得改变 OpenAI 调度行为。
var clientSessionIDHeaders = append(
	append([]string(nil), explicitOpenAIHeaderSessionNames...),
	claudeCodeSessionHeader,
)

// ClaudeCodeSessionIDFromHeader 解析 Claude Code 会话头，用于消息协议的粘性路由。
// 该入口与仅用于用量日志的 ExtractClientSessionID 分离，避免改变其他协议的会话语义。
func ClaudeCodeSessionIDFromHeader(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return sanitizeSessionID(c.GetHeader(claudeCodeSessionHeader))
}

func ExtractClientSessionID(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return gatewaysession.ExtractClientSessionID(c.GetHeader, clientSessionIDHeaders, isGrokRequestContext(c))
}

func sanitizeSessionID(raw string) string { return gatewaysession.SanitizeClientSessionID(raw) }
