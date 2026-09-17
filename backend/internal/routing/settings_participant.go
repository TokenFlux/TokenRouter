package routing

import (
	"context"
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// SettingsParticipant 准备所属键，不发布路由缓存或创建候选状态。
func SettingsParticipant() settings.Participant {
	keys := []string{SettingKeyAllowUngroupedKeyScheduling, SettingKeyEnableModelFallback, SettingKeyFallbackModelAnthropic, SettingKeyFallbackModelAntigravity, SettingKeyFallbackModelGemini, SettingKeyFallbackModelOpenAI, SettingKeyMarketplaceAvailabilityBucketMinutes, SettingKeyMarketplaceAvailabilityWindowDays}
	return settings.Participant{Module: "routing", Fields: keys, Keys: keys, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
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
		values := PrepareAdminSettings(&value)
		for key := range values {
			if _, ok := input[key]; !ok {
				delete(values, key)
			}
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}
