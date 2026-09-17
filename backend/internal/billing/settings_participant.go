package billing

import (
	"context"
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// SettingsParticipant 只准备配置；不会变更用户余额、订阅、额度累计或缓存。
func SettingsParticipant() settings.Participant {
	fields := []string{"balance_icon_svg", "balance_low_notify_enabled", "balance_low_notify_recharge_url", "balance_low_notify_threshold", "balance_unit_name", "balance_unit_symbol", "default_balance", "default_platform_quotas", "default_subscriptions", "reasoning_point_rmb_unit_price", "subscription_expiry_notify_enabled", "usd_exchange_rate"}
	keys := []string{SettingKeyBalanceIconSVG, SettingKeyBalanceLowNotifyEnabled, SettingKeyBalanceLowNotifyRechargeURL, SettingKeyBalanceLowNotifyThreshold, SettingKeyBalanceUnitName, SettingKeyBalanceUnitSymbol, SettingKeyDefaultBalance, SettingKeyDefaultPlatformQuotas, SettingKeyDefaultSubscriptions, SettingKeyReasoningPointRMBUnitPrice, SettingKeySubscriptionExpiryNotifyEnabled, SettingKeyUSDExchangeRate}
	return settings.Participant{Module: "billing", Fields: fields, Keys: keys, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
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
		values, err := PrepareAdminSettings(&value)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		quota, err := PrepareDefaultQuotaSettings(value.DefaultPlatformQuotas)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		for key, value := range quota {
			values[key] = value
		}
		for i, field := range fields {
			if _, ok := input[field]; !ok {
				delete(values, keys[i])
			}
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}
