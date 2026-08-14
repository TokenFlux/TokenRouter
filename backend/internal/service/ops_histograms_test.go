package service

import (
	"context"
	"testing"
	"time"

	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type latencyHistogramRepoStub struct {
	OpsRepository
	capturedBoundaries []int64
}

func (s *latencyHistogramRepoStub) GetLatencyHistogram(_ context.Context, _ *OpsDashboardFilter, boundaries []int64) (*OpsLatencyHistogramResponse, error) {
	s.capturedBoundaries = append([]int64(nil), boundaries...)
	return &OpsLatencyHistogramResponse{}, nil
}

func TestNormalizeOpsLatencyBucketBoundariesMS(t *testing.T) {
	require.Equal(t, []int64{100, 200, 500, 1000, 2000}, DefaultOpsLatencyBucketBoundariesMS())

	valid := []int64{1000, 2000, 5000, 10000, 20000}
	normalized, err := NormalizeOpsLatencyBucketBoundariesMS(valid)
	require.NoError(t, err)
	require.Equal(t, valid, normalized)

	invalid := [][]int64{
		{100, 200},
		{0, 200, 500, 1000, 2000},
		{100, 200, 200, 1000, 2000},
		{100, 200, 500, 1000, 86_400_001},
	}
	for _, boundaries := range invalid {
		_, err := NormalizeOpsLatencyBucketBoundariesMS(boundaries)
		require.Error(t, err, "boundaries=%v", boundaries)
	}
}

func TestOpsServiceGetLatencyHistogram_UsesDefaultBoundaries(t *testing.T) {
	now := time.Now().UTC()
	repo := &latencyHistogramRepoStub{}
	service := &OpsService{opsRepo: repo}

	response, err := service.GetLatencyHistogram(context.Background(), &OpsDashboardFilter{
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	}, nil)
	require.NoError(t, err)
	require.Equal(t, DefaultOpsLatencyBucketBoundariesMS(), repo.capturedBoundaries)
	require.Equal(t, DefaultOpsLatencyBucketBoundariesMS(), response.BucketBoundariesMS)
}

func TestOpsServiceGetLatencyHistogram_RejectsInvalidBoundaries(t *testing.T) {
	now := time.Now().UTC()
	service := &OpsService{opsRepo: &latencyHistogramRepoStub{}}

	_, err := service.GetLatencyHistogram(context.Background(), &OpsDashboardFilter{
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	}, []int64{100, 200, 200, 1000, 2000})
	require.Error(t, err)
	require.Equal(t, 400, infraerrors.Code(err))
	require.Equal(t, "OPS_LATENCY_BUCKET_BOUNDARIES_INVALID", infraerrors.Reason(err))
}
