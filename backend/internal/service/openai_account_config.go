package service

import (
	"maps"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
)

const (
	legacyOpenAICapabilitiesCredentialKey  = "openai_capabilities"
	legacyOpenAIResponsesModeExtraKey      = "openai_responses_mode"
	legacyOpenAIResponsesSupportedExtraKey = "openai_responses_supported"
)

func isOpenAIAPIKeyAccount(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeAPIKey
}

func hasOpenAIConfigurationPatch(credentials, extra map[string]any) bool {
	if credentials != nil {
		if _, ok := credentials[openAIWorkloadCapabilitiesCredentialKey]; ok {
			return true
		}
		if _, ok := credentials[legacyOpenAICapabilitiesCredentialKey]; ok {
			return true
		}
	}
	if extra == nil {
		return false
	}
	for _, key := range []string{
		openai_compat.ExtraKeyTextRouteMode,
		openai_compat.ExtraKeyResponsesProbeStatus,
		openai_compat.ExtraKeyResponsesContinuationSupported,
		legacyOpenAIResponsesModeExtraKey,
		legacyOpenAIResponsesSupportedExtraKey,
	} {
		if _, ok := extra[key]; ok {
			return true
		}
	}
	return false
}

// normalizeOpenAIAPIKeyConfiguration 将完整账号配置规范化为唯一的新持久化形状。
func normalizeOpenAIAPIKeyConfiguration(account *Account) error {
	if account != nil && account.IsOpenAI() {
		account.Extra = maps.Clone(account.Extra)
		if account.Extra == nil {
			account.Extra = map[string]any{}
		}
		DiscardDeprecatedAccountExtra(account.Extra)
		account.Extra["openai_compact_mode"] = account.GetOpenAICompactMode()
		account.Extra[openAINativeCompactionV2ModeExtraKey] = account.GetOpenAINativeCompactionV2Mode()
	}
	if !isOpenAIAPIKeyAccount(account) {
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

	if err := normalizeOpenAIWorkloadCapabilities(account.Credentials, true); err != nil {
		return err
	}
	if err := normalizeOpenAITextRouteMode(account.Extra, true); err != nil {
		return err
	}
	DiscardDeprecatedAccountExtra(account.Extra)
	if err := normalizeOpenAIResponsesContinuationSupported(account.Extra, true); err != nil {
		return err
	}
	return nil
}

// normalizeOpenAIAPIKeyConfigurationPatch 只规范化增量中显式出现的字段。
func normalizeOpenAIAPIKeyConfigurationPatch(credentials, extra map[string]any) error {
	// 增量中出现旧键表示旧客户端正在主动修改该项；即使它回传了不认识的新键，
	// 也应让本次旧字段修改生效。完整持久化数据仍由全量规范化优先采用新键。
	if _, found := credentials[legacyOpenAICapabilitiesCredentialKey]; found {
		delete(credentials, openAIWorkloadCapabilitiesCredentialKey)
	}
	if _, found := extra[legacyOpenAIResponsesModeExtraKey]; found {
		delete(extra, openai_compat.ExtraKeyTextRouteMode)
	}
	if _, found := extra[legacyOpenAIResponsesSupportedExtraKey]; found {
		delete(extra, openai_compat.ExtraKeyResponsesProbeStatus)
	}
	if err := normalizeOpenAIWorkloadCapabilities(credentials, false); err != nil {
		return err
	}
	if err := normalizeOpenAITextRouteMode(extra, false); err != nil {
		return err
	}
	DiscardDeprecatedAccountExtra(extra)
	if err := normalizeOpenAIResponsesContinuationSupported(extra, false); err != nil {
		return err
	}
	return nil
}

func normalizeOpenAIWorkloadCapabilities(credentials map[string]any, applyDefault bool) error {
	if credentials == nil {
		return nil
	}
	raw, found := credentials[openAIWorkloadCapabilitiesCredentialKey]
	if !found {
		raw, found = credentials[legacyOpenAICapabilitiesCredentialKey]
	}
	if !found && !applyDefault {
		return nil
	}
	delete(credentials, legacyOpenAICapabilitiesCredentialKey)
	if !found || raw == nil {
		credentials[openAIWorkloadCapabilitiesCredentialKey] = []string{
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
	credentials[openAIWorkloadCapabilitiesCredentialKey] = normalized
	return nil
}

func normalizeOpenAITextRouteMode(extra map[string]any, applyDefault bool) error {
	if extra == nil {
		return nil
	}
	raw, found := extra[openai_compat.ExtraKeyTextRouteMode]
	usingLegacy := false
	if !found {
		raw, found = extra[legacyOpenAIResponsesModeExtraKey]
		usingLegacy = found
	}
	if !found && !applyDefault {
		return nil
	}
	delete(extra, legacyOpenAIResponsesModeExtraKey)

	mode := openai_compat.TextRouteModePreserveClientProtocol
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
			case string(openai_compat.TextRouteModeForceResponses):
				mode = openai_compat.TextRouteModeForceResponses
			case string(openai_compat.TextRouteModeForceChatCompletions):
				mode = openai_compat.TextRouteModeForceChatCompletions
			}
		} else {
			mode = openai_compat.NormalizeTextRouteMode(value)
			if value != string(mode) {
				return infraerrors.BadRequest(
					"OPENAI_TEXT_ROUTE_MODE_INVALID",
					"openai_text_route_mode is invalid",
				)
			}
		}
	}
	extra[openai_compat.ExtraKeyTextRouteMode] = string(mode)
	return nil
}

// normalizeOpenAIResponsesContinuationSupported 规范化管理员维护的 HTTP continuation 能力开关。
func normalizeOpenAIResponsesContinuationSupported(extra map[string]any, applyDefault bool) error {
	if extra == nil {
		return nil
	}
	raw, found := extra[openai_compat.ExtraKeyResponsesContinuationSupported]
	if !found && !applyDefault {
		return nil
	}
	if !found || raw == nil {
		extra[openai_compat.ExtraKeyResponsesContinuationSupported] = false
		return nil
	}
	supported, ok := raw.(bool)
	if !ok {
		return infraerrors.BadRequest(
			"OPENAI_RESPONSES_CONTINUATION_INVALID",
			"openai_responses_continuation_supported must be a boolean or null",
		)
	}
	extra[openai_compat.ExtraKeyResponsesContinuationSupported] = supported
	return nil
}
