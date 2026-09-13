// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	maps "maps"
	strings "strings"
)

func NormalizeAccountConcurrency(platform, accountType string, concurrency int) int {
	if platform == PlatformGrok && accountType == AccountTypeOAuth {
		if concurrency <= 0 {
			return 1
		}
	}
	return concurrency
}

// NormalizeCNProviderCredentials 校验国产供应商账号组合，并为新账号补齐历史默认值。
// 旧记录缺少 mode/protocol 时由 Account 方法按 payg + chat_completions 读取，避免无关编辑
// 把兼容数据强制改写；新建记录则显式保存默认值，方便前端和监控选择适配器。
// @project-doc docs/interfaces/upstream_account_matrix.md#cn_provider_protocols
func NormalizeCNProviderCredentials(account *Record, isCreate bool) error {
	if account == nil || !IsCNProvider(account.Platform) {
		return nil
	}
	if account.Type != AccountTypeAPIKey {
		return infraerrors.BadRequest("CN_PROVIDER_ACCOUNT_TYPE_INVALID", "CN provider accounts must use API Key credentials")
	}
	if account.Credentials == nil {
		account.Credentials = make(map[string]any)
	}
	mode, _ := account.Credentials["account_mode"].(string)
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = AccountModePayG
		if isCreate {
			account.Credentials["account_mode"] = mode
		}
	}
	if mode != AccountModePayG && mode != AccountModeCoding {
		return infraerrors.BadRequest("CN_PROVIDER_ACCOUNT_MODE_INVALID", "account_mode must be payg or coding")
	}
	protocol, _ := account.Credentials["api_protocol"].(string)
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		protocol = APIProtocolChatCompletions
		if _, unified := account.Credentials[UpstreamProtocolsKey]; isCreate && !unified {
			account.Credentials["api_protocol"] = protocol
		}
	}
	switch protocol {
	case APIProtocolAdaptive, APIProtocolChatCompletions, APIProtocolAnthropic:
	case APIProtocolResponses:
		// 保存校验与转发共用平台能力，避免前端可选协议被旧白名单拒绝。
		if !account.SupportsNativeCNResponses() {
			return infraerrors.BadRequest("CN_PROVIDER_PROTOCOL_INVALID", "only DeepSeek and Kimi support Responses protocol")
		}
	default:
		return infraerrors.BadRequest("CN_PROVIDER_PROTOCOL_INVALID", "api_protocol is unsupported")
	}
	if mode == AccountModeCoding && account.Platform == PlatformDeepseek {
		return infraerrors.BadRequest("CN_PROVIDER_MODE_INVALID", "DeepSeek does not support coding plan mode")
	}
	return nil
}

// ValidateGrokMediaEligibilityExtra 校验可选的媒体调度覆盖；null 表示删除覆盖，
// 让账号恢复为根据上游观测自动判断。
func ValidateGrokMediaEligibilityExtra(platform string, extra map[string]any) error {
	if platform != PlatformGrok || extra == nil {
		return nil
	}
	raw, exists := extra[GrokMediaEligibleExtraKey]
	if !exists || raw == nil {
		return nil
	}
	if _, ok := raw.(bool); !ok {
		return infraerrors.BadRequest(
			"GROK_MEDIA_ELIGIBILITY_INVALID",
			"grok_media_eligible must be a boolean or null",
		)
	}
	return nil
}

func NormalizeGrokMediaEligibilityExtra(platform string, extra map[string]any) (map[string]any, error) {
	if platform != PlatformGrok {
		return extra, nil
	}
	if err := ValidateGrokMediaEligibilityExtra(platform, extra); err != nil {
		return nil, err
	}
	normalized := maps.Clone(extra)
	if normalized != nil && normalized[GrokMediaEligibleExtraKey] == nil {
		delete(normalized, GrokMediaEligibleExtraKey)
	}
	return normalized, nil
}

func NormalizeGrokMediaEligibilityUpdateExtra(account *Record, input *UpdateAccountInput, normalized map[string]any) (map[string]any, error) {
	if account == nil || account.Platform != PlatformGrok {
		return normalized, nil
	}
	if err := ValidateGrokMediaEligibilityExtra(account.Platform, input.Extra); err != nil {
		return nil, err
	}
	normalized = maps.Clone(normalized)
	if normalized == nil {
		normalized = make(map[string]any)
	}
	raw, provided := input.Extra[GrokMediaEligibleExtraKey]
	if provided {
		if raw == nil {
			delete(normalized, GrokMediaEligibleExtraKey)
		}
		return normalized, nil
	}
	if current, ok := account.Extra[GrokMediaEligibleExtraKey].(bool); ok {
		normalized[GrokMediaEligibleExtraKey] = current
	}
	return normalized, nil
}

// ValidateGeminiThirdPartyBaseURL 阻止第三方来源回退到官方 Gemini 默认端点。
func ValidateGeminiThirdPartyBaseURL(account *Record) error {
	if account == nil || !account.IsGeminiThirdPartyProvider() || account.HasGeminiThirdPartyBaseURL() {
		return nil
	}
	return infraerrors.BadRequest(
		"GEMINI_THIRD_PARTY_BASE_URL_REQUIRED",
		"Gemini third-party API Key accounts require a non-Google base_url",
	)
}
