// 这些查询原先采用 Get/Set；迁移不引入新的 singleflight 或查询时机。
package usage

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

type RankingReport struct {
	Data       *UserSpendingRankingResponse
	Start, End time.Time
}

func (s *DashboardService) GetUserSpendingRankingCached(ctx context.Context, start, end time.Time, limit int) (RankingReport, bool, error) {
	key := mustMarshalDashboardCacheKey(struct {
		Start string `json:"start"`
		End   string `json:"end"`
		Limit int    `json:"limit"`
	}{start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), limit})
	if e, ok := s.queryCaches.ranking.Get(key); ok {
		v, err := snapshotPayloadAs[RankingReport](e.Payload)
		return v, true, err
	}
	data, err := s.GetUserSpendingRanking(ctx, start, end, limit)
	if err != nil {
		return RankingReport{}, false, err
	}
	v := RankingReport{Data: data, Start: start, End: end}
	s.queryCaches.ranking.Set(key, v)
	return v, false, nil
}
func (s *DashboardService) GetBatchUsersUsageCached(ctx context.Context, ids []int64) (map[int64]*BatchUserUsageStats, bool, error) {
	key := mustMarshalDashboardCacheKey(struct {
		V       int     `json:"v"`
		Day     string  `json:"day"`
		UserIDs []int64 `json:"user_ids"`
	}{2, timezone.Today().Format("2006-01-02"), ids})
	if e, ok := s.queryCaches.batchUsers.Get(key); ok {
		v, err := snapshotPayloadAs[map[int64]*BatchUserUsageStats](e.Payload)
		return v, true, err
	}
	v, err := s.GetBatchUserUsageStats(ctx, ids, time.Time{}, time.Time{})
	if err != nil {
		return nil, false, err
	}
	s.queryCaches.batchUsers.Set(key, v)
	return v, false, nil
}
func (s *DashboardService) GetBatchKeysUsageCached(ctx context.Context, ids []int64) (map[int64]*BatchAPIKeyUsageStats, bool, error) {
	key := mustMarshalDashboardCacheKey(struct {
		APIKeyIDs []int64 `json:"api_key_ids"`
	}{ids})
	if e, ok := s.queryCaches.batchKeys.Get(key); ok {
		v, err := snapshotPayloadAs[map[int64]*BatchAPIKeyUsageStats](e.Payload)
		return v, true, err
	}
	v, err := s.GetBatchAPIKeyUsageStats(ctx, ids, time.Time{}, time.Time{})
	if err != nil {
		return nil, false, err
	}
	s.queryCaches.batchKeys.Set(key, v)
	return v, false, nil
}
