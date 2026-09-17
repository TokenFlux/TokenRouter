// 支付配置与管理规则的唯一实现；存储、环境和渠道构造由端口提供。
package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

const (
	SettingPaymentEnabled      = "payment_enabled"
	SettingMinRechargeAmount   = "MIN_RECHARGE_AMOUNT"
	SettingMaxRechargeAmount   = "MAX_RECHARGE_AMOUNT"
	SettingDailyRechargeLimit  = "DAILY_RECHARGE_LIMIT"
	SettingOrderTimeoutMinutes = "ORDER_TIMEOUT_MINUTES"
	SettingMaxPendingOrders    = "MAX_PENDING_ORDERS"
	SettingEnabledPaymentTypes = "ENABLED_PAYMENT_TYPES"
	SettingLoadBalanceStrategy = "LOAD_BALANCE_STRATEGY"
	SettingBalancePayDisabled  = "BALANCE_PAYMENT_DISABLED"
	SettingBalanceRechargeMult = "BALANCE_RECHARGE_MULTIPLIER"
	// SettingSubscriptionUSDToCNYRate 是订阅 CNY 换算汇率（1 USD = X CNY）。
	// 0/未配置 = 关闭换算（订阅按 price 数值直付），显式配置后 CNY 通道订阅按 price × rate 收款。
	SettingSubscriptionUSDToCNYRate      = "SUBSCRIPTION_USD_TO_CNY_RATE"
	SettingRechargeFeeRate               = "RECHARGE_FEE_RATE"
	SettingPaymentMethodFees             = "PAYMENT_METHOD_FEES"
	SettingProductNamePrefix             = "PRODUCT_NAME_PREFIX"
	SettingProductNameSuffix             = "PRODUCT_NAME_SUFFIX"
	SettingHelpImageURL                  = "PAYMENT_HELP_IMAGE_URL"
	SettingHelpText                      = "PAYMENT_HELP_TEXT"
	SettingCancelRateLimitOn             = "CANCEL_RATE_LIMIT_ENABLED"
	SettingCancelRateLimitMax            = "CANCEL_RATE_LIMIT_MAX"
	SettingCancelWindowSize              = "CANCEL_RATE_LIMIT_WINDOW"
	SettingCancelWindowUnit              = "CANCEL_RATE_LIMIT_UNIT"
	SettingCancelWindowMode              = "CANCEL_RATE_LIMIT_WINDOW_MODE"
	SettingAlipayForceQRCode             = "ALIPAY_FORCE_QRCODE"
	SettingAlipayMobilePrecreateDeepLink = "ALIPAY_MOBILE_PRECREATE_DEEP_LINK"
)

// Default values for payment configuration settings.
const (
	ConfigDefaultOrderTimeoutMin  = 30
	ConfigDefaultMaxPendingOrders = 3
)

// PaymentConfig holds the payment system configuration.
type PaymentConfig struct {
	Enabled                   bool     `json:"enabled"`
	MinAmount                 float64  `json:"min_amount"`
	MaxAmount                 float64  `json:"max_amount"`
	DailyLimit                float64  `json:"daily_limit"`
	OrderTimeoutMin           int      `json:"order_timeout_minutes"`
	MaxPendingOrders          int      `json:"max_pending_orders"`
	EnabledTypes              []string `json:"enabled_payment_types"`
	BalanceDisabled           bool     `json:"balance_disabled"`
	BalanceRechargeMultiplier float64  `json:"balance_recharge_multiplier"`
	// SubscriptionUSDToCNYRate 为 0 时订阅换算关闭（兼容存量行为）。
	SubscriptionUSDToCNYRate float64           `json:"subscription_usd_to_cny_rate"`
	RechargeFeeRate          float64           `json:"recharge_fee_rate"`
	MethodFees               MethodFeeSettings `json:"method_fees"`
	LoadBalanceStrategy      string            `json:"load_balance_strategy"`
	ProductNamePrefix        string            `json:"product_name_prefix"`
	ProductNameSuffix        string            `json:"product_name_suffix"`
	HelpImageURL             string            `json:"help_image_url"`
	HelpText                 string            `json:"help_text"`
	StripePublishableKey     string            `json:"stripe_publishable_key,omitempty"`

	// Cancel rate limit settings
	CancelRateLimitEnabled bool   `json:"cancel_rate_limit_enabled"`
	CancelRateLimitMax     int    `json:"cancel_rate_limit_max"`
	CancelRateLimitWindow  int    `json:"cancel_rate_limit_window"`
	CancelRateLimitUnit    string `json:"cancel_rate_limit_unit"`
	CancelRateLimitMode    string `json:"cancel_rate_limit_window_mode"`

	// 支付宝移动端强制使用二维码支付，不再跳转手机网站支付。
	AlipayForceQRCode bool `json:"alipay_force_qrcode"`
	// 移动端使用支付宝当面付预下单，并通过深链接唤起支付宝客户端。
	AlipayMobilePrecreateDeepLink bool `json:"alipay_mobile_precreate_deep_link"`
}

// UpdatePaymentConfigRequest contains fields to update payment configuration.
type UpdatePaymentConfigRequest struct {
	Enabled                   *bool             `json:"enabled"`
	MinAmount                 *float64          `json:"min_amount"`
	MaxAmount                 *float64          `json:"max_amount"`
	DailyLimit                *float64          `json:"daily_limit"`
	OrderTimeoutMin           *int              `json:"order_timeout_minutes"`
	MaxPendingOrders          *int              `json:"max_pending_orders"`
	EnabledTypes              []string          `json:"enabled_payment_types"`
	BalanceDisabled           *bool             `json:"balance_disabled"`
	BalanceRechargeMultiplier *float64          `json:"balance_recharge_multiplier"`
	SubscriptionUSDToCNYRate  *float64          `json:"subscription_usd_to_cny_rate"`
	RechargeFeeRate           *float64          `json:"recharge_fee_rate"`
	MethodFees                MethodFeeSettings `json:"method_fees"`
	LoadBalanceStrategy       *string           `json:"load_balance_strategy"`
	ProductNamePrefix         *string           `json:"product_name_prefix"`
	ProductNameSuffix         *string           `json:"product_name_suffix"`
	HelpImageURL              *string           `json:"help_image_url"`
	HelpText                  *string           `json:"help_text"`

	// Cancel rate limit settings
	CancelRateLimitEnabled *bool   `json:"cancel_rate_limit_enabled"`
	CancelRateLimitMax     *int    `json:"cancel_rate_limit_max"`
	CancelRateLimitWindow  *int    `json:"cancel_rate_limit_window"`
	CancelRateLimitUnit    *string `json:"cancel_rate_limit_unit"`
	CancelRateLimitMode    *string `json:"cancel_rate_limit_window_mode"`

	// 支付宝移动端强制使用二维码支付，不再跳转手机网站支付。
	AlipayForceQRCode *bool `json:"alipay_force_qrcode"`
	// 移动端使用支付宝当面付预下单，并通过深链接唤起支付宝客户端。
	AlipayMobilePrecreateDeepLink *bool `json:"alipay_mobile_precreate_deep_link"`

	VisibleMethodAlipaySource  *string `json:"payment_visible_method_alipay_source"`
	VisibleMethodWxpaySource   *string `json:"payment_visible_method_wxpay_source"`
	VisibleMethodAlipayEnabled *bool   `json:"payment_visible_method_alipay_enabled"`
	VisibleMethodWxpayEnabled  *bool   `json:"payment_visible_method_wxpay_enabled"`
}

// MethodLimits holds per-payment-type limits.
type MethodLimits struct {
	PaymentType string  `json:"payment_type"`
	DisplayName string  `json:"display_name,omitempty"`
	Currency    string  `json:"currency"`
	FeeRate     float64 `json:"fee_rate"`
	FixedFee    float64 `json:"fee_fixed"`
	DailyLimit  float64 `json:"daily_limit"`
	SingleMin   float64 `json:"single_min"`
	SingleMax   float64 `json:"single_max"`
}

// MethodFeeConfig 表示单个可见支付渠道的手续费覆盖配置。
type MethodFeeConfig struct {
	Enabled  bool    `json:"enabled"`
	FixedFee float64 `json:"fixed_fee"`
	FeeRate  float64 `json:"fee_rate"`
}

// MethodFeeSettings 按可见支付渠道保存手续费覆盖配置。
type MethodFeeSettings map[string]MethodFeeConfig

// MethodLimitsResponse is the full response for the user-facing /limits API.
// It includes per-method limits and the global widest range (union of all methods).
type MethodLimitsResponse struct {
	Methods   map[string]MethodLimits `json:"methods"`
	GlobalMin float64                 `json:"global_min"` // 0 = no minimum
	GlobalMax float64                 `json:"global_max"` // 0 = no maximum
}

type CreateProviderInstanceRequest struct {
	ProviderKey     string            `json:"provider_key"`
	Name            string            `json:"name"`
	Config          map[string]string `json:"config"`
	SupportedTypes  []string          `json:"supported_types"`
	Enabled         bool              `json:"enabled"`
	PaymentMode     string            `json:"payment_mode"`
	SortOrder       int               `json:"sort_order"`
	Limits          string            `json:"limits"`
	RefundEnabled   bool              `json:"refund_enabled"`
	AllowUserRefund bool              `json:"allow_user_refund"`
}

type UpdateProviderInstanceRequest struct {
	Name            *string           `json:"name"`
	Config          map[string]string `json:"config"`
	SupportedTypes  []string          `json:"supported_types"`
	Enabled         *bool             `json:"enabled"`
	PaymentMode     *string           `json:"payment_mode"`
	SortOrder       *int              `json:"sort_order"`
	Limits          *string           `json:"limits"`
	RefundEnabled   *bool             `json:"refund_enabled"`
	AllowUserRefund *bool             `json:"allow_user_refund"`
}

// TestProviderDraftRequest 表示管理员尚未保存的支付渠道配置草稿。
// 编辑既有实例时携带 InstanceID，以便服务端补回未回传的敏感字段。
type TestProviderDraftRequest struct {
	ProviderKey string            `json:"provider_key"`
	InstanceID  *int64            `json:"instance_id,omitempty"`
	Config      map[string]string `json:"config"`
}

// ProviderDraftTestResult 只暴露连通性结果，不返回上游响应或敏感配置。
type ProviderDraftTestResult struct {
	Reachable bool `json:"reachable"`
}

type CreatePlanRequest = billing.CreatePlanRequest

type UpdatePlanRequest = billing.UpdatePlanRequest

func (s *ConfigService) GetByID(ctx context.Context, id int64) (*SubscriptionPlan, error) {
	return s.plans.GetPlan(ctx, id)
}

// IsPaymentEnabled returns whether the payment system is enabled.
func (s *ConfigService) IsPaymentEnabled(ctx context.Context) bool {
	val, err := s.settingRepo.GetValue(ctx, SettingPaymentEnabled)
	if err != nil {
		return false
	}
	return val == "true"
}

// GetPaymentConfig returns the full payment configuration.
func (s *ConfigService) GetPaymentConfig(ctx context.Context) (*PaymentConfig, error) {
	keys := []string{
		SettingPaymentEnabled, SettingMinRechargeAmount, SettingMaxRechargeAmount,
		SettingDailyRechargeLimit, SettingOrderTimeoutMinutes, SettingMaxPendingOrders,
		SettingEnabledPaymentTypes, SettingBalancePayDisabled, SettingBalanceRechargeMult, SettingSubscriptionUSDToCNYRate, SettingRechargeFeeRate, SettingPaymentMethodFees, SettingLoadBalanceStrategy,
		SettingProductNamePrefix, SettingProductNameSuffix,
		SettingHelpImageURL, SettingHelpText,
		SettingCancelRateLimitOn, SettingCancelRateLimitMax,
		SettingCancelWindowSize, SettingCancelWindowUnit, SettingCancelWindowMode,
		SettingAlipayForceQRCode, SettingAlipayMobilePrecreateDeepLink,
		SettingPaymentVisibleMethodAlipayEnabled, SettingPaymentVisibleMethodAlipaySource,
		SettingPaymentVisibleMethodWxpayEnabled, SettingPaymentVisibleMethodWxpaySource,
	}
	vals, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("get payment config settings: %w", err)
	}
	cfg := s.ConfigParsePaymentConfig(vals)
	// Load Stripe publishable key from the first enabled Stripe provider instance
	cfg.StripePublishableKey = s.ConfigGetStripePublishableKey(ctx)
	return cfg, nil
}

func (s *ConfigService) ConfigParsePaymentConfig(vals map[string]string) *PaymentConfig {
	cfg := &PaymentConfig{
		Enabled:                   vals[SettingPaymentEnabled] == "true",
		MinAmount:                 ConfigPcParseFloat(vals[SettingMinRechargeAmount], 1),
		MaxAmount:                 ConfigPcParseFloat(vals[SettingMaxRechargeAmount], 0),
		DailyLimit:                ConfigPcParseFloat(vals[SettingDailyRechargeLimit], 0),
		OrderTimeoutMin:           ConfigPcParseInt(vals[SettingOrderTimeoutMinutes], ConfigDefaultOrderTimeoutMin),
		MaxPendingOrders:          ConfigPcParseInt(vals[SettingMaxPendingOrders], ConfigDefaultMaxPendingOrders),
		BalanceDisabled:           vals[SettingBalancePayDisabled] == "true",
		BalanceRechargeMultiplier: NormalizeBalanceRechargeMultiplier(ConfigPcParseFloat(vals[SettingBalanceRechargeMult], DefaultBalanceRechargeMultiplier)),
		SubscriptionUSDToCNYRate:  NormalizeSubscriptionUSDToCNYRate(ConfigPcParseFloat(vals[SettingSubscriptionUSDToCNYRate], 0)),
		RechargeFeeRate:           ConfigPcParseFloat(vals[SettingRechargeFeeRate], 0),
		MethodFees:                ConfigParseMethodFeeSettings(vals[SettingPaymentMethodFees]),
		LoadBalanceStrategy:       vals[SettingLoadBalanceStrategy],
		ProductNamePrefix:         vals[SettingProductNamePrefix],
		ProductNameSuffix:         vals[SettingProductNameSuffix],
		HelpImageURL:              vals[SettingHelpImageURL],
		HelpText:                  vals[SettingHelpText],

		CancelRateLimitEnabled: vals[SettingCancelRateLimitOn] == "true",
		CancelRateLimitMax:     ConfigPcParseInt(vals[SettingCancelRateLimitMax], 10),
		CancelRateLimitWindow:  ConfigPcParseInt(vals[SettingCancelWindowSize], 1),
		CancelRateLimitUnit:    vals[SettingCancelWindowUnit],
		CancelRateLimitMode:    vals[SettingCancelWindowMode],

		AlipayForceQRCode:             vals[SettingAlipayForceQRCode] == "true",
		AlipayMobilePrecreateDeepLink: vals[SettingAlipayMobilePrecreateDeepLink] == "true",
	}
	cfg.AlipayMobilePrecreateDeepLink = ConfigPcEnvBoolOverride(s.runtime.LookupEnv,
		SettingAlipayMobilePrecreateDeepLink,
		cfg.AlipayMobilePrecreateDeepLink,
	)
	if cfg.LoadBalanceStrategy == "" {
		cfg.LoadBalanceStrategy = DefaultLoadBalanceStrategy
	}
	if raw := vals[SettingEnabledPaymentTypes]; raw != "" {
		types := make([]string, 0, len(strings.Split(raw, ",")))
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				types = append(types, t)
			}
		}
		cfg.EnabledTypes = NormalizeVisibleMethods(types)
	}
	return cfg
}

func ConfigPcEnvBoolOverride(lookup func(string) (string, bool), key string, fallback bool) bool {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}

func (s *ConfigService) ConfigGetStripePublishableKey(ctx context.Context) string {
	if s.store == nil {
		return ""
	}
	instances, err := s.store.ListInstances(ctx, InstanceFilter{EnabledOnly: true, ProviderKey: TypeStripe, Limit: 1})
	if err != nil || len(instances) == 0 {
		return ""
	}
	cfg, err := s.ConfigDecryptConfig(instances[0].Config)
	if err != nil || cfg == nil {
		return ""
	}
	return cfg[ConfigKeyPublishableKey]
}

func ConfigParseMethodFeeSettings(raw string) MethodFeeSettings {
	if strings.TrimSpace(raw) == "" {
		return MethodFeeSettings{}
	}
	var settings MethodFeeSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return MethodFeeSettings{}
	}
	out := MethodFeeSettings{}
	for method, cfg := range settings {
		method = NormalizeVisibleMethod(method)
		if method == "" {
			continue
		}
		if cfg.FixedFee < 0 || cfg.FeeRate < 0 {
			continue
		}
		out[method] = MethodFeeConfig{
			Enabled:  cfg.Enabled,
			FixedFee: ConfigPcRound2(cfg.FixedFee),
			FeeRate:  ConfigPcRound2(cfg.FeeRate),
		}
	}
	return out
}

func ConfigFormatMethodFeeSettings(settings MethodFeeSettings) string {
	if settings == nil {
		return ""
	}
	normalized := MethodFeeSettings{}
	for method, cfg := range settings {
		method = NormalizeVisibleMethod(method)
		if method == "" {
			continue
		}
		normalized[method] = MethodFeeConfig{
			Enabled:  cfg.Enabled,
			FixedFee: ConfigPcRound2(cfg.FixedFee),
			FeeRate:  ConfigPcRound2(cfg.FeeRate),
		}
	}
	if len(normalized) == 0 {
		return "{}"
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func ConfigValidateMethodFeeSettings(settings MethodFeeSettings) error {
	for method, cfg := range settings {
		if !ConfigIsSupportedMethodFeeMethod(method) {
			return infraerrors.BadRequest("INVALID_PAYMENT_METHOD_FEE", "payment method fee contains unsupported method")
		}
		if !ConfigPcValidMoney2(cfg.FixedFee) {
			return infraerrors.BadRequest("INVALID_PAYMENT_METHOD_FEE", "fixed fee must be non-negative and allow at most 2 decimal places")
		}
		if !ConfigPcValidRate2(cfg.FeeRate) {
			return infraerrors.BadRequest("INVALID_PAYMENT_METHOD_FEE", "fee rate must be between 0 and 100 and allow at most 2 decimal places")
		}
	}
	return nil
}

func ConfigIsSupportedMethodFeeMethod(method string) bool {
	// 手续费配置只开放给用户可见的支付渠道，避免 easypay 等内部来源被误配。
	switch NormalizeVisibleMethod(method) {
	case TypeStripe, TypeAlipay, TypeWxpay:
		return true
	default:
		return false
	}
}

func ConfigPcValidMoney2(v float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return false
	}
	return math.Abs(math.Round(v*100)-v*100) < 1e-9
}

func ConfigPcValidRate2(v float64) bool {
	return ConfigPcValidMoney2(v) && v <= 100
}

func ConfigPcRound2(v float64) float64 {
	return math.Round(v*100) / 100
}

// EffectiveMethodFee 返回指定可见渠道最终生效的手续费配置。
func (cfg *PaymentConfig) EffectiveMethodFee(method string) FeeConfig {
	method = NormalizeVisibleMethod(method)
	if cfg == nil {
		return FeeConfig{}
	}
	if method != "" {
		if methodFee, ok := cfg.MethodFees[method]; ok && methodFee.Enabled {
			return FeeConfig{FixedFee: methodFee.FixedFee, FeeRate: methodFee.FeeRate}
		}
	}
	return FeeConfig{FeeRate: cfg.RechargeFeeRate}
}

// UpdatePaymentConfig 按 PATCH 语义更新支付配置。
// 每个字段都要独立判空后再序列化，因此函数天然较长；拆分只会掩盖字段与设置键的对应关系。
func (s *ConfigService) UpdatePaymentConfig(ctx context.Context, req UpdatePaymentConfigRequest) error {
	values, err := PreparePaymentConfig(req)
	if err != nil {
		return err
	}
	return s.settingRepo.SetMultiple(ctx, values)
}

// PreparePaymentConfig 校验并投影支付设置，供独立入口和综合原子更新共同使用。
func PreparePaymentConfig(req UpdatePaymentConfigRequest) (map[string]string, error) {
	if req.BalanceRechargeMultiplier != nil {
		if math.IsNaN(*req.BalanceRechargeMultiplier) || math.IsInf(*req.BalanceRechargeMultiplier, 0) || *req.BalanceRechargeMultiplier <= 0 {
			return nil, infraerrors.BadRequest("INVALID_BALANCE_RECHARGE_MULTIPLIER", "balance recharge multiplier must be greater than 0")
		}
	}
	if req.SubscriptionUSDToCNYRate != nil {
		v := *req.SubscriptionUSDToCNYRate
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return nil, infraerrors.BadRequest("INVALID_SUBSCRIPTION_USD_TO_CNY_RATE", "subscription USD to CNY rate must be 0 (disabled) or a positive number")
		}
	}
	if req.RechargeFeeRate != nil {
		v := *req.RechargeFeeRate
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
			return nil, infraerrors.BadRequest("INVALID_RECHARGE_FEE_RATE", "recharge fee rate must be between 0 and 100")
		}
		// 充值手续费率最多保留两位小数。
		if math.Round(v*100) != v*100 {
			return nil, infraerrors.BadRequest("INVALID_RECHARGE_FEE_RATE", "recharge fee rate allows at most 2 decimal places")
		}
	}
	if req.MethodFees != nil {
		if err := ConfigValidateMethodFeeSettings(req.MethodFees); err != nil {
			return nil, err
		}
	}
	m := make(map[string]string)
	if req.Enabled != nil {
		m[SettingPaymentEnabled] = ConfigFormatBoolOrEmpty(req.Enabled)
	}
	if req.MinAmount != nil {
		m[SettingMinRechargeAmount] = ConfigFormatPositiveFloat(req.MinAmount)
	}
	if req.MaxAmount != nil {
		m[SettingMaxRechargeAmount] = ConfigFormatPositiveFloat(req.MaxAmount)
	}
	if req.DailyLimit != nil {
		m[SettingDailyRechargeLimit] = ConfigFormatPositiveFloat(req.DailyLimit)
	}
	if req.OrderTimeoutMin != nil {
		m[SettingOrderTimeoutMinutes] = ConfigFormatPositiveInt(req.OrderTimeoutMin)
	}
	if req.MaxPendingOrders != nil {
		m[SettingMaxPendingOrders] = ConfigFormatPositiveInt(req.MaxPendingOrders)
	}
	if req.EnabledTypes != nil {
		m[SettingEnabledPaymentTypes] = strings.Join(req.EnabledTypes, ",")
	}
	if req.BalanceDisabled != nil {
		m[SettingBalancePayDisabled] = ConfigFormatBoolOrEmpty(req.BalanceDisabled)
	}
	if req.BalanceRechargeMultiplier != nil {
		m[SettingBalanceRechargeMult] = ConfigFormatPositiveFloat(req.BalanceRechargeMultiplier)
	}
	if req.SubscriptionUSDToCNYRate != nil {
		m[SettingSubscriptionUSDToCNYRate] = ConfigFormatPositiveFloatExact(req.SubscriptionUSDToCNYRate)
	}
	if req.RechargeFeeRate != nil {
		m[SettingRechargeFeeRate] = ConfigFormatNonNegativeFloat(req.RechargeFeeRate)
	}
	if req.MethodFees != nil {
		m[SettingPaymentMethodFees] = ConfigFormatMethodFeeSettings(req.MethodFees)
	}
	if req.LoadBalanceStrategy != nil {
		m[SettingLoadBalanceStrategy] = ConfigDerefStr(req.LoadBalanceStrategy)
	}
	if req.ProductNamePrefix != nil {
		m[SettingProductNamePrefix] = ConfigDerefStr(req.ProductNamePrefix)
	}
	if req.ProductNameSuffix != nil {
		m[SettingProductNameSuffix] = ConfigDerefStr(req.ProductNameSuffix)
	}
	if req.HelpImageURL != nil {
		m[SettingHelpImageURL] = ConfigDerefStr(req.HelpImageURL)
	}
	if req.HelpText != nil {
		m[SettingHelpText] = ConfigDerefStr(req.HelpText)
	}
	if req.CancelRateLimitEnabled != nil {
		m[SettingCancelRateLimitOn] = ConfigFormatBoolOrEmpty(req.CancelRateLimitEnabled)
	}
	if req.CancelRateLimitMax != nil {
		m[SettingCancelRateLimitMax] = ConfigFormatPositiveInt(req.CancelRateLimitMax)
	}
	if req.CancelRateLimitWindow != nil {
		m[SettingCancelWindowSize] = ConfigFormatPositiveInt(req.CancelRateLimitWindow)
	}
	if req.CancelRateLimitUnit != nil {
		m[SettingCancelWindowUnit] = ConfigDerefStr(req.CancelRateLimitUnit)
	}
	if req.CancelRateLimitMode != nil {
		m[SettingCancelWindowMode] = ConfigDerefStr(req.CancelRateLimitMode)
	}
	if req.AlipayForceQRCode != nil {
		m[SettingAlipayForceQRCode] = ConfigFormatBoolOrEmpty(req.AlipayForceQRCode)
	}
	if req.AlipayMobilePrecreateDeepLink != nil {
		m[SettingAlipayMobilePrecreateDeepLink] = ConfigFormatBoolOrEmpty(req.AlipayMobilePrecreateDeepLink)
	}
	if req.VisibleMethodAlipaySource != nil {
		m[SettingPaymentVisibleMethodAlipaySource] = ConfigDerefStr(req.VisibleMethodAlipaySource)
	}
	if req.VisibleMethodWxpaySource != nil {
		m[SettingPaymentVisibleMethodWxpaySource] = ConfigDerefStr(req.VisibleMethodWxpaySource)
	}
	if req.VisibleMethodAlipayEnabled != nil {
		m[SettingPaymentVisibleMethodAlipayEnabled] = ConfigFormatBoolOrEmpty(req.VisibleMethodAlipayEnabled)
	}
	if req.VisibleMethodWxpayEnabled != nil {
		m[SettingPaymentVisibleMethodWxpayEnabled] = ConfigFormatBoolOrEmpty(req.VisibleMethodWxpayEnabled)
	}
	return m, nil
}

func ConfigFormatBoolOrEmpty(v *bool) string {
	if v == nil {
		return ""
	}
	return strconv.FormatBool(*v)
}

func ConfigFormatPositiveFloat(v *float64) string {
	if v == nil || *v <= 0 {
		return "" // empty → ConfigParsePaymentConfig uses default
	}
	return strconv.FormatFloat(*v, 'f', 2, 64)
}

// ConfigFormatPositiveFloatExact 保留完整精度，用于汇率等对小数位敏感的配置。
func ConfigFormatPositiveFloatExact(v *float64) string {
	if v == nil || *v <= 0 {
		return "" // empty → ConfigParsePaymentConfig 视为未配置（换算关闭）
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func ConfigFormatNonNegativeFloat(v *float64) string {
	if v == nil || *v < 0 {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 2, 64)
}

func ConfigFormatPositiveInt(v *int) string {
	if v == nil || *v <= 0 {
		return ""
	}
	return strconv.Itoa(*v)
}

func ConfigDerefStr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func ConfigSplitTypes(s string) []string {
	if strings.TrimSpace(s) == "" {
		// 返回空切片，避免管理端 JSON 把 nil 切片编码成 null。
		return []string{}
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func ConfigJoinTypes(types []string) string {
	return strings.Join(types, ",")
}

func ConfigPcParseFloat(s string, defaultVal float64) float64 {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return defaultVal
	}
	return v
}

func ConfigPcParseInt(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}

func ConfigBuildVisibleMethodSourceAvailability(instances []*ProviderInstance) map[string]bool {
	available := make(map[string]bool, 4)
	for _, inst := range instances {
		switch inst.ProviderKey {
		case TypeAlipay:
			if inst.SupportedTypes == "" || InstanceSupportsType(inst.SupportedTypes, TypeAlipay) || InstanceSupportsType(inst.SupportedTypes, TypeAlipayDirect) {
				available[VisibleMethodSourceOfficialAlipay] = true
			}
		case TypeWxpay:
			if inst.SupportedTypes == "" || InstanceSupportsType(inst.SupportedTypes, TypeWxpay) || InstanceSupportsType(inst.SupportedTypes, TypeWxpayDirect) {
				available[VisibleMethodSourceOfficialWechat] = true
			}
		case TypeEasyPay:
			for _, supportedType := range ConfigSplitTypes(inst.SupportedTypes) {
				switch NormalizeVisibleMethod(supportedType) {
				case TypeAlipay:
					available[VisibleMethodSourceEasyPayAlipay] = true
				case TypeWxpay:
					available[VisibleMethodSourceEasyPayWechat] = true
				}
			}
		}
	}
	return available
}

func ConfigApplyVisibleMethodRoutingToEnabledTypes(base []string, vals map[string]string, available map[string]bool) []string {
	shouldExpose := map[string]bool{
		TypeAlipay: ConfigVisibleMethodShouldBeExposed(TypeAlipay, vals, available),
		TypeWxpay:  ConfigVisibleMethodShouldBeExposed(TypeWxpay, vals, available),
	}

	seen := make(map[string]struct{}, len(base)+2)
	out := make([]string, 0, len(base)+2)
	appendType := func(paymentType string) {
		paymentType = NormalizeVisibleMethod(paymentType)
		if paymentType == "" {
			return
		}
		if _, ok := seen[paymentType]; ok {
			return
		}
		seen[paymentType] = struct{}{}
		out = append(out, paymentType)
	}

	for _, paymentType := range base {
		visibleMethod := NormalizeVisibleMethod(paymentType)
		switch visibleMethod {
		case TypeAlipay, TypeWxpay:
			if shouldExpose[visibleMethod] {
				appendType(visibleMethod)
			}
		default:
			appendType(visibleMethod)
		}
	}

	for _, visibleMethod := range []string{TypeAlipay, TypeWxpay} {
		if shouldExpose[visibleMethod] {
			appendType(visibleMethod)
		}
	}
	return out
}

func ConfigVisibleMethodShouldBeExposed(method string, vals map[string]string, available map[string]bool) bool {
	enabledKey := ResumeVisibleMethodEnabledSettingKey(method)
	sourceKey := ResumeVisibleMethodSourceSettingKey(method)
	if enabledKey == "" || sourceKey == "" || vals[enabledKey] != "true" {
		return false
	}
	source := NormalizeVisibleMethodSource(method, vals[sourceKey])
	return source != "" && available[source]
}
