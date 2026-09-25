// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"strings"
)

// GetOpenAIBaseURL 解析 OpenAI 协议族账号的上游 base_url。
// 适用 openai 与国产 OpenAI 兼容供应商（kimi/zhipu/deepseek）；grok 走 GetGrokBaseURL，
// 此处对 grok 返回 "" 以保持原有行为。
func (a *Record) OpenAIBaseURL(adaptive bool) string {
	if !a.IsOpenAI() && !a.IsCNProvider() {
		return ""
	}
	if _, unified := a.Credentials[UpstreamProtocolsKey]; a.IsCNProvider() && (unified || adaptive) {
		if baseURLs, ok := a.Credentials["api_base_urls"].(map[string]any); ok {
			if baseURL, ok := baseURLs[APIProtocolChatCompletions].(string); ok && strings.TrimSpace(baseURL) != "" {
				return strings.TrimSpace(baseURL)
			}
		}
	}
	if a.Type == AccountTypeAPIKey || a.Type == AccountTypeUpstream {
		if baseURL := strings.TrimSpace(a.GetCredential("base_url")); baseURL != "" {
			return baseURL
		}
	}
	// 平台默认 base_url：CN 供应商按 account_mode 选择 payg / coding 默认值。
	switch a.Platform {
	case PlatformKimi:
		if a.GetAccountMode() == AccountModeCoding {
			return DefaultKimiCodingBaseURL
		}
		return DefaultKimiPayGBaseURL
	case PlatformZhipu:
		if a.GetAccountMode() == AccountModeCoding {
			return DefaultZhipuCodingBaseURL
		}
		return DefaultZhipuPayGBaseURL
	case PlatformDeepseek:
		return DefaultDeepseekBaseURL
	default:
		return "https://api.openai.com"
	}
}
