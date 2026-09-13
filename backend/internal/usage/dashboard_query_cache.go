package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
)

type dashboardTrendCacheKey struct {
	StartTime          string `json:"start_time"`
	EndTime            string `json:"end_time"`
	Granularity        string `json:"granularity"`
	UserID             int64  `json:"user_id"`
	APIKeyID           int64  `json:"api_key_id"`
	AccountID          int64  `json:"account_id"`
	GroupID            int64  `json:"group_id"`
	TeamID             int64  `json:"team_id"`
	Model              string `json:"model"`
	RequestType        *int16 `json:"request_type"`
	Stream             *bool  `json:"stream"`
	BillingType        *int8  `json:"billing_type"`
	NativeCompactionV2 *bool  `json:"native_compaction_v2"`
}

type dashboardModelGroupCacheKey struct {
	StartTime          string `json:"start_time"`
	EndTime            string `json:"end_time"`
	UserID             int64  `json:"user_id"`
	APIKeyID           int64  `json:"api_key_id"`
	AccountID          int64  `json:"account_id"`
	GroupID            int64  `json:"group_id"`
	TeamID             int64  `json:"team_id"`
	ModelSource        string `json:"model_source,omitempty"`
	RequestType        *int16 `json:"request_type"`
	Stream             *bool  `json:"stream"`
	BillingType        *int8  `json:"billing_type"`
	NativeCompactionV2 *bool  `json:"native_compaction_v2"`
}

type dashboardEntityTrendCacheKey struct {
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Granularity string `json:"granularity"`
	Limit       int    `json:"limit"`
}

func mustMarshalDashboardCacheKey(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func snapshotPayloadAs[T any](payload any) (T, error) {
	typed, ok := payload.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("unexpected cache payload type %T", payload)
	}
	return typed, nil
}

func (h *DashboardService) GetUsageTrendCached(
	ctx context.Context,
	startTime, endTime time.Time,
	granularity string,
	userID, apiKeyID, accountID, groupID, teamID int64,
	model string,
	requestType *int16,
	stream *bool,
	billingType *int8,
	nativeCompactionV2 *bool,
) ([]TrendDataPoint, bool, error) {
	key := mustMarshalDashboardCacheKey(dashboardTrendCacheKey{
		StartTime:          startTime.UTC().Format(time.RFC3339),
		EndTime:            endTime.UTC().Format(time.RFC3339),
		Granularity:        granularity,
		UserID:             userID,
		APIKeyID:           apiKeyID,
		AccountID:          accountID,
		GroupID:            groupID,
		TeamID:             teamID,
		Model:              model,
		RequestType:        requestType,
		Stream:             stream,
		BillingType:        billingType,
		NativeCompactionV2: nativeCompactionV2,
	})
	entry, hit, err := h.queryCaches.dashboardTrendCache.GetOrLoad(key, func() (any, error) {
		return h.GetUsageTrendWithUsageFilters(ctx, startTime, endTime, granularity, UsageLogFilters{
			UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, GroupID: groupID, TeamID: teamID,
			Model: model, RequestType: requestType, Stream: stream, BillingType: billingType,
			NativeCompactionV2: nativeCompactionV2,
		})
	})
	if err != nil {
		return nil, hit, err
	}
	trend, err := snapshotPayloadAs[[]TrendDataPoint](entry.Payload)
	return trend, hit, err
}

func (h *DashboardService) GetModelStatsCached(
	ctx context.Context,
	startTime, endTime time.Time,
	userID, apiKeyID, accountID, groupID, teamID int64,
	modelSource string,
	requestType *int16,
	stream *bool,
	billingType *int8,
	nativeCompactionV2 *bool,
) ([]ModelStat, bool, error) {
	key := mustMarshalDashboardCacheKey(dashboardModelGroupCacheKey{
		StartTime:          startTime.UTC().Format(time.RFC3339),
		EndTime:            endTime.UTC().Format(time.RFC3339),
		UserID:             userID,
		APIKeyID:           apiKeyID,
		AccountID:          accountID,
		GroupID:            groupID,
		TeamID:             teamID,
		ModelSource:        NormalizeModelSource(modelSource),
		RequestType:        requestType,
		Stream:             stream,
		BillingType:        billingType,
		NativeCompactionV2: nativeCompactionV2,
	})
	entry, hit, err := h.queryCaches.dashboardModelStatsCache.GetOrLoad(key, func() (any, error) {
		return h.GetModelStatsWithUsageFiltersBySource(ctx, startTime, endTime, UsageLogFilters{
			UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, GroupID: groupID, TeamID: teamID,
			RequestType: requestType, Stream: stream, BillingType: billingType,
			NativeCompactionV2: nativeCompactionV2,
		}, modelSource)
	})
	if err != nil {
		return nil, hit, err
	}
	stats, err := snapshotPayloadAs[[]ModelStat](entry.Payload)
	return stats, hit, err
}

func (h *DashboardService) GetGroupStatsCached(
	ctx context.Context,
	startTime, endTime time.Time,
	userID, apiKeyID, accountID, groupID, teamID int64,
	requestType *int16,
	stream *bool,
	billingType *int8,
	nativeCompactionV2 *bool,
) ([]GroupStat, bool, error) {
	key := mustMarshalDashboardCacheKey(dashboardModelGroupCacheKey{
		StartTime:          startTime.UTC().Format(time.RFC3339),
		EndTime:            endTime.UTC().Format(time.RFC3339),
		UserID:             userID,
		APIKeyID:           apiKeyID,
		AccountID:          accountID,
		GroupID:            groupID,
		TeamID:             teamID,
		RequestType:        requestType,
		Stream:             stream,
		BillingType:        billingType,
		NativeCompactionV2: nativeCompactionV2,
	})
	entry, hit, err := h.queryCaches.dashboardGroupStatsCache.GetOrLoad(key, func() (any, error) {
		return h.GetGroupStatsWithUsageFilters(ctx, startTime, endTime, UsageLogFilters{
			UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, GroupID: groupID, TeamID: teamID,
			RequestType: requestType, Stream: stream, BillingType: billingType,
			NativeCompactionV2: nativeCompactionV2,
		})
	})
	if err != nil {
		return nil, hit, err
	}
	stats, err := snapshotPayloadAs[[]GroupStat](entry.Payload)
	return stats, hit, err
}

func (h *DashboardService) GetAPIKeyUsageTrendCached(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]APIKeyUsageTrendPoint, bool, error) {
	key := mustMarshalDashboardCacheKey(dashboardEntityTrendCacheKey{
		StartTime:   startTime.UTC().Format(time.RFC3339),
		EndTime:     endTime.UTC().Format(time.RFC3339),
		Granularity: granularity,
		Limit:       limit,
	})
	entry, hit, err := h.queryCaches.dashboardAPIKeysTrendCache.GetOrLoad(key, func() (any, error) {
		return h.GetAPIKeyUsageTrend(ctx, startTime, endTime, granularity, limit)
	})
	if err != nil {
		return nil, hit, err
	}
	trend, err := snapshotPayloadAs[[]APIKeyUsageTrendPoint](entry.Payload)
	return trend, hit, err
}

func (h *DashboardService) GetUserUsageTrendCached(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]UserUsageTrendPoint, bool, error) {
	key := mustMarshalDashboardCacheKey(dashboardEntityTrendCacheKey{
		StartTime:   startTime.UTC().Format(time.RFC3339),
		EndTime:     endTime.UTC().Format(time.RFC3339),
		Granularity: granularity,
		Limit:       limit,
	})
	entry, hit, err := h.queryCaches.dashboardUsersTrendCache.GetOrLoad(key, func() (any, error) {
		return h.GetUserUsageTrend(ctx, startTime, endTime, granularity, limit)
	})
	if err != nil {
		return nil, hit, err
	}
	trend, err := snapshotPayloadAs[[]UserUsageTrendPoint](entry.Payload)
	return trend, hit, err
}

type dashboardQueryCaches struct {
	dashboardTrendCache            *querycache.Cache
	dashboardModelStatsCache       *querycache.Cache
	dashboardGroupStatsCache       *querycache.Cache
	dashboardUsersTrendCache       *querycache.Cache
	dashboardAPIKeysTrendCache     *querycache.Cache
	ranking, batchUsers, batchKeys *querycache.Cache
}

func newDashboardQueryCaches() *dashboardQueryCaches {
	return &dashboardQueryCaches{dashboardTrendCache: querycache.NewCache(30 * time.Second), dashboardModelStatsCache: querycache.NewCache(30 * time.Second), dashboardGroupStatsCache: querycache.NewCache(30 * time.Second), dashboardUsersTrendCache: querycache.NewCache(30 * time.Second), dashboardAPIKeysTrendCache: querycache.NewCache(30 * time.Second), ranking: querycache.NewCache(5 * time.Minute), batchUsers: querycache.NewCache(30 * time.Second), batchKeys: querycache.NewCache(30 * time.Second)}
}
