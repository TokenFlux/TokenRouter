package httpapi

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// IsOpenAIResponsesCompactPath 识别旧 compact 端点及其允许转发的子路径。
func IsOpenAIResponsesCompactPath(c *gin.Context) bool {
	suffix := strings.TrimSpace(OpenAIResponsesRequestPathSuffix(c))
	return suffix == "/compact" || strings.HasPrefix(suffix, "/compact/")
}

// ResolveOpenAICompactSessionID 保留客户端头、请求种子及随机标识的原优先级。
func ResolveOpenAICompactSessionID(c *gin.Context) string {
	if c != nil {
		if sessionID := strings.TrimSpace(c.GetHeader("session_id")); sessionID != "" {
			return sessionID
		}
		if conversationID := strings.TrimSpace(c.GetHeader("conversation_id")); conversationID != "" {
			return conversationID
		}
		if seed, ok := c.Get(OpenAICompactSessionSeedKey); ok {
			if seedStr, ok := seed.(string); ok && strings.TrimSpace(seedStr) != "" {
				return strings.TrimSpace(seedStr)
			}
		}
	}
	return uuid.NewString()
}

// OpenAICompactSessionSeedKey 保留历史 HTTP 种子编码，不参与认证。
const OpenAICompactSessionSeedKey = "openai_compact_session_seed"
