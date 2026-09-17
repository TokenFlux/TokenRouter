package gateway

import (
	"context"
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// FastSettingsParticipant 复用纯档位规则，省略或 null 均保持原配置。
func FastSettingsParticipant() settings.Participant {
	return settings.Participant{Module: "gateway", Fields: []string{"openai_fast_policy_settings"}, Keys: []string{SettingKeyOpenAIFastPolicySettings}, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		raw, ok := input["openai_fast_policy_settings"]
		if !ok || string(raw) == "null" {
			return settings.PreparedChange{}, nil
		}
		var value tierpolicy.OpenAIFastPolicySettings
		if err := json.Unmarshal(raw, &value); err != nil {
			return settings.PreparedChange{}, apperror.BadRequest("", err.Error())
		}
		prepared, err := tierpolicy.Prepare(&value)
		if err != nil {
			return settings.PreparedChange{}, apperror.BadRequest("", err.Error())
		}
		return settings.PreparedChange{Values: map[string]string{SettingKeyOpenAIFastPolicySettings: prepared}}, nil
	}}
}
