package postgres

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestOpsHourlyMetricsCoverageRequiresEveryBucket 验证部分聚合窗口不会被误当成完整覆盖。
func TestOpsHourlyMetricsCoverageRequiresEveryBucket(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &Store{db: db}
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	query := regexp.QuoteMeta(`
		SELECT COUNT(*)
		FROM ops_metrics_hourly
		WHERE bucket_start >= $1 AND bucket_start < $2
		  AND platform IS NULL AND group_id IS NULL
	`)

	mock.ExpectQuery(query).WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(23)))
	covered, err := repo.hasCompleteHourlyMetricsCoverage(context.Background(), start, end)
	require.NoError(t, err)
	require.False(t, covered)

	mock.ExpectQuery(query).WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(24)))
	covered, err = repo.hasCompleteHourlyMetricsCoverage(context.Background(), start, end)
	require.NoError(t, err)
	require.True(t, covered)
	require.NoError(t, mock.ExpectationsWereMet())
}
