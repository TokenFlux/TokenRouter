package admin

import "github.com/TokenFlux/TokenRouter/internal/service"

// 仅用于旧 HTTP 响应断言。
type preAggregationAvailabilityResponse struct {
	UsageAvailable          bool   `json:"usage_available"`
	UsageDisabledReason     string `json:"usage_disabled_reason,omitempty"`
	OpsAvailable            bool   `json:"ops_available"`
	OpsDisabledReason       string `json:"ops_disabled_reason,omitempty"`
	ManualBackfillAvailable bool   `json:"manual_backfill_available"`
	ManualBackfillMaxDays   int    `json:"manual_backfill_max_days"`
}

type preAggregationSettingsResponse struct {
	Settings     service.PreAggregationSettings      `json:"settings"`
	Availability preAggregationAvailabilityResponse  `json:"availability"`
	UsageStatus  service.PreAggregationRuntimeStatus `json:"usage_status"`
	OpsStatus    service.PreAggregationRuntimeStatus `json:"ops_status"`
}
