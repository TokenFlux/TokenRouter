package service

import (
	"net/http"
	"strings"

	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
)

const (
	grokConversationIDHeader         = "X-Grok-Conv-Id"
	claudeCodeSessionHeader          = "X-Claude-Code-Session-Id"
	grokClientToolCacheOptInHeader   = "X-Sub2API-Grok-Client-Tool-Cache"
	grokFreeCacheNativeToolsJSON     = `[{"type":"web_search"},{"type":"x_search"}]`
	grokFreeCacheDisabledToolChoice  = "none"
	grokClientToolCacheOptInExtraKey = "grok_client_tool_cache_enabled"
)

// extractClaudeCodeSessionID 从请求头或 Anthropic/OpenAI 兼容载荷元数据中提取
// Claude Code 会话标识。
func extractClaudeCodeSessionID(c *gin.Context, body []byte) string {
	if c != nil {
		if seed := strings.TrimSpace(c.GetHeader(claudeCodeSessionHeader)); seed != "" {
			return seed
		}
	}
	return extractClaudeCodeSessionIDFromPayload(body)
}

func extractClaudeCodeSessionIDFromPayload(body []byte) string {
	return nativegrok.ExtractClaudeCodeSessionIDFromPayload(body)
}

func resolveGrokCacheIdentity(c *gin.Context, body []byte, explicitKey, upstreamModel string) string {
	return nativegrok.ResolveCacheIdentity(grokCacheInput(c, explicitKey, upstreamModel), body)
}

func isGrokRequestContext(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if c.Request != nil {
		if platform, ok := c.Request.Context().Value(ctxkey.ForcePlatform).(string); ok && strings.TrimSpace(platform) != "" {
			return platform == PlatformGrok
		}
	}
	v, exists := c.Get("api_key")
	if !exists {
		return false
	}
	apiKey, ok := v.(*APIKey)
	return ok && apiKey != nil && apiKey.Group != nil && apiKey.Group.Platform == PlatformGrok
}

func applyGrokResponsesCacheIdentity(body, intentSourceBody []byte, identity string, injectFreeTierTools bool) ([]byte, error) {
	return nativegrok.ApplyGrokResponsesCacheIdentity(body, intentSourceBody, identity, injectFreeTierTools)
}

func hasGrokResponsesToolIntent(body []byte) bool { return nativegrok.HasGrokResponsesToolIntent(body) }

// applyGrokFreeMessagesFunctionToolCacheRoute 只为已知 Free 账号启用 xAI 可缓存的
// 混合工具路由。纯客户端工具默认启用，运维人员可在原生搜索工具会改变预期行为时
// 按账号明确关闭（#4486）。
func applyGrokFreeMessagesFunctionToolCacheRoute(body, intentSourceBody []byte, account *Account, cacheIdentity string) ([]byte, error) {
	allowPureClientTools, _ := grokClientToolCacheAccountPolicy(account)
	return applyGrokFreeToolCacheRoute(body, intentSourceBody, account, cacheIdentity, allowPureClientTools, true)
}

// applyGrokFreeRequestToolCacheRoute 还接受请求级开关。该兼容协议头仅在本地消费，
// buildGrokResponsesRequest 只向上游转发明确支持的 OpenAI-Beta 头。
func applyGrokFreeRequestToolCacheRoute(c *gin.Context, body, intentSourceBody []byte, account *Account, cacheIdentity string) ([]byte, error) {
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
func grokClientToolCacheAccountPolicy(account *Account) (enabled, explicit bool) {
	if !isKnownGrokFreeAccount(account) {
		return false, false
	}
	if account.Extra == nil {
		return true, false
	}
	value, exists := account.Extra[grokClientToolCacheOptInExtraKey]
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
	if c == nil || c.Request == nil || c.Request.URL == nil || isOpenAIResponsesCompactPath(c) {
		return false
	}
	path := strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/")
	if !strings.HasSuffix(path, "/responses") {
		return false
	}

	if !claudeCodeUAPattern.MatchString(strings.TrimSpace(c.GetHeader("User-Agent"))) {
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

func applyGrokFreeToolCacheRoute(body, intentSourceBody []byte, account *Account, cacheIdentity string, allowPureClientTools, allowFunctionSearch bool) ([]byte, error) {
	if strings.TrimSpace(cacheIdentity) == "" {
		return body, nil
	}
	return nativegrok.ApplyGrokFreeToolCacheRoute(body, intentSourceBody, isKnownGrokFreeAccount(account), cacheIdentity, allowPureClientTools, allowFunctionSearch)
}

// isKnownGrokFreeAccount 识别免费层 Grok 账号，用于免费缓存路由与媒体 free_tier 阻断，
// 其覆盖范围比软性门禁更广；软性门禁使用 isExplicitGrokFreeOAuthAccount，且只匹配明确的 free。
func isKnownGrokFreeAccount(account *Account) bool {
	if account == nil || !account.IsGrokOAuth() {
		return false
	}
	// 实时访问令牌 JWT 优先于陈旧的账单或凭据快照，令牌刷新后可立即反映降级到免费档位。
	if jwtTier := nativegrok.SubscriptionTierFromJWT(account.GetCredential("access_token")); jwtTier != "" {
		return isGrokFreeSubscriptionTier(jwtTier)
	}
	freeSignal := false
	paidSignal := false
	inferredFreeSignal := false
	if billing, err := grokBillingSnapshotFromExtra(account.Extra); err == nil && billing != nil {
		if tier := strings.TrimSpace(billing.Plan); tier != "" {
			if isGrokFreeSubscriptionTier(tier) {
				freeSignal = true
			} else if !isGrokUnknownSubscriptionTier(tier) {
				paidSignal = true
			}
		}
		// 用量百分比或月度美元上限可以证明账号属于付费计划。
		if billing.UsagePercent != nil || billing.UsedPercent != nil ||
			(billing.MonthlyLimitCents != nil && *billing.MonthlyLimitCents > 0) {
			paidSignal = true
		}
		// xAI 会故意为 Free 账号返回空 plan，只有付费订阅才带 SuperGrok plan/月度限额。
		// 因此，成功且没有付费信号的月度计费观测是 Free 的正向证据，而不是未知层级；
		// 部分探测仍按关闭策略处理。
		if strings.TrimSpace(billing.MonthlyUpdatedAt) != "" ||
			(billing.StatusCode >= http.StatusOK && billing.StatusCode < http.StatusMultipleChoices &&
				!billing.Partial && len(billing.FailedWindows) == 0) {
			inferredFreeSignal = true
		}
	}
	if snapshot, err := grokQuotaSnapshotFromExtra(account.Extra); err == nil && snapshot != nil {
		if tier := strings.TrimSpace(snapshot.SubscriptionTier); tier != "" {
			if isGrokFreeSubscriptionTier(tier) {
				freeSignal = true
			} else if !isGrokUnknownSubscriptionTier(tier) {
				paidSignal = true
			}
		}
		if snapshot.Tokens != nil && snapshot.Tokens.Limit != nil &&
			nativegrok.IsGrokFreeRolling24hTokenLimit(*snapshot.Tokens.Limit) {
			inferredFreeSignal = true
		}
	}
	// 此处仅凭证中的 subscription_tier 具有权威性，不采用 plan_type 或扩展字段。
	if tier := strings.TrimSpace(account.GetCredential("subscription_tier")); tier != "" {
		if isGrokFreeSubscriptionTier(tier) {
			freeSignal = true
		} else if !isGrokUnknownSubscriptionTier(tier) {
			paidSignal = true
		}
	}
	// 明确的付费证据始终覆盖推断的 Free 信号，避免已升级但快照陈旧的账号仍携带历史
	// 200 万 Free token 限额而被误判。
	return !paidSignal && (freeSignal || inferredFreeSignal)
}

func isGrokFreeSubscriptionTier(tier string) bool {
	switch nativegrok.NormalizeSubscriptionTier(tier) {
	case "free", "x_basic":
		return true
	default:
		return false
	}
}

func isGrokUnknownSubscriptionTier(tier string) bool {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "", "unknown", "n/a", "none":
		return true
	default:
		return false
	}
}

func appendMissingGrokFreeCacheNativeTools(body []byte) ([]byte, error) {
	return nativegrok.AppendMissingGrokFreeCacheNativeTools(body)
}

func applyGrokCacheHeaders(headers http.Header, identity string) {
	nativegrok.ApplyGrokCacheHeaders(headers, identity)
}

func stripGrokChatPromptCacheKey(body []byte) ([]byte, error) {
	return nativegrok.StripGrokChatPromptCacheKey(body)
}

// 请求读取留在适配器；原种子 helper 只在平台实际选择该分支时调用。
func grokCacheInput(c *gin.Context, explicitKey, model string) nativegrok.CacheIdentityInput {
	input := nativegrok.CacheIdentityInput{APIKeyID: getAPIKeyIDFromContext(c), Compact: isOpenAIResponsesCompactPath(c), Model: model, ExplicitKey: explicitKey, StablePrefixSeed: deriveOpenAIStablePrefixSessionSeed, AnchoredSeed: deriveOpenAIAnchoredContentSessionSeed, PreviousResponseSeed: grokPreviousResponseSessionSeed}
	if c != nil {
		input.ClaudeSession = c.GetHeader(claudeCodeSessionHeader)
		input.HeaderSession = explicitOpenAIHeaderSessionID(c)
		input.ConversationID = c.GetHeader(grokConversationIDHeader)
	}
	return input
}
