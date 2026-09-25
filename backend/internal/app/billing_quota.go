package app

import (
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// observePlatformQuota 保留额度管理日志字段、顺序与故障等级。
func observePlatformQuota(event billing.QuotaEvent) {
	switch event.Kind {
	case "before_read_failed":
		slog.Warn("quota audit before snapshot failed", "user_id", event.UserID, "err", event.Err)
	case "updated":
		beforeRecords, records, beforeErr := event.Before, event.Records, event.BeforeErr
		beforeByPlatform := make(map[string]billing.UserPlatformQuotaRecord, len(beforeRecords))
		for _, r := range beforeRecords {
			beforeByPlatform[r.Platform] = r
		}
		afterPlatforms := make(map[string]struct{}, len(records))
		for _, r := range records {
			afterPlatforms[r.Platform] = struct{}{}
		}
		changes := make([]map[string]any, 0, len(records))
		for _, r := range records {
			entry := map[string]any{
				"platform":          r.Platform,
				"daily_limit_usd":   r.DailyLimitUSD,
				"weekly_limit_usd":  r.WeeklyLimitUSD,
				"monthly_limit_usd": r.MonthlyLimitUSD,
			}
			if prev, ok := beforeByPlatform[r.Platform]; ok {
				entry["before_daily_limit_usd"] = prev.DailyLimitUSD
				entry["before_weekly_limit_usd"] = prev.WeeklyLimitUSD
				entry["before_monthly_limit_usd"] = prev.MonthlyLimitUSD
			}
			changes = append(changes, entry)
		}
		// 补充移除条目：更新前存在但更新后缺失，表示该平台被软删除。
		// 缺少这条记录，审计消费方无法察觉"管理员把某平台从配额列表移除"的操作（合规盲区）。
		for _, prev := range beforeRecords {
			if _, kept := afterPlatforms[prev.Platform]; kept {
				continue
			}
			changes = append(changes, map[string]any{
				"platform":                 prev.Platform,
				"removed":                  true,
				"before_daily_limit_usd":   prev.DailyLimitUSD,
				"before_weekly_limit_usd":  prev.WeeklyLimitUSD,
				"before_monthly_limit_usd": prev.MonthlyLimitUSD,
			})
		}
		// before_snapshot_available 让审计消费方能识别 changes 中是否带 before_* 字段；
		// false 时所有条目都只包含更新后视图，缺失 before_*_limit_usd。
		slog.Info("admin.quota_updated",
			"actor_admin_id", event.ActorID,
			"target_user_id", event.UserID,
			"platform_count", len(records),
			"before_snapshot_available", beforeErr == nil,
			"changes", changes)

	case "reset":
		slog.Info("admin.quota_window_reset", "actor_admin_id", event.ActorID, "target_user_id", event.UserID, "platform", event.Platform, "window", event.Window)
	case "invalidation_failed":
		if event.Operation == "ResetExpiredWindow" {
			slog.Error("ALERT: quota cache invalidation failed after ResetExpiredWindow; 窗口重置可能延迟至 sentinel TTL(最长 1h)", "user_id", event.UserID, "platform", event.Platform, "err", event.Err)
		} else {
			slog.Error("ALERT: quota cache invalidation failed after UpsertForUser; limit 生效可能延迟至 sentinel TTL(最长 1h),需人工确认或重试失效", "user_id", event.UserID, "platform", event.Platform, "err", event.Err)
		}
	}
}
func providePlatformQuotas(repo billing.UserPlatformQuotaRepository, cache billing.BillingCache, users *identitypostgres.UserStore, coordinator *billing.QuotaCoordinator) *billing.PlatformQuotas {
	return billing.NewPlatformQuotas(repo, cache, billingIdentityUsers{Repository: users}, coordinator, time.Now, observePlatformQuota)
}
func provideQuotaHTTP(quotas *billing.PlatformQuotas, calendar timezone.Calendar) *billinghttpapi.QuotaHandler {
	return billinghttpapi.NewQuotaHandler(quotas, calendar, time.Now)
}
