package billing

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// AdminSettings 仅描述资金展示和默认权益配置，不包含运行态消费或余额写入。
type AdminSettings struct {
	BalanceIconSVG                  string                                  `json:"balance_icon_svg"`
	BalanceLowNotifyEnabled         bool                                    `json:"balance_low_notify_enabled"`
	BalanceLowNotifyRechargeURL     string                                  `json:"balance_low_notify_recharge_url"`
	BalanceLowNotifyThreshold       float64                                 `json:"balance_low_notify_threshold"`
	BalanceUnitName                 string                                  `json:"balance_unit_name"`
	BalanceUnitSymbol               string                                  `json:"balance_unit_symbol"`
	DefaultBalance                  float64                                 `json:"default_balance"`
	DefaultPlatformQuotas           map[string]*DefaultPlatformQuotaSetting `json:"default_platform_quotas"`
	DefaultSubscriptions            []DefaultSubscriptionSetting            `json:"default_subscriptions"`
	ReasoningPointRMBUnitPrice      float64                                 `json:"reasoning_point_rmb_unit_price"`
	SubscriptionExpiryNotifyEnabled bool                                    `json:"subscription_expiry_notify_enabled"`
	USDExchangeRate                 float64                                 `json:"usd_exchange_rate"`
}

// 继续使用原配置键，与用户资金字段的存储权限分开。
const (
	SettingKeyBalanceIconSVG                  = "balance_icon_svg"
	SettingKeyBalanceUnitName                 = "balance_unit_name"
	SettingKeyBalanceUnitSymbol               = "balance_unit_symbol"
	SettingKeyDefaultBalance                  = "default_balance"
	SettingKeyDefaultPlatformQuotas           = "default_platform_quotas"
	SettingKeyDefaultSubscriptions            = "default_subscriptions"
	SettingKeyReasoningPointRMBUnitPrice      = "reasoning_point_rmb_unit_price"
	SettingKeySubscriptionExpiryNotifyEnabled = "subscription_expiry_notify_enabled"
	SettingKeyUSDExchangeRate                 = "usd_exchange_rate"
)

// PrepareAdminSettings 只规范化展示与默认值，保持原金额格式；套餐引用由协调入口在原时点校验。
func PrepareAdminSettings(settings *AdminSettings) (map[string]string, error) {
	updates := map[string]string{}
	settings.BalanceUnitName = strings.TrimSpace(settings.BalanceUnitName)
	settings.BalanceUnitSymbol = strings.TrimSpace(settings.BalanceUnitSymbol)
	settings.BalanceIconSVG = strings.TrimSpace(settings.BalanceIconSVG)
	if settings.BalanceUnitName == "" {
		settings.BalanceUnitName = "USD"
	}
	if settings.BalanceUnitSymbol == "" {
		settings.BalanceUnitSymbol = "$"
	}
	if settings.ReasoningPointRMBUnitPrice < 0 {
		settings.ReasoningPointRMBUnitPrice = 0
	}
	if settings.USDExchangeRate < 0 {
		settings.USDExchangeRate = 0
	}
	updates[SettingKeyDefaultBalance] = strconv.FormatFloat(settings.DefaultBalance, 'f', 8, 64)
	defaultSubsJSON, err := json.Marshal(settings.DefaultSubscriptions)
	if err != nil {
		return nil, fmt.Errorf("marshal default subscriptions: %w", err)
	}
	updates[SettingKeyDefaultSubscriptions] = string(defaultSubsJSON)
	updates[SettingKeyBalanceUnitName] = settings.BalanceUnitName
	updates[SettingKeyBalanceUnitSymbol] = settings.BalanceUnitSymbol
	updates[SettingKeyBalanceIconSVG] = settings.BalanceIconSVG
	updates[SettingKeyReasoningPointRMBUnitPrice] = strconv.FormatFloat(settings.ReasoningPointRMBUnitPrice, 'f', 8, 64)
	updates[SettingKeyUSDExchangeRate] = strconv.FormatFloat(settings.USDExchangeRate, 'f', 8, 64)
	updates[SettingKeyBalanceLowNotifyEnabled] = strconv.FormatBool(settings.BalanceLowNotifyEnabled)
	updates[SettingKeyBalanceLowNotifyThreshold] = strconv.FormatFloat(settings.BalanceLowNotifyThreshold, 'f', 8, 64)
	updates[SettingKeyBalanceLowNotifyRechargeURL] = settings.BalanceLowNotifyRechargeURL
	updates[SettingKeySubscriptionExpiryNotifyEnabled] = strconv.FormatBool(settings.SubscriptionExpiryNotifyEnabled)
	return updates, nil
}

// PrepareDefaultQuotaSettings 在原后段校验额度默认值，避免改变其它设置失败的优先级。
func PrepareDefaultQuotaSettings(value map[string]*DefaultPlatformQuotaSetting) (map[string]string, error) {
	updates := map[string]string{}
	if value != nil {
		if err := ValidateDefaultPlatformQuotaMap(value); err != nil {
			return nil, err
		}
		blob, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal default platform quotas: %w", err)
		}
		updates[SettingKeyDefaultPlatformQuotas] = string(blob)
	}
	return updates, nil
}
