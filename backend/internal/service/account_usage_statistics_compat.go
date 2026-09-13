// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	log "log"
	time "time"
)

// legacyLocalUsageStats 仅调用原查询并投影五个统计字段，不重新分页或计算金额。
type legacyLocalUsageStats struct{ source UsageLogRepository }

func (r legacyLocalUsageStats) GetAccountWindowStats(ctx context.Context, id int64, start time.Time) (*accountcore.WindowStats, error) {
	v, err := r.source.GetAccountWindowStats(ctx, id, start)
	if v == nil {
		return nil, err
	}
	return windowStatsFromAccountStats(v), err
}
func (r legacyLocalUsageStats) GetAccountTodayStats(ctx context.Context, id int64) (*accountcore.WindowStats, error) {
	v, err := r.source.GetAccountTodayStats(ctx, id)
	if v == nil {
		return nil, err
	}
	return windowStatsFromAccountStats(v), err
}

type legacyLocalUsageStatsBatch struct {
	legacyLocalUsageStats
	batch accountWindowStatsBatchReader
}

func (r legacyLocalUsageStatsBatch) GetAccountWindowStatsBatch(ctx context.Context, ids []int64, start time.Time) (map[int64]*accountcore.WindowStats, error) {
	values, err := r.batch.GetAccountWindowStatsBatch(ctx, ids, start)
	if values == nil {
		return nil, err
	}
	out := make(map[int64]*accountcore.WindowStats, len(values))
	for id, v := range values {
		out[id] = windowStatsFromAccountStats(v)
	}
	return out, err
}
func (s *AccountUsageService) localUsageStatistics() *accountcore.LocalUsageStatistics {
	if s.statistics != nil {
		return s.statistics
	}
	var reader accountcore.LocalUsageStats
	if s.usageLogRepo != nil {
		reader = legacyLocalUsageStats{s.usageLogRepo}
	}
	if batch, ok := s.usageLogRepo.(accountWindowStatsBatchReader); ok {
		reader = legacyLocalUsageStatsBatch{legacyLocalUsageStats{s.usageLogRepo}, batch}
	}
	return accountcore.NewLocalUsageStatistics(reader, s.cache, accountcore.LocalUsageStatisticsOptions{Now: time.Now, Today: timezone.Today, Log: log.Printf})
}

// LocalStatistics 返回 app 绑定的同一个统计组合，旧独立测试可使用只读投影回退。
func (s *AccountUsageService) LocalStatistics() *accountcore.LocalUsageStatistics {
	return s.localUsageStatistics()
}
