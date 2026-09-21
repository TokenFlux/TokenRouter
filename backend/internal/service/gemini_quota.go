// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	time "time"
)

// GeminiQuota 复用账号核心的纯值。

// GeminiUsageTotals 复用账号核心的纯值。

func geminiQuotaLocation() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.FixedZone("PST", -8*3600)
	}
	return loc
}

func geminiDailyResetTime(now time.Time) time.Time {
	return accountcore.GeminiDailyResetTime(now, geminiQuotaLocation())
}
