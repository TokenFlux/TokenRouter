package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/pkg/timezone"
	"github.com/BrandonVee/TokenRouter/internal/pkg/usagestats"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// TestResolveUsageAnalyticsWindowUsesCoveredMiddle 验证未完成回填时只让未覆盖头部读取原始表。
func TestResolveUsageAnalyticsWindowUsesCoveredMiddle(t *testing.T) {
	db, mock := newSQLMock(t)
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{sql: db, preAggregation: settings}
	start := time.Date(2026, 8, 1, 10, 15, 0, 0, time.UTC)
	end := time.Date(2026, 8, 4, 4, 30, 0, 0, time.UTC)
	coverage := time.Date(2026, 8, 2, 3, 0, 0, 0, time.UTC)
	watermark := time.Date(2026, 8, 4, 4, 25, 0, 0, time.UTC)
	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(watermark, coverage))

	window, ok, err := repo.resolveUsageAnalyticsWindow(context.Background(), start, end)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, start, window.start)
	require.Equal(t, coverage, window.aggregateStart)
	require.Equal(t, time.Date(2026, 8, 4, 5, 0, 0, 0, time.UTC), window.aggregateEnd)
	require.Equal(t, watermark, window.rawTailStart)
	require.Equal(t, time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), window.dailyStart)
	require.Equal(t, time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC), window.dailyEnd)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestBuildUsageAnalyticsQueryUsesHalfOpenRanges 验证组合查询的首尾原始区间互不重叠。
func TestBuildUsageAnalyticsQueryUsesHalfOpenRanges(t *testing.T) {
	db, mock := newSQLMock(t)
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{sql: db, preAggregation: settings}
	start := time.Date(2026, 8, 1, 10, 15, 0, 0, time.UTC)
	end := time.Date(2026, 8, 3, 11, 45, 0, 0, time.UTC)
	coverage := time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)
	watermark := time.Date(2026, 8, 3, 11, 30, 0, 0, time.UTC)
	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(watermark, coverage))

	query, ok, err := repo.buildUsageAnalyticsQuery(context.Background(), UsageLogFilters{}, start, end, true)
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, query.cte, "ul.created_at >= $1 AND ul.created_at < $3")
	require.Contains(t, query.cte, "ul.created_at >= $5 AND ul.created_at < $2")
	require.Contains(t, query.cte, "bucket_date >= $8::date AND bucket_date < $9::date")
	require.Contains(t, query.cte, "bucket_start >= $3 AND bucket_start < $6")
	require.Contains(t, query.cte, "bucket_start >= $7 AND bucket_start < $4")
	require.Contains(t, query.cte, "COALESCE(NULLIF(TRIM(ul.model), ''), NULLIF(TRIM(ul.requested_model), ''), '')")
	require.Len(t, query.args, 9)
	require.Equal(t, time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), query.args[5])
	require.Equal(t, time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), query.args[6])
	require.Equal(t, "2026-08-02", query.args[7])
	require.Equal(t, "2026-08-03", query.args[8])
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestBuildUsageAnalyticsQueryBeforeUTCMidnightKeepsTodayBoundary 验证东八区今日在 UTC 零点前不会包含昨日小时桶。
func TestBuildUsageAnalyticsQueryBeforeUTCMidnightKeepsTodayBoundary(t *testing.T) {
	db, mock := newSQLMock(t)
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{sql: db, preAggregation: settings}
	// 北京时间 2026-08-06 00:00 至 03:30 对应同一 UTC 日内的不完整窗口。
	start := time.Date(2026, 8, 5, 16, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 5, 19, 30, 0, 0, time.UTC)
	watermark := time.Date(2026, 8, 5, 19, 25, 0, 0, time.UTC)
	coverage := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(watermark, coverage))

	query, ok, err := repo.buildUsageAnalyticsQuery(context.Background(), UsageLogFilters{}, start, end, true)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, query.args, 9)
	// 日表区间为空，小时表的分割点固定在今日开始，不能回退到前一个 UTC 午夜。
	require.Equal(t, start, query.args[5])
	require.Equal(t, start, query.args[6])
	require.Equal(t, "2026-08-05", query.args[7])
	require.Equal(t, "2026-08-05", query.args[8])
	require.Contains(t, query.cte, "bucket_date >= $8::date AND bucket_date < $9::date")
	require.Contains(t, query.cte, "bucket_start >= $3 AND bucket_start < $6")
	require.Contains(t, query.cte, "bucket_start >= $7 AND bucket_start < $4")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUsageAnalyticsFallbackWarningIsRateLimited 验证同一操作一分钟内只产生一次聚合故障告警。
func TestUsageAnalyticsFallbackWarningIsRateLimited(t *testing.T) {
	operation := "test_rate_limit_analytics_fallback"
	started := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	usageAnalyticsFallbackLogState.Lock()
	delete(usageAnalyticsFallbackLogState.lastByOperation, operation)
	usageAnalyticsFallbackLogState.Unlock()
	t.Cleanup(func() {
		usageAnalyticsFallbackLogState.Lock()
		delete(usageAnalyticsFallbackLogState.lastByOperation, operation)
		usageAnalyticsFallbackLogState.Unlock()
	})

	require.True(t, shouldLogUsageAnalyticsFallback(operation, started))
	require.False(t, shouldLogUsageAnalyticsFallback(operation, started.Add(59*time.Second)))
	require.True(t, shouldLogUsageAnalyticsFallback(operation, started.Add(time.Minute)))
}

// TestBuildUsageAnalyticsQueryWithoutDailyUsesContiguousArgs 验证小时查询不携带未引用的日边界参数。
func TestBuildUsageAnalyticsQueryWithoutDailyUsesContiguousArgs(t *testing.T) {
	db, mock := newSQLMock(t)
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{sql: db, preAggregation: settings}
	start := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(4 * time.Hour)
	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(end, start))

	query, ok, err := repo.buildUsageAnalyticsQuery(context.Background(), UsageLogFilters{}, start, end, false)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, query.args, 5)
	require.NotContains(t, query.cte, "$6")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestBuildUsageAnalyticsQueryNumbersOwnedTeamFiltersContinuously 验证团队范围与筛选参数从基础边界后连续编号。
func TestBuildUsageAnalyticsQueryNumbersOwnedTeamFiltersContinuously(t *testing.T) {
	db, mock := newSQLMock(t)
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{sql: db, preAggregation: settings}
	start := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(4 * time.Hour)
	requestType := int16(service.RequestTypeStream)
	stream := true
	billingType := int8(1)
	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(end, start))
	mock.ExpectQuery("(?s)SELECT \\(.*SELECT tm.team_id.*team_memberships").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(int64(9)))

	query, ok, err := repo.buildUsageAnalyticsQuery(context.Background(), UsageLogFilters{
		UserID: 7, IncludeOwnedTeam: true, APIKeyID: 11, GroupID: 12, TeamID: 13,
		Model: "requested-model", ModelFilterSource: usagestats.ModelSourceRequested,
		RequestType: &requestType, Stream: &stream, BillingType: &billingType, BillingMode: "token",
	}, start, end, false)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, query.args, 15)
	require.Contains(t, query.cte, "user_id = $6")
	require.Contains(t, query.cte, "team_id = $7 AND user_id <> $6")
	require.Contains(t, query.where, "api_key_id = $8")
	require.Contains(t, query.where, "group_id = $9")
	require.Contains(t, query.where, "team_id = $10")
	require.Contains(t, query.where, "requested_model = $11")
	require.Contains(t, query.where, "request_type = $12")
	require.Contains(t, query.where, "stream = $13")
	require.Contains(t, query.where, "billing_type = $14")
	require.Contains(t, query.where, "billing_mode = $15")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestBuildUsageAnalyticsQueryUsesIndexableOwnedTeamRawSource 验证未覆盖原始区间不会恢复成标量子查询 OR。
func TestBuildUsageAnalyticsQueryUsesIndexableOwnedTeamRawSource(t *testing.T) {
	db, mock := newSQLMock(t)
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{sql: db, preAggregation: settings}
	start := time.Date(2026, 8, 1, 10, 15, 0, 0, time.UTC)
	end := time.Date(2026, 8, 2, 11, 45, 0, 0, time.UTC)
	coverage := time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)
	watermark := time.Date(2026, 8, 2, 11, 30, 0, 0, time.UTC)
	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(watermark, coverage))
	mock.ExpectQuery("(?s)SELECT \\(.*SELECT tm.team_id.*team_memberships").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(int64(9)))

	query, ok, err := repo.buildUsageAnalyticsQuery(context.Background(), UsageLogFilters{
		UserID: 7, IncludeOwnedTeam: true,
	}, start, end, true)
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, query.cte, "SELECT * FROM usage_logs WHERE user_id = $10")
	require.Contains(t, query.cte, "SELECT * FROM usage_logs WHERE team_id = $11 AND user_id <> $10")
	require.Contains(t, query.where, "user_id = $10 OR (team_id = $11 AND user_id <> $10)")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestBuildUsageAnalyticsQueryRejectsUnsupportedFilters 验证聚合表缺少的维度会透明回退原始查询。
func TestBuildUsageAnalyticsQueryRejectsUnsupportedFilters(t *testing.T) {
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{preAggregation: settings}
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	tests := []struct {
		name    string
		filters UsageLogFilters
	}{
		{name: "账号维度", filters: UsageLogFilters{AccountID: 1}},
		{name: "请求编号", filters: UsageLogFilters{RequestID: "request-1"}},
		{name: "默认模型语义", filters: UsageLogFilters{Model: "mapped-model"}},
		{name: "上游模型", filters: UsageLogFilters{Model: "upstream-model", ModelFilterSource: usagestats.ModelSourceUpstream}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, ok, err := repo.buildUsageAnalyticsQuery(context.Background(), test.filters, start, end, true)
			require.NoError(t, err)
			require.False(t, ok)
		})
	}
}

// TestGetModelStatsFromAnalyticsRejectsUnsupportedGrouping 验证未聚合的模型维度不会误用请求模型结果。
func TestGetModelStatsFromAnalyticsRejectsUnsupportedGrouping(t *testing.T) {
	repo := &usageLogRepository{}
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	for _, source := range []string{usagestats.ModelSourceUpstream, usagestats.ModelSourceMapping} {
		_, ok, err := repo.getModelStatsFromAnalytics(context.Background(), start, end, UsageLogFilters{
			ModelFilterSource: source,
		})
		require.NoError(t, err)
		require.False(t, ok)
	}
}

// TestGetUserSpendingRankingFromAnalyticsReturnsCurrentUsername 验证预聚合排行会关联当前用户身份字段。
func TestGetUserSpendingRankingFromAnalyticsReturnsCurrentUsername(t *testing.T) {
	db, mock := newSQLMock(t)
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{sql: db, preAggregation: settings}
	start := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(4 * time.Hour)

	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(end, start))
	mock.ExpectQuery("(?s)user_spend AS \\(.*COALESCE\\(u.username, ''\\)").
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "email", "username", "actual_cost", "requests", "tokens",
			"total_actual_cost", "total_requests", "total_tokens",
		}).AddRow(int64(7), "rank@example.com", "rank-user", 12.5, int64(9), int64(900), 12.5, int64(9), int64(900)))

	got, ok, err := repo.getUserSpendingRankingFromAnalytics(context.Background(), start, end, 12)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []usagestats.UserSpendingRankingItem{
		{UserID: 7, Email: "rank@example.com", Username: "rank-user", ActualCost: 12.5, Requests: 9, Tokens: 900},
	}, got.Ranking)
	require.Equal(t, 12.5, got.TotalActualCost)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetUsageRankingFromAnalyticsOrdersByActualCost 验证公开排行在预聚合路径中仍按实际消费金额排序。
func TestGetUsageRankingFromAnalyticsOrdersByActualCost(t *testing.T) {
	db, mock := newSQLMock(t)
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{sql: db, preAggregation: settings}
	start := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(4 * time.Hour)

	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(end, start))
	mock.ExpectQuery("(?s)ROW_NUMBER\\(\\) OVER \\(ORDER BY actual_cost DESC, total_tokens DESC, requests DESC, user_id ASC\\).*ORDER BY actual_cost DESC, total_tokens DESC, requests DESC, user_id ASC").
		WillReturnRows(sqlmock.NewRows([]string{
			"rank", "user_id", "email", "username", "avatar_url", "requests",
			"input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
			"total_tokens", "actual_cost", "total_requests", "ranking_total_tokens", "total_actual_cost",
		}).
			AddRow(1, int64(7), "spend@example.com", "spender", "", int64(4), int64(100), int64(100), int64(0), int64(0), int64(200), 12.5, int64(13), int64(5200), 16.75).
			AddRow(2, int64(8), "tokens@example.com", "tokens", "", int64(9), int64(2500), int64(2500), int64(0), int64(0), int64(5000), 4.25, int64(13), int64(5200), 16.75))

	got, ok, err := repo.getUsageRankingFromAnalytics(context.Background(), start, end, 20)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, got.Ranking, 2)
	require.Equal(t, int64(7), got.Ranking[0].UserID)
	require.Equal(t, 12.5, got.Ranking[0].ActualCost)
	require.Equal(t, int64(5000), got.Ranking[1].TotalTokens)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetUsageTrendFromAnalyticsUsesNamedTimezoneForDST 验证趋势分桶把命名时区交给 PostgreSQL 处理夏令时。
func TestGetUsageTrendFromAnalyticsUsesNamedTimezoneForDST(t *testing.T) {
	previousTimezone := timezone.Name()
	require.NoError(t, timezone.Init("America/New_York"))
	t.Cleanup(func() { _ = timezone.Init(previousTimezone) })

	db, mock := newSQLMock(t)
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{sql: db, preAggregation: settings}
	// 该 UTC 窗口跨越纽约 2026 年秋季夏令时回拨点。
	start := time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(end, start))
	mock.ExpectQuery("(?s)TO_CHAR\\(occurred_at AT TIME ZONE \\$6").
		WithArgs(start, end, start, end, end, "America/New_York").
		WillReturnRows(sqlmock.NewRows([]string{
			"date", "requests", "input_tokens", "output_tokens", "cache_creation_tokens",
			"cache_read_tokens", "total_tokens", "cost", "actual_cost",
		}))

	_, ok, err := repo.getUsageTrendFromAnalytics(context.Background(), start, end, "hour", UsageLogFilters{})
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestBatchAPIKeyUsageAnalyticsUsesDynamicTodayIDPosition 验证批量 Key 今日查询紧接 5 个边界参数编号。
func TestBatchAPIKeyUsageAnalyticsUsesDynamicTodayIDPosition(t *testing.T) {
	db, mock := newSQLMock(t)
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{sql: db, preAggregation: settings}
	now := time.Now().UTC()
	start := now.Add(-4 * time.Hour).Truncate(time.Hour)
	end := now.Add(time.Hour).Truncate(time.Hour)
	todayStart := timezone.Today().UTC()
	coverage := todayStart.Add(-time.Hour).Truncate(time.Hour)
	// 水位线紧邻当前时间，确保任意服务器时区及 UTC 零点首小时都有可用聚合窗口。
	watermark := now.Add(-time.Nanosecond)
	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(watermark, coverage))

	// 首次查询使用日表形态，共 10 个参数（批量 ID 加 9 个边界）。
	mock.ExpectQuery("(?s)SELECT api_key_id, COALESCE\\(SUM\\(actual_cost\\), 0\\).*FROM combined").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "actual_cost"}))
	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(watermark, coverage))
	mock.ExpectQuery("(?s)WHERE api_key_id = ANY\\(\\$6\\)").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "actual_cost"}))

	_, ok, err := repo.getBatchAPIKeyUsageStatsFromAnalytics(context.Background(), []int64{3, 5}, start, end)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestBatchUserUsageAnalyticsUsesDynamicTodayIDPosition 验证批量用户今日查询紧接 5 个边界参数编号。
func TestBatchUserUsageAnalyticsUsesDynamicTodayIDPosition(t *testing.T) {
	db, mock := newSQLMock(t)
	settings := service.NewPreAggregationSettingsService(nil, &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
	})
	repo := &usageLogRepository{sql: db, preAggregation: settings}
	now := time.Now().UTC()
	start := now.Add(-4 * time.Hour).Truncate(time.Hour)
	end := now.Add(time.Hour).Truncate(time.Hour)
	todayStart := timezone.Today().UTC()
	coverage := todayStart.Add(-time.Hour).Truncate(time.Hour)
	// 水位线紧邻当前时间，确保任意服务器时区及 UTC 零点首小时都有可用聚合窗口。
	watermark := now.Add(-time.Nanosecond)
	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(watermark, coverage))
	mock.ExpectQuery("(?s)SELECT user_id, platform, COALESCE\\(SUM\\(actual_cost\\), 0\\).*FROM combined").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "platform", "actual_cost"}))
	mock.ExpectQuery("(?s)SELECT live_watermark, coverage_start.*usage_analytics_aggregation_state").
		WillReturnRows(sqlmock.NewRows([]string{"live_watermark", "coverage_start"}).AddRow(watermark, coverage))
	mock.ExpectQuery("(?s)WHERE user_id = ANY\\(\\$6\\) AND actual_cost > 0").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "platform", "actual_cost"}))

	_, ok, err := repo.getBatchUserUsageStatsFromAnalytics(context.Background(), []int64{7, 9}, start, end)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}
