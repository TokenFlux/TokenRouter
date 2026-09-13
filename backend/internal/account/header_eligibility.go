// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

const credKeyHeaderOverrideEnabled = "header_override_enabled"

// IsHeaderOverrideEligible 报告账号类型是否支持请求头覆写。
// Anthropic / OpenAI / 国产供应商仅开放 api_key 账号；Grok 额外开放 oauth 账号——
// 订阅流量改发自定义转发地址时，通常需要补充中间层要求的准入头。
func (a *Record) IsHeaderOverrideEligible() bool {
	if a == nil {
		return false
	}
	switch a.Platform {
	case PlatformAnthropic, PlatformOpenAI, PlatformKimi, PlatformZhipu, PlatformDeepseek:
		return a.Type == AccountTypeAPIKey
	case PlatformGrok:
		return a.Type == AccountTypeAPIKey || a.Type == AccountTypeOAuth
	default:
		return false
	}
}

// IsHeaderOverrideEnabled 报告账号是否启用了请求头覆写。
func (a *Record) IsHeaderOverrideEnabled() bool {
	if !a.IsHeaderOverrideEligible() || a.Credentials == nil {
		return false
	}
	enabled, ok := a.Credentials[credKeyHeaderOverrideEnabled].(bool)
	return ok && enabled
}
