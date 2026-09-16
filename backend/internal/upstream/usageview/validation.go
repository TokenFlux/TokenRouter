// 只验证上游显示值的形状、有限数和单位，不计算 TokenRouter 资金。
package usageview

import (
	"errors"
	"math"
	"strings"
)

func ValidateNormalizedUsage(usage *UpstreamUsageInfo) error {
	if usage == nil || strings.TrimSpace(usage.Provider) == "" || strings.TrimSpace(usage.Mode) == "" {
		return errors.New("missing normalized usage fields")
	}
	switch usage.Mode {
	case "balance", "quota", "limits", "subscription":
	default:
		return errors.New("unknown normalized usage mode")
	}
	switch usage.Mode {
	case "balance", "quota":
		if usage.Balance == nil {
			return errors.New("missing normalized balance")
		}
	case "limits":
		if len(usage.Limits) == 0 && (usage.Subscription == nil || len(usage.Subscription.Limits) == 0) {
			return errors.New("missing normalized limits")
		}
	case "subscription":
		if usage.Subscription == nil {
			return errors.New("missing normalized subscription")
		}
	}
	if usage.Unit != "" && usage.Unit != "USD" && usage.Unit != "CNY" && usage.Unit != "TOKENS" && usage.Unit != "PERCENT" {
		return errors.New("unknown usage unit")
	}
	for _, balance := range usage.Balances {
		if strings.TrimSpace(balance.Currency) == "" || !ValidFiniteNumber(balance.Remaining) {
			return errors.New("invalid usage balance entry")
		}
	}
	if usage.Balance != nil {
		if err := ValidateUsageAmount(usage.Balance); err != nil {
			return err
		}
	}
	if err := ValidateUsageLimits(usage.Limits); err != nil {
		return err
	}
	if usage.Subscription != nil {
		if strings.TrimSpace(usage.Subscription.PlanName) == "" {
			return errors.New("missing subscription plan")
		}
		if usage.Subscription.Unlimited && (usage.Subscription.Remaining != nil || len(usage.Subscription.Limits) > 0) {
			return errors.New("unlimited subscription must not contain remaining or limits")
		}
		if !usage.Subscription.Unlimited && usage.Subscription.Remaining == nil && len(usage.Subscription.Limits) == 0 {
			return errors.New("limited subscription is missing remaining or limits")
		}
		if usage.Subscription.Remaining != nil && !ValidFiniteNumber(*usage.Subscription.Remaining) {
			return errors.New("invalid subscription remaining")
		}
		if usage.Subscription.ExpiresAt != nil && usage.Subscription.ExpiresAt.IsZero() {
			return errors.New("invalid subscription expiry")
		}
		if err := ValidateUsageLimits(usage.Subscription.Limits); err != nil {
			return err
		}
	}
	if usage.ExpiresAt != nil && usage.ExpiresAt.IsZero() {
		return errors.New("invalid expiry")
	}
	return nil
}
func ValidateUsageAmount(amount *UpstreamUsageAmount) error {
	if amount == nil || (amount.Used == nil && amount.Total == nil && amount.Remaining == nil) {
		return errors.New("missing usage amount values")
	}
	for _, value := range []*float64{amount.Used, amount.Total} {
		if value != nil && !ValidNonNegativeNumber(*value) {
			return errors.New("invalid usage amount")
		}
	}
	if amount.Remaining != nil && !ValidFiniteNumber(*amount.Remaining) {
		return errors.New("invalid usage remaining")
	}
	// 钱包余额允许为负；New API Token 额度在适配器层已经校验为非负。
	return nil
}
func ValidateUsageLimits(limits []UpstreamUsageLimit) error {
	seen := make(map[string]struct{}, len(limits))
	for _, limit := range limits {
		name := strings.TrimSpace(limit.Name)
		if name == "" {
			return errors.New("missing usage limit name")
		}
		if _, exists := seen[name]; exists {
			return errors.New("duplicate usage limit")
		}
		seen[name] = struct{}{}
		for _, value := range []*float64{limit.Used, limit.Limit} {
			if value != nil && !ValidNonNegativeNumber(*value) {
				return errors.New("invalid usage limit amount")
			}
		}
		if limit.Used == nil && limit.Limit == nil && limit.Remaining == nil {
			return errors.New("missing usage limit values")
		}
		if limit.Remaining != nil && !ValidFiniteNumber(*limit.Remaining) {
			return errors.New("invalid usage limit remaining")
		}
		if limit.ResetAt != nil && limit.ResetAt.IsZero() {
			return errors.New("invalid usage limit reset")
		}
	}
	return nil
}
func ValidNonNegativeNumber(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
func ValidPositiveNumber(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
func ValidFiniteNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
