// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	time "time"
)

// AccountRuntimeStatusOptions 只投影原读取能力；查询顺序由账号核心决定。
func AccountRuntimeStatusOptions(concurrency *ConcurrencyService, usage *AccountUsageService, sessions SessionLimitCache, rpm RPMCache, settings *SettingService) accountcore.RuntimeStatusOptions {
	out := accountcore.RuntimeStatusOptions{Now: time.Now}
	if concurrency != nil {
		out.Concurrency = concurrency.GetAccountConcurrencyBatch
	}
	if sessions != nil {
		out.Sessions = sessions.GetActiveSessionCountBatch
	}
	if rpm != nil {
		out.RPM = rpm.GetRPM
		out.RPMBatch = rpm.GetRPMBatch
	}
	if settings != nil {
		out.QuotaSettings = settings.GetOpenAIQuotaAutoPauseSettings
	}
	if usage != nil {
		out.WindowCost = func(ctx context.Context, id int64, start time.Time) (*float64, error) {
			v, err := usage.GetAccountWindowStats(ctx, id, start)
			if v == nil {
				return nil, err
			}
			cost := v.StandardCost
			return &cost, err
		}
	}
	return out
}
