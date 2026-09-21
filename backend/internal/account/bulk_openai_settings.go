// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	fmt "fmt"
	strconv "strconv"
	strings "strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// bulkOpenAISettings 描述批量 OpenAI API Key 配置中需要统一校验的字段。
type bulkOpenAISettings struct {
	workloadCapabilities    bool
	textRouteMode           bool
	continuationSupported   bool
	capabilitiesIncludeText bool
	forcedTextRoute         bool
}

func (s bulkOpenAISettings) any() bool {
	return s.workloadCapabilities || s.textRouteMode || s.continuationSupported
}

// normalizeBulkOpenAISettings 严格校验批量配置，避免部分账号写入无法路由的组合。
// long-context 计费开关不属于 fork 的账号配置，因此这里不会识别或生成该字段。
func normalizeBulkOpenAISettings(input *BulkUpdateAccountsInput) (bulkOpenAISettings, error) {
	var settings bulkOpenAISettings
	if input == nil {
		return settings, nil
	}

	if raw, exists := input.Credentials[OpenAIWorkloadCapabilitiesCredentialKey]; exists {
		settings.workloadCapabilities = true
		includeText, err := validateBulkOpenAIWorkloadCapabilities(raw)
		if err != nil {
			return settings, err
		}
		settings.capabilitiesIncludeText = includeText
	}
	if raw, exists := input.Credentials[LegacyOpenAICapabilitiesCredentialKey]; exists {
		settings.workloadCapabilities = true
		includeText, err := validateBulkOpenAIWorkloadCapabilities(raw)
		if err != nil {
			return settings, err
		}
		settings.capabilitiesIncludeText = includeText
	}

	if raw, exists := input.Extra[ExtraKeyTextRouteMode]; exists {
		settings.textRouteMode = true
		forced, err := validateBulkOpenAITextRouteMode(raw, false)
		if err != nil {
			return settings, err
		}
		settings.forcedTextRoute = forced
	}
	if raw, exists := input.Extra[LegacyOpenAIResponsesModeExtraKey]; exists {
		settings.textRouteMode = true
		forced, err := validateBulkOpenAITextRouteMode(raw, true)
		if err != nil {
			return settings, err
		}
		settings.forcedTextRoute = forced
	}
	if raw, exists := input.Extra[ExtraKeyResponsesContinuationSupported]; exists {
		settings.continuationSupported = true
		if err := validateBulkOpenAIResponsesContinuationSupported(raw); err != nil {
			return settings, err
		}
	}

	if settings.workloadCapabilities && !settings.capabilitiesIncludeText {
		if settings.forcedTextRoute {
			return settings, infraerrors.BadRequest(
				"OPENAI_TEXT_ROUTE_MODE_INVALID",
				"a forced text route requires the text_generation workload capability",
			)
		}
		if input.Extra == nil {
			input.Extra = make(map[string]any, 1)
		}
		// 仅保留 embeddings 时清除旧的强制文本路由，避免更新后账号仍被选中转发文本请求。
		input.Extra[ExtraKeyTextRouteMode] = string(TextRouteModePreserveClientProtocol)
		settings.textRouteMode = true
	}
	return settings, nil
}

func validateBulkOpenAIWorkloadCapabilities(raw any) (bool, error) {
	if raw == nil {
		return true, nil
	}
	selected := make(map[string]bool, 2)
	add := func(value string) error {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case string(OpenAIEndpointCapabilityTextGeneration), "chat_completions":
			selected[string(OpenAIEndpointCapabilityTextGeneration)] = true
		case string(OpenAIEndpointCapabilityEmbeddings):
			selected[string(OpenAIEndpointCapabilityEmbeddings)] = true
		default:
			return invalidBulkOpenAIWorkloadCapabilities()
		}
		return nil
	}

	switch typed := raw.(type) {
	case []any:
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				return false, invalidBulkOpenAIWorkloadCapabilities()
			}
			if err := add(value); err != nil {
				return false, err
			}
		}
	case []string:
		for _, value := range typed {
			if err := add(value); err != nil {
				return false, err
			}
		}
	case map[string]any:
		for key, value := range typed {
			selectedValue, ok := value.(bool)
			if !ok {
				return false, invalidBulkOpenAIWorkloadCapabilities()
			}
			if !selectedValue {
				continue
			}
			if err := add(key); err != nil {
				return false, err
			}
		}
	case map[string]bool:
		for key, selectedValue := range typed {
			if !selectedValue {
				continue
			}
			if err := add(key); err != nil {
				return false, err
			}
		}
	default:
		return false, invalidBulkOpenAIWorkloadCapabilities()
	}
	if len(selected) == 0 {
		return false, invalidBulkOpenAIWorkloadCapabilities()
	}
	return selected[string(OpenAIEndpointCapabilityTextGeneration)], nil
}

func invalidBulkOpenAIWorkloadCapabilities() error {
	return infraerrors.BadRequest(
		"OPENAI_WORKLOAD_CAPABILITIES_INVALID",
		"openai_workload_capabilities must contain text_generation, embeddings, or both",
	)
}

func validateBulkOpenAITextRouteMode(raw any, legacy bool) (bool, error) {
	if raw == nil {
		return false, nil
	}
	mode, ok := raw.(string)
	if !ok {
		return false, invalidBulkOpenAITextRouteMode()
	}
	if legacy && mode == "auto" {
		return false, nil
	}
	switch TextRouteMode(mode) {
	case TextRouteModeForceResponses, TextRouteModeForceChatCompletions:
		return true, nil
	case TextRouteModePreserveClientProtocol:
		return false, nil
	default:
		return false, invalidBulkOpenAITextRouteMode()
	}
}

func invalidBulkOpenAITextRouteMode() error {
	return infraerrors.BadRequest(
		"OPENAI_TEXT_ROUTE_MODE_INVALID",
		"openai_text_route_mode must be preserve_client_protocol, force_responses, force_chat_completions, or null",
	)
}

func validateBulkOpenAIResponsesContinuationSupported(raw any) error {
	if raw == nil {
		return nil
	}
	if _, ok := raw.(bool); !ok {
		return infraerrors.BadRequest(
			"OPENAI_RESPONSES_CONTINUATION_INVALID",
			"openai_responses_continuation_supported must be a boolean or null",
		)
	}
	return nil
}

// validateBulkOpenAISettingsTargets 在任何批量写入前检查所有目标，避免漏查 ID 或混入非 API Key。
func validateBulkOpenAISettingsTargets(input *BulkUpdateAccountsInput, settings bulkOpenAISettings, targetsByID map[int64]*Record) error {
	if input == nil || !settings.any() {
		return nil
	}
	for _, accountID := range input.AccountIDs {
		account, ok := targetsByID[accountID]
		if !ok || account == nil {
			return invalidBulkOpenAITarget(accountID, "account does not exist")
		}
		if !IsOpenAIAPIKeyAccount(account) {
			return invalidBulkOpenAITarget(accountID, "workload capabilities, text route and continuation settings require an OpenAI API-key account")
		}
		if settings.forcedTextRoute && !settings.capabilitiesIncludeText && !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityTextGeneration, nil) {
			return invalidBulkOpenAITarget(accountID, "a forced text route requires the text_generation workload capability")
		}
	}
	return nil
}

func invalidBulkOpenAITarget(accountID int64, message string) error {
	return infraerrors.BadRequest(
		"OPENAI_CONFIGURATION_TARGET_INVALID",
		fmt.Sprintf("account %d: %s", accountID, message),
	).WithMetadata(map[string]string{"account_id": strconv.FormatInt(accountID, 10)})
}
