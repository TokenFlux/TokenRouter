package payment

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// amount_rules_test.go checks recharge selection and rounding independently of
// channel fees. Explicit account overrides remain distinguishable from inherited
// terms, including equal rates, while invalid override values cannot produce a
// misleading successful preview or credit amount.

// TestPaymentAmountToleranceForThreeDecimalCurrency compares CNY, JPY,
// and KWD margins. It keeps the legacy tolerance for the first two
// currencies and checks a half-minor-unit margin for KWD.
func TestPaymentAmountToleranceForThreeDecimalCurrency(t *testing.T) {
	t.Parallel()

	assert.Equal(t, ProviderAmountTolerance, PaymentAmountToleranceForCurrency("CNY"))
	assert.Equal(t, ProviderAmountTolerance, PaymentAmountToleranceForCurrency("JPY"))
	// Allow binary representation noise in the half-minor-unit float comparison.
	assert.InDelta(t, 0.0005, PaymentAmountToleranceForCurrency("KWD"), 1e-12)
}

// TestCalculateCreditedBalanceStillUsesRechargeMultiplier applies a
// fractional multiplier and a tenfold multiplier to recharge amounts.
// It checks the two exact credited results and keeps the paid amounts
// distinguishable from the resulting credited reasoning points.
func TestCalculateCreditedBalanceStillUsesRechargeMultiplier(t *testing.T) {
	t.Parallel()

	got := CalculateCreditedBalance(10, 0.14)
	if got != 1.4 {
		t.Fatalf("credited balance = %v, want 1.4", got)
	}

	got = CalculateCreditedBalance(69.90, 10)
	if got != 699 {
		t.Fatalf("credited balance = %v, want 699", got)
	}
}

// TestResolveBalanceRechargeRates varies inheritance, explicit equal rates and
// fractional overrides. Effective credits preserve decimal rounding and returned
// overrides remain detached from their input pointers.
func TestResolveBalanceRechargeRates(t *testing.T) {
	for _, value := range []float64{18, 10, 0.5, 1, 1.23456} {
		original := value
		rates, err := ResolveBalanceRechargeRates(10, &value)
		require.NoError(t, err)
		require.Equal(t, 10.0, rates.Default)
		require.Equal(t, original, rates.Effective)
		value = 99
		require.Equal(t, original, *rates.User)
	}
	rates, err := ResolveBalanceRechargeRates(10, nil)
	require.NoError(t, err)
	require.Nil(t, rates.User)
	require.Equal(t, 10.0, rates.Effective)
	require.Equal(t, 1800.0, CalculateCreditedBalance(100, 18))
	require.Equal(t, 12.35, CalculateCreditedBalance(10, 1.23456))
	for _, value := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := ResolveBalanceRechargeRates(10, &value)
		require.Error(t, err)
	}
}

// TestRechargeCreditRoundingAndStorageBounds checks decimal half-cent ties and
// wallet limits. The selected amount must be deliverable before checkout can
// create an external payment for the buyer.
func TestRechargeCreditRoundingAndStorageBounds(t *testing.T) {
	for _, tc := range []struct {
		principal float64 // requested payment principal
		rate      float64 // selected recharge multiplier
		want      float64 // expected rounded credit
	}{
		{1, 1.005, 1.01}, {100, 0.01005, 1.01}, {100, 0.00035, 0.04}, {1e7, 1e-7, 1},
	} {
		require.Equal(t, tc.want, CalculateCreditedBalance(tc.principal, tc.rate))
	}
	for _, amount := range []float64{0, -1, 1e12, math.Inf(1), math.NaN()} {
		require.False(t, IsValidCreditedBalance(amount))
	}
	require.True(t, IsValidCreditedBalance(0.01))
	require.True(t, IsValidCreditedBalance(999999999999.99))
}
