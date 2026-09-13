//go:build unit

// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	time "time"
)

const defaultPoolModeRetryCount = accountcore.DefaultPoolModeRetryCount

const maxPoolModeRetryCount = accountcore.MaxPoolModeRetryCount

func (a *Account) isFixedDailyPeriodExpired(periodStart time.Time) bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Now: time.Now}
	}
	return view.IsFixedDailyPeriodExpired(periodStart)
}

func (a *Account) isFixedWeeklyPeriodExpired(periodStart time.Time) bool {
	var view *accountcore.Record
	if a != nil {
		view = &accountcore.Record{LoadLocation: time.LoadLocation, Extra: a.Extra,
			Now: time.Now}
	}
	return view.IsFixedWeeklyPeriodExpired(periodStart)
}
