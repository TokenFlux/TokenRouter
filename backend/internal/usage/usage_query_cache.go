package usage

import (
	"context"
	"time"
)

// 与 dashboard 查询缓存同款:30s TTL 进程内缓存,仅服务 /admin/usage/stats 读路径。

type usageStatsCacheKeyData struct {
	StartTime          string `json:"start_time"`
	EndTime            string `json:"end_time"`
	UserID             int64  `json:"user_id"`
	APIKeyID           int64  `json:"api_key_id"`
	AccountID          int64  `json:"account_id"`
	GroupID            int64  `json:"group_id"`
	TeamID             int64  `json:"team_id"`
	Model              string `json:"model"`
	BillingMode        string `json:"billing_mode"`
	RequestType        *int16 `json:"request_type"`
	Stream             *bool  `json:"stream"`
	BillingType        *int8  `json:"billing_type"`
	NativeCompactionV2 *bool  `json:"native_compaction_v2"`
}

func usageStatsCacheKey(filters UsageLogFilters) string {
	start := ""
	if filters.StartTime != nil {
		start = filters.StartTime.UTC().Format(time.RFC3339)
	}
	end := ""
	if filters.EndTime != nil {
		end = filters.EndTime.UTC().Format(time.RFC3339)
	}
	return mustMarshalDashboardCacheKey(usageStatsCacheKeyData{
		StartTime:          start,
		EndTime:            end,
		UserID:             filters.UserID,
		APIKeyID:           filters.APIKeyID,
		AccountID:          filters.AccountID,
		GroupID:            filters.GroupID,
		TeamID:             filters.TeamID,
		Model:              filters.Model,
		BillingMode:        filters.BillingMode,
		RequestType:        filters.RequestType,
		Stream:             filters.Stream,
		BillingType:        filters.BillingType,
		NativeCompactionV2: filters.NativeCompactionV2,
	})
}

// getStatsCached 命中则返回缓存,未命中则回源 usageService 并写缓存。
func (h *UsageService) GetStatsCached(ctx context.Context, filters UsageLogFilters) (*UsageStats, bool, error) {
	key := usageStatsCacheKey(filters)
	entry, hit, err := h.statsQueryCache.GetOrLoad(key, func() (any, error) {
		return h.GetStatsWithFilters(ctx, filters)
	})
	if err != nil {
		return nil, hit, err
	}
	stats, err := snapshotPayloadAs[*UsageStats](entry.Payload)
	return stats, hit, err
}
