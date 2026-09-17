package composite

import "github.com/TokenFlux/TokenRouter/internal/ops"

// ApplyOpsAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyOpsAdminReadSettings(value *ops.AdminReadSettings) {
	s.OpenAIQuotaAutoPauseSettings = value.OpenAIQuotaAutoPauseSettings
	s.OpsMetricsIntervalSeconds = value.OpsMetricsIntervalSeconds
	s.OpsMonitoringEnabled = value.OpsMonitoringEnabled
	s.OpsRealtimeMonitoringEnabled = value.OpsRealtimeMonitoringEnabled
}
