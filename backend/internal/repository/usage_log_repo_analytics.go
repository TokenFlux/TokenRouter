package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/timezone"
	"github.com/BrandonVee/TokenRouter/internal/pkg/usagestats"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/lib/pq"
	"golang.org/x/sync/errgroup"
)

type usageAnalyticsWindow struct {
	start          time.Time
	end            time.Time
	aggregateStart time.Time
	aggregateEnd   time.Time
	dailyStart     time.Time
	dailyEnd       time.Time
	rawTailStart   time.Time
}

// usageAnalyticsDailySplit 保存日表日期区间以及两侧小时表的精确时间边界。
type usageAnalyticsDailySplit struct {
	hourlyHeadEnd   time.Time
	hourlyTailStart time.Time
	dailyStartDate  string
	dailyEndDate    string
}

// dailySplit 将小时边界与日表日期拆成独立参数，避免 PostgreSQL 把小时边界推断成 date。
func (window usageAnalyticsWindow) dailySplit() usageAnalyticsDailySplit {
	hourlyHeadEnd := window.dailyStart
	hourlyTailStart := window.dailyEnd
	dailyStart := window.dailyStart
	dailyEnd := window.dailyEnd
	if !dailyEnd.After(dailyStart) {
		// 窗口内没有完整 UTC 日时，日表使用空区间，小时表覆盖全部聚合区间。
		hourlyHeadEnd = window.aggregateStart
		hourlyTailStart = window.aggregateStart
		dailyStart = window.dailyEnd
		dailyEnd = window.dailyEnd
	}
	return usageAnalyticsDailySplit{
		hourlyHeadEnd:   hourlyHeadEnd,
		hourlyTailStart: hourlyTailStart,
		dailyStartDate:  dailyStart.Format(time.DateOnly),
		dailyEndDate:    dailyEnd.Format(time.DateOnly),
	}
}

// args 按“小时头部结束、小时尾部开始、日表开始、日表结束”的顺序返回查询参数。
func (split usageAnalyticsDailySplit) args() []any {
	return []any{split.hourlyHeadEnd, split.hourlyTailStart, split.dailyStartDate, split.dailyEndDate}
}

// resolveUsageAnalyticsWindow 仅在聚合覆盖完整时返回可安全组合的聚合与原始边界。
func (r *usageLogRepository) resolveUsageAnalyticsWindow(ctx context.Context, start, end time.Time) (usageAnalyticsWindow, bool, error) {
	window := usageAnalyticsWindow{start: start.UTC(), end: end.UTC()}
	if r == nil || r.preAggregation == nil || !end.After(start) || !r.preAggregation.UsageEnabled(ctx) {
		return window, false, nil
	}
	var state struct {
		watermark time.Time
		coverage  sql.NullTime
	}
	if err := scanSingleRow(ctx, r.sql, `
		SELECT live_watermark, coverage_start
		FROM usage_analytics_aggregation_state
		WHERE id = 1
	`, nil, &state.watermark, &state.coverage); err != nil {
		return window, false, err
	}
	if !state.coverage.Valid || !state.watermark.After(time.Unix(0, 0)) {
		return window, false, nil
	}

	window.aggregateStart = ceilHourUTC(window.start)
	if coverageStart := state.coverage.Time.UTC(); coverageStart.After(window.aggregateStart) {
		// 历史覆盖尚未完成时，仅让覆盖线之前的区间回退原始表。
		window.aggregateStart = coverageStart
	}
	if end.UTC().After(state.watermark.UTC()) {
		// 当前小时聚合到 watermark，尾部只读取 watermark 之后的新记录。
		window.aggregateEnd = ceilHourUTC(state.watermark.UTC())
		window.rawTailStart = state.watermark.UTC()
	} else {
		// 历史任意结束时间只使用完整小时，剩余不足一小时从原始表读取。
		window.aggregateEnd = end.UTC().Truncate(time.Hour)
		window.rawTailStart = window.aggregateEnd
	}
	if window.rawTailStart.Before(window.aggregateStart) {
		window.rawTailStart = window.aggregateStart
	}
	if !window.aggregateEnd.After(window.aggregateStart) {
		return window, false, nil
	}
	if state.watermark.UTC().Before(window.aggregateStart) {
		return window, false, nil
	}
	window.dailyStart = ceilDayUTC(window.aggregateStart)
	window.dailyEnd = window.aggregateEnd.Truncate(24 * time.Hour)
	// 保留“不存在完整 UTC 日”的边界状态，让查询构造阶段退化为完整小时区间。
	return window, true, nil
}

func ceilHourUTC(value time.Time) time.Time {
	value = value.UTC()
	floor := value.Truncate(time.Hour)
	if value.Equal(floor) {
		return floor
	}
	return floor.Add(time.Hour)
}

func ceilDayUTC(value time.Time) time.Time {
	value = value.UTC()
	floor := value.Truncate(24 * time.Hour)
	if value.Equal(floor) {
		return floor
	}
	return floor.Add(24 * time.Hour)
}

func (r *usageLogRepository) getOwnedTeamID(ctx context.Context, userID int64) (int64, error) {
	var teamID sql.NullInt64
	err := scanSingleRow(ctx, r.sql, `
		SELECT (
			SELECT tm.team_id
			FROM team_memberships tm
			WHERE tm.user_id = $1 AND tm.role = 'owner' AND tm.left_at IS NULL
		) AS team_id
	`, []any{userID}, &teamID)
	if err != nil || !teamID.Valid {
		return 0, err
	}
	return teamID.Int64, nil
}

// getUserDashboardStatsFromAnalytics 使用小时聚合和最多两个原始边界计算用户仪表盘。
func (r *usageLogRepository) getUserDashboardStatsFromAnalytics(ctx context.Context, userID int64) (*usagestats.UserDashboardStats, bool, error) {
	if r == nil || r.preAggregation == nil || !r.preAggregation.UsageEnabled(ctx) {
		return nil, false, nil
	}
	var sourceOldest sql.NullTime
	if err := scanSingleRow(ctx, r.sql, `
		SELECT source_oldest_at FROM usage_analytics_aggregation_state WHERE id = 1
	`, nil, &sourceOldest); err != nil {
		return nil, false, err
	}
	if !sourceOldest.Valid {
		return nil, false, nil
	}
	now := time.Now().UTC()
	window, ok, err := r.resolveUsageAnalyticsWindow(ctx, sourceOldest.Time, now)
	if err != nil || !ok {
		return nil, false, err
	}
	teamID, err := r.getOwnedTeamID(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	dailySplit := window.dailySplit()

	stats := &usagestats.UserDashboardStats{}
	var totalDuration, durationCount int64
	var todayStats *UsageStats
	var rpm, tpm int64
	group, groupCtx := errgroup.WithContext(ctx)

	// Key 数量、累计用量、今日用量和实时性能互不依赖，并行读取可缩短仪表盘关键路径。
	group.Go(func() error {
		return scanSingleRow(groupCtx, r.sql, `
			WITH scoped AS (
				SELECT status FROM api_keys WHERE deleted_at IS NULL AND user_id = $1
				UNION ALL
				SELECT status FROM api_keys
				WHERE deleted_at IS NULL AND $2 > 0 AND team_id = $2 AND user_id <> $1
			)
			SELECT COUNT(*), COUNT(*) FILTER (WHERE status = $3) FROM scoped
		`, []any{userID, teamID, service.StatusActive}, &stats.TotalAPIKeys, &stats.ActiveAPIKeys)
	})
	group.Go(func() error {
		return scanSingleRow(groupCtx, r.sql, `
		WITH combined AS (
			SELECT
				total_requests, input_tokens, output_tokens,
				cache_creation_tokens, cache_read_tokens,
				total_cost, actual_cost, total_duration_ms, duration_count
			FROM usage_analytics_daily
			WHERE bucket_date >= $10::date AND bucket_date < $11::date
			  AND user_id = $1
			UNION ALL
			SELECT
				total_requests, input_tokens, output_tokens,
				cache_creation_tokens, cache_read_tokens,
				total_cost, actual_cost, total_duration_ms, duration_count
			FROM usage_analytics_daily
			WHERE bucket_date >= $10::date AND bucket_date < $11::date
			  AND $2 > 0 AND team_id = $2 AND user_id <> $1
			UNION ALL
			SELECT
				total_requests, input_tokens, output_tokens,
				cache_creation_tokens, cache_read_tokens,
				total_cost, actual_cost, total_duration_ms, duration_count
			FROM usage_analytics_hourly
			WHERE ((bucket_start >= $5 AND bucket_start < $8)
			    OR (bucket_start >= $9 AND bucket_start < $6))
			  AND user_id = $1
			UNION ALL
			SELECT
				total_requests, input_tokens, output_tokens,
				cache_creation_tokens, cache_read_tokens,
				total_cost, actual_cost, total_duration_ms, duration_count
			FROM usage_analytics_hourly
			WHERE ((bucket_start >= $5 AND bucket_start < $8)
			    OR (bucket_start >= $9 AND bucket_start < $6))
			  AND $2 > 0 AND team_id = $2 AND user_id <> $1
			UNION ALL
			SELECT
				1, input_tokens, output_tokens,
				cache_creation_tokens, cache_read_tokens,
				total_cost, actual_cost, COALESCE(duration_ms, 0),
				CASE WHEN duration_ms IS NULL THEN 0 ELSE 1 END
			FROM usage_logs
			WHERE user_id = $1
			  AND ((created_at >= $3 AND created_at < $5)
			    OR (created_at >= $7 AND created_at < $4))
			UNION ALL
			SELECT
				1, input_tokens, output_tokens,
				cache_creation_tokens, cache_read_tokens,
				total_cost, actual_cost, COALESCE(duration_ms, 0),
				CASE WHEN duration_ms IS NULL THEN 0 ELSE 1 END
			FROM usage_logs
			WHERE $2 > 0 AND team_id = $2 AND user_id <> $1
			  AND ((created_at >= $3 AND created_at < $5)
			    OR (created_at >= $7 AND created_at < $4))
		)
		SELECT
			COALESCE(SUM(total_requests), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(total_cost), 0),
			COALESCE(SUM(actual_cost), 0),
			COALESCE(SUM(total_duration_ms), 0),
			COALESCE(SUM(duration_count), 0)
		FROM combined
		`, append([]any{
			userID, teamID, window.start, window.end, window.aggregateStart,
			window.aggregateEnd, window.rawTailStart,
		}, dailySplit.args()...),
			&stats.TotalRequests, &stats.TotalInputTokens, &stats.TotalOutputTokens,
			&stats.TotalCacheCreationTokens, &stats.TotalCacheReadTokens,
			&stats.TotalCost, &stats.TotalActualCost, &totalDuration, &durationCount,
		)
	})
	group.Go(func() error {
		// “今日”按业务时区单独切边界，不能用 UTC 日桶的午夜代替本地整日明细。
		todayStart := timezone.Today()
		todayEnd := time.Now().UTC()
		todayFilters := UsageLogFilters{
			UserID: userID, IncludeOwnedTeam: true,
			StartTime: &todayStart, EndTime: &todayEnd,
		}
		var todayOK bool
		var todayErr error
		todayStats, todayOK, todayErr = r.getUsageStatsFromAnalytics(groupCtx, todayFilters)
		if todayErr != nil {
			return todayErr
		}
		if !todayOK {
			return errors.New("今日用量不在预聚合覆盖范围内")
		}
		return nil
	})
	group.Go(func() error {
		var performanceErr error
		rpm, tpm, performanceErr = r.getPerformanceStatsForScope(groupCtx, userID, teamID)
		return performanceErr
	})
	if err := group.Wait(); err != nil {
		return nil, false, err
	}
	stats.TodayRequests = todayStats.TotalRequests
	stats.TodayInputTokens = todayStats.TotalInputTokens
	stats.TodayOutputTokens = todayStats.TotalOutputTokens
	stats.TodayCacheCreationTokens = todayStats.TotalCacheCreationTokens
	stats.TodayCacheReadTokens = todayStats.TotalCacheReadTokens
	stats.TodayCost = todayStats.TotalCost
	stats.TodayActualCost = todayStats.TotalActualCost
	if durationCount > 0 {
		stats.AverageDurationMs = float64(totalDuration) / float64(durationCount)
	}
	stats.TotalTokens = stats.TotalInputTokens + stats.TotalOutputTokens + stats.TotalCacheCreationTokens + stats.TotalCacheReadTokens
	stats.TodayTokens = stats.TodayInputTokens + stats.TodayOutputTokens + stats.TodayCacheCreationTokens + stats.TodayCacheReadTokens
	stats.Rpm = rpm
	stats.Tpm = tpm
	return stats, true, nil
}

func (r *usageLogRepository) getPerformanceStatsForScope(ctx context.Context, userID, teamID int64) (int64, int64, error) {
	var requests, tokens int64
	err := scanSingleRow(ctx, r.sql, `
		WITH scoped AS (
			SELECT input_tokens, output_tokens FROM usage_logs
			WHERE user_id = $1 AND created_at >= $3
			UNION ALL
			SELECT input_tokens, output_tokens FROM usage_logs
			WHERE $2 > 0 AND team_id = $2 AND user_id <> $1 AND created_at >= $3
		)
		SELECT COUNT(*), COALESCE(SUM(input_tokens + output_tokens), 0) FROM scoped
	`, []any{userID, teamID, time.Now().Add(-5 * time.Minute)}, &requests, &tokens)
	return requests / 5, tokens / 5, err
}

func (r *usageLogRepository) getBatchAPIKeyUsageStatsFromAnalytics(ctx context.Context, apiKeyIDs []int64, start, end time.Time) (map[int64]*usagestats.BatchAPIKeyUsageStats, bool, error) {
	window, ok, err := r.resolveUsageAnalyticsWindow(ctx, start, end)
	if err != nil || !ok {
		return nil, false, err
	}
	result := make(map[int64]*usagestats.BatchAPIKeyUsageStats, len(apiKeyIDs))
	for _, id := range apiKeyIDs {
		result[id] = &usagestats.BatchAPIKeyUsageStats{APIKeyID: id}
	}
	dailySplit := window.dailySplit()
	rows, err := r.sql.QueryContext(ctx, `
		WITH combined AS (
			SELECT api_key_id, actual_cost
			FROM usage_analytics_daily
			WHERE api_key_id = ANY($1) AND bucket_date >= $9::date AND bucket_date < $10::date
			UNION ALL
			SELECT api_key_id, actual_cost
			FROM usage_analytics_hourly
			WHERE api_key_id = ANY($1)
			  AND ((bucket_start >= $4 AND bucket_start < $7)
			    OR (bucket_start >= $8 AND bucket_start < $5))
			UNION ALL
			SELECT api_key_id, actual_cost
			FROM usage_logs
			WHERE api_key_id = ANY($1)
			  AND ((created_at >= $2 AND created_at < $4)
			    OR (created_at >= $6 AND created_at < $3))
		)
		SELECT api_key_id, COALESCE(SUM(actual_cost), 0)
		FROM combined
		GROUP BY api_key_id
	`, append([]any{
		pq.Array(apiKeyIDs), window.start, window.end, window.aggregateStart,
		window.aggregateEnd, window.rawTailStart,
	}, dailySplit.args()...)...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var total float64
		if err := rows.Scan(&id, &total); err != nil {
			return nil, false, err
		}
		if stats := result[id]; stats != nil {
			stats.TotalActualCost = total
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	todayStart := timezone.Today()
	todayEnd := time.Now().UTC()
	todayQuery, todayOK, todayErr := r.buildUsageAnalyticsQuery(ctx, UsageLogFilters{}, todayStart, todayEnd, false)
	if todayErr != nil || !todayOK {
		return nil, false, todayErr
	}
	todayQuery.args = append(todayQuery.args, pq.Array(apiKeyIDs))
	apiKeyIDsPosition := len(todayQuery.args)
	todayRows, err := r.sql.QueryContext(ctx, todayQuery.cte+fmt.Sprintf(`
		SELECT api_key_id, COALESCE(SUM(actual_cost), 0)
		FROM combined
		WHERE api_key_id = ANY($%d)
		GROUP BY api_key_id
	`, apiKeyIDsPosition), todayQuery.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = todayRows.Close() }()
	for todayRows.Next() {
		var id int64
		var today float64
		if err := todayRows.Scan(&id, &today); err != nil {
			return nil, false, err
		}
		if stats := result[id]; stats != nil {
			stats.TodayActualCost = today
		}
	}
	if err := todayRows.Err(); err != nil {
		return nil, false, err
	}
	return result, true, nil
}

func (r *usageLogRepository) getBatchUserUsageStatsFromAnalytics(ctx context.Context, userIDs []int64, start, end time.Time) (map[int64]*usagestats.BatchUserUsageStats, bool, error) {
	window, ok, err := r.resolveUsageAnalyticsWindow(ctx, start, end)
	if err != nil || !ok {
		return nil, false, err
	}
	result := make(map[int64]*usagestats.BatchUserUsageStats, len(userIDs))
	for _, id := range userIDs {
		result[id] = &usagestats.BatchUserUsageStats{UserID: id}
	}
	dailySplit := window.dailySplit()
	rows, err := r.sql.QueryContext(ctx, `
		WITH combined AS (
			SELECT user_id, platform, actual_cost
			FROM usage_analytics_daily
			WHERE user_id = ANY($1) AND bucket_date >= $9::date AND bucket_date < $10::date
			UNION ALL
			SELECT user_id, platform, actual_cost
			FROM usage_analytics_hourly
			WHERE user_id = ANY($1)
			  AND ((bucket_start >= $4 AND bucket_start < $7)
			    OR (bucket_start >= $8 AND bucket_start < $5))
			UNION ALL
			SELECT ul.user_id, COALESCE(NULLIF(g.platform, ''), a.platform, ''), ul.actual_cost
			FROM usage_logs ul
			LEFT JOIN groups g ON g.id = ul.group_id
			LEFT JOIN accounts a ON a.id = ul.account_id
			WHERE ul.user_id = ANY($1) AND ul.actual_cost > 0
			  AND ((ul.created_at >= $2 AND ul.created_at < $4)
			    OR (ul.created_at >= $6 AND ul.created_at < $3))
		)
		SELECT user_id, platform, COALESCE(SUM(actual_cost), 0)
		FROM combined
		GROUP BY user_id, platform
	`, append([]any{
		pq.Array(userIDs), window.start, window.end, window.aggregateStart,
		window.aggregateEnd, window.rawTailStart,
	}, dailySplit.args()...)...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var platform string
		var total float64
		if err := rows.Scan(&id, &platform, &total); err != nil {
			return nil, false, err
		}
		stats := result[id]
		if stats == nil {
			continue
		}
		stats.TotalActualCost += total
		if platform != "" && total != 0 {
			stats.ByPlatform = append(stats.ByPlatform, usagestats.PlatformUsage{
				Platform: platform, TotalActualCost: total,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	todayStart := timezone.Today()
	todayEnd := time.Now().UTC()
	todayQuery, todayOK, todayErr := r.buildUsageAnalyticsQuery(ctx, UsageLogFilters{}, todayStart, todayEnd, false)
	if todayErr != nil || !todayOK {
		return nil, false, todayErr
	}
	todayQuery.args = append(todayQuery.args, pq.Array(userIDs))
	userIDsPosition := len(todayQuery.args)
	todayRows, err := r.sql.QueryContext(ctx, todayQuery.cte+fmt.Sprintf(`
		SELECT user_id, platform, COALESCE(SUM(actual_cost), 0)
		FROM combined
		WHERE user_id = ANY($%d) AND actual_cost > 0
		GROUP BY user_id, platform
	`, userIDsPosition), todayQuery.args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = todayRows.Close() }()
	for todayRows.Next() {
		var id int64
		var platform string
		var today float64
		if err := todayRows.Scan(&id, &platform, &today); err != nil {
			return nil, false, err
		}
		stats := result[id]
		if stats == nil {
			continue
		}
		stats.TodayActualCost += today
		updated := false
		for index := range stats.ByPlatform {
			if stats.ByPlatform[index].Platform == platform {
				stats.ByPlatform[index].TodayActualCost = today
				updated = true
				break
			}
		}
		if !updated && platform != "" {
			stats.ByPlatform = append(stats.ByPlatform, usagestats.PlatformUsage{Platform: platform, TodayActualCost: today})
		}
	}
	if err := todayRows.Err(); err != nil {
		return nil, false, err
	}
	return result, true, nil
}
