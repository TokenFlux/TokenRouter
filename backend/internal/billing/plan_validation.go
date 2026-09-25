package billing

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// NormalizePlanCurrency 只校验展示币种拼写，空值保留为空，不应用支付默认币种。
func NormalizePlanCurrency(raw string) (string, error) {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	if currency == "" {
		return "", nil
	}
	valid := len(currency) == 3
	for _, r := range currency {
		if r < 'A' || r > 'Z' {
			valid = false
		}
	}
	if !valid {
		return "", apperror.BadRequest("PLAN_CURRENCY_INVALID", "currency must be a 3-letter ISO currency code")
	}
	return currency, nil
}

func NormalizePlanGroupIDs(groupID int64, groupIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(groupIDs)+1)
	out := make([]int64, 0, len(groupIDs)+1)
	if groupID > 0 {
		seen[groupID] = struct{}{}
		out = append(out, groupID)
	}
	for _, id := range groupIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func NormalizePlanGroupRateMultipliers(groupIDs []int64, rates map[int64]float64) (map[int64]float64, error) {
	selected := make(map[int64]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			continue
		}
		selected[groupID] = struct{}{}
	}

	out := make(map[int64]float64, len(selected))
	for groupID, rate := range rates {
		if groupID <= 0 {
			continue
		}
		if _, ok := selected[groupID]; !ok {
			continue
		}
		if rate <= 0 {
			return nil, apperror.BadRequest("PLAN_GROUP_RATE_INVALID", "plan group rate multiplier must be > 0")
		}
		out[groupID] = rate
	}
	return out, nil
}

func ClonePlanGroupRates(in map[int64]float64) map[int64]float64 {
	if len(in) == 0 {
		return map[int64]float64{}
	}
	out := make(map[int64]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func ValidatePlanRequired(name string, price float64, validityDays int, validityUnit string, originalPrice *float64) error {
	if strings.TrimSpace(name) == "" {
		return apperror.BadRequest("PLAN_NAME_REQUIRED", "plan name is required")
	}
	if price <= 0 {
		return apperror.BadRequest("PLAN_PRICE_INVALID", "price must be > 0")
	}
	if validityDays <= 0 {
		return apperror.BadRequest("PLAN_VALIDITY_REQUIRED", "validity days must be > 0")
	}
	if strings.TrimSpace(validityUnit) == "" {
		return apperror.BadRequest("PLAN_VALIDITY_UNIT_REQUIRED", "validity unit is required")
	}
	if originalPrice != nil && *originalPrice < 0 {
		return apperror.BadRequest("PLAN_ORIGINAL_PRICE_INVALID", "original price must be >= 0")
	}
	return nil
}

func ValidatePlanPatch(req UpdatePlanRequest) error {
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return apperror.BadRequest("PLAN_NAME_REQUIRED", "plan name is required")
	}
	if req.Price != nil && *req.Price <= 0 {
		return apperror.BadRequest("PLAN_PRICE_INVALID", "price must be > 0")
	}
	if req.ValidityDays != nil && *req.ValidityDays <= 0 {
		return apperror.BadRequest("PLAN_VALIDITY_REQUIRED", "validity days must be > 0")
	}
	if req.ValidityUnit != nil && strings.TrimSpace(*req.ValidityUnit) == "" {
		return apperror.BadRequest("PLAN_VALIDITY_UNIT_REQUIRED", "validity unit is required")
	}
	if req.OriginalPrice.Present && req.OriginalPrice.Value != nil && *req.OriginalPrice.Value < 0 {
		return apperror.BadRequest("PLAN_ORIGINAL_PRICE_INVALID", "original price must be >= 0")
	}
	return ValidatePlanQuotaPatch(req)
}

func ValidatePlanQuotaPatch(req UpdatePlanRequest) error {
	for _, item := range []struct {
		field NullableFloat64Patch
		code  string
		label string
	}{
		{field: req.DailyLimitUSD, code: "PLAN_DAILY_LIMIT_INVALID", label: "daily limit"},
		{field: req.WeeklyLimitUSD, code: "PLAN_WEEKLY_LIMIT_INVALID", label: "weekly limit"},
		{field: req.MonthlyLimitUSD, code: "PLAN_MONTHLY_LIMIT_INVALID", label: "monthly limit"},
	} {
		if item.field.Present && item.field.Value != nil && *item.field.Value < 0 {
			return apperror.BadRequest(item.code, item.label+" must be >= 0")
		}
	}
	return nil
}
