// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/ops"

type OpsDashboardFilter = native.OpsDashboardFilter
type OpsRateSummary = native.OpsRateSummary
type OpsPercentiles = native.OpsPercentiles
type OpsDashboardOverview = native.OpsDashboardOverview
type OpsLatencyHistogramBucket = native.OpsLatencyHistogramBucket
type OpsLatencyHistogramResponse = native.OpsLatencyHistogramResponse
