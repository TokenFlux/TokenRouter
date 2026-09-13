// 兼容入口只引用 usage 的唯一统计定义，S15/S16 清理。
package usagestats

import "github.com/TokenFlux/TokenRouter/internal/usage"

type DashboardStats = usage.DashboardStats
type TrendDataPoint = usage.TrendDataPoint
type ModelStat = usage.ModelStat
type EndpointStat = usage.EndpointStat
type GroupUsageSummary = usage.GroupUsageSummary
type GroupStat = usage.GroupStat
type UserUsageTrendPoint = usage.UserUsageTrendPoint
type UserSpendingRankingItem = usage.UserSpendingRankingItem
type UserSpendingRankingResponse = usage.UserSpendingRankingResponse
type UsageRankingItem = usage.UsageRankingItem
type UsageRankingResponse = usage.UsageRankingResponse
type UserBreakdownItem = usage.UserBreakdownItem
type UserBreakdownDimension = usage.UserBreakdownDimension
type APIKeyUsageTrendPoint = usage.APIKeyUsageTrendPoint
type APIKeyDailyUsagePoint = usage.APIKeyDailyUsagePoint
type UserDashboardStats = usage.UserDashboardStats
type PlatformDashboardStats = usage.PlatformDashboardStats
type UsageLogFilters = usage.UsageLogFilters
type UsageStats = usage.UsageStats
type PlatformUsage = usage.PlatformUsage
type BatchUserUsageStats = usage.BatchUserUsageStats
type BatchAPIKeyUsageStats = usage.BatchAPIKeyUsageStats
type AccountUsageHistory = usage.AccountUsageHistory
type AccountUsageSummary = usage.AccountUsageSummary
type AccountUsageStatsResponse = usage.AccountUsageStatsResponse

const ModelSourceRequested = usage.ModelSourceRequested
const ModelSourceUpstream = usage.ModelSourceUpstream
const ModelSourceMapping = usage.ModelSourceMapping

var IsValidModelSource = usage.IsValidModelSource
var NormalizeModelSource = usage.NormalizeModelSource
