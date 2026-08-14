package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLatencyHistogramBucketLabels(t *testing.T) {
	labels := latencyHistogramBucketLabels([]int64{1000, 2000, 5000, 10000, 20000})
	require.Equal(t, []string{
		"0-1000ms",
		"1000-2000ms",
		"2000-5000ms",
		"5000-10000ms",
		"10000-20000ms",
		"20000ms+",
	}, labels)
}

func TestOpsRepositoryGetLatencyHistogram_CustomBoundariesAndZeroBuckets(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &opsRepository{db: db}
	start := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	boundaries := []int64{1000, 2000, 5000, 10000, 20000}

	mock.ExpectQuery(`(?s)WHEN ul\.duration_ms < \$3 THEN 0.*WHEN ul\.duration_ms < \$7 THEN 4.*GROUP BY 1.*ORDER BY 1 ASC`).
		WithArgs(start, end, int64(1000), int64(2000), int64(5000), int64(10000), int64(20000)).
		WillReturnRows(sqlmock.NewRows([]string{"bucket_index", "count"}).
			AddRow(0, int64(11)).
			AddRow(3, int64(4)).
			AddRow(5, int64(2)))

	response, err := repo.GetLatencyHistogram(context.Background(), &service.OpsDashboardFilter{
		StartTime: start,
		EndTime:   end,
	}, boundaries)
	require.NoError(t, err)
	require.Equal(t, boundaries, response.BucketBoundariesMS)
	require.Equal(t, int64(17), response.TotalRequests)
	require.Len(t, response.Buckets, 6)
	require.Equal(t, int64(11), response.Buckets[0].Count)
	require.Equal(t, int64(0), response.Buckets[1].Count)
	require.Equal(t, int64(4), response.Buckets[3].Count)
	require.Equal(t, int64(2), response.Buckets[5].Count)
	require.NoError(t, mock.ExpectationsWereMet())
}
