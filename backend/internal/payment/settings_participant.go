package payment

import (
	"context"
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// paymentCompositeFields 明确记录综合设置字段到现有支付配置的映射。
var paymentCompositeFields = []struct{ input, target string }{
	{"payment_enabled", "enabled"},
	{"payment_min_amount", "min_amount"},
	{"payment_max_amount", "max_amount"},
	{"payment_daily_limit", "daily_limit"},
	{"payment_order_timeout_minutes", "order_timeout_minutes"},
	{"payment_max_pending_orders", "max_pending_orders"},
	{"payment_enabled_types", "enabled_payment_types"},
	{"payment_balance_disabled", "balance_disabled"},
	{"payment_balance_recharge_multiplier", "balance_recharge_multiplier"},
	{"payment_subscription_usd_to_cny_rate", "subscription_usd_to_cny_rate"},
	{"payment_recharge_fee_rate", "recharge_fee_rate"},
	{"payment_method_fees", "method_fees"},
	{"payment_load_balance_strategy", "load_balance_strategy"},
	{"payment_product_name_prefix", "product_name_prefix"},
	{"payment_product_name_suffix", "product_name_suffix"},
	{"payment_help_image_url", "help_image_url"},
	{"payment_help_text", "help_text"},
	{"payment_cancel_rate_limit_enabled", "cancel_rate_limit_enabled"},
	{"payment_cancel_rate_limit_max", "cancel_rate_limit_max"},
	{"payment_cancel_rate_limit_window", "cancel_rate_limit_window"},
	{"payment_cancel_rate_limit_unit", "cancel_rate_limit_unit"},
	{"payment_cancel_rate_limit_window_mode", "cancel_rate_limit_window_mode"},
	{"payment_alipay_force_qrcode", "alipay_force_qrcode"},
	{"payment_alipay_mobile_precreate_deep_link", "alipay_mobile_precreate_deep_link"},
}

// SettingsParticipant 复用唯一支付校验；先准备，整批提交后才刷新渠道运行表。
func SettingsParticipant(refresh func(context.Context) error) settings.Participant {
	fields := make([]string, len(paymentCompositeFields))
	for i, field := range paymentCompositeFields {
		fields[i] = field.input
	}
	return settings.Participant{Module: "payment", Fields: fields, Keys: []string{
		SettingPaymentEnabled,
		SettingMinRechargeAmount,
		SettingMaxRechargeAmount,
		SettingDailyRechargeLimit,
		SettingOrderTimeoutMinutes,
		SettingMaxPendingOrders,
		SettingEnabledPaymentTypes,
		SettingBalancePayDisabled,
		SettingBalanceRechargeMult,
		SettingSubscriptionUSDToCNYRate,
		SettingRechargeFeeRate,
		SettingPaymentMethodFees,
		SettingLoadBalanceStrategy,
		SettingProductNamePrefix,
		SettingProductNameSuffix,
		SettingHelpImageURL,
		SettingHelpText,
		SettingCancelRateLimitOn,
		SettingCancelRateLimitMax,
		SettingCancelWindowSize,
		SettingCancelWindowUnit,
		SettingCancelWindowMode,
		SettingAlipayForceQRCode,
		SettingAlipayMobilePrecreateDeepLink,
	}, Prepare: func(ctx context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		projected := map[string]json.RawMessage{}
		for _, field := range paymentCompositeFields {
			if value, ok := input[field.input]; ok && string(value) != "null" {
				projected[field.target] = value
			}
		}
		if len(projected) == 0 {
			return settings.PreparedChange{}, nil
		}
		encoded, err := json.Marshal(projected)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		var request UpdatePaymentConfigRequest
		if err = json.Unmarshal(encoded, &request); err != nil {
			return settings.PreparedChange{}, err
		}
		values, err := PreparePaymentConfig(request)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		return settings.PreparedChange{Values: values, Apply: refresh}, nil
	}}
}
