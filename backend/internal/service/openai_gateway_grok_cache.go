package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	grokConversationIDHeader         = "X-Grok-Conv-Id"
	claudeCodeSessionHeader          = "X-Claude-Code-Session-Id"
	grokClientToolCacheOptInHeader   = "X-Sub2API-Grok-Client-Tool-Cache"
	grokFreeCacheNativeToolsJSON     = `[{"type":"web_search"},{"type":"x_search"}]`
	grokFreeCacheDisabledToolChoice  = "none"
	grokClientToolCacheOptInExtraKey = "grok_client_tool_cache_enabled"
)

// Claude Code 的 metadata.user_id 通常以 _session_<uuid> 结尾。
var claudeCodeSessionSuffixPattern = regexp.MustCompile(`_session_([a-f0-9-]+)$`)

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
	if len(body) == 0 {
		return ""
	}
	userID := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String())
	if userID == "" {
		return ""
	}
	if matches := claudeCodeSessionSuffixPattern.FindStringSubmatch(userID); len(matches) >= 2 {
		return matches[1]
	}
	// Claude Code 也可能嵌入 JSON：{"session_id":"..."}。
	if len(userID) > 0 && userID[0] == '{' {
		if sid := strings.TrimSpace(gjson.Get(userID, "session_id").String()); sid != "" {
			return sid
		}
	}
	return ""
}

// resolveGrokCacheIdentity 为 xAI 服务端提示缓存派生稳定且租户隔离的路由身份。
// 返回值不包含客户端原始会话标识，可安全发送到上游。
//
// 必须存在有效的下游 API Key。内部探测或请求上下文不完整时主动关闭缓存，避免生成
// 可能被无关租户共享的缓存身份。
func resolveGrokCacheIdentity(c *gin.Context, body []byte, explicitKey, upstreamModel string) string {
	apiKeyID := getAPIKeyIDFromContext(c)
	if apiKeyID <= 0 {
		return ""
	}
	// /responses/compact 不接受 tool_choice，且不代表正常会话轮次；该路径不得添加
	// 缓存身份或免费层路由增强字段。
	if isOpenAIResponsesCompactPath(c) {
		return ""
	}

	model := strings.ToLower(strings.TrimSpace(upstreamModel))
	if model == "" {
		return ""
	}

	seed := explicitGrokCacheSeed(c, body, explicitKey)
	if seed == "" {
		seed = deriveOpenAIStablePrefixSessionSeed(body)
		if seed == "" {
			// 仅使用模型会让缓存路由范围过大；没有可复用前缀时回退到首个用户输入派生身份，
			// 避免无关提示共享同一个租户级缓存键。
			seed = deriveOpenAIAnchoredContentSessionSeed(body)
		}
	}
	if seed == "" {
		return ""
	}

	// generateSessionUUID 会先哈希完整种子再格式化为 UUID；加入带版本的命名空间，
	// 避免该身份与 TokenRouter 派生的其它上游会话标识冲突。
	isolatedSeed := fmt.Sprintf("grok-prompt-cache:v1:%d:%s:%s", apiKeyID, model, seed)
	return generateSessionUUID(isolatedSeed)
}

func explicitGrokCacheSeed(c *gin.Context, body []byte, explicitKey string) string {
	// Claude Code 会话是 /v1/messages 到 Grok 桥接中最稳定的多轮身份，
	// 优先于通用会话头，以便提示缓存路由与 CPA 行为一致。
	seed := extractClaudeCodeSessionID(c, body)
	if seed == "" {
		seed = explicitOpenAIHeaderSessionID(c)
	}
	if seed == "" && c != nil {
		seed = strings.TrimSpace(c.GetHeader(grokConversationIDHeader))
	}
	if seed == "" && len(body) > 0 {
		seed = strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
	}
	if seed == "" {
		seed = strings.TrimSpace(explicitKey)
	}
	// previous_response_id 是最后的回退方案：没有显式会话的多轮 Responses 仍共享缓存身份，
	// 模型已包含在隔离种子中；消息 ID 会被种子辅助函数拒绝。
	if seed == "" && len(body) > 0 {
		seed = grokPreviousResponseSessionSeed(body)
	}
	return seed
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

// applyGrokResponsesCacheIdentity 将缓存路由身份写入 xAI Responses 请求。
// 客户端已有值会被租户隔离值替换，防止共享 OAuth 账号上的缓存冲突。
//
// xAI 会把未携带原生搜索工具的免费 OAuth 请求路由到不可缓存的 build-free 模型。
// 对原本无工具的请求添加原生工具并设置 tool_choice=none，可选择支持缓存的层级而不
// 实际执行搜索；客户端明确提供的函数工具由下方混合工具策略处理，该策略覆盖
// Messages 桥接、原生 Responses 和 WS HTTP bridge。
func applyGrokResponsesCacheIdentity(body, intentSourceBody []byte, identity string, injectFreeTierTools bool) ([]byte, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		if gjson.GetBytes(body, "prompt_cache_key").Exists() {
			return sjson.DeleteBytes(body, "prompt_cache_key")
		}
		return body, nil
	}
	out, err := sjson.SetBytes(body, "prompt_cache_key", identity)
	if err != nil {
		return nil, err
	}
	if !injectFreeTierTools {
		return out, nil
	}
	// 必须检查清理前的原始请求。patchGrokResponsesBody 可能移除不受支持的客户端工具
	// 及其 tool_choice，但不能因此把明确的客户端工具意图误判为可注入原生工具。
	if hasGrokResponsesToolIntent(intentSourceBody) {
		return out, nil
	}
	out, err = sjson.SetRawBytes(out, "tools", []byte(grokFreeCacheNativeToolsJSON))
	if err != nil {
		return nil, err
	}
	return sjson.SetBytes(out, "tool_choice", grokFreeCacheDisabledToolChoice)
}

func hasGrokResponsesToolIntent(body []byte) bool {
	if gjson.GetBytes(body, "tools").Exists() || gjson.GetBytes(body, "tool_choice").Exists() {
		return true
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	for _, item := range input.Array() {
		if strings.TrimSpace(item.Get("type").String()) != "additional_tools" {
			continue
		}
		tools := item.Get("tools")
		if !tools.Exists() || !tools.IsArray() || len(tools.Array()) > 0 {
			return true
		}
	}
	return false
}

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
	if strings.TrimSpace(cacheIdentity) == "" || !isKnownGrokFreeAccount(account) {
		return body, nil
	}
	intentTools := gjson.GetBytes(intentSourceBody, "tools")
	intentToolChoice := gjson.GetBytes(intentSourceBody, "tool_choice")
	if !isGrokFreeCacheFunctionToolIntent(intentTools, intentToolChoice) {
		return body, nil
	}
	if intentToolChoice.Type == gjson.String && strings.TrimSpace(intentToolChoice.String()) == grokFreeCacheDisabledToolChoice {
		// 客户端明确禁用全部工具执行时，加入原生缓存路由工具不会改变行为。
		return appendGrokFreeCacheNativeToolsWithPolicy(body, true, false)
	}
	return appendGrokFreeCacheNativeToolsWithPolicy(body, allowPureClientTools, allowFunctionSearch)
}

// isKnownGrokFreeAccount 识别免费层 Grok 账号，用于免费缓存路由与媒体 free_tier 阻断，
// 其覆盖范围比软性门禁更广；软性门禁使用 isExplicitGrokFreeOAuthAccount，且只匹配明确的 free。
func isKnownGrokFreeAccount(account *Account) bool {
	if account == nil || !account.IsGrokOAuth() {
		return false
	}
	// 实时访问令牌 JWT 优先于陈旧的账单或凭据快照，令牌刷新后可立即反映降级到免费档位。
	if jwtTier := xai.SubscriptionTierFromJWT(account.GetCredential("access_token")); jwtTier != "" {
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
			xai.IsGrokFreeRolling24hTokenLimit(*snapshot.Tokens.Limit) {
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
	switch xai.NormalizeSubscriptionTier(tier) {
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

func isGrokFreeCacheFunctionToolIntent(tools, toolChoice gjson.Result) bool {
	if !tools.IsArray() {
		return false
	}
	items := tools.Array()
	if len(items) == 0 {
		return false
	}
	for _, tool := range items {
		if !tool.IsObject() {
			return false
		}
		toolType := strings.TrimSpace(tool.Get("type").String())
		if _, ok := grokResponsesSupportedToolTypes[toolType]; !ok {
			return false
		}
		if toolType == "function" {
			// Responses 函数声明的 name 位于顶层；拒绝 Chat Completions 嵌套结构和不完整声明。
			if strings.TrimSpace(tool.Get("name").String()) == "" || tool.Get("function").Exists() {
				return false
			}
		}
	}
	if !toolChoice.Exists() {
		return true
	}
	if toolChoice.Type != gjson.String {
		return false
	}
	switch strings.TrimSpace(toolChoice.String()) {
	case "auto", grokFreeCacheDisabledToolChoice:
		return true
	default:
		return false
	}
}

func appendMissingGrokFreeCacheNativeTools(body []byte) ([]byte, error) {
	return appendGrokFreeCacheNativeTools(body, false)
}

func appendGrokFreeCacheNativeTools(body []byte, allowPureClientTools bool) ([]byte, error) {
	return appendGrokFreeCacheNativeToolsWithPolicy(body, allowPureClientTools, true)
}

func appendGrokFreeCacheNativeToolsWithPolicy(body []byte, allowPureClientTools, allowFunctionSearch bool) ([]byte, error) {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return body, nil
	}

	items := tools.Array()
	if len(items) == 0 {
		return body, nil
	}
	hasNativeSearch := false
	for _, tool := range items {
		switch strings.TrimSpace(tool.Get("type").String()) {
		case "web_search", "x_search":
			hasNativeSearch = true
		}
	}
	if !allowPureClientTools && !allowFunctionSearch && !hasNativeSearch {
		return body, nil
	}
	merged := make([]json.RawMessage, 0, len(items)+2)
	present := make(map[string]bool, 2)
	hasCompanionTool := false
	for _, tool := range items {
		toolType := strings.TrimSpace(tool.Get("type").String())
		switch toolType {
		case "function":
			name := strings.TrimSpace(tool.Get("name").String())
			if !tool.IsObject() || name == "" || tool.Get("function").Exists() {
				return body, nil
			}
			// Grok Build 可能把搜索声明成函数工具；转换为原生工具后既能让 Free OAuth
			// 保持可缓存路由，也能避免同名工具重复。
			if (name == "web_search" || name == "x_search") && allowFunctionSearch {
				if present[name] {
					continue
				}
				raw, err := json.Marshal(map[string]string{"type": name})
				if err != nil {
					return nil, err
				}
				merged = append(merged, raw)
				present[name] = true
				if allowPureClientTools {
					hasCompanionTool = true
				}
				continue
			}
			if name == "web_search" || name == "x_search" {
				// 未明确允许转换时保留客户端函数，并避免加入同名原生工具。
				present[name] = true
			}
			hasCompanionTool = true
			merged = append(merged, json.RawMessage(tool.Raw))
		case "web_search", "x_search":
			// 重试该辅助函数时跳过已经存在的原生工具，保证转换操作幂等。
			if present[toolType] {
				continue
			}
			merged = append(merged, json.RawMessage(tool.Raw))
			present[toolType] = true
		default:
			if _, ok := grokResponsesSupportedToolTypes[toolType]; !ok {
				return body, nil
			}
			hasCompanionTool = true
			merged = append(merged, json.RawMessage(tool.Raw))
		}
	}
	if !hasCompanionTool {
		return body, nil
	}
	// 未允许纯客户端工具时，只有请求已含原生或函数形式搜索工具才补齐缺失项，
	// 避免 view_image 等工具意外改变模型的自动工具选择（#4486）。
	if !allowPureClientTools && !present["web_search"] && !present["x_search"] {
		return body, nil
	}
	for _, toolType := range []string{"web_search", "x_search"} {
		if present[toolType] {
			continue
		}
		raw, err := json.Marshal(map[string]string{"type": toolType})
		if err != nil {
			return nil, err
		}
		merged = append(merged, raw)
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "tools", encoded)
}

// applyGrokCacheHeaders 写入 Chat Completions 约定的会话路由头。请求使用全新 header
// 映射构建，因此客户端提供的 x-grok header 无法覆盖服务端派生值。
func applyGrokCacheHeaders(headers http.Header, identity string) {
	if headers == nil {
		return
	}
	identity = strings.TrimSpace(identity)
	if identity == "" {
		headers.Del(grokConversationIDHeader)
		return
	}
	headers.Set(grokConversationIDHeader, identity)
}

// stripGrokChatPromptCacheKey 在身份种子使用完毕后移除 Responses 专用字段；
// Chat Completions 通过 header 路由缓存。
func stripGrokChatPromptCacheKey(body []byte) ([]byte, error) {
	if !gjson.GetBytes(body, "prompt_cache_key").Exists() {
		return body, nil
	}
	return sjson.DeleteBytes(body, "prompt_cache_key")
}
