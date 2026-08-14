package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/BrandonVee/TokenRouter/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// expectUsageAnalyticsRangeRebuild 声明多维小时和日聚合重建的 SQL 预期。
func expectUsageAnalyticsRangeRebuild(mock sqlmock.Sqlmock, hourStart, hourEnd, scanEnd time.Time) {
	dayStart := time.Date(hourStart.Year(), hourStart.Month(), hourStart.Day(), 0, 0, 0, 0, time.UTC)
	lastHour := hourEnd.Add(-time.Nanosecond)
	dayEnd := time.Date(lastHour.Year(), lastHour.Month(), lastHour.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM usage_analytics_hourly").
		WithArgs(hourStart, hourEnd).
		WillReturnResult(sqlmock.NewResult(0, 1))
	modelExpr := regexp.QuoteMeta("COALESCE(NULLIF(TRIM(ul.model), ''), NULLIF(TRIM(ul.requested_model), ''), '')")
	mock.ExpectExec("(?s)INSERT INTO usage_analytics_hourly.*"+modelExpr).
		WithArgs(hourStart, scanEnd).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM usage_analytics_daily").
		WithArgs(dayStart, dayEnd).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO usage_analytics_daily").
		WithArgs(dayStart, dayEnd).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

// TestAggregateUsageAnalyticsRangeStopsAtLiveWatermark 验证实时聚合不会读取水位之后的原始记录。
func TestAggregateUsageAnalyticsRangeStopsAtLiveWatermark(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 4, 9, 15, 0, 0, time.UTC)
	end := time.Date(2026, 8, 4, 10, 30, 0, 0, time.UTC)

	expectUsageAnalyticsRangeRebuild(mock, start.Truncate(time.Hour), end.Truncate(time.Hour).Add(time.Hour), end)

	require.NoError(t, repo.AggregateUsageAnalyticsRange(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAggregateUsageAnalyticsHourlyRangeSkipsDailyRebuild 验证定时小时刷新不会重复改写日表。
func TestAggregateUsageAnalyticsHourlyRangeSkipsDailyRebuild(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 4, 9, 15, 0, 0, time.UTC)
	end := time.Date(2026, 8, 4, 10, 30, 0, 0, time.UTC)
	hourStart := start.Truncate(time.Hour)
	hourEnd := end.Truncate(time.Hour).Add(time.Hour)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM usage_analytics_hourly").
		WithArgs(hourStart, hourEnd).
		WillReturnResult(sqlmock.NewResult(0, 1))
	modelExpr := regexp.QuoteMeta("COALESCE(NULLIF(TRIM(ul.model), ''), NULLIF(TRIM(ul.requested_model), ''), '')")
	mock.ExpectExec("(?s)INSERT INTO usage_analytics_hourly.*"+modelExpr).
		WithArgs(hourStart, end).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.AggregateUsageAnalyticsHourlyRange(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRebuildUsageAnalyticsDailyRangeUsesUTCDateBounds 验证日表接口按 UTC 日期边界独立重建。
func TestRebuildUsageAnalyticsDailyRangeUsesUTCDateBounds(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 3, 23, 30, 0, 0, time.UTC)
	end := time.Date(2026, 8, 4, 0, 30, 0, 0, time.UTC)
	dayStart := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	dayEnd := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM usage_analytics_daily").
		WithArgs(dayStart, dayEnd).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO usage_analytics_daily").
		WithArgs(dayStart, dayEnd).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.RebuildUsageAnalyticsDailyRange(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRecomputeUsageAnalyticsRangeRebuildsPartialEndBucket 验证删除范围落在小时中间时会保留末桶范围外记录。
func TestRecomputeUsageAnalyticsRangeRebuildsPartialEndBucket(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 4, 9, 15, 0, 0, time.UTC)
	end := time.Date(2026, 8, 4, 10, 30, 0, 0, time.UTC)
	hourEnd := end.Truncate(time.Hour).Add(time.Hour)

	expectUsageAnalyticsRangeRebuild(mock, start.Truncate(time.Hour), hourEnd, hourEnd)

	require.NoError(t, repo.RecomputeUsageAnalyticsRange(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRecomputeUsageAnalyticsRangeIncludesExactEndBucket 验证包含式删除命中整点时会重建以该整点开始的桶。
func TestRecomputeUsageAnalyticsRangeIncludesExactEndBucket(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	start := time.Date(2026, 8, 4, 9, 15, 0, 0, time.UTC)
	end := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	hourEnd := end.Add(time.Hour)

	expectUsageAnalyticsRangeRebuild(mock, start.Truncate(time.Hour), hourEnd, hourEnd)

	require.NoError(t, repo.RecomputeUsageAnalyticsRange(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUsageAnalyticsAggregationStatePersistsManualCursor 验证手动范围字段可随任务状态完整读写。
func TestUsageAnalyticsAggregationStatePersistsManualCursor(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	live := time.Date(2026, 8, 4, 10, 30, 0, 0, time.UTC)
	coverage := live.Add(-48 * time.Hour)
	automaticCursor := live.Add(-72 * time.Hour)
	oldest := live.Add(-30 * 24 * time.Hour)
	manualStart := live.Add(-7 * 24 * time.Hour)
	manualCursor := live.Add(-3 * time.Hour)
	lastRun := live.Add(-time.Minute)
	lastSuccess := live.Add(-2 * time.Minute)

	mock.ExpectQuery("SELECT live_watermark, coverage_start, backfill_cursor, source_oldest_at").
		WillReturnRows(sqlmock.NewRows([]string{
			"live_watermark", "coverage_start", "backfill_cursor", "source_oldest_at",
			"manual_backfill_start", "manual_backfill_cursor", "phase", "last_run_at",
			"last_success_at", "last_error_at", "last_error", "last_duration_ms",
		}).AddRow(
			live, coverage, automaticCursor, oldest, manualStart, manualCursor,
			"backfill", lastRun, lastSuccess, nil, "", int64(25),
		))

	state, err := repo.GetUsageAnalyticsAggregationState(context.Background())
	require.NoError(t, err)
	require.Equal(t, manualStart, *state.ManualBackfillStart)
	require.Equal(t, manualCursor, *state.ManualBackfillCursor)

	mock.ExpectExec("INSERT INTO usage_analytics_aggregation_state").
		WithArgs(
			live, coverage, automaticCursor, oldest, manualStart, manualCursor,
			"backfill", lastRun, lastSuccess, nil, "", int64(25),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.SaveUsageAnalyticsAggregationState(context.Background(), state))
	require.NoError(t, mock.ExpectationsWereMet())
}

// expectDashboardAggregateStatements 声明仪表盘聚合写入的 SQL 预期。
func expectDashboardAggregateStatements(
	mock sqlmock.Sqlmock,
	hourStart, hourEnd time.Time,
	dailyUserStart, dailyUserEnd time.Time,
	dayStart, dayEnd time.Time,
) {
	tzName := timezone.Name()
	mock.ExpectExec("INSERT INTO usage_dashboard_hourly_users").
		WithArgs(hourStart, hourEnd, tzName).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO usage_dashboard_daily_users").
		WithArgs(dailyUserStart, dailyUserEnd, tzName).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO usage_dashboard_hourly \\(").
		WithArgs(hourStart, hourEnd, tzName).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO usage_dashboard_daily \\(").
		WithArgs(dayStart, dayEnd, dayStart, dayEnd, tzName).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

// TestDashboardAggregateRangeKeepsExactEndExclusive 验证实时聚合的整点结束值仍是半开区间。
func TestDashboardAggregateRangeKeepsExactEndExclusive(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	loc := timezone.Location()
	start := time.Date(2026, 8, 4, 23, 15, 0, 0, loc)
	end := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	hourStart := start.Truncate(time.Hour)
	dayStart := truncateToDay(start)

	mock.ExpectBegin()
	expectDashboardAggregateStatements(mock, hourStart, end, hourStart, end, dayStart, end)
	mock.ExpectCommit()

	require.NoError(t, repo.AggregateRange(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDashboardRecomputeRangeIncludesExactEndBuckets 验证删除重算同时包含整点和零点开始的新桶。
func TestDashboardRecomputeRangeIncludesExactEndBuckets(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	loc := timezone.Location()
	start := time.Date(2026, 8, 4, 23, 15, 0, 0, loc)
	end := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	hourStart := start.Truncate(time.Hour)
	hourEnd := end.Add(time.Hour)
	dayStart := truncateToDay(start)
	dayEnd := end.AddDate(0, 0, 1)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM usage_dashboard_hourly WHERE").
		WithArgs(hourStart, hourEnd).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM usage_dashboard_hourly_users WHERE").
		WithArgs(hourStart, hourEnd).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM usage_dashboard_daily WHERE").
		WithArgs(dayStart, dayEnd).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM usage_dashboard_daily_users WHERE").
		WithArgs(dayStart, dayEnd).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectDashboardAggregateStatements(mock, hourStart, hourEnd, dayStart, dayEnd, dayStart, dayEnd)
	mock.ExpectCommit()

	require.NoError(t, repo.RecomputeRange(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}
