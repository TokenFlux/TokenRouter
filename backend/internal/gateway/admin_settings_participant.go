package gateway

import (
	"context"
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// AdminSettingsParticipant 在构造时固定纯规则，实际应用仍遵循提交后时序。
func AdminSettingsParticipant(rules AdminSettingsRules) settings.Participant {
	keys := []string{SettingKeyAntigravityUserAgentVersion, SettingKeyBackendModeEnabled, SettingKeyClaudeOAuthSystemPrompt, SettingKeyClaudeOAuthSystemPromptBlocks, SettingKeyEnableAnthropicCacheTTL1hInjection, SettingKeyEnableCCHSigning, SettingKeyEnableClaudeOAuthSystemPromptInjection, SettingKeyEnableClientDatelineNormalization, SettingKeyEnableFingerprintUnification, SettingKeyEnableIdentityPatch, SettingKeyEnableMetadataPassthrough, SettingKeyGrokCrossClientModelMapEnabled, SettingKeyGrokDefaultBaseURLMode, SettingKeyGrokDefaultTextModel, SettingKeyIdentityPatchPrompt, SettingKeyMaxClaudeCodeVersion, SettingKeyMinClaudeCodeVersion, SettingKeyOpenAIAllowClaudeCodeCodexPlugin, SettingKeyOpenAICodexUserAgent, SettingKeyOpenAITTFTMode, SettingKeyRewriteMessageCacheControl, SettingKeyUserPromptReplacementConfig}
	return settings.Participant{Module: "gateway-forwarding", Fields: keys, Keys: keys, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		if len(input) == 0 {
			return settings.PreparedChange{}, nil
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		var value AdminSettings
		if err = json.Unmarshal(raw, &value); err != nil {
			return settings.PreparedChange{}, err
		}
		values, err := PrepareAdminSettings(&value, rules)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		for key := range values {
			if _, ok := input[key]; !ok {
				delete(values, key)
			}
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}
