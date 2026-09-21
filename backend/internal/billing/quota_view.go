// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

import (
	time "time"

	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// QuotaWindowView 是只读窗口展示，过期归零不写数据库。
type QuotaWindowView struct {
	Usage       float64
	Limit       *float64
	ResetsAt    *time.Time
	WindowStart *time.Time
}
type PlatformQuotaView struct {
	Platform string
	Daily    QuotaWindowView
	Weekly   QuotaWindowView
	Monthly  QuotaWindowView
}

func ProjectPlatformQuota(r UserPlatformQuotaRecord, now time.Time, calendar timezone.Calendar) PlatformQuotaView {
	return PlatformQuotaView{Platform: r.Platform,
		Daily:   quotaWindowView(r.DailyUsageUSD, r.DailyLimitUSD, r.DailyWindowStart, r.DailyWindowStart != nil && r.DailyWindowStart.Before(calendar.StartOfDay(now)), calendar.StartOfDay(now).AddDate(0, 0, 1)),
		Weekly:  quotaWindowView(r.WeeklyUsageUSD, r.WeeklyLimitUSD, r.WeeklyWindowStart, r.WeeklyWindowStart != nil && r.WeeklyWindowStart.Before(calendar.StartOfWeek(now)), calendar.StartOfWeek(now).AddDate(0, 0, 7)),
		Monthly: quotaWindowView(r.MonthlyUsageUSD, r.MonthlyLimitUSD, r.MonthlyWindowStart, NeedsMonthlyReset(r.MonthlyWindowStart, now), NextMonthlyResetTimeFrom(r.MonthlyWindowStart, now))}
}
func quotaWindowView(usage float64, limit *float64, start *time.Time, expired bool, next time.Time) QuotaWindowView {
	out := QuotaWindowView{Usage: usage, Limit: limit, WindowStart: start}
	if expired {
		out.Usage = 0
	} else if start != nil {
		out.ResetsAt = &next
	}
	return out
}

// NeedsDailyReset 按注入日历判断窗口，保持与存储写入的窗口起点一致。
func NeedsDailyReset(start *time.Time, now time.Time, calendar timezone.Calendar) bool {
	if start == nil {
		return false
	}
	return start.Before(calendar.StartOfDay(now))
}
func NeedsWeeklyReset(start *time.Time, now time.Time, calendar timezone.Calendar) bool {
	if start == nil {
		return false
	}
	return start.Before(calendar.StartOfWeek(now))
}

// NeedsMonthlyReset 30 天滚动窗口语义（与订阅模式 NeedsMonthlyReset 一致）。
func NeedsMonthlyReset(start *time.Time, now time.Time) bool {
	if start == nil {
		return false
	}
	return now.Sub(*start) >= 30*24*time.Hour
}
func NextQuotaDisplayDailyReset(now time.Time, calendar timezone.Calendar) time.Time {
	return calendar.StartOfDay(now).AddDate(0, 0, 1)
}
func NextQuotaDisplayWeeklyReset(now time.Time, calendar timezone.Calendar) time.Time {
	return calendar.StartOfWeek(now).AddDate(0, 0, 7)
}

// NextMonthlyResetTimeFrom 计算 30 天滚动月度窗口的下次重置时间。
// 语义：
//   - start != nil → 返回 start + 30d（与 billing_cache_service.nextMonthlyResetFrom 一致）
//   - start == nil → 退化为 now + 30d（保留旧行为，避免 nil 崩溃）
//
// 导出（首字母大写）以允许测试直接调用。
func NextMonthlyResetTimeFrom(start *time.Time, now time.Time) time.Time {
	if start == nil {
		return now.Add(30 * 24 * time.Hour)
	}
	return start.Add(30 * 24 * time.Hour)
}
