package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usageBatchLogRepoStub 为批量用量测试提供最小仓库实现。
type usageBatchLogRepoStub struct{}

var _ usage.UsageLogRepository = (*usageBatchLogRepoStub)(nil)

func (r *usageBatchLogRepoStub) Create(context.Context, *usage.UsageLog) (bool, error) {
	return false, nil
}
func (r *usageBatchLogRepoStub) GetByID(context.Context, int64) (*usage.UsageLog, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) Delete(context.Context, int64) error { return nil }
func (r *usageBatchLogRepoStub) ListByUser(context.Context, int64, pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *usageBatchLogRepoStub) ListByAPIKey(context.Context, int64, pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *usageBatchLogRepoStub) ListByAccount(context.Context, int64, pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *usageBatchLogRepoStub) ListByUserAndTimeRange(context.Context, int64, time.Time, time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *usageBatchLogRepoStub) ListByAPIKeyAndTimeRange(context.Context, int64, time.Time, time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *usageBatchLogRepoStub) ListByAccountAndTimeRange(context.Context, int64, time.Time, time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *usageBatchLogRepoStub) ListByModelAndTimeRange(context.Context, string, time.Time, time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *usageBatchLogRepoStub) GetAccountWindowStats(context.Context, int64, time.Time) (*usage.AccountStats, error) {
	return &usage.AccountStats{}, nil
}
func (r *usageBatchLogRepoStub) GetAccountTodayStats(context.Context, int64) (*usage.AccountStats, error) {
	return &usage.AccountStats{}, nil
}
func (r *usageBatchLogRepoStub) GetDashboardStats(context.Context) (*usage.DashboardStats, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetUsageTrendWithFilters(context.Context, time.Time, time.Time, string, int64, int64, int64, int64, string, *int16, *bool, *int8) ([]usage.TrendDataPoint, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetModelStatsWithFilters(context.Context, time.Time, time.Time, int64, int64, int64, int64, *int16, *bool, *int8) ([]usage.ModelStat, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetEndpointStatsWithFilters(context.Context, time.Time, time.Time, int64, int64, int64, int64, string, *int16, *bool, *int8) ([]usage.EndpointStat, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetUpstreamEndpointStatsWithFilters(context.Context, time.Time, time.Time, int64, int64, int64, int64, string, *int16, *bool, *int8) ([]usage.EndpointStat, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetGroupStatsWithFilters(context.Context, time.Time, time.Time, int64, int64, int64, int64, *int16, *bool, *int8) ([]usage.GroupStat, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetUserBreakdownStats(context.Context, time.Time, time.Time, usage.UserBreakdownDimension, int) ([]usage.UserBreakdownItem, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetAllGroupUsageSummary(context.Context, time.Time) ([]usage.GroupUsageSummary, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetAPIKeyUsageTrend(context.Context, time.Time, time.Time, string, int) ([]usage.APIKeyUsageTrendPoint, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetUserUsageTrend(context.Context, time.Time, time.Time, string, int) ([]usage.UserUsageTrendPoint, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetUserSpendingRanking(context.Context, time.Time, time.Time, int) (*usage.UserSpendingRankingResponse, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetUsageRanking(context.Context, time.Time, time.Time, int, usage.UsageRankingSortBy) (*usage.UsageRankingResponse, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetBatchUserUsageStats(context.Context, []int64, time.Time, time.Time) (map[int64]*usage.BatchUserUsageStats, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetBatchAPIKeyUsageStats(context.Context, []int64, time.Time, time.Time) (map[int64]*usage.BatchAPIKeyUsageStats, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetUserDashboardStats(context.Context, int64) (*usage.UserDashboardStats, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetAPIKeyDashboardStats(context.Context, int64) (*usage.UserDashboardStats, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetUserUsageTrendByUserID(context.Context, int64, time.Time, time.Time, string) ([]usage.TrendDataPoint, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetUserModelStats(context.Context, int64, time.Time, time.Time) ([]usage.ModelStat, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, usage.UsageLogFilters) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *usageBatchLogRepoStub) GetGlobalStats(context.Context, time.Time, time.Time) (*usage.UsageStats, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetStatsWithFilters(context.Context, usage.UsageLogFilters) (*usage.UsageStats, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetAccountUsageStats(context.Context, int64, time.Time, time.Time) (*usage.AccountUsageStatsResponse, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetUserStatsAggregated(context.Context, int64, time.Time, time.Time) (*usage.UsageStats, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetAPIKeyStatsAggregated(context.Context, int64, time.Time, time.Time) (*usage.UsageStats, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetAccountStatsAggregated(context.Context, int64, time.Time, time.Time) (*usage.UsageStats, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetModelStatsAggregated(context.Context, string, time.Time, time.Time) (*usage.UsageStats, error) {
	return nil, nil
}
func (r *usageBatchLogRepoStub) GetDailyStatsAggregated(context.Context, int64, time.Time, time.Time) ([]map[string]any, error) {
	return nil, nil
}
