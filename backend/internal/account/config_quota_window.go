// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// 配置边界只解析历史 JSON 值；日历计算和重置规则统一由 billing 提供。
func quotaWindowLocation(extra map[string]any, load func(string) (*time.Location, error)) *time.Location {
	name, _ := extra["quota_reset_timezone"].(string)
	if name == "" {
		name = "UTC"
	}
	location, err := load(name)
	if err != nil {
		return time.UTC
	}
	return location
}
func accountWindowConfig(extra map[string]any, prefix string, period billing.AccountWindowPeriod) billing.FixedAccountWindow {
	mode, _ := extra[prefix+"_reset_mode"].(string)
	day := 1
	if raw, ok := extra[prefix+"_reset_day"]; ok {
		day = int(ParseExtraFloat64(raw))
	}
	return billing.FixedAccountWindow{Period: period, Mode: mode, Limit: ParseExtraFloat64(extra[prefix+"_limit"]), Hour: int(ParseExtraFloat64(extra[prefix+"_reset_hour"])), Day: day}
}
func ComputeQuotaResetAt(extra map[string]any, now time.Time, load func(string) (*time.Location, error)) {
	location := quotaWindowLocation(extra, load)
	for _, dimension := range []struct {
		prefix string
		period billing.AccountWindowPeriod
	}{{"quota_daily", billing.AccountDailyWindow}, {"quota_weekly", billing.AccountWeeklyWindow}} {
		reset, ok := billing.NextAccountFixedReset(accountWindowConfig(extra, dimension.prefix, dimension.period), location, now)
		if ok {
			extra[dimension.prefix+"_reset_at"] = reset.UTC().Format(time.RFC3339)
		} else {
			delete(extra, dimension.prefix+"_reset_at")
		}
	}
}
func NormalizeFixedQuotaWindows(extra map[string]any, now time.Time, load func(string) (*time.Location, error)) {
	if extra == nil {
		return
	}
	location := quotaWindowLocation(extra, load)
	for _, dimension := range []struct {
		prefix string
		period billing.AccountWindowPeriod
	}{{"quota_daily", billing.AccountDailyWindow}, {"quota_weekly", billing.AccountWeeklyWindow}} {
		last, reset := billing.ReconcileAccountFixedWindow(accountWindowConfig(extra, dimension.prefix, dimension.period), ParseExtraTime(extra[dimension.prefix+"_start"]), location, now)
		if reset {
			extra[dimension.prefix+"_used"] = 0.0
			extra[dimension.prefix+"_start"] = last.UTC().Format(time.RFC3339)
		}
	}
}
