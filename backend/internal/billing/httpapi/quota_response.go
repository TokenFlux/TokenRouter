package httpapi

import (
	time "time"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// QuotaResponse 保留用户/管理员的平台额度 JSON 字段与 RFC3339 时间格式。
func QuotaResponse(view billing.PlatformQuotaView, includeWindowStart bool) map[string]any {
	out := map[string]any{"platform": view.Platform}
	for _, item := range []struct {
		name   string
		window billing.QuotaWindowView
	}{{"daily", view.Daily}, {"weekly", view.Weekly}, {"monthly", view.Monthly}} {
		out[item.name+"_usage_usd"] = item.window.Usage
		out[item.name+"_limit_usd"] = item.window.Limit
		var reset *string
		if item.window.ResetsAt != nil {
			value := item.window.ResetsAt.Format(time.RFC3339)
			reset = &value
		}
		out[item.name+"_window_resets_at"] = reset
		if includeWindowStart {
			var start *string
			if item.window.WindowStart != nil {
				value := item.window.WindowStart.Format(time.RFC3339)
				start = &value
			}
			out[item.name+"_window_start"] = start
		}
	}
	return out
}

// LazyZeroQuotaForResponse 保留现有展示调用形状；规则委托 billing 日期投影。
func LazyZeroQuotaForResponse(r billing.UserPlatformQuotaRecord, now time.Time, includeWindowStart bool, calendar timezone.Calendar) map[string]any {
	return QuotaResponse(billing.ProjectPlatformQuota(r, now, calendar), includeWindowStart)
}
