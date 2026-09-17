package httpapi

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/gin-gonic/gin"
)

// 路径提取与协议白名单复用唯一实现，保持旧 Responses 子路径拒绝边界。
func OpenAIResponsesRequestPathSuffix(c *gin.Context) string {
	suffix, ok := upstream.SanitizedUpstreamPathSuffix(RawOpenAIResponsesRequestPathSuffix(c))
	if !ok {
		return ""
	}
	return suffix
}

// IsForwardableOpenAIResponsesRequestPath 判断入站请求携带的 /responses 子路径
// 是否可以安全转发。路由层用它在鉴权后、调度前直接拒绝畸形子路径。
func IsForwardableOpenAIResponsesRequestPath(c *gin.Context) bool {
	_, ok := upstream.SanitizedUpstreamPathSuffix(RawOpenAIResponsesRequestPathSuffix(c))
	return ok
}

// IsOpenAIResponsesInputTokensRequestPath 判断请求是否指向原生 Responses 输入 token 预检端点。
func IsOpenAIResponsesInputTokensRequestPath(c *gin.Context) bool {
	return OpenAIResponsesRequestPathSuffix(c) == "/input_tokens"
}

// RawOpenAIResponsesRequestPathSuffix 仅做提取，不做任何安全判断。
func RawOpenAIResponsesRequestPathSuffix(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return ""
	}
	normalizedPath := strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/")
	if normalizedPath == "" {
		return ""
	}
	idx := strings.LastIndex(normalizedPath, "/responses")
	if idx < 0 {
		return ""
	}
	suffix := normalizedPath[idx+len("/responses"):]
	if suffix == "" || suffix == "/" {
		return ""
	}
	if !strings.HasPrefix(suffix, "/") {
		return ""
	}
	return suffix
}
