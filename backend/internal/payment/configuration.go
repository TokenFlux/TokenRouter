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
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
)

// configuration.go translates stored payment settings into checkout
// configuration and prepares partial updates.
// Payment settings use string persistence while checkout consumes booleans,
// numeric defaults, normalized methods and fee maps. Ordinary reads recognize
// literal true and retain successfully parsed numeric values, while only
// multiplier, conversion rate and fee operations apply their own
// validation/normalization. Localized display settings are resolved before
// parsing, and the mobile Alipay precreate flag has a separate environment
// override.
//
// Partial updates distinguish omitted pointers/maps/slices from supplied
// false, zero or empty values. Positive-only formatting can clear stored text
// and thereby select a later parser default; conversion rates use full float
// text while ordinary money settings use two decimals. Enabled method fees
// replace the legacy percentage rate, and malformed fee JSON becomes an empty
// override map. Visibility/source helpers are callable calculations with their
// own eligibility preconditions, so their existence does not establish that
// every checkout path applies those exposure settings.

// Payment setting keys address string-backed configuration and display values.
// ConfigDefaultOrderTimeoutMin and ConfigDefaultMaxPendingOrders supply
// parser/draft defaults when configured duration or count is absent or
// unusable.
const (
	// checkout system enable setting.
	SettingPaymentEnabled = "payment_enabled"
	// minimum balance principal setting.
	SettingMinRechargeAmount = "MIN_RECHARGE_AMOUNT"
	// maximum balance principal setting.
	SettingMaxRechargeAmount = "MAX_RECHARGE_AMOUNT"
	// buyer daily purchase setting.
	SettingDailyRechargeLimit = "DAILY_RECHARGE_LIMIT"
	// pending expiry minutes setting.
	SettingOrderTimeoutMinutes = "ORDER_TIMEOUT_MINUTES"
	// unfinished buyer order setting.
	SettingMaxPendingOrders = "MAX_PENDING_ORDERS"
	// visible method list setting.
	SettingEnabledPaymentTypes = "ENABLED_PAYMENT_TYPES"
	// instance selection strategy setting.
	SettingLoadBalanceStrategy = "LOAD_BALANCE_STRATEGY"
	// balance purchase disable setting.
	SettingBalancePayDisabled = "BALANCE_PAYMENT_DISABLED"
	// credited principal multiplier setting.
	SettingBalanceRechargeMult = "BALANCE_RECHARGE_MULTIPLIER"
	// SettingSubscriptionUSDToCNYRate stores CNY per subscription USD using exact
	// positive float text. Zero or absent text disables conversion, and checkout
	// applies a positive rate before fees only for CNY charges.
	SettingSubscriptionUSDToCNYRate = "SUBSCRIPTION_USD_TO_CNY_RATE"
	// legacy percentage fee setting.
	SettingRechargeFeeRate = "RECHARGE_FEE_RATE"
	// visible fee override setting.
	SettingPaymentMethodFees = "PAYMENT_METHOD_FEES"
	// product title prefix setting.
	SettingProductNamePrefix = "PRODUCT_NAME_PREFIX"
	// product title suffix setting.
	SettingProductNameSuffix = "PRODUCT_NAME_SUFFIX"
	// checkout help image setting.
	SettingHelpImageURL = "PAYMENT_HELP_IMAGE_URL"
	// checkout help text setting.
	SettingHelpText = "PAYMENT_HELP_TEXT"
	// buyer cancellation gate setting.
	SettingCancelRateLimitOn = "CANCEL_RATE_LIMIT_ENABLED"
	// cancellation audit count setting.
	SettingCancelRateLimitMax = "CANCEL_RATE_LIMIT_MAX"
	// cancellation interval quantity setting.
	SettingCancelWindowSize = "CANCEL_RATE_LIMIT_WINDOW"
	// cancellation interval unit setting.
	SettingCancelWindowUnit = "CANCEL_RATE_LIMIT_UNIT"
	// cancellation calendar mode setting.
	SettingCancelWindowMode = "CANCEL_RATE_LIMIT_WINDOW_MODE"
	// frontend Alipay QR preference.
	SettingAlipayForceQRCode = "ALIPAY_FORCE_QRCODE"
	// official mobile precreate setting.
	SettingAlipayMobilePrecreateDeepLink = "ALIPAY_MOBILE_PRECREATE_DEEP_LINK"

	// default pending expiry minutes.
	ConfigDefaultOrderTimeoutMin = 30
	// default unfinished buyer count.
	ConfigDefaultMaxPendingOrders = 3
)

// PaymentConfig contains parsed checkout settings and the separately observed
// Stripe publishable key.
type PaymentConfig struct {
	Enabled bool `json:"enabled"` // global checkout enable flag.
	// minimum balance purchase principal.
	MinAmount float64 `json:"min_amount"`
	// maximum balance purchase principal.
	MaxAmount float64 `json:"max_amount"`
	// buyer daily purchase limit.
	DailyLimit float64 `json:"daily_limit"`
	// OrderTimeoutMin is parsed minutes for pending expiry. BuildCheckoutDraft
	// replaces a nonpositive value with ConfigDefaultOrderTimeoutMin before the
	// store calculates the expiration.
	OrderTimeoutMin int `json:"order_timeout_minutes"`
	// MaxPendingOrders is the parsed unfinished buyer order ceiling.
	// ValidateCheckoutPending substitutes ConfigDefaultMaxPendingOrders when the
	// supplied ceiling is nonpositive before comparing the store's order count.
	MaxPendingOrders int `json:"max_pending_orders"`
	// normalized stored method labels.
	EnabledTypes []string `json:"enabled_payment_types"`
	// balance purchase disable flag.
	BalanceDisabled bool `json:"balance_disabled"`
	// credited points per principal.
	BalanceRechargeMultiplier float64 `json:"balance_recharge_multiplier"`
	// CNY per subscription USD.
	SubscriptionUSDToCNYRate float64 `json:"subscription_usd_to_cny_rate"`
	// legacy fee percentage points.
	RechargeFeeRate float64 `json:"recharge_fee_rate"`
	// MethodFees is newly parsed configuration keyed by normalized visible
	// method. EffectiveMethodFee uses an enabled present entry before the legacy
	// rate; callers keep this map stable during checkout reads.
	MethodFees MethodFeeSettings `json:"method_fees"`
	// configured instance selection text.
	LoadBalanceStrategy string `json:"load_balance_strategy"`
	// localized product title prefix.
	ProductNamePrefix string `json:"product_name_prefix"`
	// localized product title suffix.
	ProductNameSuffix string `json:"product_name_suffix"`
	// localized help image URL.
	HelpImageURL string `json:"help_image_url"`
	// localized checkout help text.
	HelpText string `json:"help_text"`
	// StripePublishableKey comes from a separate first-enabled Stripe instance
	// query without SortByOrder. Read or recoverable decode failure leaves it
	// empty even when the main settings read succeeded.
	StripePublishableKey string `json:"stripe_publishable_key,omitempty"`

	// buyer cancellation gate flag.
	CancelRateLimitEnabled bool `json:"cancel_rate_limit_enabled"`
	// configured cancellation audit count.
	CancelRateLimitMax int `json:"cancel_rate_limit_max"`
	// configured cancellation interval quantity.
	CancelRateLimitWindow int `json:"cancel_rate_limit_window"`
	// configured cancellation interval unit.
	CancelRateLimitUnit string `json:"cancel_rate_limit_unit"`
	// configured cancellation calendar mode.
	CancelRateLimitMode string `json:"cancel_rate_limit_window_mode"`

	// AlipayForceQRCode is exposed for PaymentView's QR preference. Its payload
	// helper sends is_mobile=false for Alipay when true and mobile-precreate
	// preference is absent; backend provider construction follows that request
	// flag.
	AlipayForceQRCode bool `json:"alipay_force_qrcode"`
	// AlipayMobilePrecreateDeepLink permits official mobile Alipay precreate
	// through ShouldUseAlipayMobilePrecreate. A valid nonblank environment
	// boolean overrides its saved setting, and checkout-info also checks current
	// official source.
	AlipayMobilePrecreateDeepLink bool `json:"alipay_mobile_precreate_deep_link"`
}

// UpdatePaymentConfigRequest carries optional settings fields whose presence
// selects string-backed partial writes.
type UpdatePaymentConfigRequest struct {
	// Enabled supplies system enablement only when nonnil. A supplied false is
	// written as false text; nil retains its persisted setting.
	Enabled *bool `json:"enabled"`
	// MinAmount, MaxAmount and DailyLimit omit their settings when nil. Supplied
	// nonpositive values clear text, and other values use two-decimal formatting;
	// their parser defaults are one, zero and zero respectively.
	//
	// minimum purchase principal.
	MinAmount *float64 `json:"min_amount"`
	// maximum purchase principal.
	MaxAmount *float64 `json:"max_amount"`
	// daily purchase limit.
	DailyLimit *float64 `json:"daily_limit"`
	// OrderTimeoutMin and MaxPendingOrders omit their settings when nil. Positive
	// integers write decimal text; nonpositive input clears text, selecting
	// thirty-minute and three-order parser defaults respectively.
	//
	// pending expiry minutes.
	OrderTimeoutMin *int `json:"order_timeout_minutes"`
	// unfinished buyer order ceiling.
	MaxPendingOrders *int `json:"max_pending_orders"`
	// EnabledTypes is omitted when nil and joined directly when supplied. An
	// empty nonnil slice clears the stored list; GetPaymentConfig later
	// normalizes nonblank comma entries.
	EnabledTypes []string `json:"enabled_payment_types"`
	// BalanceDisabled supplies balance purchase disablement only when nonnil. A
	// supplied false is written as false text; nil retains its persisted setting.
	BalanceDisabled *bool `json:"balance_disabled"`
	// BalanceRechargeMultiplier must be finite positive when supplied, while nil
	// omits its key. Two-decimal serialization can turn a tiny positive
	// multiplier into zero text, whose later normalization selects the default
	// multiplier.
	BalanceRechargeMultiplier *float64 `json:"balance_recharge_multiplier"`
	// SubscriptionUSDToCNYRate is omitted when nil and otherwise must be finite
	// nonnegative. Zero clears text to disable conversion; positive values retain
	// full float precision.
	SubscriptionUSDToCNYRate *float64 `json:"subscription_usd_to_cny_rate"`
	// RechargeFeeRate is omitted when nil and otherwise must be finite from zero
	// through one hundred with exact two-decimal representability. Supplied zero
	// writes zero text.
	RechargeFeeRate *float64 `json:"recharge_fee_rate"`
	// MethodFees is omitted when nil and validated/serialized when supplied.
	// Empty nonnil input writes an empty JSON object, removing method overrides
	// while retaining the legacy fee rate.
	MethodFees MethodFeeSettings `json:"method_fees"`
	// LoadBalanceStrategy, ProductNamePrefix, ProductNameSuffix, HelpImageURL and
	// HelpText omit their keys when nil. Supplied values retain raw text
	// including empty; display localization and strategy interpretation occur at
	// their own consumers.
	//
	// requested selection strategy text.
	LoadBalanceStrategy *string `json:"load_balance_strategy"`
	// requested product title prefix.
	ProductNamePrefix *string `json:"product_name_prefix"`
	// requested product title suffix.
	ProductNameSuffix *string `json:"product_name_suffix"`
	// requested help image URL.
	HelpImageURL *string `json:"help_image_url"`
	// requested help display text.
	HelpText *string `json:"help_text"`

	// CancelRateLimitEnabled supplies cancellation limiting enablement only when
	// nonnil. A supplied false is written as false text; nil retains its
	// persisted setting.
	CancelRateLimitEnabled *bool `json:"cancel_rate_limit_enabled"`
	// CancelRateLimitMax and CancelRateLimitWindow omit their keys when nil.
	// Positive integers write decimal text and nonpositive values clear it,
	// selecting the ten-count and one-window parser defaults.
	//
	// cancellation audit count ceiling.
	CancelRateLimitMax *int `json:"cancel_rate_limit_max"`
	// cancellation interval quantity.
	CancelRateLimitWindow *int `json:"cancel_rate_limit_window"`
	// CancelRateLimitUnit and CancelRateLimitMode omit their keys when nil and
	// otherwise retain raw text, including empty. Cancellation threshold
	// calculation interprets the unit and fixed/rolling mode separately.
	//
	// requested cancellation interval unit.
	CancelRateLimitUnit *string `json:"cancel_rate_limit_unit"`
	// requested cancellation calendar mode.
	CancelRateLimitMode *string `json:"cancel_rate_limit_window_mode"`

	// AlipayForceQRCode and AlipayMobilePrecreateDeepLink omit saved preferences
	// when nil and write supplied false as false text. Their frontend/device and
	// official-provider consumers determine the resulting presentation
	// separately.
	//
	// frontend Alipay QR preference.
	AlipayForceQRCode *bool `json:"alipay_force_qrcode"`
	// official mobile precreate preference.
	AlipayMobilePrecreateDeepLink *bool `json:"alipay_mobile_precreate_deep_link"`

	// VisibleMethodAlipaySource and VisibleMethodWxpaySource omit their keys when
	// nil and otherwise retain raw text including empty. Source normalization and
	// availability checks belong to routing or settings participants separately.
	//
	// requested Alipay source choice.
	VisibleMethodAlipaySource *string `json:"payment_visible_method_alipay_source"`
	// requested WeChat source choice.
	VisibleMethodWxpaySource *string `json:"payment_visible_method_wxpay_source"`
	// VisibleMethodAlipayEnabled and VisibleMethodWxpayEnabled omit their keys
	// when nil and write supplied false as false text.
	// ConfigVisibleMethodShouldBeExposed interprets saved exposure together with
	// source availability.
	//
	// requested Alipay exposure flag.
	VisibleMethodAlipayEnabled *bool `json:"payment_visible_method_alipay_enabled"`
	// requested WeChat exposure flag.
	VisibleMethodWxpayEnabled *bool `json:"payment_visible_method_wxpay_enabled"`
}

// MethodLimits records one displayed method's configured bounds, currency,
// label and effective fees.
type MethodLimits struct {
	PaymentType string `json:"payment_type"` // displayed payment method label
	// optional custom display name.
	DisplayName string  `json:"display_name,omitempty"`
	Currency    string  `json:"currency"`    // resolved group payment currency
	FeeRate     float64 `json:"fee_rate"`    // effective fee percentage points
	FixedFee    float64 `json:"fee_fixed"`   // effective fixed currency fee
	DailyLimit  float64 `json:"daily_limit"` // widest configured daily cap
	SingleMin   float64 `json:"single_min"`  // widest configured order minimum
	SingleMax   float64 `json:"single_max"`  // widest configured order maximum
}

// MethodFeeConfig records whether a visible method overrides the legacy
// percentage fee.
type MethodFeeConfig struct {
	// Enabled makes the present method entry effective when true. False retains
	// that stored entry but EffectiveMethodFee selects the legacy percentage rate
	// instead.
	Enabled  bool    `json:"enabled"`
	FixedFee float64 `json:"fixed_fee"` // fixed gateway currency fee
	FeeRate  float64 `json:"fee_rate"`  // fee percentage point rate
}

// MethodFeeSettings maps method labels to fee configuration, with parse/format
// operations normalizing visible aliases.
type MethodFeeSettings map[string]MethodFeeConfig

// MethodLimitsResponse contains displayed method bounds and their global
// widest min/max range.
type MethodLimitsResponse struct {
	// Methods is the newly assembled limits map from GetAvailableMethodLimits.
	// Mixed-currency groups can be absent and callers serialize the supplied map
	// without another instance query.
	Methods   map[string]MethodLimits `json:"methods"`
	GlobalMin float64                 `json:"global_min"` // 0 = no minimum
	GlobalMax float64                 `json:"global_max"` // 0 = no maximum
}

// CreateProviderInstanceRequest carries administrator instance creation fields
// and caller-owned configuration input.
type CreateProviderInstanceRequest struct {
	ProviderKey string `json:"provider_key"` // requested exact adapter key.
	// requested instance display name.
	Name string `json:"name"`
	// Config is supplied to validation and plaintext serialization. EasyPay
	// display-name preparation can mutate this map before a later validation
	// error returns to its caller.
	Config map[string]string `json:"config"`
	// requested method support labels.
	SupportedTypes []string `json:"supported_types"`
	// requested checkout eligibility flag.
	Enabled bool `json:"enabled"`
	// requested payment presentation mode.
	PaymentMode string `json:"payment_mode"`
	// requested instance display order.
	SortOrder int `json:"sort_order"`
	// requested channel limit JSON.
	Limits string `json:"limits"`
	// requested administrator refund flag.
	RefundEnabled bool `json:"refund_enabled"`
	// AllowUserRefund requests buyer refund eligibility on creation.
	// CreateProviderInstance stores true only when both this flag and
	// RefundEnabled are true in that request.
	AllowUserRefund bool `json:"allow_user_refund"`
}

// UpdateProviderInstanceRequest carries optional instance fields whose
// presence selects administrator patch behavior.
type UpdateProviderInstanceRequest struct {
	// Name writes supplied instance text including blank, while nil retains it.
	// UpdateProviderInstance does not repeat creation's nonblank-name validation
	// for this field.
	Name *string `json:"name"`
	// Config is omitted when nil; supplied input merges current config and adds
	// an expected stored-text comparison. Blank recognized secrets retain their
	// existing credential values.
	Config map[string]string `json:"config"`
	// SupportedTypes is omitted when nil and joined when supplied. In-progress
	// orders block removal of any prior exact trimmed label, including alias
	// changes that are not exact matches.
	SupportedTypes []string `json:"supported_types"`
	// Enabled retains checkout eligibility when nil. Supplied false checks
	// in-progress count before disabling, while final enabled state invokes
	// provider construction with the available configuration.
	Enabled *bool `json:"enabled"`
	// PaymentMode writes supplied presentation text including blank, while nil
	// retains it. This field is staged separately from the merged config used by
	// constructor validation.
	PaymentMode *string `json:"payment_mode"`
	// SortOrder writes the supplied integer including zero or negative values,
	// while nil retains it. UpdateProviderInstance performs no separate
	// ordering-range validation for this field.
	SortOrder *int `json:"sort_order"`
	// Limits writes supplied channel-limit text including empty, while nil
	// retains it. This update path performs no JSON validation of the field
	// before persistence; display/selection consumers parse it separately.
	Limits *string `json:"limits"`
	// RefundEnabled retains the stored flag when nil. A supplied false also
	// stages buyer-refund false, coordinating both permission fields in the same
	// instance patch.
	RefundEnabled *bool `json:"refund_enabled"`
	// AllowUserRefund retains the stored flag when nil and writes false when
	// explicitly false. Requested true is staged only when the supplied or
	// current administrator refund flag is true.
	AllowUserRefund *bool `json:"allow_user_refund"`
}

// TestProviderDraftRequest supplies unsaved EasyPay configuration and optional
// historical secret reuse for a query probe.
type TestProviderDraftRequest struct {
	ProviderKey string `json:"provider_key"` // requested draft adapter key.
	// InstanceID selects an existing same-provider row when supplied, so draft
	// testing can merge omitted/blank known secrets. Nil keeps unsaved
	// configuration without that persisted merge.
	InstanceID *int64 `json:"instance_id,omitempty"`
	// Config supplies the unsaved query probe input. Existing-instance testing
	// merges it into newly decoded config; the probe constructs an adapter
	// without saving this map as an instance.
	Config map[string]string `json:"config"`
}

// ProviderDraftTestResult reports completion of the unsaved provider's
// query-only connectivity operation.
type ProviderDraftTestResult struct {
	// Reachable is true when constructor and QueryOrder returned no error.
	// TestProviderDraft does not inspect the returned order observation before
	// selecting this result.
	Reachable bool `json:"reachable"`
}

// GetByID reads a plan and localizes its display using the request context
// language. It returns the plan read error with the localized observation;
// subscription checkout separately requires a nonnil for-sale result.
func (s *ConfigService) GetByID(ctx context.Context, id int64) (*SubscriptionPlan, error) {
	plan, err := s.plans.GetPlan(ctx, id)
	return billing.LocalizePlan(plan, locale.FromContext(ctx)), err
}

// GetPaymentConfig reads string settings and localized display companions,
// then assembles current payment configuration. The repository returns a
// writable map on success because display text is resolved into that map
// before parsing.
//
// Initial batch-read errors abort. Literal booleans and numeric parser
// defaults follow ConfigParsePaymentConfig; display locale follows context.
// Stripe publishable key is observed separately from one enabled instance and
// can be empty after read/decode failure. Visible exposure/source values are
// requested but are not applied to EnabledTypes by this getter, which keeps
// its stored normalized list.
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
	for _, key := range []string{SettingProductNamePrefix, SettingProductNameSuffix, SettingHelpImageURL, SettingHelpText} {
		keys = append(keys, locale.TextSettingKey(key))
	}
	vals, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("get payment config settings: %w", err)
	}
	for _, key := range []string{SettingProductNamePrefix, SettingProductNameSuffix, SettingHelpImageURL, SettingHelpText} {
		vals[key] = locale.ResolveSettingText(vals, key, "", locale.FromContext(ctx))
	}
	cfg := s.ConfigParsePaymentConfig(vals)
	// Observe the public Stripe key separately after settings have parsed. Its
	// failure can leave an empty browser key while the rest of the checkout
	// configuration is still returned.
	cfg.StripePublishableKey = s.ConfigGetStripePublishableKey(ctx)
	return cfg, nil
}

// ConfigParsePaymentConfig converts supplied string settings into checkout
// values using exact true and parser defaults. Numeric parse success can
// retain zero, negative or nonfinite values except where dedicated
// multiplier/conversion normalization applies.
//
// Method fee JSON has its own filtering/rounding rules. The mobile Alipay
// precreate flag alone uses the runtime environment boolean override; callers
// provide that lookup callback through service construction. Blank strategy
// defaults, and nonblank enabled-method text becomes normalized first-seen
// labels. Exposure/source settings are not used by this parser to rewrite that
// list, so their helper calculations remain separate from basic configuration
// decoding.
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
	if cfg.LoadBalanceStrategy == "" {
		cfg.LoadBalanceStrategy = DefaultLoadBalanceStrategy
	}
	if raw := vals[SettingEnabledPaymentTypes]; raw != "" {
		types := make([]string, 0, len(strings.Split(raw, ",")))
		for t := range strings.SplitSeq(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				types = append(types, t)
			}
		}
		cfg.EnabledTypes = NormalizeVisibleMethods(types)
	}
	return cfg
}

// ConfigGetStripePublishableKey reads the first enabled Stripe row with limit
// one and no sort request. Nil store, list error/absence or recoverable config
// decode absence yields empty text; successful lookup returns the exact
// publishableKey field without trimming.
func (s *ConfigService) ConfigGetStripePublishableKey(ctx context.Context) string {
	if s.store == nil {
		return ""
	}
	instances, err := s.store.ListInstances(ctx, InstanceFilter{EnabledOnly: true, ProviderKey: TypeStripe, Limit: 1})
	// A failed or empty enabled Stripe observation supplies no public browser
	// key. Keep other parsed settings available while callers handle this empty
	// optional configuration result.
	if err != nil || len(instances) == 0 {
		return ""
	}
	cfg := s.ConfigDecryptConfig(instances[0].Config)
	if cfg == nil {
		return ""
	}
	return cfg[ConfigKeyPublishableKey]
}

// ConfigParseMethodFeeSettings decodes and normalizes fee overrides, returning
// a newly allocated empty map for blank/malformed JSON. It skips blank
// normalized keys and negative fee components, then rounds retained values to
// two decimals.
//
// Parsing does not enforce the save validator's supported-method or rate
// ceiling rules. Alias keys can normalize to the same map key, and the later
// map-iteration entry wins without defined ordering. Enabled false entries
// remain stored but do not override EffectiveMethodFee's legacy choice. This
// preserves validly parsed configuration observations rather than proving
// every persisted override passed administrator validation.
func ConfigParseMethodFeeSettings(raw string) MethodFeeSettings {
	if strings.TrimSpace(raw) == "" {
		return MethodFeeSettings{}
	}
	var settings MethodFeeSettings
	// Unreadable override JSON supplies no method-specific fees. Return an empty
	// map so EffectiveMethodFee can retain the legacy percentage choice instead
	// of a partially decoded override.
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

// ConfigFormatMethodFeeSettings normalizes and rounds supplied fee entries for
// JSON persistence, returning empty text for nil input and an empty object for
// no retained entries or marshal failure. Callers validate supported methods
// and finite ranges before formatting.
//
// Nonblank alias keys can collide at one normalized key with
// map-order-dependent replacement. Each retained entry keeps Enabled and
// rounds both components to two decimals before encoding. This formatter adds
// no independent negative/rate validation; PreparePaymentConfig owns those
// checks and distinguishes nil omission from an empty supplied map.
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
	// Failed override JSON encoding is converted to an empty object for this
	// formatter. PreparePaymentConfig validates normal submitted values first;
	// direct unencodable helper input therefore loses its entries.
	if err != nil {
		return "{}"
	}
	return string(data)
}

// ConfigValidateMethodFeeSettings requires supported normalized visible method
// keys and finite nonnegative two-decimal fee components, with rate at most
// one hundred. It validates without rewriting or copying the supplied map; map
// iteration determines which invalid entry is returned first.
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

// ConfigIsSupportedMethodFeeMethod reports Stripe, Alipay, WeChat or GMPay
// fee method identity. Internal EasyPay source labels and other normalized
// methods return false, so overrides attach to visible checkout methods rather
// than arbitrary provider keys.
func ConfigIsSupportedMethodFeeMethod(method string) bool {
	// Normalize visible method identity before checking fee support so historical
	// direct aliases share their checkout override. Internal source labels still
	// remain outside the supported visible fee categories.
	switch NormalizeVisibleMethod(method) {
	case TypeStripe, TypeAlipay, TypeWxpay, TypeEPUSDT:
		return true
	default:
		return false
	}
}

// ConfigPcValidMoney2 reports finite nonnegative values representable at two
// decimals within its scaled floating tolerance. It compares v*100 with its
// rounded integer using strict less-than 1e-9, so administrator override
// validation tolerates that narrow binary residue.
func ConfigPcValidMoney2(v float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return false
	}
	// Allow less than 1e-9 residue after scaling to hundredths.
	return math.Abs(math.Round(v*100)-v*100) < 1e-9
}

// ConfigPcValidRate2 accepts ConfigPcValidMoney2's finite nonnegative
// hundredth-valid values with an inclusive one-hundred ceiling. Method
// override validation treats false as a rejected percentage setting before its
// formatter runs.
func ConfigPcValidRate2(v float64) bool {
	// One hundred percent is the accepted override rate ceiling.
	return ConfigPcValidMoney2(v) && v <= 100
}

// ConfigPcRound2 rounds the supplied numeric display/config value to two
// decimals.
func ConfigPcRound2(v float64) float64 {
	// Round at hundredths before returning the configured/display quantity.
	return math.Round(v*100) / 100
}

// EffectiveMethodFee returns an enabled override for the normalized visible
// method, otherwise the legacy percentage rate. Nil configuration returns zero
// fees; a present disabled override does not suppress the legacy fee.
func (cfg *PaymentConfig) EffectiveMethodFee(method string) FeeConfig {
	method = NormalizeVisibleMethod(method)
	if cfg == nil {
		return FeeConfig{}
	}
	// Only an enabled normalized method entry replaces the legacy percentage.
	// Absent or disabled entries leave that historical fee choice active, rather
	// than selecting an explicit no-fee result.
	if method != "" {
		if methodFee, ok := cfg.MethodFees[method]; ok && methodFee.Enabled {
			return FeeConfig{FixedFee: methodFee.FixedFee, FeeRate: methodFee.FeeRate}
		}
	}
	return FeeConfig{FeeRate: cfg.RechargeFeeRate}
}

// UpdatePaymentConfig validates/builds supplied setting values and delegates
// one partial SetMultiple write. Omitted fields are absent from that map,
// while supplied clearing/false values follow their formatter rules;
// repository errors remain visible.
func (s *ConfigService) UpdatePaymentConfig(ctx context.Context, req UpdatePaymentConfigRequest) error {
	values, err := PreparePaymentConfig(req)
	if err != nil {
		return err
	}
	return s.settingRepo.SetMultiple(ctx, values)
}

// PreparePaymentConfig validates selected numeric/fee inputs and builds a new
// partial string-setting map without persistence. Nil pointers/maps/slices
// omit their keys, while supplied zero/false/empty inputs take their
// corresponding formatting path.
//
// Multiplier must be finite positive, subscription rate finite nonnegative,
// and legacy fee finite within zero through one hundred with exact two-decimal
// scaling. Method overrides use their separate tolerance validator. Other
// numeric/text fields receive no equivalent broad validation here:
// positive-only helpers can clear values to parser defaults or serialize
// successfully supplied nonfinite text. Standalone settings service and
// combined settings participant share this prepared map for their later write.
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
		// Legacy percentage updates require exact hundredth scaling here, unlike
		// method overrides' narrow tolerance. Reject extra fractions before the
		// later two-decimal formatter could silently round that submitted rate.
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

// ConfigFormatBoolOrEmpty returns empty for nil input and literal boolean text
// otherwise. PreparePaymentConfig checks presence before calling, so a
// supplied false writes false while omitted fields produce no setting entry.
func ConfigFormatBoolOrEmpty(v *bool) string {
	if v == nil {
		return ""
	}
	return strconv.FormatBool(*v)
}

// ConfigFormatPositiveFloat returns empty for nil/nonpositive values,
// otherwise two-decimal float text. Clearing selects the owning parser's
// default; this helper does not reject nonfinite input, and a small positive
// value can round to zero text.
func ConfigFormatPositiveFloat(v *float64) string {
	if v == nil || *v <= 0 {
		return ""
	}
	// Serialize ordinary money settings at two decimal places.
	return strconv.FormatFloat(*v, 'f', 2, 64)
}

// ConfigFormatPositiveFloatExact returns empty for nil/nonpositive input and
// full fixed float text otherwise. The helper adds no finite-value check;
// subscription updates validate before calling, so zero disables conversion
// and positive precision remains intact.
func ConfigFormatPositiveFloatExact(v *float64) string {
	if v == nil || *v <= 0 {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// ConfigFormatNonNegativeFloat returns empty for nil/negative input and
// two-decimal float text otherwise, including zero. Its helper-level
// comparison does not reject NaN/Inf; the legacy fee update validates those
// before serialization.
func ConfigFormatNonNegativeFloat(v *float64) string {
	if v == nil || *v < 0 {
		return ""
	}
	// Serialize the retained fee value at two decimals.
	return strconv.FormatFloat(*v, 'f', 2, 64)
}

// ConfigFormatPositiveInt returns decimal integer text for positive input and
// empty for nil/nonpositive values. Prepared updates call it only for present
// fields, so clearing selects the owning numeric parser's configured default.
func ConfigFormatPositiveInt(v *int) string {
	if v == nil || *v <= 0 {
		return ""
	}
	return strconv.Itoa(*v)
}

// ConfigDerefStr returns the exact pointed-to setting text or empty for nil
// input. PreparePaymentConfig checks field presence before calling, so omitted
// text fields create no key while supplied empty text clears theirs.
func ConfigDerefStr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// ConfigSplitTypes returns trimmed nonblank comma labels in order, retaining
// duplicates and spelling. Blank input returns an allocated empty slice for
// administrator JSON; visible alias normalization belongs to separate method
// consumers.
func ConfigSplitTypes(s string) []string {
	if strings.TrimSpace(s) == "" {
		// An absent support list is displayed as an array in administrator JSON.
		// Allocate the empty slice so this response representation stays distinct
		// from a null collection.
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

// ConfigJoinTypes joins supplied method labels with commas without
// normalization or validation.
func ConfigJoinTypes(types []string) string {
	return strings.Join(types, ",")
}

// ConfigPcParseFloat returns fallback for exact empty or parse-error text and
// the parsed float otherwise. It does not trim text or reject
// finite-sign/nonfinite values, so callers apply their own domain
// normalization.
func ConfigPcParseFloat(s string, defaultVal float64) float64 {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.ParseFloat(s, 64)
	// Malformed stored floating setting text uses the supplied parser default for
	// this read. Successful zero or negative values remain available to the
	// owning caller's later rule.
	if err != nil {
		return defaultVal
	}
	return v
}

// ConfigPcParseInt returns fallback for exact empty or Atoi failure and the
// parsed integer otherwise. Zero/negative successful input remains available
// to the caller's subsequent defaulting or validation rules.
func ConfigPcParseInt(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	// Malformed stored integer setting text uses the supplied parser default for
	// this read. Successful zero or negative values remain available to the
	// owning caller's later rule.
	if err != nil {
		return defaultVal
	}
	return v
}

// ConfigBuildVisibleMethodSourceAvailability reports configured
// official/EasyPay source support from supplied nonnil instances. It does not
// inspect enabled flags; callers provide the eligible collection before using
// this map for exposure.
//
// Official blank support implies its own visible method, and recognized
// support aliases also count. EasyPay source flags require listed normalized
// Alipay/WeChat labels. The result records whether each source appears, rather
// than selecting one instance or validating current credentials. This helper
// is independent of ConfigParsePaymentConfig's stored EnabledTypes behavior.
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

// ConfigApplyVisibleMethodRoutingToEnabledTypes returns normalized first-seen
// labels after applying the supplied exposure/source availability rules for
// Alipay/WeChat. Caller inputs are read without mutation and unrelated method
// labels retain their normalized order.
//
// Canonical visible methods are removed when their exact true exposure and
// recognized available source do not qualify. Qualified Alipay/WeChat are
// appended even when absent from base, with deduplication preventing repeats.
// Empty input can therefore gain those methods. This is a callable routing
// calculation; GetPaymentConfig does not invoke it when parsing the stored
// method list.
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

// ConfigVisibleMethodShouldBeExposed reports exact true exposure with a
// recognized nonempty source marked available for normalized Alipay/WeChat.
// Unsupported methods, missing flags/source or absent availability return
// false; this helper does not query or validate instances itself.
func ConfigVisibleMethodShouldBeExposed(method string, vals map[string]string, available map[string]bool) bool {
	enabledKey := ResumeVisibleMethodEnabledSettingKey(method)
	sourceKey := ResumeVisibleMethodSourceSettingKey(method)
	if enabledKey == "" || sourceKey == "" || vals[enabledKey] != "true" {
		return false
	}
	source := NormalizeVisibleMethodSource(method, vals[sourceKey])
	return source != "" && available[source]
}
