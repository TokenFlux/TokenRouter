package admin

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/usage"
)

func (h *DashboardHandler) getUsageTrendCached(
	ctx context.Context,
	startTime, endTime time.Time,
	granularity string,
	userID, apiKeyID, accountID, groupID, teamID int64,
	model string,
	requestType *int16,
	stream *bool,
	billingType *int8,
	nativeCompactionV2 *bool,
) ([]usage.TrendDataPoint, bool, error) {
	return h.dashboardService.GetUsageTrendCached(ctx, startTime, endTime, granularity, userID, apiKeyID, accountID, groupID, teamID, model, requestType, stream, billingType, nativeCompactionV2)
}
func (h *DashboardHandler) getModelStatsCached(
	ctx context.Context,
	startTime, endTime time.Time,
	userID, apiKeyID, accountID, groupID, teamID int64,
	modelSource string,
	requestType *int16,
	stream *bool,
	billingType *int8,
	nativeCompactionV2 *bool,
) ([]usage.ModelStat, bool, error) {
	return h.dashboardService.GetModelStatsCached(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, teamID, modelSource, requestType, stream, billingType, nativeCompactionV2)
}
func (h *DashboardHandler) getGroupStatsCached(
	ctx context.Context,
	startTime, endTime time.Time,
	userID, apiKeyID, accountID, groupID, teamID int64,
	requestType *int16,
	stream *bool,
	billingType *int8,
	nativeCompactionV2 *bool,
) ([]usage.GroupStat, bool, error) {
	return h.dashboardService.GetGroupStatsCached(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, teamID, requestType, stream, billingType, nativeCompactionV2)
}
func (h *DashboardHandler) getAPIKeyUsageTrendCached(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usage.APIKeyUsageTrendPoint, bool, error) {
	return h.dashboardService.GetAPIKeyUsageTrendCached(ctx, startTime, endTime, granularity, limit)
}
func (h *DashboardHandler) getUserUsageTrendCached(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usage.UserUsageTrendPoint, bool, error) {
	return h.dashboardService.GetUserUsageTrendCached(ctx, startTime, endTime, granularity, limit)
}
func cacheStatusValue(hit bool) string {
	if hit {
		return "hit"
	}
	return "miss"
}
