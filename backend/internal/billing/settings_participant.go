package billing

import (
	"context"
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// settings_participant.go prepares supplied billing settings for coordinated
// persistence.
// Administrator updates can supply one billing setting while leaving the
// remaining stored values in place. Decoding that field map into AdminSettings
// introduces zero values for omitted fields, and PrepareAdminSettings encodes
// a complete set of billing values. Field presence therefore has to survive
// the conversion back to storage keys.
//
// The participant keeps parallel field and key lists and removes every
// prepared value whose HTTP field was absent. A supplied zero, false or empty
// value remains eligible for persistence. Optional plan-reference validation
// runs only when default_subscriptions is present in the submitted fields.

// SettingsParticipant returns billing field ownership and a preparation
// callback for the settings coordinator. Empty input yields no update; invalid
// typed input, plan validation failure, or preparation failure returns an
// error, while persistence remains owned by the coordinator.
//
// Preparation decodes supplied fields into AdminSettings, validates default
// subscription plans when default_subscriptions was submitted and a validator
// is supplied, and removes encoded keys for omitted fields. That presence
// filter keeps partial updates from writing zero values introduced during
// decoding, so the field and key lists must retain their matching order.
func SettingsParticipant(validatePlans ...func(context.Context, []DefaultSubscriptionSetting) error) settings.Participant {
	var planValidator func(context.Context, []DefaultSubscriptionSetting) error
	if len(validatePlans) > 0 {
		planValidator = validatePlans[0]
	}
	fields := []string{"balance_icon_svg", "balance_low_notify_enabled", "balance_low_notify_recharge_url", "balance_low_notify_threshold", "balance_unit_name", "balance_unit_symbol", "default_balance", "default_subscriptions", "reasoning_point_rmb_unit_price", "subscription_expiry_notify_enabled", "usd_exchange_rate"}
	keys := []string{SettingKeyBalanceIconSVG, SettingKeyBalanceLowNotifyEnabled, SettingKeyBalanceLowNotifyRechargeURL, SettingKeyBalanceLowNotifyThreshold, SettingKeyBalanceUnitName, SettingKeyBalanceUnitSymbol, SettingKeyDefaultBalance, SettingKeyDefaultSubscriptions, SettingKeyReasoningPointRMBUnitPrice, SettingKeySubscriptionExpiryNotifyEnabled, SettingKeyUSDExchangeRate}
	return settings.Participant{Module: "billing", Fields: fields, Keys: keys, Prepare: func(ctx context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
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
		// Validate default subscription plans only when the caller submitted
		// default_subscriptions so unrelated partial billing saves avoid plan lookups.
		if _, ok := input["default_subscriptions"]; ok && planValidator != nil {
			if err = planValidator(ctx, value.DefaultSubscriptions); err != nil {
				return settings.PreparedChange{}, err
			}
		}
		values, err := PrepareAdminSettings(&value)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		// Decoding omitted fields gives them zero values; remove their prepared
		// storage keys so this partial update retains unrelated saved settings.
		for i, field := range fields {
			if _, ok := input[field]; !ok {
				delete(values, keys[i])
			}
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}
