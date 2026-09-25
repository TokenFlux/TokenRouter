package billing

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func ComputeValidityDays(days int, unit string) int {
	switch unit {
	case "week", "weeks":
		return days * 7
	case "month", "months":
		return days * 30
	default:
		return days
	}
}

func ValidatePlanQuotas(daily, weekly, monthly *float64) error {
	for _, item := range []struct {
		value *float64
		code  string
		label string
	}{
		{value: daily, code: "PLAN_DAILY_LIMIT_INVALID", label: "daily limit"},
		{value: weekly, code: "PLAN_WEEKLY_LIMIT_INVALID", label: "weekly limit"},
		{value: monthly, code: "PLAN_MONTHLY_LIMIT_INVALID", label: "monthly limit"},
	} {
		if item.value != nil && *item.value < 0 {
			return apperror.BadRequest(item.code, item.label+" must be >= 0")
		}
	}
	return nil
}
