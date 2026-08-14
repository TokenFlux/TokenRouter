package repository

import (
	"database/sql"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAggregateHourlyRowsWeightsTTFTBySampleCount(t *testing.T) {
	rows := []opsHourlyMetricsRow{
		{
			bucketStart:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			successCount:    100,
			ttftSampleCount: 1,
			durationP50:     sql.NullInt64{Int64: 100, Valid: true},
			durationP90:     sql.NullInt64{Int64: 200, Valid: true},
			durationAvg:     sql.NullFloat64{Float64: 150, Valid: true},
			durationP95:     sql.NullInt64{Int64: 220, Valid: true},
			durationP99:     sql.NullInt64{Int64: 250, Valid: true},
			durationMax:     sql.NullInt64{Int64: 300, Valid: true},
			ttftP50:         sql.NullInt64{Int64: 1000, Valid: true},
			ttftP90:         sql.NullInt64{Int64: 1200, Valid: true},
			ttftAvg:         sql.NullFloat64{Float64: 1100, Valid: true},
			ttftP95:         sql.NullInt64{Int64: 1300, Valid: true},
			ttftP99:         sql.NullInt64{Int64: 1400, Valid: true},
			ttftMax:         sql.NullInt64{Int64: 1500, Valid: true},
		},
		{
			bucketStart:     time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC),
			successCount:    1,
			ttftSampleCount: 99,
			durationP50:     sql.NullInt64{Int64: 1000, Valid: true},
			durationP90:     sql.NullInt64{Int64: 2000, Valid: true},
			durationAvg:     sql.NullFloat64{Float64: 1500, Valid: true},
			durationP95:     sql.NullInt64{Int64: 2200, Valid: true},
			durationP99:     sql.NullInt64{Int64: 2500, Valid: true},
			durationMax:     sql.NullInt64{Int64: 3000, Valid: true},
			ttftP50:         sql.NullInt64{Int64: 10, Valid: true},
			ttftP90:         sql.NullInt64{Int64: 20, Valid: true},
			ttftAvg:         sql.NullFloat64{Float64: 15, Valid: true},
			ttftP95:         sql.NullInt64{Int64: 25, Valid: true},
			ttftP99:         sql.NullInt64{Int64: 30, Valid: true},
			ttftMax:         sql.NullInt64{Int64: 35, Valid: true},
		},
	}

	got := aggregateHourlyRows(rows)

	require.Equal(t, int64(101), got.successCount)
	require.Equal(t, int64(100), got.ttftSampleCount)

	// 请求耗时仍按 success_count 加权。
	require.NotNil(t, got.duration.P50)
	require.Equal(t, 109, *got.duration.P50)

	// 首 token 延迟按 ttft_sample_count 加权，不能被第一桶的大量非流式成功请求稀释。
	require.NotNil(t, got.ttft.P50)
	require.Equal(t, 20, *got.ttft.P50)
	require.NotNil(t, got.ttft.P90)
	require.Equal(t, 32, *got.ttft.P90)
	require.NotNil(t, got.ttft.Avg)
	require.Equal(t, 26, *got.ttft.Avg)
	require.NotNil(t, got.ttft.P95)
	require.Equal(t, 1300, *got.ttft.P95)
}

func TestCombineApproxPercentilesUsesProvidedWeightsForTTFT(t *testing.T) {
	got := combineApproxPercentiles([]opsPercentileSegment{
		{
			weight: 1,
			p: service.OpsPercentiles{
				P50: intValuePtr(1000),
				P90: intValuePtr(1200),
				Avg: intValuePtr(1100),
				P95: intValuePtr(1300),
			},
		},
		{
			weight: 99,
			p: service.OpsPercentiles{
				P50: intValuePtr(10),
				P90: intValuePtr(20),
				Avg: intValuePtr(15),
				P95: intValuePtr(25),
			},
		},
	})

	require.NotNil(t, got.P50)
	require.Equal(t, 20, *got.P50)
	require.NotNil(t, got.P90)
	require.Equal(t, 32, *got.P90)
	require.NotNil(t, got.Avg)
	require.Equal(t, 26, *got.Avg)
	require.NotNil(t, got.P95)
	require.Equal(t, 1300, *got.P95)
}

func intValuePtr(v int) *int {
	return &v
}
