//go:build unit

package payment_test

import (
	"errors"
	"math"
	"testing"

	paymenttestkit "github.com/TokenFlux/TokenRouter/internal/payment/testkit"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRedeemAction_CodeNotFound(t *testing.T) {
	t.Parallel()
	action := payment.ResolveRedeemAction(nil, nil)
	assert.Equal(t, payment.RedeemActionCreate, action, "nil code with nil error should create")
}

func TestResolveRedeemAction_LookupError(t *testing.T) {
	t.Parallel()
	action := payment.ResolveRedeemAction(nil, errors.New("db connection lost"))
	assert.Equal(t, payment.RedeemActionCreate, action, "lookup error should fall back to create")
}

func TestResolveRedeemAction_LookupErrorWithNonNilCode(t *testing.T) {
	t.Parallel()
	// Edge case: both code and error are non-nil (shouldn't happen in practice,
	// but the function should still treat error as authoritative)
	code := &billing.RedeemCode{Status: billing.StatusUnused}
	action := payment.ResolveRedeemAction(code, errors.New("partial error"))
	assert.Equal(t, payment.RedeemActionCreate, action, "non-nil error should always result in create regardless of code")
}

func TestResolveRedeemAction_CodeExistsAndUsed(t *testing.T) {
	t.Parallel()
	code := &billing.RedeemCode{
		Code:      "test-code-123",
		Status:    billing.StatusUsed,
		Type:      billing.RedeemTypeBalance,
		Value:     10.0,
		MaxUses:   1,
		UsedCount: 1,
	}
	action := payment.ResolveRedeemAction(code, nil)
	assert.Equal(t, payment.RedeemActionSkipCompleted, action, "used code should skip to completed")
}

func TestResolveRedeemAction_CodeExistsAndUnused(t *testing.T) {
	t.Parallel()
	code := &billing.RedeemCode{
		Code:   "test-code-456",
		Status: billing.StatusUnused,
		Type:   billing.RedeemTypeBalance,
		Value:  25.0,
	}
	action := payment.ResolveRedeemAction(code, nil)
	assert.Equal(t, payment.RedeemActionRedeem, action, "unused code should skip creation and proceed to redeem")
}

func TestResolveRedeemAction_CodeExistsWithExpiredStatus(t *testing.T) {
	t.Parallel()
	// A code with a non-standard status (neither "unused" nor "used")
	// should NOT be treated as used, so it falls through to payment.RedeemActionRedeem.
	code := &billing.RedeemCode{
		Code:   "expired-code",
		Status: billing.StatusExpired,
	}
	action := payment.ResolveRedeemAction(code, nil)
	assert.Equal(t, payment.RedeemActionRedeem, action, "expired-status code is not IsUsed(), should redeem")
}

func TestResolveRedeemAction_Table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		code     *billing.RedeemCode
		err      error
		expected payment.RedeemAction
	}{
		{
			name:     "nil code, nil error — first run",
			code:     nil,
			err:      nil,
			expected: payment.RedeemActionCreate,
		},
		{
			name:     "nil code, lookup error — treat as not found",
			code:     nil,
			err:      billing.ErrRedeemCodeNotFound,
			expected: payment.RedeemActionCreate,
		},
		{
			name:     "nil code, generic DB error — treat as not found",
			code:     nil,
			err:      errors.New("connection refused"),
			expected: payment.RedeemActionCreate,
		},
		{
			name:     "code exists, used — previous run completed redeem",
			code:     &billing.RedeemCode{Status: billing.StatusUsed, MaxUses: 1, UsedCount: 1},
			err:      nil,
			expected: payment.RedeemActionSkipCompleted,
		},
		{
			name:     "code exists, unused — previous run created code but crashed before redeem",
			code:     &billing.RedeemCode{Status: billing.StatusUnused},
			err:      nil,
			expected: payment.RedeemActionRedeem,
		},
		{
			name:     "code exists but error also set — error takes precedence",
			code:     &billing.RedeemCode{Status: billing.StatusUsed, MaxUses: 1, UsedCount: 1},
			err:      errors.New("unexpected"),
			expected: payment.RedeemActionCreate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := payment.ResolveRedeemAction(tt.code, tt.err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestRedeemAction_DistinctValues(t *testing.T) {
	t.Parallel()
	// Ensure the three actions have distinct values (iota correctness)
	assert.NotEqual(t, payment.RedeemActionCreate, payment.RedeemActionRedeem)
	assert.NotEqual(t, payment.RedeemActionCreate, payment.RedeemActionSkipCompleted)
	assert.NotEqual(t, payment.RedeemActionRedeem, payment.RedeemActionSkipCompleted)
}

func TestResolveRedeemAction_IsUsedCanUseConsistency(t *testing.T) {
	t.Parallel()

	usedCode := &billing.RedeemCode{Status: billing.StatusUsed, MaxUses: 1, UsedCount: 1}
	unusedCode := &billing.RedeemCode{Status: billing.StatusUnused, MaxUses: 1}

	// Verify our decision function is consistent with the domain model methods
	assert.True(t, usedCode.IsUsed())
	assert.False(t, usedCode.CanUse())
	assert.Equal(t, payment.RedeemActionSkipCompleted, payment.ResolveRedeemAction(usedCode, nil))

	assert.False(t, unusedCode.IsUsed())
	assert.True(t, unusedCode.CanUse())
	assert.Equal(t, payment.RedeemActionRedeem, payment.ResolveRedeemAction(unusedCode, nil))
}

func TestExpectedNotificationProviderKeyPrefersOrderInstanceProvider(t *testing.T) {
	t.Parallel()

	registry := payment.NewRegistry()
	registry.Register(paymenttestkit.StaticProvider{
		Key:   payment.TypeAlipay,
		Types: []payment.PaymentType{payment.TypeAlipay},
	})

	assert.Equal(t,
		payment.TypeEasyPay,
		payment.ExpectedNotificationProviderKey(registry, payment.TypeAlipay, "", payment.TypeEasyPay),
	)
}

func TestExpectedNotificationProviderKeyUsesRegistryMappingForLegacyOrders(t *testing.T) {
	t.Parallel()

	registry := payment.NewRegistry()
	registry.Register(paymenttestkit.StaticProvider{
		Key:   payment.TypeEasyPay,
		Types: []payment.PaymentType{payment.TypeAlipay},
	})

	assert.Equal(t,
		payment.TypeEasyPay,
		payment.ExpectedNotificationProviderKey(registry, payment.TypeAlipay, "", ""),
	)
}

func TestExpectedNotificationProviderKeyFallsBackToPaymentType(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		payment.TypeWxpay,
		payment.ExpectedNotificationProviderKey(nil, payment.TypeWxpay, "", ""),
	)
}

func TestExpectedNotificationProviderKeyPrefersOrderSnapshotProviderKey(t *testing.T) {
	t.Parallel()

	registry := payment.NewRegistry()
	registry.Register(paymenttestkit.StaticProvider{
		Key:   payment.TypeAlipay,
		Types: []payment.PaymentType{payment.TypeAlipay},
	})

	assert.Equal(t,
		payment.TypeEasyPay,
		payment.ExpectedNotificationProviderKey(registry, payment.TypeAlipay, payment.TypeEasyPay, ""),
	)
}

func TestExpectedNotificationProviderKeyForOrderUsesSnapshotProviderKey(t *testing.T) {
	t.Parallel()

	registry := payment.NewRegistry()
	registry.Register(paymenttestkit.StaticProvider{
		Key:   payment.TypeAlipay,
		Types: []payment.PaymentType{payment.TypeAlipay},
	})

	order := &payment.Order{
		PaymentType: payment.TypeAlipay,
		ProviderSnapshot: map[string]any{
			"schema_version": 1,
			"provider_key":   payment.TypeEasyPay,
		},
	}

	assert.Equal(t,
		payment.TypeEasyPay, payment.ExpectedNotificationProviderKeyForOrder(registry, order, ""),
	)
}

func TestValidateProviderNotificationMetadataRejectsWxpaySnapshotMismatch(t *testing.T) {
	t.Parallel()

	order := &payment.Order{
		PaymentType: payment.TypeWxpay,
		ProviderSnapshot: map[string]any{
			"schema_version":  1,
			"merchant_app_id": "wx-app-expected",
			"merchant_id":     "mch-expected",
			"currency":        "CNY",
		},
	}

	err := payment.ValidateProviderNotificationMetadata(order, payment.TypeWxpay, map[string]string{
		"appid":       "wx-app-other",
		"mchid":       "mch-expected",
		"currency":    "CNY",
		"trade_state": "SUCCESS",
	})
	assert.ErrorContains(t, err, "wxpay appid mismatch")
}

func TestValidateProviderNotificationMetadataAllowsLegacyOrdersWithoutSnapshotFields(t *testing.T) {
	t.Parallel()

	order := &payment.Order{
		PaymentType: payment.TypeWxpay,
		ProviderSnapshot: map[string]any{
			"schema_version":       1,
			"provider_instance_id": "9",
			"provider_key":         payment.TypeWxpay,
		},
	}

	err := payment.ValidateProviderNotificationMetadata(order, payment.TypeWxpay, map[string]string{
		"appid":       "wx-app-runtime",
		"mchid":       "mch-runtime",
		"currency":    "CNY",
		"trade_state": "SUCCESS",
	})
	assert.NoError(t, err)
}

func TestIsValidProviderAmount(t *testing.T) {
	t.Parallel()

	assert.True(t, payment.IsValidProviderAmount(0.01))
	assert.False(t, payment.IsValidProviderAmount(0))
	assert.False(t, payment.IsValidProviderAmount(-1))
	assert.False(t, payment.IsValidProviderAmount(math.NaN()))
	assert.False(t, payment.IsValidProviderAmount(math.Inf(1)))
}

func TestValidateProviderNotificationMetadataRejectsAlipaySnapshotMismatch(t *testing.T) {
	t.Parallel()

	order := &payment.Order{
		PaymentType: payment.TypeAlipay,
		ProviderSnapshot: map[string]any{
			"schema_version":  2,
			"merchant_app_id": "alipay-app-expected",
		},
	}

	err := payment.ValidateProviderNotificationMetadata(order, payment.TypeAlipay, map[string]string{
		"app_id": "alipay-app-other",
	})
	assert.ErrorContains(t, err, "alipay app_id mismatch")
}

func TestValidateProviderNotificationMetadataRejectsEasyPaySnapshotMismatch(t *testing.T) {
	t.Parallel()

	order := &payment.Order{
		PaymentType: payment.TypeAlipay,
		ProviderSnapshot: map[string]any{
			"schema_version": 2,
			"merchant_id":    "pid-expected",
		},
	}

	err := payment.ValidateProviderNotificationMetadata(order, payment.TypeEasyPay, map[string]string{
		"pid": "pid-other",
	})
	assert.ErrorContains(t, err, "easypay pid mismatch")
}

func TestValidateProviderNotificationMetadataRejectsAirwallexSnapshotMismatch(t *testing.T) {
	t.Parallel()

	order := &payment.Order{
		PaymentType: payment.TypeAirwallex,
		ProviderSnapshot: map[string]any{
			"schema_version": 2,
			"merchant_id":    "acct_expected",
			"currency":       "CNY",
		},
	}

	err := payment.ValidateProviderNotificationMetadata(order, payment.TypeAirwallex, map[string]string{
		"account_id": "acct_other",
		"currency":   "CNY",
		"status":     "SUCCEEDED",
	})
	assert.ErrorContains(t, err, "airwallex account_id mismatch")

	err = payment.ValidateProviderNotificationMetadata(order, payment.TypeAirwallex, map[string]string{
		"account_id": "acct_expected",
		"currency":   "USD",
		"status":     "SUCCEEDED",
	})
	assert.ErrorContains(t, err, "airwallex currency mismatch")
}

func TestValidateProviderNotificationMetadataRejectsStripeCurrencyMismatch(t *testing.T) {
	t.Parallel()

	order := &payment.Order{
		PaymentType: payment.TypeStripe,
		ProviderSnapshot: map[string]any{
			"schema_version": 2,
			"currency":       "HKD",
		},
	}

	err := payment.ValidateProviderNotificationMetadata(order, payment.TypeStripe, map[string]string{
		"currency": "USD",
	})
	assert.ErrorContains(t, err, "stripe currency mismatch")
}

func TestPaymentAmountToleranceForThreeDecimalCurrency(t *testing.T) {
	t.Parallel()

	assert.Equal(t, payment.ProviderAmountTolerance, payment.PaymentAmountToleranceForCurrency("CNY"))
	assert.Equal(t, payment.ProviderAmountTolerance, payment.PaymentAmountToleranceForCurrency("JPY"))
	assert.InDelta(t, 0.0005, payment.PaymentAmountToleranceForCurrency("KWD"), 1e-12)
}

// TestAffiliateRebateBasePointsUsesConfiguredReasoningPointBase 锁定余额和订阅订单的返利基数口径。
func TestAffiliateRebateBasePointsUsesConfiguredReasoningPointBase(t *testing.T) {
	t.Parallel()
	monthlyPoints := 1000.0
	weeklyPoints := 300.0
	dailyPoints := 100.0
	zeroPoints := 0.0

	tests := []struct {
		name  string
		order *payment.Order
		want  float64
	}{
		{
			name: "余额订单使用实际到账积分",
			order: &payment.Order{
				OrderType: payment.OrderTypeBalance,
				Amount:    1000,
				PayAmount: 100,
			},
			want: 1000,
		},
		{
			name: "订阅订单优先使用月度积分额度",
			order: &payment.Order{
				OrderType: payment.OrderTypeSubscription,
				Amount:    100,
				PayAmount: 100,
				PlanSnapshot: billing.SubscriptionPlanSnapshot{
					MonthlyLimitUSD: &monthlyPoints,
					WeeklyLimitUSD:  &weeklyPoints,
					DailyLimitUSD:   &dailyPoints,
				},
			},
			want: 1000,
		},
		{
			name: "没有月额度时使用周额度",
			order: &payment.Order{
				OrderType: payment.OrderTypeSubscription,
				Amount:    100,
				PayAmount: 100,
				PlanSnapshot: billing.SubscriptionPlanSnapshot{
					WeeklyLimitUSD: &weeklyPoints,
					DailyLimitUSD:  &dailyPoints,
				},
			},
			want: 300,
		},
		{
			name: "没有月周额度时使用日额度",
			order: &payment.Order{
				OrderType: payment.OrderTypeSubscription,
				Amount:    100,
				PayAmount: 100,
				PlanSnapshot: billing.SubscriptionPlanSnapshot{
					DailyLimitUSD: &dailyPoints,
				},
			},
			want: 100,
		},
		{
			name: "跳过无效月额度后使用周额度",
			order: &payment.Order{
				OrderType: payment.OrderTypeSubscription,
				Amount:    100,
				PayAmount: 100,
				PlanSnapshot: billing.SubscriptionPlanSnapshot{
					MonthlyLimitUSD: &zeroPoints,
					WeeklyLimitUSD:  &weeklyPoints,
				},
			},
			want: 300,
		},
		{
			name: "没有额度时不使用套餐价格兜底",
			order: &payment.Order{
				OrderType: payment.OrderTypeSubscription,
				Amount:    100,
				PayAmount: 100,
			},
		},
		{name: "空订单不产生返利基数"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, payment.AffiliateRebateBasePoints(tt.order))
		})
	}
}
