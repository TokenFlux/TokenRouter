package ops

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// SettingsParticipant 独占 ops_advanced_settings；账号阈值仅作为明确的子配置输入。
func SettingsParticipant() settings.Participant {
	keys := []string{"ops_monitoring_enabled", "ops_realtime_monitoring_enabled", "ops_metrics_interval_seconds", "ops_advanced_settings"}
	fields := []string{"ops_monitoring_enabled", "ops_realtime_monitoring_enabled", "ops_metrics_interval_seconds", "openai_account_quota_auto_pause"}
	return settings.Participant{Module: "ops", Fields: fields, Keys: keys, Prepare: func(_ context.Context, input settings.Fields, current map[string]string) (settings.PreparedChange, error) {
		if len(input) == 0 {
			return settings.PreparedChange{}, nil
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		var value CompositeMonitoringSettings
		if err = json.Unmarshal(raw, &value); err != nil {
			return settings.PreparedChange{}, err
		}
		values := PrepareMonitoringSettings(value)
		for key := range values {
			if _, ok := input[key]; !ok {
				delete(values, key)
			}
		}
		if encoded, ok := input["openai_account_quota_auto_pause"]; ok && string(encoded) != "null" {
			var quota OpsOpenAIAccountQuotaAutoPauseSettings
			if err = json.Unmarshal(encoded, &quota); err != nil {
				return settings.PreparedChange{}, err
			}
			merged, err := MergeQuotaAutoPauseSettings(current["ops_advanced_settings"], quota)
			if err != nil {
				return settings.PreparedChange{}, err
			}
			encoded, err := json.Marshal(merged)
			if err != nil {
				return settings.PreparedChange{}, fmt.Errorf("marshal ops advanced settings: %w", err)
			}
			values["ops_advanced_settings"] = string(encoded)
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}
