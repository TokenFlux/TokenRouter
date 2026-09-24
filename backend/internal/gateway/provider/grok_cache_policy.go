package provider

import (
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

const grokClientToolCacheOptInExtraKey = "grok_client_tool_cache_enabled"

// ApplyGrokFreeMessagesFunctionToolCacheRoute 只为已知 Free 账号启用 xAI 可缓存的
// 混合工具路由。纯客户端工具默认启用，运维人员可在原生搜索工具会改变预期行为时
// 按账号明确关闭（#4486）。
func ApplyGrokFreeMessagesFunctionToolCacheRoute(body, intentSourceBody []byte, account *ExecutionAccount, cacheIdentity string) ([]byte, error) {
	allowPureClientTools, _ := GrokClientToolCacheAccountPolicy(account)
	return ApplyGrokFreeToolCacheRoute(body, intentSourceBody, account, cacheIdentity, allowPureClientTools, true)
}

// GrokClientToolCacheAccountPolicy 严格要求配置值为 JSON 布尔值。缺少键时仅对已确认的
// Grok Free OAuth 账号默认启用；付费、API Key 和未知账号保持关闭。
func GrokClientToolCacheAccountPolicy(account *ExecutionAccount) (enabled, explicit bool) {
	if !IsKnownGrokFreeAccount(account) {
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

func ApplyGrokFreeToolCacheRoute(body, intentSourceBody []byte, account *ExecutionAccount, cacheIdentity string, allowPureClientTools, allowFunctionSearch bool) ([]byte, error) {
	if strings.TrimSpace(cacheIdentity) == "" {
		return body, nil
	}
	return grok.ApplyGrokFreeToolCacheRoute(body, intentSourceBody, IsKnownGrokFreeAccount(account), cacheIdentity, allowPureClientTools, allowFunctionSearch)
}

// IsKnownGrokFreeAccount 识别免费层 Grok 账号，用于免费缓存路由与媒体 free_tier 阻断，
// 其覆盖范围比软性门禁更广；软性门禁使用 isExplicitGrokFreeOAuthAccount，且只匹配明确的 free。
func IsKnownGrokFreeAccount(account *ExecutionAccount) bool {
	return accountcore.KnownGrokFreeAccount(ExecutionRecord(account), accountprovider.GrokTierRules())
}
