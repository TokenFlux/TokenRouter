package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// limits.go validates the administrator's per-plugin spending and delivery
// policy. Omitted fields inherit conservative defaults, while explicit zeroes
// forbid the associated operation. Monetary ceilings retain decimal JSON text
// so ledger comparisons can use exact arithmetic without float rounding.
//
// Limits belong to the installation and survive version updates and rollback.
// Unknown fields, nulls, negative values, excessive precision, and invalid plan
// identifiers fail before replacing that policy. Parsing produces a complete
// value object for capability checks, and serialization records all resolved
// defaults so an administrator can review the exact policy currently applied.

// Monetary patterns distinguish the canonical operation string contract from JSON
// numeric notation, whose exponent form can represent the same exact quota value.
var (
	limitAmountPattern     = regexp.MustCompile(`^(0|[1-9][0-9]{0,6})(\.[0-9]{1,8})?$`)
	limitJSONNumberPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
)

// Limits combines the independently enforced capability groups for one installation.
type Limits struct {
	Wallet       WalletLimits       `json:"wallet"`       // balance adjustment spending policy
	Redeem       RedeemLimits       `json:"redeem"`       // redeem code issuance policy
	Subscription SubscriptionLimits `json:"subscription"` // subscription entitlement grant policy
	Email        EmailLimits        `json:"email"`        // outbound notification delivery policy
}

// WalletLimits bounds each adjustment and aggregate daily credit amounts.
type WalletLimits struct {
	MaxCreditPerOp         json.Number `json:"max_credit_per_op"`           // USD
	MaxCreditPerUserPerDay json.Number `json:"max_credit_per_user_per_day"` // USD
	MaxCreditPerDay        json.Number `json:"max_credit_per_day"`          // USD
	MaxDebitPerOp          json.Number `json:"max_debit_per_op"`            // USD
}

// RedeemLimits bounds generated code values, daily issuance, and eligible plans.
type RedeemLimits struct {
	MaxBalancePerCode          json.Number `json:"max_balance_per_code"`           // USD
	MaxBalanceValuePerDay      json.Number `json:"max_balance_value_per_day"`      // USD
	MaxConcurrencyPerCode      int64       `json:"max_concurrency_per_code"`       // concurrent request slots
	MaxCodesPerDay             int64       `json:"max_codes_per_day"`              // daily issued code count
	AllowedPlanIDs             []int64     `json:"allowed_plan_ids"`               // permitted subscription plan identifiers
	MaxSubscriptionDaysPerCode int64       `json:"max_subscription_days_per_code"` // subscription duration in days
}

// SubscriptionLimits bounds direct entitlement grants and their eligible plans.
type SubscriptionLimits struct {
	AllowedPlanIDs         []int64 `json:"allowed_plan_ids"`            // permitted subscription plan identifiers
	MaxDaysPerGrant        int64   `json:"max_days_per_grant"`          // entitlement duration in days
	MaxGrantsPerUserPerDay int64   `json:"max_grants_per_user_per_day"` // daily grants per recipient
	MaxGrantsPerDay        int64   `json:"max_grants_per_day"`          // daily aggregate grant count
}

// EmailLimits bounds notification claims across recipients and the whole plugin.
type EmailLimits struct {
	MaxPerUserPerDay int64 `json:"max_per_user_per_day"` // daily claims per recipient
	MaxPerDay        int64 `json:"max_per_day"`          // daily aggregate claim count
}

// DefaultLimits returns conservative capability limits with independently owned plan slices.
func DefaultLimits() Limits {
	return Limits{
		Wallet: WalletLimits{MaxCreditPerOp: "1", MaxCreditPerUserPerDay: "5", MaxCreditPerDay: "200", MaxDebitPerOp: "100"},
		Redeem: RedeemLimits{
			MaxBalancePerCode:          "5",
			MaxBalanceValuePerDay:      "200",
			MaxConcurrencyPerCode:      5,
			MaxCodesPerDay:             200,
			AllowedPlanIDs:             []int64{},
			MaxSubscriptionDaysPerCode: 31,
		},
		Subscription: SubscriptionLimits{AllowedPlanIDs: []int64{}, MaxDaysPerGrant: 31, MaxGrantsPerUserPerDay: 1, MaxGrantsPerDay: 50},
		Email:        EmailLimits{MaxPerUserPerDay: 5, MaxPerDay: 500},
	}
}

// ParseLimits resolves missing fields and validates the complete capability policy.
// Empty input has the same meaning as an empty object. Explicit zero remains
// zero, and null is rejected so it cannot silently restore a default ceiling.
//
// Decoding starts with defaults and replaces only supplied fields. Monetary
// text is normalized before arithmetic, preserving exact precision even when a
// browser serializes a small numeric ceiling using exponent notation. Plan lists
// are sorted after rejecting duplicates and invalid identifiers, giving persisted
// policy a deterministic representation independent
// of the administrator's selection order. Errors never return a partial policy.
func ParseLimits(data []byte) (Limits, error) {
	if len(data) == 0 {
		data = []byte(`{}`)
	}
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return Limits{}, ErrLimitsInvalid.WithCause(err)
	}
	if _, ok := raw.(map[string]any); !ok || containsNull(raw) {
		return Limits{}, ErrLimitsInvalid
	}
	result := DefaultLimits()
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Limits{}, ErrLimitsInvalid.WithCause(err)
	}
	if err := decoder.Decode(&raw); err != io.EOF {
		return Limits{}, ErrLimitsInvalid
	}
	for _, amount := range []*json.Number{
		&result.Wallet.MaxCreditPerOp,
		&result.Wallet.MaxCreditPerUserPerDay,
		&result.Wallet.MaxCreditPerDay,
		&result.Wallet.MaxDebitPerOp,
		&result.Redeem.MaxBalancePerCode,
		&result.Redeem.MaxBalanceValuePerDay,
	} {
		normalized, err := normalizeLimitAmount(*amount)
		if err != nil {
			return Limits{}, err
		}
		*amount = normalized
	}
	for _, count := range []int64{
		result.Redeem.MaxConcurrencyPerCode,
		result.Redeem.MaxCodesPerDay,
		result.Redeem.MaxSubscriptionDaysPerCode,
		result.Subscription.MaxDaysPerGrant,
		result.Subscription.MaxGrantsPerUserPerDay,
		result.Subscription.MaxGrantsPerDay,
		result.Email.MaxPerUserPerDay,
		result.Email.MaxPerDay,
	} {
		// Counts remain representable by the protocol's signed 32-bit fields before any downstream conversion or multiplication.
		if count < 0 || count > 2147483647 {
			return Limits{}, ErrLimitsInvalid
		}
	}
	for _, ids := range [][]int64{result.Redeem.AllowedPlanIDs, result.Subscription.AllowedPlanIDs} {
		slices.Sort(ids)
		for i, id := range ids {
			if id <= 0 || (i > 0 && id == ids[i-1]) {
				return Limits{}, ErrLimitsInvalid.WithCause(fmt.Errorf("plan identifiers must be positive and unique"))
			}
		}
	}
	return result, nil
}

// containsNull reports explicit nulls anywhere in a decoded administrator policy.
func containsNull(value any) bool {
	switch item := value.(type) {
	case nil:
		return true
	case map[string]any:
		for _, child := range item {
			if containsNull(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(item, containsNull)
	}
	return false
}

// normalizeLimitAmount converts nonnegative JSON numeric notation to exact decimal policy text.
// Canonical results have at most seven integer digits and eight fractional digits.
// Operation amount strings retain their stricter ParseAmount contract independently.
//
// Decimal-point movement uses digit positions rather than floating-point arithmetic,
// so values such as 1e-8 retain their smallest supported unit. Width and precision
// checks happen before allocating expanded zeros, preventing extreme exponents from
// causing large allocations. Zero coefficients normalize directly because their
// exponent cannot change the effective prohibition represented by the policy.
func normalizeLimitAmount(value json.Number) (json.Number, error) {
	text := string(value)
	if !limitJSONNumberPattern.MatchString(text) {
		return "", ErrLimitsInvalid
	}
	mantissa, exponentText, hasExponent := text, "", false
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		mantissa, exponentText, hasExponent = text[:index], text[index+1:], true
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := whole + fraction
	leading := len(digits) - len(strings.TrimLeft(digits, "0"))
	digits = strings.TrimRight(digits[leading:], "0")
	if digits == "" {
		return "0", nil
	}
	var exponent int
	if hasExponent {
		parsed, err := strconv.Atoi(exponentText)
		// A nonzero coefficient cannot offset a point shift beyond its input width
		// and the eight fractional places allowed by the stored capability policy.
		if err != nil || parsed < -len(text)-8 || parsed > len(text)+8 {
			return "", ErrLimitsInvalid
		}
		exponent = parsed
	}
	point := len(whole) + exponent - leading
	if point > 7 || len(digits)-point > 8 {
		return "", ErrLimitsInvalid
	}
	if point <= 0 {
		return json.Number("0." + strings.Repeat("0", -point) + digits), nil
	}
	if point >= len(digits) {
		return json.Number(digits + strings.Repeat("0", point-len(digits))), nil
	}
	return json.Number(digits[:point] + "." + digits[point:]), nil
}
