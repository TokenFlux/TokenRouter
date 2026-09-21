// 账号通知投影仅提供已取得的配置与用量，不决定是否发送。
package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

func QuotaNotification(a *account.Record) *billing.QuotaNotifyAccount {
	if a == nil {
		return nil
	}
	return &billing.QuotaNotifyAccount{ID: a.ID, Name: a.Name, Platform: a.Platform, Dimensions: []billing.QuotaNotifyDimension{
		{Name: "daily", Enabled: a.GetQuotaNotifyDailyEnabled(), Threshold: a.GetQuotaNotifyDailyThreshold(), ThresholdType: a.GetQuotaNotifyDailyThresholdType(), CurrentUsed: a.GetQuotaDailyUsed(), Limit: a.GetQuotaDailyLimit()},
		{Name: "weekly", Enabled: a.GetQuotaNotifyWeeklyEnabled(), Threshold: a.GetQuotaNotifyWeeklyThreshold(), ThresholdType: a.GetQuotaNotifyWeeklyThresholdType(), CurrentUsed: a.GetQuotaWeeklyUsed(), Limit: a.GetQuotaWeeklyLimit()},
		{Name: "total", Enabled: a.GetQuotaNotifyTotalEnabled(), Threshold: a.GetQuotaNotifyTotalThreshold(), ThresholdType: a.GetQuotaNotifyTotalThresholdType(), CurrentUsed: a.GetQuotaUsed(), Limit: a.GetQuotaLimit()},
	}}
}
