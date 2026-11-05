package payment

import (
	"math"

	"github.com/shopspring/decimal"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// amount_rules.go calculates credited recharge amounts and proportional
// gateway refunds using their respective rounding rules. Account overrides
// replace global recharge terms for previews and newly created balance orders.
// A balance recharge can credit a different reasoning-point amount from
// the external payment because its configured multiplier applies before
// credit rounding. Subscription currency conversion has a separate rate:
// zero disables it, while an invalid global balance multiplier selects the
// ordinary one-to-one credit. Invalid personal overrides fail selection instead
// of silently changing the buyer's terms.
//
// Refund planning relates the credited or purchased order amount to the
// channel charge, which can include fees. A requested refund within the
// currency tolerance of the order amount uses the complete rounded
// channel charge; other positive requests use a proportional amount.
// Currency-specific precision governs that channel result, while
// reasoning-point credits retain their own two-decimal rounding.

// Amount defaults govern recharge scaling and legacy payment comparison
// tolerance, while currency helpers select finer margins for three-decimal
// API amounts.
const (
	DefaultBalanceRechargeMultiplier = 1.0  // one-to-one recharge credit
	ProviderAmountTolerance          = 0.01 // legacy absolute payment margin
	// BalanceCreditCeiling is exclusive because redemption values and wallet
	// balances use numeric(20,8), leaving twelve integer digits for credited units.
	BalanceCreditCeiling = 1e12
)

// BalanceRechargeRates carries the default, personal, and effective checkout rates.
type BalanceRechargeRates struct {
	// Default retains the normalized global rate; Effective selects User when it
	// is present. User is copied so callers can keep an independent rate snapshot.
	Default   float64  // normalized global recharge rate
	Effective float64  // selected balance recharge rate
	User      *float64 // optional personal recharge rate
}

// IsValidCreditedBalance reports whether rounded credits fit positive wallet
// and redemption storage amounts.
func IsValidCreditedBalance(amount float64) bool {
	return amount > 0 && amount < BalanceCreditCeiling && !math.IsNaN(amount) && !math.IsInf(amount, 0)
}

// ResolveBalanceRechargeRates selects an account override or the normalized
// global recharge rate. A nil override follows global settings; a present
// override must be finite and positive or the operation fails.
//
// The returned personal value is copied so later account changes cannot alter
// this observation. Both checkout display and order creation use this selection,
// preserving the distinction between an explicit rate equal to the default and
// an account that inherits future global changes.
//
// @project-doc docs/domains/payments_and_entitlements.md#personal_recharge_multiplier
func ResolveBalanceRechargeRates(global float64, override *float64) (BalanceRechargeRates, error) {
	rates := BalanceRechargeRates{Default: NormalizeBalanceRechargeMultiplier(global)}
	rates.Effective = rates.Default
	if override != nil {
		value := *override
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
			return BalanceRechargeRates{}, apperror.BadRequest("INVALID_BALANCE_RECHARGE_MULTIPLIER", "balance recharge multiplier must be a finite number greater than 0")
		}
		rates.User = &value
		rates.Effective = value
	}
	return rates, nil
}

// NormalizeBalanceRechargeMultiplier returns a usable positive finite
// recharge multiplier. Nonpositive, NaN, or infinite input selects the
// default one-to-one credit, while any other value is returned unchanged
// for checkout credit calculation.
func NormalizeBalanceRechargeMultiplier(multiplier float64) float64 {
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier <= 0 {
		return DefaultBalanceRechargeMultiplier
	}
	return multiplier
}

// NormalizeSubscriptionUSDToCNYRate returns a finite nonnegative conversion
// rate, mapping negative or nonfinite input to zero. Zero disables the
// checkout subscription conversion; a positive value remains available
// for the CNY base-amount calculation.
func NormalizeSubscriptionUSDToCNYRate(rate float64) float64 {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return 0
	}
	return rate
}

// CalculateCreditedBalance multiplies the payment amount by the normalized
// recharge multiplier and rounds the credit to two decimal places. Pass
// a finite payment amount; business amount limits are validated by the
// checkout caller before this decimal calculation.
func CalculateCreditedBalance(paymentAmount, multiplier float64) float64 {
	// Reasoning-point credits retain two decimal places even when the
	// selected channel currency uses a different accepted amount precision.
	return decimal.NewFromFloat(paymentAmount).
		Mul(decimal.NewFromFloat(NormalizeBalanceRechargeMultiplier(multiplier))).
		Round(2).
		InexactFloat64()
}

// CalculateGatewayRefundAmount returns the channel amount corresponding
// to a requested refund of the recorded order amount. Nonpositive
// amounts return zero; callers supply finite values and validate the
// permitted refund size before calculation.
//
// A request within the currency tolerance of the full order amount
// returns the original channel charge rounded to its accepted precision.
// Other positive requests scale payAmount by refundAmount divided by
// orderAmount and round that result, preserving the distinction between
// entitlement amount and the channel amount including fees.
func CalculateGatewayRefundAmount(orderAmount, payAmount, refundAmount float64, currency string) float64 {
	if orderAmount <= 0 || payAmount <= 0 || refundAmount <= 0 {
		return 0
	}
	fractionDigits := int32(CurrencyMaxFractionDigits(currency))
	// A tolerated difference from the full entitlement amount selects the
	// complete channel charge, so a near-full request does not leave a
	// proportional fraction of that charge behind.
	if math.Abs(refundAmount-orderAmount) <= PaymentAmountToleranceForCurrency(currency) {
		return decimal.NewFromFloat(payAmount).Round(fractionDigits).InexactFloat64()
	}
	// Scale the original channel charge by the entitlement refund fraction
	// before applying currency rounding, carrying the fee proportion with
	// the partial refund.
	return decimal.NewFromFloat(payAmount).
		Mul(decimal.NewFromFloat(refundAmount)).
		Div(decimal.NewFromFloat(orderAmount)).
		Round(fractionDigits).
		InexactFloat64()
}

// PaymentAmountToleranceForCurrency returns the comparison margin for
// paid amounts and refund sizing. Zero- and two-decimal API scales use
// the legacy absolute margin; finer scales use half a minor unit from
// the currency helper.
func PaymentAmountToleranceForCurrency(currency string) float64 {
	minorUnit := CurrencyMinorUnit(currency)
	// Retain the legacy margin for whole- and two-decimal API amounts.
	if minorUnit <= 2 {
		return ProviderAmountTolerance
	}
	// Convert the API scale into a half-minor-unit comparison margin.
	return math.Pow10(-minorUnit) / 2
}
