package httpapi

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	providererrors "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

const (
	// 入站和上游端点路径常量，供路径规范化与推导使用。新增 API 路径时在此登记。

	EndpointMessages             = "/v1/messages"
	EndpointChatCompletions      = "/v1/chat/completions"
	EndpointSystemOne            = "/v1/systemone"
	EndpointEmbeddings           = "/v1/embeddings"
	EndpointAlphaSearch          = "/v1/alpha/search"
	EndpointResponses            = "/v1/responses"
	EndpointResponsesInputTokens = "/v1/responses/input_tokens"
	EndpointResponsesCompact     = "/v1/responses/compact"
	EndpointImagesGenerations    = "/v1/images/generations"
	EndpointImagesEdits          = "/v1/images/edits"
	EndpointVideosGenerations    = "/v1/videos/generations"
	EndpointVideosEdits          = "/v1/videos/edits"
	EndpointVideosExtensions     = "/v1/videos/extensions"
	EndpointVideos               = "/v1/videos"
	EndpointGeminiModels         = "/v1beta/models"

	// EndpointAntigravityGenerateContent 是 Antigravity 原生流式生成端点。
	EndpointAntigravityGenerateContent = "/v1internal:streamGenerateContent"

	// gin.Context keys used by the middleware and helpers below.
	ctxKeyInboundEndpoint        = "_gateway_inbound_endpoint"
	ctxKeyActualUpstreamEndpoint = "_gateway_actual_upstream_endpoint"
)

// Normalization functions

// NormalizeInboundEndpoint maps a raw request path (which may carry
// prefixes like /antigravity, /openai) to its canonical form.
//
//	"/antigravity/v1/messages"   → "/v1/messages"
//	"/v1/chat/completions"       → "/v1/chat/completions"
//	"/openai/v1/responses/foo"   → "/v1/responses"
//	"/v1beta/models/gemini:gen"  → "/v1beta/models"
//
// OpenAI Responses API 还通过若干不带 "/v1/" 前缀的裸路径或别名路径暴露，
// 包括顶级裸路径和 Codex 直连路径。"/responses/compact" 与
// "/backend-api/codex/responses/compact" 是独立的 Compact 客户端端点，
// 应归一化为 EndpointResponsesCompact，不能并入根 Responses 端点。
// 其他裸路径或别名路径下的子路径仍作为根 Responses 端点的子资源后缀：
//
//	"/v1/responses/compact"                         → EndpointResponsesCompact
//	"/v1/responses/compact/detail"                  → EndpointResponsesCompact
//	"/openai/v1/responses/compact"                  → EndpointResponsesCompact
//	"/openai/v1/responses/compact/detail"           → EndpointResponsesCompact
//	"/responses/compact"                            → EndpointResponsesCompact
//	"/responses/compact/detail"                     → EndpointResponsesCompact
//	"/backend-api/codex/responses/compact"          → EndpointResponsesCompact
//	"/backend-api/codex/responses/compact/detail"   → EndpointResponsesCompact
//	"/v1/responses"                                 → EndpointResponses
//	"/openai/v1/responses"                          → EndpointResponses
//	"/responses"                                    → EndpointResponses
//	"/backend-api/codex/responses"                  → EndpointResponses
//
// 先检查 Compact，再检查根 Responses，因为 /v1/responses 是 Compact 路径的前缀。
func NormalizeInboundEndpoint(path string) string {
	path = strings.TrimSpace(path)
	switch {
	case strings.HasSuffix(path, EndpointSystemOne):
		return EndpointSystemOne
	case strings.Contains(path, EndpointEmbeddings):
		return EndpointEmbeddings
	case strings.Contains(path, EndpointAlphaSearch) || isBareOrSubpathOf(strings.TrimRight(path, "/"), "/alpha/search") || isBareOrSubpathOf(strings.TrimRight(path, "/"), "/backend-api/codex/alpha/search"):
		return EndpointAlphaSearch
	case strings.Contains(path, EndpointChatCompletions):
		return EndpointChatCompletions
	case strings.Contains(path, EndpointMessages):
		return EndpointMessages
	case strings.Contains(path, EndpointImagesGenerations) || strings.Contains(path, "/images/generations"):
		return EndpointImagesGenerations
	case strings.Contains(path, EndpointImagesEdits) || strings.Contains(path, "/images/edits"):
		return EndpointImagesEdits
	case strings.Contains(path, EndpointVideosGenerations) || strings.Contains(path, "/videos/generations"):
		return EndpointVideosGenerations
	case strings.Contains(path, EndpointVideosEdits) || strings.Contains(path, "/videos/edits"):
		return EndpointVideosEdits
	case strings.Contains(path, EndpointVideosExtensions) || strings.Contains(path, "/videos/extensions"):
		return EndpointVideosExtensions
	case strings.Contains(path, EndpointVideos) || strings.Contains(path, "/videos/"):
		return EndpointVideos
	case strings.Contains(path, EndpointResponsesInputTokens) || isResponsesInputTokensAliasPath(path):
		return EndpointResponsesInputTokens
	case strings.Contains(path, EndpointResponsesCompact) || isResponsesCompactAliasPath(path):
		return EndpointResponsesCompact
	case strings.Contains(path, EndpointResponses) || isResponsesRootAliasPath(path):
		return EndpointResponses
	case strings.Contains(path, EndpointGeminiModels):
		return EndpointGeminiModels
	default:
		return path
	}
}

func isResponsesInputTokensAliasPath(path string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return false
	}
	return isBareOrSubpathOf(trimmed, "/responses/input_tokens") ||
		isBareOrSubpathOf(trimmed, "/backend-api/codex/responses/input_tokens")
}

// isResponsesCompactAliasPath 匹配 /responses/compact、/backend-api/codex/responses/compact 及其子路径，例如 compact/detail。
// 匹配顺序先于 isResponsesRootAliasPath，因为 /responses 是 Compact 路径的前缀。
func isResponsesCompactAliasPath(path string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return false
	}
	return isBareOrSubpathOf(trimmed, "/responses/compact") || isBareOrSubpathOf(trimmed, "/backend-api/codex/responses/compact")
}

// isResponsesRootAliasPath 匹配 /responses、/backend-api/codex/responses 及它们除 Compact 外的子路径。
// 匹配从路径开头开始，/foo/responses 等其他前缀返回 false。
func isResponsesRootAliasPath(path string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return false
	}
	return isBareOrSubpathOf(trimmed, "/responses") || isBareOrSubpathOf(trimmed, "/backend-api/codex/responses")
}

// isBareOrSubpathOf 判断 path 是否等于 root，或是否为 root 下的子路径。
// 匹配从路径开头锚定，避免命中嵌套在其他无关前缀下的同名路径。
func isBareOrSubpathOf(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

// DeriveUpstreamEndpoint 根据提供商平台和归一化后的入站端点推导上游端点。
//
// 平台规则：OpenAI 与 Grok 默认转到 /v1/responses，并保留 /compact 等子路径；
// Grok 原始 Chat 请求会由转发结果覆盖实际上游端点。embeddings、alpha search 等
// 原生端点保留自身路径；Anthropic 转到 /v1/messages；Gemini 转到
// /v1beta/models；Antigravity 根据入站端点区分 Claude 与 Gemini。
func DeriveUpstreamEndpoint(inbound, rawRequestPath, platform string) string {
	inbound = strings.TrimSpace(inbound)

	switch platform {
	case capability.PlatformOpenAI, capability.PlatformGrok:
		if inbound == EndpointSystemOne || inbound == EndpointEmbeddings || inbound == EndpointAlphaSearch || inbound == EndpointResponsesInputTokens || inbound == EndpointImagesGenerations || inbound == EndpointImagesEdits || inbound == EndpointVideosGenerations || inbound == EndpointVideosEdits || inbound == EndpointVideosExtensions || inbound == EndpointVideos {
			return inbound
		}
		// OpenAI 的非原生端点统一转到 Responses API。
		// 保留从原始路径派生的子资源后缀，例如 /compact 或 /compact/detail。
		if suffix := responsesSubpathSuffix(rawRequestPath); suffix != "" {
			return EndpointResponses + suffix
		}
		// 原始路径无法派生后缀时，若入站端点已识别为 Compact，则回退到规范
		// Compact 端点，避免静默降级为根 Responses 端点。
		if inbound == EndpointResponsesCompact {
			return EndpointResponsesCompact
		}
		return EndpointResponses

	case capability.PlatformAnthropic:
		return EndpointMessages

	case capability.PlatformGemini:
		return EndpointGeminiModels

	case capability.PlatformAntigravity:
		// Antigravity 提供商支持 Claude 与 Gemini。
		if inbound == EndpointGeminiModels {
			return EndpointGeminiModels
		}
		return EndpointMessages
	}

	// 未知平台回退到入站端点。
	return inbound
}

// responsesSubpathSuffix extracts the part after "/responses" in a raw
// request path, e.g. "/openai/v1/responses/compact" → "/compact".
// Returns "" when there is no meaningful suffix.
func responsesSubpathSuffix(rawPath string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(rawPath), "/")
	_, after, ok := strings.CutLast(trimmed, "/responses")
	if !ok {
		return ""
	}
	suffix := after
	if suffix == "" || suffix == "/" {
		return ""
	}
	if !strings.HasPrefix(suffix, "/") {
		return ""
	}
	return suffix
}

// Middleware

// InboundEndpointMiddleware normalizes the request path and stores the
// canonical inbound endpoint in gin.Context so that every handler in
// the chain can read it via GetInboundEndpoint.
//
// Apply this middleware to all gateway route groups.
func InboundEndpointMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := ""
		if c.Request != nil && c.Request.URL != nil {
			path = c.Request.URL.Path
		}
		if path == "" {
			path = c.FullPath()
		}
		normalized := NormalizeInboundEndpoint(path)
		c.Set(ctxKeyInboundEndpoint, normalized)
		if c.Request != nil {
			// 同时写入 request.Context，方便认证阶段在进入 Handler 前完成明确配置的分组回退。
			ctx := apikey.WithInboundEndpoint(c.Request.Context(), normalized)
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	}
}

// 请求上下文辅助函数，供 handler 构造用量记录快照。

// GetInboundEndpoint 返回中间件保存的规范端点。
// 中间件未运行时先规范化 c.Request.URL.Path，再使用 c.FullPath()，通配路由据此区分 Compact 和根端点。
func GetInboundEndpoint(c *gin.Context) string {
	if v, ok := c.Get(ctxKeyInboundEndpoint); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	// Fallback: normalize on the fly.
	path := ""
	if c != nil {
		if c.Request != nil && c.Request.URL != nil {
			path = c.Request.URL.Path
		}
		if path == "" {
			path = c.FullPath()
		}
	}
	return NormalizeInboundEndpoint(path)
}

// GetUpstreamEndpoint derives the upstream endpoint from the context
// and the provider platform. Handlers call this after scheduling an
// provider, passing provider.Platform.
func GetUpstreamEndpoint(c *gin.Context, platform string) string {
	// OpenAI 转发服务维护独立的运行时端点上下文，覆盖普通入站推导。
	// 这对 force_chat_completions 的错误路径尤为重要：此时可能没有
	// ForwardResult，不能把入站 /v1/responses 误报成上游端点。
	if platform == capability.PlatformOpenAI || platform == capability.PlatformGrok || providererrors.IsCNProvider(platform) {
		if endpoint := GetActualOpenAIUpstreamEndpoint(c); endpoint != "" {
			return endpoint
		}
	}
	if c != nil {
		if value, ok := c.Get(ctxKeyActualUpstreamEndpoint); ok {
			if endpoint, ok := value.(string); ok && endpoint != "" {
				return endpoint
			}
		}
	}
	inbound := GetInboundEndpoint(c)
	rawPath := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		rawPath = c.Request.URL.Path
	}
	return DeriveUpstreamEndpoint(inbound, rawPath, platform)
}

// SetActualUpstreamEndpoint 记录本次尝试实际使用的上游端点。
func SetActualUpstreamEndpoint(c *gin.Context, endpoint string) {
	if c != nil {
		c.Set(ctxKeyActualUpstreamEndpoint, strings.TrimSpace(endpoint))
	}
}
