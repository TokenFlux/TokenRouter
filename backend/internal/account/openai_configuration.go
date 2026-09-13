// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	maps "maps"
	strings "strings"
)

const (
	LegacyOpenAICapabilitiesCredentialKey = "openai_capabilities"
	LegacyOpenAIResponsesModeExtraKey     = "openai_responses_mode"
)

func IsOpenAIAPIKeyAccount(account *Record) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey
}

func HasOpenAIConfigurationPatch(credentials, extra map[string]any) bool {
	if credentials != nil {
		if _, ok := credentials[OpenAIWorkloadCapabilitiesCredentialKey]; ok {
			return true
		}
		if _, ok := credentials[LegacyOpenAICapabilitiesCredentialKey]; ok {
			return true
		}
	}
	if extra == nil {
		return false
	}
	for _, key := range []string{
		ExtraKeyTextRouteMode,
		ExtraKeyResponsesContinuationSupported,
		LegacyOpenAIResponsesModeExtraKey,
	} {
		if _, ok := extra[key]; ok {
			return true
		}
	}
	return false
}

// NormalizeOpenAIAPIKeyConfiguration 将完整账号配置规范化为唯一的新持久化形状。
func NormalizeOpenAIAPIKeyConfiguration(account *Record) error {
	if account != nil && account.IsOpenAI() {
		account.Extra = maps.Clone(account.Extra)
		if account.Extra == nil {
			account.Extra = map[string]any{}
		}
		DiscardDeprecatedAccountExtra(account.Extra)
		account.Extra["openai_compact_mode"] = account.GetOpenAICompactMode()
		account.Extra[OpenAINativeCompactionV2ModeExtraKey] = account.GetOpenAINativeCompactionV2Mode()
	}
	if !IsOpenAIAPIKeyAccount(account) {
		return nil
	}

	account.Credentials = maps.Clone(account.Credentials)
	if account.Credentials == nil {
		account.Credentials = make(map[string]any)
	}
	account.Extra = maps.Clone(account.Extra)
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}

	_, unified := account.Credentials[UpstreamProtocolsKey]
	if err := NormalizeOpenAIWorkloadCapabilities(account.Credentials, !unified); err != nil {
		return err
	}
	if err := NormalizeOpenAITextRouteMode(account.Extra, !unified); err != nil {
		return err
	}
	DiscardDeprecatedAccountExtra(account.Extra)
	if err := NormalizeOpenAIResponsesContinuationSupported(account.Extra, true); err != nil {
		return err
	}
	return nil
}

// NormalizeOpenAIAPIKeyConfigurationPatch 只规范化增量中显式出现的字段。
func NormalizeOpenAIAPIKeyConfigurationPatch(credentials, extra map[string]any) error {
	// 增量中出现旧键表示旧客户端正在主动修改该项；即使它回传了不认识的新键，
	// 也应让本次旧字段修改生效。完整持久化数据仍由全量规范化优先采用新键。
	if _, found := credentials[LegacyOpenAICapabilitiesCredentialKey]; found {
		delete(credentials, OpenAIWorkloadCapabilitiesCredentialKey)
	}
	if _, found := extra[LegacyOpenAIResponsesModeExtraKey]; found {
		delete(extra, ExtraKeyTextRouteMode)
	}
	if err := NormalizeOpenAIWorkloadCapabilities(credentials, false); err != nil {
		return err
	}
	if err := NormalizeOpenAITextRouteMode(extra, false); err != nil {
		return err
	}
	DiscardDeprecatedAccountExtra(extra)
	if err := NormalizeOpenAIResponsesContinuationSupported(extra, false); err != nil {
		return err
	}
	return nil
}

func NormalizeOpenAIWorkloadCapabilities(credentials map[string]any, applyDefault bool) error {
	if credentials == nil {
		return nil
	}
	raw, found := credentials[OpenAIWorkloadCapabilitiesCredentialKey]
	if !found {
		raw, found = credentials[LegacyOpenAICapabilitiesCredentialKey]
	}
	if !found && !applyDefault {
		return nil
	}
	delete(credentials, LegacyOpenAICapabilitiesCredentialKey)
	if !found || raw == nil {
		credentials[OpenAIWorkloadCapabilitiesCredentialKey] = []string{
			string(OpenAIEndpointCapabilityTextGeneration),
			string(OpenAIEndpointCapabilityEmbeddings),
		}
		return nil
	}

	enabled := make(map[string]bool, 2)
	add := func(value string) {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case string(OpenAIEndpointCapabilityTextGeneration), "chat_completions":
			enabled[string(OpenAIEndpointCapabilityTextGeneration)] = true
		case string(OpenAIEndpointCapabilityEmbeddings):
			enabled[string(OpenAIEndpointCapabilityEmbeddings)] = true
		}
	}
	switch capabilities := raw.(type) {
	case []any:
		for _, item := range capabilities {
			if value, ok := item.(string); ok {
				add(value)
			}
		}
	case []string:
		for _, value := range capabilities {
			add(value)
		}
	case map[string]any:
		for key, value := range capabilities {
			if selected, ok := value.(bool); ok && selected {
				add(key)
			}
		}
	case map[string]bool:
		for key, selected := range capabilities {
			if selected {
				add(key)
			}
		}
	default:
		return infraerrors.BadRequest(
			"OPENAI_WORKLOAD_CAPABILITIES_INVALID",
			"openai_workload_capabilities must be an array or object",
		)
	}

	normalized := make([]string, 0, 2)
	for _, capability := range []OpenAIEndpointCapability{
		OpenAIEndpointCapabilityTextGeneration,
		OpenAIEndpointCapabilityEmbeddings,
	} {
		if enabled[string(capability)] {
			normalized = append(normalized, string(capability))
		}
	}
	credentials[OpenAIWorkloadCapabilitiesCredentialKey] = normalized
	return nil
}

func NormalizeOpenAITextRouteMode(extra map[string]any, applyDefault bool) error {
	if extra == nil {
		return nil
	}
	raw, found := extra[ExtraKeyTextRouteMode]
	usingLegacy := false
	if !found {
		raw, found = extra[LegacyOpenAIResponsesModeExtraKey]
		usingLegacy = found
	}
	if !found && !applyDefault {
		return nil
	}
	delete(extra, LegacyOpenAIResponsesModeExtraKey)

	mode := TextRouteModePreserveClientProtocol
	if found {
		value, ok := raw.(string)
		if !ok {
			if !usingLegacy {
				return infraerrors.BadRequest(
					"OPENAI_TEXT_ROUTE_MODE_INVALID",
					"openai_text_route_mode must be a valid string",
				)
			}
		} else if usingLegacy {
			switch value {
			case string(TextRouteModeForceResponses):
				mode = TextRouteModeForceResponses
			case string(TextRouteModeForceChatCompletions):
				mode = TextRouteModeForceChatCompletions
			}
		} else {
			mode = NormalizeTextRouteMode(value)
			if value != string(mode) {
				return infraerrors.BadRequest(
					"OPENAI_TEXT_ROUTE_MODE_INVALID",
					"openai_text_route_mode is invalid",
				)
			}
		}
	}
	extra[ExtraKeyTextRouteMode] = string(mode)
	return nil
}

// NormalizeOpenAIResponsesContinuationSupported 规范化管理员维护的 HTTP continuation 能力开关。
func NormalizeOpenAIResponsesContinuationSupported(extra map[string]any, applyDefault bool) error {
	if extra == nil {
		return nil
	}
	raw, found := extra[ExtraKeyResponsesContinuationSupported]
	if !found && !applyDefault {
		return nil
	}
	if !found || raw == nil {
		extra[ExtraKeyResponsesContinuationSupported] = false
		return nil
	}
	supported, ok := raw.(bool)
	if !ok {
		return infraerrors.BadRequest(
			"OPENAI_RESPONSES_CONTINUATION_INVALID",
			"openai_responses_continuation_supported must be a boolean or null",
		)
	}
	extra[ExtraKeyResponsesContinuationSupported] = supported
	return nil
}
