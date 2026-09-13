// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	fmt "fmt"
	errgroup "golang.org/x/sync/errgroup"
	sync "sync"
	time "time"
)

// LocalUsageStats 只读取账号展示所需五个累计值；原 SQL 查询仍归 S08 用量适配。
type LocalUsageStats interface {
	GetAccountWindowStats(context.Context, int64, time.Time) (*WindowStats, error)
	GetAccountTodayStats(context.Context, int64) (*WindowStats, error)
}
type LocalUsageStatsBatch interface {
	GetAccountWindowStatsBatch(context.Context, []int64, time.Time) (map[int64]*WindowStats, error)
}
type LocalUsageStatisticsOptions struct {
	Now   func() time.Time
	Today func() time.Time
	Log   func(string, ...any)
}

// LocalUsageStatistics 复用唯一窗口缓存，只拥有懒查询、并发回退和展示投影。
type LocalUsageStatistics struct {
	usageLogRepo LocalUsageStats
	cache        *OAuthUsageCache
	options      LocalUsageStatisticsOptions
}

func NewLocalUsageStatistics(reader LocalUsageStats, cache *OAuthUsageCache, options LocalUsageStatisticsOptions) *LocalUsageStatistics {
	return &LocalUsageStatistics{reader, cache, options}
}
func normalizedLocalWindowStats(value *WindowStats) *WindowStats {
	if value == nil {
		return &WindowStats{}
	}
	return clonePointer(value)
}

// addWindowStats 为 usage 数据添加窗口期统计
// 使用独立缓存（1 分钟），与 API 缓存分离
func (s *LocalUsageStatistics) AddWindowStats(ctx context.Context, account *Record, usage *UsageInfo) {
	// 修复：即使 FiveHour 为 nil，也要尝试获取统计数据
	// 因为 SevenDay/SevenDaySonnet/SevenDayFable 可能需要
	if usage.FiveHour == nil && usage.SevenDay == nil && usage.SevenDaySonnet == nil && usage.SevenDayFable == nil {
		return
	}

	// 检查窗口统计缓存（1 分钟）
	var windowStats *WindowStats
	if cached, ok := s.cache.LoadWindow(account.ID); ok {
		if cache, ok := cached.(*OAuthWindowStatsCache); ok && s.options.Now().Sub(cache.Timestamp) < OAuthUsageWindowStatsCacheTTL {
			windowStats = cache.Stats
		}
	}

	// 如果没有缓存，从数据库查询
	if windowStats == nil {
		// 使用统一的窗口开始时间计算逻辑（考虑窗口过期情况）
		startTime := (&RuntimeConfig{Extra: account.Extra, Concurrency: account.Concurrency, SessionWindowStart: account.SessionWindowStart, SessionWindowEnd: account.SessionWindowEnd}).GetCurrentWindowStartTime(s.options.Now())

		stats, err := s.usageLogRepo.GetAccountWindowStats(ctx, account.ID, startTime)
		if err != nil {
			s.options.Log("Failed to get window stats for account %d: %v", account.ID, err)
			return
		}

		windowStats = &WindowStats{
			Requests:     stats.Requests,
			Tokens:       stats.Tokens,
			Cost:         stats.Cost,
			StandardCost: stats.StandardCost,
			UserCost:     stats.UserCost,
		}

		// 缓存窗口统计（1 分钟）
		s.cache.StoreWindow(account.ID, &OAuthWindowStatsCache{
			Stats:     windowStats,
			Timestamp: s.options.Now(),
		})
	}

	// 为 FiveHour 添加 WindowStats（5h 窗口统计）
	if usage.FiveHour != nil {
		usage.FiveHour.WindowStats = windowStats
	}
}

// GetTodayStats 获取账号今日统计
func (s *LocalUsageStatistics) GetTodayStats(ctx context.Context, accountID int64) (*WindowStats, error) {
	stats, err := s.usageLogRepo.GetAccountTodayStats(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("get today stats failed: %w", err)
	}

	return &WindowStats{
		Requests:     stats.Requests,
		Tokens:       stats.Tokens,
		Cost:         stats.Cost,
		StandardCost: stats.StandardCost,
		UserCost:     stats.UserCost,
	}, nil
}

// GetTodayStatsBatch 批量获取账号今日统计，优先走批量 SQL，失败时回退单账号查询。
func (s *LocalUsageStatistics) GetTodayStatsBatch(ctx context.Context, accountIDs []int64) (map[int64]*WindowStats, error) {
	uniqueIDs := make([]int64, 0, len(accountIDs))
	seen := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		if _, exists := seen[accountID]; exists {
			continue
		}
		seen[accountID] = struct{}{}
		uniqueIDs = append(uniqueIDs, accountID)
	}

	result := make(map[int64]*WindowStats, len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		return result, nil
	}

	startTime := s.options.Today()
	if batchReader, ok := s.usageLogRepo.(LocalUsageStatsBatch); ok {
		statsByAccount, err := batchReader.GetAccountWindowStatsBatch(ctx, uniqueIDs, startTime)
		if err == nil {
			for _, accountID := range uniqueIDs {
				result[accountID] = normalizedLocalWindowStats(statsByAccount[accountID])
			}
			return result, nil
		}
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)

	for _, accountID := range uniqueIDs {
		id := accountID
		g.Go(func() error {
			stats, err := s.usageLogRepo.GetAccountWindowStats(gctx, id, startTime)
			if err != nil {
				return nil
			}
			mu.Lock()
			result[id] = normalizedLocalWindowStats(stats)
			mu.Unlock()
			return nil
		})
	}

	_ = g.Wait()

	for _, accountID := range uniqueIDs {
		if _, ok := result[accountID]; !ok {
			result[accountID] = &WindowStats{}
		}
	}
	return result, nil
}
