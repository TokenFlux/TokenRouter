// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"strings"
	"time"
)

// GeminiQuota 保留共享池与按模型限额；-1 仍表示原按量付费无限额。
type GeminiQuota struct {
	SharedRPD int64 `json:"shared_rpd,omitempty"`
	SharedRPM int64 `json:"shared_rpm,omitempty"`
	ProRPD    int64 `json:"pro_rpd,omitempty"`
	ProRPM    int64 `json:"pro_rpm,omitempty"`
	FlashRPD  int64 `json:"flash_rpd,omitempty"`
	FlashRPM  int64 `json:"flash_rpm,omitempty"`
}
type GeminiUsageTotals struct {
	ProRequests, FlashRequests, ProTokens, FlashTokens int64
	ProCost, FlashCost                                 float64
}
type GeminiModelUsage struct {
	Model                 string
	Requests, TotalTokens int64
	AccountCost           float64
}
type GeminiModelClass string

const (
	GeminiModelPro   GeminiModelClass = "pro"
	GeminiModelFlash GeminiModelClass = "flash"
)

func GeminiModelClassFromName(model string) GeminiModelClass {
	name := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(name, "flash") || strings.Contains(name, "lite") {
		return GeminiModelFlash
	}
	return GeminiModelPro
}
func AggregateGeminiUsage(stats []GeminiModelUsage) GeminiUsageTotals {
	var totals GeminiUsageTotals
	for _, stat := range stats {
		switch GeminiModelClassFromName(stat.Model) {
		case GeminiModelFlash:
			totals.FlashRequests += stat.Requests
			totals.FlashTokens += stat.TotalTokens
			totals.FlashCost += stat.AccountCost
		default:
			totals.ProRequests += stat.Requests
			totals.ProTokens += stat.TotalTokens
			totals.ProCost += stat.AccountCost
		}
	}
	return totals
}
func GeminiDailyWindowStart(now time.Time, loc *time.Location) time.Time {
	localNow := now.In(loc)
	return time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
}
func GeminiDailyResetTime(now time.Time, loc *time.Location) time.Time {
	localNow := now.In(loc)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	reset := start.Add(24 * time.Hour)
	if !reset.After(localNow) {
		reset = reset.Add(24 * time.Hour)
	}
	return reset
}
