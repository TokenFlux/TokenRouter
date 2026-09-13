// PreAggregationRuntimeStatus 是查询控制器的运行投影。
package preaggregation

import "time"

// PreAggregationRuntimeStatus 是设置接口返回的任务运行状态。
type PreAggregationRuntimeStatus struct {
	Phase          string     `json:"phase"`
	LiveWatermark  *time.Time `json:"live_watermark,omitempty"`
	CoverageStart  *time.Time `json:"coverage_start,omitempty"`
	SourceOldestAt *time.Time `json:"source_oldest_at,omitempty"`
	LagSeconds     int64      `json:"lag_seconds"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	LastSuccessAt  *time.Time `json:"last_success_at,omitempty"`
	LastErrorAt    *time.Time `json:"last_error_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	LastDurationMS int64      `json:"last_duration_ms"`
}
