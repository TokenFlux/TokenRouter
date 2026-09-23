package service

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"
)

const (
	grokClientToolCacheOptInHeader   = "X-Sub2API-Grok-Client-Tool-Cache"
	grokFreeCacheNativeToolsJSON     = `[{"type":"web_search"},{"type":"x_search"}]`
	grokFreeCacheDisabledToolChoice  = "none"
	grokClientToolCacheOptInExtraKey = "grok_client_tool_cache_enabled"
)

// extractClaudeCodeSessionID 从请求头或 Anthropic/OpenAI 兼容载荷元数据中提取
// Claude Code 会话标识。
func extractClaudeCodeSessionID(c *gin.Context, body []byte) string {
	if c != nil {
		if seed := strings.TrimSpace(c.GetHeader(gatewayhttp.ClaudeCodeSessionHeader)); seed != "" {
			return seed
		}
	}
	return grok.ExtractClaudeCodeSessionIDFromPayload(body)
}

func resolveGrokCacheIdentity(c *gin.Context, body []byte, explicitKey, upstreamModel string) string {
	return grok.ResolveCacheIdentity(grokCacheInput(c, explicitKey, upstreamModel), body)
}

// applyGrokFreeMessagesFunctionToolCacheRoute 只为已知 Free 账号启用 xAI 可缓存的
// 混合工具路由。纯客户端工具默认启用，运维人员可在原生搜索工具会改变预期行为时
// 按账号明确关闭（#4486）。
func applyGrokFreeMessagesFunctionToolCacheRoute(body, intentSourceBody []byte, account *gatewayprovider.ExecutionAccount, cacheIdentity string) ([]byte, error) {
	allowPureClientTools, _ := grokClientToolCacheAccountPolicy(account)
	return applyGrokFreeToolCacheRoute(body, intentSourceBody, account, cacheIdentity, allowPureClientTools, true)
}

// applyGrokFreeRequestToolCacheRoute 还接受请求级开关。该兼容协议头仅在本地消费，
// buildGrokResponsesRequest 只向上游转发明确支持的 OpenAI-Beta 头。
func applyGrokFreeRequestToolCacheRoute(c *gin.Context, body, intentSourceBody []byte, account *gatewayprovider.ExecutionAccount, cacheIdentity string) ([]byte, error) {
	allowPureClientTools, accountPolicyExplicit := grokClientToolCacheAccountPolicy(account)
	requestOptOut := false
	if c != nil {
		switch strings.ToLower(strings.TrimSpace(c.GetHeader(grokClientToolCacheOptInHeader))) {
		case "1", "true", "yes", "on", "prefer-cache":
			allowPureClientTools = true
		case "0", "false", "no", "off":
			allowPureClientTools = false
			requestOptOut = true
		}
	}
	if !allowPureClientTools && !accountPolicyExplicit && !requestOptOut && isGrokClaudeDesktopResponsesCacheRequest(c) {
		allowPureClientTools = true
	}
	// 名为 web_search/x_search 的函数仍是客户端函数。已知 Free OAuth 账号默认使用
	// 缓存路由；请求级启用可覆盖账号关闭，而请求级关闭始终优先。旧 Claude 指纹仅在
	// 尚无账号策略时作为兼容回退（#4486）。
	return applyGrokFreeToolCacheRoute(body, intentSourceBody, account, cacheIdentity, allowPureClientTools, allowPureClientTools)
}

// grokClientToolCacheAccountPolicy 严格要求配置值为 JSON 布尔值。缺少键时仅对已确认的
// Grok Free OAuth 账号默认启用；付费、API Key 和未知账号保持关闭。
func grokClientToolCacheAccountPolicy(account *gatewayprovider.ExecutionAccount) (enabled, explicit bool) {
	if !isKnownGrokFreeAccount(account) {
		return false, false
	}
	if account.Record.Extra == nil {
		return true, false
	}
	value, exists := account.Record.Extra[grokClientToolCacheOptInExtraKey]
	if !exists {
		return true, false
	}
	enabled, valid := value.(bool)
	if !valid {
		return false, true
	}
	return enabled, true
}

// isGrokClaudeDesktopResponsesCacheRequest 识别 Claude Desktop 本地代理经 CC Switch
// 转为 OpenAI Responses 请求时的严格线路指纹。必须同时满足所有独立信号，避免普通
// Claude 兼容客户端或 Chat bridge 被静默加入原生/客户端混合工具路由。
func isGrokClaudeDesktopResponsesCacheRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil || gatewayhttp.IsOpenAIResponsesCompactPath(c) {
		return false
	}
	path := strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/")
	if !strings.HasSuffix(path, "/responses") {
		return false
	}

	if !clientmeta.NewClaudeCodeValidator().ValidateUserAgent(strings.TrimSpace(c.GetHeader("User-Agent"))) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(c.GetHeader("X-App"))) {
	case "cli", "cli-bg":
	default:
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(c.GetHeader("anthropic-client-platform")), "desktop_app") {
		return false
	}
	return strings.TrimSpace(c.GetHeader("X-Claude-Code-Session-Id")) != ""
}

func applyGrokFreeToolCacheRoute(body, intentSourceBody []byte, account *gatewayprovider.ExecutionAccount, cacheIdentity string, allowPureClientTools, allowFunctionSearch bool) ([]byte, error) {
	if strings.TrimSpace(cacheIdentity) == "" {
		return body, nil
	}
	return grok.ApplyGrokFreeToolCacheRoute(body, intentSourceBody, isKnownGrokFreeAccount(account), cacheIdentity, allowPureClientTools, allowFunctionSearch)
}

// isKnownGrokFreeAccount 识别免费层 Grok 账号，用于免费缓存路由与媒体 free_tier 阻断，
// 其覆盖范围比软性门禁更广；软性门禁使用 isExplicitGrokFreeOAuthAccount，且只匹配明确的 free。
func isKnownGrokFreeAccount(account *gatewayprovider.ExecutionAccount) bool {
	return accountcore.KnownGrokFreeAccount(gatewayprovider.ExecutionRecord(account), accountprovider.GrokTierRules())
}

// 请求读取留在适配器；原种子 helper 只在平台实际选择该分支时调用。
func grokCacheInput(c *gin.Context, explicitKey, model string) grok.CacheIdentityInput {
	input := grok.CacheIdentityInput{APIKeyID: gatewayhttp.APIKeyIDFromContext(c), Compact: gatewayhttp.IsOpenAIResponsesCompactPath(c), Model: model, ExplicitKey: explicitKey, StablePrefixSeed: gatewaysession.OpenAIStablePrefixSeed, AnchoredSeed: gatewaysession.OpenAIAnchoredContentSeed, PreviousResponseSeed: gatewaysession.GrokPreviousResponseSeed}
	if c != nil {
		input.ClaudeSession = c.GetHeader(gatewayhttp.ClaudeCodeSessionHeader)
		input.HeaderSession = gatewayhttp.ExplicitOpenAIHeaderSessionID(c)
		input.ConversationID = c.GetHeader(gatewayhttp.GrokConversationIDHeader)
	}
	return input
}
