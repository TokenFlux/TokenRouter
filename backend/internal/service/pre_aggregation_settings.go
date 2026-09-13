// 旧设置入口只投影部署参数，状态与算法由 preaggregation 唯一持有。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	p "github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
)

type PreAggregationUsageSettings = p.PreAggregationUsageSettings
type PreAggregationOpsSettings = p.PreAggregationOpsSettings
type PreAggregationSettings = p.PreAggregationSettings
type PreAggregationAvailability = p.PreAggregationAvailability
type PreAggregationSettingsService = p.PreAggregationSettingsService

const PreAggregationMinIntervalSeconds = p.PreAggregationMinIntervalSeconds
const PreAggregationMaxIntervalSeconds = p.PreAggregationMaxIntervalSeconds

func NewPreAggregationSettingsService(repo SettingRepository, cfg *config.Config) *PreAggregationSettingsService {
	return p.NewPreAggregationSettingsService(repo, LegacyPreAggregationOptions(cfg))
}

// LegacyPreAggregationOptions 仅供保留的独立旧构造入口使用；生产由 app 投影。
func LegacyPreAggregationOptions(cfg *config.Config) *p.Options {
	if cfg == nil {
		return nil
	}
	return &p.Options{Usage: p.UsageOptions{Enabled: cfg.DashboardAgg.Enabled, IntervalSeconds: cfg.DashboardAgg.IntervalSeconds, BackfillEnabled: cfg.DashboardAgg.BackfillEnabled, BackfillMaxDays: cfg.DashboardAgg.BackfillMaxDays}, OpsEnabled: cfg.Ops.Enabled, OpsAggregationEnabled: cfg.Ops.Aggregation.Enabled}
}
