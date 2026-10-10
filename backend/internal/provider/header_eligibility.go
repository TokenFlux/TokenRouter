package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/egress"
)

const credKeyHeaderOverrideEnabled = "header_override_enabled"

// IsHeaderOverrideEligible 报告提供商类型是否支持请求头覆写。
// Anthropic、OpenAI、国产供应商和 Jev 向 API Key 提供商开放 Header 覆盖，Grok 还支持 OAuth 提供商。
// 订阅流量改发自定义转发地址时，通常需要补充中间层要求的准入头。
func (r *Record) IsHeaderOverrideEligible() bool {
	if r == nil {
		return false
	}
	switch r.Platform {
	case PlatformAnthropic, PlatformOpenAI, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformJev:
		return r.Type == ProviderTypeAPIKey
	case PlatformGrok:
		return r.Type == ProviderTypeAPIKey || r.Type == ProviderTypeOAuth
	default:
		return false
	}
}

// IsHeaderOverrideEnabled 报告提供商是否启用了请求头覆写。
func (r *Record) IsHeaderOverrideEnabled() bool {
	if !r.IsHeaderOverrideEligible() || r.Credentials == nil {
		return false
	}
	enabled, ok := r.Credentials[credKeyHeaderOverrideEnabled].(bool)
	return ok && enabled
}

// HeaderOverrides 由提供商决定适用性，名称/值安全规则由 egress 唯一执行。
// 返回的 map 是 credentials 中 Header 配置的独立副本。
func (r *Record) HeaderOverrides() map[string]string {
	if !r.IsHeaderOverrideEnabled() {
		return nil
	}
	return egress.ResolveHeaderOverrides(StringMappingFromRaw(r.Credentials["header_overrides"]))
}
