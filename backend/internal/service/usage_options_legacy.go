// 兼容入口的部署参数投影；生产装配逐批改由 app 注入。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

func LegacyUsageOptions(c *config.Config) *usage.Options {
	if c == nil {
		return nil
	}
	return &usage.Options{DashboardAgg: usage.DashboardAggregationConfig{Enabled: c.DashboardAgg.Enabled, IntervalSeconds: c.DashboardAgg.IntervalSeconds, LookbackSeconds: c.DashboardAgg.LookbackSeconds, BackfillEnabled: c.DashboardAgg.BackfillEnabled, BackfillMaxDays: c.DashboardAgg.BackfillMaxDays, RecomputeDays: c.DashboardAgg.RecomputeDays, Retention: usage.DashboardAggregationRetentionConfig{UsageLogsDays: c.DashboardAgg.Retention.UsageLogsDays, UsageBillingDedupDays: c.DashboardAgg.Retention.UsageBillingDedupDays, HourlyDays: c.DashboardAgg.Retention.HourlyDays, DailyDays: c.DashboardAgg.Retention.DailyDays}}, UsageCleanup: usage.UsageCleanupConfig{Enabled: c.UsageCleanup.Enabled, MaxRangeDays: c.UsageCleanup.MaxRangeDays, BatchSize: c.UsageCleanup.BatchSize, WorkerIntervalSeconds: c.UsageCleanup.WorkerIntervalSeconds, TaskTimeoutSeconds: c.UsageCleanup.TaskTimeoutSeconds}, Dashboard: usage.DashboardConfig{Enabled: c.Dashboard.Enabled, StatsFreshTTLSeconds: c.Dashboard.StatsFreshTTLSeconds, StatsTTLSeconds: c.Dashboard.StatsTTLSeconds, StatsRefreshTimeoutSeconds: c.Dashboard.StatsRefreshTimeoutSeconds}}
}
